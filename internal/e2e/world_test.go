package e2e

// The shared fixture of the permission, crawl and cross-user tests: one library
// with staff, patrons, books with covers, loans, reviews and a logo, and the
// means to look at its database from outside (to prove a refusal changed
// nothing) and to read the route table from the source (so a new route cannot
// slip past the matrix).

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"jamieslibrary/internal/testutil"
)

// ---- the route table, read from the source ----------------------------------

// route is one registered route and what it declares about who may call it.
type route struct {
	Method, Path string // Method is "" for a pattern with none (any method)
	Kind         string // require, signedin, public, publiccatalog, mixed
	Perm         string // the permission ID, for require
	Multipart    bool
	WhileMust    bool
}

func (r route) String() string { return strings.TrimSpace(r.Method + " " + r.Path) }

// permNames maps the Go name of each permission constant to its ID, read from internal/perm.
func permNames(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "perm", "perm.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(s, ".") {
							out[n.Name] = s
						}
					}
				}
			}
		}
	}
	return out
}

// routeTable reads every Handle and HandleFunc call in the httpapi package (not
// its tests) and what each declares. A declaration it cannot read fails the
// test: a route may not be left out of the matrix because it was written oddly.
func routeTable(t *testing.T) []route {
	t.Helper()
	names := permNames(t)
	dir := filepath.Join("..", "httpapi")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []route
	seen := map[string]bool{}
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(nd ast.Node) bool {
			call, ok := nd.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pattern, _ := strconv.Unquote(lit.Value)
			rt := route{}
			if i := strings.Index(pattern, " "); i > 0 {
				rt.Method, rt.Path = pattern[:i], strings.TrimSpace(pattern[i+1:])
			} else {
				rt.Path = pattern
			}
			expr := call.Args[1]
			for {
				c, ok := expr.(*ast.CallExpr)
				if !ok {
					t.Errorf("%s: route %s: cannot read its access declaration", fset.Position(call.Pos()), pattern)
					return true
				}
				switch fn := c.Fun.(type) {
				case *ast.SelectorExpr:
					switch fn.Sel.Name {
					case "Multipart":
						rt.Multipart = true
					case "AllowWhileMustChange":
						rt.WhileMust = true
					default:
						t.Errorf("%s: route %s: unknown access modifier %s", fset.Position(call.Pos()), pattern, fn.Sel.Name)
						return true
					}
					expr = fn.X
					continue
				case *ast.Ident:
					switch fn.Name {
					case "Require":
						rt.Kind = "require"
						arg, ok := c.Args[0].(*ast.SelectorExpr)
						if !ok || names[arg.Sel.Name] == "" {
							t.Errorf("%s: route %s: cannot read the permission", fset.Position(call.Pos()), pattern)
							return true
						}
						rt.Perm = names[arg.Sel.Name]
					case "SignedIn":
						rt.Kind = "signedin"
					case "Public":
						rt.Kind = "public"
					case "PublicCatalog":
						rt.Kind = "publiccatalog"
					case "Mixed":
						rt.Kind = "mixed"
					default:
						t.Errorf("%s: route %s: unknown access %s", fset.Position(call.Pos()), pattern, fn.Name)
						return true
					}
				}
				break
			}
			if !seen[rt.String()] {
				seen[rt.String()] = true
				out = append(out, rt)
			}
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if len(out) < 60 {
		t.Fatalf("read only %d routes from internal/httpapi: the table reader is broken", len(out))
	}
	return out
}

// ---- looking at the database from outside -----------------------------------

// snapshot is every row of every table (but the sessions, which every request
// touches), as sorted text, so two can be compared.
type snapshot map[string]string

func takeSnapshot(t *testing.T, dir string) snapshot {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "library.db")+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	snap := snapshot{}
	for _, tb := range tables {
		if tb == "sessions" {
			continue
		}
		r, err := db.Query(`SELECT * FROM "` + tb + `"`)
		if err != nil {
			t.Fatalf("reading %s: %v", tb, err)
		}
		cols, _ := r.Columns()
		var lines []string
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			var sb strings.Builder
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					v = fmt.Sprintf("%x", b)
				}
				fmt.Fprintf(&sb, "%s=%v|", cols[i], v)
			}
			lines = append(lines, sb.String())
		}
		r.Close()
		sort.Strings(lines)
		snap[tb] = strings.Join(lines, "\n")
	}
	return snap
}

// diff names the tables that differ.
func (a snapshot) diff(b snapshot) []string {
	var out []string
	for k, v := range a {
		if b[k] != v {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ---- requests with headers and bodies of any kind ---------------------------

type rawReq struct {
	Method, Path string
	Header       map[string]string
	Body         []byte
	ContentType  string
}

func (w *who) raw(r rawReq) reply {
	w.t.Helper()
	var rd io.Reader
	if r.Body != nil {
		rd = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequest(r.Method, w.s.URL+r.Path, rd)
	if err != nil {
		w.t.Fatal(err)
	}
	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	resp, err := w.c.Do(req)
	if err != nil {
		w.t.Fatalf("%s %s: %v", r.Method, r.Path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	out := reply{Status: resp.StatusCode, Header: resp.Header, Raw: string(raw)}
	_ = json.Unmarshal(raw, &out.JSON)
	return out
}

// pngBytes is a small valid PNG.
func pngBytes(c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for x := 0; x < 40; x++ {
		for y := 0; y < 40; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// formFile is a multipart body with one file part.
func formFile(field, name string, data []byte) ([]byte, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, name))
	h.Set("Content-Type", "image/png")
	p, _ := mw.CreatePart(h)
	_, _ = p.Write(data)
	_ = mw.Close()
	return b.Bytes(), mw.FormDataContentType()
}

func idOf(r reply, key string) int64 {
	f, _ := r.JSON[key].(float64)
	return int64(f)
}

// ---- the library -------------------------------------------------------------

// member is a signed-in account.
type member struct {
	ID   int64
	Name string
	W    *who
	Card string
}

// world is one library with a bit of everything in it.
type world struct {
	t    *testing.T
	s    *testutil.Server
	root *who
	ol   *testutil.FakeOL

	locID      int64
	coverBook  int64  // an ISBN book with a cover
	coverURL   string // its /media/ URL
	logoURL    string
	logoPath   string
	bookID     int64 // a hand-catalogued book
	pa, pb     *member
	loanA      int64 // pa's open loan
	loanB      int64
	copyA      string // codes of the copies on loan to pa and pb
	copyB      string
	reviewA    int64
	reviewText string // text of pa's review, a canary
	seq        int
}

const reviewCanary = "ZEBRA-CRIMSON-REVIEW-7731"

// newWorld installs a library and fills it: two patrons with a loan and one
// review each, books with covers, a logo and a location. selfCheckout turns the
// self-checkout setting on.
func newWorld(t *testing.T) *world {
	t.Helper()
	ol := testutil.FakeOpenLibrary(t)
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: ol})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatalf("no setup code in the console output:\n%s", s.Stdout())
	}
	root := newWho(t, s)
	root.want(200, "POST", "/api/setup/admin", map[string]string{
		"code": code, "username": "root", "display_name": "Root", "password": adminPassword})
	w := &world{t: t, s: s, root: root, ol: ol}

	w.locID = idOf(root.want(201, "POST", "/api/locations", map[string]string{"name": "Fixture shelf"}), "id")

	// A book with a cover, from the provider.
	found := root.want(200, "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780140328721"})
	b := root.want(201, "POST", "/api/books", map[string]any{
		"title": "Fantastic Mr Fox", "authors": []string{"Roald Dahl"}, "isbn": "9780140328721",
		"description": "Three farmers, one fox.", "cover_token": found.JSON["cover_token"]})
	w.coverBook = idOf(b, "id")
	w.coverURL, _ = b.JSON["cover_url"].(string)
	if w.coverURL == "" {
		t.Fatalf("the book has no cover: %s", b.Raw)
	}
	root.want(201, "POST", fmt.Sprintf("/api/books/%d/copies", w.coverBook), map[string]any{"location_id": w.locID, "condition": "good"})

	// A logo.
	body, ct := formFile("logo", "logo.png", pngBytes(color.RGBA{200, 80, 40, 255}))
	r := root.raw(rawReq{Method: "PUT", Path: "/api/admin/branding/logo", Body: body, ContentType: ct})
	if r.Status != 200 {
		t.Fatalf("logo upload: %d %s", r.Status, r.Raw)
	}
	lib := root.want(200, "GET", "/api/library", nil)
	w.logoURL, _ = lib.JSON["logo_url"].(string)
	if w.logoURL == "" {
		t.Fatalf("no logo URL after the upload: %s", lib.Raw)
	}

	// Hand-catalogued books with copies, patrons, loans and reviews.
	w.bookID = w.newBook()
	w.pa = w.patron("alice-a")
	w.pb = w.patron("bob-b")
	root.want(200, "PUT", "/api/admin/settings", map[string]any{"self_checkout": true})
	w.copyA, w.loanA = w.lend(w.pa)
	w.copyB, w.loanB = w.lend(w.pb)
	w.reviewText = reviewCanary
	rv := w.pa.W.want(200, "PUT", fmt.Sprintf("/api/books/%d/review", w.bookID), map[string]any{"rating": 5, "text": w.reviewText})
	w.reviewA = idOf(rv, "id")
	if w.reviewA == 0 {
		// the body may wrap it; find it in the list
		l := root.want(200, "GET", fmt.Sprintf("/api/books/%d/reviews", w.bookID), nil)
		if rs, _ := l.JSON["reviews"].([]any); len(rs) > 0 {
			w.reviewA = int64(rs[0].(map[string]any)["id"].(float64))
		}
	}
	if w.reviewA == 0 {
		t.Fatalf("no review id found: %s", rv.Raw)
	}
	w.pb.W.want(200, "PUT", fmt.Sprintf("/api/books/%d/review", w.bookID), map[string]any{"rating": 2, "text": "second opinion from bob"})
	return w
}

func (w *world) next() int { w.seq++; return w.seq }

// newBook makes a hand-catalogued book (no ISBN, so every copy has a barcode).
func (w *world) newBook() int64 {
	w.t.Helper()
	n := w.next()
	r := w.root.want(201, "POST", "/api/books", map[string]any{
		"title": fmt.Sprintf("Fixture book %d", n), "authors": []string{"Ann Author"}, "description": "A book for tests."})
	return idOf(r, "id")
}

// newCopy adds a copy to book and returns its id and barcode.
func (w *world) newCopy(book int64) (int64, string) {
	w.t.Helper()
	r := w.root.want(201, "POST", fmt.Sprintf("/api/books/%d/copies", book), map[string]any{"location_id": w.locID, "condition": "good"})
	code, _ := r.JSON["barcode"].(string)
	if code == "" {
		w.t.Fatalf("a copy with no barcode: %s", r.Raw)
	}
	return idOf(r, "id"), code
}

// patron registers a new patron with an invitation.
func (w *world) patron(name string) *member {
	return w.account("patron", name)
}

func (w *world) account(role, name string) *member {
	w.t.Helper()
	id, n := w.root.member(role, name)
	m := &member{ID: id, Name: name, W: n}
	if c := n.do("GET", "/api/me/card", nil); c.Status == 200 {
		m.Card, _ = c.JSON["number"].(string)
	}
	return m
}

// lend checks a fresh copy out to m at the desk and returns its code and the loan's id.
func (w *world) lend(m *member) (string, int64) {
	w.t.Helper()
	_, code := w.newCopy(w.newBook())
	if m.Card == "" {
		w.t.Fatalf("%s has no card", m.Name)
	}
	r := w.root.want(200, "POST", "/api/desk/checkout", map[string]string{"patron": m.Card, "code": code})
	id := idOf(r, "id")
	if id == 0 {
		w.t.Fatalf("the checkout gave no loan id: %s", r.Raw)
	}
	return code, id
}

// signedIn is a new client signed in as username.
func (w *world) signedIn(username string) *who {
	w.t.Helper()
	n := newWho(w.t, w.s)
	n.mustSignIn(username, adminPassword)
	return n
}

func (w *world) snap() snapshot { return takeSnapshot(w.t, w.s.DataDir) }

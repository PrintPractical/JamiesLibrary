package e2e

// DEPLOY-T3: the data directory is the whole installation. A library is
// populated, the server stopped, only the data directory copied to an empty
// place, and the same build started on the copy.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// populate fills a fresh library through the API: locations, books with and
// without covers, copies, a librarian and two patrons, loans (one returned),
// a logo, changed settings, and the copy-ID mode switched to the library's own.
func populate(t *testing.T, root *pc, dir string) {
	t.Helper()
	root.must(200, "PUT", "/api/admin/branding", map[string]string{"name": "Restore Test Library", "scheme": "hearth", "accent": ""})
	root.must(200, "PUT", "/api/admin/settings", map[string]any{"loan_period_days": 21, "max_loans": 4})
	var logo bytes.Buffer
	mw := multipart.NewWriter(&logo)
	fw, _ := mw.CreateFormFile("logo", "logo.png")
	_, _ = fw.Write(pngCover(99))
	_ = mw.Close()
	if st, raw, err := root.raw("PUT", "/api/admin/branding/logo", mw.FormDataContentType(), logo.Bytes()); err != nil || st != 200 {
		t.Fatalf("logo: %d %s %v", st, raw, err)
	}
	loc := root.must(201, "POST", "/api/locations", map[string]string{"name": "Reading room"})
	locID := int64(loc["id"].(float64))

	isbns := []string{"9780140328721", "9780261103573", "9780064400558", "9780306406157", "", ""}
	for i := 0; i < 6; i++ {
		fields := [][2]string{{"title", fmt.Sprintf("Book number %d — ünïcode ☕", i)}, {"authors", fmt.Sprintf("Author %d", i)}, {"authors", "Second Author"}, {"description", strings.Repeat("A long description. ", 20)}}
		if isbns[i] != "" {
			fields = append(fields, [2]string{"isbn", isbns[i]})
		}
		var cover []byte
		if i != 2 && i != 5 { // two books have no cover
			cover = pngCover(i + 1)
		}
		st, b, err := root.bookForm("POST", "/api/books", fields, cover)
		if err != nil || st != 201 {
			t.Fatalf("book %d: %d %v %v", i, st, b, err)
		}
		id := int64(b["id"].(float64))
		for n := 0; n < 1+i%3; n++ {
			body := map[string]any{"condition": "good"}
			if n%2 == 0 {
				body["location_id"] = locID
			} else {
				body["new_location"] = fmt.Sprintf("Shelf %d-%d", i, n)
			}
			root.must(201, "POST", fmt.Sprintf("/api/books/%d/copies", id), body)
		}
	}
	root.must(200, "PUT", "/api/admin/copy-id-mode", map[string]string{"mode": "own"})
	cards := map[string]string{}
	for _, name := range []string{"lib", "pat1", "pat2"} {
		role := "patron"
		if name == "lib" {
			role = "librarian"
		}
		inv := root.must(201, "POST", "/api/admin/invitations", map[string]string{"role": role})
		c := newPC(t, root.base)
		c.must(201, "POST", "/api/register", map[string]string{"code": inv["code"].(string), "username": name, "display_name": "User " + name, "password": adminPassword})
		cards[name] = c.must(200, "GET", "/api/me/card", nil)["number"].(string)
	}
	// check three copies out to patrons, return one
	codes := ownCodes(t, dir)
	if len(codes) < 5 {
		t.Fatalf("expected library codes on the copies, got %v", codes)
	}
	root.must(200, "POST", "/api/desk/checkout", map[string]string{"patron": cards["pat1"], "code": codes[0]})
	root.must(200, "POST", "/api/desk/checkout", map[string]string{"patron": cards["pat1"], "code": codes[1]})
	root.must(200, "POST", "/api/desk/checkout", map[string]string{"patron": cards["pat2"], "code": codes[2]})
	root.must(200, "POST", "/api/desk/checkin", map[string]string{"code": codes[1]})
}

func bookIDs(t *testing.T, c *pc) []int64 {
	t.Helper()
	var ids []int64
	for page := 1; ; page++ {
		r := c.must(200, "GET", fmt.Sprintf("/api/books?page=%d", page), nil)
		books, _ := r["books"].([]any)
		for _, b := range books {
			ids = append(ids, int64(b.(map[string]any)["id"].(float64)))
		}
		if len(books) == 0 || len(ids) >= int(r["total"].(float64)) {
			return ids
		}
	}
}

// ownCodes lists the library's own barcodes of live copies, read from the data directory.
func ownCodes(t testing.TB, dir string) []string {
	t.Helper()
	rows, err := openRO(t, dir).Query(`SELECT i.code FROM identifiers i JOIN copies c ON c.id = i.copy_id
		WHERE i.kind = 'copy' AND c.removed_at IS NULL ORDER BY c.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

// export is everything the library shows an administrator, as canonical text
// keyed by what it is, plus the hash of every cover and the logo.
func export(t *testing.T, c *pc) map[string]string { return exportWith(t, c, true) }

// exportWith is export; without detail it leaves out the book pages, which an
// older release (the current code on an older schema) can't draw when they
// need a table the newer schema added, and reads the book list and covers instead.
func exportWith(t *testing.T, c *pc, detail bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	get := func(key, path string) []byte {
		st, raw, err := c.raw("GET", path, "", nil)
		if err != nil || st != 200 {
			t.Fatalf("GET %s: %d %v %s", path, st, err, raw)
		}
		out[key] = string(raw)
		return raw
	}
	lib := c.must(200, "GET", "/api/library", nil)
	get("library", "/api/library")
	get("settings", "/api/admin/settings")
	get("locations", "/api/locations")
	get("activity", "/api/admin/activity?per_page=100")
	if logo, _ := lib["logo_url"].(string); logo != "" {
		_, raw, _ := c.raw("GET", logo, "", nil)
		out["logo-sha"] = sum(raw)
	}
	users := c.must(200, "GET", "/api/admin/users", nil)
	for _, u := range users["users"].([]any) {
		id := int64(u.(map[string]any)["id"].(float64))
		get(fmt.Sprintf("patron-%d", id), fmt.Sprintf("/api/patrons/%d", id))
		get(fmt.Sprintf("patron-loans-%d", id), fmt.Sprintf("/api/patron-loans/%d", id))
	}
	get("users", "/api/admin/users")
	get("overdue", "/api/desk/overdue")
	for _, id := range bookIDs(t, c) {
		var d map[string]any
		if detail {
			get(fmt.Sprintf("book-%d", id), fmt.Sprintf("/api/books/%d", id))
			d = c.must(200, "GET", fmt.Sprintf("/api/books/%d", id), nil)
		} else {
			d = nil
			for page := 1; d == nil; page++ {
				r := c.must(200, "GET", fmt.Sprintf("/api/books?page=%d", page), nil)
				for _, b := range r["books"].([]any) {
					if int64(b.(map[string]any)["id"].(float64)) == id {
						d = b.(map[string]any)
					}
				}
				if page > 100 {
					t.Fatalf("book %d not in the list", id)
				}
			}
			b, _ := json.Marshal(d)
			out[fmt.Sprintf("listed-%d", id)] = string(b)
		}
		if cu, _ := d["cover_url"].(string); cu != "" {
			u, _ := url.Parse(cu)
			_, img, _ := c.raw("GET", u.Path, "", nil)
			out[fmt.Sprintf("cover-sha-%d", id)] = sum(img)
		}
	}
	return out
}

func compareExports(t *testing.T, want, got map[string]string) {
	t.Helper()
	var keys []string
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got[k] != want[k] {
			t.Errorf("%s differs after the restore\nbefore: %.400s\nafter:  %.400s", k, want[k], got[k])
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s appeared after the restore", k)
		}
	}
}

// imageHashes is the sha-256 of every file in the images directory, by name.
func imageHashes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	ents, err := os.ReadDir(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, "images", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = sum(b)
	}
	return out
}

func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != dir {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// DEPLOY-T3 "Move to another machine": the library is stopped, only its data
// directory is copied to an empty place, and the same build, started there with
// nothing else (an empty home and working directory, no other variables), shows
// the same library: every book, copy, loan, account and setting, and the same bytes for every cover and the logo.
func TestRestoreFromOnlyTheDataDirectory(t *testing.T) {
	src := filepath.Join(t.TempDir(), "data")
	p := start(t, src, nil)
	root := firstAdmin(t, p)
	populate(t, root, src)
	before := export(t, root)
	if len(before) < 20 {
		t.Fatalf("the export is too small to prove anything: %d parts", len(before))
	}
	p.stop()
	srcDB := dumpDB(t, filepath.Join(src, "library.db"), "sessions")
	srcImages := imageHashes(t, src)
	if len(srcImages) < 5 {
		t.Fatalf("expected the covers and the logo on disk, found %v", srcImages)
	}

	dst := filepath.Join(t.TempDir(), "elsewhere", "data")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	copyDir(t, src, dst)
	if err := os.RemoveAll(src); err != nil { // the original is gone: nothing may be read from it
		t.Fatal(err)
	}

	q := launch(t, dst, nil)
	if err := q.waitReady(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.Stdout.String(), "SETUP CODE") {
		t.Errorf("the restored library offered first-run setup although it has an administrator:\n%s", q.Stdout.String())
	}
	c := newPC(t, q.Base)
	c.signIn("root", adminPassword)
	after := export(t, c)
	compareExports(t, before, after)
	// it still works: a new loan and a return
	codes := ownCodes(t, dst)
	q.stop()

	if d := diffLines(srcDB, dumpDB(t, filepath.Join(dst, "library.db"), "sessions")); d != "" {
		t.Errorf("the database changed by only being restored and read:\n%s", d)
	}
	if got := imageHashes(t, dst); fmt.Sprint(got) != fmt.Sprint(srcImages) {
		t.Errorf("image files differ after the restore:\nbefore %v\nafter  %v", srcImages, got)
	}
	if len(codes) == 0 {
		t.Error("no copy codes seen")
	}
	// and the restored library takes writes: another start, a loan and its return
	w := start(t, dst, nil)
	wc := newPC(t, w.Base)
	wc.signIn("root", adminPassword)
	var card string
	if err := openRO(t, dst).QueryRow(`SELECT code FROM identifiers WHERE kind='card' LIMIT 1`).Scan(&card); err != nil {
		t.Fatal(err)
	}
	wc.must(200, "POST", "/api/desk/checkout", map[string]string{"patron": card, "code": codes[len(codes)-1]})
	wc.must(200, "POST", "/api/desk/checkin", map[string]string{"code": codes[len(codes)-1]})
	w.stop()
	// the process wrote nothing outside the data directory
	if left := listTree(t, q.cmd.Dir); len(left) != 0 {
		t.Errorf("the server left files in its working directory: %v", left)
	}
	for _, e := range q.cmd.Env {
		if strings.HasPrefix(e, "HOME=") {
			if left := listTree(t, strings.TrimPrefix(e, "HOME=")); len(left) != 0 {
				t.Errorf("the server left files in its home directory: %v", left)
			}
		}
	}
}

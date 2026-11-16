package e2e

// Hostile text: search characters that mean something to LIKE or SQL, huge
// bodies and query strings, and ISBNs written in the three ways people write
// them. Everything goes through HTTP to the real server and database.

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

func titles(r reply) []string {
	var out []string
	bs, _ := r.JSON["books"].([]any)
	for _, b := range bs {
		out = append(out, b.(map[string]any)["title"].(string))
	}
	sort.Strings(out)
	return out
}

func (w *who) search(q string) reply {
	w.t.Helper()
	return w.want(200, "GET", "/api/books?q="+url.QueryEscape(q), nil)
}

func TestSearchTreatsWildcardsAndQuotesAsLiterals(t *testing.T) {
	_, root := install(t, nil)
	for _, title := range []string{
		"100% Pure", "100 Pure", "Pure_Gold", "PureXGold", `Back\slash`, "Back slash",
		"O'Brien's Tale", `The "Quoted" Book`, "Éclair Café", "Ünïcode Straße", "Plain",
	} {
		root.want(201, "POST", "/api/books", map[string]any{"title": title, "authors": []string{"Pat Writer"}})
	}
	all := len(root.search("").JSON["books"].([]any))
	if all != 11 {
		t.Fatalf("setup: %d books", all)
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"%", []string{"100% Pure"}},
		{"100%", []string{"100% Pure"}},
		{"_", []string{"Pure_Gold"}},
		{"pure_gold", []string{"Pure_Gold"}},
		{"\\", []string{`Back\slash`}},
		{`back\s`, []string{`Back\slash`}},
		{"o'brien", []string{"O'Brien's Tale"}},
		{`"quoted"`, []string{`The "Quoted" Book`}},
		{"éCLAIR", []string{"Éclair Café"}},
		{"CAFÉ éclair", []string{"Éclair Café"}}, // words in any order, any case
		{"ÜNÏCODE", []string{"Ünïcode Straße"}},
		{"' OR '1'='1", nil},
		{"'; DROP TABLE books; --", nil},
		{"%' OR 1=1 --", nil},
		{"zzzz-nothing", nil},
		{"pat writer", nil}, // placeholder replaced below
	}
	cases[len(cases)-1].want = []string{"100 Pure", "100% Pure", `Back slash`, `Back\slash`, "Éclair Café", "O'Brien's Tale", "Plain", "Pure_Gold", "PureXGold", `The "Quoted" Book`, "Ünïcode Straße"}
	sort.Strings(cases[len(cases)-1].want)
	for _, c := range cases {
		got := titles(root.search(c.q))
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("search %q: got %q, want %q", c.q, got, c.want)
		}
	}
	// A search that finds nothing says so: an empty list, not an error.
	if r := root.search("zzzz-nothing"); r.JSON["total"].(float64) != 0 {
		t.Errorf("no match must be total 0: %s", r.Raw)
	}
	// The injection attempts did not damage anything.
	if n := len(root.search("").JSON["books"].([]any)); n != all {
		t.Errorf("books after the injection searches: %d, was %d", n, all)
	}
	// Empty and blank queries list everything.
	for _, q := range []string{"", "   ", "\t"} {
		if n := len(root.search(q).JSON["books"].([]any)); n != all {
			t.Errorf("search %q: %d books, want all %d", q, n, all)
		}
	}
}

func TestSearchRefusesOversizeAndControlInput(t *testing.T) {
	_, root := install(t, nil)
	root.want(201, "POST", "/api/books", map[string]any{"title": "Anything", "authors": []string{"X"}})
	for name, q := range map[string]string{
		"null byte":        "a\x00b",
		"201 characters":   strings.Repeat("a", 201),
		"a megabyte":       strings.Repeat("a", 1<<20),
		"many words":       strings.Repeat("a ", 200),
		"escape character": "a\x1bb",
	} {
		r := root.do("GET", "/api/books?q="+url.QueryEscape(q), nil)
		if r.Status < 400 || r.Status >= 500 {
			t.Errorf("%s: want a 4xx, got %d: %.200s", name, r.Status, r.Raw)
		}
	}
	// Exactly 200 characters is fine.
	root.want(200, "GET", "/api/books?q="+strings.Repeat("a", 200), nil)
	for _, p := range []string{"page=-1", "page=abc", "page=99999999999999999999", "page=2147483649", "location=x", "location=-5"} {
		if r := root.do("GET", "/api/books?"+p, nil); r.Status < 400 || r.Status >= 500 {
			t.Errorf("%s: want a 4xx, got %d", p, r.Status)
		}
	}
	root.want(200, "GET", "/api/health", nil)
}

func TestHugeBodiesAreRefusedAndTheServerGoesOn(t *testing.T) {
	s, root := install(t, nil)
	big := func(n int) string { return strings.Repeat("x", n) }

	// Over the 64 KB JSON limit, on every kind of JSON route we can reach.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/books"}, {"POST", "/api/locations"}, {"POST", "/api/catalog/lookup"},
		{"POST", "/api/auth/signin"}, {"PUT", "/api/admin/settings"},
	} {
		r := root.raw(rawReq{Method: c.method, Path: c.path, ContentType: "application/json",
			Body: []byte(`{"title":"` + big(10<<20) + `"}`)})
		if r.Status != 413 && r.Status != 400 && r.Status != 422 {
			t.Errorf("%s %s with 10 MB: got %d", c.method, c.path, r.Status)
		}
	}
	// A 1 MB review and description, and a title of exactly spaces.
	book := idOf(root.want(201, "POST", "/api/books", map[string]any{"title": "Reviewed", "authors": []string{"A"}}), "id")
	before := takeSnapshot(t, s.DataDir)
	if r := root.do("PUT", fmt.Sprintf("/api/books/%d/review", book), map[string]any{"rating": 4, "text": big(1 << 20)}); r.Status != 413 && r.Status != 422 {
		t.Errorf("1 MB review: got %d", r.Status)
	}
	if r := root.do("POST", "/api/books", map[string]any{"title": "Long description", "authors": []string{"A"}, "description": big(1 << 20)}); r.Status != 413 && r.Status != 422 {
		t.Errorf("1 MB description: got %d", r.Status)
	}
	if r := root.do("POST", "/api/books", map[string]any{"title": "   \t ", "authors": []string{"A"}}); r.Status != 422 {
		t.Errorf("a title of only spaces: got %d, want 422", r.Status)
	}
	if r := root.do("POST", "/api/books", map[string]any{"title": big(5000), "authors": []string{"A"}}); r.Status != 422 {
		t.Errorf("a 5000-character title: got %d, want 422", r.Status)
	}
	if r := root.do("POST", "/api/books", map[string]any{"title": "Many authors", "authors": strings.Split(strings.Repeat("a,", 5000), ",")}); r.Status < 400 || r.Status >= 500 {
		t.Errorf("5000 authors: got %d", r.Status)
	}
	// Truncated, nested and mistyped JSON.
	for name, body := range map[string]string{
		"truncated": `{"title":"x`, "deeply nested": strings.Repeat("[", 100000), "wrong type": `{"title":5}`,
		"two values": `{"title":"a"}{"title":"b"}`, "null bytes": "{\"title\":\"a\x00b\"}", "empty": ``, "array": `[]`,
	} {
		r := root.raw(rawReq{Method: "POST", Path: "/api/books", ContentType: "application/json", Body: []byte(body)})
		if r.Status < 400 || r.Status >= 500 {
			t.Errorf("%s: got %d: %.200s", name, r.Status, r.Raw)
		}
	}
	// A multipart form with a 6 MB text field and one with hundreds of parts.
	body, ct := bookMultipart(map[string]string{"title": "t", "authors": "a", "description": big(6 << 20)}, "", "", nil)
	if r := root.raw(rawReq{Method: "POST", Path: "/api/books", Body: body, ContentType: ct}); r.Status < 400 || r.Status >= 500 {
		t.Errorf("6 MB description field: got %d", r.Status)
	}
	parts := map[string]string{"title": "many parts"}
	for i := 0; i < 400; i++ {
		parts[fmt.Sprintf("extra%d", i)] = "x"
	}
	body, ct = bookMultipart(parts, "", "", nil)
	if r := root.raw(rawReq{Method: "POST", Path: "/api/books", Body: body, ContentType: ct}); r.Status < 400 || r.Status >= 500 {
		t.Errorf("400 parts: got %d", r.Status)
	}
	// A header of a megabyte and an address of a megabyte.
	if r := root.raw(rawReq{Method: "GET", Path: "/api/books", Header: map[string]string{"X-Big": big(1 << 20)}}); r.Status < 400 {
		t.Errorf("1 MB header: got %d", r.Status)
	}
	if r := root.raw(rawReq{Method: "GET", Path: "/api/books?q=" + big(2<<20)}); r.Status < 400 || r.Status >= 500 {
		t.Errorf("2 MB query string: got %d", r.Status)
	}

	// Nothing was saved by any of it, and the server still answers.
	if d := before.diff(takeSnapshot(t, s.DataDir)); len(d) > 0 {
		t.Errorf("refused requests changed tables %v", d)
	}
	if got := imageFiles(t, s.DataDir); len(got) != 0 {
		t.Errorf("images saved by refused requests: %v", got)
	}
	root.want(200, "GET", "/api/health", nil)
}

// ---- ISBN-10, ISBN-13 and hyphens are one book ------------------------------

func TestISBNFormsAreOneBook(t *testing.T) {
	ol := testutil.FakeOpenLibrary(t)
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: ol})
	code, _ := s.SetupCode()
	root := newWho(t, s)
	root.want(200, "POST", "/api/setup/admin", map[string]string{"code": code, "username": "root", "display_name": "Root", "password": adminPassword})

	forms := []string{"9780140328721", "978-0-14-032872-1", "978 0 14 032872 1", "0140328726", "0-14-032872-6", " 0140328726 "}
	first := root.want(200, "POST", "/api/catalog/lookup", map[string]string{"isbn": forms[3]})
	if first.JSON["status"] != "found" || first.JSON["isbn"] != "9780140328721" {
		t.Fatalf("ISBN-10 lookup: %s", first.Raw)
	}
	book := idOf(root.want(201, "POST", "/api/books", map[string]any{
		"title": "Fantastic Mr Fox", "authors": []string{"Roald Dahl"}, "isbn": forms[1], "cover_token": first.JSON["cover_token"]}), "id")

	reqs := len(ol.Requests())
	for _, f := range forms {
		r := root.want(200, "POST", "/api/catalog/lookup", map[string]string{"isbn": f})
		if r.JSON["status"] != "exists" || idOf(r, "book_id") != book {
			t.Errorf("lookup %q: %s", f, r.Raw)
		}
		// Saving it again, in any form, is refused and says which book it is.
		d := root.do("POST", "/api/books", map[string]any{"title": "Second try", "authors": []string{"X"}, "isbn": f})
		if d.Status != 409 || idOf(d, "book_id") != book {
			t.Errorf("save %q again: %d %s", f, d.Status, d.Raw)
		}
		// Search by it finds the one book.
		if got := titles(root.search(f)); strings.Join(got, "|") != "Fantastic Mr Fox" {
			t.Errorf("search %q: %q", f, got)
		}
	}
	if n := len(ol.Requests()); n != reqs {
		t.Errorf("an ISBN already in the catalog was asked of the provider again (%d new requests)", n-reqs)
	}
	if n := bookCountOf(root); n != 1 {
		t.Errorf("%d books, want 1", n)
	}

	// ISBN-10 with an X check digit, in either case, is the same ISBN-13.
	a := root.want(201, "POST", "/api/books", map[string]any{"title": "Ends in X", "authors": []string{"A"}, "isbn": "080442957X"})
	if a.JSON["isbn"] != "9780804429573" {
		t.Errorf("080442957X stored as %v", a.JSON["isbn"])
	}
	if d := root.do("POST", "/api/books", map[string]any{"title": "x again", "authors": []string{"A"}, "isbn": "080442957x"}); d.Status != 409 {
		t.Errorf("lower-case x again: %d", d.Status)
	}

	// Wrong check digits, wrong lengths and letters are refused before the provider is asked.
	reqs = len(ol.Requests())
	for _, bad := range []string{"9780140328722", "0140328727", "123", "", "abcdefghij", "97801403287211", "978-0-14-03287", "9780140328721; DROP", "٩٧٨٠١٤٠٣٢٨٧٢١"} {
		r := root.do("POST", "/api/catalog/lookup", map[string]string{"isbn": bad})
		if r.Status != 422 {
			t.Errorf("lookup %q: want 422, got %d: %.200s", bad, r.Status, r.Raw)
		}
		if r.errMessage() == "" {
			t.Errorf("lookup %q: no message", bad)
		}
		if d := root.do("POST", "/api/books", map[string]any{"title": "Bad isbn", "authors": []string{"A"}, "isbn": bad}); bad != "" && d.Status != 422 {
			t.Errorf("save with ISBN %q: want 422, got %d", bad, d.Status)
		}
	}
	if n := len(ol.Requests()); n != reqs {
		t.Errorf("a bad ISBN was sent to the provider (%d requests)", n-reqs)
	}
}

func bookCountOf(w *who) int {
	n, _ := w.want(200, "GET", "/api/books", nil).JSON["total"].(float64)
	return int(n)
}

// ---- the provider in every bad mood ------------------------------------------

func TestLookupSurvivesEveryProviderFailure(t *testing.T) {
	ol := testutil.FakeOpenLibrary(t)
	ol.SetSlowDelay(11 * time.Second) // outlasts the 10 s deadline; the test waits for it once
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: ol})
	code, _ := s.SetupCode()
	root := newWho(t, s)
	root.want(200, "POST", "/api/setup/admin", map[string]string{"code": code, "username": "root", "display_name": "Root", "password": adminPassword})

	// Every failure: the librarian is told in words, nothing is saved, nothing
	// is a 500, and entering the book by hand then works.
	modes := []struct {
		name string
		mode testutil.Mode
		max  time.Duration
	}{
		{"forbidden 403", testutil.ModeForbidden, 5 * time.Second},
		{"too many requests 429", testutil.ModeTooManyRequests, 5 * time.Second},
		{"server error 500", testutil.ModeServerError, 5 * time.Second},
		{"garbage", testutil.ModeGarbage, 5 * time.Second},
		{"gone", testutil.ModeGone, 5 * time.Second},
		{"slow", testutil.ModeSlow, 10*time.Second + 1500*time.Millisecond},
	}
	isbns := []string{"9780261103573", "9780140328721"}
	for i, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			ol.SetMode(m.mode)
			isbn := isbns[i%2]
			start := time.Now()
			r := root.do("POST", "/api/catalog/lookup", map[string]string{"isbn": isbn})
			took := time.Since(start)
			if took > m.max {
				t.Errorf("answered after %s, more than %s", took, m.max)
			}
			if m.mode == testutil.ModeSlow && took < 9*time.Second {
				t.Errorf("the slow provider was given up on after only %s: did the fake not hold the request?", took)
			}
			if r.Status != 503 && !(r.Status == 200 && r.JSON["status"] == "not_found") {
				t.Fatalf("want 503 'unavailable' (or not_found), got %d: %.300s", r.Status, r.Raw)
			}
			if r.Status == 503 && !strings.Contains(strings.ToLower(r.errMessage()), "enter the details") {
				t.Errorf("the message doesn't say what to do next: %q", r.errMessage())
			}
			if r.JSON["cover_token"] != nil || r.JSON["title"] != nil {
				t.Errorf("a failed lookup returned book data: %s", r.Raw)
			}
			if n := bookCountOf(root); n != 0 {
				t.Errorf("a failed lookup saved %d books", n)
			}
			if got := imageFiles(t, s.DataDir); len(got) != 0 {
				t.Errorf("a failed lookup left images: %v", got)
			}
			// By hand, with the provider still down.
			b := root.want(201, "POST", "/api/books", map[string]any{"title": "By hand " + m.name, "authors": []string{"Me"}, "isbn": isbn})
			root.want(204, "DELETE", fmt.Sprintf("/api/books/%d", idOf(b, "id")), nil)
		})
	}

	// It is not sticky: the provider recovers and the next lookup finds the book.
	ol.SetMode(testutil.ModeOK)
	r := root.want(200, "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780261103573"})
	if r.JSON["status"] != "found" || r.JSON["title"] != "The Fellowship of the Ring" {
		t.Errorf("after the provider recovered: %s", r.Raw)
	}
	// Found: nothing saved until confirmed.
	if n := bookCountOf(root); n != 0 {
		t.Errorf("a lookup that found a book saved it: %d books", n)
	}

	// A 1x1 pixel "cover" is a missing cover.
	ol.SetMode(testutil.ModeTinyCover)
	r = root.want(200, "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780140328721"})
	if r.JSON["status"] != "found" {
		t.Fatalf("tiny cover mode: %s", r.Raw)
	}
	if tok, _ := r.JSON["cover_token"].(string); tok != "" {
		t.Errorf("a 1x1 pixel image was offered as a cover: %s", r.Raw)
	}
	// A book the provider doesn't know.
	ol.SetMode(testutil.ModeOK)
	if r := root.want(200, "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780306406157"}); r.JSON["status"] != "not_found" {
		t.Errorf("unknown ISBN: %s", r.Raw)
	}
	// A book with no cover at the provider is found without one.
	r = root.want(200, "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780064400558"})
	if r.JSON["status"] != "found" || r.JSON["cover_token"] != nil {
		t.Errorf("a book with no cover: %s", r.Raw)
	}
}

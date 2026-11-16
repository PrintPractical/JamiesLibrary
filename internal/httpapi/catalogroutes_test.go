package httpapi

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/circ"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/metadata"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/reviews"
	"jamieslibrary/internal/users"
)

// catSite is a site with the catalog routes, circulation (so copies can be on
// loan) and a fake lookup provider (so a fetched cover can be saved).
type catSite struct {
	*site
	cat     *catalog.Service
	fake    *fakeProvider
	libUser users.User
	lib     string // a librarian's session
	pat     string // a patron's session
}

func newCatSite(t *testing.T) *catSite {
	t.Helper()
	c := &catSite{fake: newFakeProvider(t)}
	c.site = newSite(t, func(o *Options) {
		c.cat = catalog.New(o.Store, catalog.Options{Settings: o.Settings, Loans: circ.Loans{}, Now: clock.Now})
		tmp, err := o.Media.Sibling(o.Store.TmpDir())
		if err != nil {
			t.Fatal(err)
		}
		o.Catalog = c.cat
		o.Reviews = reviews.New(o.Store, c.cat)
		o.Circ = circ.New(o.Store, circ.Options{Settings: o.Settings, Catalog: c.cat, Users: o.Users})
		o.Metadata, err = metadata.New(metadata.Options{Images: o.Media, Tmp: tmp, Version: "test", BaseURL: c.fake.srv.URL,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: clock.Now})
		if err != nil {
			t.Fatal(err)
		}
	})
	c.libUser = c.addUser("lib", goodPassword, perm.Librarian)
	c.addUser("pat", goodPassword, perm.Patron)
	c.lib, c.pat = c.signIn("lib", goodPassword), c.signIn("pat", goodPassword)
	return c
}

// checkout lends the copy that carries code to the patron whose session is pat.
func (c *catSite) checkout(code string) {
	c.t.Helper()
	rec := c.req("POST", "/api/desk/checkout", c.lib, body(map[string]string{"patron": c.cardOf(c.pat), "code": code}))
	if rec.Code != 200 {
		c.t.Fatalf("checkout %s: %d %s", code, rec.Code, rec.Body)
	}
}

func (c *catSite) count(table string) int {
	var n int
	_ = c.st.DB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)
	return n
}

func (c *catSite) images() int {
	ents, _ := os.ReadDir(filepath.Join(c.dir, "images"))
	return len(ents)
}

// form sends a multipart book form to method path; file is the cover's bytes ("" for none).
func (c *catSite) form(method, path, cookie string, fields [][2]string, file []byte) *httptest.ResponseRecorder {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, f := range fields {
		_ = mw.WriteField(f[0], f[1])
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("cover", "cover.bin")
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	return c.req(method, path, cookie, b.String(), "Content-Type", mw.FormDataContentType())
}

func titles(b searchBody) []string {
	var out []string
	for _, x := range b.Books {
		out = append(out, x.Title)
	}
	return out
}

// ROLE-3 "Patron tries staff action" and the routes' own permissions: a caller
// without the permission is refused with 403 (401 signed out) and nothing changes.
func TestCatalogRoutesNeedThePermission(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	staff := []struct{ method, path, body string }{
		{"POST", "/api/books", body(map[string]any{"title": "New", "authors": []string{"A"}})},
		{"PUT", fmt.Sprintf("/api/books/%d", b.ID), body(map[string]any{"title": "Changed", "authors": []string{"A"}})},
		{"DELETE", fmt.Sprintf("/api/books/%d", b.ID), ""},
		{"POST", "/api/locations", body(nameBody{"Attic"})},
		{"PUT", "/api/locations/1", body(nameBody{"Renamed"})},
		{"DELETE", "/api/locations/1?move_to=2", ""},
	}
	for _, s := range staff {
		if rec := c.req(s.method, s.path, c.pat, s.body); rec.Code != 403 {
			t.Errorf("%s %s as a patron: %d", s.method, s.path, rec.Code)
		}
		if rec := c.req(s.method, s.path, "", s.body); rec.Code != 401 {
			t.Errorf("%s %s signed out: %d", s.method, s.path, rec.Code)
		}
	}
	got, _ := c.cat.GetBook(ctxBG, b.ID)
	locs, _ := c.cat.Locations(ctxBG)
	if got.Title != "The Hobbit" || c.count("books") != 1 || len(locs) != 1 || locs[0].Name != "Main shelf" {
		t.Errorf("something changed: %+v %v", got, locs)
	}
	// Browsing needs catalog.browse.
	u := c.addUser("nobrowse", goodPassword, perm.Patron)
	nb := c.signIn("nobrowse", goodPassword)
	if _, err := c.users.SetPermission(ctxBG, u.ID, perm.CatalogBrowse, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/books", fmt.Sprintf("/api/books/%d", b.ID), "/api/locations"} {
		if rec := c.req("GET", p, nb, ""); rec.Code != 403 {
			t.Errorf("GET %s without browse: %d", p, rec.Code)
		}
		if rec := c.req("GET", p, "", ""); rec.Code != 401 {
			t.Errorf("GET %s signed out: %d", p, rec.Code)
		}
	}
}

// ROLE-3 "Removed permission": a librarian whose catalog.manage is switched off can't edit a book.
func TestROLE3_RemovedPermission(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	edit := body(map[string]any{"title": "Edited", "authors": []string{"A"}})
	path := fmt.Sprintf("/api/books/%d", b.ID)
	if rec := c.req("PUT", path, c.lib, edit); rec.Code != 200 {
		t.Fatalf("before: %d %s", rec.Code, rec.Body)
	}
	if _, err := c.users.SetPermission(ctxBG, c.libUser.ID, perm.CatalogManage, false); err != nil {
		t.Fatal(err)
	}
	again := body(map[string]any{"title": "Edited again", "authors": []string{"A"}})
	if rec := c.req("PUT", path, c.lib, again); rec.Code != 403 {
		t.Errorf("after: %d", rec.Code)
	}
	if got, _ := c.cat.GetBook(ctxBG, b.ID); got.Title != "Edited" {
		t.Errorf("title is %q", got.Title)
	}
}

// CAT-1 "Add a full book": title, two authors, ISBN, description and a cover
// (uploaded) are saved and the book page shows them all.
func TestCAT1_AddAFullBook(t *testing.T) {
	c := newCatSite(t)
	rec := c.form("POST", "/api/books", c.lib, [][2]string{{"title", "Good Omens"}, {"authors", "Terry Pratchett"},
		{"authors", "Neil Gaiman"}, {"isbn", "0-14-032872-6"}, {"description", "The end of the world."}}, smallJPEG(t))
	made := decodeAs[bookDetail](t, rec, 201)
	rec = c.req("GET", fmt.Sprintf("/api/books/%d", made.ID), c.pat, "")
	got := decodeAs[bookDetail](t, rec, 200)
	if got.Title != "Good Omens" || len(got.Authors) != 2 || got.Authors[1] != "Neil Gaiman" || got.ISBN != "9780140328721" ||
		got.Description != "The end of the world." || !strings.HasPrefix(got.CoverURL, "/media/") {
		t.Fatalf("%+v", got)
	}
	if img := c.req("GET", got.CoverURL, c.pat, ""); img.Code != 200 || img.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("cover: %d", img.Code)
	}
}

// CAT-1 "Add a full book" with the cover fetched by the lookup.
func TestCAT1_AddABookWithAFetchedCover(t *testing.T) {
	c := newCatSite(t)
	look := decodeAs[lookupBody](t, c.req("POST", "/api/catalog/lookup", c.lib, body(map[string]string{"isbn": foxISBN})), 200)
	if look.CoverToken == "" {
		t.Fatalf("%+v", look)
	}
	rec := c.req("POST", "/api/books", c.lib, body(map[string]any{"title": look.Title, "authors": look.Authors, "isbn": look.ISBN,
		"description": look.Description, "cover_token": look.CoverToken}))
	made := decodeAs[bookDetail](t, rec, 201)
	if !strings.HasPrefix(made.CoverURL, "/media/") || c.req("GET", made.CoverURL, c.pat, "").Code != 200 {
		t.Errorf("cover %q", made.CoverURL)
	}
	// The token is spent; a second book can't use it.
	rec = c.req("POST", "/api/books", c.lib, body(map[string]any{"title": "Other", "authors": []string{"A"}, "cover_token": look.CoverToken}))
	if rec.Code != 422 || c.count("books") != 1 {
		t.Errorf("reused token: %d %s", rec.Code, rec.Body)
	}
}

// CAT-1 "Old book with no ISBN or cover".
func TestCAT1_OldBookWithNoISBNOrCover(t *testing.T) {
	c := newCatSite(t)
	made := decodeAs[bookDetail](t, c.req("POST", "/api/books", c.lib, body(map[string]any{"title": "Old Tales", "authors": []string{"Anon"}})), 201)
	if made.ISBN != "" || made.CoverURL != "" || made.Title != "Old Tales" {
		t.Errorf("%+v", made)
	}
	res := decodeAs[searchBody](t, c.req("GET", "/api/books?q=old", c.pat, ""), 200)
	if len(res.Books) != 1 || res.Books[0].ID != made.ID {
		t.Errorf("%v", titles(res))
	}
}

// CAT-1 "Missing title": refused, says the title is required, nothing is saved (not even the cover).
func TestCAT1_MissingTitle(t *testing.T) {
	c := newCatSite(t)
	rec := c.req("POST", "/api/books", c.lib, body(map[string]any{"title": "  ", "authors": []string{"A"}}))
	if rec.Code != 422 || !strings.Contains(errMessage(t, rec), "title is required") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	rec = c.form("POST", "/api/books", c.lib, [][2]string{{"authors", "A"}}, smallJPEG(t))
	if rec.Code != 422 || !strings.Contains(errMessage(t, rec), "title is required") {
		t.Errorf("with a cover: %d %s", rec.Code, rec.Body)
	}
	if c.count("books") != 0 || c.images() != 0 {
		t.Errorf("saved %d books and %d images", c.count("books"), c.images())
	}
}

// CAT-1 "Bad cover": a non-image, or one over 5 MB, is refused with a message
// and the book is not saved (new) or unchanged (edit).
func TestCAT1_BadCover(t *testing.T) {
	c := newCatSite(t)
	fields := [][2]string{{"title", "Book"}, {"authors", "A"}}
	notImage := c.form("POST", "/api/books", c.lib, fields, []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"))
	if notImage.Code != 422 || !strings.Contains(errMessage(t, notImage), "JPEG, PNG or WebP") {
		t.Errorf("non-image: %d %s", notImage.Code, notImage.Body)
	}
	huge := c.form("POST", "/api/books", c.lib, fields, bytes.Repeat([]byte{1}, 5<<20+1))
	if huge.Code != 413 || !strings.Contains(errMessage(t, huge), "5 MB") {
		t.Errorf("over 5 MB: %d %s", huge.Code, huge.Body)
	}
	if c.count("books") != 0 || c.images() != 0 {
		t.Errorf("saved %d books and %d images", c.count("books"), c.images())
	}

	b := addBook(t, c.cat, "Kept", "", 0)
	rec := c.form("PUT", fmt.Sprintf("/api/books/%d", b.ID), c.lib, [][2]string{{"title", "Changed"}, {"authors", "A"}}, []byte("not an image"))
	got, _ := c.cat.GetBook(ctxBG, b.ID)
	if rec.Code != 422 || got.Title != "Kept" || got.Cover != "" || c.images() != 0 {
		t.Errorf("edit: %d %+v", rec.Code, got)
	}
	both := c.form("POST", "/api/books", c.lib, append(fields, [2]string{"cover_token", strings.Repeat("a", 32) + ".jpg"}), smallJPEG(t))
	if both.Code != 422 || c.images() != 0 {
		t.Errorf("both: %d %s", both.Code, both.Body)
	}
}

// Editing replaces the cover and removes the old image; a duplicate ISBN is a
// 409 that names the existing book.
func TestEditBookAndDuplicateISBN(t *testing.T) {
	c := newCatSite(t)
	other := addBook(t, c.cat, "Other", "9780306406157", 0)
	made := decodeAs[bookDetail](t, c.form("POST", "/api/books", c.lib, [][2]string{{"title", "Mine"}, {"authors", "A"}}, smallJPEG(t)), 201)
	old := made.CoverURL
	rec := c.form("PUT", fmt.Sprintf("/api/books/%d", made.ID), c.lib, [][2]string{{"title", "Mine, revised"}, {"authors", "B"}}, smallJPEG(t))
	got := decodeAs[bookDetail](t, rec, 200)
	if got.Title != "Mine, revised" || got.Authors[0] != "B" || got.CoverURL == old || c.images() != 1 {
		t.Errorf("%+v, %d images", got, c.images())
	}
	// Without a new cover the cover stays.
	got2 := decodeAs[bookDetail](t, c.req("PUT", fmt.Sprintf("/api/books/%d", made.ID), c.lib, body(map[string]any{"title": "Again", "authors": []string{"B"}})), 200)
	if got2.CoverURL != got.CoverURL {
		t.Errorf("cover changed: %q", got2.CoverURL)
	}
	rec = c.req("PUT", fmt.Sprintf("/api/books/%d", made.ID), c.lib, body(map[string]any{"title": "Mine", "authors": []string{"B"}, "isbn": "9780306406157"}))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"book_id":%d`, other.ID)) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec := c.req("PUT", "/api/books/9999", c.lib, body(map[string]any{"title": "x", "authors": []string{"B"}})); rec.Code != 404 {
		t.Errorf("unknown book: %d", rec.Code)
	}
}

func TestBookBodyIsStrict(t *testing.T) {
	c := newCatSite(t)
	for _, b := range []string{`{"title":"x","authors":["a"],"extra":1}`, `{"title":`, ``} {
		if rec := c.req("POST", "/api/books", c.lib, b); rec.Code != 400 {
			t.Errorf("%q: %d", b, rec.Code)
		}
	}
	rec := c.form("POST", "/api/books", c.lib, [][2]string{{"title", "x"}, {"authors", "a"}, {"role", "admin"}}, nil)
	if rec.Code != 400 || c.count("books") != 0 {
		t.Errorf("unknown form field: %d", rec.Code)
	}
}

// CAT-2: "Search by author", "Partial title", "No match".
func TestCAT2_Search(t *testing.T) {
	c := newCatSite(t)
	for _, b := range []struct{ title, author string }{{"The Hobbit", "J. R. R. Tolkien"}, {"Emma", "Jane Austen"}, {"Hobbit Cooking", "Someone"}} {
		made := decodeAs[bookDetail](t, c.req("POST", "/api/books", c.lib, body(map[string]any{"title": b.title, "authors": []string{b.author}})), 201)
		_ = made
	}
	search := func(q string) searchBody {
		return decodeAs[searchBody](t, c.req("GET", "/api/books?q="+strings.ReplaceAll(q, " ", "%20"), c.pat, ""), 200)
	}
	if got := search("tolkien"); got.Total != 1 || got.Books[0].Title != "The Hobbit" || got.Books[0].Authors[0] != "J. R. R. Tolkien" {
		t.Errorf("by author: %v", titles(got))
	}
	if got := search("HOBB"); got.Total != 2 {
		t.Errorf("partial title: %v", titles(got))
	}
	if got := search("zzzz"); got.Total != 0 || got.Books == nil || len(got.Books) != 0 {
		t.Errorf("no match: %+v", got)
	}
	if got := search(""); got.Total != 3 || got.PageSize != 50 || got.Page != 1 {
		t.Errorf("browse: %+v", got)
	}
}

// CAT-2 limits: a term over 200 characters, an unreadable page or location, and a LIKE wildcard.
func TestSearchLimits(t *testing.T) {
	c := newCatSite(t)
	addBook(t, c.cat, "100% Real", "", 0)
	addBook(t, c.cat, "Other", "", 0)
	for _, q := range []string{"q=" + strings.Repeat("a", 201), "page=x", "page=-1", "location=abc"} {
		if rec := c.req("GET", "/api/books?"+q, c.pat, ""); rec.Code != 422 {
			t.Errorf("%.30s: %d", q, rec.Code)
		}
	}
	if rec := c.req("GET", "/api/books?q="+strings.Repeat("a", 200), c.pat, ""); rec.Code != 200 {
		t.Errorf("200 characters: %d", rec.Code)
	}
	if got := decodeAs[searchBody](t, c.req("GET", "/api/books?q=%25", c.pat, ""), 200); got.Total != 1 {
		t.Errorf("%% matched %d books", got.Total)
	}
}

func TestSearchPages(t *testing.T) {
	c := newCatSite(t)
	for i := 0; i < 55; i++ {
		addBook(t, c.cat, fmt.Sprintf("Volume %02d", i), "", 0)
	}
	p1 := decodeAs[searchBody](t, c.req("GET", "/api/books", c.pat, ""), 200)
	p2 := decodeAs[searchBody](t, c.req("GET", "/api/books?page=2", c.pat, ""), 200)
	p3 := decodeAs[searchBody](t, c.req("GET", "/api/books?page=3", c.pat, ""), 200)
	if p1.Total != 55 || len(p1.Books) != 50 || len(p2.Books) != 5 || len(p3.Books) != 0 || p2.Page != 2 {
		t.Errorf("%d %d %d of %d", len(p1.Books), len(p2.Books), len(p3.Books), p1.Total)
	}
}

// CAT-8 "Filter by location": only books with a copy in the chosen location are listed.
func TestCAT8_FilterByLocation(t *testing.T) {
	c := newCatSite(t)
	study := decodeAs[locationBody](t, c.req("POST", "/api/locations", c.lib, body(nameBody{"Study"})), 201)
	kitchen := decodeAs[locationBody](t, c.req("POST", "/api/locations", c.lib, body(nameBody{"Kitchen"})), 201)
	for title, loc := range map[string]int64{"In the study": study.ID, "In the kitchen": kitchen.ID} {
		b := addBook(t, c.cat, title, "", 0)
		if _, err := c.cat.AddCopy(ctxBG, 1, b.ID, catalog.CopyInput{LocationID: loc}); err != nil {
			t.Fatal(err)
		}
	}
	got := decodeAs[searchBody](t, c.req("GET", fmt.Sprintf("/api/books?location=%d", kitchen.ID), c.pat, ""), 200)
	if got.Total != 1 || got.Books[0].Title != "In the kitchen" {
		t.Errorf("%v", titles(got))
	}
	got = decodeAs[searchBody](t, c.req("GET", fmt.Sprintf("/api/books?q=study&location=%d", kitchen.ID), c.pat, ""), 200)
	if got.Total != 0 {
		t.Errorf("search and filter: %v", titles(got))
	}
	// A book page shows each copy's location.
	book := decodeAs[searchBody](t, c.req("GET", fmt.Sprintf("/api/books?location=%d", study.ID), c.pat, ""), 200).Books[0]
	d := decodeAs[bookDetail](t, c.req("GET", fmt.Sprintf("/api/books/%d", book.ID), c.pat, ""), 200)
	if len(d.Copies) != 1 || d.Copies[0].Location != "Study" {
		t.Errorf("%+v", d.Copies)
	}
}

// CAT-8: a new installation has "Main shelf"; it can be renamed for all its copies; names are unique ignoring case.
func TestCAT8_ListRenameAndDuplicateName(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "Book", "", 1)
	ls := decodeAs[[]locationBody](t, c.req("GET", "/api/locations", c.pat, ""), 200)
	if len(ls) != 1 || ls[0].Name != "Main shelf" || ls[0].Copies != 1 {
		t.Fatalf("%+v", ls)
	}
	decodeAs[locationBody](t, c.req("POST", "/api/locations", c.lib, body(nameBody{"Study"})), 201)
	for _, name := range []string{"study", "STUDY", " Study "} {
		if rec := c.req("POST", "/api/locations", c.lib, body(nameBody{name})); rec.Code != 409 {
			t.Errorf("duplicate %q: %d", name, rec.Code)
		}
	}
	if rec := c.req("POST", "/api/locations", c.lib, body(nameBody{" "})); rec.Code != 422 {
		t.Errorf("empty name: %d", rec.Code)
	}
	if rec := c.req("PUT", "/api/locations/1", c.lib, body(nameBody{"study"})); rec.Code != 409 {
		t.Errorf("rename onto another: %d", rec.Code)
	}
	if got := decodeAs[locationBody](t, c.req("PUT", "/api/locations/1", c.lib, body(nameBody{"Front room"})), 200); got.Name != "Front room" {
		t.Errorf("%+v", got)
	}
	d := decodeAs[bookDetail](t, c.req("GET", fmt.Sprintf("/api/books/%d", b.ID), c.pat, ""), 200)
	if d.Copies[0].Location != "Front room" {
		t.Errorf("the copy shows %q", d.Copies[0].Location)
	}
	if rec := c.req("PUT", "/api/locations/999", c.lib, body(nameBody{"x"})); rec.Code != 404 {
		t.Errorf("unknown: %d", rec.Code)
	}
}

// CAT-8 "Delete a location in use" and "Delete an empty location".
func TestCAT8_DeleteLocation(t *testing.T) {
	c := newCatSite(t)
	addBook(t, c.cat, "Book", "", 3)
	attic := decodeAs[locationBody](t, c.req("POST", "/api/locations", c.lib, body(nameBody{"Attic"})), 201)

	rec := c.req("DELETE", "/api/locations/1", c.lib, "")
	if rec.Code != 409 || !strings.Contains(errMessage(t, rec), "3 copies are there") || !strings.Contains(errMessage(t, rec), "Move them") {
		t.Errorf("in use: %d %s", rec.Code, rec.Body)
	}
	if n := c.count("copies WHERE location_id = 1"); n != 3 {
		t.Errorf("%d copies left in the location", n)
	}
	// Moving to itself or to nowhere changes nothing.
	for _, q := range []string{"move_to=1", "move_to=999", "move_to=0", "move_to=x"} {
		if rec := c.req("DELETE", "/api/locations/1?"+q, c.lib, ""); rec.Code != 422 {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
	if len(decodeAs[[]locationBody](t, c.req("GET", "/api/locations", c.pat, ""), 200)) != 2 || c.count("copies WHERE location_id = 1") != 3 {
		t.Error("a refused move changed something")
	}
	if rec := c.req("DELETE", fmt.Sprintf("/api/locations/1?move_to=%d", attic.ID), c.lib, ""); rec.Code != 204 {
		t.Fatalf("move and delete: %d %s", rec.Code, rec.Body)
	}
	ls := decodeAs[[]locationBody](t, c.req("GET", "/api/locations", c.pat, ""), 200)
	if len(ls) != 1 || ls[0].Name != "Attic" || ls[0].Copies != 3 {
		t.Errorf("%+v", ls)
	}
	// Now Attic holds copies; add an empty one and delete it.
	empty := decodeAs[locationBody](t, c.req("POST", "/api/locations", c.lib, body(nameBody{"Empty"})), 201)
	if rec := c.req("DELETE", fmt.Sprintf("/api/locations/%d", empty.ID), c.lib, ""); rec.Code != 204 {
		t.Errorf("empty: %d", rec.Code)
	}
	if rec := c.req("DELETE", fmt.Sprintf("/api/locations/%d", empty.ID), c.lib, ""); rec.Code != 404 {
		t.Errorf("again: %d", rec.Code)
	}
}

// CAT-4 "All copies out": the page says none is available and when each is due, and never who has it.
func TestCAT4_AllCopiesOut(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	d := decodeAs[bookDetail](t, c.req("GET", fmt.Sprintf("/api/books/%d", b.ID), c.pat, ""), 200)
	if d.Available != 1 || d.CopyCount != 1 || !d.Copies[0].Available || d.Copies[0].DueDate != "" {
		t.Fatalf("on the shelf: %+v", d)
	}
	c.checkout("9780306406157")
	rec := c.req("GET", fmt.Sprintf("/api/books/%d", b.ID), c.pat, "")
	d = decodeAs[bookDetail](t, rec, 200)
	if d.Available != 0 || d.CopyCount != 1 || d.Copies[0].Available || d.Copies[0].DueDate != "2026-01-29" {
		t.Errorf("all out: %+v", d)
	}
	for _, leak := range []string{"pat", "patron", "card"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
			t.Errorf("the page mentions %q: %s", leak, rec.Body)
		}
	}
	if res := decodeAs[searchBody](t, c.req("GET", "/api/books?q=hobbit", c.pat, ""), 200); res.Books[0].Available != 0 || res.Books[0].CopyCount != 1 {
		t.Errorf("search shows %+v", res.Books[0])
	}
	if rec := c.req("GET", "/api/books/9999", c.pat, ""); rec.Code != 404 {
		t.Errorf("unknown book: %d", rec.Code)
	}
	if rec := c.req("GET", "/api/books/abc", c.pat, ""); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

// CAT-5: a book can't be removed while a copy is out, says why, and after check-in
// removal keeps the loan; the book leaves search and its page.
func TestCAT5_HistorySurvivesRemoval(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 2)
	path := fmt.Sprintf("/api/books/%d", b.ID)
	c.checkout("9780306406157")
	rec := c.req("DELETE", path, c.lib, "")
	if rec.Code != 409 || !strings.Contains(errMessage(t, rec), "1 copy is on loan") {
		t.Fatalf("refused while on loan: %d %s", rec.Code, rec.Body)
	}
	if c.req("GET", path, c.pat, "").Code != 200 {
		t.Error("the refused removal removed the book")
	}
	if rec := c.req("POST", "/api/desk/checkin", c.lib, body(map[string]string{"code": "9780306406157"})); rec.Code != 200 {
		t.Fatalf("checkin: %d %s", rec.Code, rec.Body)
	}
	if rec := c.req("DELETE", path, c.lib, ""); rec.Code != 204 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if c.req("GET", path, c.pat, "").Code != 404 {
		t.Error("the removed book still has a page")
	}
	if res := decodeAs[searchBody](t, c.req("GET", "/api/books?q=hobbit", c.pat, ""), 200); res.Total != 0 {
		t.Errorf("the removed book is in search: %v", titles(res))
	}
	if c.count("loans") != 1 || c.count("books") != 1 {
		t.Errorf("history lost: %d loans, %d books", c.count("loans"), c.count("books"))
	}
	if rec := c.req("DELETE", path, c.lib, ""); rec.Code != 404 {
		t.Errorf("again: %d", rec.Code)
	}
}

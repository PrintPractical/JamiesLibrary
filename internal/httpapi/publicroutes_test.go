package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"jamieslibrary/internal/catalog"
)

// publicAllowlist is the exact set of fields of sec §7.
var publicAllowlist = map[string]bool{
	"id": true, "title": true, "authors": true, "isbn": true, "description": true, "cover_url": true,
	"available_copies": true, "total_copies": true, "rating_average": true, "rating_count": true,
}

func (c *catSite) publicOn(on bool) {
	c.t.Helper()
	if err := c.set.SetPublicCatalog(ctxBG, on); err != nil {
		c.t.Fatal(err)
	}
}

// ACCESS-4 "Off by default": with the setting untouched, nobody signed out reaches the public catalog.
func TestACCESS4_OffByDefault(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	for _, p := range []string{"/api/public/books", "/api/public/books?q=hobbit", fmt.Sprintf("/api/public/books/%d", b.ID)} {
		if rec := c.req("GET", p, "", ""); rec.Code != 404 {
			t.Errorf("GET %s signed out: %d", p, rec.Code)
		}
	}
	// Signed-in callers get the same 404: the routes don't exist while it is off.
	if rec := c.req("GET", "/api/public/books", c.lib, ""); rec.Code != 404 {
		t.Errorf("signed in: %d", rec.Code)
	}
}

// ACCESS-4 "Enabled": signed out, a visitor can search and open a book, sees whether a copy is available, and the setting is checked on every request.
func TestACCESS4_Enabled(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 2)
	addBook(t, c.cat, "Dune", "", 1)
	c.publicOn(true)

	res := decodeAs[publicSearchBody](t, c.req("GET", "/api/public/books?q=hobbit", "", ""), 200)
	if res.Total != 1 || len(res.Books) != 1 || res.Books[0].Title != "The Hobbit" || res.Books[0].Available != 2 || res.Books[0].Total != 2 {
		t.Fatalf("search: %+v", res)
	}
	d := decodeAs[publicBook](t, c.req("GET", fmt.Sprintf("/api/public/books/%d", b.ID), "", ""), 200)
	if d.ID != b.ID || d.ISBN != "9780306406157" || len(d.Authors) != 1 || d.Available != 2 || d.Total != 2 {
		t.Fatalf("detail: %+v", d)
	}
	c.checkout("9780306406157")
	c.checkout("9780306406157")
	d = decodeAs[publicBook](t, c.req("GET", fmt.Sprintf("/api/public/books/%d", b.ID), "", ""), 200)
	if d.Available != 0 || d.Total != 2 {
		t.Errorf("all out: %+v", d)
	}
	if rec := c.req("GET", "/api/public/books/9999", "", ""); rec.Code != 404 {
		t.Errorf("unknown book: %d", rec.Code)
	}
	if rec := c.req("GET", "/api/public/books/abc", "", ""); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
	c.publicOn(false)
	if rec := c.req("GET", "/api/public/books", "", ""); rec.Code != 404 {
		t.Errorf("after turning it off: %d", rec.Code)
	}
}

// Anonymous callers reach nothing else of the catalog while it is on.
func TestPublicCatalogOpensNothingElse(t *testing.T) {
	c := newCatSite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.publicOn(true)
	for _, p := range []string{"/api/books", fmt.Sprintf("/api/books/%d", b.ID), "/api/locations"} {
		if rec := c.req("GET", p, "", ""); rec.Code != 401 {
			t.Errorf("GET %s signed out: %d", p, rec.Code)
		}
	}
}

// Bounds: the search is at most 200 characters, a page at most 50 books.
func TestPublicSearchBounds(t *testing.T) {
	c := newCatSite(t)
	for i := 0; i < 55; i++ {
		addBook(t, c.cat, fmt.Sprintf("Volume %02d", i), "", 0)
	}
	c.publicOn(true)
	res := decodeAs[publicSearchBody](t, c.req("GET", "/api/public/books?q=volume", "", ""), 200)
	if len(res.Books) != 50 || res.Total != 55 || res.PageSize != 50 {
		t.Errorf("page 1: %d books, total %d, size %d", len(res.Books), res.Total, res.PageSize)
	}
	res = decodeAs[publicSearchBody](t, c.req("GET", "/api/public/books?q=volume&page=2", "", ""), 200)
	if len(res.Books) != 5 {
		t.Errorf("page 2: %d books", len(res.Books))
	}
	if rec := c.req("GET", "/api/public/books?q="+strings.Repeat("a", 201), "", ""); rec.Code != 422 {
		t.Errorf("201 characters: %d", rec.Code)
	}
	if rec := c.req("GET", "/api/public/books?q="+strings.Repeat("a", 200), "", ""); rec.Code != 200 {
		t.Errorf("200 characters: %d", rec.Code)
	}
	if rec := c.req("GET", "/api/public/books?page=-1", "", ""); rec.Code != 422 {
		t.Errorf("bad page: %d", rec.Code)
	}
}

// The public location filter doesn't exist: it would let a visitor probe location names.
func TestPublicSearchIgnoresLocation(t *testing.T) {
	c := newCatSite(t)
	addBook(t, c.cat, "The Hobbit", "", 1)
	addBook(t, c.cat, "Dune", "", 1)
	c.publicOn(true)
	res := decodeAs[publicSearchBody](t, c.req("GET", "/api/public/books?location=999", "", ""), 200)
	if res.Total != 2 {
		t.Errorf("total %d", res.Total)
	}
}

// Every field of every public response is on the allowlist, and nothing about
// copies, loans, patrons or locations leaks, for a book that is on loan.
func TestPublicResponsesHoldOnlyTheAllowlist(t *testing.T) {
	c := newCatSite(t)
	attic, err := c.cat.CreateLocation(ctxBG, 1, "Grandmother's attic")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.cat.CreateBook(ctxBG, 1, catalog.BookInput{Title: "The Hobbit", Authors: []string{"J. R. R. Tolkien"},
		ISBN: "9780306406157", Description: "There and back again."})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.cat.AddCopy(ctxBG, 1, b.ID, catalog.CopyInput{LocationID: attic.ID, Condition: "damaged", Sticker: "STICKER-7731"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.cat.AddCopy(ctxBG, 1, b.ID, catalog.CopyInput{LocationID: attic.ID}); err != nil {
		t.Fatal(err)
	}
	c.checkout("STICKER-7731")
	c.publicOn(true)

	card := c.cardOf(c.pat)
	secrets := []string{"attic", "damaged", "STICKER-7731", "pat", "card", card, "due", "loan", "review", "2026-", "condition", "location", "barcode", "code"}
	for _, p := range []string{"/api/public/books", "/api/public/books?q=hobbit", fmt.Sprintf("/api/public/books/%d", b.ID)} {
		rec := c.req("GET", p, "", "")
		if rec.Code != 200 {
			t.Fatalf("GET %s: %d", p, rec.Code)
		}
		var raw any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		checkFields(t, p, raw, true)
		low := strings.ToLower(rec.Body.String())
		for _, s := range secrets {
			if strings.Contains(low, strings.ToLower(s)) {
				t.Errorf("GET %s mentions %q: %s", p, s, rec.Body)
			}
		}
	}
}

// checkFields fails on any object key outside the allowlist, except the
// search envelope's own keys at the top of a search.
func checkFields(t *testing.T, where string, v any, top bool) {
	t.Helper()
	envelope := map[string]bool{"books": true, "total": true, "page": true, "page_size": true}
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if !publicAllowlist[k] && !(top && envelope[k]) {
				t.Errorf("%s: field %q is not on the allowlist", where, k)
			}
			checkFields(t, where, val, false)
		}
	case []any:
		for _, e := range x {
			checkFields(t, where, e, false)
		}
	}
}

// A page of several books shows each book's own counts and rating: the batched
// reads must not mix one book's numbers into another's.
func TestPublicSearchShowsEachBooksOwnCountsAndRatings(t *testing.T) {
	c := newCatSite(t)
	a := addBook(t, c.cat, "Alpha", "9780306406157", 2)
	b := addBook(t, c.cat, "Beta", "", 1)
	addBook(t, c.cat, "Gamma", "", 0)
	c.publicOn(true)
	c.checkout("9780306406157")
	put := func(id int64, rating any) {
		t.Helper()
		rec := c.req("PUT", fmt.Sprintf("/api/books/%d/review", id), c.pat, body(map[string]any{"rating": rating, "text": "ok"}))
		if rec.Code != 200 {
			t.Fatalf("review: %d %s", rec.Code, rec.Body)
		}
	}
	put(a.ID, 4)
	put(b.ID, nil) // a review without a rating is not counted

	res := decodeAs[publicSearchBody](t, c.req("GET", "/api/public/books", "", ""), 200)
	if len(res.Books) != 3 {
		t.Fatalf("books: %+v", res.Books)
	}
	want := []struct {
		title           string
		avail, total, n int
		average         float64
	}{{"Alpha", 1, 2, 1, 4}, {"Beta", 1, 1, 0, 0}, {"Gamma", 0, 0, 0, 0}}
	for i, w := range want {
		g := res.Books[i]
		if g.Title != w.title || g.Available != w.avail || g.Total != w.total || g.RatingCount != w.n || g.RatingAverage != w.average {
			t.Errorf("book %d = %+v, want %+v", i, g, w)
		}
	}
}

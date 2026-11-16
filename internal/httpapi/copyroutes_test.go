package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"jamieslibrary/internal/barcode/barcodetest"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
)

// copySite is a catalog site with an administrator signed in too.
type copySite struct {
	*catSite
	admin string
}

func newCopySite(t *testing.T) *copySite {
	t.Helper()
	c := &copySite{catSite: newCatSite(t)}
	c.addUser("boss", goodPassword, perm.Administrator)
	c.admin = c.signIn("boss", goodPassword)
	return c
}

func (c *copySite) addCopy(bookID int64, form copyForm) *httptest.ResponseRecorder {
	return c.req("POST", fmt.Sprintf("/api/books/%d/copies", bookID), c.lib, body(form))
}

func (c *copySite) setMode(mode string) *httptest.ResponseRecorder {
	return c.req("PUT", "/api/admin/copy-id-mode", c.admin, body(map[string]string{"mode": mode}))
}

func (c *copySite) mustSetMode(mode string) {
	c.t.Helper()
	if rec := c.setMode(mode); rec.Code != 200 {
		c.t.Fatalf("mode %s: %d %s", mode, rec.Code, rec.Body)
	}
}

func (c *copySite) bookPage(id int64) bookDetail {
	c.t.Helper()
	return decodeAs[bookDetail](c.t, c.req("GET", fmt.Sprintf("/api/books/%d", id), c.pat, ""), 200)
}

func (c *copySite) labels(ids []int64, layout string) *httptest.ResponseRecorder {
	return c.req("POST", "/api/catalog/labels", c.lib, body(map[string]any{"copy_ids": ids, "layout": layout}))
}

func (c *copySite) pdf(rec *httptest.ResponseRecorder) []barcodetest.PDFPage {
	c.t.Helper()
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" {
		c.t.Fatalf("labels: %d %s %.200s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	pages, err := barcodetest.ReadPDF(rec.Body.Bytes())
	if err != nil {
		c.t.Fatal(err)
	}
	return pages
}

func (c *copySite) activityCount() int {
	return c.count("activity")
}

func has(texts []string, want string) bool {
	for _, t := range texts {
		if t == want {
			return true
		}
	}
	return false
}

func near1(a, b float64) bool { return a-b < 0.1 && b-a < 0.1 }

// ROLE-3 and the routes' own permissions: a caller without the permission is
// refused with 403 (401 signed out) and nothing changes.
func TestCopyRoutesNeedThePermission(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	cp, _ := c.cat.CopiesOf(ctxBG, b.ID)
	staff := []struct{ method, path, body, cookie string }{
		{"POST", fmt.Sprintf("/api/books/%d/copies", b.ID), body(copyForm{LocationID: 1}), c.pat},
		{"PUT", fmt.Sprintf("/api/copies/%d", cp[0].ID), body(copyForm{LocationID: 1, Condition: "poor"}), c.pat},
		{"DELETE", fmt.Sprintf("/api/copies/%d", cp[0].ID), "", c.pat},
		{"POST", "/api/catalog/labels", body(map[string]any{"copy_ids": []int64{cp[0].ID}, "layout": "single"}), c.pat},
		{"PUT", "/api/admin/copy-id-mode", body(map[string]string{"mode": "own"}), c.pat},
		{"PUT", "/api/admin/copy-id-mode", body(map[string]string{"mode": "own"}), c.lib}, // a librarian can't change settings
	}
	for _, s := range staff {
		if rec := c.req(s.method, s.path, s.cookie, s.body); rec.Code != 403 {
			t.Errorf("%s %s as %s: %d", s.method, s.path, s.cookie[:4], rec.Code)
		}
		if rec := c.req(s.method, s.path, "", s.body); rec.Code != 401 {
			t.Errorf("%s %s signed out: %d", s.method, s.path, rec.Code)
		}
	}
	// The admin has settings.manage but no catalog.manage in the librarian sense: staff routes are the librarian's.
	got, _ := c.cat.CopiesOf(ctxBG, b.ID)
	if len(got) != 1 || got[0].Condition != cp[0].Condition || c.set.Get().CopyIDMode != settings.CopyIDISBN || c.activityCount() != 0 {
		t.Errorf("something changed: %+v, mode %s, %d log entries", got, c.set.Get().CopyIDMode, c.activityCount())
	}
}

// CAT-3 "Two copies": both copies are listed with number, condition, location and availability.
func TestCAT3_TwoCopies(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 0)
	attic := decodeAs[locationBody](t, c.req("POST", "/api/locations", c.lib, body(nameBody{"Attic"})), 201)
	decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1, Condition: "new"}), 201)
	decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: attic.ID, Condition: "worn"}), 201)
	c.checkout("9780306406157")
	got := c.bookPage(b.ID).Copies
	if len(got) != 2 {
		t.Fatalf("%d copies: %+v", len(got), got)
	}
	if got[0].Number != 1 || got[0].Condition != "new" || got[0].Location != "Main shelf" || got[0].Available {
		t.Errorf("copy 1: %+v", got[0])
	}
	if got[1].Number != 2 || got[1].Condition != "worn" || got[1].Location != "Attic" || !got[1].Available {
		t.Errorf("copy 2: %+v", got[1])
	}
}

// CAT-3 "Generated barcode": in own-barcode mode a copy added with no barcode
// gets a unique one that is no patron's card number.
func TestCAT3_GeneratedBarcode(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 0)
	c.mustSetMode("own")
	one := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1}), 201)
	two := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1}), 201)
	if one.Barcode == "" || two.Barcode == "" || one.Barcode == two.Barcode {
		t.Fatalf("barcodes %q %q", one.Barcode, two.Barcode)
	}
	for _, cookie := range []string{c.pat, c.lib} {
		if n := c.cardOf(cookie); n == one.Barcode || n == two.Barcode {
			t.Errorf("a copy has a patron's card number %s", n)
		}
	}
}

// CAT-3 "Existing sticker": the entered barcode is the copy's, and scanning it finds the copy.
func TestCAT3_ExistingSticker(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 0)
	c.mustSetMode("own")
	made := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1, Sticker: " x-123 "}), 201)
	if made.Barcode != "X-123" {
		t.Errorf("barcode %q", made.Barcode)
	}
	r := decodeAs[resolveResponse](t, c.req("POST", "/api/desk/resolve", c.lib, `{"code":"x-123","step":"item"}`), 200)
	if r.Kind != "copy" || r.Copy.ID != made.ID {
		t.Errorf("scanned: %+v", r)
	}
}

// CAT-3 "Duplicate copy barcode": another copy can't have barcode X, and a
// refused copy leaves nothing behind (no copy, no location made for it).
func TestCAT3_DuplicateCopyBarcode(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 0)
	c.mustSetMode("own")
	decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1, Sticker: "X1"}), 201)
	rec := c.addCopy(b.ID, copyForm{NewLocation: "Attic", Sticker: "x1"})
	if rec.Code != 409 {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	if c.count("copies") != 1 || c.count("locations") != 1 {
		t.Errorf("%d copies, %d locations after a refusal", c.count("copies"), c.count("locations"))
	}
}

// CAT-3 "Sticker equals a card number": refused, and it says it is a card number.
func TestCAT3_StickerEqualsACardNumber(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 0)
	c.mustSetMode("own")
	rec := c.addCopy(b.ID, copyForm{LocationID: 1, Sticker: c.cardOf(c.pat)})
	if rec.Code != 409 || !strings.Contains(errMessage(t, rec), "card number") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if c.count("copies") != 0 {
		t.Error("the copy was saved")
	}
}

// A copy can go in a location made in the same request; bad input is refused.
func TestAddCopyWithANewLocationAndBadInput(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 0)
	made := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{NewLocation: "  Reading room "}), 201)
	if made.Location != "Reading room" || made.Condition != "good" || made.Number != 1 || !made.Available {
		t.Errorf("%+v", made)
	}
	if rec := c.addCopy(b.ID, copyForm{NewLocation: "reading ROOM"}); rec.Code != 409 {
		t.Errorf("same name again: %d %s", rec.Code, rec.Body)
	}
	bad := map[string]copyForm{
		"no location":      {},
		"both locations":   {LocationID: 1, NewLocation: "Attic"},
		"unknown location": {LocationID: 99},
		"bad condition":    {LocationID: 1, Condition: "sticky"},
		"bad sticker":      {LocationID: 1, Sticker: "has space"},
	}
	for name, f := range bad {
		if rec := c.addCopy(b.ID, f); rec.Code != 422 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := c.addCopy(999, copyForm{LocationID: 1}); rec.Code != 404 {
		t.Errorf("unknown book: %d", rec.Code)
	}
	if rec := c.req("POST", fmt.Sprintf("/api/books/%d/copies", b.ID), c.lib, `{"location_id":1,"extra":1}`); rec.Code == 201 {
		t.Error("an unknown field was accepted")
	}
	if c.count("copies") != 1 || c.count("locations") != 2 {
		t.Errorf("%d copies, %d locations", c.count("copies"), c.count("locations"))
	}
}

// CAT-5: edit a copy's condition, location and barcode; its number stays.
func TestEditCopy(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 2)
	cps, _ := c.cat.CopiesOf(ctxBG, b.ID)
	path := fmt.Sprintf("/api/copies/%d", cps[1].ID)
	if _, err := c.cat.UpdateCopy(ctxBG, 1, cps[0].ID, catalog.CopyInput{LocationID: 1, Sticker: "FIRST"}); err != nil {
		t.Fatal(err)
	}
	got := decodeAs[staffCopyBody](t, c.req("PUT", path, c.lib, body(copyForm{NewLocation: "Attic", Condition: "poor", Sticker: "st-9"})), 200)
	if got.Number != 2 || got.Condition != "poor" || got.Location != "Attic" || got.Barcode != "ST-9" {
		t.Errorf("%+v", got)
	}
	if rec := c.req("PUT", path, c.lib, body(copyForm{LocationID: 1, Sticker: "first"})); rec.Code != 409 {
		t.Errorf("another copy's barcode: %d %s", rec.Code, rec.Body)
	}
	if got, _ := c.cat.GetCopy(ctxBG, cps[1].ID); got.Code != "ST-9" {
		t.Errorf("the refused edit changed the copy: %+v", got)
	}
	if rec := c.req("PUT", "/api/copies/999", c.lib, body(copyForm{LocationID: 1})); rec.Code != 404 {
		t.Errorf("unknown copy: %d", rec.Code)
	}
	if rec := c.req("PUT", "/api/copies/zero", c.lib, body(copyForm{LocationID: 1})); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

// CAT-5 "Remove checked-out copy": refused, and says why; the copy stays.
func TestCAT5_RemoveCheckedOutCopy(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.checkout("9780306406157")
	cps, _ := c.cat.CopiesOf(ctxBG, b.ID)
	rec := c.req("DELETE", fmt.Sprintf("/api/copies/%d", cps[0].ID), c.lib, "")
	if rec.Code != 409 || !strings.Contains(errMessage(t, rec), "on loan") || !strings.Contains(errMessage(t, rec), "check it in") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := c.bookPage(b.ID).Copies; len(got) != 1 || got[0].Available {
		t.Errorf("the refused removal changed the copy: %+v", got)
	}
}

// CAT-5 "History survives removal": a returned copy is removed, leaves the
// catalog, and its past loans stay (with the patron and the dates).
func TestCAT5_CopyHistorySurvivesRemoval(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 2)
	cps, _ := c.cat.CopiesOf(ctxBG, b.ID)
	c.mustSetMode("own")
	cps, _ = c.cat.CopiesOf(ctxBG, b.ID)
	c.checkout(cps[0].Code)
	if rec := c.req("POST", "/api/desk/checkin", c.lib, body(map[string]string{"code": cps[0].Code})); rec.Code != 200 {
		t.Fatalf("checkin: %d %s", rec.Code, rec.Body)
	}
	if rec := c.req("DELETE", fmt.Sprintf("/api/copies/%d", cps[0].ID), c.lib, ""); rec.Code != 204 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if got := c.bookPage(b.ID).Copies; len(got) != 1 || got[0].Number != 2 {
		t.Errorf("copies after removal: %+v", got)
	}
	if rec := c.req("DELETE", fmt.Sprintf("/api/copies/%d", cps[0].ID), c.lib, ""); rec.Code != 404 {
		t.Errorf("again: %d", rec.Code)
	}
	if rec := c.req("POST", "/api/desk/resolve", c.lib, body(map[string]string{"code": cps[0].Code, "step": "item"})); rec.Code != 404 {
		t.Errorf("a removed copy's code still scans: %d", rec.Code)
	}
	var patron int64
	var returned string
	err := c.st.DB().QueryRow(`SELECT patron_id, COALESCE(returned_at, '') FROM loans WHERE copy_id = ?`, cps[0].ID).Scan(&patron, &returned)
	if err != nil || patron == 0 || returned == "" || c.count("loans") != 1 {
		t.Errorf("history lost: %v patron %d returned %q, %d loans", err, patron, returned, c.count("loans"))
	}
	// The sticker is free again for a new copy.
	made := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1, Sticker: cps[0].Code}), 201)
	if made.Number != 3 || made.Barcode != cps[0].Code {
		t.Errorf("%+v", made)
	}
}

// CAT-7 "Print a sheet": own-barcode mode, several copies including one just
// added with no sticker, Avery 5160: a US Letter page with each copy's barcode and title.
func TestCAT7_PrintASheet(t *testing.T) {
	c := newCopySite(t)
	hobbit := addBook(t, c.cat, "The Hobbit", "9780306406157", 2)
	dune := addBook(t, c.cat, "Dune", "", 0)
	c.mustSetMode("own")
	fresh := decodeAs[staffCopyBody](t, c.addCopy(dune.ID, copyForm{LocationID: 1}), 201)
	cps, _ := c.cat.CopiesOf(ctxBG, hobbit.ID)
	ids := []int64{cps[0].ID, cps[1].ID, fresh.ID}
	pages := c.pdf(c.labels(ids, "avery5160"))
	if len(pages) != 1 {
		t.Fatalf("%d pages", len(pages))
	}
	p := pages[0]
	if !near1(p.Width, 215.9) || !near1(p.Height, 279.4) {
		t.Errorf("page is %.1f x %.1f mm, want US Letter", p.Width, p.Height)
	}
	for _, code := range []string{cps[0].Code, cps[1].Code, fresh.Barcode} {
		if code == "" || !has(p.Texts, code) {
			t.Errorf("barcode %q is not on the sheet: %q", code, p.Texts)
		}
	}
	titles := 0
	for _, s := range p.Texts {
		if s == "The Hobbit" || s == "Dune" {
			titles++
		}
	}
	if titles != 3 || p.Bars == 0 {
		t.Errorf("%d titles, %d bars: %q", titles, p.Bars, p.Texts)
	}
	if has(p.Texts, "Copy 1") || has(p.Texts, "Main shelf") {
		t.Errorf("own-barcode labels show only barcode and title: %q", p.Texts)
	}
}

// CAT-7 "By-ISBN label": by-ISBN mode, copy 2, one per page: the ISBN
// barcode, title, "copy 2" and location on a label-sized page.
func TestCAT7_ByISBNLabel(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	attic := decodeAs[locationBody](t, c.req("POST", "/api/locations", c.lib, body(nameBody{"Attic"})), 201)
	two := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: attic.ID}), 201)
	if two.Number != 2 || two.Barcode != "" {
		t.Fatalf("copy 2: %+v", two)
	}
	pages := c.pdf(c.labels([]int64{two.ID}, "single"))
	if len(pages) != 1 {
		t.Fatalf("%d pages", len(pages))
	}
	p := pages[0]
	if !near1(p.Width, 62) || !near1(p.Height, 29) {
		t.Errorf("page is %.1f x %.1f mm", p.Width, p.Height)
	}
	for _, want := range []string{"9780306406157", "The Hobbit", "Copy 2", "Attic"} {
		if !has(p.Texts, want) {
			t.Errorf("%q is not on the label: %q", want, p.Texts)
		}
	}
	// A book with no ISBN has no ISBN barcode: its label carries its own code.
	old := addBook(t, c.cat, "Old Almanac", "", 1)
	oldCopy, _ := c.cat.CopiesOf(ctxBG, old.ID)
	if oldCopy[0].Code == "" {
		t.Fatal("a book with no ISBN has a copy with no code")
	}
	p = c.pdf(c.labels([]int64{oldCopy[0].ID}, "single"))[0]
	if !has(p.Texts, oldCopy[0].Code) || !has(p.Texts, "Old Almanac") || !has(p.Texts, "Copy 1") {
		t.Errorf("%q", p.Texts)
	}
}

// 31 copies on Avery 5160 give 2 pages, 30 labels then 1; L7160 is A4 with 21 a page.
func TestLabelSheetsPaginate(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 31)
	cps, _ := c.cat.CopiesOf(ctxBG, b.ID)
	var ids []int64
	for _, cp := range cps {
		ids = append(ids, cp.ID)
	}
	perPage := func(pages []barcodetest.PDFPage) (out []int) {
		for _, p := range pages {
			n := 0
			for _, s := range p.Texts {
				if s == "The Hobbit" {
					n++
				}
			}
			out = append(out, n)
		}
		return out
	}
	pages := c.pdf(c.labels(ids, "avery5160"))
	if got := perPage(pages); fmt.Sprint(got) != "[30 1]" {
		t.Errorf("5160 labels per page %v", got)
	}
	pages = c.pdf(c.labels(ids, "averyl7160"))
	if got := perPage(pages); fmt.Sprint(got) != "[21 10]" || !near1(pages[0].Width, 210) || !near1(pages[0].Height, 297) {
		t.Errorf("L7160 labels per page %v, page %.1f x %.1f", got, pages[0].Width, pages[0].Height)
	}
	if got := perPage(c.pdf(c.labels(ids, "single"))); len(got) != 31 {
		t.Errorf("single: %d pages", len(got))
	}
}

func TestLabelRequestsAreChecked(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 2)
	cps, _ := c.cat.CopiesOf(ctxBG, b.ID)
	id := cps[0].ID
	if err := c.cat.RemoveCopy(ctxBG, 1, cps[1].ID); err != nil {
		t.Fatal(err)
	}
	many := make([]int64, 5001)
	for i := range many {
		many[i] = id
	}
	cases := map[string]struct {
		ids    []int64
		layout string
		status int
	}{
		"unknown layout": {[]int64{id}, "avery9999", 422},
		"no layout":      {[]int64{id}, "", 422},
		"no copies":      {nil, "single", 422},
		"too many":       {many, "avery5160", 422},
		"unknown copy":   {[]int64{id, 999}, "single", 404},
		"removed copy":   {[]int64{cps[1].ID}, "single", 404},
	}
	for name, tc := range cases {
		if rec := c.labels(tc.ids, tc.layout); rec.Code != tc.status || rec.Header().Get("Content-Type") == "application/pdf" {
			t.Errorf("%s: %d %.150s", name, rec.Code, rec.Body)
		}
	}
	// A code too long to scan on a small label is refused with a message, not a 500.
	c.mustSetMode("own")
	long := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1, Sticker: strings.Repeat("W", 17)}), 201)
	if rec := c.labels([]int64{long.ID}, "single"); rec.Code != 422 || !strings.Contains(errMessage(t, rec), "scannable") {
		t.Errorf("long code: %d %s", rec.Code, rec.Body)
	}
	if rec := c.labels([]int64{long.ID}, "avery5160"); rec.Code != 200 {
		t.Errorf("long code on a sheet: %d %s", rec.Code, rec.Body)
	}
}

// CAT-9 "Default": a fresh installation asks for no barcode and shows copy 2.
func TestCAT9_Default(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	made := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1}), 201)
	if made.Number != 2 || made.Barcode != "" {
		t.Errorf("%+v", made)
	}
	if got := c.bookPage(b.ID).Copies; len(got) != 2 || got[1].Number != 2 {
		t.Errorf("%+v", got)
	}
	if mode := decodeAs[adminSettingsBody](t, c.req("GET", "/api/admin/settings", c.admin, ""), 200).CopyIDMode; mode != "isbn" {
		t.Errorf("mode %q", mode)
	}
}

// CAT-9 "No ISBN": in by-ISBN mode a copy of a book with no ISBN gets its own barcode.
func TestCAT9_NoISBN(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "Old Almanac", "", 0)
	made := decodeAs[staffCopyBody](t, c.addCopy(b.ID, copyForm{LocationID: 1}), 201)
	if made.Barcode == "" {
		t.Errorf("%+v", made)
	}
}

// CAT-9 "Switch to own barcodes": every copy gets a unique barcode, open loans
// are unchanged, and the change is in the activity log.
func TestCAT9_SwitchToOwnBarcodes(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 3)
	c.checkout("9780306406157")
	before := c.count("loans")
	var due string
	_ = c.st.DB().QueryRow(`SELECT due_date FROM loans`).Scan(&due)
	if rec := c.setMode("own"); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	seen := map[string]bool{}
	for _, cp := range c.cats(b.ID) {
		if cp.Code == "" || seen[cp.Code] {
			t.Errorf("copy %d has barcode %q", cp.Number, cp.Code)
		}
		seen[cp.Code] = true
	}
	var due2 string
	_ = c.st.DB().QueryRow(`SELECT due_date FROM loans WHERE returned_at IS NULL`).Scan(&due2)
	if len(seen) != 3 || c.count("loans") != before || due2 != due {
		t.Errorf("%d barcodes, %d loans, due %s -> %s", len(seen), c.count("loans"), due, due2)
	}
	if got := decodeAs[adminSettingsBody](t, c.req("GET", "/api/admin/settings", c.admin, ""), 200).CopyIDMode; got != "own" {
		t.Errorf("mode %q", got)
	}
	log := c.req("GET", "/api/admin/activity", c.admin, "").Body.String()
	if c.activityCount() != 1 || !strings.Contains(log, "settings.changed") || !strings.Contains(log, `"own"`) || !strings.Contains(log, `"isbn"`) {
		t.Errorf("%d log entries: %s", c.activityCount(), log)
	}
}

func (c *copySite) cats(bookID int64) []catalog.Copy {
	cps, err := c.cat.CopiesOf(ctxBG, bookID)
	if err != nil {
		c.t.Fatal(err)
	}
	return cps
}

// CAT-9 "Switch back": copies keep their barcodes, a barcode still finds its
// copy and the ISBN finds the book.
func TestCAT9_SwitchBack(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 2)
	c.mustSetMode("own")
	before := c.cats(b.ID)
	c.mustSetMode("isbn")
	for i, cp := range c.cats(b.ID) {
		if cp.Code == "" || cp.Code != before[i].Code {
			t.Errorf("copy %d: %q, was %q", cp.Number, cp.Code, before[i].Code)
		}
	}
	scan := func(code string) resolveResponse {
		return decodeAs[resolveResponse](t, c.req("POST", "/api/desk/resolve", c.lib, body(map[string]string{"code": code, "step": "item"})), 200)
	}
	if r := scan(before[1].Code); r.Kind != "copy" || r.Copy.Number != 2 {
		t.Errorf("barcode: %+v", r)
	}
	if r := scan("9780306406157"); r.Kind != "book" || r.Book.ID != b.ID {
		t.Errorf("ISBN: %+v", r)
	}
}

// CAT-9 "ISBN scanned in own-barcode mode": the librarian is told to scan the copy's own barcode.
func TestCAT9_ISBNScannedInOwnMode(t *testing.T) {
	c := newCopySite(t)
	addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	c.mustSetMode("own")
	rec := c.req("POST", "/api/desk/resolve", c.lib, `{"code":"9780306406157","step":"item"}`)
	if rec.Code != 422 || !strings.Contains(errMessage(t, rec), "own barcode") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// A mode that isn't one is refused and nothing is logged; an unchanged mode is harmless.
func TestCopyIDModeIsChecked(t *testing.T) {
	c := newCopySite(t)
	b := addBook(t, c.cat, "The Hobbit", "9780306406157", 1)
	for _, in := range []string{`{"mode":"both"}`, `{"mode":""}`, `{}`, `{"mode":"own","x":1}`, `[]`} {
		if rec := c.req("PUT", "/api/admin/copy-id-mode", c.admin, in); rec.Code != 422 && rec.Code != 400 {
			t.Errorf("%s: %d %s", in, rec.Code, rec.Body)
		}
	}
	if c.activityCount() != 0 || c.set.Get().CopyIDMode != "isbn" {
		t.Errorf("%d log entries, mode %s", c.activityCount(), c.set.Get().CopyIDMode)
	}
	c.mustSetMode("isbn")
	if got := c.cats(b.ID); got[0].Code != "" {
		t.Errorf("staying in by-ISBN mode gave a copy a barcode: %q", got[0].Code)
	}
}

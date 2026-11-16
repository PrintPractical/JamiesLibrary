package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/circ"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
)

// deskSite is a site with circulation behind the desk routes.
func deskSite(t *testing.T) (*site, *catalog.Service) {
	t.Helper()
	var cat *catalog.Service
	s := newSite(t, func(o *Options) {
		cat = catalog.New(o.Store, catalog.Options{Settings: o.Settings, Loans: circ.Loans{}, Now: clock.Now})
		o.Circ = circ.New(o.Store, circ.Options{Settings: o.Settings, Catalog: cat, Users: o.Users})
	})
	return s, cat
}

func addBook(t *testing.T, cat *catalog.Service, title, isbn string, copies int) catalog.Book {
	t.Helper()
	b, err := cat.CreateBook(ctxBG, 1, catalog.BookInput{Title: title, Authors: []string{"A. Author"}, ISBN: isbn})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < copies; i++ {
		if _, err := cat.AddCopy(ctxBG, 1, b.ID, catalog.CopyInput{LocationID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func body(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeAs[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	var v T
	if rec.Code != status || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("status %d (want %d): %s", rec.Code, status, rec.Body)
	}
	return v
}

// Every desk route needs circ.desk: a caller without it gets 403, nobody signed in gets 401, and nothing changes.
func TestDeskRoutesNeedThePermission(t *testing.T) {
	s, cat := deskSite(t)
	addBook(t, cat, "The Hobbit", "9780306406157", 1)
	s.addUser("lib", goodPassword, perm.Librarian)
	s.addUser("pat", goodPassword, perm.Patron)
	patCookie := s.signIn("pat", goodPassword)
	card := s.cardOf(patCookie)
	for _, c := range []struct{ path, body string }{
		{"/api/desk/resolve", body(map[string]string{"code": card, "step": "patron"})},
		{"/api/desk/checkout", body(map[string]string{"patron": card, "code": "9780306406157"})},
		{"/api/desk/checkin", body(map[string]string{"code": "9780306406157"})},
	} {
		if rec := s.req("POST", c.path, patCookie, c.body); rec.Code != 403 {
			t.Errorf("%s as a patron: %d", c.path, rec.Code)
		}
		if rec := s.req("POST", c.path, "", c.body); rec.Code != 401 {
			t.Errorf("%s signed out: %d", c.path, rec.Code)
		}
	}
	var n int
	_ = s.st.DB().QueryRow(`SELECT count(*) FROM loans`).Scan(&n)
	if n != 0 {
		t.Errorf("%d loans were made", n)
	}
}

// SCAN-4, SCAN-3 and CAT-9 through the API: the desk finds a patron by card, a book by ISBN, a copy by its barcode.
func TestDeskResolve(t *testing.T) {
	s, cat := deskSite(t)
	b := addBook(t, cat, "The Hobbit", "9780306406157", 2)
	s.addUser("lib", goodPassword, perm.Librarian)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	lib := s.signIn("lib", goodPassword)
	card, _ := s.users.Card(ctxBG, pat.ID)
	resolve := func(code, step string) *httptest.ResponseRecorder {
		return s.req("POST", "/api/desk/resolve", lib, body(map[string]string{"code": code, "step": step}))
	}

	r := decodeAs[resolveResponse](t, resolve(card, "patron"), 200)
	if r.Kind != "patron" || r.Patron.ID != pat.ID || r.Patron.DisplayName != "pat" || r.Patron.Limit != 5 || !r.Patron.Active {
		t.Errorf("patron: %+v", r.Patron)
	}
	if rec := resolve("999999999999", "patron"); rec.Code != 404 || !strings.Contains(errMessage(t, rec), "No such patron") {
		t.Errorf("unknown card: %d %s", rec.Code, rec.Body)
	}
	r = decodeAs[resolveResponse](t, resolve("978-0-306-40615-7", "item"), 200)
	if r.Kind != "book" || r.Book.ID != b.ID || r.Book.Available != 2 {
		t.Errorf("book: %+v", r.Book)
	}
	if rec := resolve("nothing-here", "item"); rec.Code != 404 || !strings.Contains(errMessage(t, rec), "Nothing matches") {
		t.Errorf("unknown code: %d %s", rec.Code, rec.Body)
	}
	if rec := resolve(card, "item"); rec.Code != 404 {
		t.Errorf("a card at the item step: %d", rec.Code)
	}
	if err := cat.SetCopyIDMode(ctxBG, 1, settings.CopyIDOwn, nil); err != nil {
		t.Fatal(err)
	}
	if rec := resolve("9780306406157", "item"); rec.Code != 422 || !strings.Contains(errMessage(t, rec), "own barcode") {
		t.Errorf("ISBN in own mode: %d %s", rec.Code, rec.Body)
	}
	copies, _ := cat.CopiesOf(ctxBG, b.ID)
	r = decodeAs[resolveResponse](t, resolve(copies[1].Code, "item"), 200)
	if r.Kind != "copy" || r.Copy.Number != 2 || r.Copy.Code != copies[1].Code {
		t.Errorf("copy: %+v", r.Copy)
	}
	if rec := s.req("POST", "/api/desk/resolve", lib, `{"code":"x","step":"patron","extra":1}`); rec.Code == 200 {
		t.Error("an unknown field was accepted")
	}
}

// CIRC-1, CIRC-2, CIRC-4 and ADMIN-2 through the API, with the messages a librarian acts on.
func TestDeskCheckoutAndCheckin(t *testing.T) {
	s, cat := deskSite(t)
	addBook(t, cat, "The Hobbit", "9780306406157", 2)
	s.addUser("lib", goodPassword, perm.Librarian)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	sam := s.addUser("sam", goodPassword, perm.Patron)
	lib := s.signIn("lib", goodPassword)
	card, _ := s.users.Card(ctxBG, pat.ID)
	samCard, _ := s.users.Card(ctxBG, sam.ID)
	if err := s.set.SetMaxLoans(ctxBG, 2); err != nil {
		t.Fatal(err)
	}
	out := func(patron, code string) *httptest.ResponseRecorder {
		return s.req("POST", "/api/desk/checkout", lib, body(map[string]string{"patron": patron, "code": code}))
	}
	in := func(v map[string]any) *httptest.ResponseRecorder {
		return s.req("POST", "/api/desk/checkin", lib, body(v))
	}

	l := decodeAs[deskLoanBody](t, out(card, "9780306406157"), 200)
	if l.CopyNumber != 1 || l.DueDate != "2026-01-29" || l.PatronName != "pat" || l.Title != "The Hobbit" {
		t.Errorf("loan: %+v", l)
	}
	l2 := decodeAs[deskLoanBody](t, out(samCard, "9780306406157"), 200)
	if l2.CopyNumber != 2 {
		t.Errorf("second loan: %+v", l2)
	}
	if rec := out(card, "9780306406157"); rec.Code != 409 || !strings.Contains(errMessage(t, rec), "No copy of The Hobbit is available") {
		t.Errorf("none left: %d %s", rec.Code, rec.Body)
	}
	if rec := out("000000000000", "9780306406157"); rec.Code != 404 {
		t.Errorf("unknown patron: %d", rec.Code)
	}

	c := decodeAs[checkinResponse](t, in(map[string]any{"code": "9780306406157"}), 200)
	if c.Status != "choose" || len(c.Choices) != 2 || c.Choices[0].PatronName != "pat" || c.Choices[1].CopyNumber != 2 {
		t.Fatalf("choose: %+v", c)
	}
	c = decodeAs[checkinResponse](t, in(map[string]any{"code": "9780306406157", "loan_id": l2.ID}), 200)
	if c.Status != "returned" || c.Loan.ID != l2.ID || c.Loan.PatronName != "sam" {
		t.Errorf("by choice: %+v", c)
	}
	// the limit: pat has 1 of 2, takes the second copy, then is at the limit
	decodeAs[deskLoanBody](t, out(card, "9780306406157"), 200)
	addBook(t, cat, "Dune", "9780140328721", 1)
	if rec := out(card, "9780140328721"); rec.Code != 409 || !strings.Contains(errMessage(t, rec), "Limit reached") {
		t.Errorf("limit: %d %s", rec.Code, rec.Body)
	}
	c = decodeAs[checkinResponse](t, in(map[string]any{"code": "9780306406157", "card": card, "loan_id": 0}), 200)
	if c.Status != "choose" || len(c.Choices) != 2 {
		t.Errorf("one patron, two copies: %+v", c)
	}
	c = decodeAs[checkinResponse](t, in(map[string]any{"code": "9780140328721"}), 200)
	if c.Status != "not_out" || !strings.Contains(c.Message, "None is checked out") {
		t.Errorf("nothing out: %+v", c)
	}

	// ADMIN-2: a deactivated patron can't borrow, and the loan stays visible
	s.addUser("ada", goodPassword, perm.Administrator)
	ada := s.signIn("ada", goodPassword)
	if rec := s.req("POST", path(pat.ID, "deactivate"), ada, `{}`); rec.Code != 200 {
		t.Fatalf("deactivate: %d %s", rec.Code, rec.Body)
	}
	if rec := out(card, "9780140328721"); rec.Code != 409 || !strings.Contains(errMessage(t, rec), "deactivated") {
		t.Errorf("deactivated patron: %d %s", rec.Code, rec.Body)
	}
	r := decodeAs[resolveResponse](t, s.req("POST", "/api/desk/resolve", lib, body(map[string]string{"code": card, "step": "patron"})), 200)
	if r.Patron.Active || r.Patron.Out != 2 {
		t.Errorf("patron after deactivation: %+v", r.Patron)
	}
	if rec := in(map[string]any{"code": "x", "loan_id": "nope"}); rec.Code == 200 {
		t.Error("a bad body was accepted")
	}
}

type findAnswer struct {
	Kind    string `json:"kind"`
	Patron  *deskPatronBody
	Patrons []patronMatchBody `json:"patrons"`
	More    *bool             `json:"more"`
}

// CIRC-8 through the API: a card selects its holder as resolve does; a name lists matches, whoever they are.
func TestDeskFind(t *testing.T) {
	s, _ := deskSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	lib := s.signIn("lib", goodPassword)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	card, _ := s.users.Card(ctxBG, pat.ID)
	off := s.addUser("patoff", goodPassword, perm.Patron)
	s.exec(`UPDATE users SET active = 0 WHERE id = ?`, off.ID)
	none := s.addUser("patnone", goodPassword, perm.Patron)
	s.exec(`DELETE FROM identifiers WHERE user_id = ?`, none.ID)
	s.addUser("patstaff", goodPassword, perm.Administrator)
	find := func(q string, cookie string) *httptest.ResponseRecorder {
		return s.req("POST", "/api/desk/find", cookie, body(map[string]string{"q": q}))
	}

	got := decodeAs[findAnswer](t, find(card, lib), 200)
	want := decodeAs[resolveResponse](t, s.req("POST", "/api/desk/resolve", lib, body(map[string]string{"code": card, "step": "patron"})), 200)
	if got.Kind != "patron" || got.Patron == nil || *got.Patron != *want.Patron || got.Patron.ID != pat.ID || got.Patrons != nil {
		t.Errorf("card: %+v", got)
	}
	got = decodeAs[findAnswer](t, find("PAT", lib), 200)
	if got.Kind != "matches" || got.Patron != nil || len(got.Patrons) != 4 || got.More == nil || *got.More {
		t.Fatalf("name: %s", find("PAT", lib).Body)
	}
	byName := map[string]patronMatchBody{}
	for _, p := range got.Patrons {
		byName[p.Username] = p
	}
	if p := byName["patoff"]; p.Active || p.Card == "" {
		t.Errorf("deactivated: %+v", p)
	}
	if p := byName["patnone"]; !p.Active || p.Card != "" {
		t.Errorf("no card: %+v", p)
	}
	if p := byName["patstaff"]; !p.Active || p.Card == "" {
		t.Errorf("staff: %+v", p)
	}
	if p := byName["pat"]; p.Card != card || p.ID != pat.ID {
		t.Errorf("patron: %+v", p)
	}
	// no match is an empty list, not an error, and the list is `[]` in the body
	rec := find("zzz", lib)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"patrons":[]`) || !strings.Contains(rec.Body.String(), `"more":false`) {
		t.Errorf("no match: %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"", "  ", "a\u0007b", strings.Repeat("x", 201)} {
		if rec := find(q, lib); rec.Code != 422 || !strings.Contains(rec.Body.String(), `"q"`) {
			t.Errorf("%q: %d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := s.req("POST", "/api/desk/find", lib, `{"q":"x","extra":1}`); rec.Code == 200 {
		t.Error("an unknown field was accepted")
	}
	if s.logTotal() != 0 {
		t.Errorf("a look-up was logged: %d entries", s.logTotal())
	}
}

// CIRC-8 "Over 50": exactly 50 say nothing more; 51 give 50 and more=true.
func TestDeskFindLimit(t *testing.T) {
	s, _ := deskSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	lib := s.signIn("lib", goodPassword)
	for i := 0; i < 50; i++ {
		s.addUser(fmt.Sprintf("many%02d", i), goodPassword, perm.Patron)
	}
	got := decodeAs[findAnswer](t, s.req("POST", "/api/desk/find", lib, `{"q":"many"}`), 200)
	if len(got.Patrons) != 50 || *got.More {
		t.Errorf("50: %d %v", len(got.Patrons), *got.More)
	}
	s.addUser("many50", goodPassword, perm.Patron)
	got = decodeAs[findAnswer](t, s.req("POST", "/api/desk/find", lib, `{"q":"many"}`), 200)
	if len(got.Patrons) != 50 || !*got.More {
		t.Errorf("51: %d %v", len(got.Patrons), *got.More)
	}
}

// CIRC-8: circ.desk alone is enough (a patron granted it has no patrons.lookup) and answers as a librarian does; without circ.desk, refused.
func TestDeskFindNeedsCircDeskOnly(t *testing.T) {
	s, _ := deskSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	lib := s.signIn("lib", goodPassword)
	clerk := s.addUser("clerk", goodPassword, perm.Patron)
	if err := s.setPerm(clerk.ID, perm.CircDesk); err != nil {
		t.Fatal(err)
	}
	s.addUser("pat", goodPassword, perm.Patron)
	pat := s.signIn("pat", goodPassword)
	cl := s.signIn("clerk", goodPassword)
	for _, path := range []string{"/api/patrons?q=pat", "/api/patrons/1", "/api/patron-loans/1", "/api/patrons/by-card/" + s.cardOf(pat)} {
		if rec := s.req("GET", path, cl, ""); rec.Code != 403 {
			t.Fatalf("the clerk reached %s: %d", path, rec.Code)
		}
	}
	a, b := s.req("POST", "/api/desk/find", cl, `{"q":"pat"}`), s.req("POST", "/api/desk/find", lib, `{"q":"pat"}`)
	if a.Code != 200 || a.Body.String() != b.Body.String() {
		t.Errorf("clerk %d %s, librarian %d %s", a.Code, a.Body, b.Code, b.Body)
	}
	if rec := s.req("POST", "/api/desk/find", pat, `{"q":"pat"}`); rec.Code != 403 {
		t.Errorf("a patron: %d", rec.Code)
	}
	if rec := s.req("POST", "/api/desk/find", "", `{"q":"pat"}`); rec.Code != 401 {
		t.Errorf("signed out: %d", rec.Code)
	}
}

func keysOf(t *testing.T, m map[string]any, allowed ...string) {
	t.Helper()
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	for k := range m {
		if !ok[k] {
			t.Errorf("unexpected key %q in %v", k, m)
		}
	}
}

// security-design-r4: the answers carry only the allowed keys, so a field added for patrons.lookup can't reach circ.desk holders.
func TestDeskFindAnswersOnlyAllowedKeys(t *testing.T) {
	s, _ := deskSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	lib := s.signIn("lib", goodPassword)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	card, _ := s.users.Card(ctxBG, pat.ID)
	decode := func(q string) map[string]any {
		var m map[string]any
		rec := s.req("POST", "/api/desk/find", lib, body(map[string]string{"q": q}))
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return m
	}
	m := decode(card)
	keysOf(t, m, "kind", "patron")
	keysOf(t, m["patron"].(map[string]any), "id", "display_name", "card", "active", "out", "overdue", "limit")
	m = decode("pat")
	keysOf(t, m, "kind", "patrons", "more")
	list := m["patrons"].([]any)
	if len(list) == 0 {
		t.Fatal("no matches")
	}
	for _, p := range list {
		keysOf(t, p.(map[string]any), "id", "display_name", "username", "card", "active", "out", "overdue")
	}
}

// A card matches only whole; a prefix or an interior part finds no one; hostile input behaves; 60 give 50 and more.
func TestDeskFindCardAndHostileInput(t *testing.T) {
	s, _ := deskSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	lib := s.signIn("lib", goodPassword)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	card, _ := s.users.Card(ctxBG, pat.ID)
	s.addUser("back", goodPassword, perm.Patron)
	s.exec(`UPDATE users SET display_name = ? WHERE username = 'back'`, `Back\slash 😀`)
	find := func(q string) *httptest.ResponseRecorder {
		return s.req("POST", "/api/desk/find", lib, body(map[string]string{"q": q}))
	}
	for _, q := range []string{card[:len(card)-1], card[:6], card[3:9], card[1 : len(card)-1]} {
		if got := decodeAs[findAnswer](t, find(q), 200); got.Kind != "matches" || len(got.Patrons) != 0 {
			t.Errorf("%q: %+v", q, got)
		}
	}
	if got := decodeAs[findAnswer](t, find(card), 200); got.Kind != "patron" || got.Patron.ID != pat.ID {
		t.Errorf("whole card: %+v", got)
	}
	for _, q := range []string{`\`, "😀"} {
		if got := decodeAs[findAnswer](t, find(q), 200); len(got.Patrons) != 1 || got.Patrons[0].Username != "back" {
			t.Errorf("%q: %+v", q, got)
		}
	}
	if rec := find(strings.Repeat("😀", 200)); rec.Code != 200 {
		t.Errorf("200 code points: %d %s", rec.Code, rec.Body)
	}
	if rec := find(strings.Repeat("😀", 201)); rec.Code != 422 {
		t.Errorf("201 code points: %d", rec.Code)
	}
	if rec := find("‏‮"); rec.Code != 200 {
		t.Errorf("RTL marks: %d", rec.Code)
	}
	for i := 0; i < 60; i++ {
		s.addUser(fmt.Sprintf("sixty%02d", i), goodPassword, perm.Patron)
	}
	if got := decodeAs[findAnswer](t, find("sixty"), 200); len(got.Patrons) != 50 || !*got.More {
		t.Errorf("60: %d", len(got.Patrons))
	}
}

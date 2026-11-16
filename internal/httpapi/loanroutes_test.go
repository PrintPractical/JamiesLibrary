package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
)

// loanSite is a desk site with a librarian, and patrons ann and bob, signed in.
type loanSite struct {
	*site
	lib, ann, bob    string
	annID, bobID     int64
	annCard, bobCard string
	isbn             string
	catalog          *catalog.Service
}

func newLoanSite(t *testing.T) *loanSite {
	s, cat := deskSite(t)
	addBook(t, cat, "The Hobbit", "9780306406157", 2)
	addBook(t, cat, "Dune", "9780140328721", 1)
	s.addUser("lib", goodPassword, perm.Librarian)
	a := s.addUser("ann", goodPassword, perm.Patron)
	b := s.addUser("bob", goodPassword, perm.Patron)
	if err := s.set.SetMaxRenewals(ctxBG, 1); err != nil {
		t.Fatal(err)
	}
	ls := &loanSite{site: s, lib: s.signIn("lib", goodPassword), ann: s.signIn("ann", goodPassword), bob: s.signIn("bob", goodPassword),
		annID: a.ID, bobID: b.ID, isbn: "9780306406157", catalog: cat}
	ls.annCard, _ = s.users.Card(ctxBG, a.ID)
	ls.bobCard, _ = s.users.Card(ctxBG, b.ID)
	return ls
}

func (s *loanSite) lend(card, code string) deskLoanBody {
	s.t.Helper()
	return decodeAs[deskLoanBody](s.t, s.req("POST", "/api/desk/checkout", s.lib, body(map[string]string{"patron": card, "code": code})), 200)
}

// at moves the clock and signs everyone in again: a day later their sessions have gone idle.
func (s *loanSite) at(t time.Time) {
	s.now = t
	s.lib, s.ann, s.bob = s.signIn("lib", goodPassword), s.signIn("ann", goodPassword), s.signIn("bob", goodPassword)
}

type ownLoans struct {
	Loans []deskLoanBody `json:"loans"`
}

// Every route here refuses a caller without its permission, with nothing changed (ROLE-3).
func TestLoanRoutesNeedThePermission(t *testing.T) {
	s := newLoanSite(t)
	l := s.lend(s.annCard, s.isbn)
	if err := s.set.SetSelfCheckout(ctxBG, true); err != nil {
		t.Fatal(err)
	}
	// a patron without the own-loan permissions, and a librarian without desk or lookup
	bare := s.addUser("bare", goodPassword, perm.Patron)
	for _, p := range []perm.Permission{perm.LoansOwnView, perm.LoansOwnRenew, perm.SelfCheckout} {
		if _, err := s.users.SetPermission(ctxBG, bare.ID, p, false); err != nil {
			t.Fatal(err)
		}
	}
	bareCookie := s.signIn("bare", goodPassword)
	id := fmt.Sprint(l.ID)
	for _, c := range []struct{ method, path, cookie, body string }{
		{"GET", "/api/me/loans", bareCookie, ""},
		{"GET", "/api/me/loans/history", bareCookie, ""},
		{"POST", "/api/me/loans/" + id + "/renew", bareCookie, ""},
		{"POST", "/api/me/checkout", bareCookie, body(map[string]string{"code": s.isbn})},
		{"POST", "/api/desk/loans/" + id + "/renew", s.ann, ""},
		{"GET", "/api/desk/overdue", s.ann, ""},
		{"GET", "/api/patrons?q=ann", s.ann, ""},
		{"GET", fmt.Sprintf("/api/patrons/%d", s.annID), s.ann, ""},
		{"GET", fmt.Sprintf("/api/patron-loans/%d", s.annID), s.ann, ""},
	} {
		if rec := s.req(c.method, c.path, c.cookie, c.body); rec.Code != 403 {
			t.Errorf("%s %s without the permission: %d", c.method, c.path, rec.Code)
		}
		if rec := s.req(c.method, c.path, "", c.body); rec.Code != 401 {
			t.Errorf("%s %s signed out: %d", c.method, c.path, rec.Code)
		}
	}
	var renewals, n int
	_ = s.st.DB().QueryRow(`SELECT sum(renewals), count(*) FROM loans`).Scan(&renewals, &n)
	if renewals != 0 || n != 1 {
		t.Errorf("%d renewals, %d loans", renewals, n)
	}
}

// PATRON-1 "Dashboard": the loan and its due date are shown, and only the caller's own.
func TestOwnLoans(t *testing.T) {
	s := newLoanSite(t)
	s.lend(s.annCard, s.isbn)
	s.lend(s.bobCard, "9780140328721")
	got := decodeAs[ownLoans](t, s.req("GET", "/api/me/loans", s.ann, ""), 200)
	if len(got.Loans) != 1 || got.Loans[0].Title != "The Hobbit" || got.Loans[0].DueDate != "2026-01-29" || got.Loans[0].Overdue {
		t.Errorf("dashboard: %+v", got)
	}
	s.at(s.now.AddDate(0, 0, 20))
	got = decodeAs[ownLoans](t, s.req("GET", "/api/me/loans", s.ann, ""), 200)
	if !got.Loans[0].Overdue || got.Loans[0].DaysLate != 6 {
		t.Errorf("overdue flag: %+v", got.Loans[0])
	}
	// history: returned loans too, a page at a time
	s.req("POST", "/api/desk/checkin", s.lib, body(map[string]string{"code": s.isbn}))
	s.lend(s.annCard, s.isbn)
	h := decodeAs[loanPageBody](t, s.req("GET", "/api/me/loans/history?per_page=1", s.ann, ""), 200)
	if h.Total != 2 || len(h.Loans) != 1 || h.Page != 1 || h.PerPage != 1 || h.Loans[0].ReturnedAt != "" {
		t.Errorf("history page 1: %+v", h)
	}
	h = decodeAs[loanPageBody](t, s.req("GET", "/api/me/loans/history?per_page=1&page=2", s.ann, ""), 200)
	if len(h.Loans) != 1 || h.Loans[0].ReturnedAt == "" {
		t.Errorf("history page 2: %+v", h)
	}
	for _, q := range []string{"page=0", "per_page=1000", "page=x", "per_page=-1"} {
		if rec := s.req("GET", "/api/me/loans/history?"+q, s.ann, ""); rec.Code != 422 {
			t.Errorf("?%s: %d", q, rec.Code)
		}
	}
}

// CIRC-5 "Renew", "Over limit", "Overdue loan" through the API, and patron A can't renew patron B's loan by id.
func TestRenewOwnLoan(t *testing.T) {
	s := newLoanSite(t)
	l := s.lend(s.annCard, s.isbn)
	path := fmt.Sprintf("/api/me/loans/%d/renew", l.ID)
	if rec := s.req("POST", path, s.bob, ""); rec.Code != 404 {
		t.Errorf("another patron's loan: %d %s", rec.Code, rec.Body)
	}
	if rec := s.req("POST", "/api/me/loans/999/renew", s.bob, ""); rec.Code != 404 {
		t.Errorf("no such loan: %d", rec.Code)
	}
	if rec := s.req("POST", "/api/me/loans/x/renew", s.ann, ""); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
	got := decodeAs[deskLoanBody](t, s.req("POST", path, s.ann, ""), 200)
	if got.DueDate != "2026-02-12" || got.Renewals != 1 {
		t.Errorf("renewed: %+v", got)
	}
	if rec := s.req("POST", path, s.ann, ""); rec.Code != 409 || !strings.Contains(errMessage(t, rec), "most times allowed") {
		t.Errorf("over limit: %d %s", rec.Code, rec.Body)
	}
	s.at(time.Date(2026, 2, 13, 12, 0, 0, 0, time.UTC))
	if rec := s.req("POST", path, s.ann, ""); rec.Code != 409 {
		t.Errorf("overdue: %d", rec.Code)
	}
	s.set.SetMaxRenewals(ctxBG, 5)
	if rec := s.req("POST", path, s.ann, ""); rec.Code != 409 || !strings.Contains(errMessage(t, rec), "overdue") {
		t.Errorf("overdue and under the limit: %d %s", rec.Code, rec.Body)
	}
}

// CIRC-5 "Librarian renews": the desk renews anyone's loan; CIRC-6 and CIRC-3 through the API.
func TestDeskRenewOverdueAndLookup(t *testing.T) {
	s := newLoanSite(t)
	l := s.lend(s.annCard, s.isbn)
	s.lend(s.annCard, "9780140328721")
	got := decodeAs[deskLoanBody](t, s.req("POST", fmt.Sprintf("/api/desk/loans/%d/renew", l.ID), s.lib, ""), 200)
	if got.DueDate != "2026-02-12" || got.PatronID != s.annID {
		t.Errorf("renewed: %+v", got)
	}
	if rec := s.req("POST", "/api/desk/loans/999/renew", s.lib, ""); rec.Code != 404 {
		t.Errorf("no such loan: %d", rec.Code)
	}

	// CIRC-6 "Overdue list"
	s.at(time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)) // Dune's loan (due 29 Jan) is 3 days overdue; The Hobbit's (12 Feb) is not
	od := decodeAs[loanPageBody](t, s.req("GET", "/api/desk/overdue", s.lib, ""), 200)
	if od.Total != 1 || od.Loans[0].Title != "Dune" || od.Loans[0].PatronName != "ann" || od.Loans[0].DaysLate != 3 {
		t.Errorf("overdue list: %+v", od)
	}

	// CIRC-3 "Lookup": a patron with two loans, by name and by card
	found := decodeAs[struct {
		Patrons []patronMatchBody `json:"patrons"`
	}](t, s.req("GET", "/api/patrons?q=AN", s.lib, ""), 200)
	if len(found.Patrons) != 1 || found.Patrons[0].ID != s.annID || found.Patrons[0].Out != 2 || found.Patrons[0].Overdue != 1 || found.Patrons[0].Card != s.annCard {
		t.Errorf("by name: %+v", found)
	}
	found = decodeAs[struct {
		Patrons []patronMatchBody `json:"patrons"`
	}](t, s.req("GET", "/api/patrons?q="+s.annCard, s.lib, ""), 200)
	if len(found.Patrons) != 1 || found.Patrons[0].ID != s.annID {
		t.Errorf("by card: %+v", found)
	}
	rec := decodeAs[struct {
		Patron  patronMatchBody `json:"patron"`
		Current []deskLoanBody  `json:"current"`
		Overdue []deskLoanBody  `json:"overdue"`
	}](t, s.req("GET", fmt.Sprintf("/api/patrons/%d", s.annID), s.lib, ""), 200)
	if len(rec.Current) != 2 || len(rec.Overdue) != 1 || rec.Overdue[0].Title != "Dune" || rec.Patron.DisplayName != "ann" {
		t.Errorf("record: %+v", rec)
	}
	h := decodeAs[loanPageBody](t, s.req("GET", fmt.Sprintf("/api/patron-loans/%d", s.annID), s.lib, ""), 200)
	if h.Total != 2 {
		t.Errorf("history: %+v", h)
	}
	for _, p := range []string{"/api/patrons?q=", "/api/patrons?q=" + strings.Repeat("a", 201), "/api/patrons"} {
		if r := s.req("GET", p, s.lib, ""); r.Code != 422 {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
	for _, p := range []string{"/api/patrons/999", "/api/patrons/x", "/api/patrons/0"} {
		if r := s.req("GET", p, s.lib, ""); r.Code != 404 {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
}

// CIRC-7 through the API: "Off", "On", "On, by ISBN" and "No copy available".
func TestSelfCheckoutRoute(t *testing.T) {
	s := newLoanSite(t)
	co := func(cookie, code string) *httptest.ResponseRecorder {
		return s.req("POST", "/api/me/checkout", cookie, body(map[string]string{"code": code}))
	}
	if rec := co(s.ann, s.isbn); rec.Code != 403 || !strings.Contains(errMessage(t, rec), "switched off") {
		t.Errorf("off: %d %s", rec.Code, rec.Body)
	}
	if err := s.set.SetSelfCheckout(ctxBG, true); err != nil {
		t.Fatal(err)
	}
	l := decodeAs[deskLoanBody](t, co(s.ann, s.isbn), 200) // by ISBN
	if l.PatronID != s.annID || l.CopyNumber != 1 || l.DueDate != "2026-01-29" {
		t.Errorf("by ISBN: %+v", l)
	}
	l = decodeAs[deskLoanBody](t, co(s.bob, s.isbn), 200)
	if l.PatronID != s.bobID || l.CopyNumber != 2 {
		t.Errorf("second copy: %+v", l)
	}
	if rec := co(s.bob, s.isbn); rec.Code != 409 || !strings.Contains(errMessage(t, rec), "No copy of The Hobbit is available") {
		t.Errorf("none available: %d %s", rec.Code, rec.Body)
	}
	// the loan is the caller's whatever the body says
	if rec := s.req("POST", "/api/me/checkout", s.ann, `{"code":"9780140328721","patron":"`+s.bobCard+`"}`); rec.Code == 200 {
		t.Error("a patron field was accepted")
	}
	if err := s.catalog.SetCopyIDMode(ctxBG, 1, settings.CopyIDOwn, nil); err != nil {
		t.Fatal(err)
	}
	if rec := co(s.ann, "9780140328721"); rec.Code != 422 {
		t.Errorf("ISBN in own mode: %d", rec.Code)
	}
	var n int
	_ = s.st.DB().QueryRow(`SELECT count(*) FROM loans WHERE patron_id = ?`, s.annID).Scan(&n)
	if n != 1 {
		t.Errorf("ann has %d loans", n)
	}
}

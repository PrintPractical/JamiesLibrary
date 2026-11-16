package circ

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

// lend checks isbn out to a card and fails the test if it can't.
func (r *rig) lend(card, code string) Loan {
	r.t.Helper()
	r.desks++
	lib, _ := r.user(fmt.Sprintf("desk%d", r.desks), perm.Librarian)
	l, err := r.svc.Checkout(ctx, lib.ID, card, code)
	if err != nil {
		r.t.Fatal(err)
	}
	return l
}

func (r *rig) maxRenewals(n int) {
	r.t.Helper()
	if err := r.set.SetMaxRenewals(ctx, n); err != nil {
		r.t.Fatal(err)
	}
}

// CIRC-5 "Renew": due Friday with a 14-day period becomes 14 days after that Friday.
func TestRenew(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(2)
	pat, card := r.user("pat", perm.Patron)
	l := r.lend(card, isbnA) // 2026-01-15 + 14
	if l.DueDate != "2026-01-29" {
		t.Fatalf("due %s", l.DueDate)
	}
	r.now = r.now.AddDate(0, 0, 3)
	got, err := r.svc.Renew(ctx, l.ID, pat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DueDate != "2026-02-12" || got.Renewals != 1 {
		t.Errorf("renewed = %+v", got)
	}
	mine, _ := r.svc.OpenLoansOf(ctx, pat.ID)
	if len(mine) != 1 || mine[0].DueDate != "2026-02-12" {
		t.Errorf("stored = %+v", mine)
	}
	if err := r.set.SetLoanPeriod(ctx, 7); err != nil {
		t.Fatal(err)
	}
	got, err = r.svc.Renew(ctx, l.ID, pat.ID)
	if err != nil || got.DueDate != "2026-02-19" || got.Renewals != 2 {
		t.Errorf("second renewal = %+v %v", got, err)
	}
}

// CIRC-5 "Over limit": refused, saying why, and nothing changes.
func TestRenewOverLimit(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(1)
	pat, card := r.user("pat", perm.Patron)
	l := r.lend(card, isbnA)
	if _, err := r.svc.Renew(ctx, l.ID, pat.ID); err != nil {
		t.Fatal(err)
	}
	_, err := r.svc.Renew(ctx, l.ID, pat.ID)
	wantErr(t, err, apperr.Conflict, "renewed the most times allowed (1)")
	if mine, _ := r.svc.OpenLoansOf(ctx, pat.ID); mine[0].DueDate != "2026-02-12" || mine[0].Renewals != 1 {
		t.Errorf("changed: %+v", mine[0])
	}
}

// CIRC-5 "Overdue loan": refused, saying it is overdue; due today is not overdue.
func TestRenewOverdue(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(3)
	pat, card := r.user("pat", perm.Patron)
	l := r.lend(card, isbnA) // due 2026-01-29
	r.now = time.Date(2026, 1, 29, 23, 0, 0, 0, time.UTC)
	if _, err := r.svc.Renew(ctx, l.ID, pat.ID); err != nil {
		t.Fatalf("due today: %v", err)
	}
	r.now = time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC) // new due date 2026-02-12 has passed
	_, err := r.svc.Renew(ctx, l.ID, 0)
	wantErr(t, err, apperr.Conflict, "overdue")
	if mine, _ := r.svc.OpenLoansOf(ctx, pat.ID); mine[0].DueDate != "2026-02-12" || mine[0].Renewals != 1 {
		t.Errorf("changed: %+v", mine[0])
	}
}

// Due 23:59 and 00:00 in the library's zone: renewable until the day ends there, not in UTC.
func TestRenewFollowsTheLibraryZone(t *testing.T) {
	r := newRig(t)
	if err := r.set.SetTimeZone(ctx, "Pacific/Kiritimati"); err != nil { // UTC+14
		t.Fatal(err)
	}
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(3)
	_, card := r.user("pat", perm.Patron)
	l := r.lend(card, isbnA)                              // due 2026-01-30 there
	r.now = time.Date(2026, 1, 30, 10, 0, 0, 0, time.UTC) // 00:00 on the 31st there
	_, err := r.svc.Renew(ctx, l.ID, 0)
	wantErr(t, err, apperr.Conflict, "overdue")
	r.now = time.Date(2026, 1, 30, 9, 59, 0, 0, time.UTC) // 23:59 on the 30th there
	if _, err := r.svc.Renew(ctx, l.ID, 0); err != nil {
		t.Errorf("23:59: %v", err)
	}
}

// CIRC-5 "Librarian renews": a desk renewal (no patron check) moves the date as a patron's does.
func TestLibrarianRenews(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(2)
	_, card := r.user("pat", perm.Patron)
	l := r.lend(card, isbnA)
	got, err := r.svc.Renew(ctx, l.ID, 0)
	if err != nil || got.DueDate != "2026-02-12" {
		t.Errorf("%+v %v", got, err)
	}
}

// Patron A can't renew, or learn of, patron B's loan by its id: 404, and nothing changes.
func TestRenewSomeoneElsesLoan(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(2)
	a, _ := r.user("ann", perm.Patron)
	_, cardB := r.user("bob", perm.Patron)
	l := r.lend(cardB, isbnA)
	_, err := r.svc.Renew(ctx, l.ID, a.ID)
	wantErr(t, err, apperr.NotFound, "no such loan")
	_, err = r.svc.Renew(ctx, l.ID+100, a.ID)
	wantErr(t, err, apperr.NotFound, "no such loan")
	if ls, _ := r.svc.OpenLoansOf(ctx, l.PatronID); ls[0].Renewals != 0 {
		t.Error("renewed")
	}
}

func TestRenewReturnedLoan(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(2)
	pat, card := r.user("pat", perm.Patron)
	l := r.lend(card, isbnA)
	lib, _ := r.user("lib", perm.Librarian)
	if _, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, ""); err != nil {
		t.Fatal(err)
	}
	_, err := r.svc.Renew(ctx, l.ID, pat.ID)
	wantErr(t, err, apperr.Conflict, "already returned")
}

// Concurrent renewals never pass the limit.
func TestConcurrentRenewalsNeverPassTheLimit(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.maxRenewals(2)
	pat, card := r.user("pat", perm.Patron)
	l := r.lend(card, isbnA)
	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.svc.Renew(ctx, l.ID, pat.ID)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if e, isApp := apperr.As(err); !isApp || e.Code != apperr.Conflict {
			t.Errorf("unexpected error: %v", err)
		}
	}
	mine, _ := r.svc.OpenLoansOf(ctx, pat.ID)
	if ok != 2 || mine[0].Renewals != 2 || mine[0].DueDate != "2026-02-26" {
		t.Errorf("%d succeeded: %+v", ok, mine[0])
	}
}

// CIRC-6 "Overdue list": the overdue loan appears with its patron and days overdue; one due today does not.
func TestOverdueList(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.book("Dune", isbnB, 1)
	_, c1 := r.user("ann", perm.Patron)
	_, c2 := r.user("bob", perm.Patron)
	late := r.lend(c1, isbnA) // due 2026-01-29
	r.now = r.now.AddDate(0, 0, 10)
	fresh := r.lend(c2, isbnB) // due 2026-02-08
	r.now = time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	p, err := r.svc.Overdue(ctx, 1, 0)
	if err != nil || p.Total != 1 || len(p.Loans) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if g := p.Loans[0]; g.ID != late.ID || g.PatronName != "Ann" || g.DaysLate != 2 || g.Title != "The Hobbit" {
		t.Errorf("loan = %+v", g)
	}
	r.now = time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC) // fresh is due today
	if p, _ = r.svc.Overdue(ctx, 1, 0); p.Total != 1 || p.Loans[0].ID == fresh.ID {
		t.Errorf("due today listed: %+v", p)
	}
	// a returned loan leaves the list
	lib, _ := r.user("lib", perm.Librarian)
	if _, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, ""); err != nil {
		t.Fatal(err)
	}
	if p, _ = r.svc.Overdue(ctx, 1, 0); p.Total != 0 || len(p.Loans) != 0 {
		t.Errorf("returned loan listed: %+v", p)
	}
}

func TestOverdueListPages(t *testing.T) {
	r := newRig(t)
	for i, isbn := range []string{isbnA, isbnB, "9780261102217"} {
		r.book("Book "+string(rune('A'+i)), isbn, 1)
		_, card := r.user("pat"+string(rune('a'+i)), perm.Patron)
		r.lend(card, isbn)
	}
	r.now = r.now.AddDate(0, 0, 30)
	p, err := r.svc.Overdue(ctx, 2, 2)
	if err != nil || p.Total != 3 || len(p.Loans) != 1 || p.Page != 2 || p.PageSize != 2 {
		t.Errorf("%+v %v", p, err)
	}
	if p, _ = r.svc.Overdue(ctx, 3, 2); p.Loans == nil || len(p.Loans) != 0 {
		t.Errorf("past the end: %+v", p)
	}
	for _, c := range [][2]int{{0, 0}, {1, 101}, {1, -1}, {maxPage + 1, 5}} {
		if _, err := r.svc.Overdue(ctx, c[0], c[1]); err == nil {
			t.Errorf("page %d size %d accepted", c[0], c[1])
		}
	}
}

// CIRC-3 "Lookup": a patron with two loans, found by name and by card; both loans shown.
func TestLookup(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.book("Dune", isbnB, 1)
	pat, card := r.user("pat", perm.Patron)
	r.user("patricia", perm.Patron)
	r.lend(card, isbnA)
	r.lend(card, isbnB)
	r.now = r.now.AddDate(0, 0, 20) // both overdue

	found, err := r.svc.FindPatrons(ctx, "PAT")
	if err != nil || len(found) != 2 || found[0].DisplayName != "Pat" || found[0].Out != 2 || found[0].Overdue != 2 || found[0].Card != card {
		t.Fatalf("by name: %+v %v", found, err)
	}
	found, err = r.svc.FindPatrons(ctx, card)
	if err != nil || len(found) != 1 || found[0].ID != pat.ID {
		t.Fatalf("by card: %+v %v", found, err)
	}
	if found, _ = r.svc.FindPatrons(ctx, "nobody"); len(found) != 0 {
		t.Errorf("found %+v", found)
	}
	if found, _ = r.svc.FindPatrons(ctx, "%"); len(found) != 0 {
		t.Errorf("a wildcard matched %+v", found)
	}
	rec, err := r.svc.PatronRecord(ctx, pat.ID)
	if err != nil || len(rec.Current) != 2 || len(rec.Overdue) != 2 || rec.Patron.Card != card {
		t.Errorf("record: %+v %v", rec, err)
	}
	h, err := r.svc.History(ctx, pat.ID, 1, 0)
	if err != nil || h.Total != 2 {
		t.Errorf("history: %+v %v", h, err)
	}
	_, err = r.svc.PatronRecord(ctx, 9999)
	wantErr(t, err, apperr.NotFound, "no such user")
}

func TestLookupRefusesBadQueries(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"", "   ", string(make([]byte, 201)), "a\x00b"} {
		if _, err := r.svc.FindPatrons(ctx, q); err == nil {
			t.Errorf("%q accepted", q)
		}
	}
	long := ""
	for i := 0; i < 200; i++ {
		long += "é"
	}
	if _, err := r.svc.FindPatrons(ctx, long); err != nil {
		t.Errorf("200 characters refused: %v", err)
	}
	if _, err := r.svc.FindPatrons(ctx, long+"é"); err == nil {
		t.Error("201 characters accepted")
	}
}

// PATRON-1 "Dashboard" and history: current loans with due dates and overdue flag; history includes returned loans and removed copies.
func TestDashboardAndHistory(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	r.book("Dune", isbnB, 1)
	pat, card := r.user("pat", perm.Patron)
	lib, _ := r.user("lib", perm.Librarian)
	first := r.lend(card, isbnA)
	if _, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.cat.RemoveCopy(ctx, 1, cs[0].ID); err != nil {
		t.Fatal(err)
	}
	r.lend(card, isbnB)
	cur, err := r.svc.OpenLoansOf(ctx, pat.ID)
	if err != nil || len(cur) != 1 || cur[0].Title != "Dune" || cur[0].DueDate != "2026-01-29" || cur[0].DaysLate != 0 {
		t.Fatalf("dashboard: %+v %v", cur, err)
	}
	h, err := r.svc.History(ctx, pat.ID, 1, 0)
	if err != nil || h.Total != 2 || len(h.Loans) != 2 {
		t.Fatalf("history: %+v %v", h, err)
	}
	if h.Loans[0].Title != "Dune" || h.Loans[1].ID != first.ID || h.Loans[1].ReturnedAt == "" || h.Loans[1].Title != "The Hobbit" {
		t.Errorf("history order or content: %+v", h.Loans)
	}
	other, _ := r.user("sam", perm.Patron)
	if h, _ = r.svc.History(ctx, other.ID, 1, 0); h.Total != 0 {
		t.Errorf("someone else's history: %+v", h)
	}
}

// CIRC-7 "Off": refused while the setting is off, and nothing is lent.
func TestSelfCheckoutOff(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	pat, _ := r.user("pat", perm.Patron)
	_, err := r.svc.SelfCheckout(ctx, pat.ID, isbnA)
	wantErr(t, err, apperr.Forbidden, "switched off")
	if openCount(t, r) != 0 {
		t.Error("a loan was made")
	}
}

// CIRC-7 "On": the copy is lent to the caller under the borrowing policy.
func TestSelfCheckoutOn(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	r.ownMode()
	if err := r.set.SetSelfCheckout(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := r.set.SetMaxLoans(ctx, 1); err != nil {
		t.Fatal(err)
	}
	_, ds := r.book("Dune", isbnB, 1)
	pat, _ := r.user("pat", perm.Patron)
	l, err := r.svc.SelfCheckout(ctx, pat.ID, r.code(cs[0].ID))
	if err != nil || l.PatronID != pat.ID || l.DueDate != "2026-01-29" {
		t.Fatalf("%+v %v", l, err)
	}
	// the limit applies, and speaks to the patron
	_, err = r.svc.SelfCheckout(ctx, pat.ID, r.code(ds[0].ID))
	wantErr(t, err, apperr.Conflict, "you already have 1 of 1")
	// an ISBN in own-barcode mode is turned away
	_, err = r.svc.SelfCheckout(ctx, pat.ID, isbnB)
	wantErr(t, err, apperr.Invalid, "own barcode")
}

// CIRC-7 "On, by ISBN": an available copy is lent and its number shown.
func TestSelfCheckoutByISBN(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 3)
	if err := r.set.SetSelfCheckout(ctx, true); err != nil {
		t.Fatal(err)
	}
	_, other := r.user("sam", perm.Patron)
	r.lend(other, isbnA)
	pat, _ := r.user("pat", perm.Patron)
	l, err := r.svc.SelfCheckout(ctx, pat.ID, isbnA)
	if err != nil || l.CopyNumber != 2 || l.CopyID != cs[1].ID || l.PatronID != pat.ID {
		t.Errorf("%+v %v", l, err)
	}
}

// CIRC-7 "No copy available": refused, saying so, and no other patron is named.
func TestSelfCheckoutNoCopyAvailable(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	if err := r.set.SetSelfCheckout(ctx, true); err != nil {
		t.Fatal(err)
	}
	_, other := r.user("sam", perm.Patron)
	r.lend(other, isbnA)
	pat, _ := r.user("pat", perm.Patron)
	_, err := r.svc.SelfCheckout(ctx, pat.ID, isbnA)
	wantErr(t, err, apperr.Conflict, "No copy of The Hobbit is available")
	r.ownMode()
	_, err = r.svc.SelfCheckout(ctx, pat.ID, r.code(cs[0].ID))
	wantErr(t, err, apperr.Conflict, "already out")
	if e, _ := apperr.As(err); strings.Contains(e.Message, "Sam") {
		t.Errorf("names the other patron: %s", e.Message)
	}
}

// A deactivated patron can't check themselves out a book.
func TestSelfCheckoutDeactivated(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	if err := r.set.SetSelfCheckout(ctx, true); err != nil {
		t.Fatal(err)
	}
	pat, _ := r.user("pat", perm.Patron)
	if err := r.st.WithTx(ctx, func(tx store.Tx) error { return r.us.SetActiveTx(ctx, tx, pat.ID, false) }); err != nil {
		t.Fatal(err)
	}
	_, err := r.svc.SelfCheckout(ctx, pat.ID, isbnA)
	wantErr(t, err, apperr.Conflict, "deactivated")
}

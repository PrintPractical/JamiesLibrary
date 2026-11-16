package circ

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

var ctx = context.Background()

const isbnA, isbnB = "9780306406157", "9780140328721"

type rig struct {
	t     *testing.T
	st    *store.Store
	set   *settings.Service
	cat   *catalog.Service
	us    *users.Service
	svc   *Service
	now   time.Time
	desks int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, now: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	t.Cleanup(clock.SetSource(func() time.Time { return r.now }))
	var err error
	if r.st, err = store.Open(ctx, t.TempDir(), store.Options{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.st.Close() })
	if r.set, err = settings.New(ctx, r.st); err != nil {
		t.Fatal(err)
	}
	r.cat = catalog.New(r.st, catalog.Options{Settings: r.set, Loans: Loans{}, Now: clock.Now})
	r.us = users.New(r.st)
	r.svc = New(r.st, Options{Settings: r.set, Catalog: r.cat, Users: r.us})
	return r
}

func (r *rig) user(name string, role perm.Role) (users.User, string) {
	r.t.Helper()
	u, err := r.us.Create(ctx, users.NewUser{Username: name, DisplayName: strings.ToUpper(name[:1]) + name[1:], PasswordHash: "x", Role: role})
	if err != nil {
		r.t.Fatal(err)
	}
	card, err := r.us.Card(ctx, u.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	return u, card
}

func (r *rig) book(title, isbn string, copies int) (catalog.Book, []catalog.Copy) {
	r.t.Helper()
	b, err := r.cat.CreateBook(ctx, 1, catalog.BookInput{Title: title, Authors: []string{"An Author"}, ISBN: isbn})
	if err != nil {
		r.t.Fatal(err)
	}
	var cs []catalog.Copy
	for i := 0; i < copies; i++ {
		c, err := r.cat.AddCopy(ctx, 1, b.ID, catalog.CopyInput{LocationID: 1})
		if err != nil {
			r.t.Fatal(err)
		}
		cs = append(cs, c)
	}
	return b, cs
}

func (r *rig) ownMode() {
	r.t.Helper()
	if err := r.cat.SetCopyIDMode(ctx, 1, settings.CopyIDOwn, nil); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) code(copyID int64) string {
	r.t.Helper()
	c, err := r.cat.GetCopy(ctx, copyID)
	if err != nil || c.Code == "" {
		r.t.Fatalf("copy %d has no code: %+v %v", copyID, c, err)
	}
	return c.Code
}

func wantErr(t *testing.T, err error, code apperr.Code, contains string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code || !strings.Contains(e.Message, contains) {
		t.Fatalf("got %v, want a %s error containing %q", err, code, contains)
	}
}

// CIRC-1 "Checkout": the copy becomes unavailable and the patron sees it with its due date.
func TestCheckout(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	r.ownMode()
	lib, _ := r.user("lib", perm.Librarian)
	pat, card := r.user("pat", perm.Patron)

	l, err := r.svc.Checkout(ctx, lib.ID, card, r.code(cs[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if l.CopyID != cs[0].ID || l.PatronID != pat.ID || l.DueDate != "2026-01-29" || l.Title != "The Hobbit" {
		t.Errorf("loan = %+v", l)
	}
	got, err := r.cat.GetCopy(ctx, cs[0].ID)
	if err != nil || !got.OnLoan {
		t.Errorf("copy after checkout: %+v %v", got, err)
	}
	mine, err := r.svc.OpenLoansOf(ctx, pat.ID)
	if err != nil || len(mine) != 1 || mine[0].DueDate != "2026-01-29" {
		t.Errorf("the patron sees %+v %v", mine, err)
	}
}

// CIRC-1 "Copy already out": refused, and nothing changes.
func TestCopyAlreadyOut(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	r.ownMode()
	lib, _ := r.user("lib", perm.Librarian)
	_, c1 := r.user("sam", perm.Patron)
	_, c2 := r.user("pat", perm.Patron)
	code := r.code(cs[0].ID)
	if _, err := r.svc.Checkout(ctx, lib.ID, c1, code); err != nil {
		t.Fatal(err)
	}
	_, err := r.svc.Checkout(ctx, lib.ID, c2, code)
	wantErr(t, err, apperr.Conflict, "on loan to Sam, due Thu 29 Jan.")
	if !strings.Contains(err.(*apperr.Error).Message, "check it in first") {
		t.Errorf("the message doesn't say what to do: %v", err)
	}
	var n int
	_ = r.st.DB().QueryRow(`SELECT count(*) FROM loans`).Scan(&n)
	if n != 1 {
		t.Errorf("%d loans", n)
	}
}

// CIRC-1 "Checkout by ISBN": copy 1 out, 2 and 3 available: copy 2 is lent and shown as copy 2.
func TestCheckoutByISBN(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 3)
	lib, _ := r.user("lib", perm.Librarian)
	_, c1 := r.user("sam", perm.Patron)
	pat, c2 := r.user("pat", perm.Patron)
	first, err := r.svc.Checkout(ctx, lib.ID, c1, isbnA)
	if err != nil || first.CopyID != cs[0].ID {
		t.Fatalf("first: %+v %v", first, err)
	}
	l, err := r.svc.Checkout(ctx, lib.ID, c2, "978-0-306-40615-7")
	if err != nil {
		t.Fatal(err)
	}
	if l.CopyNumber != 2 || l.CopyID != cs[1].ID || l.PatronID != pat.ID {
		t.Errorf("loan = %+v", l)
	}
}

// CIRC-1 "No copy available by ISBN": refused, saying no copy is available.
func TestNoCopyAvailableByISBN(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 2)
	lib, _ := r.user("lib", perm.Librarian)
	_, c1 := r.user("sam", perm.Patron)
	_, c2 := r.user("pat", perm.Patron)
	for i := 0; i < 2; i++ {
		if _, err := r.svc.Checkout(ctx, lib.ID, c1, isbnA); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.svc.Checkout(ctx, lib.ID, c2, isbnA)
	wantErr(t, err, apperr.Conflict, "No copy of The Hobbit is available")
}

// CIRC-1 "Due at end of day": the due date and the overdue moment follow the library's zone, not the server's.
func TestDueAtEndOfDay(t *testing.T) {
	r := newRig(t)
	if err := r.set.SetTimeZone(ctx, "Pacific/Kiritimati"); err != nil { // UTC+14
		t.Fatal(err)
	}
	r.book("The Hobbit", isbnA, 1)
	lib, _ := r.user("lib", perm.Librarian)
	pat, card := r.user("pat", perm.Patron)
	// 12:00 UTC on the 15th is 02:00 on the 16th there: due 14 days on from the 16th, not the 15th.
	l, err := r.svc.Checkout(ctx, lib.ID, card, isbnA)
	if err != nil || l.DueDate != "2026-01-30" {
		t.Fatalf("%+v %v", l, err)
	}
	overdue := func() int {
		p, err := r.svc.Resolve(ctx, card, StepPatron)
		if err != nil {
			t.Fatal(err)
		}
		return p.Patron.Overdue
	}
	r.now = time.Date(2026, 1, 30, 9, 59, 0, 0, time.UTC) // 23:59 on the 30th there
	if overdue() != 0 {
		t.Error("overdue before the day ended in the library's zone")
	}
	r.now = time.Date(2026, 1, 30, 10, 0, 0, 0, time.UTC) // midnight on the 31st there
	if overdue() != 1 {
		t.Error("not overdue at midnight in the library's zone")
	}
	if ls, _ := r.svc.OpenLoansOf(ctx, pat.ID); len(ls) != 1 || ls[0].DaysLate != 1 {
		t.Errorf("%+v", ls)
	}
}

// CIRC-2 "Return": the copy is available and the loan stays as returned history.
func TestReturn(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	r.ownMode()
	lib, _ := r.user("lib", perm.Librarian)
	pat, card := r.user("pat", perm.Patron)
	code := r.code(cs[0].ID)
	if _, err := r.svc.Checkout(ctx, lib.ID, card, code); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.AddDate(0, 0, 17) // three days late
	res, err := r.svc.Checkin(ctx, lib.ID, code, 0, "")
	if err != nil || res.Status != Returned || res.Loan.PatronID != pat.ID || res.Loan.DaysLate != 3 || res.Loan.Location != "Main shelf" {
		t.Fatalf("%+v %v", res, err)
	}
	if c, _ := r.cat.GetCopy(ctx, cs[0].ID); c.OnLoan {
		t.Error("still on loan")
	}
	var returnedAt string
	var by int64
	if err := r.st.DB().QueryRow(`SELECT returned_at, returned_by FROM loans WHERE patron_id = ?`, pat.ID).Scan(&returnedAt, &by); err != nil || returnedAt == "" || by != lib.ID {
		t.Errorf("history row: %q %d %v", returnedAt, by, err)
	}
	if ls, _ := r.svc.OpenLoansOf(ctx, pat.ID); len(ls) != 0 {
		t.Errorf("open loans %+v", ls)
	}
	// and it can be lent again
	if _, err := r.svc.Checkout(ctx, lib.ID, card, code); err != nil {
		t.Errorf("lending it again: %v", err)
	}
}

// CIRC-2 "Single copy out by ISBN": that loan is returned.
func TestSingleCopyOutByISBN(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 3)
	lib, _ := r.user("lib", perm.Librarian)
	_, card := r.user("pat", perm.Patron)
	if _, err := r.svc.Checkout(ctx, lib.ID, card, isbnA); err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, "")
	if err != nil || res.Status != Returned || res.Loan.CopyID != cs[0].ID {
		t.Fatalf("%+v %v", res, err)
	}
}

// CIRC-2 "Several out by ISBN": asked to scan a card or choose; only the chosen loan is returned.
func TestSeveralOutByISBN(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 3)
	lib, _ := r.user("lib", perm.Librarian)
	_, c1 := r.user("sam", perm.Patron)
	_, c3 := r.user("pat", perm.Patron)
	l1, _ := r.svc.Checkout(ctx, lib.ID, c1, isbnA)
	_, _ = r.svc.Checkout(ctx, lib.ID, c1, isbnA) // copy 2, also Sam's, then back
	if _, err := r.svc.Checkin(ctx, lib.ID, isbnA, l1.ID+1, ""); err != nil {
		t.Fatal(err)
	}
	l3, err := r.svc.Checkout(ctx, lib.ID, c3, isbnA)
	if err != nil || l3.CopyNumber != 2 {
		t.Fatalf("%+v %v", l3, err)
	}
	// copies 1 (Sam) and 2 (Pat) are out now

	res, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, "")
	if err != nil || res.Status != Choose || len(res.Choices) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Choices[0].PatronName != "Sam" || res.Choices[0].CopyNumber != 1 || res.Choices[1].PatronName != "Pat" || res.Choices[1].CopyNumber != 2 {
		t.Errorf("choices = %+v", res.Choices)
	}
	if n := openCount(t, r); n != 2 {
		t.Fatalf("asking returned something: %d open", n)
	}
	// narrowing by a scanned card returns that patron's loan
	res, err = r.svc.Checkin(ctx, lib.ID, isbnA, 0, c3)
	if err != nil || res.Status != Returned || res.Loan.PatronName != "Pat" || res.Loan.CopyID != cs[1].ID {
		t.Fatalf("by card: %+v %v", res, err)
	}
	if n := openCount(t, r); n != 1 {
		t.Errorf("%d open after returning one", n)
	}
	// choosing from the list returns only the chosen loan
	res, err = r.svc.Checkin(ctx, lib.ID, isbnA, l1.ID, "")
	if err != nil || res.Status != Returned || res.Loan.ID != l1.ID {
		t.Fatalf("by choice: %+v %v", res, err)
	}
	if n := openCount(t, r); n != 0 {
		t.Errorf("%d open", n)
	}
}

func openCount(t *testing.T, r *rig) int {
	t.Helper()
	var n int
	if err := r.st.DB().QueryRow(`SELECT count(*) FROM loans WHERE returned_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Narrowing and choosing are checked: a card or loan that isn't one of the book's open loans returns nothing.
func TestCheckinChoiceMustBeThisBooksOpenLoan(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 2)
	r.book("Dune", isbnB, 1)
	lib, _ := r.user("lib", perm.Librarian)
	_, c1 := r.user("sam", perm.Patron)
	_, c2 := r.user("pat", perm.Patron)
	hobbit1, _ := r.svc.Checkout(ctx, lib.ID, c1, isbnA)
	_, _ = r.svc.Checkout(ctx, lib.ID, c2, isbnA)
	dune, _ := r.svc.Checkout(ctx, lib.ID, c1, isbnB)
	if _, err := r.svc.Checkin(ctx, lib.ID, isbnA, dune.ID, ""); err == nil {
		t.Error("returned another book's loan by choice")
	}
	if _, err := r.svc.Checkin(ctx, lib.ID, isbnA, hobbit1.ID+99, ""); err == nil {
		t.Error("returned a loan that doesn't exist")
	}
	_, c3 := r.user("quinn", perm.Patron)
	_, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, c3)
	wantErr(t, err, apperr.Conflict, "doesn't have The Hobbit out")
	_, err = r.svc.Checkin(ctx, lib.ID, isbnA, 0, "000000000000")
	wantErr(t, err, apperr.NotFound, "No such patron")
	if n := openCount(t, r); n != 3 {
		t.Errorf("%d open", n)
	}
}

// CIRC-2 "Nothing out": they are told none is checked out, and a copy that isn't out is left alone.
func TestNothingOut(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	lib, _ := r.user("lib", perm.Librarian)
	res, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, "")
	if err != nil || res.Status != NotOut || !strings.Contains(res.Message, "None is checked out") || res.Book == nil {
		t.Fatalf("%+v %v", res, err)
	}
	r.ownMode()
	res, err = r.svc.Checkin(ctx, lib.ID, r.code(cs[0].ID), 0, "")
	if err != nil || res.Status != NotOut || res.Copy == nil || !strings.Contains(res.Message, "wasn't checked out") {
		t.Fatalf("%+v %v", res, err)
	}
}

// CIRC-4 "Limit reached": the next checkout is refused, and a return makes room.
func TestLimitReached(t *testing.T) {
	r := newRig(t)
	if err := r.set.SetMaxLoans(ctx, 2); err != nil {
		t.Fatal(err)
	}
	r.book("The Hobbit", isbnA, 3)
	lib, _ := r.user("lib", perm.Librarian)
	_, card := r.user("pat", perm.Patron)
	for i := 0; i < 2; i++ {
		if _, err := r.svc.Checkout(ctx, lib.ID, card, isbnA); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.svc.Checkout(ctx, lib.ID, card, isbnA)
	wantErr(t, err, apperr.Conflict, "Limit reached")
	if n := openCount(t, r); n != 2 {
		t.Errorf("%d open", n)
	}
	// a return makes room
	res, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, card)
	if err != nil || res.Status != Choose {
		t.Fatalf("%+v %v", res, err)
	}
	if res, err = r.svc.Checkin(ctx, lib.ID, isbnA, res.Choices[0].ID, card); err != nil || res.Status != Returned {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := r.svc.Checkout(ctx, lib.ID, card, isbnA); err != nil {
		t.Errorf("after the return: %v", err)
	}
	p, _ := r.svc.Resolve(ctx, card, StepPatron)
	if p.Patron.Limit != 2 {
		t.Errorf("limit shown as %d", p.Patron.Limit)
	}
}

// SCAN-3 "Scan copy barcode": own-barcode mode, the copy is identified.
func TestScanCopyBarcode(t *testing.T) {
	r := newRig(t)
	b, cs := r.book("The Hobbit", isbnA, 2)
	r.ownMode()
	got, err := r.svc.Resolve(ctx, r.code(cs[1].ID), StepItem)
	if err != nil || got.Copy == nil || got.Copy.ID != cs[1].ID || got.Copy.Number != 2 || got.Copy.BookID != b.ID || got.Copy.Title != "The Hobbit" {
		t.Fatalf("%+v %v", got, err)
	}
}

// SCAN-3 "Scan ISBN": by-ISBN mode, the book is identified.
func TestScanISBN(t *testing.T) {
	r := newRig(t)
	b, _ := r.book("The Hobbit", isbnA, 2)
	got, err := r.svc.Resolve(ctx, isbnA, StepItem)
	if err != nil || got.Book == nil || got.Book.ID != b.ID || got.Book.Copies != 2 || got.Book.Available != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}

// SCAN-3 "Typed code": typed in lower case with spaces round it, or an ISBN with hyphens, finds the same copy or book.
func TestTypedCode(t *testing.T) {
	r := newRig(t)
	b, cs := r.book("The Hobbit", isbnA, 1)
	got, err := r.svc.Resolve(ctx, " 978-0-306-40615-7 ", StepItem)
	if err != nil || got.Book == nil || got.Book.ID != b.ID {
		t.Fatalf("%+v %v", got, err)
	}
	// the ISBN-10 of the same book
	if got, err = r.svc.Resolve(ctx, "0-306-40615-2", StepItem); err != nil || got.Book == nil || got.Book.ID != b.ID {
		t.Fatalf("isbn-10: %+v %v", got, err)
	}
	r.ownMode()
	typed := " " + strings.ToLower(r.code(cs[0].ID)) + " "
	if got, err = r.svc.Resolve(ctx, typed, StepItem); err != nil || got.Copy == nil || got.Copy.ID != cs[0].ID {
		t.Fatalf("%+v %v", got, err)
	}
}

// SCAN-3 "Unknown code": told nothing matches. A card is not an item, and a copy's code is not a patron.
func TestUnknownCode(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	_, card := r.user("pat", perm.Patron)
	r.ownMode()
	for _, code := range []string{"NOPE123", "", "has space", strings.Repeat("9", 40), card, "9780140328721"} {
		_, err := r.svc.Resolve(ctx, code, StepItem)
		if code == "9780140328721" {
			wantErr(t, err, apperr.Invalid, "own barcode") // a valid ISBN in own mode: CAT-9
			continue
		}
		wantErr(t, err, apperr.NotFound, "Nothing matches")
	}
	_, err := r.svc.Resolve(ctx, r.code(cs[0].ID), StepPatron)
	wantErr(t, err, apperr.NotFound, "No such patron")
	// a valid ISBN no book has, in ISBN mode
	if err := r.cat.SetCopyIDMode(ctx, 1, settings.CopyIDISBN, nil); err != nil {
		t.Fatal(err)
	}
	_, err = r.svc.Resolve(ctx, isbnB, StepItem)
	wantErr(t, err, apperr.NotFound, "Nothing matches")
	_, err = r.svc.Resolve(ctx, "x", "bogus")
	wantErr(t, err, apperr.Invalid, "step")
}

// CAT-9 "ISBN scanned in own-barcode mode": told to scan the copy's own barcode, at checkout and check-in.
func TestISBNScannedInOwnBarcodeMode(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 1)
	r.ownMode()
	lib, _ := r.user("lib", perm.Librarian)
	_, card := r.user("pat", perm.Patron)
	_, err := r.svc.Resolve(ctx, isbnA, StepItem)
	wantErr(t, err, apperr.Invalid, "Scan the copy's own barcode")
	_, err = r.svc.Checkout(ctx, lib.ID, card, isbnA)
	wantErr(t, err, apperr.Invalid, "Scan the copy's own barcode")
	_, err = r.svc.Checkin(ctx, lib.ID, isbnA, 0, "")
	wantErr(t, err, apperr.Invalid, "Scan the copy's own barcode")
	if openCount(t, r) != 0 {
		t.Error("a loan was made")
	}
}

// SCAN-4 "Scan card" and "Typed card number": the patron is selected, however the number was written.
func TestScanCard(t *testing.T) {
	r := newRig(t)
	pat, card := r.user("pat", perm.Patron)
	for _, typed := range []string{card, " " + card + "\n"} {
		got, err := r.svc.Resolve(ctx, typed, StepPatron)
		if err != nil || got.Patron == nil || got.Patron.ID != pat.ID || got.Patron.DisplayName != "Pat" || got.Patron.Card != card || !got.Patron.Active {
			t.Fatalf("%q: %+v %v", typed, got, err)
		}
	}
}

// SCAN-4 "Unknown card": told no such patron exists, whatever was typed.
func TestUnknownCard(t *testing.T) {
	r := newRig(t)
	for _, code := range []string{"123456789012", "", "not a card", strings.Repeat("1", 99)} {
		_, err := r.svc.Resolve(ctx, code, StepPatron)
		wantErr(t, err, apperr.NotFound, "No such patron")
	}
	r.book("The Hobbit", isbnA, 1)
	lib, _ := r.user("lib", perm.Librarian)
	_, err := r.svc.Checkout(ctx, lib.ID, "123456789012", isbnA)
	wantErr(t, err, apperr.NotFound, "No such patron")
}

// ADMIN-2 "Deactivate patron with a loan": checkout is refused, and the loan is still visible.
func TestDeactivatedPatronWithALoan(t *testing.T) {
	r := newRig(t)
	r.book("The Hobbit", isbnA, 2)
	lib, _ := r.user("lib", perm.Librarian)
	pat, card := r.user("pat", perm.Patron)
	if _, err := r.svc.Checkout(ctx, lib.ID, card, isbnA); err != nil {
		t.Fatal(err)
	}
	err := r.st.WithTx(ctx, func(tx store.Tx) error { return r.us.SetActiveTx(ctx, tx, pat.ID, false) })
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.svc.Checkout(ctx, lib.ID, card, isbnA)
	wantErr(t, err, apperr.Conflict, "deactivated")
	mine, err := r.svc.OpenLoansOf(ctx, pat.ID)
	if err != nil || len(mine) != 1 {
		t.Errorf("the loan is no longer visible: %+v %v", mine, err)
	}
	if p, err := r.svc.Resolve(ctx, card, StepPatron); err != nil || p.Patron.Active || p.Patron.Out != 1 {
		t.Errorf("%+v %v", p, err)
	}
	// the copy can still be checked in
	if res, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, ""); err != nil || res.Status != Returned {
		t.Errorf("%+v %v", res, err)
	}
}

// CAT-5 / the on-loan interface: a copy or book on loan can't be removed, and can once it is back.
func TestCatalogRefusesRemovalWhileOnLoan(t *testing.T) {
	r := newRig(t)
	b, cs := r.book("The Hobbit", isbnA, 1)
	lib, _ := r.user("lib", perm.Librarian)
	_, card := r.user("pat", perm.Patron)
	if _, err := r.svc.Checkout(ctx, lib.ID, card, isbnA); err != nil {
		t.Fatal(err)
	}
	wantErr(t, r.cat.RemoveCopy(ctx, 1, cs[0].ID), apperr.Conflict, "on loan")
	wantErr(t, r.cat.RemoveBook(ctx, 1, b.ID), apperr.Conflict, "on loan")
	if _, err := r.svc.Checkin(ctx, lib.ID, isbnA, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.cat.RemoveBook(ctx, 1, b.ID); err != nil {
		t.Errorf("after the return: %v", err)
	}
	// a removed copy can't be lent
	_, err := r.svc.Checkout(ctx, lib.ID, card, isbnA)
	wantErr(t, err, apperr.NotFound, "Nothing matches")
}

// Two checkouts of the last copy at once: exactly one wins.
func TestConcurrentCheckoutsOfTheLastCopy(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	r.ownMode()
	lib, _ := r.user("lib", perm.Librarian)
	const n = 8
	cards := make([]string, n)
	for i := range cards {
		_, cards[i] = r.user("pat"+string(rune('a'+i)), perm.Patron)
	}
	code := r.code(cs[0].ID)
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.svc.Checkout(ctx, lib.ID, cards[i], code)
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
	if ok != 1 || openCount(t, r) != 1 {
		t.Errorf("%d succeeded, %d open loans", ok, openCount(t, r))
	}
}

// Concurrent checkouts never take a patron past the limit.
func TestConcurrentCheckoutsNeverExceedTheLimit(t *testing.T) {
	r := newRig(t)
	if err := r.set.SetMaxLoans(ctx, 3); err != nil {
		t.Fatal(err)
	}
	r.book("The Hobbit", isbnA, 10)
	lib, _ := r.user("lib", perm.Librarian)
	pat, card := r.user("pat", perm.Patron)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.svc.Checkout(ctx, lib.ID, card, isbnA)
		}()
	}
	wg.Wait()
	mine, err := r.svc.OpenLoansOf(ctx, pat.ID)
	if err != nil || len(mine) != 3 {
		t.Fatalf("%d open loans (want exactly the limit, 3): %v", len(mine), err)
	}
	seen := map[int64]bool{}
	for _, l := range mine {
		if seen[l.CopyID] {
			t.Errorf("copy %d lent twice", l.CopyID)
		}
		seen[l.CopyID] = true
	}
}

// The database itself allows one open loan per copy.
func TestOpenLoanIsUniquePerCopy(t *testing.T) {
	r := newRig(t)
	_, cs := r.book("The Hobbit", isbnA, 1)
	lib, _ := r.user("lib", perm.Librarian)
	ins := func() error {
		_, err := r.st.DB().Exec(`INSERT INTO loans (copy_id, patron_id, checked_out_at, checked_out_by, due_date)
			VALUES (?, ?, 'x', ?, '2026-02-01')`, cs[0].ID, lib.ID, lib.ID)
		return err
	}
	if err := ins(); err != nil {
		t.Fatal(err)
	}
	if err := ins(); err == nil {
		t.Error("a second open loan of the same copy was accepted")
	}
	if _, err := r.st.DB().Exec(`UPDATE loans SET returned_at = 'y'`); err != nil {
		t.Fatal(err)
	}
	if err := ins(); err != nil {
		t.Errorf("a loan after the return: %v", err)
	}
}

package reports

import (
	"context"
	"fmt"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

var ctx = context.Background()

type rig struct {
	t   *testing.T
	st  *store.Store
	set *settings.Service
	us  *users.Service
	svc *Service
	now time.Time
	n   int
}

func newRig(t *testing.T, zone string) *rig {
	t.Helper()
	r := &rig{t: t, now: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)}
	t.Cleanup(clock.SetSource(func() time.Time { return r.now }))
	var err error
	if r.st, err = store.Open(ctx, t.TempDir(), store.Options{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.st.Close() })
	if r.set, err = settings.New(ctx, r.st); err != nil {
		t.Fatal(err)
	}
	if zone != "" {
		if err := r.set.SetTimeZone(ctx, zone); err != nil {
			t.Fatal(err)
		}
	}
	r.us = users.New(r.st)
	r.svc = New(r.st, r.set.Location, r.set.Today)
	return r
}

func (r *rig) exec(q string, args ...any) int64 {
	r.t.Helper()
	res, err := r.st.DB().Exec(q, args...)
	if err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (r *rig) user(role perm.Role) users.User {
	r.t.Helper()
	r.n++
	u, err := r.us.Create(ctx, users.NewUser{Username: fmt.Sprintf("u%d", r.n), DisplayName: fmt.Sprintf("User %d", r.n), PasswordHash: "x", Role: role})
	if err != nil {
		r.t.Fatal(err)
	}
	return u
}

func (r *rig) book() int64 {
	r.n++
	return r.exec(`INSERT INTO books (title, search_text, created_at) VALUES (?, ?, '2026-01-01T00:00:00Z')`, "B", "b")
}

func (r *rig) copyOf(book int64) int64 {
	r.n++
	return r.exec(`INSERT INTO copies (book_id, number, condition, location_id, created_at) VALUES (?, ?, 'good', 1, '2026-01-01T00:00:00Z')`, book, r.n)
}

// loan adds a loan; out and back are UTC stamps ("" for not returned).
func (r *rig) loan(copyID, patron int64, out, back, due string) {
	r.t.Helper()
	var ret any
	if back != "" {
		ret = back
	}
	r.exec(`INSERT INTO loans (copy_id, patron_id, checked_out_at, checked_out_by, due_date, returned_at) VALUES (?, ?, ?, ?, ?, ?)`,
		copyID, patron, out, patron, due, ret)
}

func (r *rig) circulation(from, to string) Circulation {
	r.t.Helper()
	c, err := r.svc.Circulation(ctx, from, to)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

// ADMIN-3 "Report": 3 checkouts and 2 returns in January show as 3 and 2.
func TestReport(t *testing.T) {
	r := newRig(t, "")
	p := r.user(perm.Patron)
	b := r.book()
	c1, c2, c3 := r.copyOf(b), r.copyOf(b), r.copyOf(b)
	r.loan(c1, p.ID, "2026-01-05T10:00:00Z", "2026-01-12T10:00:00Z", "2026-01-19")
	r.loan(c2, p.ID, "2026-01-10T10:00:00Z", "2026-01-20T10:00:00Z", "2026-01-24")
	r.loan(c3, p.ID, "2026-01-31T23:59:59Z", "", "2026-02-14")
	// outside January: checked out in December, returned in February
	r.loan(c1, p.ID, "2025-12-31T23:59:59Z", "2026-02-01T00:00:00Z", "2026-01-14")
	got := r.circulation("2026-01-01", "2026-01-31")
	if got.Checkouts != 3 || got.Returns != 2 || got.From != "2026-01-01" || got.To != "2026-01-31" {
		t.Fatalf("report %+v", got)
	}
}

func TestReportInANonUTCZoneUsesTheLibrarysMidnights(t *testing.T) {
	r := newRig(t, "America/Chicago") // UTC-6 in winter
	p := r.user(perm.Patron)
	c := r.copyOf(r.book())
	// 23:30 local on 31 January is 05:30 UTC on 1 February: still January.
	r.loan(c, p.ID, "2026-02-01T05:30:00Z", "", "2026-02-15")
	// 00:00 local on 1 February is 06:00 UTC: February. One second before is January.
	r.loan(r.copyOf(r.book()), p.ID, "2026-02-01T06:00:00Z", "", "2026-02-15")
	r.loan(r.copyOf(r.book()), p.ID, "2026-02-01T05:59:59Z", "", "2026-02-15")
	// 00:00 local on 1 January is 06:00 UTC: January. One second before is December.
	r.loan(r.copyOf(r.book()), p.ID, "2026-01-01T06:00:00Z", "", "2026-01-15")
	r.loan(r.copyOf(r.book()), p.ID, "2026-01-01T05:59:59Z", "", "2026-01-15")
	if got := r.circulation("2026-01-01", "2026-01-31"); got.Checkouts != 3 {
		t.Fatalf("January: %+v", got)
	}
	if got := r.circulation("2026-02-01", "2026-02-01"); got.Checkouts != 1 {
		t.Fatalf("1 February: %+v", got)
	}
}

func TestReportCountsReturnsAtTheLibrarysMidnightsToo(t *testing.T) {
	r := newRig(t, "Pacific/Auckland") // UTC+13 in January
	p := r.user(perm.Patron)
	// 00:00 local on 1 Jan is 11:00 UTC on 31 Dec.
	r.loan(r.copyOf(r.book()), p.ID, "2025-12-01T00:00:00Z", "2025-12-31T11:00:00Z", "2025-12-20")
	r.loan(r.copyOf(r.book()), p.ID, "2025-12-01T00:00:00Z", "2025-12-31T10:59:59Z", "2025-12-20")
	if got := r.circulation("2026-01-01", "2026-01-01"); got.Returns != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestReportAcrossAClockChange(t *testing.T) {
	r := newRig(t, "America/Chicago") // clocks go forward on 2026-03-08
	p := r.user(perm.Patron)
	// 8 March is 23 hours long: 06:00 UTC to 05:00 UTC.
	r.loan(r.copyOf(r.book()), p.ID, "2026-03-09T04:59:59Z", "", "2026-03-20")
	r.loan(r.copyOf(r.book()), p.ID, "2026-03-09T05:00:00Z", "", "2026-03-20")
	if got := r.circulation("2026-03-08", "2026-03-08"); got.Checkouts != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestReportStillCountsLoansOfRemovedCopiesAndBooks(t *testing.T) {
	r := newRig(t, "")
	p := r.user(perm.Patron)
	b := r.book()
	c := r.copyOf(b)
	r.loan(c, p.ID, "2026-01-05T10:00:00Z", "2026-01-06T10:00:00Z", "2026-01-19")
	r.exec(`UPDATE copies SET removed_at = '2026-02-01T00:00:00Z' WHERE id = ?`, c)
	r.exec(`UPDATE books SET removed_at = '2026-02-01T00:00:00Z' WHERE id = ?`, b)
	if got := r.circulation("2026-01-01", "2026-01-31"); got.Checkouts != 1 || got.Returns != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestReportRangeRules(t *testing.T) {
	r := newRig(t, "")
	for _, c := range []struct{ from, to string }{
		{"2026-01-01", "2026-01-01"}, // one day
		{"2026-01-01", "2030-12-31"}, // 5 years, both ends included
		{"2024-02-29", "2029-02-27"}, // from a leap day
	} {
		if _, err := r.svc.Circulation(ctx, c.from, c.to); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
	for _, c := range []struct{ from, to, field string }{
		{"2026-01-01", "2031-01-01", "to"}, // one day over 5 years
		{"2026-01-31", "2026-01-01", "to"}, // backwards
		{"", "2026-01-01", "from"},
		{"2026-01-01", "", "to"},
		{"2026-1-1", "2026-01-31", "from"},
		{"2026-02-30", "2026-03-31", "from"},
		{"01/02/2026", "2026-03-31", "from"},
		{"2026-01-01T00:00:00Z", "2026-03-31", "from"},
	} {
		_, err := r.svc.Circulation(ctx, c.from, c.to)
		ae, ok := err.(*apperr.Error)
		if !ok || ae.Code != apperr.Invalid || ae.Field != c.field {
			t.Errorf("%v: %v", c, err)
		}
	}
}

func TestStatistics(t *testing.T) {
	r := newRig(t, "")
	admin := r.user(perm.Administrator)
	lib := r.user(perm.Librarian)
	pat := r.user(perm.Patron)
	gone := r.user(perm.Patron)
	noBorrow := r.user(perm.Patron)
	r.exec(`UPDATE users SET active = 0 WHERE id = ?`, gone.ID)
	r.exec(`DELETE FROM user_permissions WHERE user_id = ? AND permission = ?`, noBorrow.ID, string(perm.LoansOwnView))

	b1, b2 := r.book(), r.book()
	removedBook := r.book()
	c1, c2, c3, c4 := r.copyOf(b1), r.copyOf(b1), r.copyOf(b2), r.copyOf(b2)
	r.copyOf(removedBook) // copy of a removed book isn't live
	r.exec(`UPDATE books SET removed_at = '2026-02-01T00:00:00Z' WHERE id = ?`, removedBook)
	r.exec(`UPDATE copies SET removed_at = '2026-02-01T00:00:00Z' WHERE id = ?`, c4)

	// now is 2026-03-10 12:00 UTC: due 2026-03-10 isn't overdue, 2026-03-09 is.
	r.loan(c1, pat.ID, "2026-03-01T10:00:00Z", "", "2026-03-09")
	r.loan(c2, pat.ID, "2026-03-02T10:00:00Z", "", "2026-03-10")
	r.loan(c3, pat.ID, "2026-02-01T10:00:00Z", "2026-02-05T10:00:00Z", "2026-02-15") // returned
	r.loan(c4, pat.ID, "2026-02-20T10:00:00Z", "", "2026-02-27")                     // on a removed copy: still out, still overdue

	got, err := r.svc.Statistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Statistics{Books: 2, Copies: 3, Patrons: 3, CurrentLoans: 3, OverdueLoans: 2}
	if got != want {
		t.Fatalf("got %+v want %+v (admin %d lib %d)", got, want, admin.ID, lib.ID)
	}
}

func TestOverdueUsesTheLibrarysDate(t *testing.T) {
	r := newRig(t, "Pacific/Auckland") // 2026-03-10 12:00 UTC is already 11 March there
	p := r.user(perm.Patron)
	r.loan(r.copyOf(r.book()), p.ID, "2026-03-01T10:00:00Z", "", "2026-03-10")
	got, err := r.svc.Statistics(ctx)
	if err != nil || got.OverdueLoans != 1 || got.CurrentLoans != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

// Package reports answers the administrator's questions about the library as a
// whole (ADMIN-3): how many books, copies, patrons and loans there are, and how
// many checkouts and returns happened over a range of dates. It is the one
// package that reads other areas' tables directly, with read-only SQL
// (architecture §3). It returns counts only: no patron's name or ID leaves it.
package reports

import (
	"context"
	"fmt"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

// MaxRangeYears is the longest range a circulation report may cover.
const MaxRangeYears = 5

// stampFormat is how loans store checked_out_at and returned_at: UTC, to the
// second, so comparing the text compares the instants.
const stampFormat = "2006-01-02T15:04:05Z"

// Service reads the reports.
type Service struct {
	st *store.Store
	// loc and today give the library's time zone and its current date
	// (YYYY-MM-DD); settings.Service has both.
	loc   func() *time.Location
	today func() string
}

// New makes a Service over st. loc is the library's time zone and today its
// current calendar date, both asked each time because the zone can be changed.
func New(st *store.Store, loc func() *time.Location, today func() string) *Service {
	return &Service{st: st, loc: loc, today: today}
}

// Statistics are the library's counts as of now.
type Statistics struct {
	Books        int
	Copies       int
	Patrons      int
	CurrentLoans int
	OverdueLoans int
}

// Statistics counts, as of now:
//   - books and copies that are not removed (a copy whose book is removed is
//     not live either);
//   - active patrons: active accounts that hold loans.own.view, the permission
//     the patron preset is built around. It is the permission, not the stored
//     role, so a librarian or administrator who can borrow counts too, and a
//     patron whose borrowing was switched off does not;
//   - current loans: loans not yet returned, including those of removed copies;
//   - overdue loans: current loans whose due date is before today in the
//     library's zone, the rule circulation uses.
func (s *Service) Statistics(ctx context.Context) (Statistics, error) {
	var out Statistics
	err := s.st.DB().QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM books WHERE removed_at IS NULL),
		(SELECT count(*) FROM copies c JOIN books b ON b.id = c.book_id
			WHERE c.removed_at IS NULL AND b.removed_at IS NULL),
		(SELECT count(*) FROM users u JOIN user_permissions p ON p.user_id = u.id
			WHERE u.active = 1 AND p.permission = ?),
		(SELECT count(*) FROM loans WHERE returned_at IS NULL),
		(SELECT count(*) FROM loans WHERE returned_at IS NULL AND due_date < ?)`,
		string(perm.LoansOwnView), s.today()).
		Scan(&out.Books, &out.Copies, &out.Patrons, &out.CurrentLoans, &out.OverdueLoans)
	if err != nil {
		return Statistics{}, fmt.Errorf("reading the library statistics: %w", err)
	}
	return out, nil
}

// Circulation is the number of checkouts and returns over a range of dates.
type Circulation struct {
	From, To  string // YYYY-MM-DD, both days included
	Checkouts int
	Returns   int
}

// Circulation counts the loans checked out, and the loans returned, from the
// start of day from to the end of day to, both days included, in the library's
// time zone: a loan checked out at 23:30 local on 31 January is in January
// whatever that is in UTC. Loans of removed copies count. The range is at most
// MaxRangeYears years.
func (s *Service) Circulation(ctx context.Context, from, to string) (Circulation, error) {
	loc := s.loc()
	start, err := parseDay("from", from, loc)
	if err != nil {
		return Circulation{}, err
	}
	end, err := parseDay("to", to, loc)
	if err != nil {
		return Circulation{}, err
	}
	if end.Before(start) {
		return Circulation{}, apperr.OnField(apperr.Invalid, "to", "The end date can't be before the start date.")
	}
	// from + 5 years is the first day that is too far, so to must be before it.
	if !end.Before(start.AddDate(MaxRangeYears, 0, 0)) {
		return Circulation{}, apperr.OnField(apperr.Invalid, "to",
			fmt.Sprintf("A report can cover at most %d years. Choose a shorter range.", MaxRangeYears))
	}
	// Midnight to midnight in the library's zone, found by calendar date so a
	// day with a clock change is 23 or 25 hours long, not 24.
	lo := start.UTC().Format(stampFormat)
	hi := end.AddDate(0, 0, 1).UTC().Format(stampFormat)
	out := Circulation{From: from, To: to}
	err = s.st.DB().QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM loans WHERE checked_out_at >= ? AND checked_out_at < ?),
		(SELECT count(*) FROM loans WHERE returned_at >= ? AND returned_at < ?)`,
		lo, hi, lo, hi).Scan(&out.Checkouts, &out.Returns)
	if err != nil {
		return Circulation{}, fmt.Errorf("reading the circulation report: %w", err)
	}
	return out, nil
}

// parseDay reads a YYYY-MM-DD date as midnight in loc. A date that isn't
// exactly that form, or isn't a real day, is refused.
func parseDay(field, v string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation(time.DateOnly, v, loc)
	if err != nil || t.Format(time.DateOnly) != v {
		return time.Time{}, apperr.OnField(apperr.Invalid, field, "Give the date as YYYY-MM-DD, such as 2026-01-31.")
	}
	return t, nil
}

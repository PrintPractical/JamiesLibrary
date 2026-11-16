package circ

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/store"
)

// Page sizes for lists of loans.
const (
	DefaultPageSize = 50
	MaxPageSize     = 100
	maxPage         = 100000
)

// MaxPatronResults is the most patrons one look-up lists.
const MaxPatronResults = 50

// LoanPage is one page of loans and how many there are in all.
type LoanPage struct {
	Loans    []Loan
	Total    int
	Page     int
	PageSize int
}

func checkPaging(page, size int) (int, error) {
	if size == 0 {
		size = DefaultPageSize
	}
	if size < 1 || size > MaxPageSize {
		return 0, apperr.OnField(apperr.Invalid, "per_page", fmt.Sprintf("Ask for between 1 and %d loans per page.", MaxPageSize))
	}
	if page < 1 || page > maxPage {
		return 0, apperr.OnField(apperr.Invalid, "page", "That page number is not available.")
	}
	return size, nil
}

// loanPage reads page `page` of the loans matching where (which may use `?`
// with args), newest ordering given by order.
func (s *Service) loanPage(ctx context.Context, where, order string, args []any, page, size int) (LoanPage, error) {
	size, err := checkPaging(page, size)
	if err != nil {
		return LoanPage{}, err
	}
	out := LoanPage{Loans: []Loan{}, Page: page, PageSize: size}
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM loans l WHERE `+where, args...).Scan(&out.Total); err != nil {
		return LoanPage{}, err
	}
	if (page-1)*size >= out.Total {
		return out, nil
	}
	ls, err := s.loansWhere(ctx, s.st.DB(), `WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), size, (page-1)*size)...)
	if err != nil {
		return LoanPage{}, err
	}
	s.markLate(ls)
	out.Loans = ls
	return out, nil
}

// Renew moves an open loan's due date out by the loan period (CIRC-5). It is
// refused, saying which, when the loan is overdue or already renewed the most
// times. A non-zero onlyPatron is a patron renewing their own loan: a loan that
// is anyone else's is "not found", the same as one that does not exist.
func (s *Service) Renew(ctx context.Context, loanID, onlyPatron int64) (Loan, error) {
	var out Loan
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		ls, err := s.loansWhere(ctx, tx, `WHERE l.id = ?`, loanID)
		if err != nil {
			return err
		}
		if len(ls) == 0 || (onlyPatron != 0 && ls[0].PatronID != onlyPatron) {
			return errNoSuchLoan
		}
		l := ls[0]
		if l.ReturnedAt != "" {
			return apperr.New(apperr.Conflict, "That loan was already returned, so there is nothing to renew.")
		}
		today := s.settings.Today()
		if l.DueDate < today {
			return apperr.New(apperr.Conflict, fmt.Sprintf("%s is overdue (due %s), so it can't be renewed. It has to be returned to a librarian.", l.Title, friendlyDate(l.DueDate)))
		}
		set := s.settings.Get()
		if l.Renewals >= set.MaxRenewals {
			return apperr.New(apperr.Conflict, fmt.Sprintf("%s has been renewed the most times allowed (%d), so it can't be renewed again. It has to be returned by %s.",
				l.Title, set.MaxRenewals, friendlyDate(l.DueDate)))
		}
		due, err := time.Parse(time.DateOnly, l.DueDate)
		if err != nil {
			return err
		}
		next := due.AddDate(0, 0, set.LoanPeriodDays).Format(time.DateOnly)
		// the conditions again, in the statement: the last guard on the limit
		r, err := tx.ExecContext(ctx, `UPDATE loans SET due_date = ?, renewals = renewals + 1
			WHERE id = ? AND returned_at IS NULL AND renewals < ? AND due_date >= ?`, next, l.ID, set.MaxRenewals, today)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n != 1 {
			return apperr.New(apperr.Conflict, "That loan changed while it was being renewed. Look at it again.")
		}
		l.DueDate, l.Renewals = next, l.Renewals+1
		out = l
		return nil
	})
	return out, err
}

var errNoSuchLoan = apperr.New(apperr.NotFound, "There is no such loan.")

// Overdue lists open loans past their due date, the longest overdue first
// (CIRC-6). Overdue is worked out from the date in the library's zone.
func (s *Service) Overdue(ctx context.Context, page, size int) (LoanPage, error) {
	return s.loanPage(ctx, `l.returned_at IS NULL AND l.due_date < ?`, `l.due_date, l.id`, []any{s.settings.Today()}, page, size)
}

// History lists every loan a patron has had, open or returned, newest first
// (PATRON-1, CIRC-3). A loan of a copy since removed is still there.
func (s *Service) History(ctx context.Context, patronID int64, page, size int) (LoanPage, error) {
	return s.loanPage(ctx, `l.patron_id = ?`, `l.checked_out_at DESC, l.id DESC`, []any{patronID}, page, size)
}

// PatronMatch is a patron found by a look-up, with how much they have out.
type PatronMatch struct {
	ID          int64
	Username    string
	DisplayName string
	Card        string // "" when they have none
	Active      bool
	Out         int
	Overdue     int
}

// FindPatrons looks up patrons by part of a name (display name or username,
// ignoring case) or by a card number (CIRC-3). At most MaxPatronResults come back.
func (s *Service) FindPatrons(ctx context.Context, query string) ([]PatronMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperr.OnField(apperr.Invalid, "q", "Type a name or a card number to look for.")
	}
	if utf8.RuneCountInString(query) > catalog.MaxSearchTerm {
		return nil, apperr.OnField(apperr.Invalid, "q", "The search is too long: at most 200 characters.")
	}
	for _, r := range query {
		if r < 0x20 || r == 0x7f {
			return nil, apperr.OnField(apperr.Invalid, "q", "The search has a character that is not allowed.")
		}
	}
	rows, err := s.st.DB().QueryContext(ctx, `
		SELECT u.id, u.username, u.display_name, COALESCE(i.code, ''), u.active,
			(SELECT count(*) FROM loans WHERE patron_id = u.id AND returned_at IS NULL),
			(SELECT count(*) FROM loans WHERE patron_id = u.id AND returned_at IS NULL AND due_date < ?)
		FROM users u LEFT JOIN identifiers i ON i.user_id = u.id AND i.kind = ?
		WHERE i.code = ? OR u.display_name LIKE ? ESCAPE '\' OR u.username LIKE ? ESCAPE '\'
		ORDER BY u.display_name COLLATE NOCASE, u.id LIMIT ?`,
		s.settings.Today(), catalog.KindCard, strings.ToUpper(query), "%"+likeEscape(query)+"%", "%"+likeEscape(query)+"%", MaxPatronResults)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PatronMatch{}
	for rows.Next() {
		var p PatronMatch
		if err := rows.Scan(&p.ID, &p.Username, &p.DisplayName, &p.Card, &p.Active, &p.Out, &p.Overdue); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// PatronRecord is what a librarian sees of one patron: who they are, what they
// have out, which of it is overdue, and (a page at a time) what they have had.
type PatronRecord struct {
	Patron  PatronMatch
	Current []Loan
	Overdue []Loan
}

// PatronRecord reads a patron's current and overdue loans. Their history is
// History.
func (s *Service) PatronRecord(ctx context.Context, patronID int64) (PatronRecord, error) {
	u, err := s.users.Get(ctx, patronID)
	if err != nil {
		return PatronRecord{}, err
	}
	cur, err := s.OpenLoansOf(ctx, patronID)
	if err != nil {
		return PatronRecord{}, err
	}
	rec := PatronRecord{Current: cur, Overdue: []Loan{},
		Patron: PatronMatch{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Active: u.Active, Out: len(cur)}}
	for _, l := range cur {
		if l.DaysLate > 0 {
			rec.Overdue = append(rec.Overdue, l)
		}
	}
	rec.Patron.Overdue = len(rec.Overdue)
	if rec.Patron.Card, err = s.users.Card(ctx, patronID); err != nil {
		if e, ok := apperr.As(err); !ok || e.Code != apperr.NotFound {
			return PatronRecord{}, err
		}
	}
	return rec, nil
}

// SelfCheckout lends the code's copy to the patron themselves (CIRC-7). It is
// refused while the setting is off. The borrowing policy is CIRC-1's.
func (s *Service) SelfCheckout(ctx context.Context, patronID int64, code string) (Loan, error) {
	if !s.settings.Get().SelfCheckout {
		return Loan{}, apperr.New(apperr.Forbidden, "Self-checkout is switched off in this library. Take the book to a librarian.")
	}
	u, err := s.users.Get(ctx, patronID)
	if err != nil {
		return Loan{}, err
	}
	return s.lend(ctx, patronID, Patron{ID: u.ID, DisplayName: u.DisplayName, Active: u.Active}, code, true)
}

// Package circ owns loans and what happens at the circulation desk: working
// out what a scanned code identifies, checking a copy out to a patron and
// checking it back in. It never sees HTTP. The catalog asks it, through
// Loans, whether a copy is on loan.
package circ

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

// Loans answers the catalog's questions about loans (catalog.Loans).
type Loans struct{}

// LoanedCopies is how many copies of the book are on loan now.
func (Loans) LoanedCopies(ctx context.Context, q catalog.DB, bookID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM loans l JOIN copies c ON c.id = l.copy_id
		WHERE c.book_id = ? AND l.returned_at IS NULL`, bookID).Scan(&n)
	return n, err
}

// OnLoan reports whether the copy is on loan now.
func (Loans) OnLoan(ctx context.Context, q catalog.DB, copyID int64) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM loans WHERE copy_id = ? AND returned_at IS NULL`, copyID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// DueDates maps each of the book's copies on loan now to its due date.
func (Loans) DueDates(ctx context.Context, q catalog.DB, bookID int64) (map[int64]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT l.copy_id, l.due_date FROM loans l JOIN copies c ON c.id = l.copy_id
		WHERE c.book_id = ? AND l.returned_at IS NULL`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var due string
		if err := rows.Scan(&id, &due); err != nil {
			return nil, err
		}
		out[id] = due
	}
	return out, rows.Err()
}

// Steps of the desk: what the scanned code is meant to identify.
const (
	StepPatron = "patron"
	StepItem   = "item"
)

// Options are what a Service needs; all are required.
type Options struct {
	Settings *settings.Service
	Catalog  *catalog.Service
	Users    *users.Service
}

// Service is circulation's rules over a store.
type Service struct {
	st       *store.Store
	settings *settings.Service
	catalog  *catalog.Service
	users    *users.Service
}

// New makes a Service.
func New(st *store.Store, o Options) *Service {
	if o.Settings == nil || o.Catalog == nil || o.Users == nil {
		panic("circ.New: Options.Settings, Catalog and Users are required")
	}
	return &Service{st: st, settings: o.Settings, catalog: o.Catalog, users: o.Users}
}

var (
	errNoSuchPatron = apperr.New(apperr.NotFound, "No such patron: no library card has that number. Check the card, or type the number again.")
	errNoMatch      = apperr.New(apperr.NotFound, "Nothing matches that code. Check the label, or type it again.")
	errOwnBarcode   = apperr.New(apperr.Invalid, "That is a book's ISBN, and this library identifies copies by their own barcodes. Scan the copy's own barcode.")
)

// Resolved is what a code identifies: exactly one of Patron, Copy or Book is set.
type Resolved struct {
	Patron *Patron
	Copy   *CopyInfo
	Book   *BookInfo
}

// Patron is who a card identifies, with how much they have out.
type Patron struct {
	ID          int64
	DisplayName string
	Card        string
	Active      bool
	Out         int // open loans
	Overdue     int // of those, past due
	Limit       int // the most items a patron may have out
}

// CopyInfo is a copy with its book, for showing at the desk.
type CopyInfo struct {
	ID       int64
	Number   int
	Code     string
	BookID   int64
	Title    string
	Authors  []string
	Location string
	OnLoan   bool
}

// BookInfo is a book identified by its ISBN.
type BookInfo struct {
	ID        int64
	Title     string
	Authors   []string
	ISBN      string
	Copies    int
	Available int
}

// Resolve says what a scanned or typed code identifies at a step of the desk
// (SCAN-3, SCAN-4, CAT-9). At the patron step only a card matches. At the item
// step a copy's code matches first; then, in ISBN mode, a valid ISBN matches a
// book, while in own-barcode mode an ISBN is told to scan the copy's own barcode.
func (s *Service) Resolve(ctx context.Context, code, step string) (Resolved, error) {
	switch step {
	case StepPatron:
		p, err := s.patronByCard(ctx, s.st.DB(), code)
		return Resolved{Patron: &p}, err
	case StepItem:
		return s.resolveItem(ctx, s.st.DB(), code)
	}
	return Resolved{}, apperr.OnField(apperr.Invalid, "step", `The step must be "patron" or "item".`)
}

func (s *Service) patronByCard(ctx context.Context, q catalog.DB, code string) (Patron, error) {
	u, err := s.users.PatronByCard(ctx, code)
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == apperr.NotFound {
			return Patron{}, errNoSuchPatron
		}
		return Patron{}, err
	}
	p := Patron{ID: u.ID, DisplayName: u.DisplayName, Active: u.Active, Limit: s.settings.Get().MaxLoans}
	if p.Card, err = catalog.NormaliseCode(code); err != nil {
		return Patron{}, err
	}
	err = q.QueryRowContext(ctx, `SELECT count(*), COALESCE(SUM(due_date < ?), 0) FROM loans
		WHERE patron_id = ? AND returned_at IS NULL`, s.settings.Today(), u.ID).Scan(&p.Out, &p.Overdue)
	return p, err
}

func (s *Service) resolveItem(ctx context.Context, q catalog.DB, code string) (Resolved, error) {
	c, ok, err := s.catalog.LookupCodeIn(ctx, q, code)
	if err != nil {
		return Resolved{}, err
	}
	if ok {
		info, err := s.copyInfo(ctx, q, c)
		return Resolved{Copy: &info}, err
	}
	isbn, perr := catalog.ParseISBN(code)
	if perr != nil {
		return Resolved{}, errNoMatch
	}
	if s.settings.Get().CopyIDMode != settings.CopyIDISBN {
		return Resolved{}, errOwnBarcode
	}
	b, ok, err := s.catalog.BookByISBNIn(ctx, q, isbn)
	if err != nil {
		return Resolved{}, err
	}
	if !ok {
		return Resolved{}, errNoMatch
	}
	copies, err := s.catalog.CopiesIn(ctx, q, b.ID)
	if err != nil {
		return Resolved{}, err
	}
	info := BookInfo{ID: b.ID, Title: b.Title, Authors: b.Authors, ISBN: b.ISBN13, Copies: len(copies)}
	for _, c := range copies {
		if !c.OnLoan {
			info.Available++
		}
	}
	return Resolved{Book: &info}, nil
}

func (s *Service) copyInfo(ctx context.Context, q catalog.DB, c catalog.Copy) (CopyInfo, error) {
	b, err := s.catalog.GetBookIn(ctx, q, c.BookID)
	if err != nil {
		return CopyInfo{}, err
	}
	return CopyInfo{ID: c.ID, Number: c.Number, Code: c.Code, BookID: c.BookID, Title: b.Title,
		Authors: b.Authors, Location: c.LocationName, OnLoan: c.OnLoan}, nil
}

// Loan is one loan with what the desk shows of it.
type Loan struct {
	ID           int64
	CopyID       int64
	CopyNumber   int
	CopyCode     string
	BookID       int64
	Title        string
	Authors      []string
	Location     string
	PatronID     int64
	PatronName   string
	CheckedOutAt string
	DueDate      string
	Renewals     int
	ReturnedAt   string // "" while open
	DaysLate     int    // for an open loan today, or at its return
}

const loanSelect = `SELECT l.id, l.copy_id, c.number, COALESCE(i.code, ''), c.book_id, b.title,
		COALESCE(loc.name, ''), l.patron_id, u.display_name, l.checked_out_at, l.due_date, l.renewals,
		COALESCE(l.returned_at, '')
	FROM loans l JOIN copies c ON c.id = l.copy_id JOIN books b ON b.id = c.book_id
	JOIN users u ON u.id = l.patron_id
	LEFT JOIN identifiers i ON i.copy_id = c.id LEFT JOIN locations loc ON loc.id = c.location_id `

func (s *Service) loansWhere(ctx context.Context, q catalog.DB, where string, args ...any) ([]Loan, error) {
	rows, err := q.QueryContext(ctx, loanSelect+where, args...)
	if err != nil {
		return nil, err
	}
	var out []Loan
	for rows.Next() {
		var l Loan
		if err := rows.Scan(&l.ID, &l.CopyID, &l.CopyNumber, &l.CopyCode, &l.BookID, &l.Title, &l.Location,
			&l.PatronID, &l.PatronName, &l.CheckedOutAt, &l.DueDate, &l.Renewals, &l.ReturnedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Authors, err = s.authorsOf(ctx, q, out[i].BookID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) authorsOf(ctx context.Context, q catalog.DB, bookID int64) ([]string, error) {
	b, err := s.catalog.GetBookIn(ctx, q, bookID)
	if err != nil {
		// a removed book keeps its loans but has no live record to read authors from
		if e, ok := apperr.As(err); ok && e.Code == apperr.NotFound {
			return []string{}, nil
		}
		return nil, err
	}
	return b.Authors, nil
}

// OpenLoansOf lists a patron's open loans, earliest due first. It reads
// whether or not the patron is still active.
func (s *Service) OpenLoansOf(ctx context.Context, patronID int64) ([]Loan, error) {
	ls, err := s.loansWhere(ctx, s.st.DB(), `WHERE l.patron_id = ? AND l.returned_at IS NULL ORDER BY l.due_date, l.id`, patronID)
	if err != nil {
		return nil, err
	}
	s.markLate(ls)
	return ls, nil
}

// markLate sets DaysLate on open loans: whole days between the due date and
// today in the library's zone, 0 when not past due.
func (s *Service) markLate(ls []Loan) {
	for i := range ls {
		if ls[i].ReturnedAt == "" {
			ls[i].DaysLate = daysLate(ls[i].DueDate, s.settings.Today())
		}
	}
}

// daysLate is how many calendar days after due the date `on` is, or 0.
func daysLate(due, on string) int {
	d, err1 := time.Parse(time.DateOnly, due)
	t, err2 := time.Parse(time.DateOnly, on)
	if err1 != nil || err2 != nil || !t.After(d) {
		return 0
	}
	return int(t.Sub(d).Hours() / 24)
}

func stamp() string { return clock.Now().UTC().Format("2006-01-02T15:04:05Z") }

// Checkout lends a copy to the patron the card names (CIRC-1, CIRC-4), in one
// transaction. In ISBN mode a scanned ISBN lends the book's lowest-numbered
// copy that is on the shelf. Everything refused says what to do next.
func (s *Service) Checkout(ctx context.Context, actor int64, card, code string) (Loan, error) {
	patron, err := s.patronByCard(ctx, s.st.DB(), card)
	if err != nil {
		return Loan{}, err
	}
	return s.lend(ctx, actor, patron, code, false)
}

// lend is the checkout transaction. self is a patron lending to themselves:
// the refusals then speak to them, and send returns to the desk.
func (s *Service) lend(ctx context.Context, actor int64, patron Patron, code string, self bool) (Loan, error) {
	var out Loan
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		active, err := s.users.IsActiveTx(ctx, tx, patron.ID)
		if err != nil {
			return err
		}
		if !active {
			if self {
				return apperr.New(apperr.Conflict, "Your account is deactivated, so nothing can be checked out to you. Ask an administrator to reactivate it.")
			}
			return apperr.New(apperr.Conflict, patron.DisplayName+"'s account is deactivated, so nothing can be checked out to them. An administrator can reactivate it.")
		}
		it, err := s.resolveItem(ctx, tx, code)
		if err != nil {
			return err
		}
		var copyID int64
		switch {
		case it.Copy != nil:
			if it.Copy.OnLoan {
				if self { // another patron's name is not for a patron to read
					return apperr.New(apperr.Conflict, it.Copy.Title+" is already out. If it's in your hand, give it to a librarian to check in.")
				}
				return s.alreadyOut(ctx, tx, *it.Copy)
			}
			copyID = it.Copy.ID
		default:
			copies, err := s.catalog.CopiesIn(ctx, tx, it.Book.ID)
			if err != nil {
				return err
			}
			for _, c := range copies { // by number: the lowest that is on the shelf
				if !c.OnLoan {
					copyID = c.ID
					break
				}
			}
			if it.Book.Copies == 0 {
				if self {
					return apperr.New(apperr.Conflict, it.Book.Title+" has no copies in the library to lend. Ask a librarian.")
				}
				return apperr.New(apperr.Conflict, it.Book.Title+" has no copies in the library to lend. Add a copy to the catalog first.")
			}
			if copyID == 0 {
				hint := "Check one in first, or wait for a return."
				if self {
					hint = "Ask a librarian, or try again after a return."
				}
				return apperr.New(apperr.Conflict, fmt.Sprintf("No copy of %s is available: %s out. %s",
					it.Book.Title, plural(it.Book.Copies, "the only copy is", "all %d copies are"), hint))
			}
		}
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM loans WHERE patron_id = ? AND returned_at IS NULL`, patron.ID).Scan(&open); err != nil {
			return err
		}
		if max := s.settings.Get().MaxLoans; open >= max {
			if self {
				return apperr.New(apperr.Conflict, fmt.Sprintf("Limit reached: you already have %d of %d items out. Return one to a librarian before borrowing another.", open, max))
			}
			return apperr.New(apperr.Conflict, fmt.Sprintf("Limit reached: %s already has %d of %d items out. Check one in before lending another.",
				patron.DisplayName, open, max))
		}
		due, err := s.dueDate()
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO loans (copy_id, patron_id, checked_out_at, checked_out_by, due_date)
			VALUES (?, ?, ?, ?, ?)`, copyID, patron.ID, stamp(), actor, due)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		ls, err := s.loansWhere(ctx, tx, `WHERE l.id = ?`, id)
		if err != nil || len(ls) != 1 {
			return fmt.Errorf("reading back loan %d: %v", id, err)
		}
		out = ls[0]
		return nil
	})
	return out, err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

// dueDate is today in the library's zone plus the loan period, as a calendar date.
func (s *Service) dueDate() (string, error) {
	today, err := time.Parse(time.DateOnly, s.settings.Today())
	if err != nil {
		return "", err
	}
	return today.AddDate(0, 0, s.settings.Get().LoanPeriodDays).Format(time.DateOnly), nil
}

// alreadyOut is the refusal for a copy that is on loan: who has it and what to do.
func (s *Service) alreadyOut(ctx context.Context, q catalog.DB, c CopyInfo) error {
	ls, err := s.loansWhere(ctx, q, `WHERE l.copy_id = ? AND l.returned_at IS NULL`, c.ID)
	if err != nil {
		return err
	}
	if len(ls) == 0 {
		return apperr.New(apperr.Conflict, c.Title+" is already out.")
	}
	l := ls[0]
	return apperr.New(apperr.Conflict, fmt.Sprintf("%s is already out: copy %d is on loan to %s, due %s. If it's in your hand, it was returned without being checked in: check it in first, then scan it again.",
		c.Title, c.Number, l.PatronName, l.DueDate))
}

// Check-in outcomes.
const (
	Returned = "returned" // Loan is the loan that was closed
	Choose   = "choose"   // several copies of the book are out: Choices lists them
	NotOut   = "not_out"  // nothing to return; Copy or Book says what was scanned
)

// CheckinResult is what a check-in did.
type CheckinResult struct {
	Status  string
	Message string
	Loan    *Loan     // Returned
	Choices []Loan    // Choose
	Copy    *CopyInfo // NotOut, by a copy's code
	Book    *BookInfo // NotOut, by ISBN
}

// Checkin returns what the code identifies (CIRC-2). A copy's code returns its
// open loan. A book's ISBN returns its only open loan; with several out the
// result lists them to choose from, and loanID (a choice) or card (a patron's
// card, to narrow them) says which one. A copy that isn't out changes nothing.
func (s *Service) Checkin(ctx context.Context, actor int64, code string, loanID int64, card string) (CheckinResult, error) {
	var narrow *Patron
	if card != "" {
		p, err := s.patronByCard(ctx, s.st.DB(), card)
		if err != nil {
			return CheckinResult{}, err
		}
		narrow = &p
	}
	var res CheckinResult
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		res = CheckinResult{}
		it, err := s.resolveItem(ctx, tx, code)
		if err != nil {
			return err
		}
		var open []Loan
		if it.Copy != nil {
			open, err = s.loansWhere(ctx, tx, `WHERE l.copy_id = ? AND l.returned_at IS NULL`, it.Copy.ID)
			if err != nil {
				return err
			}
			if len(open) == 0 {
				res = CheckinResult{Status: NotOut, Copy: it.Copy,
					Message: fmt.Sprintf("%s (copy %d) wasn't checked out, so nothing changed.", it.Copy.Title, it.Copy.Number)}
				return nil
			}
			if loanID != 0 && loanID != open[0].ID {
				return apperr.OnField(apperr.Invalid, "loan_id", "That loan is not this copy's. Scan the copy again.")
			}
		} else {
			open, err = s.loansWhere(ctx, tx, `WHERE c.book_id = ? AND l.returned_at IS NULL ORDER BY c.number`, it.Book.ID)
			if err != nil {
				return err
			}
			if len(open) == 0 {
				res = CheckinResult{Status: NotOut, Book: it.Book,
					Message: "None is checked out: no copy of " + it.Book.Title + " is on loan."}
				return nil
			}
			if loanID != 0 {
				open = pick(open, func(l Loan) bool { return l.ID == loanID })
				if len(open) == 0 {
					return apperr.OnField(apperr.Invalid, "loan_id", "That loan is not an open loan of this book. Scan the book again to see who has it.")
				}
			} else if narrow != nil {
				mine := pick(open, func(l Loan) bool { return l.PatronID == narrow.ID })
				if len(mine) == 0 {
					return apperr.New(apperr.Conflict, fmt.Sprintf("%s doesn't have %s out. Choose a loan from the list.", narrow.DisplayName, it.Book.Title))
				}
				open = mine
			}
			if len(open) > 1 {
				s.markLate(open)
				res = CheckinResult{Status: Choose, Choices: open,
					Message: fmt.Sprintf("%d copies of %s are out. Scan the patron's card, or choose which loan is being returned.", len(open), it.Book.Title)}
				return nil
			}
		}
		l := open[0]
		late := daysLate(l.DueDate, s.settings.Today())
		r, err := tx.ExecContext(ctx, `UPDATE loans SET returned_at = ?, returned_by = ? WHERE id = ? AND returned_at IS NULL`, stamp(), actor, l.ID)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n != 1 {
			return apperr.New(apperr.Conflict, "That loan was just returned by someone else.")
		}
		l.ReturnedAt = stamp()
		l.DaysLate = late
		res = CheckinResult{Status: Returned, Loan: &l, Message: "Checked in."}
		return nil
	})
	return res, err
}

func pick(ls []Loan, keep func(Loan) bool) []Loan {
	var out []Loan
	for _, l := range ls {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

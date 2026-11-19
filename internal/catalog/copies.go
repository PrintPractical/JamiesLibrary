package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
)

// Conditions are the states a copy can be in, best first.
var Conditions = []string{"new", "good", "fair", "worn", "poor", "damaged"}

// DefaultCondition is the condition a copy has when none is given.
const DefaultCondition = "good"

// Copy is one physical copy of a book.
type Copy struct {
	ID           int64
	BookID       int64
	Number       int // 1, 2, 3... within the book
	Condition    string
	LocationID   int64
	LocationName string
	Code         string // the copy's scannable code, "" when it has none
	OnLoan       bool
}

// CopyInput is what a librarian saves about a copy.
type CopyInput struct {
	LocationID int64
	// NewLocation names a location to create and put the copy in, instead of
	// LocationID. A name a location already has is refused.
	NewLocation string
	Condition   string // "" means DefaultCondition when adding, unchanged when updating
	// Sticker is a barcode already on the copy. When adding, "" asks for a
	// generated code if the copy needs one; when updating, "" leaves the code as it is.
	Sticker string
}

var errCopyNotFound = apperr.New(apperr.NotFound, "That copy is not in the catalog.")

const copyColumns = `c.id, c.book_id, c.number, c.condition, c.location_id, COALESCE(l.name, ''), COALESCE(i.code, '')`
const copyFrom = ` FROM copies c LEFT JOIN locations l ON l.id = c.location_id LEFT JOIN identifiers i ON i.copy_id = c.id`

func (s *Service) stamp() string { return s.now().UTC().Format("2006-01-02T15:04:05Z") }

func scanCopies(rows *sql.Rows) ([]Copy, error) {
	defer rows.Close()
	out := []Copy{}
	for rows.Next() {
		var c Copy
		if err := rows.Scan(&c.ID, &c.BookID, &c.Number, &c.Condition, &c.LocationID, &c.LocationName, &c.Code); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// copiesWhere reads the live copies a condition on the copies table picks.
func (s *Service) copiesWhere(ctx context.Context, q DB, where string, args ...any) ([]Copy, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+copyColumns+copyFrom+` WHERE c.removed_at IS NULL AND `+where+` ORDER BY c.book_id, c.number`, args...)
	if err != nil {
		return nil, err
	}
	cs, err := scanCopies(rows)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		if cs[i].OnLoan, err = s.loans.OnLoan(ctx, q, cs[i].ID); err != nil {
			return nil, err
		}
	}
	return cs, nil
}

// CopiesOf lists a book's copies by number, with where each is and whether it
// is on loan. A book that isn't in the catalog is not found.
func (s *Service) CopiesOf(ctx context.Context, bookID int64) ([]Copy, error) {
	if err := mustExist(ctx, s.st.DB(), bookID); err != nil {
		return nil, err
	}
	return s.copiesWhere(ctx, s.st.DB(), `c.book_id = ?`, bookID)
}

// GetCopy returns one copy that is in the catalog.
func (s *Service) GetCopy(ctx context.Context, id int64) (Copy, error) {
	return s.getCopy(ctx, s.st.DB(), id)
}

func (s *Service) getCopy(ctx context.Context, q DB, id int64) (Copy, error) {
	cs, err := s.copiesWhere(ctx, q, `c.id = ?`, id)
	if err != nil {
		return Copy{}, err
	}
	if len(cs) == 0 {
		return Copy{}, errCopyNotFound
	}
	return cs[0], nil
}

func cleanCondition(c string, def string) (string, error) {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == "" {
		c = def
	}
	for _, ok := range Conditions {
		if c == ok {
			return c, nil
		}
	}
	return "", apperr.OnField(apperr.Invalid, "condition", "Choose a condition: "+strings.Join(Conditions, ", ")+".")
}

// cleanSticker normalises an entered barcode; "" stays "".
func cleanSticker(in string) (string, error) {
	if strings.TrimSpace(in) == "" {
		return "", nil
	}
	code, err := NormaliseCode(in)
	if err != nil {
		return "", apperr.OnField(apperr.Invalid, "sticker", err.Error())
	}
	return code, nil
}

func mustHaveLocation(ctx context.Context, q DB, id int64) error {
	if id == 0 {
		return apperr.OnField(apperr.Invalid, "location", "Choose where the copy is kept.")
	}
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM locations WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.OnField(apperr.Invalid, "location", "That location does not exist.")
	}
	return err
}

// resolveLocation is the location a copy is saved into: the one named by ID,
// or a new one made now, in the caller's transaction, so a refused copy leaves
// no location behind.
func resolveLocation(ctx context.Context, tx DB, in CopyInput) (int64, error) {
	if strings.TrimSpace(in.NewLocation) == "" {
		return in.LocationID, mustHaveLocation(ctx, tx, in.LocationID)
	}
	if in.LocationID != 0 {
		return 0, apperr.OnField(apperr.Invalid, "new_location", "Choose an existing location or name a new one, not both.")
	}
	l, err := insertLocation(ctx, tx, in.NewLocation, "new_location")
	return l.ID, err
}

// copyIDMode reads the mode inside the transaction, so a switch and an add
// can't disagree.
func copyIDMode(ctx context.Context, q DB) (string, error) {
	var mode string
	err := q.QueryRowContext(ctx, `SELECT copy_id_mode FROM settings WHERE id = 1`).Scan(&mode)
	return mode, err
}

// needsCode is whether a copy of the book must carry a code of its own: in
// own-barcode mode every copy does, and a book without an ISBN always does.
func needsCode(ctx context.Context, q DB, bookID int64) (bool, error) {
	mode, err := copyIDMode(ctx, q)
	if err != nil {
		return false, err
	}
	if mode == settings.CopyIDOwn {
		return true, nil
	}
	var isbn sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT isbn13 FROM books WHERE id = ?`, bookID).Scan(&isbn); err != nil {
		return false, err
	}
	return !isbn.Valid, nil
}

// AddCopy adds a copy to a book, numbered after the book's last. It gets a
// code when the library is in own-barcode mode or the book has no ISBN: the
// entered sticker, or a generated one. Otherwise it gets one only if a sticker
// is given. A sticker any other copy or any card already has is refused.
func (s *Service) AddCopy(ctx context.Context, actor, bookID int64, in CopyInput) (Copy, error) {
	cond, err := cleanCondition(in.Condition, DefaultCondition)
	if err != nil {
		return Copy{}, err
	}
	sticker, err := cleanSticker(in.Sticker)
	if err != nil {
		return Copy{}, err
	}
	var id int64
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := mustExist(ctx, tx, bookID); err != nil {
			return err
		}
		locID, err := resolveLocation(ctx, tx, in)
		if err != nil {
			return err
		}
		if sticker != "" {
			if err := checkCodeFree(ctx, tx, sticker, 0); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO copies (book_id, number, condition, location_id, created_at)
			VALUES (?, (SELECT COALESCE(MAX(number), 0) + 1 FROM copies WHERE book_id = ?), ?, ?, ?)`,
			bookID, bookID, cond, locID, s.stamp())
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return s.assignCode(ctx, tx, id, bookID, sticker)
	})
	if err != nil {
		return Copy{}, err
	}
	return s.GetCopy(ctx, id)
}

// assignCode gives a copy the sticker when there is one, else a generated code
// when the copy needs one and has none.
func (s *Service) assignCode(ctx context.Context, tx DB, copyID, bookID int64, sticker string) error {
	if sticker != "" {
		return setCopyCode(ctx, tx, copyID, sticker)
	}
	need, err := needsCode(ctx, tx, bookID)
	if err != nil || !need {
		return err
	}
	return giveGeneratedCode(ctx, tx, copyID)
}

// UpdateCopy changes a copy's condition, location and, when a sticker is
// given, its code. Its number never changes.
func (s *Service) UpdateCopy(ctx context.Context, actor, copyID int64, in CopyInput) (Copy, error) {
	sticker, err := cleanSticker(in.Sticker)
	if err != nil {
		return Copy{}, err
	}
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		cur, err := s.getCopy(ctx, tx, copyID)
		if err != nil {
			return err
		}
		cond, err := cleanCondition(in.Condition, cur.Condition)
		if err != nil {
			return err
		}
		locID, err := resolveLocation(ctx, tx, in)
		if err != nil {
			return err
		}
		if sticker != "" {
			if err := checkCodeFree(ctx, tx, sticker, copyID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE copies SET condition = ?, location_id = ? WHERE id = ?`, cond, locID, copyID); err != nil {
			return err
		}
		return s.assignCode(ctx, tx, copyID, cur.BookID, sticker)
	})
	if err != nil {
		return Copy{}, err
	}
	return s.GetCopy(ctx, copyID)
}

// RemoveCopy removes a copy from the catalog (its row stays, marked, with its
// loans) and frees its code. A copy on loan is refused.
func (s *Service) RemoveCopy(ctx context.Context, actor, copyID int64) error {
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		c, err := s.getCopy(ctx, tx, copyID)
		if err != nil {
			return err
		}
		if c.OnLoan {
			return apperr.New(apperr.Conflict, fmt.Sprintf("Copy %d is on loan and can't be removed: check it in first.", c.Number))
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM identifiers WHERE copy_id = ?`, copyID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE copies SET removed_at = ? WHERE id = ?`, s.stamp(), copyID)
		return err
	})
}

// MoveCopies moves every copy in one location to another and says how many
// moved.
func (s *Service) MoveCopies(ctx context.Context, actor, from, to int64) (int, error) {
	if from == to {
		return 0, apperr.OnField(apperr.Invalid, "to", "Choose a different location to move the copies to.")
	}
	var n int64
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := locationExists(ctx, tx, from); err != nil {
			return err
		}
		if err := mustHaveLocation(ctx, tx, to); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE copies SET location_id = ? WHERE location_id = ? AND removed_at IS NULL`, to, from)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}

// Audit writes the activity entry for a change, inside the transaction that
// makes it, so the change and its entry stand or fall together.
type Audit func(ctx context.Context, tx store.Tx, details map[string]any) error

// SetCopyIDMode switches how copies are identified (CAT-9), in one
// transaction. Switching to own-barcode mode gives every copy without a code a
// generated one; switching to ISBN mode changes only the setting. Copies and
// loans are untouched. audit, when it isn't nil, writes the activity entry in
// the same transaction: the mode before and after, and how many copies were
// given a code.
func (s *Service) SetCopyIDMode(ctx context.Context, actor int64, mode string, audit Audit) error {
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		was, err := copyIDMode(ctx, tx)
		if err != nil {
			return err
		}
		if err := s.settings.SetCopyIDModeTx(ctx, tx, mode); err != nil {
			return err
		}
		coded := 0
		if mode == settings.CopyIDOwn {
			if coded, err = codeBareCopies(ctx, tx); err != nil {
				return err
			}
		}
		if audit == nil {
			return nil
		}
		return audit(ctx, tx, map[string]any{"copy_id_mode": map[string]any{"from": was, "to": mode}, "copies_given_ids": coded})
	})
	if err != nil {
		return err
	}
	return s.settings.Reload(ctx)
}

// codeBareCopies gives every live copy without a code a generated one, and
// says how many that was.
func codeBareCopies(ctx context.Context, tx store.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.id FROM copies c WHERE c.removed_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM identifiers i WHERE i.copy_id = c.id) ORDER BY c.id`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := giveGeneratedCode(ctx, tx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// LookupCode finds the copy a scanned code identifies. A code that is no
// copy's (a card's, unknown, or not a valid code at all) finds none.
func (s *Service) LookupCode(ctx context.Context, code string) (Copy, bool, error) {
	return s.LookupCodeIn(ctx, s.st.DB(), code)
}

// LookupCodeIn is LookupCode inside the caller's transaction.
func (s *Service) LookupCodeIn(ctx context.Context, q DB, code string) (Copy, bool, error) {
	code, err := NormaliseCode(code)
	if err != nil {
		return Copy{}, false, nil
	}
	var id int64
	err = q.QueryRowContext(ctx, `SELECT copy_id FROM identifiers WHERE code = ? AND kind = 'copy'`, code).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Copy{}, false, nil
	}
	if err != nil {
		return Copy{}, false, err
	}
	c, err := s.getCopy(ctx, q, id)
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == apperr.NotFound {
			return Copy{}, false, nil
		}
		return Copy{}, false, err
	}
	return c, true, nil
}

// CopiesIn is a book's live copies by number, read inside the caller's
// transaction, with each one's location and whether it is on loan.
func (s *Service) CopiesIn(ctx context.Context, q DB, bookID int64) ([]Copy, error) {
	return s.copiesWhere(ctx, q, `c.book_id = ?`, bookID)
}

// GetCopyIn is GetCopy inside the caller's transaction.
func (s *Service) GetCopyIn(ctx context.Context, q DB, id int64) (Copy, error) {
	return s.getCopy(ctx, q, id)
}

// countByLocation is how many copies each location holds; a location with
// none is missing.
func countByLocation(ctx context.Context, q DB) (map[int64]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT location_id, count(*) FROM copies WHERE removed_at IS NULL GROUP BY location_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// hasCopyAtSQL is a condition on the books table that holds for a book with a
// copy in the location bound to its one "?".
const hasCopyAtSQL = `EXISTS (SELECT 1 FROM copies c WHERE c.book_id = books.id AND c.location_id = ? AND c.removed_at IS NULL)`

// CopyCount is how many live copies a book has and how many are on loan.
type CopyCount struct {
	Copies int
	Loaned int
}

// Available is how many of the copies can be borrowed now.
func (c CopyCount) Available() int { return c.Copies - c.Loaned }

// InList is the placeholders and arguments for an IN condition over ids.
func InList(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

// liveCopyCounts counts live copies per book in one statement, with none on
// loan; it is the answer when there are no loans.
func liveCopyCounts(ctx context.Context, q DB, ids []int64) (map[int64]CopyCount, error) {
	out := map[int64]CopyCount{}
	if len(ids) == 0 {
		return out, nil
	}
	in, args := InList(ids)
	rows, err := q.QueryContext(ctx, `SELECT book_id, count(*) FROM copies WHERE removed_at IS NULL AND book_id IN `+in+` GROUP BY book_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c CopyCount
		if err := rows.Scan(&id, &c.Copies); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// CountsFor is the copy and loan counts of each of the books, read in one
// statement and so from one snapshot: a check-out or check-in made while it
// runs is in all of the counts or in none. A book with no copies has a zero
// CopyCount.
func (s *Service) CountsFor(ctx context.Context, bookIDs []int64) (map[int64]CopyCount, error) {
	return s.loans.CopyCounts(ctx, s.st.DB(), bookIDs)
}

// Counts is how many copies of the book are in the catalog and how many of
// them can be borrowed now. A book that isn't in the catalog is not found.
func (s *Service) Counts(ctx context.Context, bookID int64) (copies, available int, err error) {
	if err = mustExist(ctx, s.st.DB(), bookID); err != nil {
		return 0, 0, err
	}
	m, err := s.CountsFor(ctx, []int64{bookID})
	if err != nil {
		return 0, 0, err
	}
	return m[bookID].Copies, m[bookID].Available(), nil
}

// DueDates maps each of the book's copies on loan now to its due date.
func (s *Service) DueDates(ctx context.Context, bookID int64) (map[int64]string, error) {
	return s.loans.DueDates(ctx, s.st.DB(), bookID)
}

// LabelCopy is a copy with what its label shows about its book.
type LabelCopy struct {
	Copy
	Title string
	ISBN  string // the book's ISBN-13, "" when it has none
}

// LabelCopies returns the copies with the given IDs, in the order given, each
// with its book's title and ISBN, and the library's copy-ID mode, all read in
// one transaction so a label never mixes two modes. A copy that isn't in the
// catalog is not found.
func (s *Service) LabelCopies(ctx context.Context, ids []int64) ([]LabelCopy, string, error) {
	out := make([]LabelCopy, 0, len(ids))
	var mode string
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		var err error
		if mode, err = copyIDMode(ctx, tx); err != nil {
			return err
		}
		for _, id := range ids {
			c, err := s.getCopy(ctx, tx, id)
			if err != nil {
				return err
			}
			var isbn sql.NullString
			var title string
			if err := tx.QueryRowContext(ctx, `SELECT title, isbn13 FROM books WHERE id = ?`, c.BookID).Scan(&title, &isbn); err != nil {
				return err
			}
			out = append(out, LabelCopy{Copy: c, Title: title, ISBN: isbn.String})
		}
		return nil
	})
	return out, mode, err
}

package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"jamieslibrary/internal/apperr"
)

// Kinds of scannable code held in the identifiers table.
const (
	KindCard = "card"
	KindCopy = "copy"
)

// MaxCodeLength is the longest scannable code.
const MaxCodeLength = 32

// maxGenerateTries bounds the search for a free generated code: only codes
// someone typed in by hand can be in the way.
const maxGenerateTries = 100000

// NormaliseCode is the one way a scannable code is written down and compared:
// trimmed and upper-cased, 1 to 32 printable ASCII characters with no spaces.
// Everything that stores or looks up a code (copies, cards, scanning) uses it.
func NormaliseCode(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", errors.New("A code can't be empty.")
	}
	if len(code) > MaxCodeLength {
		return "", fmt.Errorf("A code can be at most %d characters.", MaxCodeLength)
	}
	for i := 0; i < len(code); i++ {
		if c := code[i]; c < 0x21 || c > 0x7e {
			return "", errors.New("A code can only have letters, digits and punctuation from the basic keyboard, with no spaces.")
		}
	}
	return code, nil
}

// checkCodeFree refuses a normalised code that any identifier other than the
// one of copy `except` already holds. A card number says so.
func checkCodeFree(ctx context.Context, q DB, code string, except int64) error {
	var kind string
	var copyID sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT kind, copy_id FROM identifiers WHERE code = ?`, code).Scan(&kind, &copyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == KindCard {
		return apperr.OnField(apperr.Conflict, "sticker", "That code is a library card number, so it can't be a copy's barcode. Use a different barcode.")
	}
	if copyID.Int64 == except {
		return nil
	}
	return apperr.OnField(apperr.Conflict, "sticker", "Another copy already has this barcode. Each copy needs its own.")
}

// generateCode returns a free code for a copy: "C" and the copy's number in the
// library's sequence of copies, zero-padded. A code someone typed that is in
// the way is skipped.
func generateCode(ctx context.Context, q DB, copyID int64) (string, error) {
	for n := copyID; n < copyID+maxGenerateTries; n++ {
		code := fmt.Sprintf("C%07d", n)
		var one int
		err := q.QueryRowContext(ctx, `SELECT 1 FROM identifiers WHERE code = ?`, code).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return code, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("no free copy code could be generated")
}

// setCopyCode gives the copy exactly this (already checked) code.
func setCopyCode(ctx context.Context, q DB, copyID int64, code string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM identifiers WHERE copy_id = ?`, copyID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO identifiers (code, kind, copy_id) VALUES (?, 'copy', ?)`, code, copyID)
	return err
}

// giveGeneratedCode gives the copy a generated code unless it has one.
func giveGeneratedCode(ctx context.Context, q DB, copyID int64) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM identifiers WHERE copy_id = ?`, copyID).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	code, err := generateCode(ctx, q, copyID)
	if err != nil {
		return err
	}
	return setCopyCode(ctx, q, copyID, code)
}

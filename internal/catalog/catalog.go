// Package catalog owns books, their authors and the named locations, and the
// rules about them: what a book needs, how ISBNs are kept, how search works.
// It also owns copies and the codes that identify them. It never sees HTTP.
// Where a rule needs to know about loans, it asks a Loans.
package catalog

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
)

// Limits on what is stored and searched.
const (
	MaxTitle       = 500
	MaxAuthorName  = 200
	MaxAuthors     = 50
	MaxDescription = 10000
	MaxLocation    = 100
	MaxSearchTerm  = 200
	MaxSearchWords = 10
	PageSize       = 50
	maxPage        = 100000
)

// DB is what both *sql.DB and a transaction offer.
type DB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Loans is what the catalog needs to know about loans, which belong to
// circulation. Until circulation exists, NoLoans answers it.
type Loans interface {
	// LoanedCopies is how many copies of the book are on loan now.
	LoanedCopies(ctx context.Context, q DB, bookID int64) (int, error)
	// OnLoan reports whether the copy is on loan now.
	OnLoan(ctx context.Context, q DB, copyID int64) (bool, error)
	// DueDates maps the ID of each of the book's copies that is on loan now to
	// its due date (YYYY-MM-DD). It says nothing about who has the copy.
	DueDates(ctx context.Context, q DB, bookID int64) (map[int64]string, error)
}

// NoLoans is the Loans of a library with no loans table yet: nothing is on loan.
type NoLoans struct{}

func (NoLoans) LoanedCopies(context.Context, DB, int64) (int, error) { return 0, nil }
func (NoLoans) OnLoan(context.Context, DB, int64) (bool, error)      { return false, nil }
func (NoLoans) DueDates(context.Context, DB, int64) (map[int64]string, error) {
	return map[int64]string{}, nil
}

// Options change how a Service is made; the zero value is not enough.
type Options struct {
	Settings *settings.Service // required: the copy-ID mode lives there
	Loans    Loans             // default NoLoans
	Now      func() time.Time  // default time.Now
}

// Service is the catalog's rules over a store.
type Service struct {
	st       *store.Store
	settings *settings.Service
	loans    Loans
	now      func() time.Time
}

// New makes a Service.
func New(st *store.Store, opts Options) *Service {
	s := &Service{st: st, settings: opts.Settings, loans: opts.Loans, now: opts.Now}
	if s.settings == nil {
		panic("catalog.New: Options.Settings is required")
	}
	if s.loans == nil {
		s.loans = NoLoans{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// cleanText trims s and checks it against a limit counted in characters.
func cleanText(s, field, what string, max int, required, multiline bool) (string, error) {
	s = strings.TrimSpace(s)
	if hasUnsafeChars(s, multiline) {
		return "", apperr.OnField(apperr.Invalid, field, "The "+what+" has a character that is not allowed (a control or text-direction character).")
	}
	if s == "" && required {
		return "", apperr.OnField(apperr.Invalid, field, "The "+what+" is required.")
	}
	if utf8.RuneCountInString(s) > max {
		return "", apperr.OnField(apperr.Invalid, field, "The "+what+" is too long: at most "+itoa(max)+" characters.")
	}
	return s, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// hasUnsafeChars reports control characters (newline and tab allowed when
// multiline) and the bidirectional override and isolate characters.
func hasUnsafeChars(s string, multiline bool) bool {
	for _, r := range s {
		switch {
		case multiline && (r == '\n' || r == '\t' || r == '\r'):
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			return true
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			return true
		}
	}
	return false
}

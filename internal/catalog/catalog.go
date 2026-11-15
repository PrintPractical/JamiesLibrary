// Package catalog owns books, their authors and the named locations, and the
// rules about them: what a book needs, how ISBNs are kept, how search works.
// It never sees HTTP. Copies belong to a later part of this package; where a
// rule needs to know about them, it asks a Copies.
package catalog

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
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
	PageSize       = 50
	maxPage        = 100000
)

// DB is what both *sql.DB and a transaction offer.
type DB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Copies is what the catalog needs to know about copies. Until copies exist,
// NoCopies answers it.
type Copies interface {
	// LoanedCopies is how many copies of the book are on loan now.
	LoanedCopies(ctx context.Context, q DB, bookID int64) (int, error)
	// CountByLocation is how many copies each location holds; a location
	// with none may be missing.
	CountByLocation(ctx context.Context, q DB) (map[int64]int, error)
	// HasCopyAtSQL is a condition on the books table (as "books") that holds
	// for a book with a copy in the location bound to its one "?".
	HasCopyAtSQL() string
}

// NoCopies is the Copies of a library with no copies table yet.
type NoCopies struct{}

func (NoCopies) LoanedCopies(context.Context, DB, int64) (int, error) { return 0, nil }
func (NoCopies) CountByLocation(context.Context, DB) (map[int64]int, error) {
	return nil, nil
}
func (NoCopies) HasCopyAtSQL() string { return "(0 AND ? IS NOT NULL)" }

// Options change how a Service is made; the zero value is what the server uses.
type Options struct {
	Copies Copies           // default NoCopies
	Now    func() time.Time // default time.Now
}

// Service is the catalog's rules over a store.
type Service struct {
	st     *store.Store
	copies Copies
	now    func() time.Time
}

// New makes a Service.
func New(st *store.Store, opts Options) *Service {
	s := &Service{st: st, copies: opts.Copies, now: opts.Now}
	if s.copies == nil {
		s.copies = NoCopies{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// cleanText trims s and checks it against a limit counted in characters.
func cleanText(s, field, what string, max int, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" && required {
		return "", apperr.OnField(apperr.Invalid, field, "The "+what+" is required.")
	}
	if utf8.RuneCountInString(s) > max {
		return "", apperr.OnField(apperr.Invalid, field, "The "+what+" is too long: at most "+itoa(max)+" characters.")
	}
	return s, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

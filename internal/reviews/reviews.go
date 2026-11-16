// Package reviews owns ratings and reviews (CAT-6): one rating and one review
// per user per book, which the user can change or delete, and which a librarian
// can remove. It never sees HTTP. Every method that acts for a user takes the
// user's ID from the caller's session, never from a request.
package reviews

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/store"
)

// Limits (security design §12) and the size of a page of reviews.
const (
	MaxTextLength   = 2000 // code points
	DefaultPageSize = 20
	MaxPageSize     = 100
	// MaxPage bounds the page number, so (page-1)*size cannot overflow.
	MaxPage = 1_000_000
)

// Service is ratings and reviews over a store.
type Service struct {
	st      *store.Store
	catalog *catalog.Service
}

// New makes a Service. The catalog says which books exist.
func New(st *store.Store, cat *catalog.Service) *Service { return &Service{st: st, catalog: cat} }

// Input is what a user submits for a book: a rating, text, or both. Nil leaves
// that part out; an empty text counts as none. A submission replaces what the
// user had, so leaving the text out of an edit removes the old text.
type Input struct {
	Rating *int
	Text   *string
}

// Review is one user's rating and review of a book. Rating is 0 and Text is ""
// when the user left that part out.
type Review struct {
	ID          int64
	BookID      int64
	UserID      int64
	DisplayName string
	Rating      int
	Text        string
	UpdatedAt   time.Time
}

// Summary is a book's average rating over the ratings given and how many there are.
type Summary struct {
	Average float64
	Count   int
}

// Page is one page of a book's reviews that have text, newest first.
type Page struct {
	Reviews  []Review
	Page     int
	PageSize int
	Total    int
}

func (in Input) clean() (rating sql.NullInt64, text sql.NullString, err error) {
	if in.Rating != nil {
		if *in.Rating < 1 || *in.Rating > 5 {
			return rating, text, apperr.OnField(apperr.Invalid, "rating", "A rating is from 1 to 5 stars.")
		}
		rating = sql.NullInt64{Int64: int64(*in.Rating), Valid: true}
	}
	if in.Text != nil && *in.Text != "" {
		if !utf8.ValidString(*in.Text) {
			return rating, text, apperr.OnField(apperr.Invalid, "text", "The review is not valid text.")
		}
		if n := utf8.RuneCountInString(*in.Text); n > MaxTextLength {
			return rating, text, apperr.OnField(apperr.Invalid, "text",
				fmt.Sprintf("A review can be at most %d characters; this one is %d.", MaxTextLength, n))
		}
		text = sql.NullString{String: *in.Text, Valid: true}
	}
	if !rating.Valid && !text.Valid {
		return rating, text, apperr.New(apperr.Invalid, "Give a rating from 1 to 5, a review, or both.")
	}
	return rating, text, nil
}

var errNoReview = apperr.New(apperr.NotFound, "You have no review of this book.")

// Put sets the user's rating and review of the book, creating it or replacing
// the one they had, so a user never has two. A review a librarian removed is not
// replaced.
func (s *Service) Put(ctx context.Context, userID, bookID int64, in Input) (Review, error) {
	rating, text, err := in.clean()
	if err != nil {
		return Review{}, err
	}
	now := clock.Now().UTC().Format(time.RFC3339)
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if _, err := s.catalog.GetBookIn(ctx, tx, bookID); err != nil {
			return err
		}
		var removed sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT removed_at FROM reviews WHERE book_id = ? AND user_id = ?`, bookID, userID).Scan(&removed)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `INSERT INTO reviews (book_id, user_id, rating, text, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
				bookID, userID, rating, text, now, now)
		case err != nil:
		case removed.Valid:
			return apperr.New(apperr.Forbidden, "A librarian removed your review of this book, so it can't be posted again.")
		default:
			_, err = tx.ExecContext(ctx, `UPDATE reviews SET rating = ?, text = ?, updated_at = ? WHERE book_id = ? AND user_id = ?`,
				rating, text, now, bookID, userID)
		}
		if err != nil {
			return fmt.Errorf("saving a review: %w", err)
		}
		return nil
	})
	if err != nil {
		return Review{}, err
	}
	return s.Mine(ctx, userID, bookID)
}

// Mine is the user's own rating and review of the book, or a not-found error.
func (s *Service) Mine(ctx context.Context, userID, bookID int64) (Review, error) {
	rs, err := s.query(ctx, `WHERE r.book_id = ? AND r.user_id = ? AND r.removed_at IS NULL`, bookID, userID)
	if err != nil {
		return Review{}, err
	}
	if len(rs) == 0 {
		return Review{}, errNoReview
	}
	return rs[0], nil
}

// Delete removes the user's own rating and review of the book for good.
func (s *Service) Delete(ctx context.Context, userID, bookID int64) error {
	res, err := s.st.DB().ExecContext(ctx, `DELETE FROM reviews WHERE book_id = ? AND user_id = ? AND removed_at IS NULL`, bookID, userID)
	if err != nil {
		return fmt.Errorf("deleting a review: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoReview
	}
	return nil
}

// Remove takes a review down on a librarian's say: it no longer shows or
// counts, and the removal is logged with who did it. It is kept, not deleted.
func (s *Service) Remove(ctx context.Context, actorID, reviewID int64) error {
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		var bookID, userID int64
		err := tx.QueryRowContext(ctx, `SELECT book_id, user_id FROM reviews WHERE id = ? AND removed_at IS NULL`, reviewID).Scan(&bookID, &userID)
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.New(apperr.NotFound, "There is no such review.")
		}
		if err != nil {
			return fmt.Errorf("finding a review: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE reviews SET removed_at = ?, removed_by = ? WHERE id = ?`,
			clock.Now().UTC().Format(time.RFC3339), actorID, reviewID); err != nil {
			return fmt.Errorf("removing a review: %w", err)
		}
		return activity.Record(ctx, tx, activity.ByUser(actorID), activity.ReviewRemoved, fmt.Sprintf("review:%d", reviewID),
			map[string]any{"book_id": bookID, "author_id": userID})
	})
}

// Summary is the book's average rating and rating count; removed reviews and
// reviews with no rating are left out. A book nobody rated has a zero Summary.
func (s *Service) Summary(ctx context.Context, bookID int64) (Summary, error) {
	var avg sql.NullFloat64
	var out Summary
	err := s.st.DB().QueryRowContext(ctx, `SELECT avg(rating), count(rating) FROM reviews WHERE book_id = ? AND removed_at IS NULL`, bookID).Scan(&avg, &out.Count)
	if err != nil {
		return Summary{}, fmt.Errorf("averaging ratings: %w", err)
	}
	out.Average = math.Round(avg.Float64*10) / 10
	return out, nil
}

// List is page number page (from 1) of the book's reviews that have text, with
// their authors' display names, newest first. A size of 0 means DefaultPageSize.
func (s *Service) List(ctx context.Context, bookID int64, page, size int) (Page, error) {
	if size == 0 {
		size = DefaultPageSize
	}
	if size < 1 || size > MaxPageSize {
		return Page{}, apperr.OnField(apperr.Invalid, "per_page", fmt.Sprintf("Ask for between 1 and %d reviews per page.", MaxPageSize))
	}
	if page < 1 || page > MaxPage {
		return Page{}, apperr.OnField(apperr.Invalid, "page", fmt.Sprintf("The page number is from 1 to %d.", MaxPage))
	}
	out := Page{Page: page, PageSize: size}
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM reviews WHERE book_id = ? AND removed_at IS NULL AND text IS NOT NULL`, bookID).Scan(&out.Total); err != nil {
		return Page{}, fmt.Errorf("counting reviews: %w", err)
	}
	var err error
	out.Reviews, err = s.query(ctx, `WHERE r.book_id = ? AND r.removed_at IS NULL AND r.text IS NOT NULL
		ORDER BY r.updated_at DESC, r.id DESC LIMIT ? OFFSET ?`, bookID, size, (page-1)*size)
	return out, err
}

func (s *Service) query(ctx context.Context, where string, args ...any) ([]Review, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT r.id, r.book_id, r.user_id, u.display_name, r.rating, r.text, r.updated_at
		FROM reviews r JOIN users u ON u.id = r.user_id `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("reading reviews: %w", err)
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var (
			r      Review
			rating sql.NullInt64
			text   sql.NullString
			at     string
		)
		if err := rows.Scan(&r.ID, &r.BookID, &r.UserID, &r.DisplayName, &rating, &text, &at); err != nil {
			return nil, fmt.Errorf("reading reviews: %w", err)
		}
		r.Rating, r.Text = int(rating.Int64), text.String
		if r.UpdatedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("review %d has a bad time %q: %w", r.ID, at, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/store"
)

// Book is a catalog record.
type Book struct {
	ID          int64
	ItemType    string
	Title       string
	Authors     []string
	ISBN13      string // "" when the book has none
	Description string
	Cover       string // the stored media file's name, "" when none
	CreatedAt   string
}

// BookInput is what a librarian saves about a book.
type BookInput struct {
	Title       string
	Authors     []string
	ISBN        string // optional, ISBN-10 or -13
	Description string
}

// DuplicateISBN is the conflict for an ISBN another book already has.
// It is an *apperr.Error too (errors.As finds it), carrying the book's ID.
type DuplicateISBN struct {
	BookID int64
	err    *apperr.Error
}

func (e *DuplicateISBN) Error() string { return e.err.Error() }
func (e *DuplicateISBN) Unwrap() error { return e.err }

type cleanBook struct {
	title, description, isbn string
	authors                  []string
}

func (in BookInput) clean() (cleanBook, error) {
	var c cleanBook
	var err error
	if c.title, err = cleanText(in.Title, "title", "title", MaxTitle, true, false); err != nil {
		return c, err
	}
	for _, a := range in.Authors {
		a, err := cleanText(a, "authors", "author name", MaxAuthorName, false, false)
		if err != nil {
			return c, err
		}
		if a != "" {
			c.authors = append(c.authors, a)
		}
	}
	if len(c.authors) == 0 {
		return c, apperr.OnField(apperr.Invalid, "authors", "A book needs at least one author.")
	}
	if len(c.authors) > MaxAuthors {
		return c, apperr.OnField(apperr.Invalid, "authors", fmt.Sprintf("A book can have at most %d authors.", MaxAuthors))
	}
	if c.description, err = cleanText(in.Description, "description", "description", MaxDescription, false, true); err != nil {
		return c, err
	}
	if strings.TrimSpace(in.ISBN) != "" {
		if c.isbn, err = ParseISBN(in.ISBN); err != nil {
			return c, err
		}
	}
	return c, nil
}

// searchText is the lower-cased title and author names, a line each.
func (c cleanBook) searchText() string {
	return strings.ToLower(c.title + "\n" + strings.Join(c.authors, "\n"))
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CreateBook adds a book and returns it. An ISBN another book has is a
// *DuplicateISBN.
func (s *Service) CreateBook(ctx context.Context, actor int64, in BookInput) (Book, error) {
	c, err := in.clean()
	if err != nil {
		return Book{}, err
	}
	var id int64
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := checkISBNFree(ctx, tx, c.isbn, 0); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO books (title, description, isbn13, search_text, created_at) VALUES (?, ?, ?, ?, ?)`,
			c.title, c.description, nullable(c.isbn), c.searchText(), s.stamp())
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return writeAuthors(ctx, tx, id, c.authors)
	})
	if err != nil {
		return Book{}, err
	}
	return s.GetBook(ctx, id)
}

// UpdateBook replaces a book's title, authors, ISBN and description. The
// cover is left alone (see SetCover).
func (s *Service) UpdateBook(ctx context.Context, actor, id int64, in BookInput) (Book, error) {
	c, err := in.clean()
	if err != nil {
		return Book{}, err
	}
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := mustExist(ctx, tx, id); err != nil {
			return err
		}
		if err := checkISBNFree(ctx, tx, c.isbn, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE books SET title = ?, description = ?, isbn13 = ?, search_text = ? WHERE id = ?`,
			c.title, c.description, nullable(c.isbn), c.searchText(), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM book_authors WHERE book_id = ?`, id); err != nil {
			return err
		}
		return writeAuthors(ctx, tx, id, c.authors)
	})
	if err != nil {
		return Book{}, err
	}
	return s.GetBook(ctx, id)
}

// SetCover sets the book's cover to a file the media store already holds, or
// clears it when name is "". The name is only a name: no path.
func (s *Service) SetCover(ctx context.Context, actor, id int64, name string) error {
	if name != "" && (len(name) > 200 || strings.ContainsAny(name, `/\`+"\x00") || strings.HasPrefix(name, ".")) {
		return apperr.OnField(apperr.Invalid, "cover", "That is not a stored image name.")
	}
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := mustExist(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE books SET cover = ? WHERE id = ?`, nullable(name), id)
		return err
	})
}

// RemoveBook removes a book and its copies from the catalog (the rows stay,
// marked; the copies' codes are freed). It is refused while any copy of it is
// on loan.
func (s *Service) RemoveBook(ctx context.Context, actor, id int64) error {
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := mustExist(ctx, tx, id); err != nil {
			return err
		}
		n, err := s.loans.LoanedCopies(ctx, tx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.New(apperr.Conflict, fmt.Sprintf("This book cannot be removed while %s on loan: check %s in first.", plural(n, "copy is", "copies are"), plural(n, "it", "them")))
		}
		at := s.stamp()
		if _, err := tx.ExecContext(ctx, `DELETE FROM identifiers WHERE copy_id IN (SELECT id FROM copies WHERE book_id = ? AND removed_at IS NULL)`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE copies SET removed_at = ? WHERE book_id = ? AND removed_at IS NULL`, at, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE books SET removed_at = ? WHERE id = ?`, at, id)
		return err
	})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// GetBook returns a book that is in the catalog; a removed one is not found.
func (s *Service) GetBook(ctx context.Context, id int64) (Book, error) {
	return s.GetBookIn(ctx, s.st.DB(), id)
}

// GetBookIn is GetBook inside the caller's transaction.
func (s *Service) GetBookIn(ctx context.Context, q DB, id int64) (Book, error) {
	books, err := queryBooks(ctx, q, `SELECT id, item_type, title, description, isbn13, cover, created_at FROM books WHERE id = ? AND removed_at IS NULL`, id)
	if err != nil {
		return Book{}, err
	}
	if len(books) == 0 {
		return Book{}, errBookNotFound
	}
	return books[0], nil
}

// BookByISBNIn finds the book in the catalog with this ISBN-13 (as ParseISBN
// returns it), inside the caller's transaction.
func (s *Service) BookByISBNIn(ctx context.Context, q DB, isbn13 string) (Book, bool, error) {
	books, err := queryBooks(ctx, q, `SELECT id, item_type, title, description, isbn13, cover, created_at FROM books WHERE isbn13 = ? AND removed_at IS NULL`, isbn13)
	if err != nil || len(books) == 0 {
		return Book{}, false, err
	}
	return books[0], true, nil
}

var errBookNotFound = apperr.New(apperr.NotFound, "That book is not in the catalog.")

func mustExist(ctx context.Context, q DB, id int64) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM books WHERE id = ? AND removed_at IS NULL`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return errBookNotFound
	}
	return err
}

// checkISBNFree refuses an ISBN that a book other than except already has.
func checkISBNFree(ctx context.Context, q DB, isbn string, except int64) error {
	if isbn == "" {
		return nil
	}
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM books WHERE isbn13 = ? AND removed_at IS NULL AND id <> ?`, isbn, except).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return &DuplicateISBN{BookID: id, err: apperr.OnField(apperr.Conflict, "isbn", "A book with this ISBN is already in the catalog.")}
}

func writeAuthors(ctx context.Context, q DB, id int64, authors []string) error {
	for i, a := range authors {
		if _, err := q.ExecContext(ctx, `INSERT INTO book_authors (book_id, position, name) VALUES (?, ?, ?)`, id, i, a); err != nil {
			return err
		}
	}
	return nil
}

// queryBooks runs a query that selects book columns, then adds their authors.
func queryBooks(ctx context.Context, q DB, query string, args ...any) ([]Book, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var books []Book
	for rows.Next() {
		var b Book
		var isbn, cover sql.NullString
		if err := rows.Scan(&b.ID, &b.ItemType, &b.Title, &b.Description, &isbn, &cover, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.ISBN13, b.Cover = isbn.String, cover.String
		b.Authors = []string{}
		books = append(books, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(books) == 0 {
		return books, nil
	}
	at := make(map[int64]*Book, len(books))
	ids := make([]any, len(books))
	for i := range books {
		at[books[i].ID] = &books[i]
		ids[i] = books[i].ID
	}
	arows, err := q.QueryContext(ctx, `SELECT book_id, name FROM book_authors WHERE book_id IN (?`+strings.Repeat(",?", len(ids)-1)+`) ORDER BY book_id, position`, ids...)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var id int64
		var name string
		if err := arows.Scan(&id, &name); err != nil {
			return nil, err
		}
		at[id].Authors = append(at[id].Authors, name)
	}
	return books, arows.Err()
}

// BookByISBN finds the book in the catalog with this ISBN-13 (as ParseISBN returns it).
func (s *Service) BookByISBN(ctx context.Context, isbn13 string) (Book, bool, error) {
	return s.BookByISBNIn(ctx, s.st.DB(), isbn13)
}

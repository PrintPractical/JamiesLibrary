package catalog

import (
	"context"
	"strings"
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
)

// SearchResult is one page of matching books and how many match in all.
type SearchResult struct {
	Books    []Book
	Total    int
	Page     int
	PageSize int
}

// Search finds books by title or author (the words appear anywhere,
// ignoring case) or by ISBN (the query is a valid ISBN-10 or -13), optionally
// only those with a copy in locationID (0 for any). An empty query lists every
// book. Removed books never appear. Pages count from 1 and hold PageSize books.
func (s *Service) Search(ctx context.Context, query string, locationID int64, page int) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > MaxSearchTerm {
		return SearchResult{}, apperr.OnField(apperr.Invalid, "q", "The search is too long: at most 200 characters.")
	}
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		return SearchResult{}, apperr.OnField(apperr.Invalid, "page", "That page is too far on.")
	}

	where := []string{"books.removed_at IS NULL"}
	var args []any
	if query != "" {
		if isbn, ok := normaliseISBN(query); ok {
			where = append(where, "books.isbn13 = ?")
			args = append(args, isbn)
		} else {
			where = append(where, `books.search_text LIKE ? ESCAPE '\'`)
			args = append(args, "%"+likeEscape(strings.ToLower(oneLine(query)))+"%")
		}
	}
	if locationID != 0 {
		where = append(where, s.copies.HasCopyAtSQL())
		args = append(args, locationID)
	}
	cond := strings.Join(where, " AND ")

	res := SearchResult{Page: page, PageSize: PageSize, Books: []Book{}}
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM books WHERE `+cond, args...).Scan(&res.Total); err != nil {
		return SearchResult{}, err
	}
	if res.Total == 0 || (page-1)*PageSize >= res.Total {
		return res, nil
	}
	books, err := queryBooks(ctx, s.st.DB(),
		`SELECT id, item_type, title, description, isbn13, cover, created_at FROM books WHERE `+cond+` ORDER BY search_text, id LIMIT ? OFFSET ?`,
		append(args, PageSize, (page-1)*PageSize)...)
	if err != nil {
		return SearchResult{}, err
	}
	res.Books = books
	return res, nil
}

// oneLine turns line breaks into spaces: the stored text keeps one title or
// author per line, and a search must not match across them.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// likeEscape makes s match itself literally in LIKE ... ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

package catalog

import (
	"context"
	"fmt"
	"strings"
	"unicode"
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
	query = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(query))
	if hasUnsafeChars(query, false) {
		return SearchResult{}, apperr.OnField(apperr.Invalid, "q", "The search has a character that is not allowed.")
	}
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
			words := searchWords(query)
			if len(words) > MaxSearchWords {
				return SearchResult{}, apperr.OnField(apperr.Invalid, "q", fmt.Sprintf("The search has too many words: at most %d.", MaxSearchWords))
			}
			for _, w := range words {
				where = append(where, `books.search_text LIKE ? ESCAPE '\'`)
				args = append(args, "%"+likeEscape(w)+"%")
			}
		}
	}
	if locationID != 0 {
		where = append(where, hasCopyAtSQL)
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

// searchWords splits a query into the lower-cased words that must each appear
// in a book's title or an author's name. Periods separate words too, so
// "J.R.R. Tolkien" finds "J. R. R. Tolkien". A query of nothing but periods is
// searched as written.
func searchWords(q string) []string {
	q = strings.ToLower(q)
	words := strings.FieldsFunc(q, func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
	if len(words) == 0 {
		return []string{q}
	}
	return words
}

// likeEscape makes s match itself literally in LIKE ... ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

package httpapi

import (
	"net/http"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/reviews"
)

// registerPublicRoutes registers the signed-out catalog (ACCESS-4, sec §7). Each
// route is declared PublicCatalog, so it answers 404 while the setting is off,
// checked on every request.
func registerPublicRoutes(rt *Router, o Options) {
	p := &publicRoutes{cat: o.Catalog, reviews: o.Reviews}
	rt.HandleFunc("GET /api/public/books", PublicCatalog(), p.search)
	rt.HandleFunc("GET /api/public/books/{id}", PublicCatalog(), p.detail)
}

type publicRoutes struct {
	cat     *catalog.Service
	reviews *reviews.Service // nil: no book has a rating
}

// publicBook is a book as a signed-out visitor sees it. It holds exactly the
// fields of the allowlist in sec §7 and shares no type with the staff or patron
// responses, so a field added there can never reach here. It has no reviews,
// names, loans, due dates, barcodes, condition or location names.
type publicBook struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	ISBN        string   `json:"isbn"`
	Description string   `json:"description"`
	CoverURL    string   `json:"cover_url"`
	Available   int      `json:"available_copies"`
	Total       int      `json:"total_copies"`
	// The average and the count only: never a review or a name (sec §7).
	RatingAverage float64 `json:"rating_average"`
	RatingCount   int     `json:"rating_count"`
}

type publicSearchBody struct {
	Books    []publicBook `json:"books"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

// books builds the visitor's view of each of the books with one read of the
// counts and one of the ratings, whatever the number of books.
func (p *publicRoutes) books(r *http.Request, bs []catalog.Book) ([]publicBook, error) {
	ids := make([]int64, len(bs))
	for i, b := range bs {
		ids[i] = b.ID
	}
	counts, err := p.cat.CountsFor(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	var sums map[int64]reviews.Summary
	if p.reviews != nil {
		if sums, err = p.reviews.Summaries(r.Context(), ids); err != nil {
			return nil, err
		}
	}
	out := make([]publicBook, 0, len(bs))
	for _, b := range bs {
		authors := b.Authors
		if authors == nil {
			authors = []string{}
		}
		n := counts[b.ID]
		pb := publicBook{ID: b.ID, Title: b.Title, Authors: authors, ISBN: b.ISBN13, Description: b.Description,
			CoverURL: coverURL(b.Cover), Available: n.Available(), Total: n.Copies}
		pb.RatingAverage, pb.RatingCount = sums[b.ID].Average, sums[b.ID].Count
		out = append(out, pb)
	}
	return out, nil
}

// search takes only q and page: the location filter would let a visitor probe
// location names, so it is not offered here.
func (p *publicRoutes) search(w http.ResponseWriter, r *http.Request) {
	page, err := intQuery(r, "page")
	if err != nil || page > 1<<31 {
		WriteError(w, r, apperr.OnField(apperr.Invalid, "page", "That is not a valid page."))
		return
	}
	res, err := p.cat.Search(r.Context(), r.URL.Query().Get("q"), 0, int(page))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	books, err := p.books(r, res.Books)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, publicSearchBody{Books: books, Total: res.Total, Page: res.Page, PageSize: res.PageSize})
}

func (p *publicRoutes) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	b, err := p.cat.GetBook(r.Context(), id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	pbs, err := p.books(r, []catalog.Book{b})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, pbs[0])
}

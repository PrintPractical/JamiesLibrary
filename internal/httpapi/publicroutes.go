package httpapi

import (
	"net/http"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/catalog"
)

// registerPublicRoutes registers the signed-out catalog (ACCESS-4, sec §7). Each
// route is declared PublicCatalog, so it answers 404 while the setting is off,
// checked on every request.
func registerPublicRoutes(rt *Router, o Options) {
	p := &publicRoutes{cat: o.Catalog}
	rt.HandleFunc("GET /api/public/books", PublicCatalog(), p.search)
	rt.HandleFunc("GET /api/public/books/{id}", PublicCatalog(), p.detail)
}

type publicRoutes struct {
	cat *catalog.Service
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
	// Reviews are not stored yet, so every book has no rating.
	RatingAverage float64 `json:"rating_average"`
	RatingCount   int     `json:"rating_count"`
}

type publicSearchBody struct {
	Books    []publicBook `json:"books"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

func (p *publicRoutes) book(r *http.Request, b catalog.Book) (publicBook, error) {
	total, avail, err := p.cat.Counts(r.Context(), b.ID)
	if err != nil {
		return publicBook{}, err
	}
	authors := b.Authors
	if authors == nil {
		authors = []string{}
	}
	return publicBook{ID: b.ID, Title: b.Title, Authors: authors, ISBN: b.ISBN13, Description: b.Description,
		CoverURL: coverURL(b.Cover), Available: avail, Total: total}, nil
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
	out := publicSearchBody{Books: make([]publicBook, 0, len(res.Books)), Total: res.Total, Page: res.Page, PageSize: res.PageSize}
	for _, b := range res.Books {
		pb, err := p.book(r, b)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		out.Books = append(out.Books, pb)
	}
	WriteJSON(w, http.StatusOK, out)
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
	pb, err := p.book(r, b)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, pb)
}

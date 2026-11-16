package httpapi

import (
	"errors"
	"net/http"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/media"
	"jamieslibrary/internal/metadata"
	"jamieslibrary/internal/perm"
)

// registerLookupRoutes registers the ISBN lookup and the preview of the cover
// it fetched (SCAN-2, CAT-T1). The lookup saves nothing: the librarian confirms
// the prefill and the book is created by its own route.
func registerLookupRoutes(rt *Router, o Options) {
	l := &lookupRoutes{meta: o.Metadata, cat: o.Catalog}
	rt.HandleFunc("POST /api/catalog/lookup", Require(perm.CatalogManage), l.lookup)
	rt.HandleFunc("GET /media-tmp/{token}", Require(perm.CatalogManage), l.preview)
}

type lookupRoutes struct {
	meta *metadata.Service
	cat  *catalog.Service
}

// lookupBody is the answer. Status is "found" (the fields are the prefill),
// "exists" (BookID is the book already in the catalog: offer a copy of it) or
// "not_found". A provider that can't be reached is a 503 error instead.
type lookupBody struct {
	Status      string   `json:"status"`
	BookID      int64    `json:"book_id,omitempty"`
	ISBN        string   `json:"isbn,omitempty"`
	Title       string   `json:"title,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	Description string   `json:"description,omitempty"`
	CoverToken  string   `json:"cover_token,omitempty"`
	CoverURL    string   `json:"cover_url,omitempty"`
}

func (l *lookupRoutes) lookup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ISBN string `json:"isbn"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	isbn, err := catalog.ParseISBN(in.ISBN) // nothing is asked of anyone for a bad ISBN
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if b, ok, err := l.cat.BookByISBN(r.Context(), isbn); err != nil {
		WriteError(w, r, err)
		return
	} else if ok {
		WriteJSON(w, http.StatusOK, lookupBody{Status: "exists", BookID: b.ID, ISBN: isbn})
		return
	}
	b, err := l.meta.Lookup(r.Context(), auth.Caller(r.Context()).UserID, isbn)
	switch {
	case errors.Is(err, metadata.ErrNotFound):
		WriteJSON(w, http.StatusOK, lookupBody{Status: "not_found", ISBN: isbn})
	case errors.Is(err, metadata.ErrUnavailable):
		WriteError(w, r, apperr.New(apperr.Unavailable, "Lookup unavailable, enter the details."))
	case err != nil:
		WriteError(w, r, err)
	default:
		out := lookupBody{Status: "found", ISBN: b.ISBN13, Title: b.Title, Authors: b.Authors,
			Description: b.Description, CoverToken: b.CoverToken}
		if b.CoverToken != "" {
			out.CoverURL = "/media-tmp/" + b.CoverToken
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// preview serves a fetched cover to the one who fetched it, until it's saved.
func (l *lookupRoutes) preview(w http.ResponseWriter, r *http.Request) {
	f, err := l.meta.OpenCover(r.PathValue("token"), auth.Caller(r.Context()).UserID)
	if errors.Is(err, media.ErrNotFound) {
		notFound(w, r)
		return
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		WriteError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", media.ContentType(r.PathValue("token")))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

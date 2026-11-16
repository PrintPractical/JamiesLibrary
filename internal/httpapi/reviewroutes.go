package httpapi

import (
	"net/http"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/reviews"
)

// registerReviewRoutes registers ratings and reviews (CAT-6). A user's own
// review is reached through the book and the session: no path or body names
// another user's, so there is no way to change it (sec §4). The text is JSON
// data and the web app renders it as plain text (sec §8).
func registerReviewRoutes(rt *Router, o Options) {
	v := &reviewRoutes{svc: o.Reviews}
	rt.HandleFunc("GET /api/books/{id}/reviews", Require(perm.CatalogBrowse), v.list)
	rt.HandleFunc("PUT /api/books/{id}/review", Require(perm.ReviewsWrite), v.put)
	rt.HandleFunc("DELETE /api/books/{id}/review", Require(perm.ReviewsWrite), v.deleteOwn)
	rt.HandleFunc("DELETE /api/reviews/{id}", Require(perm.ReviewsRemove), v.remove)
}

type reviewRoutes struct {
	svc *reviews.Service
}

// reviewBody is a review as a signed-in user sees it. Rating is 0 and text is
// empty when the author left that part out.
type reviewBody struct {
	ID          int64     `json:"id"`
	DisplayName string    `json:"display_name"`
	Rating      int       `json:"rating"`
	Text        string    `json:"text"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toReviewBody(r reviews.Review) reviewBody {
	return reviewBody{ID: r.ID, DisplayName: r.DisplayName, Rating: r.Rating, Text: r.Text, UpdatedAt: r.UpdatedAt}
}

func toReviewBodies(rs []reviews.Review) []reviewBody {
	out := make([]reviewBody, 0, len(rs))
	for _, r := range rs {
		out = append(out, toReviewBody(r))
	}
	return out
}

// fillReviews adds a book's rating, first page of reviews and the caller's own
// review to its page.
func fillReviews(r *http.Request, svc *reviews.Service, d *bookDetail) error {
	ctx := r.Context()
	sum, err := svc.Summary(ctx, d.ID)
	if err != nil {
		return err
	}
	page, err := svc.List(ctx, d.ID, 1, 0)
	if err != nil {
		return err
	}
	d.RatingAverage, d.RatingCount = sum.Average, sum.Count
	d.Reviews, d.ReviewsTotal = toReviewBodies(page.Reviews), page.Total
	if c := auth.Caller(ctx); c != nil {
		mine, err := svc.Mine(ctx, c.UserID, d.ID)
		if e, ok := apperr.As(err); ok && e.Code == apperr.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		b := toReviewBody(mine)
		d.MyReview = &b
	}
	return nil
}

func (v *reviewRoutes) list(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	page, err := intParam(r, "page", 1)
	if err == nil {
		var size int
		if size, err = intParam(r, "per_page", reviews.DefaultPageSize); err == nil {
			var p reviews.Page
			if p, err = v.svc.List(r.Context(), id, page, size); err == nil {
				WriteJSON(w, http.StatusOK, map[string]any{
					"reviews": toReviewBodies(p.Reviews), "page": p.Page, "per_page": p.PageSize, "total": p.Total})
				return
			}
		}
	}
	WriteError(w, r, err)
}

type putReviewRequest struct {
	Rating *int    `json:"rating"`
	Text   *string `json:"text"`
}

func (v *reviewRoutes) put(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	var req putReviewRequest
	if err := DecodeJSON(w, r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	rv, err := v.svc.Put(r.Context(), auth.Caller(r.Context()).UserID, id, reviews.Input{Rating: req.Rating, Text: req.Text})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toReviewBody(rv))
}

func (v *reviewRoutes) deleteOwn(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	if err := v.svc.Delete(r.Context(), auth.Caller(r.Context()).UserID, id); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (v *reviewRoutes) remove(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "review")
	if err == nil {
		if err = v.svc.Remove(r.Context(), auth.Caller(r.Context()).UserID, id); err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	WriteError(w, r, err)
}

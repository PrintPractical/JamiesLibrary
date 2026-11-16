package httpapi

import (
	"net/http"
	"strconv"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/barcode"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

// registerCardRoutes registers library cards (CARD-1, security design §15).
func registerCardRoutes(rt *Router, o Options) {
	c := &cardRoutes{users: o.Users, st: o.Store}
	rt.HandleFunc("GET /api/me/card", SignedIn(), c.own)
	rt.HandleFunc("GET /api/me/card/barcode.svg", SignedIn(), c.image(barcode.Code128))
	rt.HandleFunc("GET /api/me/card/qr.svg", SignedIn(), c.image(barcode.QR))
	rt.HandleFunc("POST /api/users/{id}/card/reissue", Require(perm.CardsReissue), c.reissue)
	rt.HandleFunc("GET /api/patrons/by-card/{code}", Require(perm.PatronsLookup), c.byCard)
}

type cardRoutes struct {
	users *users.Service
	st    *store.Store
}

type cardBody struct {
	Name   string `json:"name"`
	Number string `json:"number"`
}

type patronBody struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
}

// ownCard loads the caller's name and card number. The user comes from the
// session only, never from the path or the body (security design §4).
func (c *cardRoutes) ownCard(r *http.Request) (cardBody, error) {
	caller := auth.Caller(r.Context())
	u, err := c.users.Get(r.Context(), caller.UserID)
	if err != nil {
		return cardBody{}, err
	}
	number, err := c.users.Card(r.Context(), caller.UserID)
	if err != nil {
		return cardBody{}, err
	}
	return cardBody{Name: u.DisplayName, Number: number}, nil
}

func (c *cardRoutes) own(w http.ResponseWriter, r *http.Request) {
	card, err := c.ownCard(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, card)
}

// image serves the caller's card as an SVG in the given symbology. A QR holds
// only the code, never a URL or a token.
func (c *cardRoutes) image(kind barcode.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		number, err := c.users.Card(r.Context(), auth.Caller(r.Context()).UserID)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		svg, err := barcode.SVG(kind, number)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(svg))
	}
}

// reissue replaces a user's card. The new number is returned to the librarian
// but not logged: the activity entry names only who and whom.
func (c *cardRoutes) reissue(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		WriteError(w, r, apperr.New(apperr.NotFound, "There is no such user."))
		return
	}
	caller := auth.Caller(r.Context())
	var number string
	err = c.st.WithTx(r.Context(), func(tx store.Tx) error {
		var err error
		if number, err = c.users.ReissueCard(r.Context(), tx, caller.Permissions, id); err != nil {
			return err
		}
		return activity.Record(r.Context(), tx, activity.ByUser(caller.UserID), activity.CardReissued,
			"user:"+strconv.FormatInt(id, 10), nil)
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"number": number})
}

func (c *cardRoutes) byCard(w http.ResponseWriter, r *http.Request) {
	p, err := c.users.PatronByCard(r.Context(), r.PathValue("code"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, patronBody{ID: p.ID, Username: p.Username, DisplayName: p.DisplayName, Active: p.Active})
}

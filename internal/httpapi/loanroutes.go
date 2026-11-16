package httpapi

import (
	"net/http"
	"strconv"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/circ"
	"jamieslibrary/internal/perm"
)

// registerLoanRoutes registers renewals, the overdue list, patron look-up, a
// patron's own loans and self-checkout (CIRC-3, CIRC-5, CIRC-6, CIRC-7,
// PATRON-1). The routes under /api/me take the patron from the session only.
func registerLoanRoutes(rt *Router, o Options) {
	l := &loanRoutes{circ: o.Circ}
	rt.HandleFunc("GET /api/me/loans", Require(perm.LoansOwnView), l.own)
	rt.HandleFunc("GET /api/me/loans/history", Require(perm.LoansOwnView), l.ownHistory)
	rt.HandleFunc("POST /api/me/loans/{id}/renew", Require(perm.LoansOwnRenew), l.renewOwn)
	rt.HandleFunc("POST /api/me/checkout", Require(perm.SelfCheckout), l.selfCheckout)
	rt.HandleFunc("POST /api/desk/loans/{id}/renew", Require(perm.CircDesk), l.renewForPatron)
	rt.HandleFunc("GET /api/desk/overdue", Require(perm.CircDesk), l.overdue)
	rt.HandleFunc("GET /api/patrons", Require(perm.PatronsLookup), l.find)
	rt.HandleFunc("GET /api/patrons/{id}", Require(perm.PatronsLookup), l.patron)
	rt.HandleFunc("GET /api/patron-loans/{id}", Require(perm.PatronsLookup), l.patronHistory)
}

type loanRoutes struct {
	circ *circ.Service
}

type loanPageBody struct {
	Loans   []deskLoanBody `json:"loans"`
	Page    int            `json:"page"`
	PerPage int            `json:"per_page"`
	Total   int            `json:"total"`
}

func loanBodies(ls []circ.Loan) []deskLoanBody {
	out := make([]deskLoanBody, 0, len(ls))
	for _, l := range ls {
		out = append(out, loanBody(l))
	}
	return out
}

func toLoanPage(p circ.LoanPage) loanPageBody {
	return loanPageBody{Loans: loanBodies(p.Loans), Page: p.Page, PerPage: p.PageSize, Total: p.Total}
}

// pageParams reads ?page and ?per_page.
func pageParams(r *http.Request) (page, size int, err error) {
	if page, err = intParam(r, "page", 1); err != nil {
		return
	}
	size, err = intParam(r, "per_page", circ.DefaultPageSize)
	return
}

// idParam reads a numeric path id; anything else is a 404, like an id that matches nothing.
func idParam(r *http.Request, what string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.New(apperr.NotFound, "There is no such "+what+".")
	}
	return id, nil
}

func (l *loanRoutes) own(w http.ResponseWriter, r *http.Request) {
	ls, err := l.circ.OpenLoansOf(r.Context(), auth.Caller(r.Context()).UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"loans": loanBodies(ls)})
}

func (l *loanRoutes) ownHistory(w http.ResponseWriter, r *http.Request) {
	page, size, err := pageParams(r)
	if err == nil {
		var p circ.LoanPage
		if p, err = l.circ.History(r.Context(), auth.Caller(r.Context()).UserID, page, size); err == nil {
			WriteJSON(w, http.StatusOK, toLoanPage(p))
			return
		}
	}
	WriteError(w, r, err)
}

func (l *loanRoutes) renewOwn(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "loan")
	if err == nil {
		var loan circ.Loan
		if loan, err = l.circ.Renew(r.Context(), id, auth.Caller(r.Context()).UserID); err == nil {
			WriteJSON(w, http.StatusOK, loanBody(loan))
			return
		}
	}
	WriteError(w, r, err)
}

func (l *loanRoutes) renewForPatron(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "loan")
	if err == nil {
		var loan circ.Loan
		if loan, err = l.circ.Renew(r.Context(), id, 0); err == nil {
			WriteJSON(w, http.StatusOK, loanBody(loan))
			return
		}
	}
	WriteError(w, r, err)
}

type selfCheckoutRequest struct {
	Code string `json:"code"` // a copy's barcode, or a book's ISBN in ISBN mode
}

func (l *loanRoutes) selfCheckout(w http.ResponseWriter, r *http.Request) {
	var req selfCheckoutRequest
	if err := DecodeJSON(w, r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	loan, err := l.circ.SelfCheckout(r.Context(), auth.Caller(r.Context()).UserID, req.Code)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, loanBody(loan))
}

func (l *loanRoutes) overdue(w http.ResponseWriter, r *http.Request) {
	page, size, err := pageParams(r)
	if err == nil {
		var p circ.LoanPage
		if p, err = l.circ.Overdue(r.Context(), page, size); err == nil {
			WriteJSON(w, http.StatusOK, toLoanPage(p))
			return
		}
	}
	WriteError(w, r, err)
}

type patronMatchBody struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Card        string `json:"card"`
	Active      bool   `json:"active"`
	Out         int    `json:"out"`
	Overdue     int    `json:"overdue"`
}

func toPatronMatch(p circ.PatronMatch) patronMatchBody {
	return patronMatchBody{ID: p.ID, Username: p.Username, DisplayName: p.DisplayName, Card: p.Card, Active: p.Active, Out: p.Out, Overdue: p.Overdue}
}

func (l *loanRoutes) find(w http.ResponseWriter, r *http.Request) {
	found, err := l.circ.FindPatrons(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := make([]patronMatchBody, 0, len(found))
	for _, p := range found {
		out = append(out, toPatronMatch(p))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"patrons": out})
}

func (l *loanRoutes) patron(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "patron")
	if err == nil {
		var rec circ.PatronRecord
		if rec, err = l.circ.PatronRecord(r.Context(), id); err == nil {
			WriteJSON(w, http.StatusOK, map[string]any{
				"patron": toPatronMatch(rec.Patron), "current": loanBodies(rec.Current), "overdue": loanBodies(rec.Overdue)})
			return
		}
	}
	WriteError(w, r, err)
}

func (l *loanRoutes) patronHistory(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r, "patron")
	if err == nil {
		var page, size int
		if page, size, err = pageParams(r); err == nil {
			var p circ.LoanPage
			if p, err = l.circ.History(r.Context(), id, page, size); err == nil {
				WriteJSON(w, http.StatusOK, toLoanPage(p))
				return
			}
		}
	}
	WriteError(w, r, err)
}

package httpapi

import (
	"net/http"

	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/circ"
	"jamieslibrary/internal/perm"
)

// registerDeskRoutes registers the circulation desk: what a scanned code
// identifies, check-out and check-in (CIRC-1, CIRC-2, SCAN-3, SCAN-4). The
// rules live in circ, not here.
func registerDeskRoutes(rt *Router, o Options) {
	d := &deskRoutes{circ: o.Circ}
	rt.HandleFunc("POST /api/desk/resolve", Require(perm.CircDesk), d.resolve)
	rt.HandleFunc("POST /api/desk/checkout", Require(perm.CircDesk), d.checkout)
	rt.HandleFunc("POST /api/desk/checkin", Require(perm.CircDesk), d.checkin)
}

type deskRoutes struct {
	circ *circ.Service
}

type deskPatronBody struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Card        string `json:"card"`
	Active      bool   `json:"active"`
	Out         int    `json:"out"`
	Overdue     int    `json:"overdue"`
	Limit       int    `json:"limit"`
}

type deskCopyBody struct {
	ID       int64    `json:"id"`
	Number   int      `json:"number"`
	Code     string   `json:"code"`
	BookID   int64    `json:"book_id"`
	Title    string   `json:"title"`
	Authors  []string `json:"authors"`
	Location string   `json:"location"`
	OnLoan   bool     `json:"on_loan"`
}

type deskBookBody struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	ISBN      string   `json:"isbn"`
	Copies    int      `json:"copies"`
	Available int      `json:"available"`
}

type deskLoanBody struct {
	ID           int64    `json:"id"`
	CopyID       int64    `json:"copy_id"`
	CopyNumber   int      `json:"copy_number"`
	CopyCode     string   `json:"copy_code"`
	BookID       int64    `json:"book_id"`
	Title        string   `json:"title"`
	Authors      []string `json:"authors"`
	Location     string   `json:"location"`
	PatronID     int64    `json:"patron_id"`
	PatronName   string   `json:"patron_name"`
	CheckedOutAt string   `json:"checked_out_at"`
	DueDate      string   `json:"due_date"`
	ReturnedAt   string   `json:"returned_at,omitempty"`
	DaysLate     int      `json:"days_late"`
}

func loanBody(l circ.Loan) deskLoanBody {
	return deskLoanBody{ID: l.ID, CopyID: l.CopyID, CopyNumber: l.CopyNumber, CopyCode: l.CopyCode, BookID: l.BookID,
		Title: l.Title, Authors: l.Authors, Location: l.Location, PatronID: l.PatronID, PatronName: l.PatronName,
		CheckedOutAt: l.CheckedOutAt, DueDate: l.DueDate, ReturnedAt: l.ReturnedAt, DaysLate: l.DaysLate}
}

func copyBody(c *circ.CopyInfo) *deskCopyBody {
	if c == nil {
		return nil
	}
	return &deskCopyBody{ID: c.ID, Number: c.Number, Code: c.Code, BookID: c.BookID, Title: c.Title,
		Authors: c.Authors, Location: c.Location, OnLoan: c.OnLoan}
}

func bookBody(b *circ.BookInfo) *deskBookBody {
	if b == nil {
		return nil
	}
	return &deskBookBody{ID: b.ID, Title: b.Title, Authors: b.Authors, ISBN: b.ISBN, Copies: b.Copies, Available: b.Available}
}

type resolveRequest struct {
	Code string `json:"code"`
	Step string `json:"step"`
}

type resolveResponse struct {
	Kind   string          `json:"kind"` // patron, copy or book
	Patron *deskPatronBody `json:"patron,omitempty"`
	Copy   *deskCopyBody   `json:"copy,omitempty"`
	Book   *deskBookBody   `json:"book,omitempty"`
}

func (d *deskRoutes) resolve(w http.ResponseWriter, r *http.Request) {
	var req resolveRequest
	if err := DecodeJSON(w, r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	got, err := d.circ.Resolve(r.Context(), req.Code, req.Step)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := resolveResponse{Copy: copyBody(got.Copy), Book: bookBody(got.Book)}
	switch {
	case got.Patron != nil:
		p := got.Patron
		out.Kind = "patron"
		out.Patron = &deskPatronBody{ID: p.ID, DisplayName: p.DisplayName, Card: p.Card, Active: p.Active, Out: p.Out, Overdue: p.Overdue, Limit: p.Limit}
	case got.Copy != nil:
		out.Kind = "copy"
	default:
		out.Kind = "book"
	}
	WriteJSON(w, http.StatusOK, out)
}

type checkoutRequest struct {
	Patron string `json:"patron"` // the patron's card number
	Code   string `json:"code"`   // a copy's barcode, or a book's ISBN in ISBN mode
}

func (d *deskRoutes) checkout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := DecodeJSON(w, r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	l, err := d.circ.Checkout(r.Context(), auth.Caller(r.Context()).UserID, req.Patron, req.Code)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, loanBody(l))
}

type checkinRequest struct {
	Code   string `json:"code"`
	LoanID int64  `json:"loan_id,omitempty"` // which open loan, chosen from a list
	Card   string `json:"card,omitempty"`    // a patron's card, to narrow the list
}

type checkinResponse struct {
	Status  string         `json:"status"` // returned, choose or not_out
	Message string         `json:"message"`
	Loan    *deskLoanBody  `json:"loan,omitempty"`
	Choices []deskLoanBody `json:"choices,omitempty"`
	Copy    *deskCopyBody  `json:"copy,omitempty"`
	Book    *deskBookBody  `json:"book,omitempty"`
}

func (d *deskRoutes) checkin(w http.ResponseWriter, r *http.Request) {
	var req checkinRequest
	if err := DecodeJSON(w, r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	if req.LoanID < 0 {
		req.LoanID = 0
	}
	got, err := d.circ.Checkin(r.Context(), auth.Caller(r.Context()).UserID, req.Code, req.LoanID, req.Card)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := checkinResponse{Status: got.Status, Message: got.Message, Copy: copyBody(got.Copy), Book: bookBody(got.Book)}
	if got.Loan != nil {
		b := loanBody(*got.Loan)
		out.Loan = &b
	}
	for _, l := range got.Choices {
		out.Choices = append(out.Choices, loanBody(l))
	}
	WriteJSON(w, http.StatusOK, out)
}

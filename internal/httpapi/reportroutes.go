package httpapi

import (
	"net/http"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/reports"
)

// registerReportRoutes registers the library statistics and the circulation
// report (ADMIN-3). Both are counts only.
func registerReportRoutes(rt *Router, o Options) {
	a := &reportRoutes{svc: o.Reports}
	rt.HandleFunc("GET /api/admin/reports/statistics", Require(perm.ReportsView), a.statistics)
	rt.HandleFunc("GET /api/admin/reports/circulation", Require(perm.ReportsView), a.circulation)
}

type reportRoutes struct{ svc *reports.Service }

type statisticsBody struct {
	Books        int `json:"books"`
	Copies       int `json:"copies"`
	Patrons      int `json:"patrons"`
	CurrentLoans int `json:"current_loans"`
	OverdueLoans int `json:"overdue_loans"`
}

type circulationBody struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Checkouts int    `json:"checkouts"`
	Returns   int    `json:"returns"`
}

func (a *reportRoutes) statistics(w http.ResponseWriter, r *http.Request) {
	st, err := a.svc.Statistics(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, statisticsBody(st))
}

func (a *reportRoutes) circulation(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c, err := a.svc.Circulation(r.Context(), q.Get("from"), q.Get("to"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, circulationBody(c))
}

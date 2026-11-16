package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/reports"
)

func newReportSite(t *testing.T) *site {
	return newSite(t, func(o *Options) { o.Reports = reports.New(o.Store, o.Settings.Location, o.Settings.Today) })
}

// ADMIN-3 "Report": an administrator reports on January and sees 3 checkouts and 2 returns, as counts only.
func TestAdministratorReportsOnJanuary(t *testing.T) {
	s := newReportSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	p := s.addUser("pat", goodPassword, perm.Patron)
	cookie := s.signIn("ada", goodPassword)
	s.exec(`INSERT INTO books (title, search_text, created_at) VALUES ('Secret Title', 's', '2026-01-01T00:00:00Z')`)
	for i := 1; i <= 3; i++ {
		s.exec(`INSERT INTO copies (book_id, number, condition, location_id, created_at) VALUES (1, ?, 'good', 1, '2026-01-01T00:00:00Z')`, i)
		ret := any(nil)
		if i <= 2 {
			ret = "2026-01-20T10:00:00Z"
		}
		s.exec(`INSERT INTO loans (copy_id, patron_id, checked_out_at, checked_out_by, due_date, returned_at) VALUES (?, ?, '2026-01-10T10:00:00Z', ?, '2026-01-24', ?)`, i, p.ID, p.ID, ret)
	}
	rec := s.req("GET", "/api/admin/reports/circulation?from=2026-01-01&to=2026-01-31", cookie, "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got circulationBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got != (circulationBody{From: "2026-01-01", To: "2026-01-31", Checkouts: 3, Returns: 2}) {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "pat") || strings.Contains(rec.Body.String(), "Secret") {
		t.Fatalf("the report names someone: %s", rec.Body)
	}

	rec = s.req("GET", "/api/admin/reports/statistics", cookie, "")
	var st statisticsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || rec.Code != 200 || st != (statisticsBody{Books: 1, Copies: 3, Patrons: 2, CurrentLoans: 1, OverdueLoans: 0}) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestReportRefusesABadRange(t *testing.T) {
	s := newReportSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	cookie := s.signIn("ada", goodPassword)
	for _, q := range []string{"", "from=2026-01-01", "from=2026-02-01&to=2026-01-01", "from=2020-01-01&to=2025-01-01", "from=x&to=y"} {
		if rec := s.req("GET", "/api/admin/reports/circulation?"+q, cookie, ""); rec.Code != 422 {
			t.Errorf("%q: %d %s", q, rec.Code, rec.Body)
		}
	}
}

// ROLE-3: a caller without reports.view is refused on both routes, and so is one who isn't signed in.
func TestReportsRefuseCallersWithoutThePermission(t *testing.T) {
	s := newReportSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	s.addUser("pat", goodPassword, perm.Patron)
	for _, path := range []string{"/api/admin/reports/statistics", "/api/admin/reports/circulation?from=2026-01-01&to=2026-01-31"} {
		for _, name := range []string{"lib", "pat"} {
			if rec := s.req("GET", path, s.signIn(name, goodPassword), ""); rec.Code != 403 {
				t.Errorf("%s %s: %d %s", name, path, rec.Code, rec.Body)
			}
		}
		if rec := s.req("GET", path, "", ""); rec.Code != 401 {
			t.Errorf("anonymous %s: %d", path, rec.Code)
		}
	}
}

func (s *site) exec(q string, args ...any) {
	s.t.Helper()
	if _, err := s.st.DB().Exec(q, args...); err != nil {
		s.t.Fatalf("%s: %v", q, err)
	}
}

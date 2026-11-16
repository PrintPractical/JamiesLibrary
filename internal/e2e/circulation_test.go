package e2e

// Circulation reports (ADMIN-3), judged from outside: desk check-outs and
// check-ins at moments chosen on the server's clock, then the administrator's
// report over HTTP.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

// deskLibrary is a running library with an administrator, a patron with a card,
// and a book without an ISBN whose copies get generated barcodes.
type deskLibrary struct {
	t      *testing.T
	s      *testutil.Server
	root   *who
	patron *who
	card   string
	bookID int64
	loc    int64
	pat    int64
}

func newDeskLibrary(t *testing.T, zone string, start time.Time) *deskLibrary {
	t.Helper()
	s := testutil.StartServer(t, testutil.Options{Now: start})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatal("no setup code")
	}
	root := newWho(t, s)
	root.want(200, "POST", "/api/setup/admin", map[string]string{
		"code": code, "username": "root", "display_name": "Root", "password": adminPassword})
	root.want(200, "PUT", "/api/admin/settings", map[string]any{"time_zone": zone})
	d := &deskLibrary{t: t, s: s, root: root}
	d.pat, d.patron = root.member("patron", "pat")
	d.card, _ = d.patron.want(200, "GET", "/api/me/card", nil).JSON["number"].(string)
	if d.card == "" {
		t.Fatal("the patron has no card number")
	}
	book := root.want(201, "POST", "/api/books", map[string]any{"title": "Old Tales", "authors": []string{"Anon"}})
	d.bookID = int64(book.JSON["id"].(float64))
	loc := d.root.want(201, "POST", "/api/locations", map[string]string{"name": "Report shelf"})
	d.loc = int64(loc.JSON["id"].(float64))
	return d
}

// addCopy adds a copy and returns its id and its barcode.
func (d *deskLibrary) addCopy() (int64, string) {
	d.t.Helper()
	r := d.root.want(201, "POST", fmt.Sprintf("/api/books/%d/copies", d.bookID),
		map[string]any{"condition": "good", "location_id": d.loc})
	barcode, _ := r.JSON["barcode"].(string)
	if barcode == "" {
		d.t.Fatalf("the copy has no barcode: %s", r.Raw)
	}
	return int64(r.JSON["id"].(float64)), barcode
}

// at moves the clock and signs the administrator in again, as a long gap ends a session.
func (d *deskLibrary) at(when string) {
	d.t.Helper()
	tm, err := time.Parse(time.RFC3339, when)
	if err != nil {
		d.t.Fatal(err)
	}
	if err := d.s.SetNow(tm); err != nil {
		d.t.Fatal(err)
	}
	d.root = newWho(d.t, d.s)
	d.root.mustSignIn("root", adminPassword)
}

func (d *deskLibrary) checkOut(code string) {
	d.t.Helper()
	d.root.want(200, "POST", "/api/desk/checkout", map[string]string{"patron": d.card, "code": code})
}

func (d *deskLibrary) checkIn(code string) {
	d.t.Helper()
	d.root.want(200, "POST", "/api/desk/checkin", map[string]string{"code": code})
}

// report is the administrator's circulation report for the dates.
func (d *deskLibrary) report(from, to string) (checkouts, returns int) {
	d.t.Helper()
	r := d.root.want(200, "GET", "/api/admin/reports/circulation?from="+from+"&to="+to, nil)
	co, _ := r.JSON["checkouts"].(float64)
	rt, _ := r.JSON["returns"].(float64)
	if strings.Contains(r.Raw, "pat") || strings.Contains(r.Raw, d.card) {
		d.t.Fatalf("the report names a patron: %s", r.Raw)
	}
	return int(co), int(rt)
}

// ADMIN-3 "Report": a loan belongs to the day it happened at in the library's
// own zone, so the second either side of local midnight falls on either side
// of the date.
func TestReportBoundariesAreLocalMidnight(t *testing.T) {
	d := newDeskLibrary(t, "America/Chicago", time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)) // UTC-6
	_, a := d.addCopy()
	_, b := d.addCopy()
	_, c := d.addCopy()
	_, e := d.addCopy()
	// Each copy goes out, and the middle two come straight back, at the second named.
	for _, m := range []struct {
		when, code string
		back       bool
	}{
		{"2026-01-01T05:59:59Z", a, false}, // 23:59:59 on 31 December, local
		{"2026-01-01T06:00:00Z", b, true},  // 00:00:00 on 1 January
		{"2026-02-01T05:59:59Z", c, true},  // 23:59:59 on 31 January
		{"2026-02-01T06:00:00Z", e, false}, // 00:00:00 on 1 February
	} {
		d.at(m.when)
		d.checkOut(m.code)
		if m.back {
			d.checkIn(m.code)
		}
	}
	for _, want := range []struct {
		from, to           string
		checkouts, returns int
	}{
		{"2026-01-01", "2026-01-31", 2, 2},
		{"2025-12-31", "2025-12-31", 1, 0},
		{"2026-02-01", "2026-02-01", 1, 0},
		{"2026-01-31", "2026-02-01", 2, 1},
	} {
		if co, rt := d.report(want.from, want.to); co != want.checkouts || rt != want.returns {
			t.Errorf("%s to %s: %d checkouts and %d returns, want %d and %d",
				want.from, want.to, co, rt, want.checkouts, want.returns)
		}
	}
}

// ADMIN-3: what happened stays in the history and the reports after the copy,
// and then its book, are removed.
func TestHistoryAndReportsSurviveRemovingTheCopyAndBook(t *testing.T) {
	d := newDeskLibrary(t, "America/Chicago", time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC))
	copyID, code := d.addCopy()
	d.checkOut(code)
	d.checkIn(code)
	d.root.want(204, "DELETE", fmt.Sprintf("/api/copies/%d", copyID), nil)
	d.root.want(204, "DELETE", fmt.Sprintf("/api/books/%d", d.bookID), nil)

	if co, rt := d.report("2026-01-10", "2026-01-10"); co != 1 || rt != 1 {
		t.Errorf("after removal: %d checkouts and %d returns, want 1 and 1", co, rt)
	}
	for name, h := range map[string]reply{
		"the patron's history": d.patron.want(200, "GET", "/api/me/loans/history", nil),
		"the librarian's view": d.root.want(200, "GET", fmt.Sprintf("/api/patron-loans/%d", d.pat), nil),
	} {
		if !strings.Contains(h.Raw, "Old Tales") {
			t.Errorf("%s lost the loan: %s", name, h.Raw)
		}
	}
	stats := d.root.want(200, "GET", "/api/admin/reports/statistics", nil)
	if stats.JSON["books"].(float64) != 0 || stats.JSON["copies"].(float64) != 0 {
		t.Errorf("removed books and copies still counted: %s", stats.Raw)
	}
}

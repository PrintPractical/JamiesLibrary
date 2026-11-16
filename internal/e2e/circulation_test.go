package e2e

// Circulation judged from outside, over HTTP against the running server: many
// clients at once on the last copy, the loan limit and the renewal limit; the
// library's time zone far from the server's, on days the clocks change and at
// the second a loan falls due; the copy-numbering, barcode and mode rules that
// circulation relies on; and the administrator's report at local midnight.
// The clock is the server's, moved through the control listener.

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

// ---- helpers ----------------------------------------------------------------

type lib struct {
	t    *testing.T
	s    *testutil.Server
	root *who
	loc  int64 // the shelf every copy is put on
	pats []*pat
}

type pat struct {
	id   int64
	user string
	card string
	w    *who
}

func newLib(t *testing.T) *lib {
	t.Helper()
	s, root := install(t, nil)
	return setUpLib(t, s, root)
}

// newLibInZone runs the real binary with its own time zone (TZ) set to
// serverZone, for tests where that zone must differ from the library's. The
// binary is the same one the process tests use, with the clock control.
func newLibInZone(t *testing.T, serverZone string) *lib {
	t.Helper()
	dir := t.TempDir()
	control := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	p := launch(t, dir, map[string]string{
		"TZ":                   serverZone,
		"JL_TEST_CONTROL_ADDR": control,
		"JL_TEST_NOW":          testutil.DefaultNow.UTC().Format(time.RFC3339),
	})
	if err := p.waitReady(); err != nil {
		t.Fatalf("server not ready: %v\n%s", err, p.Stderr.String())
	}
	code, ok := testutil.ParseSetupCode(p.Stdout.String())
	if !ok {
		t.Fatalf("no setup code in the console output:\n%s", p.Stdout.String())
	}
	s := &testutil.Server{URL: p.Base, ControlURL: "http://" + control, DataDir: dir}
	root := newWho(t, s)
	root.want(200, "POST", "/api/setup/admin", map[string]string{
		"code": code, "username": "root", "display_name": "Root", "password": adminPassword})
	return setUpLib(t, s, root)
}

func setUpLib(t *testing.T, s *testutil.Server, root *who) *lib {
	t.Helper()
	l := &lib{t: t, s: s, root: root}
	// A long idle time, so moving the clock by months doesn't end sessions;
	// at() signs in again anyway.
	l.set(map[string]any{"session_idle_minutes": 43200, "max_loans": 50})
	r := root.want(201, "POST", "/api/locations", map[string]string{"name": "Test shelf"})
	l.loc = num(r.JSON, "id")
	return l
}

func (l *lib) set(m map[string]any) {
	l.t.Helper()
	l.root.want(200, "PUT", "/api/admin/settings", m)
}

func (l *lib) mode(m string) {
	l.t.Helper()
	l.root.want(200, "PUT", "/api/admin/copy-id-mode", map[string]string{"mode": m})
}

// at moves the server's clock and signs everyone in again, as people do after a long time away.
func (l *lib) at(ts string) {
	l.t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := l.s.SetNow(tm); err != nil {
		l.t.Fatal(err)
	}
	l.root.mustSignIn("root", adminPassword)
	for _, p := range l.pats {
		p.w.mustSignIn(p.user, adminPassword)
	}
}

func (l *lib) book(title, isbn string) int64 {
	l.t.Helper()
	body := map[string]any{"title": title, "authors": []string{"A. Writer"}}
	if isbn != "" {
		body["isbn"] = isbn
	}
	return num(l.root.want(201, "POST", "/api/books", body).JSON, "id")
}

type copyRec struct {
	ID      int64
	Number  int
	Barcode string
}

func (l *lib) tryCopy(book int64, sticker string) (copyRec, reply) {
	body := map[string]any{"location_id": l.loc, "condition": "good"}
	if sticker != "" {
		body["sticker"] = sticker
	}
	r := l.root.do("POST", fmt.Sprintf("/api/books/%d/copies", book), body)
	return copyRec{ID: num(r.JSON, "id"), Number: int(num(r.JSON, "number")), Barcode: str(r.JSON, "barcode")}, r
}

func (l *lib) addCopy(book int64) copyRec {
	l.t.Helper()
	c, r := l.tryCopy(book, "")
	if r.Status != 201 {
		l.t.Fatalf("add copy: %d %s", r.Status, r.Raw)
	}
	return c
}

func (l *lib) patron(name string) *pat {
	l.t.Helper()
	id, w := l.root.member("patron", name)
	card := str(w.want(200, "GET", "/api/me/card", nil).JSON, "number")
	if card == "" {
		l.t.Fatal("patron without a card number")
	}
	p := &pat{id: id, user: name, card: card, w: w}
	l.pats = append(l.pats, p)
	return p
}

func (l *lib) desk(p *pat, code string) reply {
	return l.root.do("POST", "/api/desk/checkout", map[string]string{"patron": p.card, "code": code})
}

func (l *lib) self(p *pat, code string) reply {
	return p.w.do("POST", "/api/me/checkout", map[string]string{"code": code})
}

func (l *lib) mustDesk(p *pat, code string) reply {
	l.t.Helper()
	r := l.desk(p, code)
	if r.Status != 200 {
		l.t.Fatalf("check out %s to %s: %d %s", code, p.user, r.Status, r.Raw)
	}
	return r
}

func (l *lib) checkinLoan(code string, loan int64) reply {
	l.t.Helper()
	return l.root.want(200, "POST", "/api/desk/checkin", map[string]any{"code": code, "loan_id": loan})
}

func (l *lib) db() *sql.DB { return openRO(l.t, l.s.DataDir) }

func scalar(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// race runs f(0..n-1) at the same moment and collects the replies in order.
func race(n int, f func(i int) reply) []reply {
	out := make([]reply, n)
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			out[i] = f(i)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	return out
}

func statuses(rs []reply) map[int]int {
	m := map[int]int{}
	for _, r := range rs {
		m[r.Status]++
	}
	return m
}

func noServerErrors(t *testing.T, rs []reply) {
	t.Helper()
	for _, r := range rs {
		if r.Status >= 500 || r.Status == 0 {
			t.Errorf("a server error under concurrency: %d %s", r.Status, r.Raw)
		}
	}
}

func openLoansOfPatron(t *testing.T, db *sql.DB, id int64) int {
	return scalar(t, db, `SELECT count(*) FROM loans WHERE patron_id = ? AND returned_at IS NULL`, id)
}

// ---- CIRC-1: the last copy --------------------------------------------------

// Several clients check out the last copy at the same moment, at the desk and
// by self-checkout: one wins, the rest are refused, and exactly one loan exists.
func TestLastCopyCheckedOutByManyClientsHasOneWinner(t *testing.T) {
	for _, mode := range []string{"isbn", "own"} {
		t.Run(mode, func(t *testing.T) {
			l := newLib(t)
			l.set(map[string]any{"self_checkout": true})
			l.mode(mode)
			ps := make([]*pat, 6)
			for i := range ps {
				ps[i] = l.patron(fmt.Sprintf("racer%d", i))
			}
			db := l.db()
			for round := 0; round < 12; round++ {
				isbn := isbn13(round)
				b := l.book(fmt.Sprintf("Last copy %d", round), isbn)
				c := l.addCopy(b)
				code := isbn
				if mode == "own" {
					code = c.Barcode
					if code == "" {
						t.Fatal("own-barcode mode gave the copy no barcode")
					}
				}
				rs := race(len(ps), func(i int) reply {
					if i%2 == 0 {
						return l.desk(ps[i], code)
					}
					return l.self(ps[i], code)
				})
				noServerErrors(t, rs)
				st := statuses(rs)
				if st[200] != 1 || st[409] != len(ps)-1 {
					t.Fatalf("round %d: want one 200 and %d refusals, got %v", round, len(ps)-1, st)
				}
				if n := scalar(t, db, `SELECT count(*) FROM loans WHERE copy_id = ? AND returned_at IS NULL`, c.ID); n != 1 {
					t.Fatalf("round %d: %d open loans on one copy", round, n)
				}
				if n := scalar(t, db, `SELECT count(*) FROM loans WHERE copy_id = ?`, c.ID); n != 1 {
					t.Fatalf("round %d: %d loan rows for one copy: a loser left a row behind", round, n)
				}
			}
		})
	}
}

// ---- CIRC-4: the limit never exceeded ---------------------------------------

func TestMaximumLoansNeverExceededByConcurrentCheckouts(t *testing.T) {
	l := newLib(t)
	l.set(map[string]any{"self_checkout": true, "max_loans": 3})
	a, b := l.patron("alice"), l.patron("bob")
	const books = 12
	codes := make([]string, books)
	for i := range codes {
		codes[i] = isbn13(i)
		l.addCopy(l.book(fmt.Sprintf("Book %d", i), codes[i]))
	}
	// Both patrons go for every book at once, at the desk and by themselves.
	rs := race(4*books, func(i int) reply {
		p, code := a, codes[i%books]
		if i >= 2*books {
			p = b
		}
		if i%2 == 0 {
			return l.desk(p, code)
		}
		return l.self(p, code)
	})
	noServerErrors(t, rs)
	db := l.db()
	for _, p := range []*pat{a, b} {
		if n := openLoansOfPatron(t, db, p.id); n != 3 {
			t.Errorf("%s has %d loans out, the limit is 3", p.user, n)
		}
	}
	if n := scalar(t, db, `SELECT count(*) FROM (SELECT copy_id FROM loans WHERE returned_at IS NULL GROUP BY copy_id HAVING count(*) > 1)`); n != 0 {
		t.Errorf("%d copies are out twice", n)
	}
	// What was refused for the limit says so.
	r := l.desk(a, codes[0])
	if r.Status != 409 {
		t.Fatalf("a fourth loan: %d %s", r.Status, r.Raw)
	}
	var limited int
	for _, r := range rs {
		if strings.Contains(r.errMessage(), "Limit reached") {
			limited++
		}
	}
	if limited == 0 {
		t.Errorf("no refusal said the limit was reached: %v", statuses(rs))
	}
}

// ---- CIRC-5: renewals -------------------------------------------------------

func TestRenewalLimitHoldsUnderConcurrentRenewals(t *testing.T) {
	l := newLib(t)
	l.set(map[string]any{"loan_period_days": 7, "max_renewals": 2})
	p := l.patron("renewer")
	db := l.db()
	for round := 0; round < 5; round++ {
		isbn := isbn13(round)
		l.addCopy(l.book(fmt.Sprintf("Renewable %d", round), isbn))
		loan := l.mustDesk(p, isbn)
		id := num(loan.JSON, "id")
		due0, _ := time.Parse(time.DateOnly, str(loan.JSON, "due_date"))
		rs := race(12, func(i int) reply {
			if i%2 == 0 {
				return p.w.do("POST", fmt.Sprintf("/api/me/loans/%d/renew", id), nil)
			}
			return l.root.do("POST", fmt.Sprintf("/api/desk/loans/%d/renew", id), nil)
		})
		noServerErrors(t, rs)
		st := statuses(rs)
		if st[200] != 2 || st[409] != 10 {
			t.Fatalf("round %d: the limit is 2 renewals; got %v", round, st)
		}
		var renewals int
		var due string
		if err := db.QueryRow(`SELECT renewals, due_date FROM loans WHERE id = ?`, id).Scan(&renewals, &due); err != nil {
			t.Fatal(err)
		}
		if want := due0.AddDate(0, 0, 14).Format(time.DateOnly); renewals != 2 || due != want {
			t.Fatalf("round %d: renewals %d due %s, want 2 and %s (counted from the due date)", round, renewals, due, want)
		}
		for _, r := range rs {
			if r.Status == 409 && !strings.Contains(r.errMessage(), "renewed the most times") {
				t.Errorf("a refusal without the reason: %s", r.Raw)
			}
		}
	}
}

// ---- CAT-5: removing a copy against a checkout ------------------------------

func TestRemovingACopyAndCheckingItOutAtOnceNeverDoBoth(t *testing.T) {
	l := newLib(t)
	l.mode("own")
	p := l.patron("borrower")
	db := l.db()
	b := l.book("Contested", isbn13(1))
	l.addCopy(b)
	var checkedOut, removed int
	for round := 0; round < 40; round++ {
		c := l.addCopy(b)
		var co, rm reply
		rs := race(2, func(i int) reply {
			if i == 0 {
				return l.desk(p, c.Barcode)
			}
			return l.root.do("DELETE", fmt.Sprintf("/api/copies/%d", c.ID), nil)
		})
		co, rm = rs[0], rs[1]
		noServerErrors(t, rs)
		switch {
		case co.Status == 200 && rm.Status == 409:
			checkedOut++
		case co.Status != 200 && rm.Status == 204:
			removed++
		default:
			t.Fatalf("round %d: checkout %d %s / remove %d %s: both won or both lost", round, co.Status, co.Raw, rm.Status, rm.Raw)
		}
		if n := scalar(t, db, `SELECT count(*) FROM loans l JOIN copies c ON c.id = l.copy_id
			WHERE l.returned_at IS NULL AND c.removed_at IS NOT NULL`); n != 0 {
			t.Fatalf("round %d: %d open loans on removed copies", round, n)
		}
		if co.Status == 200 {
			l.checkinLoan(c.Barcode, num(co.JSON, "id"))
		}
	}
	t.Logf("checkout won %d times, removal won %d times", checkedOut, removed)
}

// ---- CAT-3 / CAT-9: numbering and barcodes ----------------------------------

func TestConcurrentCopyAddsGetDistinctNumbersAndBarcodes(t *testing.T) {
	for _, mode := range []string{"isbn", "own"} {
		t.Run(mode, func(t *testing.T) {
			l := newLib(t)
			l.mode(mode)
			b := l.book("Popular", isbn13(7))
			const n = 20
			cs := make([]copyRec, n)
			rs := race(n, func(i int) reply {
				c, r := l.tryCopy(b, "")
				cs[i] = c
				return r
			})
			noServerErrors(t, rs)
			if st := statuses(rs); st[201] != n {
				t.Fatalf("every add should succeed: %v", st)
			}
			var nums []int
			codes := map[string]bool{}
			for _, c := range cs {
				nums = append(nums, c.Number)
				if mode == "own" {
					if c.Barcode == "" || codes[c.Barcode] {
						t.Fatalf("barcode %q missing or repeated", c.Barcode)
					}
					codes[c.Barcode] = true
				}
			}
			sort.Ints(nums)
			for i, v := range nums {
				if v != i+1 {
					t.Fatalf("copy numbers are %v, want 1..%d with no gap or repeat", nums, n)
				}
			}
		})
	}
}

func TestTwoCopiesGivenOneBarcodeAtOnceOnlyOneKeepsIt(t *testing.T) {
	l := newLib(t)
	a, b := l.book("Alpha", isbn13(1)), l.book("Beta", isbn13(2))
	rs := race(10, func(i int) reply {
		_, r := l.tryCopy([]int64{a, b}[i%2], "SHELF-0042")
		return r
	})
	noServerErrors(t, rs)
	if st := statuses(rs); st[201] != 1 {
		t.Fatalf("one barcode, ten adds: want exactly one 201, got %v", st)
	}
	db := l.db()
	if n := scalar(t, db, `SELECT count(*) FROM identifiers WHERE code = 'SHELF-0042'`); n != 1 {
		t.Fatalf("the barcode is held %d times", n)
	}
	// The losers left no copy behind.
	if n := scalar(t, db, `SELECT count(*) FROM copies`); n != 1 {
		t.Fatalf("%d copies exist after one successful add", n)
	}
}

func TestGeneratedBarcodesNeverCollideWithCardsOrEachOther(t *testing.T) {
	l := newLib(t)
	l.mode("own")
	cards := map[string]bool{}
	for i := 0; i < 15; i++ {
		cards[l.patron(fmt.Sprintf("card%d", i)).card] = true
	}
	b := l.book("Many", isbn13(3))
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		c := l.addCopy(b)
		if cards[c.Barcode] || seen[c.Barcode] {
			t.Fatalf("barcode %q repeats or equals a card number", c.Barcode)
		}
		seen[c.Barcode] = true
	}
	// A card number typed in as a sticker is refused.
	for card := range cards {
		if _, r := l.tryCopy(b, card); r.Status < 400 || r.Status >= 500 {
			t.Fatalf("a card number as a copy barcode: %d %s", r.Status, r.Raw)
		}
		break
	}
}

// CIRC-1: by ISBN the lowest-numbered available copy is lent, gaps in the numbering included.
func TestByISBNLendsTheLowestAvailableCopyWithGaps(t *testing.T) {
	l := newLib(t)
	p := l.patron("reader")
	isbn := isbn13(5)
	b := l.book("Gappy", isbn)
	var cs []copyRec
	for i := 0; i < 4; i++ {
		cs = append(cs, l.addCopy(b))
	}
	// Copies 1 and 3 are taken off the shelf: 2 and 4 remain.
	for _, i := range []int{0, 2} {
		l.root.want(204, "DELETE", fmt.Sprintf("/api/copies/%d", cs[i].ID), nil)
	}
	first := l.mustDesk(p, isbn)
	if n := num(first.JSON, "copy_number"); n != 2 {
		t.Fatalf("copies 2 and 4 are available: got copy %d", n)
	}
	if n := num(l.mustDesk(p, isbn).JSON, "copy_number"); n != 4 {
		t.Fatalf("copy 2 is out: want copy 4, got %d", n)
	}
	r := l.desk(p, isbn)
	if r.Status != 409 || !strings.Contains(r.errMessage(), "No copy") {
		t.Fatalf("all copies out: %d %s", r.Status, r.Raw)
	}
	// Copy 2 comes back, and it is the one lent next, not a later one.
	l.checkinLoan(isbn, num(first.JSON, "id"))
	if n := num(l.mustDesk(p, isbn).JSON, "copy_number"); n != 2 {
		t.Fatalf("copy 2 was returned: want copy 2 again, got %d", n)
	}
}

// ---- CIRC-7 / CIRC-1: who may check out --------------------------------------

func TestSelfCheckoutIsOffByDefaultAndNeedsOptionAndPermission(t *testing.T) {
	l := newLib(t)
	isbn := isbn13(9)
	l.addCopy(l.book("Self", isbn))
	l.addCopy(l.book("Other", isbn13(10)))
	p := l.patron("selfer")
	db := l.db()
	if v := l.root.want(200, "GET", "/api/admin/settings", nil).JSON["self_checkout"]; v != false {
		t.Fatalf("self-checkout on a fresh installation: %v", v)
	}
	refused := func(why string, r reply) {
		t.Helper()
		if r.Status < 400 || r.Status >= 500 {
			t.Fatalf("%s: want a refusal, got %d %s", why, r.Status, r.Raw)
		}
		if n := scalar(t, db, `SELECT count(*) FROM loans`); n != 0 {
			t.Fatalf("%s: a loan was made", why)
		}
	}
	refused("option off", l.self(p, isbn))
	l.set(map[string]any{"self_checkout": true})
	// The permission switched off, the option on.
	l.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/circ.self_checkout", p.id), map[string]bool{"on": false})
	refused("permission off", l.self(p, isbn))
	l.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/circ.self_checkout", p.id), map[string]bool{"on": true})
	p.w.mustSignIn(p.user, adminPassword) // switching a permission off ended the session
	// Both on: the book's ISBN lends a copy and says which.
	r := l.self(p, isbn)
	if r.Status != 200 || num(r.JSON, "copy_number") != 1 || str(r.JSON, "due_date") == "" {
		t.Fatalf("self-checkout with option and permission: %d %s", r.Status, r.Raw)
	}
	// Every copy out: refused, and says so.
	again := l.patron("another")
	if r := l.self(again, isbn); r.Status != 409 || !strings.Contains(r.errMessage(), "available") && !strings.Contains(r.errMessage(), "already out") {
		t.Fatalf("no copy available: %d %s", r.Status, r.Raw)
	}
	// A patron can't return, whatever they hold.
	p.w.want(403, "POST", "/api/desk/checkin", map[string]any{"code": isbn})
	if n := scalar(t, db, `SELECT count(*) FROM loans WHERE returned_at IS NULL`); n != 1 {
		t.Fatalf("a patron's refused return changed the loans: %d open", n)
	}
	// Option off again: refused again, the existing loan untouched.
	l.set(map[string]any{"self_checkout": false})
	if r := l.self(again, isbn13(10)); r.Status < 400 || r.Status >= 500 {
		t.Fatalf("option switched off again: %d %s", r.Status, r.Raw)
	}
}

func TestDeactivatedPatronCannotBorrowUntilReactivated(t *testing.T) {
	l := newLib(t)
	l.set(map[string]any{"self_checkout": true})
	isbn := isbn13(11)
	l.addCopy(l.book("Denied", isbn))
	p := l.patron("lapsed")
	db := l.db()
	l.root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", p.id), nil)
	if r := l.desk(p, isbn); r.Status != 409 || !strings.Contains(r.errMessage(), "deactivated") {
		t.Fatalf("desk checkout for a deactivated patron: %d %s", r.Status, r.Raw)
	}
	if r := l.self(p, isbn); r.Status != 401 {
		t.Fatalf("a deactivated patron's old session: want 401, got %d %s", r.Status, r.Raw)
	}
	if n := scalar(t, db, `SELECT count(*) FROM loans`); n != 0 {
		t.Fatalf("%d loans made for a deactivated patron", n)
	}
	l.root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/reactivate", p.id), nil)
	l.mustDesk(p, isbn)
}

// ---- CAT-9: switching the mode keeps copies and loans ------------------------

func TestSwitchingCopyIdentificationKeepsCopiesAndLoans(t *testing.T) {
	l := newLib(t)
	p := l.patron("keeper")
	isbn := isbn13(12)
	b := l.book("Three copies", isbn)
	for i := 0; i < 3; i++ {
		l.addCopy(b)
	}
	loan := l.mustDesk(p, isbn) // copy 1 out
	loanID := num(loan.JSON, "id")
	db := l.db()
	snapshot := func() string {
		var out []string
		rows, err := db.Query(`SELECT id, copy_id, patron_id, checked_out_at, due_date, renewals, coalesce(returned_at,'') FROM loans ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, cid, pid, ren int64
			var co, due, ret string
			if err := rows.Scan(&id, &cid, &pid, &co, &due, &ren, &ret); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(id, cid, pid, co, due, ren, ret))
		}
		return strings.Join(out, "|")
	}
	before := snapshot()

	l.mode("own")
	if snapshot() != before {
		t.Fatal("switching to own barcodes changed a loan")
	}
	rows, err := db.Query(`SELECT c.number, coalesce(i.code, '') FROM copies c LEFT JOIN identifiers i ON i.copy_id = c.id
		WHERE c.book_id = ? AND c.removed_at IS NULL`, b)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]int{} // barcode -> copy number
	for rows.Next() {
		var number int
		var code string
		if err := rows.Scan(&number, &code); err != nil {
			t.Fatal(err)
		}
		if code == "" || codes[code] != 0 {
			t.Fatalf("copy %d has no unique barcode after the switch (%q)", number, code)
		}
		codes[code] = number
	}
	rows.Close()
	if len(codes) != 3 {
		t.Fatalf("copies after the switch: %d", len(codes))
	}
	// The ISBN no longer identifies a copy in this mode.
	if r := l.desk(p, isbn); r.Status < 400 || r.Status >= 500 || !strings.Contains(strings.ToLower(r.errMessage()), "barcode") {
		t.Fatalf("ISBN scanned in own-barcode mode: %d %s", r.Status, r.Raw)
	}

	l.mode("isbn")
	if snapshot() != before {
		t.Fatal("switching back changed a loan")
	}
	for code, number := range codes {
		if number == 1 {
			continue // out
		}
		r := l.root.want(200, "POST", "/api/desk/resolve", map[string]string{"code": code, "step": "item"})
		if str(r.JSON, "kind") != "copy" || int(num(r.JSON["copy"].(map[string]any), "number")) != number {
			t.Fatalf("after switching back, barcode %s should identify copy %d: %s", code, number, r.Raw)
		}
	}
	// And the book's ISBN still lends the lowest available copy: 2.
	if n := num(l.mustDesk(p, isbn).JSON, "copy_number"); n != 2 {
		t.Fatalf("by ISBN after the round trip: copy %d", n)
	}
	// The loan from before the switches is the same one and can still be renewed and returned.
	l.root.want(200, "POST", fmt.Sprintf("/api/desk/loans/%d/renew", loanID), nil)
	l.checkinLoan(isbn, loanID)
}

// ---- CAT-5 / ADMIN-3: history and reports survive removal --------------------

func TestHistoryAndReportsSurviveRemovingTheCopyAndBook(t *testing.T) {
	l := newLib(t)
	p := l.patron("historian")
	isbn := isbn13(13)
	b := l.book("Short lived", isbn)
	c := l.addCopy(b)
	loan := l.mustDesk(p, isbn)
	l.checkinLoan(isbn, num(loan.JSON, "id"))
	l.root.want(204, "DELETE", fmt.Sprintf("/api/copies/%d", c.ID), nil)
	l.root.want(204, "DELETE", fmt.Sprintf("/api/books/%d", b), nil)

	for _, who := range []struct {
		name *who
		path string
	}{
		{p.w, "/api/me/loans/history"},
		{l.root, fmt.Sprintf("/api/patron-loans/%d", p.id)},
	} {
		r := who.name.want(200, "GET", who.path, nil)
		ls, _ := r.JSON["loans"].([]any)
		if len(ls) != 1 || str(ls[0].(map[string]any), "title") != "Short lived" {
			t.Fatalf("%s after removal: %s", who.path, r.Raw)
		}
	}
	rep := l.root.want(200, "GET", "/api/admin/reports/circulation?from=2026-01-15&to=2026-01-15", nil)
	if num(rep.JSON, "checkouts") != 1 || num(rep.JSON, "returns") != 1 {
		t.Fatalf("report after removal: %s", rep.Raw)
	}
	st := l.root.want(200, "GET", "/api/admin/reports/statistics", nil)
	if num(st.JSON, "books") != 0 || num(st.JSON, "copies") != 0 || num(st.JSON, "current_loans") != 0 {
		t.Fatalf("statistics after removal: %s", st.Raw)
	}
}

// ---- CIRC-1 / CIRC-5 / CIRC-6: the library's zone ----------------------------

// The library is in Auckland (UTC+13 in January); the server's own zone is
// Honolulu (UTC-10). A loan checked out at 01:30 Auckland time on 16 January
// is still 15 January in UTC and in Honolulu, and is due on the 30th by the
// library's calendar. It falls overdue when the library's clock reaches
// 00:00:00 on the 31st, not a second sooner and not when the server's zone
// or UTC does.
func TestDueAndOverdueFollowTheLibrarysZoneNotTheServers(t *testing.T) {
	l := newLibInZone(t, "Pacific/Honolulu")
	l.set(map[string]any{"time_zone": "Pacific/Auckland", "loan_period_days": 14, "max_renewals": 3})
	p := l.patron("islander")
	var isbns [3]string
	for i := range isbns {
		isbns[i] = isbn13(20 + i)
		l.addCopy(l.book(fmt.Sprintf("Zoned %d", i), isbns[i]))
	}
	l.at("2026-01-15T12:30:00Z") // 2026-01-16 01:30 in Auckland
	var ids [3]int64
	for i, isbn := range isbns {
		r := l.mustDesk(p, isbn)
		ids[i] = num(r.JSON, "id")
		if due := str(r.JSON, "due_date"); due != "2026-01-30" {
			t.Fatalf("due date %s, want 2026-01-30 (library calendar, not UTC's 29th)", due)
		}
	}
	overdue := func() []map[string]any {
		r := l.root.want(200, "GET", "/api/desk/overdue", nil)
		var out []map[string]any
		for _, v := range r.JSON["loans"].([]any) {
			out = append(out, v.(map[string]any))
		}
		return out
	}

	l.at("2026-01-30T10:59:59Z") // 23:59:59 on the 30th in Auckland
	if o := overdue(); len(o) != 0 {
		t.Fatalf("at 23:59:59 on the due date nothing is overdue: %v", o)
	}
	if st := l.root.want(200, "GET", "/api/admin/reports/statistics", nil); num(st.JSON, "overdue_loans") != 0 {
		t.Fatalf("statistics at 23:59:59: %s", st.Raw)
	}
	r := p.w.want(200, "POST", fmt.Sprintf("/api/me/loans/%d/renew", ids[1]), nil)
	if str(r.JSON, "due_date") != "2026-02-13" {
		t.Fatalf("renewed at 23:59:59 on the due date: %s", r.Raw)
	}

	l.at("2026-01-30T11:00:00Z") // 00:00:00 on the 31st in Auckland
	o := overdue()
	if len(o) != 2 {
		t.Fatalf("at 00:00:00 after the due date two loans are overdue (the renewed one is not): %v", o)
	}
	for _, m := range o {
		if num(m, "days_late") != 1 || m["overdue"] != true || num(m, "patron_id") != p.id {
			t.Fatalf("overdue entry: %v", m)
		}
	}
	if st := l.root.want(200, "GET", "/api/admin/reports/statistics", nil); num(st.JSON, "overdue_loans") != 2 {
		t.Fatalf("statistics at 00:00:00: %s", st.Raw)
	}
	// An overdue loan can't be renewed, by the patron or by a librarian, and says why.
	for name, r := range map[string]reply{
		"patron":    p.w.do("POST", fmt.Sprintf("/api/me/loans/%d/renew", ids[0]), nil),
		"librarian": l.root.do("POST", fmt.Sprintf("/api/desk/loans/%d/renew", ids[2]), nil),
	} {
		if r.Status != 409 || !strings.Contains(r.errMessage(), "overdue") {
			t.Fatalf("%s renewing an overdue loan: %d %s", name, r.Status, r.Raw)
		}
	}
	db := l.db()
	if n := scalar(t, db, `SELECT sum(renewals) FROM loans`); n != 1 {
		t.Fatalf("refused renewals changed the loans: %d renewals", n)
	}
	// The renewed loan, and the overdue ones once returned, leave the list.
	l.checkinLoan(isbns[0], ids[0])
	if o := overdue(); len(o) != 1 || num(o[0], "id") != ids[2] {
		t.Fatalf("overdue after one return: %v", o)
	}
}

// Days the clocks change are 23 and 25 hours long; a loan is overdue from the
// library's midnight, whatever its length. The server's own zone is far away.
func TestOverdueOnDaysTheClocksChange(t *testing.T) {
	l := newLibInZone(t, "Asia/Kolkata")
	l.set(map[string]any{"time_zone": "America/Chicago", "loan_period_days": 1, "max_renewals": 1})
	p := l.patron("chicagoan")
	n := 0
	lend := func(at string) (map[string]any, string) {
		n++
		isbn := isbn13(40 + n)
		l.addCopy(l.book(fmt.Sprintf("Clocks %d", n), isbn))
		l.at(at)
		return l.mustDesk(p, isbn).JSON, isbn
	}
	overdueIDs := func() map[int64]bool {
		out := map[int64]bool{}
		for _, v := range l.root.want(200, "GET", "/api/desk/overdue", nil).JSON["loans"].([]any) {
			out[num(v.(map[string]any), "id")] = true
		}
		return out
	}

	// Around local midnight, and around the 2 a.m. gap, on 8 March 2026 (Chicago springs forward at 08:00Z).
	for _, c := range []struct{ at, due string }{
		{"2026-03-08T05:59:59Z", "2026-03-08"}, // 23:59:59 on the 7th
		{"2026-03-08T06:00:00Z", "2026-03-09"}, // 00:00:00 on the 8th
		{"2026-03-08T07:59:59Z", "2026-03-09"}, // 01:59:59 CST, the last second before the gap
		{"2026-03-08T08:00:00Z", "2026-03-09"}, // 03:00:00 CDT
	} {
		loan, _ := lend(c.at)
		if due := str(loan, "due_date"); due != c.due {
			t.Errorf("checked out at %s: due %s, want %s", c.at, due, c.due)
		}
	}

	// A loan due on the 23-hour day: overdue at 00:00 CDT on the 9th = 05:00Z (not 06:00Z).
	lent, _ := lend("2026-03-07T18:00:00Z")
	if str(lent, "due_date") != "2026-03-08" {
		t.Fatalf("due date: %v", lent)
	}
	id := num(lent, "id")
	l.at("2026-03-09T04:59:59Z")
	if overdueIDs()[id] {
		t.Fatal("overdue one second before midnight on the short day")
	}
	l.at("2026-03-09T05:00:00Z")
	if !overdueIDs()[id] {
		t.Fatal("not overdue at midnight after the short day")
	}
	if r := l.root.do("POST", fmt.Sprintf("/api/desk/loans/%d/renew", id), nil); r.Status != 409 || !strings.Contains(r.errMessage(), "overdue") {
		t.Fatalf("renewing it: %d %s", r.Status, r.Raw)
	}

	// A loan due on the 25-hour day (1 November 2026; Chicago falls back at 07:00Z): overdue from 06:00Z on the 2nd, not 05:00Z.
	lent, _ = lend("2026-10-31T18:00:00Z")
	if str(lent, "due_date") != "2026-11-01" {
		t.Fatalf("due date: %v", lent)
	}
	id = num(lent, "id")
	l.at("2026-11-02T05:59:59Z")
	if overdueIDs()[id] {
		t.Fatal("overdue one second before midnight on the long day")
	}
	if r := l.root.do("POST", fmt.Sprintf("/api/desk/loans/%d/renew", id), nil); r.Status != 200 {
		t.Fatalf("renewing on the last second of the due day: %d %s", r.Status, r.Raw)
	}
	l.at("2026-11-02T06:00:00Z")
	// renewed once, due Nov 2 now: still not overdue at Nov 2 00:00 local.
	if overdueIDs()[id] {
		t.Fatal("a loan renewed to the 2nd is overdue at the start of the 2nd")
	}
}

// ---- ADMIN-3: the report's days are the library's days -----------------------

func TestReportBoundariesAreLocalMidnight(t *testing.T) {
	l := newLib(t)
	l.set(map[string]any{"time_zone": "Pacific/Auckland", "max_loans": 10})
	p := l.patron("counted")
	isbns := map[string]string{}
	for _, k := range []string{"A", "B", "C", "D", "E"} {
		isbns[k] = isbn13(60 + int(k[0]-'A'))
		l.addCopy(l.book("Report "+k, isbns[k]))
	}
	loans := map[string]int64{}
	// Auckland is UTC+13 in January and February: local midnight is 11:00Z the day before.
	type step struct {
		at, kind, loan string
	}
	steps := []step{
		{"2025-12-30T00:00:00Z", "out", "E"},
		{"2025-12-31T10:59:59Z", "out", "A"}, // 23:59:59 on 31 December
		{"2025-12-31T10:59:59Z", "in", "E"},  // 23:59:59 on 31 December
		{"2025-12-31T11:00:00Z", "in", "A"},  // 00:00:00 on 1 January
		{"2025-12-31T11:00:00Z", "out", "B"}, // 00:00:00 on 1 January
		{"2026-01-31T10:59:59Z", "out", "C"}, // 23:59:59 on 31 January
		{"2026-01-31T10:59:59Z", "in", "B"},  // 23:59:59 on 31 January
		{"2026-01-31T11:00:00Z", "in", "C"},  // 00:00:00 on 1 February
		{"2026-01-31T11:00:00Z", "out", "D"}, // 00:00:00 on 1 February
	}
	for _, s := range steps {
		l.at(s.at)
		switch s.kind {
		case "out":
			loans[s.loan] = num(l.mustDesk(p, isbns[s.loan]).JSON, "id")
		default:
			l.checkinLoan(isbns[s.loan], loans[s.loan])
		}
	}
	for _, c := range []struct {
		from, to     string
		checks, rets int
		whatItCovers string
	}{
		{"2026-01-01", "2026-01-31", 2, 2, "January: B and C out; A and B in"},
		{"2025-12-01", "2025-12-31", 2, 1, "December: E and A out; E in"},
		{"2026-02-01", "2026-02-01", 1, 1, "1 February: D out; C in"},
		{"2026-01-01", "2026-01-01", 1, 1, "1 January alone: B out; A in"},
		{"2026-01-31", "2026-01-31", 1, 1, "31 January alone: C out; B in"},
		{"2025-12-01", "2026-02-28", 5, 4, "the whole span"},
	} {
		r := l.root.want(200, "GET", "/api/admin/reports/circulation?from="+c.from+"&to="+c.to, nil)
		if int(num(r.JSON, "checkouts")) != c.checks || int(num(r.JSON, "returns")) != c.rets {
			t.Errorf("%s (%s to %s): got %s", c.whatItCovers, c.from, c.to, r.Raw)
		}
		if len(r.JSON) != 4 || strings.Contains(r.Raw, "counted") || strings.Contains(r.Raw, "patron") {
			t.Errorf("the report is counts only: %s", r.Raw)
		}
	}
}

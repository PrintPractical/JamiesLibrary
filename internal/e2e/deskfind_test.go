package e2e

// Name search at the checkout desk (CIRC-8) judged from outside: POST
// /api/desk/find over HTTP against the running server. The people who may use
// it, what it may tell them, what hostile input does to it, its bounds, what
// choosing a match does at checkout, and what it leaves in the logs.

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/testutil"
)

type deskFindWorld struct {
	t    *testing.T
	s    *testutil.Server
	root *who
	ids  map[string]int64
	web  map[string]*who
	card map[string]string
}

// newDeskFindWorld is a library with people to find and people who search:
// "deskonly" holds circ.desk and nothing else, "lookuponly" holds patrons.lookup
// and nothing else.
func newDeskFindWorld(t *testing.T) *deskFindWorld {
	t.Helper()
	s, root := install(t, nil)
	d := &deskFindWorld{t: t, s: s, root: root, ids: map[string]int64{}, web: map[string]*who{}, card: map[string]string{}}
	d.add("patron", "ann.reader", "Ann Reader")
	d.add("patron", "annabel", "Annabel Lee")
	d.add("librarian", "ann.staff", "Ann Staff")
	d.add("patron", "ann.gone", "Ann Gone")
	d.add("patron", "selfer", "Self Checkout Patron")
	d.add("patron", "deskonly", "Desk Only")
	d.add("patron", "lookuponly", "Lookup Only")
	d.only("deskonly", perm.CircDesk)
	d.only("lookuponly", perm.PatronsLookup)
	root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", d.ids["ann.gone"]), nil)
	d.ids["root"] = 1
	return d
}

func (d *deskFindWorld) add(role, username, display string) {
	d.t.Helper()
	_, code := d.root.invite(role)
	w := newWho(d.t, d.s)
	r := w.want(201, "POST", "/api/register", map[string]string{
		"code": code, "username": username, "display_name": display, "password": adminPassword})
	u, _ := r.JSON["user"].(map[string]any)
	d.ids[username] = int64(u["id"].(float64))
	d.web[username] = w
	if c := w.do("GET", "/api/me/card", nil); c.Status == 200 {
		d.card[username] = str(c.JSON, "number")
	}
}

// only leaves the account holding exactly one permission.
func (d *deskFindWorld) only(username string, keep perm.Permission) {
	d.t.Helper()
	for _, p := range perm.All {
		d.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/%s", d.ids[username], p), map[string]bool{"on": p == keep})
	}
	d.web[username] = d.signedIn(username)
	got := perms(d.web[username].want(200, "GET", "/api/me", nil))
	if len(got) != 1 || !got[string(keep)] {
		d.t.Fatalf("%s holds %v, want only %s", username, got, keep)
	}
}

func (d *deskFindWorld) signedIn(username string) *who {
	n := newWho(d.t, d.s)
	n.mustSignIn(username, adminPassword)
	return n
}

func find(w *who, q string) reply { return w.do("POST", "/api/desk/find", map[string]string{"q": q}) }

func (d *deskFindWorld) matches(w *who, q string) []map[string]any {
	d.t.Helper()
	r := find(w, q)
	if r.Status != 200 || str(r.JSON, "kind") != "matches" {
		d.t.Fatalf("find %q: %d %s", q, r.Status, r.Raw)
	}
	var out []map[string]any
	for _, m := range r.JSON["patrons"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func usernames(ms []map[string]any) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m["username"].(string))
	}
	sort.Strings(out)
	return out
}

// ---- who may search ---------------------------------------------------------

// CIRC-8 "Permission to check out only": circ.desk alone is enough, and sees
// what an administrator sees; the same account is still kept out of every
// route that returns a patron's loans.
func TestDeskFindNeedsOnlyCircDeskAndOpensNothingElse(t *testing.T) {
	d := newDeskFindWorld(t)
	desk := d.web["deskonly"]
	for _, q := range []string{"ann", "ANN", "reader", "a"} {
		got, want := d.matches(desk, q), d.matches(d.root, q)
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("q=%q: desk-only saw %v, administrator saw %v", q, got, want)
		}
	}
	id := d.ids["ann.reader"]
	for _, p := range []string{
		"/api/patrons?q=ann", fmt.Sprintf("/api/patrons/%d", id), fmt.Sprintf("/api/patron-loans/%d", id),
		"/api/patrons/by-card/" + d.card["ann.reader"],
	} {
		if r := desk.do("GET", p, nil); r.Status != 403 {
			t.Errorf("GET %s as circ.desk only: %d, want 403: %.200s", p, r.Status, r.Raw)
		}
		if r := d.web["lookuponly"].do("GET", p, nil); r.Status != 200 {
			t.Errorf("GET %s as patrons.lookup only: %d, want 200 (the route must still work): %.200s", p, r.Status, r.Raw)
		}
	}
}

func TestDeskFindRefusesEveryoneWithoutCircDesk(t *testing.T) {
	d := newDeskFindWorld(t)
	// the preset patron holds circ.self_checkout and no circ.desk
	if p := perms(d.web["ann.reader"].want(200, "GET", "/api/me", nil)); !p["circ.self_checkout"] || p["circ.desk"] {
		t.Fatalf("a patron's permissions: %v", p)
	}
	for _, who := range []string{"ann.reader", "lookuponly"} {
		if r := find(d.web[who], "ann"); r.Status != 403 {
			t.Errorf("%s: %d, want 403: %.200s", who, r.Status, r.Raw)
		}
	}
	if r := find(newWho(t, d.s), "ann"); r.Status != 401 {
		t.Errorf("signed out: %d, want 401: %.200s", r.Status, r.Raw)
	}
	// a card in the query is no way round it
	if r := find(d.web["ann.reader"], d.card["ann.staff"]); r.Status != 403 || strings.Contains(r.Raw, "Ann Staff") {
		t.Errorf("a patron searching a card: %d %.200s", r.Status, r.Raw)
	}
	// switching circ.desk off ends the access at once
	d.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/circ.desk", d.ids["deskonly"]), map[string]bool{"on": false})
	if r := find(d.web["deskonly"], "ann"); r.Status != 403 && r.Status != 401 {
		t.Errorf("after circ.desk was taken away: %d, want 403: %.200s", r.Status, r.Raw)
	}
}

// Self-checkout never reaches other people's names, and nothing under /api/me lists users.
func TestSelfCheckoutNeverListsOtherUsers(t *testing.T) {
	d := newDeskFindWorld(t)
	d.root.want(200, "PUT", "/api/admin/settings", map[string]any{"self_checkout": true})
	pat := d.web["ann.reader"]
	for _, p := range []string{"/api/me", "/api/me/card", "/api/me/loans", "/api/me/loans?q=ann", "/api/me/patrons", "/api/me/users", "/api/me/find"} {
		r := pat.do("GET", p, nil)
		for _, other := range []string{"Annabel", "Ann Staff", "Ann Gone", "deskonly", "lookuponly", d.card["annabel"], d.card["ann.staff"]} {
			if strings.Contains(r.Raw, other) {
				t.Errorf("GET %s tells a patron about %q: %.300s", p, other, r.Raw)
			}
		}
	}
	for _, m := range []string{"GET", "POST"} {
		if r := pat.do(m, "/api/me/find", map[string]string{"q": "ann"}); r.Status < 400 {
			t.Errorf("%s /api/me/find answered %d", m, r.Status)
		}
	}
}

// ---- what it returns --------------------------------------------------------

var matchKeys = map[string]bool{"id": true, "display_name": true, "username": true, "card": true, "active": true, "out": true, "overdue": true, "limit": true}
var topKeys = map[string]bool{"kind": true, "patrons": true, "patron": true, "more": true}

func assertKeys(t *testing.T, what string, m map[string]any, allowed map[string]bool) {
	t.Helper()
	for k := range m {
		if !allowed[k] {
			t.Errorf("%s has the field %q, which a circ.desk holder may not be given: %v", what, k, m)
		}
	}
}

// Only the allowlisted fields, for a name search and for a card, whichever role asks.
func TestDeskFindAnswersOnlyAllowlistedFields(t *testing.T) {
	d := newDeskFindWorld(t)
	// give the patron a loan, so loan data exists to leak
	_, code := d.root.invite("patron") // an unused invitation: its code must not leak either
	b := d.root.want(201, "POST", "/api/books", map[string]any{"title": "Leaky Title", "authors": []string{"Ann Author"}}).JSON["id"]
	loc := idOf(d.root.want(201, "POST", "/api/locations", map[string]string{"name": "Shelf"}), "id")
	cp := d.root.want(201, "POST", fmt.Sprintf("/api/books/%d/copies", int64(b.(float64))), map[string]any{"location_id": loc, "condition": "good"})
	d.root.want(200, "POST", "/api/desk/checkout", map[string]string{"patron": d.card["ann.reader"], "code": str(cp.JSON, "barcode")})
	d.root.want(200, "PUT", "/api/admin/settings", map[string]any{"self_checkout": true})

	for _, w := range []*who{d.web["deskonly"], d.root} {
		for _, q := range []string{"ann", "a", d.card["ann.reader"], d.card["ann.staff"]} {
			r := find(w, q)
			if r.Status != 200 {
				t.Fatalf("find %q: %d %s", q, r.Status, r.Raw)
			}
			assertKeys(t, "answer to "+q, r.JSON, topKeys)
			if ms, _ := r.JSON["patrons"].([]any); ms != nil {
				for _, m := range ms {
					assertKeys(t, "a match", m.(map[string]any), matchKeys)
				}
			}
			if p, _ := r.JSON["patron"].(map[string]any); p != nil {
				assertKeys(t, "the patron", p, matchKeys)
			}
			for _, secret := range []string{"Leaky Title", code, "last_sign_in", "preset", "role", "permissions", "created", "password", "email"} {
				if strings.Contains(r.Raw, secret) {
					t.Errorf("find %q leaks %q: %.300s", q, secret, r.Raw)
				}
			}
		}
	}
	// the loan is counted, not described
	for _, m := range d.matches(d.web["deskonly"], "ann.reader") {
		if m["out"].(float64) != 1 {
			t.Errorf("out: %v", m)
		}
	}
}

// ---- what it finds ----------------------------------------------------------

// An exact card names its holder; a prefix or a part of a card names no one.
func TestDeskFindCardIsExactOnly(t *testing.T) {
	d := newDeskFindWorld(t)
	card := d.card["ann.reader"]
	for _, w := range []*who{d.web["deskonly"], d.root} {
		r := find(w, card)
		p, _ := r.JSON["patron"].(map[string]any)
		if r.Status != 200 || str(r.JSON, "kind") != "patron" || p == nil || p["card"] != card || int64(p["id"].(float64)) != d.ids["ann.reader"] {
			t.Fatalf("exact card: %d %s", r.Status, r.Raw)
		}
		if r := find(w, " "+card+" "); str(r.JSON, "kind") != "patron" {
			t.Errorf("a card with spaces round it: %s", r.Raw)
		}
	}
	for _, q := range []string{card[:len(card)-1], card[:12-1], card[:6], card[3:9], card[1 : len(card)-1], card[len(card)-4:], card + "0"} {
		got := d.matches(d.web["deskonly"], q)
		if len(got) != 0 {
			t.Errorf("part of a card %q found %v", q, got)
		}
	}
}

// Name and username, any case; staff and administrators, deactivated accounts and accounts with no card are listed too.
func TestDeskFindListsEveryKindOfAccount(t *testing.T) {
	d := newDeskFindWorld(t)
	// an account that has no card: the card row is removed, as an old library may have it
	db, err := sql.Open("sqlite", "file:"+d.s.DataDir+"/library.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM identifiers WHERE user_id = ?`, d.ids["annabel"]); err != nil {
		t.Fatalf("making a user with no card: %v", err)
	}
	for _, q := range []string{"ann", "ANN", "AnN"} {
		got := d.matches(d.web["deskonly"], q)
		by := map[string]map[string]any{}
		for _, m := range got {
			by[m["username"].(string)] = m
		}
		if want := []string{"ann.gone", "ann.reader", "ann.staff", "annabel"}; !reflect.DeepEqual(usernames(got), want) {
			t.Fatalf("q=%q: %v, want %v", q, usernames(got), want)
		}
		if by["ann.gone"]["active"] != false || by["ann.gone"]["card"] == nil {
			t.Errorf("deactivated: %v", by["ann.gone"])
		}
		if by["annabel"]["active"] != true || by["annabel"]["card"] != "" {
			t.Errorf("no card: %v", by["annabel"])
		}
		if by["ann.staff"]["active"] != true || by["ann.staff"]["card"] != d.card["ann.staff"] {
			t.Errorf("staff: %v", by["ann.staff"])
		}
	}
	// by username, and by the administrator's name
	if got := usernames(d.matches(d.web["deskonly"], "READER")); !reflect.DeepEqual(got, []string{"ann.reader"}) {
		t.Errorf("by username: %v", got)
	}
	if got := usernames(d.matches(d.web["deskonly"], "root")); !reflect.DeepEqual(got, []string{"root"}) {
		t.Errorf("administrator: %v", got)
	}
	if got := d.matches(d.web["deskonly"], "nobody-has-this-name"); len(got) != 0 {
		t.Errorf("no match: %v", got)
	}
}

// ---- hostile input ----------------------------------------------------------

func TestDeskFindHostileInput(t *testing.T) {
	d := newDeskFindWorld(t)
	d.root.want(201, "POST", "/api/admin/invitations", map[string]string{"role": "patron"})
	for i, n := range []struct{ user, name string }{
		{"pct", "100% Pure"}, {"und", "Pure_Gold"}, {"bsl", `Back\slash`}, {"emo", "Zoë 😀 Quinn"}, {"sql", `Robert'); DROP TABLE users;--`}, {"plain", "PureXGold Back slash"},
	} {
		_ = i
		d.add("patron", n.user, n.name)
	}
	desk := d.web["deskonly"]
	lit := func(q string, want ...string) {
		t.Helper()
		if got := usernames(d.matches(desk, q)); !reflect.DeepEqual(got, append([]string{}, want...)) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("q=%q: %v, want %v", q, got, want)
		}
	}
	lit("%", "pct")
	lit("_", "und")
	lit(`\`, "bsl")
	lit(`back\s`, "bsl")
	lit("pure_gold", "und")
	lit("😀", "emo")
	lit("zoë 😀", "emo")
	// an RTL mark can't be in a name, so it finds no one, but it is no error
	lit("\u200f")
	lit("\u200fAnn")
	lit("\u202eAnn")
	lit(`'); DROP TABLE users;--`, "sql")
	lit(`' OR '1'='1`)
	lit(`" OR 1=1 --`)
	lit("%' OR '1'='1")
	if len(d.matches(desk, "root")) != 1 {
		t.Error("the users table is damaged")
	}

	code := func(n int) string { return strings.Repeat("é", n) } // 2 bytes each: bytes are not code points
	if r := find(desk, code(200)); r.Status != 200 {
		t.Errorf("200 code points: %d %s", r.Status, r.Raw)
	}
	if r := find(desk, strings.Repeat("😀", 200)); r.Status != 200 {
		t.Errorf("200 emoji: %d %s", r.Status, r.Raw)
	}
	for name, q := range map[string]string{
		"201 code points": code(201), "201 emoji": strings.Repeat("😀", 201), "empty": "", "spaces": "   ", "tab": "\t", "newline only": "\n",
		"NUL": "a\x00b", "control": "a\x01b", "escape": "\x1bx", "DEL": "a\x7fb", "newline inside": "a\nb", "US": "a\x1fb"} {
		r := find(desk, q)
		if r.Status != 422 || fieldOf(r) != "q" {
			t.Errorf("%s: %d %.200s, want 422 on q", name, r.Status, r.Raw)
		}
	}
	// wrong shapes and sizes are client errors, never a crash
	post := func(body string) reply {
		return desk.raw(rawReq{Method: "POST", Path: "/api/desk/find", Body: []byte(body), ContentType: "application/json"})
	}
	for name, body := range map[string]string{
		"number": `{"q":1}`, "array": `{"q":["a"]}`, "null": `{"q":null}`, "object": `{"q":{}}`, "bool": `{"q":true}`, "not an object": `[]`, "string": `"ann"`,
		"empty body": ``, "truncated": `{"q":"an`, "not JSON": `q=ann`, "extra field": `{"q":"a","x":1}`, "two values": `{"q":"a"}{"q":"b"}`,
		"missing q": `{}`, "huge body": `{"q":"` + strings.Repeat("a", 2<<20) + `"}`, "huge nesting": strings.Repeat("[", 100000),
	} {
		if r := post(body); r.Status < 400 || r.Status >= 500 {
			t.Errorf("%s: %d %.200s, want a 4xx", name, r.Status, r.Raw)
		}
	}
	if r := post(`{"q":"\ud800"}`); r.Status >= 500 { // a lone surrogate is read as U+FFFD
		t.Errorf("lone surrogate: %d %.200s", r.Status, r.Raw)
	}
	// a query string and the wrong method are not the route
	if r := desk.do("GET", "/api/desk/find?q="+url.QueryEscape("ann"), nil); r.Status < 400 || strings.Contains(r.Raw, "Ann Reader") {
		t.Errorf("GET: %d %.200s", r.Status, r.Raw)
	}
	// still fine afterwards
	if r := find(desk, "ann"); r.Status != 200 {
		t.Errorf("after the abuse: %d", r.Status)
	}
}

func fieldOf(r reply) string {
	e, _ := r.JSON["error"].(map[string]any)
	f, _ := e["field"].(string)
	return f
}

// ---- bounds -----------------------------------------------------------------

func TestDeskFindIsBoundedAtFifty(t *testing.T) {
	d := newDeskFindWorld(t)
	for i := 0; i < 60; i++ {
		d.add("patron", fmt.Sprintf("mass%02d", i), fmt.Sprintf("Zmass Person %02d", i))
	}
	for i := 0; i < 50; i++ {
		d.add("patron", fmt.Sprintf("exact%02d", i), fmt.Sprintf("Zexact Person %02d", i))
	}
	for i := 0; i < 51; i++ {
		d.add("patron", fmt.Sprintf("edge%02d", i), fmt.Sprintf("Zedge Person %02d", i))
	}
	desk := d.web["deskonly"]
	r := find(desk, "zmass")
	if ms, _ := r.JSON["patrons"].([]any); len(ms) != 50 || r.JSON["more"] != true {
		t.Errorf("60 matches: %d returned, more=%v", len(ms), r.JSON["more"])
	}
	r = find(desk, "zexact")
	if ms, _ := r.JSON["patrons"].([]any); len(ms) != 50 || r.JSON["more"] != false {
		t.Errorf("exactly 50 matches: %d returned, more=%v", len(ms), r.JSON["more"])
	}
	r = find(desk, "zedge")
	if ms, _ := r.JSON["patrons"].([]any); len(ms) != 50 || r.JSON["more"] != true {
		t.Errorf("51 matches: %d returned, more=%v", len(ms), r.JSON["more"])
	}
	// the broadest search is bounded too
	r = find(desk, "e")
	if ms, _ := r.JSON["patrons"].([]any); len(ms) != 50 || r.JSON["more"] != true {
		t.Errorf("a one-letter search: %d returned, more=%v", len(ms), r.JSON["more"])
	}
}

// ---- choosing a match -------------------------------------------------------

func (d *deskFindWorld) copyCode(title string) string {
	d.t.Helper()
	b := d.root.want(201, "POST", "/api/books", map[string]any{"title": title, "authors": []string{"A. Writer"}}).JSON["id"]
	loc := d.root.want(201, "POST", "/api/locations", map[string]string{"name": "Shelf " + title})
	cp := d.root.want(201, "POST", fmt.Sprintf("/api/books/%d/copies", int64(b.(float64))), map[string]any{"location_id": idOf(loc, "id"), "condition": "good"})
	return str(cp.JSON, "barcode")
}

func (d *deskFindWorld) openLoans(id int64) int {
	db := openRO(d.t, d.s.DataDir)
	return openLoansOfPatron(d.t, db, id)
}

// The server enforces what the screen only hides.
func TestChoosingAMatchCannotBorrowOnADeactivatedOrReissuedCard(t *testing.T) {
	d := newDeskFindWorld(t)
	desk := d.web["deskonly"]

	// a deactivated patron's card is refused, and nothing is lent
	gone := d.matches(desk, "ann.gone")[0]
	if gone["active"] != false {
		t.Fatalf("the deactivated patron shows as active: %v", gone)
	}
	r := desk.do("POST", "/api/desk/checkout", map[string]string{"patron": gone["card"].(string), "code": d.copyCode("For the gone")})
	if r.Status < 400 || r.Status >= 500 || d.openLoans(d.ids["ann.gone"]) != 0 {
		t.Errorf("checkout to a deactivated patron: %d %.200s (loans %d)", r.Status, r.Raw, d.openLoans(d.ids["ann.gone"]))
	}

	// choose a match, then the card is re-issued before the checkout
	chosen := d.matches(desk, "ann.reader")[0]
	old := chosen["card"].(string)
	d.root.want(200, "POST", fmt.Sprintf("/api/users/%d/card/reissue", d.ids["ann.reader"]), nil)
	code := d.copyCode("For the reissued")
	r = desk.do("POST", "/api/desk/checkout", map[string]string{"patron": old, "code": code})
	if r.Status != 404 || d.openLoans(d.ids["ann.reader"]) != 0 {
		t.Errorf("checkout on the old card: %d %.200s (loans %d), want 404 and none", r.Status, r.Raw, d.openLoans(d.ids["ann.reader"]))
	}
	// searching again gives the new card, which works
	fresh := d.matches(desk, "ann.reader")[0]["card"].(string)
	if fresh == old {
		t.Fatal("the card was not re-issued")
	}
	desk.want(200, "POST", "/api/desk/checkout", map[string]string{"patron": fresh, "code": code})
	if d.openLoans(d.ids["ann.reader"]) != 1 {
		t.Error("the new card did not borrow")
	}
	// no route takes a user id in place of a card
	for _, body := range []map[string]any{{"patron_id": d.ids["annabel"], "code": d.copyCode("By id")}, {"patron": d.ids["annabel"], "code": d.copyCode("By number")}} {
		if r := desk.do("POST", "/api/desk/checkout", body); r.Status < 400 || d.openLoans(d.ids["annabel"]) != 0 {
			t.Errorf("checkout by user id: %d %.200s", r.Status, r.Raw)
		}
	}
}

// ---- logs -------------------------------------------------------------------

// What was typed in the search box is in no log: not the server's, not the activity log.
func TestDeskFindQueryIsNeverLogged(t *testing.T) {
	d := newDeskFindWorld(t)
	desk := d.web["deskonly"]
	const canary = "Zqxjcanary Wumpus 8841"
	for _, q := range []string{canary, strings.ToUpper(canary), "ann", "bad\x00" + canary} {
		find(desk, q)
	}
	// a wrong type and a too long one: their errors must not echo it into the log either
	desk.do("POST", "/api/desk/find", map[string]any{"q": []string{canary}})
	find(desk, canary+strings.Repeat("x", 300))
	// a card searched for, too
	find(desk, d.card["ann.reader"])

	log := d.s.Log() + "\n" + d.s.Stdout()
	// the request log is on, so that it holding no query proves something
	if n := strings.Count(log, "path=/api/desk/find"); n < 7 {
		t.Fatalf("the request log has %d lines for the searches, want 7: it would prove nothing", n)
	}
	act := d.root.want(200, "GET", "/api/admin/activity?per_page=100", nil).Raw
	for name, text := range map[string]string{"server log": log, "activity log": act} {
		for _, secret := range []string{"Zqxjcanary", "zqxjcanary", "Wumpus", "8841", d.card["ann.reader"]} {
			if strings.Contains(text, secret) {
				t.Errorf("the %s holds %q", name, secret)
			}
		}
	}
	if strings.Contains(act, "desk/find") || strings.Contains(act, "desk.find") {
		t.Errorf("a search is an activity event: %.300s", act)
	}
	grepDataDirLogs(t, d.s.DataDir, "Zqxjcanary")
}

// grepDataDirLogs fails if a canary is in any file under dir other than the database (a stray log, say).
func grepDataDirLogs(t *testing.T, dir string, secrets ...string) {
	t.Helper()
	for _, f := range dataFiles(t, dir) {
		if strings.HasSuffix(f, ".db") || strings.HasSuffix(f, "-wal") || strings.HasSuffix(f, "-shm") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%q is written in %s", s, f)
			}
		}
	}
}

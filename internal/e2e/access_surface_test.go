package e2e

// ACCESS-1, ACCESS-4/CAT-6, ROLE-3 and the browser protections, over the whole
// API: an anonymous crawl, what a visitor sees when the catalog is public,
// patrons reaching for each other's records, cross-site and form posts on every
// state-changing route, and a permission switched off while the user is signed in.

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/testutil"
)

// pathFor fills a route's path values with ones that look real.
func (w *world) pathFor(rt route) string {
	p := rt.Path
	p = strings.ReplaceAll(p, "{id}", fmt.Sprint(w.coverBook))
	p = strings.ReplaceAll(p, "{code}", w.pa.Card)
	p = strings.ReplaceAll(p, "{token}", "sometoken")
	p = strings.ReplaceAll(p, "{permission}", "catalog.browse")
	p = strings.ReplaceAll(p, "{name}", strings.TrimPrefix(w.coverURL, "/media/"))
	return p
}

// secrets are strings a signed-out visitor must never be shown.
func (w *world) secrets() []string {
	return []string{
		"alice-a", "bob-b", reviewCanary, "second opinion from bob", w.pa.Card, w.pb.Card, w.copyA, w.copyB,
		"Fixture shelf", "Ann Author", "Fixture book", "root", "Root",
	}
}

func assertNone(t *testing.T, what, body string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		// a 404 that repeats the address the caller typed has told them nothing new
		if strings.Contains(body, s) && !strings.Contains(what, s) {
			t.Errorf("%s shows %q: %.300s", what, s, body)
		}
	}
}

var allMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// ACCESS-1 "Anonymous visitor": nothing but the public pages answers a stranger,
// by any method, and nothing is changed by trying.
func TestAnonymousCrawlOfEveryRouteFindsNothingOnAFilledLibrary(t *testing.T) {
	routes := routeTable(t)
	w := newWorld(t)
	anon := newWho(t, w.s)
	secrets := w.secrets()
	before := w.snap()
	opened := 0

	for _, rt := range routes {
		methods := allMethods
		if rt.Method != "" {
			methods = append([]string{rt.Method}, allMethods...)
		}
		path := w.pathFor(rt)
		switch rt.Kind {
		case "require", "signedin":
			for _, m := range methods {
				body := []byte(nil)
				if m != "GET" && m != "HEAD" && m != "OPTIONS" {
					body = jsonBody(map[string]any{"title": "x", "role": "patron", "code": w.copyA})
				}
				r := anon.raw(rawReq{Method: m, Path: path, Body: body, ContentType: "application/json"})
				switch r.Status {
				case 401, 404, 405:
				default:
					t.Errorf("anonymous %s %s answered %d: %.200s", m, path, r.Status, r.Raw)
				}
				assertNone(t, fmt.Sprintf("anonymous %s %s", m, path), r.Raw, secrets)
			}
			opened++
		case "publiccatalog":
			// The public catalog is off by default.
			r := anon.do(rt.Method, path, nil)
			if r.Status != 404 {
				t.Errorf("anonymous %s %s with the public catalog off: %d, want 404", rt, path, r.Status)
			}
			assertNone(t, "anonymous "+path, r.Raw, secrets)
		}
	}
	if opened < 40 {
		t.Fatalf("crawled only %d guarded routes", opened)
	}

	// Images: a cover is behind sign-in, the logo is the one public image, and an
	// image that doesn't exist answers as one that does (no way to probe names).
	if r := anon.do("GET", w.coverURL, nil); r.Status != 401 {
		t.Errorf("an anonymous visitor fetching a cover: %d, want 401", r.Status)
	}
	if r := anon.do("GET", "/media/"+strings.Repeat("0", 32)+".jpg", nil); r.Status != 401 {
		t.Errorf("an anonymous visitor fetching a missing image: %d, want 401 (the same as for a real one)", r.Status)
	}
	if r := anon.do("GET", w.logoURL, nil); r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "image/") {
		t.Errorf("the logo is public by design, but answered %d %q", r.Status, r.Header.Get("Content-Type"))
	}
	for _, p := range []string{"/media-tmp/anything", "/media-tmp/", "/media/", "/media/..%2f..%2flibrary.db", "/media/../library.db",
		"/media/%2e%2e/%2e%2e/library.db", "/media//etc/passwd", "/media/" + strings.TrimPrefix(w.coverURL, "/media/") + "/../../library.db"} {
		r := anon.do("GET", p, nil)
		if r.Status/100 == 2 && !strings.Contains(r.Header.Get("Content-Type"), "html") {
			t.Errorf("GET %s answered %d %q", p, r.Status, r.Header.Get("Content-Type"))
		}
		if strings.Contains(r.Raw, "SQLite format") {
			t.Errorf("GET %s returned the database", p)
		}
	}

	// The public JSON says only what the sign-in page needs.
	checkKeys := func(path string, allowed ...string) {
		r := anon.want(200, "GET", path, nil)
		ok := map[string]bool{}
		for _, k := range allowed {
			ok[k] = true
		}
		for k := range r.JSON {
			if !ok[k] {
				t.Errorf("GET %s shows %q: %.300s", path, k, r.Raw)
			}
		}
		assertNone(t, "GET "+path, r.Raw, secrets)
	}
	checkKeys("/api/health", "status", "version")
	checkKeys("/api/client-config", "trust_port")
	checkKeys("/api/library", "name", "scheme", "accent", "colours", "logo_url", "public_catalog")
	if r := anon.do("GET", "/api/setup/status", nil); r.Status != 404 {
		t.Errorf("setup status after the first administrator: %d, want 404", r.Status)
	}

	// The web app is the same page for everyone and carries no data; nothing else is served from the machine.
	for _, p := range []string{"/", "/browse", "/desk", "/library.db", "/library.db-wal", "/../library.db", "/%2e%2e/library.db", "/.env",
		"/data/library.db", "/tls/ca.key", "/backups/", "/api", "/api/", "/api/%2e%2e/library.db"} {
		for _, m := range []string{"GET", "HEAD"} {
			r := anon.raw(rawReq{Method: m, Path: p})
			assertNone(t, m+" "+p, r.Raw, secrets)
			if strings.Contains(r.Raw, "SQLite format") || strings.Contains(r.Raw, "PRIVATE KEY") {
				t.Errorf("%s %s returned a data file", m, p)
			}
			if r.Status >= 500 {
				t.Errorf("%s %s: %d", m, p, r.Status)
			}
		}
	}

	if d := before.diff(w.snap()); len(d) > 0 {
		t.Errorf("an anonymous crawl changed the database: %v", d)
	}
}

// Before the first administrator the library is empty, but the same holds: the
// only things that answer are the setup, sign-in and branding pages.
func TestAnonymousCrawlOfAFreshInstallBeforeSetup(t *testing.T) {
	routes := routeTable(t)
	s := testutil.StartServer(t, testutil.Options{})
	anon := newWho(t, s)
	before := takeSnapshot(t, s.DataDir)
	for _, rt := range routes {
		if rt.Kind != "require" && rt.Kind != "signedin" {
			continue
		}
		path := strings.NewReplacer("{id}", "1", "{code}", "1", "{token}", "x", "{permission}", "catalog.browse", "{name}", "x.jpg").Replace(rt.Path)
		r := anon.raw(rawReq{Method: rt.Method, Path: path, Body: jsonBody(map[string]any{"title": "x"})})
		if r.Status != 401 {
			t.Errorf("%s on a fresh install: %d, want 401: %.200s", rt, r.Status, r.Raw)
		}
	}
	if r := anon.do("GET", "/api/public/books", nil); r.Status != 404 {
		t.Errorf("the public catalog on a fresh install: %d, want 404", r.Status)
	}
	if d := before.diff(takeSnapshot(t, s.DataDir)); len(d) > 0 {
		t.Errorf("the crawl changed the database: %v", d)
	}
}

// ACCESS-4 and CAT-6: with the public catalog on, a visitor sees books, covers
// and counts, and not one patron, loan, review or name; it is off by default and
// off means off at once; and every other route stays closed.
func TestPublicCatalogShowsVisitorsNothingAboutPatronsLoansOrReviews(t *testing.T) {
	routes := routeTable(t)
	w := newWorld(t)
	anon := newWho(t, w.s)
	secrets := w.secrets()
	// Titles of the books are the catalog itself, so they are not secrets here.
	secrets = secrets[:0:0]
	secrets = append(secrets, "alice-a", "bob-b", reviewCanary, "second opinion from bob", w.pa.Card, w.pb.Card, w.copyA, w.copyB,
		"Fixture shelf", "root", "Root", "due", "loan", "patron_", "display_name", "reviews", "barcode", "location")

	// Off by default: the visitor gets nothing, and a cover is behind sign-in.
	if lib := anon.want(200, "GET", "/api/library", nil); lib.JSON["public_catalog"] != false {
		t.Fatalf("the public catalog is on by default: %s", lib.Raw)
	}
	if r := anon.do("GET", "/api/public/books?q=", nil); r.Status != 404 {
		t.Fatalf("public search while off: %d, want 404", r.Status)
	}
	if r := anon.do("GET", w.coverURL, nil); r.Status != 401 {
		t.Fatalf("a cover while the public catalog is off: %d, want 401", r.Status)
	}

	w.root.want(200, "PUT", "/api/admin/settings", map[string]any{"public_catalog": true})

	allowed := map[string]bool{"id": true, "title": true, "authors": true, "isbn": true, "description": true, "cover_url": true,
		"available_copies": true, "total_copies": true, "rating_average": true, "rating_count": true}
	res := anon.want(200, "GET", "/api/public/books?q=", nil)
	books, _ := res.JSON["books"].([]any)
	if len(books) < 3 {
		t.Fatalf("the visitor sees %d books, want the catalog: %s", len(books), res.Raw)
	}
	check := func(what string, r reply) {
		t.Helper()
		assertNone(t, what, r.Raw, secrets)
	}
	check("public search", res)
	for _, b := range books {
		bm := b.(map[string]any)
		for k := range bm {
			if !allowed[k] {
				t.Errorf("a public book shows %q", k)
			}
		}
		d := anon.want(200, "GET", fmt.Sprintf("/api/public/books/%d", int64(bm["id"].(float64))), nil)
		for k := range d.JSON {
			if !allowed[k] {
				t.Errorf("a public book page shows %q: %.200s", k, d.Raw)
			}
		}
		check("public book page", d)
	}
	// The book with the review has a rating and nothing else.
	d := anon.want(200, "GET", fmt.Sprintf("/api/public/books/%d", w.bookID), nil)
	if d.JSON["rating_count"] != float64(2) {
		t.Errorf("the public page should show the count of reviews (2): %s", d.Raw)
	}
	for _, q := range []string{"alice", "bob-b", reviewCanary, "second opinion", "ZEBRA", w.pa.Card, w.copyA, "root"} {
		r := anon.want(200, "GET", "/api/public/books?q="+url.QueryEscape(q), nil)
		if n, _ := r.JSON["total"].(float64); n != 0 {
			t.Errorf("searching for %q finds %v books: a visitor can look inside reviews, loans or patrons: %.200s", q, n, r.Raw)
		}
	}
	if r := anon.want(200, "GET", w.coverURL, nil); !strings.HasPrefix(r.Header.Get("Content-Type"), "image/") {
		t.Errorf("a cover for a visitor: %q", r.Header.Get("Content-Type"))
	}

	// Everything else stays closed to a visitor, whatever the setting.
	for _, rt := range routes {
		if rt.Kind != "require" && rt.Kind != "signedin" {
			continue
		}
		for _, m := range allMethods {
			r := anon.raw(rawReq{Method: m, Path: w.pathFor(rt)})
			if r.Status != 401 && r.Status != 404 && r.Status != 405 {
				t.Errorf("a visitor with the public catalog on: %s %s answered %d: %.200s", m, rt.Path, r.Status, r.Raw)
			}
			assertNone(t, "visitor "+m+" "+w.pathFor(rt), r.Raw, []string{"alice-a", "bob-b", reviewCanary, w.pa.Card})
		}
	}
	if r := anon.do("GET", "/media/"+strings.Repeat("0", 32)+".jpg", nil); r.Status != 404 {
		t.Logf("a missing image for a visitor with the public catalog on: %d", r.Status)
	}

	// Off means off, at once.
	w.root.want(200, "PUT", "/api/admin/settings", map[string]any{"public_catalog": false})
	if r := anon.do("GET", "/api/public/books?q=", nil); r.Status != 404 {
		t.Errorf("public search right after switching off: %d, want 404", r.Status)
	}
	if r := anon.do("GET", w.coverURL, nil); r.Status != 401 {
		t.Errorf("a cover right after switching off: %d, want 401", r.Status)
	}
}

// IDOR (CIRC-1/PATRON-1/CARD-1): a patron who changes an id, a query value or a
// header reaches nothing of another patron's.
func TestPatronsCannotReachEachOthersLoansCardOrHistory(t *testing.T) {
	w := newWorld(t)
	for _, pair := range [][2]*member{{w.pa, w.pb}, {w.pb, w.pa}} {
		me, other := pair[0], pair[1]
		otherLoan, otherCopy := w.loanA, w.copyA
		myCopy := w.copyB
		if other == w.pb {
			otherLoan, otherCopy = w.loanB, w.copyB
			myCopy = w.copyA
		}
		t.Run(me.Name+" reaching for "+other.Name, func(t *testing.T) {
			before := w.snap()
			myCard := me.W.want(200, "GET", "/api/me/card", nil).Raw
			otherCard := other.W.want(200, "GET", "/api/me/card", nil).Raw
			if myCard == otherCard {
				t.Fatal("two patrons share a card")
			}

			// Renew another's loan, by its id and by ids around it.
			miss := me.W.do("POST", "/api/me/loans/987654/renew", nil)
			for _, id := range []string{fmt.Sprint(otherLoan), fmt.Sprintf("0%d", otherLoan), fmt.Sprintf("%d%%20", otherLoan), fmt.Sprintf("%d.0", otherLoan),
				fmt.Sprintf("%d;1", otherLoan), "-1", "0", "1%20OR%201=1", "9223372036854775808", "abc"} {
				r := me.W.do("POST", "/api/me/loans/"+id+"/renew", nil)
				if r.Status/100 == 2 || r.Status >= 500 {
					t.Errorf("renewing loan %q as %s: %d %.200s", id, me.Name, r.Status, r.Raw)
				}
			}
			r := me.W.do("POST", fmt.Sprintf("/api/me/loans/%d/renew", otherLoan), nil)
			if r.Status != miss.Status || r.errMessage() != miss.errMessage() {
				t.Errorf("another patron's loan answers %d %q, a loan that doesn't exist %d %q: that tells a patron which loans exist", r.Status, r.errMessage(), miss.Status, miss.errMessage())
			}
			if d := before.diff(w.snap()); len(d) > 0 {
				t.Errorf("a refused renewal changed %v", d)
			}

			// Lists, with the tampering a person would try.
			for _, base := range []string{"/api/me/loans", "/api/me/loans/history"} {
				for _, q := range []string{"", "?user_id=%d", "?patron=%d", "?patron_id=%d", "?user=%d", "?id=%d", "?card=%s", "?per_page=100000&page=1", "?q=" + other.Name} {
					if strings.Contains(q, "%d") {
						q = fmt.Sprintf(q, other.ID)
					} else if strings.Contains(q, "%s") {
						q = fmt.Sprintf(q, other.Card)
					}
					r := me.W.raw(rawReq{Method: "GET", Path: base + q, Header: map[string]string{
						"X-User-Id": fmt.Sprint(other.ID), "X-Forwarded-User": other.Name, "X-Patron": other.Card}})
					if r.Status != 200 && r.Status != 422 && r.Status != 400 {
						t.Errorf("GET %s%s: %d %.200s", base, q, r.Status, r.Raw)
					}
					assertNone(t, "GET "+base+q+" as "+me.Name, r.Raw, []string{otherCopy, other.Card, other.Name})
					if base == "/api/me/loans" && r.Status == 200 && !strings.Contains(r.Raw, myCopy) {
						t.Errorf("GET %s%s no longer shows the patron's own loan: %.200s", base, q, r.Raw)
					}
				}
			}

			// The card, and its pictures.
			for _, p := range []string{"/api/me/card", "/api/me/card/barcode.svg", "/api/me/card/qr.svg"} {
				plain := me.W.do("GET", p, nil)
				for _, q := range []string{fmt.Sprintf("?user_id=%d", other.ID), "?number=" + other.Card, "?card=" + other.Card} {
					r := me.W.raw(rawReq{Method: "GET", Path: p + q, Header: map[string]string{"X-User-Id": fmt.Sprint(other.ID)}})
					if r.Raw != plain.Raw {
						t.Errorf("GET %s%s gives %s something other than their own card", p, q, me.Name)
					}
				}
				if p == "/api/me/card" {
					assertNone(t, "the card as "+me.Name, plain.Raw, []string{other.Card})
				}
			}

			// Staff reads and the card re-issue.
			for _, c := range []call{
				{method: "GET", path: fmt.Sprintf("/api/patrons/%d", other.ID)},
				{method: "GET", path: fmt.Sprintf("/api/patron-loans/%d", other.ID)},
				{method: "GET", path: "/api/patrons/by-card/" + other.Card},
				{method: "GET", path: "/api/patrons?q=" + other.Name},
				{method: "POST", path: fmt.Sprintf("/api/users/%d/card/reissue", other.ID)},
				{method: "POST", path: fmt.Sprintf("/api/desk/loans/%d/renew", otherLoan)},
				{method: "POST", path: "/api/desk/checkin", body: jsonBody(map[string]string{"code": otherCopy})},
			} {
				if r := c.send(me.W); r.Status != 403 {
					t.Errorf("%s %s as a patron: %d, want 403: %.200s", c.method, c.path, r.Status, r.Raw)
				}
			}
			if now := other.W.want(200, "GET", "/api/me/card", nil).Raw; now != otherCard {
				t.Errorf("%s's card changed", other.Name)
			}

			// Self-checkout always goes to the caller, whoever the body names.
			_, code := w.newCopy(w.newBook())
			otherBefore := other.W.want(200, "GET", "/api/me/loans", nil).Raw
			r = me.W.do("POST", "/api/me/checkout", map[string]any{"code": code, "patron": other.Card, "card": other.Card, "user_id": other.ID, "patron_id": other.ID})
			if r.Status >= 500 {
				t.Errorf("self-checkout with extra fields: %d", r.Status)
			}
			if after := other.W.want(200, "GET", "/api/me/loans", nil).Raw; after != otherBefore {
				t.Errorf("%s's loans changed when %s checked a book out", other.Name, me.Name)
			}
			if r.Status == 200 {
				mine := me.W.want(200, "GET", "/api/me/loans", nil)
				if !strings.Contains(mine.Raw, code) {
					t.Errorf("the self-checkout went to nobody: %s", mine.Raw)
				}
			}
		})
	}

	// Reviews: a patron deletes their own and nobody else's.
	rv := w.pb.W.do("DELETE", fmt.Sprintf("/api/reviews/%d", w.reviewA), nil)
	if rv.Status != 403 {
		t.Errorf("a patron removing another's review by id: %d, want 403", rv.Status)
	}
	w.pb.W.want(204, "DELETE", fmt.Sprintf("/api/books/%d/review", w.bookID), nil)
	list := w.pa.W.want(200, "GET", fmt.Sprintf("/api/books/%d/reviews", w.bookID), nil)
	if !strings.Contains(list.Raw, reviewCanary) || strings.Contains(list.Raw, "second opinion from bob") {
		t.Errorf("deleting one's own review should remove that one only: %s", list.Raw)
	}

	// Positive control: the tests above would pass for a server that refuses everything.
	w.pa.W.want(200, "POST", fmt.Sprintf("/api/me/loans/%d/renew", w.loanA), nil)
}

// SEC-T1 (browser protections): a cross-site request, a plain form post and any
// body that isn't JSON are refused on every state-changing route, and change nothing.
func TestEveryStateChangingRouteRefusesCrossSiteAndFormPosts(t *testing.T) {
	routes := routeTable(t)
	w := newWorld(t)
	n := 0
	for _, rt := range routes {
		if rt.Method == "" || rt.Method == "GET" || rt.Method == "HEAD" || rt.Method == "OPTIONS" {
			continue
		}
		if rt.Kind != "require" && rt.Kind != "signedin" {
			continue
		}
		rt := rt
		t.Run(rt.String(), func(t *testing.T) {
			var c call
			switch rt.String() {
			case "POST /api/auth/signout":
				c = call{method: "POST", path: rt.Path}
			case "POST /api/me/password":
				c = call{method: "POST", path: rt.Path, body: jsonBody(map[string]string{"current_password": adminPassword, "new_password": adminPassword + " 2"})}
			default:
				c = w.target(t, rt, nil)
			}
			if c.body == nil {
				c.body = []byte("{}")
			}
			root := w.root
			before := w.snap()
			attempt := func(what string, hdr map[string]string, ctype string, body []byte, want int) {
				t.Helper()
				r := root.raw(rawReq{Method: c.method, Path: c.path, Header: hdr, Body: body, ContentType: ctype})
				if r.Status != want {
					t.Errorf("%s: %d, want %d: %.200s", what, r.Status, want, r.Raw)
				}
			}
			jsonType := "application/json"
			if c.ctype != "" {
				jsonType = c.ctype
			}
			attempt("from another site", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, jsonType, c.body, 403)
			attempt("from a sibling site", map[string]string{"Origin": "https://other." + "example", "Sec-Fetch-Site": "same-site"}, jsonType, c.body, 403)
			attempt("with only an Origin of another site", map[string]string{"Origin": "https://evil.example"}, jsonType, c.body, 403)
			if rt.Multipart {
				attempt("as a plain-text post", nil, "text/plain", c.body, 415)
				attempt("as a url-encoded form", nil, "application/x-www-form-urlencoded", []byte("title=x&name=x"), 415)
			} else {
				attempt("as a plain-text post", nil, "text/plain", c.body, 415)
				attempt("as a url-encoded form", nil, "application/x-www-form-urlencoded", []byte("role=administrator&on=true&name=x"), 415)
				attempt("as a multipart form", nil, "multipart/form-data; boundary=x", []byte("--x\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\nb\r\n--x--\r\n"), 415)
			}
			if d := before.diff(w.snap()); len(d) > 0 {
				t.Errorf("a refused request changed the database: %v", d)
			}
			if rt.Path != "/api/auth/signout" && rt.Path != "/api/me/password" {
				if me := root.do("GET", "/api/me", nil); me.Status != 200 {
					t.Errorf("the refused requests ended the session: %d", me.Status)
				}
			}
			n++
		})
	}
	if n < 35 {
		t.Errorf("only %d state-changing routes were tried", n)
	}

	// Controls: the same requests from the library's own page go through.
	w.root.want(200, "PUT", "/api/admin/settings", map[string]any{"max_loans": 9})
	r := w.root.raw(rawReq{Method: "PUT", Path: "/api/admin/settings", Header: map[string]string{"Origin": w.s.URL, "Sec-Fetch-Site": "same-origin"},
		Body: jsonBody(map[string]any{"max_loans": 8}), ContentType: "application/json"})
	if r.Status != 200 {
		t.Errorf("a same-origin request was refused: %d %s", r.Status, r.Raw)
	}
	r = w.root.raw(rawReq{Method: "PUT", Path: "/api/admin/settings", Header: map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"},
		Body: jsonBody(map[string]any{"max_loans": 3}), ContentType: "application/json"})
	if r.Status != 403 || w.root.want(200, "GET", "/api/admin/settings", nil).JSON["max_loans"] != float64(8) {
		t.Errorf("cross-site settings change: %d, and the value is no longer 8", r.Status)
	}

	// Sign-in, registration and setup are forgeable too: a page on another site must not sign a visitor in as someone else, or make accounts.
	anon := newWho(t, w.s)
	r = anon.raw(rawReq{Method: "POST", Path: "/api/auth/signin", Header: map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"},
		Body: jsonBody(map[string]string{"username": "root", "password": adminPassword}), ContentType: "application/json"})
	if r.Status != 403 || r.Header.Get("Set-Cookie") != "" {
		t.Errorf("a cross-site sign-in: %d, Set-Cookie %q", r.Status, r.Header.Get("Set-Cookie"))
	}
	_, code := w.root.invite("patron")
	r = anon.raw(rawReq{Method: "POST", Path: "/api/register", Header: map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"},
		Body: jsonBody(map[string]string{"code": code, "username": "csrf-made", "display_name": "x", "password": adminPassword}), ContentType: "application/json"})
	if r.Status != 403 {
		t.Errorf("a cross-site registration: %d", r.Status)
	}
	if r := newWho(t, w.s).signIn("csrf-made", adminPassword); r.Status == 200 {
		t.Errorf("the cross-site registration made an account")
	}
	_ = time.Second
}

// ROLE-3 "Removed permission": switching a permission off, or lowering a role,
// applies to a signed-in user's very next request.
func TestAPermissionSwitchedOffMidSessionIsRefusedOnTheNextRequest(t *testing.T) {
	w := newWorld(t)
	// For each permission, a probe request a holder passes.
	probe := map[perm.Permission]call{
		perm.CatalogBrowse:     {method: "GET", path: "/api/books?q="},
		perm.LoansOwnView:      {method: "GET", path: "/api/me/loans"},
		perm.PatronsLookup:     {method: "GET", path: "/api/patrons?q=a"},
		perm.CircDesk:          {method: "GET", path: "/api/desk/overdue"},
		perm.ReportsView:       {method: "GET", path: "/api/admin/reports/statistics"},
		perm.ActivityView:      {method: "GET", path: "/api/admin/activity"},
		perm.SettingsManage:    {method: "GET", path: "/api/admin/settings"},
		perm.InvitationsManage: {method: "GET", path: "/api/admin/invitations"},
		perm.UsersManage:       {method: "GET", path: "/api/admin/users"},
		perm.CatalogManage:     {method: "POST", path: "/api/catalog/lookup", body: jsonBody(map[string]string{"isbn": "9780261103573"})},
	}
	acc := w.account("administrator", "midsession")
	for p, c := range probe {
		t.Run(string(p), func(t *testing.T) {
			if r := c.send(acc.W); refusedByGuard(r) || r.Status >= 500 {
				t.Fatalf("a holder of %s is refused before anything is switched off: %d %.200s", p, r.Status, r.Raw)
			}
			w.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/%s", acc.ID, p), map[string]bool{"on": false})
			if r := c.send(acc.W); r.Status != 403 && r.Status != 401 {
				t.Errorf("the very next request after switching %s off: %d, want 403: %.200s", p, r.Status, r.Raw)
			}
			w.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/%s", acc.ID, p), map[string]bool{"on": true})
			if r := c.send(acc.W); r.Status == 401 {
				// cutting a permission may end the session (security design §3): a new sign-in must then work
				acc.W = w.signedIn(acc.Name)
			}
			if r := c.send(acc.W); refusedByGuard(r) {
				t.Errorf("after switching %s back on: %d %.200s", p, r.Status, r.Raw)
			}
		})
	}

	// A role lowered mid-session: the librarian is a patron on the next request.
	lib := w.account("librarian", "lowered")
	lib.W.want(200, "GET", "/api/patrons?q=a", nil)
	w.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/role", lib.ID), map[string]string{"role": "patron"})
	if r := lib.W.do("GET", "/api/patrons?q=a", nil); r.Status != 403 && r.Status != 401 {
		t.Errorf("a librarian made a patron can still look up patrons: %d", r.Status)
	}
	if r := lib.W.do("POST", "/api/books", map[string]any{"title": "late"}); r.Status != 403 && r.Status != 401 {
		t.Errorf("a librarian made a patron can still add a book: %d", r.Status)
	}
}

// ROLE-3: the self-checkout permission is not enough while the library's option is off.
func TestSelfCheckoutNeedsTheOptionAsWellAsThePermission(t *testing.T) {
	w := newWorld(t)
	w.root.want(200, "PUT", "/api/admin/settings", map[string]any{"self_checkout": false})
	_, code := w.newCopy(w.newBook())
	before := w.snap()
	r := w.pa.W.do("POST", "/api/me/checkout", map[string]string{"code": code})
	if r.Status/100 == 2 || r.Status >= 500 {
		t.Errorf("self-checkout with the option off: %d %s", r.Status, r.Raw)
	}
	if d := before.diff(w.snap()); len(d) > 0 {
		t.Errorf("the refused self-checkout changed %v", d)
	}
}

// The sign-in return address: the plain-HTTP listener that sends people to
// HTTPS follows the address the visitor typed, never a value from the path or
// the query, so a crafted link can't send anyone to another site. (A request
// line in absolute form, "GET https://other/ HTTP/1.1", names its own host; no
// browser sends one to an origin server, so a link can't cause it.)
func TestPlainHTTPRedirectNeverFollowsAValueFromThePathOrQuery(t *testing.T) {
	s := startHTTPS(t, t.TempDir(), testutil.DefaultNow, nil)
	addr := strings.TrimPrefix(s.TrustURL, "http://")
	host, _, _ := net.SplitHostPort(addr)
	for _, target := range []string{"/", "//evil.example/", "/\\evil.example", "///evil.example", "/?next=https://evil.example", "/?redirect=//evil.example",
		"/%2f%2fevil.example", "/signin?from=//evil.example", "//evil.example@" + host + "/", "/../../evil.example"} {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target, addr)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			conn.Close()
			continue // a request line the server will not parse at all is a refusal too
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		conn.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode/100 == 3 {
			u, err := url.Parse(loc)
			if err != nil || u.Scheme != "https" || u.Hostname() != host {
				t.Errorf("GET %s was sent to %q, not to https on %s", target, loc, host)
			}
		}
		if strings.Contains(loc, "evil.example") && !strings.HasPrefix(loc, "https://"+host) {
			t.Errorf("GET %s leaks to %q", target, loc)
		}
	}
}

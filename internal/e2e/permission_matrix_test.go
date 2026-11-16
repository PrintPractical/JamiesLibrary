package e2e

// ROLE-1/2/3 over the whole API: every route is read from the source, and for
// each one a user who holds only one permission is sent a request that would
// really change something. Only the holder of the permission the route declares
// gets past the guard; everyone else is refused with 403 (an anonymous caller
// with 401), and the database is the same afterwards.

import (
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"strings"
	"testing"

	"jamieslibrary/internal/perm"
)

const guardMessage = "You don't have permission to do that."

// call is one request the matrix sends.
type call struct {
	method, path string
	body         []byte
	ctype        string
	changes      bool // a request that passes the guard changes the database
	noHolder     bool // ...but not for the holder of this one permission alone: it may not hand out what it lacks
}

func jsonBody(v any) []byte {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func (c call) send(w *who) reply {
	r := rawReq{Method: c.method, Path: c.path, Body: c.body, ContentType: c.ctype}
	if c.body != nil && c.ctype == "" {
		r.ContentType = "application/json"
	}
	return w.raw(r)
}

// refusedByGuard is a refusal by the route's own declaration, not by a handler.
func refusedByGuard(r reply) bool {
	return r.Status == 401 || (r.Status == 403 && r.errMessage() == guardMessage)
}

// reviewOn has m review book and returns the review's id.
func (w *world) reviewOn(m *member, book int64, text string) int64 {
	w.t.Helper()
	m.W.want(200, "PUT", fmt.Sprintf("/api/books/%d/review", book), map[string]any{"rating": 4, "text": text})
	l := w.root.want(200, "GET", fmt.Sprintf("/api/books/%d/reviews", book), nil)
	rs, _ := l.JSON["reviews"].([]any)
	for _, x := range rs {
		rv := x.(map[string]any)
		if rv["text"] == text {
			return int64(rv["id"].(float64))
		}
	}
	w.t.Fatalf("review %q not found: %s", text, l.Raw)
	return 0
}

// bare registers a patron and takes every permission off them: an account
// anyone may administer, whatever they hold.
func (w *world) bare(name string) *member {
	w.t.Helper()
	m := w.patron(name)
	for _, p := range perm.Preset(perm.Patron).List() {
		w.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/%s", m.ID, p), map[string]bool{"on": false})
	}
	return m
}

// target builds the request for route rt. For holder nil it is aimed at things
// that exist and would change if the guard let a stranger through; for a
// holder it is aimed at things that holder may act on.
func (w *world) target(t *testing.T, rt route, holder *member) call {
	t.Helper()
	n := w.next()
	id := func(v int64) string { return fmt.Sprint(v) }
	lib := w.root.want(200, "GET", "/api/library", nil)
	scheme, _ := lib.JSON["scheme"].(string)
	accent, _ := lib.JSON["accent"].(string)
	adminSet := w.root.want(200, "GET", "/api/admin/settings", nil)
	path := rt.Path
	replace := func(k, v string) { path = strings.ReplaceAll(path, "{"+k+"}", v) }
	c := call{method: rt.Method}
	switch rt.String() {
	case "DELETE /api/admin/branding/logo":
		c.changes = true
	case "DELETE /api/books/{id}":
		replace("id", id(w.newBook()))
		c.changes = true
	case "DELETE /api/books/{id}/review":
		if holder == nil {
			replace("id", id(w.bookID))
		} else {
			b := w.newBook()
			w.reviewOn(holder, b, fmt.Sprintf("holder review %d", n))
			replace("id", id(b))
		}
		c.changes = true
	case "DELETE /api/copies/{id}":
		cid, _ := w.newCopy(w.newBook())
		replace("id", id(cid))
		c.changes = true
	case "DELETE /api/locations/{id}":
		l := w.root.want(201, "POST", "/api/locations", map[string]string{"name": fmt.Sprintf("Spare %d", n)})
		replace("id", id(idOf(l, "id")))
		c.changes = true
	case "DELETE /api/reviews/{id}":
		b := w.newBook()
		replace("id", id(w.reviewOn(w.pb, b, fmt.Sprintf("a review to remove %d", n))))
		c.changes = true
	case "GET /api/books/{id}", "GET /api/books/{id}/reviews":
		replace("id", id(w.coverBook))
	case "GET /api/patron-loans/{id}", "GET /api/patrons/{id}":
		replace("id", id(w.pa.ID))
	case "GET /api/patrons/by-card/{code}":
		replace("code", w.pa.Card)
	case "GET /media-tmp/{token}":
		replace("token", "no-such-token")
	case "POST /api/admin/branding/check":
		c.body = jsonBody(map[string]string{"scheme": scheme, "accent": accent})
	case "POST /api/admin/invitations":
		c.body = jsonBody(map[string]string{"role": "patron"})
		c.changes, c.noHolder = true, true // a role's permissions must all be the inviter's own
	case "POST /api/admin/invitations/{id}/revoke":
		inv, _ := w.root.invite("patron")
		replace("id", id(inv))
		c.changes, c.noHolder = true, true
	case "POST /api/admin/users/{id}/deactivate":
		replace("id", id(w.bare(fmt.Sprintf("target%d", n)).ID))
		c.changes = true
	case "POST /api/admin/users/{id}/reactivate":
		u := w.bare(fmt.Sprintf("target%d", n))
		w.root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", u.ID), nil)
		replace("id", id(u.ID))
		c.changes = true
	case "POST /api/admin/users/{id}/reset-password":
		replace("id", id(w.bare(fmt.Sprintf("target%d", n)).ID))
		c.changes = true
	case "POST /api/books":
		c.body = jsonBody(map[string]any{"title": fmt.Sprintf("Planted %d", n), "authors": []string{"Mallory"}})
		c.changes = true
	case "POST /api/books/{id}/copies":
		replace("id", id(w.newBook()))
		c.body = jsonBody(map[string]any{"location_id": w.locID, "condition": "good"})
		c.changes = true
	case "POST /api/catalog/labels":
		cid, _ := w.newCopy(w.newBook())
		c.body = jsonBody(map[string]any{"copy_ids": []int64{cid}, "layout": "single"})
	case "POST /api/catalog/lookup":
		c.body = jsonBody(map[string]string{"isbn": "9780261103573"})
	case "POST /api/desk/checkin":
		code, _ := w.lend(w.pb)
		c.body = jsonBody(map[string]string{"code": code})
		c.changes = true
	case "POST /api/desk/checkout":
		_, code := w.newCopy(w.newBook())
		c.body = jsonBody(map[string]string{"patron": w.pb.Card, "code": code})
		c.changes = true
	case "POST /api/desk/loans/{id}/renew":
		_, loan := w.lend(w.pa)
		replace("id", id(loan))
		c.changes = true
	case "POST /api/desk/resolve":
		c.body = jsonBody(map[string]string{"code": w.pa.Card, "step": "patron"})
	case "POST /api/locations":
		c.body = jsonBody(map[string]string{"name": fmt.Sprintf("Planted shelf %d", n)})
		c.changes = true
	case "POST /api/me/checkout":
		_, code := w.newCopy(w.newBook())
		c.body = jsonBody(map[string]string{"code": code})
		c.changes = true
	case "POST /api/me/loans/{id}/renew":
		owner := w.pa
		if holder != nil {
			owner = holder
		}
		_, loan := w.lend(owner)
		replace("id", id(loan))
		c.changes = true
	case "POST /api/users/{id}/card/reissue":
		replace("id", id(w.bare(fmt.Sprintf("target%d", n)).ID))
		c.changes = true
	case "PUT /api/admin/branding":
		c.body = jsonBody(map[string]string{"name": fmt.Sprintf("Hijacked %d", n), "scheme": scheme, "accent": accent})
		c.changes = true
	case "PUT /api/admin/branding/logo":
		c.body, c.ctype = formFile("logo", "logo.png", pngBytes(colorOf(n)))
		c.changes = true
	case "PUT /api/admin/copy-id-mode":
		mode := "own"
		if adminSet.JSON["copy_id_mode"] == "own" {
			mode = "isbn"
		}
		c.body = jsonBody(map[string]string{"mode": mode})
		c.changes = true
	case "PUT /api/admin/settings":
		c.body = jsonBody(map[string]any{"max_loans": int(adminSet.JSON["max_loans"].(float64)) + 1})
		c.changes = true
	case "PUT /api/admin/users/{id}/permissions/{permission}":
		replace("id", id(w.bare(fmt.Sprintf("target%d", n)).ID))
		// a permission the caller holds may be given; one they lack may not
		given := perm.CatalogBrowse
		if holder != nil {
			given = perm.UsersPermissions
		}
		replace("permission", string(given))
		c.body = jsonBody(map[string]bool{"on": true})
		c.changes = true
	case "PUT /api/admin/users/{id}/role":
		replace("id", id(w.bare(fmt.Sprintf("target%d", n)).ID))
		c.body = jsonBody(map[string]string{"role": "librarian"})
		c.changes, c.noHolder = true, true
	case "PUT /api/books/{id}":
		replace("id", id(w.newBook()))
		c.body = jsonBody(map[string]any{"title": fmt.Sprintf("Rewritten %d", n), "authors": []string{"Mallory"}})
		c.changes = true
	case "PUT /api/books/{id}/review":
		if holder == nil {
			replace("id", id(w.bookID))
		} else {
			replace("id", id(w.newBook()))
		}
		c.body = jsonBody(map[string]any{"rating": 1, "text": fmt.Sprintf("vandalism %d", n)})
		c.changes = true
	case "PUT /api/copies/{id}":
		cid, _ := w.newCopy(w.newBook())
		replace("id", id(cid))
		c.body = jsonBody(map[string]any{"location_id": w.locID, "condition": "worn"})
		c.changes = true
	case "PUT /api/locations/{id}":
		l := w.root.want(201, "POST", "/api/locations", map[string]string{"name": fmt.Sprintf("Old name %d", n)})
		replace("id", id(idOf(l, "id")))
		c.body = jsonBody(map[string]string{"name": fmt.Sprintf("Renamed %d", n)})
		c.changes = true
	default:
		// a read with no path value
		if strings.Contains(path, "{") || rt.Method != "GET" {
			t.Fatalf("the matrix has no request for %s: add one to world.target", rt)
		}
	}
	c.path = path
	return c
}

func colorOf(n int) color.Color { return color.RGBA{uint8(n * 37), uint8(n * 91), uint8(n * 53), 255} }

// accounts are the users the matrix sends requests as: for each permission one
// account that holds only it, and one that holds every permission but it.
type accounts struct {
	only, allBut map[perm.Permission]*member
}

func (w *world) accounts(t *testing.T) accounts {
	t.Helper()
	out := accounts{only: map[perm.Permission]*member{}, allBut: map[perm.Permission]*member{}}
	for i, p := range perm.All {
		for _, kind := range []string{"only", "allbut"} {
			var m *member
			if kind == "only" {
				m = w.account("patron", fmt.Sprintf("only%02d", i+1))
			} else {
				m = w.account("administrator", fmt.Sprintf("allbut%02d", i+1))
			}
			for _, q := range perm.All {
				want := (q == p) == (kind == "only")
				w.root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/%s", m.ID, q), map[string]bool{"on": want})
			}
			m.W = w.signedIn(m.Name)
			got := perms(m.W.want(200, "GET", "/api/me", nil))
			if kind == "only" && (len(got) != 1 || !got[string(p)]) {
				t.Fatalf("%s should hold only %s, holds %v", m.Name, p, got)
			}
			if kind == "allbut" && (len(got) != len(perm.All)-1 || got[string(p)]) {
				t.Fatalf("%s should hold every permission but %s, holds %v", m.Name, p, got)
			}
			if kind == "only" {
				out.only[p] = m
			} else {
				out.allBut[p] = m
			}
		}
	}
	return out
}

// ROLE-3 "Permission enforced on the server": every route x every permission.
func TestEveryRouteRefusesEveryoneButTheHolderOfItsPermission(t *testing.T) {
	routes := routeTable(t)
	w := newWorld(t)
	acc := w.accounts(t)
	singles := acc.only
	anon := newWho(t, w.s)

	used := map[string]bool{}
	checked := 0
	for _, rt := range routes {
		if rt.Kind != "require" {
			continue
		}
		used[rt.Perm] = true
		rt := rt
		t.Run(rt.String(), func(t *testing.T) {
			// Refusals: aimed at things that exist, with a body that works for the holder.
			c := w.target(t, rt, nil)
			before := w.snap()
			if r := c.send(anon); r.Status != 401 {
				t.Errorf("anonymous: %d, want 401: %.200s", r.Status, r.Raw)
			}
			for _, p := range perm.All {
				if string(p) == rt.Perm {
					continue
				}
				m := singles[p]
				r := c.send(m.W)
				if r.Status != 403 || r.errMessage() != guardMessage {
					t.Errorf("a user holding only %s was answered %d, want 403: %.200s", p, r.Status, r.Raw)
				}
			}
			// ...and so is the user who holds everything else.
			if r := c.send(acc.allBut[perm.Permission(rt.Perm)].W); r.Status != 403 || r.errMessage() != guardMessage {
				t.Errorf("a user holding everything but %s was answered %d, want 403: %.200s", rt.Perm, r.Status, r.Raw)
			}
			if d := before.diff(w.snap()); len(d) > 0 {
				t.Errorf("refused requests changed the database: tables %v", d)
			}

			// The holder gets past the guard and, for a request that changes things, changes them.
			holder := singles[perm.Permission(rt.Perm)]
			hc := w.target(t, rt, holder)
			before = w.snap()
			r := hc.send(holder.W)
			if refusedByGuard(r) {
				t.Fatalf("the holder of %s was refused: %d %.200s", rt.Perm, r.Status, r.Raw)
			}
			if hc.changes && !hc.noHolder {
				if r.Status >= 300 {
					t.Errorf("the holder's request was not carried out: %d %.300s", r.Status, r.Raw)
				}
				if len(before.diff(w.snap())) == 0 {
					t.Errorf("the holder's request changed nothing, so the refusals above proved nothing: %d %.200s", r.Status, r.Raw)
				}
			}
			if r.Status >= 500 {
				t.Errorf("server error: %d %.200s", r.Status, r.Raw)
			}
			checked++
		})
	}
	for _, p := range perm.All {
		if !used[string(p)] {
			t.Errorf("no route requires %s, so no route can be refused for lacking it", p)
		}
	}
	if checked < 40 {
		t.Errorf("only %d permission routes were walked", checked)
	}
}

// Routes that need only a signed-in caller: everyone passes, a stranger doesn't.
func TestSignedInRoutesAdmitEveryAccountAndNoStranger(t *testing.T) {
	routes := routeTable(t)
	w := newWorld(t)
	acc := w.accounts(t)
	singles := acc.only
	anon := newWho(t, w.s)
	n := 0
	for _, rt := range routes {
		if rt.Kind != "signedin" {
			continue
		}
		n++
		path := rt.Path
		var body []byte
		if rt.Method == "POST" && rt.Path == "/api/me/password" {
			body = jsonBody(map[string]string{"current_password": adminPassword, "new_password": adminPassword + " 2"})
		}
		c := call{method: rt.Method, path: path, body: body}
		if r := c.send(anon); r.Status != 401 {
			t.Errorf("%s: anonymous got %d, want 401", rt, r.Status)
		}
		for p, m := range singles {
			who := m.W
			if rt.Method != "GET" {
				who = w.signedIn(m.Name) // these end or change the session
			}
			r := c.send(who)
			if refusedByGuard(r) {
				t.Errorf("%s: a user holding only %s was refused: %d %.200s", rt, p, r.Status, r.Raw)
			}
			if rt.Method == "GET" && r.Status != 200 {
				t.Errorf("%s: %d for a user holding only %s", rt, r.Status, p)
			}
			if rt.Method == http.MethodPost && rt.Path == "/api/auth/signout" {
				if again := who.do("GET", "/api/me", nil); again.Status != 401 {
					t.Errorf("sign-out left the session alive: %d", again.Status)
				}
			}
		}
	}
	if n < 6 {
		t.Errorf("only %d signed-in routes found", n)
	}
}

// expectedAccess is what each route should declare, written out by hand from
// ROLE-1's list of permissions and the security design, and not read from the
// code: the matrix above shows the guard enforces what a route declares, and
// this shows that what it declares is right. A new route fails the test until
// someone has said who may call it.
var expectedAccess = map[string]string{
	"/":                          "public",
	"/api/":                      "public",
	"/media-tmp/":                "public",
	"/media/":                    "public",
	"GET /api/client-config":     "public",
	"GET /api/health":            "public",
	"GET /api/library":           "public",
	"GET /api/setup/status":      "public",
	"POST /api/setup/admin":      "public",
	"POST /api/auth/signin":      "public",
	"POST /api/register":         "public",
	"GET /api/public/books":      "publiccatalog",
	"GET /api/public/books/{id}": "publiccatalog",
	"GET /media/{name}":          "mixed",

	"GET /api/me":                  "signedin",
	"GET /api/me/card":             "signedin",
	"GET /api/me/card/barcode.svg": "signedin",
	"GET /api/me/card/qr.svg":      "signedin",
	"POST /api/auth/signout":       "signedin",
	"POST /api/me/password":        "signedin",

	"GET /api/books":                "catalog.browse",
	"GET /api/books/{id}":           "catalog.browse",
	"GET /api/books/{id}/reviews":   "catalog.browse",
	"GET /api/locations":            "catalog.browse",
	"GET /api/me/loans":             "loans.own.view",
	"GET /api/me/loans/history":     "loans.own.view",
	"POST /api/me/loans/{id}/renew": "loans.own.renew",
	"PUT /api/books/{id}/review":    "reviews.write",
	"DELETE /api/books/{id}/review": "reviews.write",
	"POST /api/me/checkout":         "circ.self_checkout",

	"GET /api/patrons":                  "patrons.lookup",
	"GET /api/patrons/{id}":             "patrons.lookup",
	"GET /api/patrons/by-card/{code}":   "patrons.lookup",
	"GET /api/patron-loans/{id}":        "patrons.lookup",
	"POST /api/desk/resolve":            "circ.desk",
	"POST /api/desk/checkout":           "circ.desk",
	"POST /api/desk/checkin":            "circ.desk",
	"POST /api/desk/loans/{id}/renew":   "circ.desk",
	"GET /api/desk/overdue":             "circ.desk",
	"POST /api/users/{id}/card/reissue": "cards.reissue",

	"POST /api/books":             "catalog.manage",
	"PUT /api/books/{id}":         "catalog.manage",
	"DELETE /api/books/{id}":      "catalog.manage",
	"POST /api/books/{id}/copies": "catalog.manage",
	"PUT /api/copies/{id}":        "catalog.manage",
	"DELETE /api/copies/{id}":     "catalog.manage",
	"POST /api/locations":         "catalog.manage",
	"PUT /api/locations/{id}":     "catalog.manage",
	"DELETE /api/locations/{id}":  "catalog.manage",
	"POST /api/catalog/labels":    "catalog.manage",
	"POST /api/catalog/lookup":    "catalog.manage",
	"GET /media-tmp/{token}":      "catalog.manage",
	"DELETE /api/reviews/{id}":    "reviews.remove",

	"GET /api/admin/reports/statistics":  "reports.view",
	"GET /api/admin/reports/circulation": "reports.view",
	"GET /api/admin/activity":            "activity.view",

	"GET /api/admin/settings":         "settings.manage",
	"PUT /api/admin/settings":         "settings.manage",
	"PUT /api/admin/branding":         "settings.manage",
	"POST /api/admin/branding/check":  "settings.manage",
	"PUT /api/admin/branding/logo":    "settings.manage",
	"DELETE /api/admin/branding/logo": "settings.manage",
	"PUT /api/admin/copy-id-mode":     "settings.manage",

	"GET /api/admin/invitations":              "invitations.manage",
	"POST /api/admin/invitations":             "invitations.manage",
	"POST /api/admin/invitations/{id}/revoke": "invitations.manage",

	"GET /api/admin/users":                               "users.manage",
	"POST /api/admin/users/{id}/deactivate":              "users.manage",
	"POST /api/admin/users/{id}/reactivate":              "users.manage",
	"POST /api/admin/users/{id}/reset-password":          "users.manage",
	"PUT /api/admin/users/{id}/role":                     "users.permissions",
	"PUT /api/admin/users/{id}/permissions/{permission}": "users.permissions",
}

// ROLE-1/ROLE-3: each route needs the permission its action is named after.
func TestEveryRouteDeclaresThePermissionItsActionIsNamedAfter(t *testing.T) {
	got := map[string]string{}
	for _, rt := range routeTable(t) {
		a := rt.Kind
		if rt.Kind == "require" {
			a = rt.Perm
		}
		got[rt.String()] = a
	}
	for k, want := range expectedAccess {
		g, ok := got[k]
		if !ok {
			t.Errorf("%s is expected but is not registered", k)
		} else if g != want {
			t.Errorf("%s declares %q, want %q", k, g, want)
		}
	}
	for k, g := range got {
		if _, ok := expectedAccess[k]; !ok {
			t.Errorf("%s (%s) is registered but nobody has said who may call it: add it to expectedAccess", k, g)
		}
	}
}

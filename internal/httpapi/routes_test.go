package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { WriteJSON(w, 200, map[string]string{"ok": "yes"}) })

// asCaller is an Authenticate that gives every request the caller c (nil: none).
func asCaller(c *auth.CallerInfo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c != nil {
				r = r.WithContext(auth.WithCaller(r.Context(), c))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func guarded(t *testing.T, c *auth.CallerInfo, publicOn bool) http.Handler {
	t.Helper()
	h, err := NewHandler(Options{
		Authenticate:    asCaller(c),
		PublicCatalogOn: func() bool { return publicOn },
		Routes: func(r *Router) {
			r.Handle("GET /api/books", Require(perm.CatalogBrowse), ok)
			r.Handle("POST /api/books", Require(perm.CatalogManage), ok)
			r.Handle("GET /api/me", SignedIn().AllowWhileMustChange(), ok)
			r.Handle("POST /api/me/password", Require(perm.CatalogBrowse).AllowWhileMustChange(), ok)
			r.Handle("GET /api/public/books", PublicCatalog(), ok)
			r.Handle("POST /api/sign-in", Public(), ok)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func code(h http.Handler, method, path string) (int, string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Code, rec.Body.String()
}

// Every route the server really registers carries a declaration (sec §4).
func TestEveryRouteIsDeclared(t *testing.T) {
	rt, err := buildRouter(Options{Static: ok})
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Routes()) == 0 {
		t.Fatal("no routes registered")
	}
	for _, ri := range rt.Routes() {
		if ri.Access.kind == undeclared || ri.Access.String() == "undeclared" {
			t.Errorf("route %q has no access declaration", ri.Pattern)
		}
	}
}

// An undeclared route stops the server at start-up (fail closed).
func TestUndeclaredRouteStopsStartUp(t *testing.T) {
	_, err := NewHandler(Options{Routes: func(r *Router) {
		r.Handle("GET /api/secret", Access{}, ok)
	}})
	if err == nil || !strings.Contains(err.Error(), "/api/secret") || !strings.Contains(err.Error(), "no access") {
		t.Fatalf("err = %v, want a refusal naming the route", err)
	}
}

func TestBadRegistrationsStopStartUp(t *testing.T) {
	cases := map[string]func(*Router){
		"nil handler":        func(r *Router) { r.Handle("GET /api/x", Public(), nil) },
		"nil func":           func(r *Router) { r.HandleFunc("GET /api/x", Public(), nil) },
		"unknown permission": func(r *Router) { r.Handle("GET /api/x", Require("nope"), ok) },
		"duplicate":          func(r *Router) { r.Handle("GET /api/x", Public(), ok); r.Handle("GET /api/x", Public(), ok) },
		"must-change public": func(r *Router) { r.Handle("GET /api/x", Public().AllowWhileMustChange(), ok) },
		"bad pattern":        func(r *Router) { r.Handle("GET", Public(), ok) },
	}
	for name, f := range cases {
		if _, err := NewHandler(Options{Routes: f}); err == nil {
			t.Errorf("%s: server would start", name)
		}
	}
}

// With no caller (before sessions exist), every Require route answers 401.
func TestNoCallerIs401(t *testing.T) {
	h := guarded(t, nil, true)
	for _, r := range [][2]string{{"GET", "/api/books"}, {"POST", "/api/books"}, {"GET", "/api/me"}, {"POST", "/api/me/password"}} {
		if c, _ := code(h, r[0], r[1]); c != 401 {
			t.Errorf("%s %s = %d, want 401", r[0], r[1], c)
		}
	}
	if c, _ := code(h, "POST", "/api/sign-in"); c != 200 {
		t.Errorf("public route = %d", c)
	}
}

// ROLE-3: a caller without the route's permission is refused and the handler never runs.
func TestWithoutPermissionIs403AndHandlerDoesNotRun(t *testing.T) {
	ran := false
	h, err := NewHandler(Options{
		Authenticate: asCaller(&auth.CallerInfo{UserID: 1, Permissions: perm.Preset(perm.Patron)}),
		Routes: func(r *Router) {
			r.HandleFunc("POST /api/books", Require(perm.CatalogManage), func(w http.ResponseWriter, _ *http.Request) { ran = true })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := code(h, "POST", "/api/books"); c != 403 || ran {
		t.Fatalf("status %d, handler ran %v", c, ran)
	}
}

func TestPermissionHeldIsLetIn(t *testing.T) {
	lib := guarded(t, &auth.CallerInfo{UserID: 1, Permissions: perm.Preset(perm.Librarian)}, false)
	if c, _ := code(lib, "POST", "/api/books"); c != 200 {
		t.Fatalf("librarian = %d", c)
	}
	// ROLE-3 "Removed permission": the librarian with it switched off is refused.
	set := perm.Preset(perm.Librarian)
	delete(set, perm.CatalogManage)
	off := guarded(t, &auth.CallerInfo{UserID: 1, Permissions: set}, false)
	if c, _ := code(off, "POST", "/api/books"); c != 403 {
		t.Fatalf("switched off = %d", c)
	}
	if c, _ := code(off, "GET", "/api/books"); c != 200 {
		t.Fatalf("other routes still open = %d", c)
	}
}

func TestSignedInNeedsNoPermission(t *testing.T) {
	h := guarded(t, &auth.CallerInfo{UserID: 1, Permissions: perm.Set{}}, false)
	if c, _ := code(h, "GET", "/api/me"); c != 200 {
		t.Fatalf("me with no permissions = %d", c)
	}
}

func TestMustChangePasswordReachesOnlyMarkedRoutes(t *testing.T) {
	h := guarded(t, &auth.CallerInfo{UserID: 1, Permissions: perm.Preset(perm.Administrator), MustChangePassword: true}, true)
	for path, want := range map[string]int{"/api/me": 200} {
		if c, _ := code(h, "GET", path); c != want {
			t.Errorf("GET %s = %d, want %d", path, c, want)
		}
	}
	if c, _ := code(h, "POST", "/api/me/password"); c != 200 {
		t.Errorf("change password = %d", c)
	}
	for _, r := range [][2]string{{"GET", "/api/books"}, {"POST", "/api/books"}} {
		if c, _ := code(h, r[0], r[1]); c != 403 {
			t.Errorf("%s %s = %d, want 403", r[0], r[1], c)
		}
	}
}

func TestPublicCatalogIs404WhileOff(t *testing.T) {
	if c, body := code(guarded(t, nil, false), "GET", "/api/public/books"); c != 404 || !strings.Contains(body, "not_found") {
		t.Fatalf("off = %d %s", c, body)
	}
	if c, _ := code(guarded(t, nil, true), "GET", "/api/public/books"); c != 200 {
		t.Fatalf("on = %d", c)
	}
}

func TestPublicCatalogFollowsTheSettingLive(t *testing.T) {
	on := false
	h, err := NewHandler(Options{
		PublicCatalogOn: func() bool { return on },
		Routes:          func(r *Router) { r.Handle("GET /api/public/books", PublicCatalog(), ok) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := code(h, "GET", "/api/public/books"); c != 404 {
		t.Fatalf("off = %d", c)
	}
	on = true
	if c, _ := code(h, "GET", "/api/public/books"); c != 200 {
		t.Fatalf("on = %d", c)
	}
}

func TestPublicCatalogIsOffWithoutASetting(t *testing.T) {
	h, _ := NewHandler(Options{Routes: func(r *Router) { r.Handle("GET /api/public/books", PublicCatalog(), ok) }})
	if c, _ := code(h, "GET", "/api/public/books"); c != 404 {
		t.Fatalf("= %d", c)
	}
}

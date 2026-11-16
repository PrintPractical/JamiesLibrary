package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/useradmin"
	"jamieslibrary/internal/users"
)

const goodPassword = "correct horse battery"

// site is the real services behind the real handler, on a data directory of its own.
type site struct {
	t     *testing.T
	dir   string
	st    *store.Store
	users *users.Service
	set   *settings.Service
	auth  *auth.Service
	h     http.Handler
	logs  *bytes.Buffer
	now   time.Time
	// remote is the connection address requests come from; "" is httptest's default.
	remote string
}

func newSite(t *testing.T, tweak ...func(*Options)) *site {
	t.Helper()
	ctx := context.Background()
	s := &site{t: t, dir: t.TempDir(), logs: &bytes.Buffer{}, now: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	t.Cleanup(clock.SetSource(func() time.Time { return s.now }))
	var err error
	if s.st, err = store.Open(ctx, s.dir, store.Options{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.st.Close() })
	s.users = users.New(s.st)
	if s.set, err = settings.New(ctx, s.st); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(s.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s.auth = auth.NewService(s.st, s.users,
		func() time.Duration { return time.Duration(s.set.Get().SessionIdleMinutes) * time.Minute }, log)
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>app</html>") })
	opts := Options{
		Logger: log, Static: static, Auth: s.auth, Users: s.users, Settings: s.set, Activity: activity.New(s.st), Store: s.st,
		UserAdmin: useradmin.New(s.st, s.users, s.auth), PublicCatalogOn: func() bool { return s.set.Get().PublicCatalog },
	}
	for _, f := range tweak {
		f(&opts)
	}
	s.h, err = NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *site) addUser(name, password string, role perm.Role) users.User {
	s.t.Helper()
	hash, err := s.auth.Passwords.Hash(context.Background(), "password", password)
	if err != nil {
		s.t.Fatal(err)
	}
	u, err := s.users.Create(context.Background(), users.NewUser{Username: name, DisplayName: name, PasswordHash: hash, Role: role})
	if err != nil {
		s.t.Fatal(err)
	}
	return u
}

// req sends one request; cookie is the session token to send ("" for none).
func (s *site) req(method, path, cookie, body string, hdr ...string) *httptest.ResponseRecorder {
	s.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	if s.remote != "" {
		r.RemoteAddr = s.remote
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}

func (s *site) signIn(name, password string) string {
	s.t.Helper()
	rec := s.req("POST", "/api/auth/signin", "", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password))
	c := sessionCookie(rec)
	if rec.Code != 200 || c == nil {
		s.t.Fatalf("sign-in of %s: %d %s", name, rec.Code, rec.Body)
	}
	return c.Value
}

func errMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e errResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return e.Error.Message
}

// ACCESS-5 "Wrong password": a registered user who gives the wrong password is refused and told the credentials are wrong.
func TestWrongPassword(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	wrong := s.req("POST", "/api/auth/signin", "", `{"username":"ann","password":"not the password"}`)
	unknown := s.req("POST", "/api/auth/signin", "", `{"username":"nobody","password":"not the password"}`)
	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong": wrong, "unknown": unknown} {
		if rec.Code != 401 || sessionCookie(rec) != nil {
			t.Errorf("%s: status %d, cookie %v", name, rec.Code, sessionCookie(rec))
		}
		if m := errMessage(t, rec); !strings.Contains(m, "username or password is wrong") {
			t.Errorf("%s: message %q", name, m)
		}
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("an unknown user is told apart from a wrong password:\n%s\n%s", wrong.Body, unknown.Body)
	}
}

func TestSignInSetsTheCookieAndAnswersLikeMe(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Librarian)
	rec := s.req("POST", "/api/auth/signin", "", `{"username":"ANN","password":"`+goodPassword+`"}`)
	c := sessionCookie(rec)
	if rec.Code != 200 || c == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if c.Name != "__Host-jl_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode ||
		c.Path != "/" || c.Domain != "" || c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Fatalf("cookie flags: %+v", c)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("header %q", rec.Header().Get("Set-Cookie"))
	}
	var me meBody
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.User.Username != "ann" || me.User.Role != perm.Librarian || me.MustChangePassword || me.Settings.Name != "Jamie's Library" {
		t.Fatalf("body %s", rec.Body)
	}
	if !contains(me.Permissions, "circ.desk") || contains(me.Permissions, "users.manage") {
		t.Fatalf("permissions %v", me.Permissions)
	}
	if strings.Contains(rec.Body.String(), "argon2") || strings.Contains(rec.Body.String(), goodPassword) {
		t.Fatalf("the body holds a hash or a password: %s", rec.Body)
	}
	me2 := s.req("GET", "/api/me", c.Value, "")
	if me2.Code != 200 || me2.Body.String() != rec.Body.String() {
		t.Fatalf("/api/me: %d %s", me2.Code, me2.Body)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestSignInNeverAdoptsAPresentedToken(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	old := s.signIn("ann", goodPassword)
	rec := s.req("POST", "/api/auth/signin", "attacker-chosen", `{"username":"ann","password":"`+goodPassword+`"}`)
	c := sessionCookie(rec)
	if c == nil || c.Value == "attacker-chosen" {
		t.Fatalf("cookie %+v", c)
	}
	rec = s.req("POST", "/api/auth/signin", old, `{"username":"ann","password":"`+goodPassword+`"}`)
	if s.req("GET", "/api/me", old, "").Code != 401 {
		t.Fatal("signing in again left the browser's old session alive")
	}
}

func TestDeactivatedAccountIsToldOnlyAfterTheRightPassword(t *testing.T) {
	s := newSite(t)
	u := s.addUser("ann", goodPassword, perm.Patron)
	s.st.DB().Exec(`UPDATE users SET active = 0 WHERE id = ?`, u.ID)
	wrong := s.req("POST", "/api/auth/signin", "", `{"username":"ann","password":"not the password"}`)
	right := s.req("POST", "/api/auth/signin", "", `{"username":"ann","password":"`+goodPassword+`"}`)
	if wrong.Code != 401 || strings.Contains(wrong.Body.String(), "deactivated") {
		t.Fatalf("wrong password: %d %s", wrong.Code, wrong.Body)
	}
	if right.Code != 403 || !strings.Contains(right.Body.String(), "deactivated") || sessionCookie(right) != nil {
		t.Fatalf("right password: %d %s", right.Code, right.Body)
	}
}

func TestAnAccountWithNoPasswordSetCannotBeSignedInto(t *testing.T) {
	s := newSite(t)
	_, err := s.users.Create(context.Background(), users.NewUser{Username: "ghost", DisplayName: "Ghost", PasswordHash: "", Role: perm.Patron})
	if err != nil {
		t.Fatal(err)
	}
	for _, pw := range []string{"", "anything at all"} {
		rec := s.req("POST", "/api/auth/signin", "", fmt.Sprintf(`{"username":"ghost","password":%q}`, pw))
		if rec.Code != 401 {
			t.Fatalf("password %q: %d", pw, rec.Code)
		}
	}
}

func TestSignInBodyProblems(t *testing.T) {
	s := newSite(t)
	for name, body := range map[string]string{
		"empty": "", "not json": "{", "unknown field": `{"username":"a","password":"b","x":1}`,
		"wrong type": `{"username":1,"password":"b"}`, "too big": `{"username":"` + strings.Repeat("a", 70000) + `"}`,
	} {
		rec := s.req("POST", "/api/auth/signin", "", body)
		if rec.Code < 400 || rec.Code >= 500 || sessionCookie(rec) != nil {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
}

func TestSignOutEndsTheSessionAndClearsTheCookie(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	rec := s.req("POST", "/api/auth/signout", tok, "")
	if rec.Code != 204 {
		t.Fatalf("status %d", rec.Code)
	}
	if c := sessionCookie(rec); c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("cookie %+v", c)
	}
	if s.req("GET", "/api/me", tok, "").Code != 401 {
		t.Fatal("the session lives on after sign-out")
	}
	if s.req("POST", "/api/auth/signout", "", "").Code != 401 {
		t.Fatal("anonymous sign-out is not 401")
	}
}

// ACCESS-6 "Change": the next sign-in needs the new password.
func TestChange(t *testing.T) {
	s := newSite(t)
	u := s.addUser("ann", goodPassword, perm.Patron)
	s.st.DB().Exec(`UPDATE users SET must_change_password = 1 WHERE id = ?`, u.ID)
	tok, other := s.signIn("ann", goodPassword), s.signIn("ann", goodPassword)
	// a must-change user can reach only me, change-password and sign-out
	if s.req("GET", "/api/health", tok, "").Code != 200 {
		t.Fatal("health")
	}
	rec := s.req("POST", "/api/me/password", tok, `{"current_password":"`+goodPassword+`","new_password":"twelve chars!"}`)
	if rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if s.req("POST", "/api/auth/signin", "", `{"username":"ann","password":"`+goodPassword+`"}`).Code != 401 {
		t.Fatal("the old password still signs in")
	}
	s.signIn("ann", "twelve chars!")
	var me meBody
	json.Unmarshal(s.req("GET", "/api/me", tok, "").Body.Bytes(), &me)
	if me.MustChangePassword {
		t.Fatal("must-change not cleared")
	}
	if s.req("GET", "/api/me", other, "").Code != 401 {
		t.Fatal("another session survived the change")
	}
}

// ACCESS-6 "Wrong current password": the change is refused.
func TestWrongCurrentPassword(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	rec := s.req("POST", "/api/me/password", tok, `{"current_password":"not the password","new_password":"twelve chars!"}`)
	if rec.Code != 422 || !strings.Contains(errMessage(t, rec), "current password is wrong") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	s.signIn("ann", goodPassword) // unchanged
	if s.req("POST", "/api/auth/signin", "", `{"username":"ann","password":"twelve chars!"}`).Code != 401 {
		t.Fatal("the new password was stored")
	}
}

// ACCESS-6 "Too short": a new password of 6 characters is refused and the message says the minimum is 10.
func TestTooShort(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	rec := s.req("POST", "/api/me/password", tok, `{"current_password":"`+goodPassword+`","new_password":"abc123"}`)
	if rec.Code != 422 || !strings.Contains(errMessage(t, rec), "at least 10") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	s.signIn("ann", goodPassword)
}

func TestChangePasswordNeedsASession(t *testing.T) {
	s := newSite(t)
	if rec := s.req("POST", "/api/me/password", "", `{"current_password":"a","new_password":"b"}`); rec.Code != 401 {
		t.Fatalf("%d", rec.Code)
	}
}

// SEC-T1 "Idle session": a user idle for longer than the session timeout must sign in again.
func TestIdleSession(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	s.now = s.now.Add(7 * time.Hour)
	if s.req("GET", "/api/me", tok, "").Code != 200 {
		t.Fatal("out after 7 hours")
	}
	s.now = s.now.Add(7 * time.Hour) // 14 hours in all, but active 7 hours ago
	if s.req("GET", "/api/me", tok, "").Code != 200 {
		t.Fatal("an active session was ended")
	}
	s.now = s.now.Add(8*time.Hour + time.Minute)
	rec := s.req("GET", "/api/me", tok, "")
	if rec.Code != 401 {
		t.Fatalf("after 8h01 idle: %d", rec.Code)
	}
	s.signIn("ann", goodPassword) // and they can sign in again
}

func TestIdleTimeIsTheAdministratorsSetting(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	if err := s.set.SetSessionIdle(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	tok := s.signIn("ann", goodPassword)
	s.now = s.now.Add(31 * time.Minute)
	if s.req("GET", "/api/me", tok, "").Code != 401 {
		t.Fatal("still signed in after 31 minutes with a 30-minute setting")
	}
}

func TestDeactivationAndPermissionChangesTakeEffectOnTheNextRequest(t *testing.T) {
	s := newSite(t)
	u := s.addUser("ann", goodPassword, perm.Librarian)
	tok := s.signIn("ann", goodPassword)
	s.users.SetPermission(context.Background(), u.ID, perm.CircDesk, false)
	var me meBody
	json.Unmarshal(s.req("GET", "/api/me", tok, "").Body.Bytes(), &me)
	if contains(me.Permissions, "circ.desk") {
		t.Fatal("a switched-off permission is still listed")
	}
	s.st.DB().Exec(`UPDATE users SET active = 0 WHERE id = ?`, u.ID)
	if s.req("GET", "/api/me", tok, "").Code != 401 {
		t.Fatal("a deactivated user is still signed in")
	}
}

// ACCESS-1 "Anonymous visitor": nobody who isn't signed in sees catalog data; every route that needs a permission is 401,
// and the app's own pages load the app shell, which shows sign-in.
func TestAnonymousVisitor(t *testing.T) {
	s := newSite(t)
	rt, err := buildRouter(Options{Auth: s.auth, Users: s.users, Settings: s.set, Store: s.st,
		Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, ri := range rt.Routes() {
		if ri.Access.String() == "public" || ri.Access.String() == "public catalog" {
			continue
		}
		method, path, _ := strings.Cut(ri.Pattern, " ")
		path = strings.NewReplacer("{id}", "1", "{code}", "x", "{...}", "x").Replace(path)
		rec := s.req(method, path, "", "")
		if rec.Code != 401 {
			t.Errorf("%s anonymous: %d", ri.Pattern, rec.Code)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no route that needs a session was checked")
	}
	for _, p := range []string{"/", "/books/12", "/signin", "/desk/checkout", "/catalog/new"} {
		rec := s.req("GET", p, "", "")
		if rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body)
		}
	}
	for _, p := range []string{"/api/books", "/api/catalog/books", "/api/public/books", "/media/covers/x.jpg"} {
		if rec := s.req("GET", p, "", ""); rec.Code < 400 || strings.Contains(rec.Body.String(), "title") {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	// a made-up or stale cookie is no better than none
	if s.req("GET", "/api/me", "forged-token", "").Code != 401 {
		t.Fatal("a forged cookie got in")
	}
}

func TestCrossOriginPostIsRefused(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	body := `{"current_password":"` + goodPassword + `","new_password":"twelve chars!"}`
	for name, h := range map[string][]string{
		"foreign Origin":      {"Origin", "https://evil.example"},
		"Sec-Fetch-Site":      {"Sec-Fetch-Site", "cross-site"},
		"same-site subdomain": {"Sec-Fetch-Site", "same-site"},
	} {
		rec := s.req("POST", "/api/me/password", tok, body, h...)
		if rec.Code != 403 {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	s.signIn("ann", goodPassword) // nothing was changed
	for name, h := range map[string][]string{
		"same Origin":    {"Origin", "http://example.com"},
		"same-origin":    {"Sec-Fetch-Site", "same-origin"},
		"no browser hdr": nil,
	} {
		rec := s.req("POST", "/api/auth/signin", "", `{"username":"ann","password":"`+goodPassword+`"}`, h...)
		if rec.Code != 200 {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	if h := s.req("GET", "/api/health", "", "", "Origin", "https://evil.example").Header(); h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("a CORS header was sent")
	}
}

func TestStateChangingRoutesTakeOnlyJSON(t *testing.T) {
	s := newSite(t)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		r := httptest.NewRequest("POST", "/api/auth/signin", strings.NewReader(`{"username":"a","password":"b"}`))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, r)
		if rec.Code != 415 {
			t.Errorf("%q: status %d", ct, rec.Code)
		}
	}
	// JSON with a charset is still JSON
	r := httptest.NewRequest("POST", "/api/auth/signin", strings.NewReader(`{"username":"a","password":"b"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	if rec.Code != 401 {
		t.Errorf("json with charset: %d", rec.Code)
	}
}

func TestMultipartOnlyWhereARouteSaysSo(t *testing.T) {
	h, err := NewHandler(Options{Routes: func(rt *Router) {
		rt.Handle("POST /api/upload", SignedIn().Multipart(), ok)
		rt.Handle("POST /api/plain", Public(), ok)
	}, Authenticate: asCaller(&auth.CallerInfo{UserID: 1})})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/api/upload": 200, "/api/plain": 415} {
		r := httptest.NewRequest("POST", path, strings.NewReader("--x--"))
		r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	rt, _ := buildRouter(Options{Routes: func(rt *Router) { rt.Handle("POST /api/upload", Public().Multipart(), ok) }})
	if rt.Err() != nil {
		t.Fatal(rt.Err())
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newSite(t)
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; font-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Permissions-Policy":      "camera=(self), microphone=(), geolocation=()",
	}
	for _, path := range []string{"/", "/books/1", "/api/health", "/api/me", "/api/nope", "/media/x.jpg"} {
		rec := s.req("GET", path, "", "")
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s %s = %q", path, k, got)
			}
		}
		if rec.Header().Get("Strict-Transport-Security") != "" {
			t.Errorf("%s: HSTS over plain HTTP", path)
		}
		if got, api := rec.Header().Get("Cache-Control"), strings.HasPrefix(path, "/api/"); (got == "no-store") != api {
			t.Errorf("%s: Cache-Control %q", path, got)
		}
	}
	// error responses of every kind carry them too
	if rec := s.req("POST", "/api/auth/signin", "", "{"); rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("headers missing on an error response")
	}
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("HSTS over HTTPS = %q", got)
	}
}

func TestNothingSecretIsLogged(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	s.req("POST", "/api/auth/signin", "", `{"username":"`+strings.Repeat("x", 100)+`","password":"hunter2hunter2"}`)
	tok := s.signIn("ann", goodPassword)
	s.req("GET", "/api/me?token="+tok, tok, "")
	s.req("POST", "/api/me/password", tok, `{"current_password":"`+goodPassword+`","new_password":"twelve chars!"}`)
	out := s.logs.String()
	for _, secret := range []string{"hunter2hunter2", goodPassword, "twelve chars!", tok, "__Host-jl_session", "password_hash", "$argon2id"} {
		if strings.Contains(out, secret) {
			t.Errorf("the log holds %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "sign-in failed") || !strings.Contains(out, strings.Repeat("x", 64)) || strings.Contains(out, strings.Repeat("x", 65)) {
		t.Errorf("a failed sign-in should log the username cut to 64 characters:\n%s", out)
	}
	if strings.Contains(out, "?token") {
		t.Error("the query string was logged")
	}
}

// ACCESS-5: passwords are stored only as salted hashes. Nothing in the data directory holds one in the clear.
func TestNoPlaintextPasswordInTheDataDirectory(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", "first secret phrase", perm.Patron)
	tok := s.signIn("ann", "first secret phrase")
	rec := s.req("POST", "/api/me/password", tok, `{"current_password":"first secret phrase","new_password":"second secret phrase"}`)
	if rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	s.signIn("ann", "second secret phrase")
	found := 0
	filepath.Walk(s.dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		found++
		for _, secret := range []string{"first secret phrase", "second secret phrase", tok} {
			if bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s holds %q", p, secret)
			}
		}
		return nil
	})
	if found == 0 {
		t.Fatal("scanned no files")
	}
	var hash string
	s.st.DB().QueryRow(`SELECT password_hash FROM users WHERE username = 'ann'`).Scan(&hash)
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("stored hash %q", hash)
	}
}

func TestSettingsServiceIsTheOneTheServerShares(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	s.set.SetName(context.Background(), "Elm Street Library")
	var me meBody
	json.Unmarshal(s.req("GET", "/api/me", tok, "").Body.Bytes(), &me)
	if me.Settings.Name != "Elm Street Library" {
		t.Fatalf("settings %+v", me.Settings)
	}
	if s.req("GET", "/api/public/books", "", "").Code != 404 {
		// no public routes exist yet: this is the route table's 404 either way
		t.Fatal("public catalog route answered")
	}
}

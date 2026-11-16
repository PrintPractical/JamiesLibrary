package e2e

// Sign-in, sessions, limits, setup code, invitations and user administration,
// judged from outside: over HTTP against the running server, with the clock
// moved through the control listener. Nothing here reads the server's own
// packages beyond the test harness.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

const adminPassword = "correct horse battery"

// who is one client of the server: its own cookie jar, and optionally a
// forwarded address it claims in X-Forwarded-For.
type who struct {
	t   *testing.T
	s   *testutil.Server
	c   *http.Client
	xff string
}

func newWho(t *testing.T, s *testutil.Server) *who { return &who{t: t, s: s, c: s.Client()} }

type reply struct {
	Status int
	Header http.Header
	Raw    string
	JSON   map[string]any
}

// errField is the error's field name, if the reply is an error with one.
func (r reply) errMessage() string {
	e, _ := r.JSON["error"].(map[string]any)
	m, _ := e["message"].(string)
	return m
}

func (w *who) do(method, path string, body any) reply {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, w.s.URL+path, rd)
	if err != nil {
		w.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if w.xff != "" {
		req.Header.Set("X-Forwarded-For", w.xff)
	}
	resp, err := w.c.Do(req)
	if err != nil {
		w.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	r := reply{Status: resp.StatusCode, Header: resp.Header, Raw: string(raw)}
	_ = json.Unmarshal(raw, &r.JSON)
	return r
}

func (w *who) want(status int, method, path string, body any) reply {
	w.t.Helper()
	r := w.do(method, path, body)
	if r.Status != status {
		w.t.Fatalf("%s %s: want %d, got %d: %s", method, path, status, r.Status, r.Raw)
	}
	return r
}

func (w *who) signIn(user, password string) reply {
	return w.do("POST", "/api/auth/signin", map[string]string{"username": user, "password": password})
}

func (w *who) mustSignIn(user, password string) {
	w.t.Helper()
	if r := w.signIn(user, password); r.Status != 200 {
		w.t.Fatalf("sign-in as %s: %d %s", user, r.Status, r.Raw)
	}
}

// install starts a fresh library and creates its first administrator "root".
func install(t *testing.T, env map[string]string) (*testutil.Server, *who) {
	t.Helper()
	s := testutil.StartServer(t, testutil.Options{Env: env})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatalf("no setup code in the console output:\n%s", s.Stdout())
	}
	root := newWho(t, s)
	root.want(200, "POST", "/api/setup/admin", map[string]string{
		"code": code, "username": "root", "display_name": "Root", "password": adminPassword})
	return s, root
}

// invite makes an invitation for role as w and returns its id and code.
func (w *who) invite(role string) (int64, string) {
	w.t.Helper()
	r := w.want(201, "POST", "/api/admin/invitations", map[string]string{"role": role})
	id, _ := r.JSON["id"].(float64)
	code, _ := r.JSON["code"].(string)
	if code == "" {
		w.t.Fatalf("invitation without a code: %s", r.Raw)
	}
	return int64(id), code
}

// member registers a new account with a fresh invitation and returns its id and a signed-in client.
func (w *who) member(role, username string) (int64, *who) {
	w.t.Helper()
	_, code := w.invite(role)
	n := newWho(w.t, w.s)
	r := n.want(201, "POST", "/api/register", map[string]string{
		"code": code, "username": username, "display_name": username, "password": adminPassword})
	u, _ := r.JSON["user"].(map[string]any)
	return int64(u["id"].(float64)), n
}

func perms(r reply) map[string]bool {
	out := map[string]bool{}
	list, _ := r.JSON["permissions"].([]any)
	for _, p := range list {
		out[p.(string)] = true
	}
	return out
}

// dataFiles lists every file under the data directory.
func dataFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// grepDataDir fails if any of the secrets is found in any file of the data directory.
func grepDataDir(t *testing.T, dir string, secrets ...string) {
	t.Helper()
	files := dataFiles(t, dir)
	if len(files) == 0 {
		t.Fatal("the data directory holds no files: the scan would prove nothing")
	}
	canary := false
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// the account name is stored as text: if it can't be found, the scan can't see what it is looking for
		canary = canary || bytes.Contains(b, []byte("root"))
		for _, s := range secrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%q is stored in clear in %s", s, f)
			}
		}
	}
	if !canary {
		t.Fatal("the scan found no readable account name in the data directory, so it proves nothing")
	}
}

// ---- DEPLOY-3: the setup code ----------------------------------------------

// DEPLOY-3 "Wrong or missing setup code": nothing is created, and the right
// code afterwards still works. "Setup closed after the first administrator".
func TestSetupCodeWrongMissingAndDeadAfterTheFirstAdministrator(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatalf("no setup code printed:\n%s", s.Stdout())
	}
	v := newWho(t, s)
	v.want(200, "GET", "/api/setup/status", nil)
	body := func(code string) map[string]string {
		return map[string]string{"code": code, "username": "mallory", "display_name": "M", "password": adminPassword}
	}
	for name, c := range map[string]string{"missing": "", "wrong": "AAAA-AAAA-AAAA-AAAA", "lower-cased right code with a character changed": strings.ToLower(code[:len(code)-1]) + otherThan(code[len(code)-1]), "garbage": "' OR 1=1 --"} {
		if r := v.do("POST", "/api/setup/admin", body(c)); r.Status != 403 {
			t.Errorf("%s code: want 403, got %d %s", name, r.Status, r.Raw)
		}
	}
	if r := v.signIn("mallory", adminPassword); r.Status != 401 {
		t.Fatalf("a refused setup created an account: sign-in gave %d", r.Status)
	}
	v.want(200, "GET", "/api/setup/status", nil) // still open

	good := newWho(t, s)
	r := good.want(200, "POST", "/api/setup/admin", map[string]string{"code": code, "username": "root", "display_name": "Root", "password": adminPassword})
	if !perms(r)["users.manage"] || !perms(r)["users.permissions"] {
		t.Fatalf("first administrator lacks the administrator permissions: %s", r.Raw)
	}
	list := good.want(200, "GET", "/api/admin/users", nil)
	if list.JSON["total"].(float64) != 1 {
		t.Fatalf("expected exactly one user, got %s", list.Raw)
	}
	// dead: the same code, a new code, status, all 404, and no second admin
	for _, c := range []string{code, "AAAA-AAAA-AAAA-AAAA", ""} {
		if r := v.do("POST", "/api/setup/admin", body(c)); r.Status != 404 {
			t.Errorf("setup after the first administrator with %q: want 404, got %d %s", c, r.Status, r.Raw)
		}
	}
	v.want(404, "GET", "/api/setup/status", nil)

	// and after a restart: no new code is printed and setup stays closed
	dir := s.DataDir
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	s2 := testutil.StartServer(t, testutil.Options{DataDir: dir})
	if c2, printed := s2.SetupCode(); printed {
		t.Fatalf("a restart after setup printed a setup code: %s", c2)
	}
	v2 := newWho(t, s2)
	v2.want(404, "GET", "/api/setup/status", nil)
	v2.want(404, "POST", "/api/setup/admin", body(code))
}

// DEPLOY-3 "Two setups at once": racing requests, all with the right code,
// give exactly one administrator.
func TestConcurrentSetupsGiveOneAdministrator(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, _ := s.SetupCode()
	const n = 8
	var wg sync.WaitGroup
	statuses := make([]int, n)
	clients := make([]*who, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		clients[i] = newWho(t, s)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			statuses[i] = clients[i].do("POST", "/api/setup/admin", map[string]string{
				"code": code, "username": fmt.Sprintf("racer%d", i), "display_name": "R", "password": adminPassword}).Status
		}(i)
	}
	close(start)
	wg.Wait()
	won := -1
	for i, st := range statuses {
		switch st {
		case 200:
			if won >= 0 {
				t.Fatalf("two setups succeeded: %v", statuses)
			}
			won = i
		case 404, 409:
		default:
			t.Errorf("racer %d: unexpected status %d (all: %v)", i, st, statuses)
		}
	}
	if won < 0 {
		t.Fatalf("no setup succeeded: %v", statuses)
	}
	if got := clients[won].want(200, "GET", "/api/admin/users", nil).JSON["total"].(float64); got != 1 {
		t.Fatalf("%v users after a racing setup, want 1", got)
	}
}

// DEPLOY-3 + SEC-T1: wrong setup codes count with registration's failures: ten
// block the address even for the right code.
func TestTenWrongSetupCodesBlockTheAddressThenItRecovers(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, _ := s.SetupCode()
	v := newWho(t, s)
	in := func(c string) map[string]string {
		return map[string]string{"code": c, "username": "root", "display_name": "R", "password": adminPassword}
	}
	for i := 0; i < 10; i++ {
		v.want(403, "POST", "/api/setup/admin", in("AAAA-AAAA-AAAA-AAAA"))
	}
	if r := v.do("POST", "/api/setup/admin", in(code)); r.Status != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("the right code from a blocked address: %d %v", r.Status, r.Raw)
	}
	if err := s.Advance(16 * time.Minute); err != nil {
		t.Fatal(err)
	}
	v.want(200, "POST", "/api/setup/admin", in(code))
}

// ---- ACCESS-5/6/7 ----------------------------------------------------------

// ACCESS-5/6/7: no password, chosen or temporary, is ever stored in clear in the
// data directory; a temporary password forces a change and then stops working.
func TestPasswordsNeverStoredInClearAndTemporaryOnesForceAChange(t *testing.T) {
	s, root := install(t, nil)
	const memberPassword = "member-secret-phrase-77"
	_, code := root.invite("patron")
	pat := newWho(t, s)
	pat.want(201, "POST", "/api/register", map[string]string{"code": code, "username": "pat", "display_name": "Pat", "password": memberPassword})

	reset := root.want(200, "POST", "/api/admin/users/2/reset-password", nil)
	temp, _ := reset.JSON["temporary_password"].(string)
	if len(temp) < 12 {
		t.Fatalf("temporary password too short or missing: %s", reset.Raw)
	}
	if temp == memberPassword {
		t.Fatal("the temporary password equals the old password")
	}
	pat.want(401, "GET", "/api/me", nil) // its live session ended with the reset
	// the old password is dead at once
	if r := newWho(t, s).signIn("pat", memberPassword); r.Status != 401 {
		t.Fatalf("old password after a reset: %d", r.Status)
	}

	// scan while the server runs (the write-ahead log holds recent writes) and after it stops
	grepDataDir(t, s.DataDir, adminPassword, memberPassword, temp)

	// the temporary password works, but only to change it
	tmp := newWho(t, s)
	signed := tmp.want(200, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": temp})
	if signed.JSON["must_change_password"] != true {
		t.Fatalf("sign-in with a temporary password does not say it must be changed: %s", signed.Raw)
	}
	for _, p := range []string{"/api/books", "/api/locations", "/api/me/card"} {
		if r := tmp.do("GET", p, nil); r.Status != 403 {
			t.Errorf("GET %s while the password must change: want 403, got %d", p, r.Status)
		}
	}
	tmp.want(200, "GET", "/api/me", nil)
	const next = "brand-new-choice-4242"
	tmp.want(422, "POST", "/api/me/password", map[string]string{"current_password": temp, "new_password": "short"})
	tmp.want(204, "POST", "/api/me/password", map[string]string{"current_password": temp, "new_password": next})
	tmp.want(200, "GET", "/api/books", nil)
	if r := newWho(t, s).signIn("pat", temp); r.Status != 401 {
		t.Fatalf("temporary password still works after the change: %d", r.Status)
	}
	newWho(t, s).mustSignIn("pat", next)

	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	grepDataDir(t, s.DataDir, adminPassword, memberPassword, temp, next)
	logs := s.Stdout() + s.Log()
	if len(logs) < 100 {
		t.Fatalf("logs are suspiciously short (%d bytes): the scan proves nothing", len(logs))
	}
	for _, secret := range []string{adminPassword, memberPassword, temp, next} {
		if strings.Contains(logs, secret) {
			t.Errorf("a password is written to the logs: %q", secret)
		}
	}
}

// ACCESS-6: a reset ends the user's live sessions at once.
func TestResetEndsLiveSessions(t *testing.T) {
	s, root := install(t, nil)
	id, pat := root.member("patron", "pat")
	pat.want(200, "GET", "/api/me", nil)
	root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/reset-password", id), nil)
	pat.want(401, "GET", "/api/me", nil)
	_ = s
}

// ---- SEC-T1 ------------------------------------------------------------------

// SEC-T1 "Idle expiry": a session unused for longer than the idle setting is
// gone, one in regular use is not.
func TestSessionExpiresWhenIdleNotWhenInUse(t *testing.T) {
	s, root := install(t, nil)
	root.want(200, "PUT", "/api/admin/settings", map[string]int{"session_idle_minutes": 30})
	_, pat := root.member("patron", "pat")
	pat.want(200, "GET", "/api/me", nil)

	// in regular use, 20 minutes apart, for well over the idle time
	for i := 0; i < 4; i++ {
		if err := s.Advance(20 * time.Minute); err != nil {
			t.Fatal(err)
		}
		pat.want(200, "GET", "/api/me", nil)
	}
	if err := s.Advance(29 * time.Minute); err != nil {
		t.Fatal(err)
	}
	pat.want(200, "GET", "/api/me", nil)
	if err := s.Advance(31 * time.Minute); err != nil {
		t.Fatal(err)
	}
	pat.want(401, "GET", "/api/me", nil)
	// the administrator, idle just as long, is gone too
	root.want(401, "GET", "/api/me", nil)
	// and it stays dead: going back in time does not revive it
	if err := s.Advance(-2 * time.Hour); err != nil {
		t.Fatal(err)
	}
	pat.want(401, "GET", "/api/me", nil)
	newWho(t, s).mustSignIn("pat", adminPassword)
}

// SEC-T1: the settings floor stops an idle time that would sign everyone out at once.
func TestIdleSettingBounds(t *testing.T) {
	_, root := install(t, nil)
	for _, v := range []int{0, -5, 14, 43201} {
		if r := root.do("PUT", "/api/admin/settings", map[string]int{"session_idle_minutes": v}); r.Status != 422 {
			t.Errorf("idle %d: want 422, got %d %s", v, r.Status, r.Raw)
		}
	}
	root.want(200, "PUT", "/api/admin/settings", map[string]int{"session_idle_minutes": 15})
	root.want(200, "PUT", "/api/admin/settings", map[string]int{"session_idle_minutes": 43200})
}

// SEC-T1 "Repeated failed sign-ins": five failures block the address for 15
// minutes, even for the right password; another address is not affected; the
// account itself is never locked.
func TestFiveFailuresBlockTheAddressNotTheAccount(t *testing.T) {
	s, root := install(t, map[string]string{"JL_TRUSTED_PROXIES": "127.0.0.0/8"})
	root.member("patron", "pat")
	attacker := newWho(t, s)
	attacker.xff = "203.0.113.7"
	for i := 0; i < 5; i++ {
		attacker.want(401, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": "wrong password!"})
	}
	r := attacker.signIn("pat", adminPassword)
	if r.Status != 429 || r.Header.Get("Retry-After") != "900" {
		t.Fatalf("right password from a blocked address: %d retry-after %q", r.Status, r.Header.Get("Retry-After"))
	}
	if len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("a refused sign-in set a cookie: %v", r.Header.Values("Set-Cookie"))
	}
	// the owner of the account, from another address, signs in at once
	owner := newWho(t, s)
	owner.xff = "198.51.100.20"
	owner.mustSignIn("pat", adminPassword)
	// ...and so does the same address once 15 minutes have gone
	if err := s.Advance(14*time.Minute + 50*time.Second); err != nil {
		t.Fatal(err)
	}
	attacker.want(429, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": adminPassword})
	if err := s.Advance(20 * time.Second); err != nil {
		t.Fatal(err)
	}
	attacker.mustSignIn("pat", adminPassword)
}

// SEC-T1: the failure count is forgotten after a quiet spell, so four failures
// now and four after the reset period do not add up.
func TestFailuresAreForgottenAfterQuietTime(t *testing.T) {
	s, root := install(t, nil)
	root.member("patron", "pat")
	v := newWho(t, s)
	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			v.want(401, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": "wrong password!"})
		}
		if err := s.Advance(16 * time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	v.mustSignIn("pat", adminPassword)
}

// SEC-T1 "Invitation guessing": ten bad codes block registration from the
// address, even for a valid code; another address can still register.
func TestTenBadInvitationCodesBlockRegistration(t *testing.T) {
	s, root := install(t, map[string]string{"JL_TRUSTED_PROXIES": "127.0.0.0/8"})
	_, good := root.invite("patron")
	guesser := newWho(t, s)
	guesser.xff = "203.0.113.50"
	reg := func(w *who, code, name string) reply {
		return w.do("POST", "/api/register", map[string]string{"code": code, "username": name, "display_name": name, "password": adminPassword})
	}
	for i := 0; i < 10; i++ {
		if r := reg(guesser, fmt.Sprintf("WRONG-CODE-%02d", i), "g"); r.Status != 403 && r.Status != 404 && r.Status != 422 {
			t.Fatalf("bad code %d: unexpected %d %s", i, r.Status, r.Raw)
		}
	}
	if r := reg(guesser, good, "guesser"); r.Status != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("valid code from a blocked address: %d %s", r.Status, r.Raw)
	}
	other := newWho(t, s)
	other.xff = "198.51.100.9"
	if r := reg(other, good, "honest"); r.Status != 201 {
		t.Fatalf("another address was affected: %d %s", r.Status, r.Raw)
	}
	// the block is also over after 15 minutes
	if err := s.Advance(16 * time.Minute); err != nil {
		t.Fatal(err)
	}
	_, again := root.invite("patron")
	if r := reg(guesser, again, "guesser"); r.Status != 201 {
		t.Fatalf("registration after the block: %d %s", r.Status, r.Raw)
	}
}

// SEC-T1 "Forged forwarding header": without JL_TRUSTED_PROXIES the header is
// ignored (a changing forged address does not escape the limit); with it, the
// address a trusted proxy reports counts, one by one.
func TestForgedForwardedForIsIgnoredWithoutTrustedProxies(t *testing.T) {
	s, root := install(t, nil)
	root.member("patron", "pat")
	v := newWho(t, s)
	for i := 0; i < 5; i++ {
		v.xff = fmt.Sprintf("203.0.113.%d", i+1)
		v.want(401, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": "wrong password!"})
	}
	v.xff = "192.0.2.200" // a fresh forged address, as the "other visitor"
	v.want(429, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": adminPassword})
	// the same limit applies to registration
	for i := 0; i < 10; i++ {
		v.xff = fmt.Sprintf("203.0.113.%d", 100+i)
		v.do("POST", "/api/register", map[string]string{"code": "NOPE-NOPE", "username": "x", "display_name": "x", "password": adminPassword})
	}
	v.xff = "192.0.2.201"
	v.want(429, "POST", "/api/register", map[string]string{"code": "NOPE-NOPE", "username": "x", "display_name": "x", "password": adminPassword})
}

func TestForwardedForCountsPerAddressBehindATrustedProxy(t *testing.T) {
	s, root := install(t, map[string]string{"JL_TRUSTED_PROXIES": "127.0.0.1/32"})
	root.member("patron", "pat")
	v := newWho(t, s)
	// four different reported visitors, each failing 4 times: nobody is blocked
	for a := 1; a <= 4; a++ {
		v.xff = fmt.Sprintf("203.0.113.%d", a)
		for i := 0; i < 4; i++ {
			v.want(401, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": "wrong password!"})
		}
	}
	// a visitor the proxy reports as 203.0.113.1 reaches the fifth failure, then is blocked
	v.xff = "203.0.113.1"
	v.want(401, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": "wrong password!"})
	v.want(429, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": adminPassword})
	// the others are still free
	v.xff = "203.0.113.2"
	v.mustSignIn("pat", adminPassword)
	// a client-supplied chain: the right-most entry the proxy appended is the visitor, a forged left part buys nothing
	f := newWho(t, s)
	for i := 0; i < 5; i++ {
		f.xff = fmt.Sprintf("10.9.9.%d, 203.0.113.77", i)
		f.want(401, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": "wrong password!"})
	}
	f.xff = "10.1.1.1, 203.0.113.77"
	f.want(429, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": adminPassword})
}

// SEC-T1 "Session cookie": HttpOnly, Secure, SameSite, host-only, no Domain,
// and no cookie on a failed sign-in. Sign-out clears it and kills the session.
func TestSessionCookieFlags(t *testing.T) {
	s, root := install(t, nil)
	_, code := root.invite("patron")
	var tokens []string
	check := func(what string, r reply) {
		t.Helper()
		var found string
		for _, c := range r.Header.Values("Set-Cookie") {
			if strings.Contains(c, "jl_session") {
				found = c
			}
		}
		if found == "" {
			t.Fatalf("%s: no session cookie in %v", what, r.Header.Values("Set-Cookie"))
		}
		tokens = append(tokens, strings.SplitN(strings.SplitN(found, "=", 2)[1], ";", 2)[0])
		low := strings.ToLower(found)
		if !strings.HasPrefix(found, "__Host-jl_session=") {
			t.Errorf("%s: cookie name without the __Host- prefix: %s", what, found)
		}
		for _, want := range []string{"httponly", "secure", "samesite=lax", "path=/"} {
			if !strings.Contains(low, want) {
				t.Errorf("%s: cookie lacks %s: %s", what, want, found)
			}
		}
		if strings.Contains(low, "domain=") {
			t.Errorf("%s: cookie has a Domain: %s", what, found)
		}
	}
	reg := newWho(t, s)
	check("register", reg.want(201, "POST", "/api/register", map[string]string{"code": code, "username": "pat", "display_name": "P", "password": adminPassword}))
	in := newWho(t, s)
	check("sign-in", in.want(200, "POST", "/api/auth/signin", map[string]string{"username": "pat", "password": adminPassword}))
	if len(tokens) != 2 || tokens[0] == "" || tokens[0] == tokens[1] || len(tokens[0]) < 32 {
		t.Errorf("sessions tokens should be long and differ per sign-in: %q", tokens)
	}
	if r := newWho(t, s).signIn("pat", "wrong password!"); len(r.Header.Values("Set-Cookie")) != 0 {
		t.Errorf("failed sign-in sets cookies: %v", r.Header.Values("Set-Cookie"))
	}
	// the token is long and unguessable: two sign-ins differ, and a made-up token is nothing
	forged := newWho(t, s)
	req, _ := http.NewRequest("GET", s.URL+"/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-jl_session", Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	resp, err := forged.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("forged token: %d", resp.StatusCode)
	}
	// sign-out
	out := in.want(204, "POST", "/api/auth/signout", nil)
	if c := strings.Join(out.Header.Values("Set-Cookie"), ";"); !strings.Contains(c, "Max-Age=0") && !strings.Contains(c, "Max-Age=-1") && !strings.Contains(strings.ToLower(c), "expires=") {
		t.Errorf("sign-out does not clear the cookie: %s", c)
	}
	in.want(401, "GET", "/api/me", nil)
	// the other session of the same user is untouched by it
	reg.want(200, "GET", "/api/me", nil)
}

// SEC-T1: signing in does not adopt a token the browser already holds (no session fixation).
func TestSignInDoesNotAdoptAPresentedToken(t *testing.T) {
	s, root := install(t, nil)
	root.member("patron", "pat")
	root.want(200, "GET", "/api/me", nil)
	// a browser holding root's session signs in as pat: root's old token must die, pat's new one live
	var oldToken string
	if u, err := url.Parse(s.URL); err == nil {
		for _, c := range root.c.Jar.Cookies(u) {
			if strings.HasSuffix(c.Name, "jl_session") {
				oldToken = c.Value
			}
		}
	}
	if oldToken == "" {
		t.Fatal("root's session cookie not found in its jar")
	}
	jar := root.c
	r := (&who{t: t, s: s, c: jar}).signIn("pat", adminPassword)
	if r.Status != 200 {
		t.Fatalf("sign-in: %d %s", r.Status, r.Raw)
	}
	me := root.want(200, "GET", "/api/me", nil)
	if u, _ := me.JSON["user"].(map[string]any); u["username"] != "pat" {
		t.Fatalf("the browser should now be pat, got %s", me.Raw)
	}
	// replayed from a separate client, root's old token is dead
	req, _ := http.NewRequest("GET", s.URL+"/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-jl_session", Value: oldToken})
	resp, err := newWho(t, s).c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("the old token still works after the browser signed in as someone else: %d", resp.StatusCode)
	}
}

// ---- ACCESS-2 / ACCESS-3 ---------------------------------------------------

// ACCESS-2 "One invitation, two registrations": racing registrations with one
// code give exactly one account; the code is then used and refused.
func TestOneInvitationCannotMakeTwoAccounts(t *testing.T) {
	s, root := install(t, nil)
	_, code := root.invite("patron")
	const n = 6
	var wg sync.WaitGroup
	statuses := make([]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := newWho(t, s)
			<-start
			statuses[i] = w.do("POST", "/api/register", map[string]string{
				"code": code, "username": fmt.Sprintf("racer%d", i), "display_name": "R", "password": adminPassword}).Status
		}(i)
	}
	close(start)
	wg.Wait()
	created := 0
	for i, st := range statuses {
		switch st {
		case 201:
			created++
		case 403, 404, 409, 422:
		default:
			t.Errorf("racer %d: unexpected status %d (all %v)", i, st, statuses)
		}
	}
	if created != 1 {
		t.Fatalf("%d accounts from one invitation: %v", created, statuses)
	}
	users := root.want(200, "GET", "/api/admin/users", nil)
	if users.JSON["total"].(float64) != 2 {
		t.Fatalf("expected root and one registrant: %s", users.Raw)
	}
	if r := newWho(t, s).do("POST", "/api/register", map[string]string{"code": code, "username": "late", "display_name": "L", "password": adminPassword}); r.Status == 201 {
		t.Fatalf("a used code registered again %v", r.Raw)
	}
}

// otherThan returns a character that differs from c however the case is compared.
func otherThan(c byte) string {
	if c == '0' {
		return "1"
	}
	return "0"
}

// ACCESS-2 "Invalid code": revoked, used, unknown and malformed codes make no account.
func TestRevokedAndUnknownCodesMakeNoAccount(t *testing.T) {
	s, root := install(t, nil)
	id, code := root.invite("patron")
	root.want(200, "POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", id), nil)
	v := newWho(t, s)
	for _, c := range []string{code, "", "x", strings.Repeat("A", 5000), "' OR '1'='1", strings.ToLower(code) + "x"} {
		if r := v.do("POST", "/api/register", map[string]string{"code": c, "username": "ghost", "display_name": "G", "password": adminPassword}); r.Status == 201 || r.Status >= 500 {
			t.Errorf("code %.20q: %d %s", c, r.Status, r.Raw)
		}
	}
	if got := root.want(200, "GET", "/api/admin/users", nil).JSON["total"].(float64); got != 1 {
		t.Fatalf("an invalid code created an account: %v users", got)
	}
}

// ACCESS-3 "No escalation": the invitation's role is what the registrant gets,
// whatever the form claims; nobody can invite a role above their own permissions.
func TestForgedRoleAndNoEscalation(t *testing.T) {
	s, root := install(t, nil)
	_, code := root.invite("patron")
	v := newWho(t, s)
	forged := map[string]any{
		"code": code, "username": "sneaky", "display_name": "S", "password": adminPassword,
		"role": "administrator", "permissions": []string{"users.manage", "users.permissions"}, "id": 1, "active": true}
	// the form's extra fields are refused outright, or at least ignored: never obeyed
	if r := v.do("POST", "/api/register", forged); r.Status == 201 {
		if u, _ := r.JSON["user"].(map[string]any); u["role"] != "patron" || perms(r)["users.manage"] || perms(r)["settings.manage"] {
			t.Fatalf("forged role took effect: %s", r.Raw)
		}
	} else if r.Status != 400 && r.Status != 422 {
		t.Fatalf("forged registration: %d %s", r.Status, r.Raw)
	} else if n := root.want(200, "GET", "/api/admin/users", nil).JSON["total"].(float64); n != 1 {
		t.Fatalf("a refused forged registration made an account: %v users", n)
	}
	// the refusal did not burn the invitation: a plain registration with it gives a patron
	if r := newWho(t, s).do("POST", "/api/register", map[string]string{"code": code, "username": "sneaky", "display_name": "S", "password": adminPassword}); r.Status == 201 {
		if u, _ := r.JSON["user"].(map[string]any); u["role"] != "patron" || perms(r)["users.manage"] {
			t.Fatalf("patron invitation gave %s", r.Raw)
		}
	}
	v = newWho(t, s)
	v.mustSignIn("sneaky", adminPassword)
	v.want(403, "GET", "/api/admin/users", nil)
	v.want(403, "POST", "/api/admin/invitations", map[string]string{"role": "administrator"})

	// a librarian given invitations.manage still can't make an administrator or invite above themself
	libID, lib := root.member("librarian", "lib")
	lib.want(403, "POST", "/api/admin/invitations", map[string]string{"role": "patron"}) // preset lacks it
	root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/invitations.manage", libID), map[string]bool{"on": true})
	lib.want(201, "POST", "/api/admin/invitations", map[string]string{"role": "patron"})
	for _, role := range []string{"administrator", "root", ""} {
		if r := lib.do("POST", "/api/admin/invitations", map[string]string{"role": role}); r.Status != 403 && r.Status != 422 {
			t.Errorf("librarian inviting %q: %d %s", role, r.Status, r.Raw)
		}
	}
	// a librarian cannot grant themselves anything
	lib.want(403, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/users.manage", libID), map[string]bool{"on": true})
	lib.want(403, "PUT", fmt.Sprintf("/api/admin/users/%d/role", libID), map[string]string{"role": "administrator"})
	// a patron cannot do any of it either
	_, pat := root.member("patron", "pat")
	pat.want(403, "PUT", fmt.Sprintf("/api/admin/users/%d/role", libID), map[string]string{"role": "patron"})
	pat.want(403, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", libID), nil)
	pat.want(403, "POST", fmt.Sprintf("/api/admin/users/%d/reset-password", 1), nil)
	if got := root.want(200, "GET", "/api/admin/users", nil); !strings.Contains(got.Raw, `"username":"lib"`) {
		t.Fatalf("lib vanished: %s", got.Raw)
	}
}

// ---- ADMIN-2 ---------------------------------------------------------------

func activeManagers(t *testing.T, w *who) int {
	t.Helper()
	r := w.want(200, "GET", "/api/admin/users?per_page=100", nil)
	n := 0
	for _, u := range r.JSON["users"].([]any) {
		m := u.(map[string]any)
		if m["active"] != true {
			continue
		}
		has := map[string]bool{}
		for _, p := range m["permissions"].([]any) {
			has[p.(string)] = true
		}
		if has["users.manage"] && has["users.permissions"] {
			n++
		}
	}
	return n
}

// ADMIN-2 "Last administrator": the only administrator can't be deactivated,
// demoted, or stripped of either permission, by themselves, and nothing changes.
func TestLastAdministratorIsProtectedOnEveryPath(t *testing.T) {
	_, root := install(t, nil)
	paths := []struct {
		name, method, path string
		body               any
	}{
		{"deactivate", "POST", "/api/admin/users/1/deactivate", nil},
		{"demote", "PUT", "/api/admin/users/1/role", map[string]string{"role": "librarian"}},
		{"demote to patron", "PUT", "/api/admin/users/1/role", map[string]string{"role": "patron"}},
		{"switch off users.manage", "PUT", "/api/admin/users/1/permissions/users.manage", map[string]bool{"on": false}},
		{"switch off users.permissions", "PUT", "/api/admin/users/1/permissions/users.permissions", map[string]bool{"on": false}},
	}
	for _, p := range paths {
		if r := root.do(p.method, p.path, p.body); r.Status != 409 {
			t.Errorf("%s: want 409, got %d %s", p.name, r.Status, r.Raw)
		}
	}
	if n := activeManagers(t, root); n != 1 {
		t.Fatalf("%d active managers after refusals", n)
	}
	root.want(200, "GET", "/api/me", nil) // still signed in
	// a second administrator makes the same actions possible, and then the rule applies to the remaining one
	id, second := root.member("administrator", "second")
	root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", id), nil)
	second.want(401, "GET", "/api/me", nil)
	root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/reactivate", id), nil)
	root.want(200, "PUT", "/api/admin/users/1/role", map[string]string{"role": "librarian"})
	// root is now a librarian: the remaining administrator can't be removed by anyone
	third := newWho(t, root.s)
	third.mustSignIn("second", adminPassword)
	third.want(409, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", id), nil)
	third.want(409, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/users.manage", id), map[string]bool{"on": false})
	if n := activeManagers(t, third); n != 1 {
		t.Fatalf("%d managers", n)
	}
}

// ADMIN-2 "Two administrators removing each other at once": whatever the
// interleaving, one is always left, over many rounds and over every path.
func TestTwoAdministratorsRemovingEachOtherLeaveOne(t *testing.T) {
	s, root := install(t, nil)
	otherID, other := root.member("administrator", "other")
	type action func(w *who, target int64) int
	actions := map[string]action{
		"deactivate": func(w *who, target int64) int {
			return w.do("POST", fmt.Sprintf("/api/admin/users/%d/deactivate", target), nil).Status
		},
		"demote": func(w *who, target int64) int {
			return w.do("PUT", fmt.Sprintf("/api/admin/users/%d/role", target), map[string]string{"role": "patron"}).Status
		},
		"switch off users.manage": func(w *who, target int64) int {
			return w.do("PUT", fmt.Sprintf("/api/admin/users/%d/permissions/users.manage", target), map[string]bool{"on": false}).Status
		},
		"switch off users.permissions": func(w *who, target int64) int {
			return w.do("PUT", fmt.Sprintf("/api/admin/users/%d/permissions/users.permissions", target), map[string]bool{"on": false}).Status
		},
	}
	for name, act := range actions {
		for round := 0; round < 8; round++ {
			var wg sync.WaitGroup
			var a, b int
			start := make(chan struct{})
			wg.Add(2)
			go func() { defer wg.Done(); <-start; a = act(root, otherID) }()
			go func() { defer wg.Done(); <-start; b = act(other, 1) }()
			close(start)
			wg.Wait()
			if a >= 500 || b >= 500 {
				t.Fatalf("%s round %d: server error %d/%d", name, round, a, b)
			}
			// who is left? Whoever still holds both administrator permissions.
			var left *who
			for _, u := range []struct{ n string }{{"root"}, {"other"}} {
				w := newWho(t, s)
				if r := w.signIn(u.n, adminPassword); r.Status == 200 && perms(r)["users.manage"] && perms(r)["users.permissions"] {
					left = w
				}
			}
			if left == nil {
				t.Fatalf("%s round %d: nobody can manage users any more (statuses %d, %d)", name, round, a, b)
			}
			if n := activeManagers(t, left); n < 1 {
				t.Fatalf("%s round %d: no active manager (statuses %d, %d)", name, round, a, b)
			}
			if a == 200 && b == 200 {
				t.Fatalf("%s round %d: both removals succeeded", name, round)
			}
			// restore both as administrators and active for the next round
			for _, id := range []int64{1, otherID} {
				left.do("POST", fmt.Sprintf("/api/admin/users/%d/reactivate", id), nil)
				left.do("PUT", fmt.Sprintf("/api/admin/users/%d/role", id), map[string]string{"role": "administrator"})
				left.do("PUT", fmt.Sprintf("/api/admin/users/%d/permissions/users.manage", id), map[string]bool{"on": true})
				left.do("PUT", fmt.Sprintf("/api/admin/users/%d/permissions/users.permissions", id), map[string]bool{"on": true})
			}
			root = newWho(t, s)
			root.mustSignIn("root", adminPassword)
			other = newWho(t, s)
			other.mustSignIn("other", adminPassword)
			if activeManagers(t, root) != 2 {
				t.Fatalf("%s round %d: restore failed", name, round)
			}
		}
	}
}

// ADMIN-2 "Deactivation": the user's live sessions end at once and they can't
// sign in; reactivation lets them back in. A change of permissions applies to
// the next request without a new sign-in.
func TestDeactivationEndsLiveSessionAtOnce(t *testing.T) {
	s, root := install(t, nil)
	id, pat := root.member("patron", "pat")
	pat2 := newWho(t, s)
	pat2.mustSignIn("pat", adminPassword)
	pat.want(200, "GET", "/api/books", nil)
	root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", id), nil)
	pat.want(401, "GET", "/api/me", nil)
	pat2.want(401, "GET", "/api/me", nil)
	if r := newWho(t, s).signIn("pat", adminPassword); r.Status == 200 {
		t.Fatal("a deactivated user signed in")
	}
	root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/reactivate", id), nil)
	pat.want(401, "GET", "/api/me", nil) // reactivation does not revive the ended sessions
	back := newWho(t, s)
	back.mustSignIn("pat", adminPassword)
	back.want(200, "GET", "/api/books", nil)
	// permission switched off mid-session applies to the very next request
	root.want(200, "PUT", fmt.Sprintf("/api/admin/users/%d/permissions/catalog.browse", id), map[string]bool{"on": false})
	if r := back.do("GET", "/api/books", nil); r.Status != 403 && r.Status != 401 {
		t.Fatalf("catalog.browse switched off, still served: %d", r.Status)
	}
	// the override shows as different from the preset
	list := root.want(200, "GET", "/api/admin/users", nil)
	for _, u := range list.JSON["users"].([]any) {
		m := u.(map[string]any)
		if m["username"] == "pat" {
			d := m["from_preset"].(map[string]any)
			if d["differs"] != true || !strings.Contains(fmt.Sprint(d["removed"]), "catalog.browse") {
				t.Fatalf("override not shown as differing: %v", d)
			}
		}
	}
}

// ADMIN-2: refused administration is refused, not half done: unknown users and
// permissions, malformed ids and bodies change nothing and give no server error.
func TestUserAdministrationRefusesBadInput(t *testing.T) {
	_, root := install(t, nil)
	id, _ := root.member("patron", "pat")
	cases := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/admin/users/999/deactivate", nil},
		{"POST", "/api/admin/users/abc/deactivate", nil},
		{"POST", "/api/admin/users/-1/reset-password", nil},
		{"POST", "/api/admin/users/99999999999999999999/deactivate", nil},
		{"PUT", fmt.Sprintf("/api/admin/users/%d/role", id), map[string]string{"role": "superuser"}},
		{"PUT", fmt.Sprintf("/api/admin/users/%d/role", id), map[string]string{}},
		{"PUT", fmt.Sprintf("/api/admin/users/%d/permissions/not.a.permission", id), map[string]bool{"on": true}},
		{"PUT", fmt.Sprintf("/api/admin/users/%d/permissions/catalog.browse", id), map[string]string{}},
	}
	for _, c := range cases {
		if r := root.do(c.method, c.path, c.body); r.Status < 400 || r.Status >= 500 {
			t.Errorf("%s %s: want a 4xx, got %d %s", c.method, c.path, r.Status, r.Raw)
		}
	}
	list := root.want(200, "GET", "/api/admin/users", nil)
	for _, u := range list.JSON["users"].([]any) {
		m := u.(map[string]any)
		if m["username"] == "pat" && (m["active"] != true || m["role"] != "patron") {
			t.Fatalf("a refused request changed the user: %v", m)
		}
	}
}

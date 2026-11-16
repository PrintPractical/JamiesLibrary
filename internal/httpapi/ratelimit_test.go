package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/ratelimit"
)

func signInBody(name, password string) string {
	return `{"username":"` + name + `","password":"` + password + `"}`
}

// failSignIns makes n wrong sign-ins as the site's current remote address.
func (s *site) failSignIns(n int, hdr ...string) {
	s.t.Helper()
	for i := 0; i < n; i++ {
		if rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", "wrong password!"), hdr...); rec.Code != 401 {
			s.t.Fatalf("wrong sign-in %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
}

// SEC-T1 "Repeated failed sign-ins": after 5 failures from one address even the right password is refused for 15 minutes.
func TestRepeatedFailedSignIns(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	s.failSignIns(5)
	rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword))
	if rec.Code != 429 || sessionCookie(rec) != nil || !strings.Contains(errMessage(t, rec), "Too many attempts") {
		t.Fatalf("right password while blocked: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Retry-After") != "900" {
		t.Fatalf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	s.now = s.now.Add(14*time.Minute + 59*time.Second)
	if rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword)); rec.Code != 429 {
		t.Fatalf("a second before the end of the block: %d", rec.Code)
	}
	s.now = s.now.Add(2 * time.Second)
	s.signIn("ann", goodPassword)
}

// The block is refused before any password work: a blocked attempt leaves no failure in the log.
func TestBlockedSignInDoesNoPasswordWork(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	s.failSignIns(5)
	before := strings.Count(s.logs.String(), "sign-in failed")
	for i := 0; i < 3; i++ {
		s.req("POST", "/api/auth/signin", "", signInBody("ann", "another wrong one"))
	}
	if after := strings.Count(s.logs.String(), "sign-in failed"); after != before {
		t.Fatalf("a blocked attempt was verified: %d failures logged before, %d after", before, after)
	}
}

func TestSuccessfulSignInResetsTheCount(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	for round := 0; round < 3; round++ {
		s.failSignIns(4)
		s.signIn("ann", goodPassword)
	}
}

// SEC-T1 "Other address unaffected": the block is of the address, never the account.
func TestOtherAddressUnaffected(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	s.remote = "198.51.100.7:4000"
	s.failSignIns(5)
	s.remote = "203.0.113.9:5000"
	s.signIn("ann", goodPassword)
	s.remote = "198.51.100.7:4001" // same address, another port
	if rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword)); rec.Code != 429 {
		t.Fatalf("%d", rec.Code)
	}
}

// SEC-T1 "Forged address": without a trusted proxy, a header never changes who the client is.
func TestForgedAddress(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	forged := []string{"X-Forwarded-For", "203.0.113.1", "X-Real-IP", "203.0.113.2", "Forwarded", "for=203.0.113.3"}
	s.failSignIns(5, forged...)
	for i := 0; i < 4; i++ { // a new address in the header each time doesn't escape the block
		rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword),
			"X-Forwarded-For", "203.0.113."+string(rune('4'+i)))
		if rec.Code != 429 {
			t.Fatalf("a forged address got through: %d", rec.Code)
		}
	}
}

// A trusted proxy's X-Forwarded-For is believed only when the connection is the proxy.
func TestForgedAddressFromAnUntrustedPeer(t *testing.T) {
	s := newSite(t, func(o *Options) {
		trusted, _ := ratelimit.ParseTrusted("10.0.0.0/8")
		o.Clients = ratelimit.NewClients(trusted)
	})
	s.addUser("ann", goodPassword, perm.Patron)
	s.remote = "198.51.100.7:4000" // not the proxy
	s.failSignIns(5, "X-Forwarded-For", "203.0.113.50")
	if rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword), "X-Forwarded-For", "203.0.113.51"); rec.Code != 429 {
		t.Fatalf("%d", rec.Code)
	}
}

// SEC-T1 "Behind a configured proxy": visitors are told apart by the address the proxy reports.
func TestBehindAConfiguredProxy(t *testing.T) {
	s := newSite(t, func(o *Options) {
		trusted, err := ratelimit.ParseTrusted("10.0.0.0/8")
		if err != nil {
			t.Fatal(err)
		}
		o.Clients = ratelimit.NewClients(trusted)
	})
	s.addUser("ann", goodPassword, perm.Patron)
	s.remote = "10.1.2.3:5555"
	// The visitor puts their own entries first; the proxy appends the real address.
	s.failSignIns(5, "X-Forwarded-For", "1.1.1.1, 203.0.113.50")
	rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword), "X-Forwarded-For", "2.2.2.2, 203.0.113.50")
	if rec.Code != 429 {
		t.Fatalf("the visitor the proxy reported was not blocked: %d", rec.Code)
	}
	if rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword), "X-Forwarded-For", "203.0.113.50, 203.0.113.60"); rec.Code != 200 {
		t.Fatalf("another visitor behind the same proxy: %d %s", rec.Code, rec.Body)
	}
}

// ACCESS-6 / security design §3: guessing the current password from a left-signed-in session is limited too.
func TestWrongCurrentPasswordsAreLimited(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	body := func(cur string) string { return `{"current_password":"` + cur + `","new_password":"twelve chars!"}` }
	for i := 0; i < 5; i++ {
		if rec := s.req("POST", "/api/me/password", tok, body("not the password")); rec.Code != 422 {
			t.Fatalf("wrong current %d: %d", i+1, rec.Code)
		}
	}
	rec := s.req("POST", "/api/me/password", tok, body(goodPassword))
	if rec.Code != 429 || !strings.Contains(errMessage(t, rec), "Too many attempts") {
		t.Fatalf("right current password while blocked: %d %s", rec.Code, rec.Body)
	}
	// The same block stops sign-in from that address.
	if rec := s.req("POST", "/api/auth/signin", "", signInBody("ann", goodPassword)); rec.Code != 429 {
		t.Fatalf("sign-in from a blocked address: %d", rec.Code)
	}
	s.remote = "203.0.113.9:1"
	if rec := s.req("POST", "/api/me/password", tok, body(goodPassword)); rec.Code != 204 {
		t.Fatalf("another address: %d %s", rec.Code, rec.Body)
	}
}

// Changes that go through, and refusals that aren't wrong passwords, don't count.
func TestPasswordChangesThatAreNotGuessesDoNotCount(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	for i := 0; i < 8; i++ {
		if rec := s.req("POST", "/api/me/password", tok, `{"current_password":"`+goodPassword+`","new_password":"abc"}`); rec.Code != 422 {
			t.Fatalf("too-short new password %d: %d", i, rec.Code)
		}
	}
	cur := goodPassword
	for i := 0; i < 8; i++ {
		next := strings.Repeat("n", 12) + string(rune('a'+i))
		rec := s.req("POST", "/api/me/password", tok, `{"current_password":"`+cur+`","new_password":"`+next+`"}`)
		if rec.Code != 204 {
			t.Fatalf("change %d: %d %s", i, rec.Code, rec.Body)
		}
		cur = next
	}
}

func TestMalformedSignInIsNotACountedFailure(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	for i := 0; i < 8; i++ {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/auth/signin", strings.NewReader("{not json"))
		r.Header.Set("Content-Type", "application/json")
		s.h.ServeHTTP(rec, r)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatal("malformed bodies were counted")
		}
	}
	s.signIn("ann", goodPassword)
}

// Signing in as yourself between guesses at someone else's password doesn't clear the count.
func TestOwnSignInDoesNotSwitchTheLimitOff(t *testing.T) {
	s := newSite(t)
	s.addUser("admin", goodPassword, perm.Administrator)
	s.addUser("mallory", goodPassword, perm.Patron)
	guess := func(n int) {
		for i := 0; i < n; i++ {
			if rec := s.req("POST", "/api/auth/signin", "", signInBody("Admin", "guess guess guess")); rec.Code != 401 {
				t.Fatalf("guess at admin: %d %s", rec.Code, rec.Body)
			}
		}
	}
	guess(4)
	s.signIn("mallory", goodPassword)
	guess(1) // the 5th wrong guess at admin starts the block
	if rec := s.req("POST", "/api/auth/signin", "", signInBody("admin", "guess guess guess")); rec.Code != 429 {
		t.Fatalf("after 5 guesses with a sign-in between: %d", rec.Code)
	}
}

func TestOwnPasswordChangeFailuresAreResetOnlyByTheirOwnSignIn(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	s.addUser("mallory", goodPassword, perm.Patron)
	tok := s.signIn("ann", goodPassword)
	wrong := func() *httptest.ResponseRecorder {
		return s.req("POST", "/api/me/password", tok, `{"current_password":"not the password","new_password":"twelve chars!"}`)
	}
	for i := 0; i < 4; i++ {
		if rec := wrong(); rec.Code != 422 {
			t.Fatalf("%d", rec.Code)
		}
	}
	s.signIn("mallory", goodPassword) // another account from the same address doesn't clear it
	if rec := wrong(); rec.Code != 422 {
		t.Fatalf("5th wrong current password: %d", rec.Code)
	}
	if rec := wrong(); rec.Code != 429 {
		t.Fatalf("the block did not start: %d", rec.Code)
	}
}

// Security design §12: a failed sign-in is logged with the client's address, not the proxy's.
func TestFailedSignInLogsTheClientBehindAProxy(t *testing.T) {
	s := newSite(t, func(o *Options) {
		trusted, _ := ratelimit.ParseTrusted("10.0.0.0/8")
		o.Clients = ratelimit.NewClients(trusted)
	})
	s.addUser("ann", goodPassword, perm.Patron)
	s.remote = "10.1.2.3:5555"
	s.logs.Reset()
	s.req("POST", "/api/auth/signin", "", signInBody("ann", "wrong password"), "X-Forwarded-For", "1.1.1.1, 203.0.113.50")
	out := s.logs.String()
	if !strings.Contains(out, "sign-in failed") || !strings.Contains(out, "remote=203.0.113.50") {
		t.Fatalf("log does not carry the client address: %s", out)
	}
	if strings.Contains(out, `msg="sign-in failed" remote=10.1.2.3`) {
		t.Fatalf("logged the proxy: %s", out)
	}
}

// The per-request access line carries the client's address behind a trusted proxy.
func TestAccessLineLogsTheClientBehindAProxy(t *testing.T) {
	s := newSite(t, func(o *Options) {
		trusted, _ := ratelimit.ParseTrusted("10.0.0.0/8")
		o.Clients = ratelimit.NewClients(trusted)
	})
	s.remote = "10.1.2.3:5555"
	s.logs.Reset()
	s.req("GET", "/api/health", "", "", "X-Forwarded-For", "1.1.1.1, 203.0.113.50")
	out := s.logs.String()
	if !strings.Contains(out, "msg=request") || !strings.Contains(out, "remote=203.0.113.50") || strings.Contains(out, "remote=10.1.2.3") {
		t.Fatalf("access line does not carry the client address: %s", out)
	}
}

// With no trusted proxy a forged X-Forwarded-For is ignored in the access line.
func TestAccessLineIgnoresForgedHeaderWithoutProxy(t *testing.T) {
	s := newSite(t, func(*Options) {})
	s.remote = "198.51.100.7:5555"
	s.logs.Reset()
	s.req("GET", "/api/health", "", "", "X-Forwarded-For", "6.6.6.6")
	out := s.logs.String()
	if !strings.Contains(out, "remote=198.51.100.7") || strings.Contains(out, "6.6.6.6") {
		t.Fatalf("forged header reached the access line: %s", out)
	}
}

package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

const wrongCode = "0000-0000-0000-0000"

// postFrom posts JSON as a visitor the server sees as xff (the header is only
// believed from a trusted proxy) and returns the status and body.
func postFrom(t *testing.T, c *http.Client, url, xff string, body any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func registerBody(code, username string) map[string]string {
	return map[string]string{"code": code, "username": username, "display_name": "New " + username, "password": adminPassword}
}

func guessRegister(t *testing.T, s *testutil.Server, c *http.Client, xff string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if st, body := postFrom(t, c, s.URL+"/api/register", xff, registerBody(wrongCode, "guess")); st != 422 {
			t.Fatalf("register guess %d: %d %s", i+1, st, body)
		}
	}
}

func guessSetup(t *testing.T, s *testutil.Server, c *http.Client, xff string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if st, body := postFrom(t, c, s.URL+"/api/setup/admin", xff, setupBody(wrongCode, "guess")); st != 403 {
			t.Fatalf("setup guess %d: %d %s", i+1, st, body)
		}
	}
}

func wantBlocked(t *testing.T, st int, body string) {
	t.Helper()
	if st != 429 || !strings.Contains(body, "try again later") {
		t.Fatalf("want 429 try again later, got %d %s", st, body)
	}
}

// SEC-T1 "Guessing invitation codes": wrong codes sent to setup and to
// registration are one count, so ten between them block both for that
// address, a valid setup code included, and the code is still unused afterwards.
func TestSetupAndRegistrationShareOneCount(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, _ := s.SetupCode()
	c := s.Client()
	guessSetup(t, s, c, "", 5)
	guessRegister(t, s, c, "", 5)
	st, body := postFrom(t, c, s.URL+"/api/setup/admin", "", setupBody(code, "jamie"))
	wantBlocked(t, st, body)
	if st, body := postFrom(t, c, s.URL+"/api/register", "", registerBody(wrongCode, "x")); st != 429 {
		t.Fatalf("register while blocked: %d %s", st, body)
	}
	if n := countUsers(t, s.DataDir); n != 0 {
		t.Fatalf("%d users made while blocked", n)
	}
	if err := s.Advance(15*time.Minute + time.Second); err != nil {
		t.Fatal(err)
	}
	if st, body := postFrom(t, c, s.URL+"/api/setup/admin", "", setupBody(code, "jamie")); st != 200 {
		t.Fatalf("the code was spent by the refusal: %d %s", st, body)
	}
}

// Used and revoked codes count like wrong ones, and a valid code that is
// refused while blocked is not consumed.
func TestUsedAndRevokedCodesCountAndABlockedCodeIsKept(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	setup, _ := s.SetupCode()
	owner := s.Client()
	if st, body := postFrom(t, owner, s.URL+"/api/setup/admin", "", setupBody(setup, "jamie")); st != 200 {
		t.Fatalf("setup: %d %s", st, body)
	}
	invite := func() (int64, string) {
		st, body := postFrom(t, owner, s.URL+"/api/admin/invitations", "", map[string]string{"role": "patron"})
		var inv struct {
			ID   int64
			Code string
		}
		if st != 201 || json.Unmarshal([]byte(body), &inv) != nil {
			t.Fatalf("invite: %d %s", st, body)
		}
		return inv.ID, inv.Code
	}
	revokedID, revoked := invite()
	_, used := invite()
	_, spare := invite()
	if st, body := postFrom(t, owner, fmt.Sprintf("%s/api/admin/invitations/%d/revoke", s.URL, revokedID), "", struct{}{}); st != 200 {
		t.Fatalf("revoke: %d %s", st, body)
	}
	v := s.Client()
	if st, body := postFrom(t, v, s.URL+"/api/register", "", registerBody(used, "first")); st != 201 {
		t.Fatalf("first registration: %d %s", st, body)
	}
	for i := 0; i < 5; i++ {
		for _, code := range []string{revoked, used} {
			if st, body := postFrom(t, v, s.URL+"/api/register", "", registerBody(code, "again")); st != 422 {
				t.Fatalf("%q: %d %s", code, st, body)
			}
		}
	}
	st, body := postFrom(t, v, s.URL+"/api/register", "", registerBody(spare, "second"))
	wantBlocked(t, st, body)
	if err := s.Advance(15*time.Minute + time.Second); err != nil {
		t.Fatal(err)
	}
	if st, body := postFrom(t, v, s.URL+"/api/register", "", registerBody(spare, "second")); st != 201 {
		t.Fatalf("the blocked code was consumed: %d %s", st, body)
	}
}

// Behind a trusted proxy two real addresses are counted apart.
func TestRegistrationLimitBehindAProxy(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{Env: map[string]string{"JL_TRUSTED_PROXIES": "127.0.0.0/8, ::1/128"}})
	c := s.Client()
	guessRegister(t, s, c, "1.1.1.1, 203.0.113.1", 10)
	st, body := postFrom(t, c, s.URL+"/api/register", "2.2.2.2, 203.0.113.1", registerBody(wrongCode, "x"))
	wantBlocked(t, st, body)
	if st, body := postFrom(t, c, s.URL+"/api/register", "203.0.113.1, 203.0.113.2", registerBody(wrongCode, "x")); st != 422 {
		t.Fatalf("another visitor behind the proxy: %d %s", st, body)
	}
	// the setup route sees the same counts
	code, _ := s.SetupCode()
	st, body = postFrom(t, c, s.URL+"/api/setup/admin", "9.9.9.9, 203.0.113.1", setupBody(code, "jamie"))
	wantBlocked(t, st, body)
}

// Without a trusted proxy a forged X-Forwarded-For counts against the connection's address.
func TestRegistrationLimitIgnoresAForgedAddress(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	c := s.Client()
	for i := 0; i < 10; i++ {
		st, body := postFrom(t, c, s.URL+"/api/register", fmt.Sprintf("203.0.113.%d", i+1), registerBody(wrongCode, "x"))
		if st != 422 {
			t.Fatalf("guess %d: %d %s", i+1, st, body)
		}
	}
	st, body := postFrom(t, c, s.URL+"/api/register", "198.51.100.77", registerBody(wrongCode, "x"))
	wantBlocked(t, st, body)
}

// A valid registration takes back only its own attempt: the wrong codes before
// it stay counted, so one more wrong code after nine makes ten.
func TestAValidCodeNeverResetsTheCount(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	setup, _ := s.SetupCode()
	owner := s.Client()
	if st, body := postFrom(t, owner, s.URL+"/api/setup/admin", "", setupBody(setup, "jamie")); st != 200 {
		t.Fatalf("setup: %d %s", st, body)
	}
	invite := func() string {
		st, body := postFrom(t, owner, s.URL+"/api/admin/invitations", "", map[string]string{"role": "patron"})
		var inv struct{ Code string }
		if st != 201 || json.Unmarshal([]byte(body), &inv) != nil {
			t.Fatalf("invite: %d %s", st, body)
		}
		return inv.Code
	}
	first, second := invite(), invite()
	v := s.Client()
	guessRegister(t, s, v, "", 9)
	if st, body := postFrom(t, v, s.URL+"/api/register", "", registerBody(first, "first")); st != 201 {
		t.Fatalf("valid registration at nine failures: %d %s", st, body)
	}
	guessRegister(t, s, v, "", 1)
	st, body := postFrom(t, v, s.URL+"/api/register", "", registerBody(second, "second"))
	wantBlocked(t, st, body)
}

// A code whose creator has since been deactivated is no longer valid, and
// counts like any other wrong code.
func TestACodeOfADeactivatedCreatorCounts(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	setup, _ := s.SetupCode()
	owner := s.Client()
	if st, body := postFrom(t, owner, s.URL+"/api/setup/admin", "", setupBody(setup, "jamie")); st != 200 {
		t.Fatalf("setup: %d %s", st, body)
	}
	invite := func(c *http.Client, role string) string {
		st, body := postFrom(t, c, s.URL+"/api/admin/invitations", "", map[string]string{"role": role})
		var inv struct{ Code string }
		if st != 201 || json.Unmarshal([]byte(body), &inv) != nil {
			t.Fatalf("invite: %d %s", st, body)
		}
		return inv.Code
	}
	helper := s.Client()
	st, body := postFrom(t, helper, s.URL+"/api/register", "", registerBody(invite(owner, "administrator"), "helper"))
	var reg struct {
		ID   int64
		User struct{ ID int64 }
	}
	if st != 201 || json.Unmarshal([]byte(body), &reg) != nil {
		t.Fatalf("helper registration: %d %s", st, body)
	}
	id := reg.ID
	if id == 0 {
		id = reg.User.ID
	}
	orphan := invite(helper, "patron")
	if st, body := postFrom(t, owner, fmt.Sprintf("%s/api/admin/users/%d/deactivate", s.URL, id), "", struct{}{}); st != 200 {
		t.Fatalf("deactivate: %d %s", st, body)
	}
	v := s.Client()
	for i := 0; i < 10; i++ {
		if st, body := postFrom(t, v, s.URL+"/api/register", "", registerBody(orphan, "late")); st != 422 {
			t.Fatalf("attempt %d with the orphaned code: %d %s", i+1, st, body)
		}
	}
	st, body = postFrom(t, v, s.URL+"/api/register", "", registerBody(wrongCode, "late"))
	wantBlocked(t, st, body)
}

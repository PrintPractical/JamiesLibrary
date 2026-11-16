package server_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"jamieslibrary/internal/store"
	"jamieslibrary/internal/testutil"
)

const adminPassword = "correct horse battery"

func postJSON(t *testing.T, c *http.Client, url string, body any) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, string(out)
}

func getStatus(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func setupBody(code, username string) map[string]string {
	return map[string]string{"code": code, "username": username, "display_name": "Jamie Owner", "password": adminPassword}
}

func countUsers(t *testing.T, dir string) int {
	t.Helper()
	st, err := store.Open(context.Background(), dir, store.Options{NoLock: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// DEPLOY-3 "Owner sets up": the code from the console creates the first
// administrator, who is signed in, has every permission and a card, and the
// code then no longer works.
func TestOwnerSetsUp(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatalf("no setup code on the console:\n%s", s.Stdout())
	}
	c := s.Client()
	if st, body := getStatus(t, c, s.URL+"/api/setup/status"); st != 200 || !strings.Contains(body, `"open":true`) {
		t.Fatalf("status while open: %d %s", st, body)
	}
	// typed by hand: lower case, spaces for dashes
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	resp, body := postJSON(t, c, s.URL+"/api/setup/admin", setupBody(typed, "jamie"))
	if resp.StatusCode != 200 {
		t.Fatalf("setup: %d %s", resp.StatusCode, body)
	}
	var me struct {
		User        struct{ Username, Role string }
		Permissions []string
	}
	if err := json.Unmarshal([]byte(body), &me); err != nil {
		t.Fatal(err)
	}
	if me.User.Username != "jamie" || me.User.Role != "administrator" || len(me.Permissions) != 16 {
		t.Fatalf("the new account: %s", body)
	}
	// signed in: the same client reaches /api/me and has a card
	if st, _ := getStatus(t, c, s.URL+"/api/me"); st != 200 {
		t.Fatalf("not signed in after setup: %d", st)
	}
	if st, b := getStatus(t, c, s.URL+"/api/me/card"); st != 200 || !strings.Contains(b, `"number"`) {
		t.Fatalf("no card: %d %s", st, b)
	}
	// the code no longer works, and the code never reached the log
	resp, _ = postJSON(t, s.Client(), s.URL+"/api/setup/admin", setupBody(code, "second"))
	if resp.StatusCode != 404 {
		t.Fatalf("the used code: %d", resp.StatusCode)
	}
	if strings.Contains(s.Log(), code) || strings.Contains(s.Log(), strings.ReplaceAll(code, "-", "")) {
		t.Fatal("the setup code was logged")
	}
	if n := countUsers(t, s.DataDir); n != 1 {
		t.Fatalf("%d users", n)
	}
}

// DEPLOY-3 "Visitor without the code": no code or a wrong one is refused, and no account is made.
func TestVisitorWithoutTheCode(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, _ := s.SetupCode()
	c := s.Client()
	for name, in := range map[string]map[string]string{
		"no code":    setupBody("", "mallory"),
		"wrong code": setupBody("0000-0000-0000-0000", "mallory"),
		"a prefix":   setupBody(code[:9], "mallory"),
	} {
		resp, body := postJSON(t, c, s.URL+"/api/setup/admin", in)
		if resp.StatusCode != 403 {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
		if len(resp.Cookies()) != 0 {
			t.Errorf("%s: got a cookie", name)
		}
	}
	if st, _ := getStatus(t, c, s.URL+"/api/me"); st != 401 {
		t.Fatalf("a visitor is signed in: %d", st)
	}
	if n := countUsers(t, s.DataDir); n != 0 {
		t.Fatalf("%d users were created", n)
	}
	// the right code still works afterwards: refusals didn't close setup
	if resp, body := postJSON(t, c, s.URL+"/api/setup/admin", setupBody(code, "jamie")); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// DEPLOY-3 "Setup closed": once an administrator exists, every setup route is a 404 and a restart prints no code.
func TestSetupClosed(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, _ := s.SetupCode()
	if resp, body := postJSON(t, s.Client(), s.URL+"/api/setup/admin", setupBody(code, "jamie")); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	check := func(s *testutil.Server) {
		t.Helper()
		c := s.Client()
		if st, _ := getStatus(t, c, s.URL+"/api/setup/status"); st != 404 {
			t.Errorf("status: %d", st)
		}
		if resp, _ := postJSON(t, c, s.URL+"/api/setup/admin", setupBody(code, "again")); resp.StatusCode != 404 {
			t.Errorf("create: %d", resp.StatusCode)
		}
	}
	check(s)
	dir := s.DataDir
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	s2 := testutil.StartServer(t, testutil.Options{DataDir: dir})
	if _, ok := s2.SetupCode(); ok || strings.Contains(s2.Stdout(), "SETUP CODE") {
		t.Fatalf("a restart with an administrator printed a code:\n%s", s2.Stdout())
	}
	check(s2)
	if n := countUsers(t, dir); n != 1 {
		t.Fatalf("%d users", n)
	}
}

// A restart before any administrator prints a new code, and the old one fails.
func TestRestartBeforeSetupGivesANewCode(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	first, ok := s.SetupCode()
	if !ok {
		t.Fatal("no code")
	}
	dir := s.DataDir
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	s2 := testutil.StartServer(t, testutil.Options{DataDir: dir})
	second, ok := s2.SetupCode()
	if !ok || second == first {
		t.Fatalf("second code %q (first %q)", second, first)
	}
	c := s2.Client()
	if resp, _ := postJSON(t, c, s2.URL+"/api/setup/admin", setupBody(first, "jamie")); resp.StatusCode != 403 {
		t.Fatalf("the old code: %d", resp.StatusCode)
	}
	if resp, body := postJSON(t, c, s2.URL+"/api/setup/admin", setupBody(second, "jamie")); resp.StatusCode != 200 {
		t.Fatalf("the new code: %d %s", resp.StatusCode, body)
	}
}

// Two setups that run at once with the right code yield one administrator.
func TestConcurrentSetupsYieldOneAdministrator(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, _ := s.SetupCode()
	const n = 8
	statuses := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			resp, _ := postJSON(t, s.Client(), s.URL+"/api/setup/admin", setupBody(code, "admin"+string(rune('a'+i))))
			statuses[i] = resp.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()
	won := 0
	for _, st := range statuses {
		switch st {
		case 200:
			won++
		case 404:
		default:
			t.Errorf("a setup answered %d", st)
		}
	}
	if won != 1 {
		t.Fatalf("%d setups succeeded: %v", won, statuses)
	}
	if got := countUsers(t, s.DataDir); got != 1 {
		t.Fatalf("%d users", got)
	}
}

// Ten wrong codes block that address, even for the right code, with the same
// "too many attempts" the other limited routes give.
func TestTenWrongSetupCodesBlockTheAddress(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	code, _ := s.SetupCode()
	c := s.Client()
	for i := 0; i < 10; i++ {
		if resp, _ := postJSON(t, c, s.URL+"/api/setup/admin", setupBody("0000-0000-0000-0000", "x")); resp.StatusCode != 403 {
			t.Fatalf("attempt %d: %d", i+1, resp.StatusCode)
		}
	}
	resp, _ := postJSON(t, c, s.URL+"/api/setup/admin", setupBody(code, "jamie"))
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("after ten wrong codes: %d", resp.StatusCode)
	}
	if n := countUsers(t, s.DataDir); n != 0 {
		t.Fatalf("%d users", n)
	}
}

// The code is printed with the trust guide's address when the app is served with the local CA.
func TestConsolePrintsTheTrustGuideWithTheCode(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{Env: map[string]string{"JL_HTTP_ONLY": ""}})
	out := s.Stdout()
	if !strings.Contains(out, "/trust/") || !strings.Contains(out, "SETUP CODE: ") {
		t.Fatalf("console:\n%s", out)
	}
	if strings.Index(out, "/trust/") > strings.Index(out, "SETUP CODE: ") {
		t.Fatal("the trust guide should come before the code")
	}
}

// SCAN-T2: the trust guide tells people to compare a device's fingerprint with
// the one on the Settings page, so the administrator's settings carry the one
// the console prints, and it is the fingerprint of the certificate the guide
// hands out. Without the local CA there is none.
func TestSettingsShowTheFingerprintTheConsolePrints(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{Env: map[string]string{"JL_HTTP_ONLY": ""}})
	code, _ := s.SetupCode()
	c := s.Client()
	// Certificate checking isn't what's tested here; the CA tests do that.
	c.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	if resp, body := postJSON(t, c, s.URL+"/api/setup/admin", setupBody(code, "jamie")); resp.StatusCode != 200 {
		t.Fatalf("setup: %d %s", resp.StatusCode, body)
	}
	st, body := getStatus(t, c, s.URL+"/api/admin/settings")
	var got struct {
		Fingerprint string `json:"ca_fingerprint"`
	}
	if st != 200 || json.Unmarshal([]byte(body), &got) != nil || got.Fingerprint == "" {
		t.Fatalf("settings: %d %s", st, body)
	}
	if want := "Local CA root SHA-256 fingerprint: " + got.Fingerprint; !strings.Contains(s.Stdout(), want) {
		t.Fatalf("console lacks %q:\n%s", want, s.Stdout())
	}
	resp, err := http.Get(s.TrustURL + "/trust/ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	der, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	sum := sha256.Sum256(der)
	if want := strings.ToUpper(hex.EncodeToString(sum[:])); strings.ReplaceAll(got.Fingerprint, ":", "") != want {
		t.Fatalf("settings fingerprint %s is not the downloaded certificate's %s", got.Fingerprint, want)
	}

	// no local CA: nothing to show
	p := testutil.StartServer(t, testutil.Options{})
	pc := p.Client()
	pcode, _ := p.SetupCode()
	postJSON(t, pc, p.URL+"/api/setup/admin", setupBody(pcode, "jamie"))
	if _, b := getStatus(t, pc, p.URL+"/api/admin/settings"); strings.Contains(b, "ca_fingerprint") {
		t.Fatalf("plain install shows a fingerprint: %s", b)
	}
}

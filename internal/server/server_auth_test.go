package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"jamieslibrary/internal/testutil"
)

// ACCESS-1 "Anonymous visitor" on the running server: a fresh installation
// shows the app shell for a catalog page and no data from the API.
func TestAnonymousVisitorOnTheRunningServer(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	c := s.Client()
	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := c.Get(s.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	for _, p := range []string{"/books/12", "/signin", "/desk/checkout"} {
		resp, body := get(p)
		if resp.StatusCode != 200 || !strings.Contains(body, "<div id=\"root\"") && !strings.Contains(body, "<html") {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	for _, p := range []string{"/api/me"} {
		if resp, _ := get(p); resp.StatusCode != 401 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	resp, _ := get("/api/health")
	if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("Cache-Control") != "no-store" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers on the running server: %v", resp.Header)
	}
	// a cross-origin POST is refused before anything runs
	req, _ := http.NewRequest("POST", s.URL+"/api/auth/signin", strings.NewReader(`{"username":"a","password":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("cross-origin sign-in: %d", r.StatusCode)
	}
}

// SEC-T1 on the running server: JL_TRUSTED_PROXIES makes X-Forwarded-For count, and without it the header is ignored.
func TestSignInLimitFollowsTheConfiguredProxies(t *testing.T) {
	attempts := func(s *testutil.Server, xff func(i int) string) (last int) {
		for i := 0; i < 6; i++ {
			req, _ := http.NewRequest("POST", s.URL+"/api/auth/signin", strings.NewReader(`{"username":"ann","password":"wrong"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Forwarded-For", xff(i))
			resp, err := s.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			last = resp.StatusCode
		}
		return last
	}
	differing := func(i int) string { return "203.0.113." + string(rune('1'+i)) }

	plain := testutil.StartServer(t, testutil.Options{})
	if got := attempts(plain, differing); got != 429 {
		t.Fatalf("forged addresses escaped the limit without a proxy: %d", got)
	}
	proxied := testutil.StartServer(t, testutil.Options{Env: map[string]string{"JL_TRUSTED_PROXIES": "127.0.0.0/8, ::1/128"}})
	if got := attempts(proxied, differing); got != 401 {
		t.Fatalf("visitors the proxy told apart were limited together: %d", got)
	}
	if got := attempts(proxied, func(int) string { return "203.0.113.99" }); got != 429 {
		t.Fatalf("one reported visitor was not limited: %d", got)
	}
}

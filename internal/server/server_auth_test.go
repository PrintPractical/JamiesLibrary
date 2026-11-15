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

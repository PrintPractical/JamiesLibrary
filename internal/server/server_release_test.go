//go:build !testhooks

package server

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// A release build, started with every JL_TEST_* variable set, runs as if they
// were not there: no control listener, the real clock, no failed migration.
func TestReleaseServerIgnoresTestVariables(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	controlAddr := probe.Addr().String()
	probe.Close() // free again: a build that obeyed would bind it
	env := map[string]string{
		"JL_DATA_DIR":            t.TempDir(),
		"JL_HTTP_ONLY":           "1",                                          // the app on the one listener this test hands over
		"JL_TEST_CONTROL_ADDR":   "0.0.0.0:" + controlAddr[len("127.0.0.1:"):], // would be refused if read
		"JL_TEST_METADATA_URL":   "http://127.0.0.1:1",
		"JL_TEST_FAIL_MIGRATION": "1",
		"JL_TEST_NOW":            "2001-01-01T00:00:00Z",
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan Ready, 1)
	done := make(chan error, 1)
	var out, log bytes.Buffer
	go func() {
		done <- Run(ctx, Options{Getenv: func(k string) string { return env[k] }, Stdout: &out, Stderr: &log, Listener: ln,
			Ready: func(r Ready) { ready <- r }})
	}()
	var r Ready
	select {
	case r = <-ready:
	case err := <-done:
		t.Fatalf("a release build refused to start over a JL_TEST_ variable: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("not ready")
	}
	if r.ControlAddr != "" {
		t.Errorf("control listener at %q in a release build", r.ControlAddr)
	}
	if c, err := net.DialTimeout("tcp", controlAddr, 300*time.Millisecond); err == nil {
		c.Close()
		t.Errorf("something listens on the control address %s", controlAddr)
	}
	if resp, err := http.Get("http://" + r.HTTPAddr + "/"); err != nil {
		t.Error(err)
	} else {
		resp.Body.Close()
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

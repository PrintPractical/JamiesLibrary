package server

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a log buffer the server goroutine writes while the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// start runs the server on configured ports (no listeners handed over) and
// returns its log once it is ready.
func start(t *testing.T, env map[string]string, args ...string) (*syncBuffer, Ready) {
	t.Helper()
	env["JL_DATA_DIR"] = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan Ready, 1)
	done := make(chan error, 1)
	log := &syncBuffer{}
	go func() {
		done <- Run(ctx, Options{Args: args, Getenv: func(k string) string { return env[k] },
			Stdout: &bytes.Buffer{}, Stderr: log, Ready: func(r Ready) { ready <- r }})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case r := <-ready:
		return log, r
	case err := <-done:
		t.Fatalf("did not start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("not ready")
	}
	return nil, Ready{}
}

func dials(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func TestBindAddressRestrictsBothListeners(t *testing.T) {
	// 127.0.0.2 is another local address on Linux; elsewhere the test can't tell.
	if l, err := net.Listen("tcp", "127.0.0.2:0"); err != nil {
		t.Skip("no second loopback address:", err)
	} else {
		l.Close()
	}
	http, https := freePort(t), freePort(t)
	_, r := start(t, map[string]string{
		"JL_HTTP_PORT": strconv.Itoa(http), "JL_HTTPS_PORT": strconv.Itoa(https), "JL_BIND_ADDRESS": "127.0.0.1",
	})
	for _, port := range []int{http, https} {
		p := strconv.Itoa(port)
		if !dials("127.0.0.1:" + p) {
			t.Errorf("port %s not reachable on the bound address", p)
		}
		if dials("127.0.0.2:" + p) {
			t.Errorf("port %s reachable on 127.0.0.2 although bound to 127.0.0.1", p)
		}
	}
	if !strings.HasPrefix(r.HTTPAddr, "127.0.0.1:") || !strings.HasPrefix(r.TrustAddr, "127.0.0.1:") {
		t.Errorf("addresses %q %q", r.HTTPAddr, r.TrustAddr)
	}
}

func TestBindFlagAndInvalidValue(t *testing.T) {
	_, r := start(t, map[string]string{"JL_HTTP_ONLY": "1", "JL_HTTP_PORT": strconv.Itoa(freePort(t))}, "--bind", "127.0.0.1")
	if !strings.HasPrefix(r.HTTPAddr, "127.0.0.1:") {
		t.Errorf("--bind ignored: %q", r.HTTPAddr)
	}
	err := Run(context.Background(), Options{Getenv: func(k string) string {
		return map[string]string{"JL_DATA_DIR": t.TempDir(), "JL_BIND_ADDRESS": "localhost"}[k]
	}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "JL_BIND_ADDRESS") || !strings.Contains(err.Error(), "localhost") {
		t.Errorf("invalid value: %v", err)
	}
}

func TestPlainHTTPWarningOnlyWithoutBindAddress(t *testing.T) {
	const warning = "plain HTTP on every network interface"
	port := func() string { return strconv.Itoa(freePort(t)) }
	for name, tc := range map[string]struct {
		env  map[string]string
		want bool
	}{
		"http only, no bind": {map[string]string{"JL_HTTP_ONLY": "1"}, true},
		"http only, bound":   {map[string]string{"JL_HTTP_ONLY": "1", "JL_BIND_ADDRESS": "127.0.0.1"}, false},
		"https mode":         {map[string]string{"JL_HTTPS_PORT": port()}, false},
	} {
		tc.env["JL_HTTP_PORT"] = port()
		log, _ := start(t, tc.env)
		if got := strings.Count(log.String(), warning); (got == 1) != tc.want || got > 1 {
			t.Errorf("%s: warning count %d, log:\n%s", name, got, log.String())
		}
	}
}

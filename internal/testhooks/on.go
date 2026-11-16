//go:build testhooks

package testhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"jamieslibrary/internal/clock"
)

// Enabled says whether this build has the test hooks.
const Enabled = true

type control struct {
	ln      net.Listener
	srv     *http.Server
	done    chan struct{}
	restore func()
	clock   *fakeClock
}

var (
	mu            sync.Mutex
	metadataURL   string
	failMigration int
)

// Start reads the JL_TEST_* variables through getenv. With
// JL_TEST_CONTROL_ADDR set it opens the control listener (loopback only: any
// other address is an error, so the server refuses to start) and puts a clock
// the listener moves in place of the system clock. It returns nil when no
// control address is given.
func Start(getenv func(string) string) (*Control, error) {
	mu.Lock()
	defer mu.Unlock()
	fail := 0
	if v := strings.TrimSpace(getenv(EnvFailMigration)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%s must be a migration number, got %q", EnvFailMigration, v)
		}
		fail = n
	}
	var c *Control
	if v := strings.TrimSpace(getenv(EnvControlAddr)); v != "" {
		var err error
		var start time.Time
		if n := strings.TrimSpace(getenv(EnvNow)); n != "" {
			if start, err = time.Parse(time.RFC3339, n); err != nil {
				return nil, fmt.Errorf("%s must be an RFC 3339 time, got %q", EnvNow, n)
			}
		}
		if c, err = startControl(v, start); err != nil {
			return nil, err
		}
	}
	metadataURL = strings.TrimSpace(getenv(EnvMetadataURL))
	failMigration = fail
	return c, nil
}

// MetadataURL is the base URL that replaces the Open Library hosts, "" if none.
func MetadataURL() string {
	mu.Lock()
	defer mu.Unlock()
	return metadataURL
}

// FailMigration is an error for the migration the test asked to fail, nil for any other.
func FailMigration(version int) error {
	mu.Lock()
	defer mu.Unlock()
	if failMigration != 0 && failMigration == version {
		return fmt.Errorf("%s=%d: failure injected by a test", EnvFailMigration, version)
	}
	return nil
}

// loopbackListener binds addr, which must name a loopback IP (or localhost).
func loopbackListener(addr string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%s must be host:port, got %q: %w", EnvControlAddr, addr, err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("%s must be a loopback address such as 127.0.0.1:PORT, got %q", EnvControlAddr, addr)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), port))
	if err != nil {
		return nil, fmt.Errorf("%s: cannot listen on %q: %w", EnvControlAddr, addr, err)
	}
	return ln, nil
}

func startControl(addr string, start time.Time) (*Control, error) {
	ln, err := loopbackListener(addr)
	if err != nil {
		return nil, err
	}
	fc := &fakeClock{}
	if !start.IsZero() {
		fc.now, fc.set = start, true
	}
	c := &control{ln: ln, clock: fc, done: make(chan struct{})}
	c.restore = clock.SetSource(fc.Now)
	mux := http.NewServeMux()
	mux.HandleFunc("/clock", fc.serveHTTP)
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		defer close(c.done)
		_ = c.srv.Serve(ln)
	}()
	return &Control{impl: c}, nil
}

func (c *control) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.srv.Shutdown(ctx)
	<-c.done
	c.restore()
	mu.Lock()
	metadataURL, failMigration = "", 0
	mu.Unlock()
	return err
}

// fakeClock follows the system clock until a test sets or advances it, and
// stands still at the time it was given from then on.
type fakeClock struct {
	mu  sync.Mutex
	set bool
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.set {
		return time.Now()
	}
	return f.now
}

func (f *fakeClock) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeNow(w, http.StatusOK, f.Now())
	case http.MethodPost:
		f.post(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
	}
}

func (f *fakeClock) post(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Now     *time.Time `json:"now"`
		Advance *string    `json:"advance"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, `body must be {"now":"RFC 3339 time"} or {"advance":"36h"}: `+err.Error(), http.StatusBadRequest)
		return
	}
	if (body.Now == nil) == (body.Advance == nil) {
		http.Error(w, `give exactly one of "now" and "advance"`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if body.Now != nil {
		f.now, f.set = *body.Now, true
	} else {
		d, err := time.ParseDuration(*body.Advance)
		if err != nil {
			http.Error(w, "advance: "+err.Error(), http.StatusBadRequest)
			return
		}
		if !f.set {
			f.now, f.set = time.Now(), true
		}
		f.now = f.now.Add(d)
	}
	writeNow(w, http.StatusOK, f.now)
}

func writeNow(w http.ResponseWriter, status int, t time.Time) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"now": t.UTC().Format(time.RFC3339Nano)})
}

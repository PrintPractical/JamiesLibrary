// Package testutil starts the real server for tests: in-process, in a
// temporary data directory, on free ports, with a clock the test moves.
// It builds nothing itself and needs the testhooks build tag.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/server"
	"jamieslibrary/internal/setupcode"
	"jamieslibrary/internal/testhooks"
)

// DefaultNow is the fake clock's time at start-up when Options.Now is not set.
var DefaultNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// Options choose how StartServer starts the server; the zero value is a fresh library.
type Options struct {
	// DataDir is the data directory; a new temporary one when empty. Pass the
	// same directory to a second server to restart on the same data.
	DataDir string
	// Now is the fake clock's time from start-up on, DefaultNow when zero.
	Now time.Time
	// OpenLibrary, when set, is the provider the server asks (JL_TEST_METADATA_URL).
	OpenLibrary *FakeOL
	// Env adds environment variables (JL_*), seen by the server as its environment.
	// The server starts with JL_HTTP_ONLY=1, so URL is the app over plain HTTP;
	// set "JL_HTTP_ONLY": "" to start it as an installation does, with the local
	// CA: URL is then https and TrustURL serves the CA downloads.
	Env map[string]string
	// Args are extra command-line flags.
	Args []string
	// Migrations replaces the embedded migrations.
	Migrations fs.FS
}

// serverMu lets one in-process server run at a time: the clock and the test
// hooks are one per process.
var serverMu sync.Mutex

// Server is a running server.
type Server struct {
	// URL is the base URL of the web server, such as http://127.0.0.1:41234.
	URL string
	// TrustURL is the base URL of the plain-HTTP listener (trust downloads and
	// the redirect to HTTPS); "" in JL_HTTP_ONLY mode.
	TrustURL string
	// ControlURL is the base URL of the test control listener.
	ControlURL string
	// DataDir is the data directory.
	DataDir string

	stdout, stderr lockedBuffer
	cancel         context.CancelFunc
	done           chan struct{}
	err            error
	stopOnce       sync.Once
	unlock         func()
}

// StartServer starts the server and fails the test if it can't start. The
// server is stopped when the test ends; Stop does it earlier.
func StartServer(t testing.TB, opts Options) *Server {
	t.Helper()
	s, err := TryStartServer(t, opts)
	if err != nil {
		t.Fatalf("starting the server: %v", err)
	}
	return s
}

// TryStartServer is StartServer for a test that expects the start to fail:
// it returns the error the server gave, and the stdout and log it wrote
// before it stopped are in the *StartError.
func TryStartServer(t testing.TB, opts Options) (*Server, error) {
	t.Helper()
	if !testhooks.Enabled {
		t.Skip("needs the test hooks: run with -tags testhooks")
	}
	serverMu.Lock()
	s := &Server{DataDir: opts.DataDir, done: make(chan struct{}), unlock: serverMu.Unlock}
	if s.DataDir == "" {
		s.DataDir = t.TempDir()
	}
	env := map[string]string{
		"JL_DATA_DIR":            s.DataDir,
		"JL_LOG_LEVEL":           "debug",
		testhooks.EnvControlAddr: "127.0.0.1:0",
		"JL_HTTP_ONLY":           "1",
	}
	now := opts.Now
	if now.IsZero() {
		now = DefaultNow
	}
	env[testhooks.EnvNow] = now.UTC().Format(time.RFC3339Nano)
	if opts.OpenLibrary != nil {
		env[testhooks.EnvMetadataURL] = opts.OpenLibrary.URL
	}
	for k, v := range opts.Env {
		env[k] = v
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.unlock()
		return nil, err
	}
	var trustLn net.Listener
	if env["JL_HTTP_ONLY"] == "" {
		if trustLn, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			_ = ln.Close()
			s.unlock()
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	ready := make(chan server.Ready, 1)
	go func() {
		defer close(s.done)
		s.err = server.Run(ctx, server.Options{
			Args:          opts.Args,
			Getenv:        func(k string) string { return env[k] },
			Stdout:        &s.stdout,
			Stderr:        &s.stderr,
			Version:       "test",
			Listener:      ln,
			TrustListener: trustLn,
			Ready:         func(r server.Ready) { ready <- r },
			Migrations:    opts.Migrations,
		})
	}()
	select {
	case r := <-ready:
		s.URL = "http://" + r.HTTPAddr
		if r.TLS {
			s.URL = "https://" + r.HTTPAddr
		}
		if r.TrustAddr != "" {
			s.TrustURL = "http://" + r.TrustAddr
		}
		s.ControlURL = "http://" + r.ControlAddr
	case <-s.done:
		_ = ln.Close()
		if trustLn != nil {
			_ = trustLn.Close()
		}
		s.unlock()
		return s, &StartError{Err: s.err, Stdout: s.Stdout(), Log: s.Log()}
	case <-time.After(30 * time.Second):
		cancel()
		<-s.done
		s.unlock()
		return nil, fmt.Errorf("the server was not ready in 30 s; log:\n%s", s.Log())
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, nil
}

// StartError is a start-up that failed: the error and what the server printed.
type StartError struct {
	Err    error
	Stdout string
	Log    string
}

func (e *StartError) Error() string { return e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// Stop shuts the server down and waits until it has stopped. It returns the
// error the server ended with, nil for a clean shutdown, and is safe to call twice.
func (s *Server) Stop() error {
	s.stopOnce.Do(func() {
		s.cancel()
		<-s.done
		s.unlock()
	})
	return s.err
}

// Stdout is what the server has printed on standard output so far.
func (s *Server) Stdout() string { return s.stdout.String() }

// Log is what the server has logged so far.
func (s *Server) Log() string { return s.stderr.String() }

// SetupCode is the code from the setup-code line on standard output.
func (s *Server) SetupCode() (string, bool) { return ParseSetupCode(s.Stdout()) }

// ParseSetupCode finds the line "SETUP CODE: XXXX-XXXX-XXXX-XXXX" in output.
func ParseSetupCode(output string) (string, bool) {
	m := setupcode.Pattern.FindStringSubmatch(output)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Client is an HTTP client with its own cookie jar, so each makes its own
// sign-in. The session cookie is Secure in every mode, so the jar treats the
// server's plain-HTTP address as the HTTPS one a browser behind a proxy sees. It follows no redirects: a test sees the redirect itself.
func (s *Server) Client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     secureJar{jar},
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// secureJar presents every URL as https to the cookie jar underneath.
type secureJar struct{ http.CookieJar }

func asHTTPS(u *url.URL) *url.URL {
	c := *u
	c.Scheme = "https"
	return &c
}

func (j secureJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.CookieJar.SetCookies(asHTTPS(u), cookies)
}
func (j secureJar) Cookies(u *url.URL) []*http.Cookie { return j.CookieJar.Cookies(asHTTPS(u)) }

// SetNow sets the clock the server sees.
func (s *Server) SetNow(t time.Time) error {
	return s.control(map[string]string{"now": t.UTC().Format(time.RFC3339Nano)})
}

// Advance moves the clock the server sees forward by d (backward if negative).
func (s *Server) Advance(d time.Duration) error {
	return s.control(map[string]string{"advance": d.String()})
}

// Now is the time the server sees.
func (s *Server) Now() (time.Time, error) {
	resp, err := http.Get(s.ControlURL + "/clock")
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	return decodeNow(resp)
}

func (s *Server) control(body map[string]string) error {
	b, _ := json.Marshal(body)
	resp, err := http.Post(s.ControlURL+"/clock", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = decodeNow(resp)
	return err
}

func decodeNow(resp *http.Response) (time.Time, error) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("control: %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	var v struct{ Now time.Time }
	if err := json.Unmarshal(body, &v); err != nil {
		return time.Time{}, fmt.Errorf("control: %w", err)
	}
	return v.Now, nil
}

// lockedBuffer is a bytes.Buffer that the server writes and a test reads at once.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

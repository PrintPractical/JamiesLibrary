package e2e

// The installation as an operator runs it: the real binary, built with the
// test hooks, as a separate process with a clean environment. This is what lets
// a test stop it, copy its data directory, or kill -9 it.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"jamieslibrary/internal/testutil"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// binary builds the server once per test run, with the test hooks, and returns its path.
func binary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the real binary: skipped with -short")
	}
	binOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		root := filepath.Join(filepath.Dir(file), "..", "..")
		dir, err := os.MkdirTemp("", "jl-e2e-bin-")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "jamieslibrary")
		cmd := exec.Command("go", "build", "-tags", "testhooks", "-o", binPath, "./cmd/jamieslibrary")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func freePort(t testing.TB) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// proc is one running server process.
type proc struct {
	t      *testing.T
	cmd    *exec.Cmd
	dir    string
	Base   string
	Stdout syncBuf
	Stderr syncBuf
	done   chan struct{}
	err    error
}

// launch starts the binary on dir with only the variables given (and a HOME and
// working directory of their own, both empty), and does not wait for it.
func launch(t *testing.T, dir string, env map[string]string) *proc {
	t.Helper()
	p := &proc{t: t, dir: dir, done: make(chan struct{})}
	port := freePort(t)
	control := freePort(t)
	for control == port { // both are released before the server binds them, so the same number can come back
		control = freePort(t)
	}
	p.Base = fmt.Sprintf("http://127.0.0.1:%d", port)
	e := []string{
		"JL_DATA_DIR=" + dir,
		fmt.Sprintf("JL_HTTP_PORT=%d", port),
		"JL_HTTP_ONLY=1",
		"JL_BIND_ADDRESS=127.0.0.1",
		fmt.Sprintf("JL_TEST_CONTROL_ADDR=127.0.0.1:%d", control),
		"HOME=" + t.TempDir(),
	}
	for k, v := range env {
		e = append(e, k+"="+v)
	}
	p.cmd = exec.Command(binary(t), "serve")
	p.cmd.Dir = t.TempDir()
	p.cmd.Env = e
	p.cmd.Stdout, p.cmd.Stderr = &p.Stdout, &p.Stderr
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		p.err = p.cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
	return p
}

// waitReady waits until the server answers its health check. It returns an
// error if the process exits first or the wait runs out.
func (p *proc) waitReady() error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return fmt.Errorf("the server exited (%v) before it was ready\nstdout: %s\nstderr: %s", p.err, p.Stdout.String(), p.Stderr.String())
		default:
		}
		resp, err := http.Get(p.Base + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("the server was not ready in 20 s\nstderr: %s", p.Stderr.String())
}

// start launches and waits for readiness, failing the test if it never comes up.
func start(t *testing.T, dir string, env map[string]string) *proc {
	t.Helper()
	p := launch(t, dir, env)
	if err := p.waitReady(); err != nil {
		t.Fatal(err)
	}
	return p
}

// stop asks the server to stop as an operator does (SIGTERM) and requires a clean exit.
func (p *proc) stop() {
	p.t.Helper()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		p.t.Fatalf("the server did not stop within 20 s of SIGTERM\nstderr: %s", p.Stderr.String())
	}
	if p.err != nil {
		p.t.Fatalf("the server did not stop cleanly: %v\nstderr: %s", p.err, p.Stderr.String())
	}
}

// kill is kill -9: no shutdown code runs.
func (p *proc) kill() {
	_ = p.cmd.Process.Signal(syscall.SIGKILL)
	<-p.done
}

// exited reports whether the process has ended, within d.
func (p *proc) exited(d time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(d):
		return false
	}
}

// ---- a client of the process ------------------------------------------------

type pc struct {
	t    testing.TB
	base string
	hc   *http.Client
}

type secJar struct{ http.CookieJar }

func https(u *url.URL) *url.URL { c := *u; c.Scheme = "https"; return &c }

func (j secJar) SetCookies(u *url.URL, c []*http.Cookie) { j.CookieJar.SetCookies(https(u), c) }
func (j secJar) Cookies(u *url.URL) []*http.Cookie       { return j.CookieJar.Cookies(https(u)) }

func newPC(t testing.TB, base string) *pc {
	jar, _ := cookiejar.New(nil)
	return &pc{t: t, base: base, hc: &http.Client{Jar: secJar{jar}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// raw sends one request; a transport error is returned, not fatal, so a test can
// keep going while the server is being killed.
func (c *pc) raw(method, path, ctype string, body []byte) (int, []byte, error) {
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.StatusCode, b, err
}

func (c *pc) json(method, path string, v any) (int, map[string]any, error) {
	var b []byte
	ct := ""
	if v != nil {
		b, _ = json.Marshal(v)
		ct = "application/json"
	}
	st, raw, err := c.raw(method, path, ct, b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return st, m, err
}

// must is json that fails the test unless the status is want.
func (c *pc) must(want int, method, path string, v any) map[string]any {
	c.t.Helper()
	st, m, err := c.json(method, path, v)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	if st != want {
		c.t.Fatalf("%s %s: want %d, got %d: %v", method, path, want, st, m)
	}
	return m
}

func (c *pc) signIn(user, password string) {
	c.t.Helper()
	c.must(200, "POST", "/api/auth/signin", map[string]string{"username": user, "password": password})
}

// bookForm posts a multipart book form (cover may be nil).
func (c *pc) bookForm(method, path string, fields [][2]string, cover []byte) (int, map[string]any, error) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, f := range fields {
		_ = mw.WriteField(f[0], f[1])
	}
	if cover != nil {
		fw, _ := mw.CreateFormFile("cover", "cover.png")
		_, _ = fw.Write(cover)
	}
	_ = mw.Close()
	st, raw, err := c.raw(method, path, mw.FormDataContentType(), b.Bytes())
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return st, m, err
}

// pngCover is a distinct small image for each seed.
func pngCover(seed int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{uint8(seed*37 + x*3), uint8(seed*91 + y*2), uint8(seed * 13), 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// firstAdmin sets up the first administrator from the setup code on the console.
func firstAdmin(t *testing.T, p *proc) *pc {
	t.Helper()
	// The console is copied from the process by a goroutine, so the line can
	// arrive a moment after the server answers its health check.
	code, ok := testutil.ParseSetupCode(p.Stdout.String())
	for deadline := time.Now().Add(5 * time.Second); !ok && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		code, ok = testutil.ParseSetupCode(p.Stdout.String())
	}
	if !ok {
		t.Fatalf("no setup code on the console:\n%s", p.Stdout.String())
	}
	c := newPC(t, p.Base)
	c.must(200, "POST", "/api/setup/admin", map[string]string{
		"code": code, "username": "root", "display_name": "Root", "password": adminPassword})
	return c
}

// ---- the data directory, read directly --------------------------------------

func openRO(t testing.TB, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "library.db")+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	// A connection kept open between queries holds SQLite's shared-memory lock on the
	// library. A server that is then killed (kill -9) is not recovered from by the next one,
	// which trusts a wal-index that lacks the last commits and serves a stale picture of the
	// data. Releasing each connection when its query is done keeps the test from being the
	// other process that holds the file.
	t.Cleanup(func() { db.Close() })
	return db
}

// dumpDB is every row of every table, as sorted text lines, so two databases
// can be compared. Tables in skip are left out.
func dumpDB(t testing.TB, path string, skip ...string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return dumpOf(t, db, skip...)
}

func dumpOf(t testing.TB, db *sql.DB, skip ...string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	var out []string
next:
	for _, tb := range tables {
		for _, s := range skip {
			if s == tb {
				continue next
			}
		}
		out = append(out, "TABLE "+tb)
		r, err := db.Query(`SELECT * FROM "` + tb + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := r.Columns()
		var lines []string
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			var parts []string
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				parts = append(parts, fmt.Sprintf("%s=%#v", cols[i], v))
			}
			lines = append(lines, tb+": "+strings.Join(parts, " "))
		}
		r.Close()
		sort.Strings(lines)
		out = append(out, lines...)
	}
	return out
}

func diffLines(a, b []string) string {
	seen := map[string]int{}
	for _, l := range a {
		seen[l]++
	}
	for _, l := range b {
		seen[l]--
	}
	var out []string
	for l, n := range seen {
		if n > 0 {
			out = append(out, "only before: "+l)
		} else if n < 0 {
			out = append(out, "only after:  "+l)
		}
	}
	sort.Strings(out)
	if len(out) > 20 {
		out = append(out[:20], "...")
	}
	return strings.Join(out, "\n")
}

// copyDir copies a whole directory tree, keeping file modes.
func copyDir(t testing.TB, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

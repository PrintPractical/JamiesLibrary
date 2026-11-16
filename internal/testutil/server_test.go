//go:build testhooks

package testutil

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"jamieslibrary/internal/store"
	"jamieslibrary/internal/testhooks"
)

func get(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestServerStartsOnAFreePortAndAnswers(t *testing.T) {
	s := StartServer(t, Options{})
	resp, _ := get(t, s.Client(), s.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %s", resp.Status)
	}
	if !strings.Contains(s.Log(), "listening") {
		t.Errorf("log: %q", s.Log())
	}
	if s.DataDir == "" || !strings.HasPrefix(s.DataDir, os.TempDir()) {
		t.Errorf("data dir %q is not a temporary one", s.DataDir)
	}
}

func TestStopWaitsForShutdownAndFreesThePort(t *testing.T) {
	s := StartServer(t, Options{})
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	c := &http.Client{Timeout: time.Second}
	if _, err := c.Get(s.URL + "/"); err == nil {
		t.Error("the server still answers after Stop")
	}
	// The data directory's lock is free again.
	StartServer(t, Options{DataDir: s.DataDir})
}

func TestControlEndpointMovesTheClockTheServerSees(t *testing.T) {
	at := time.Date(2031, 6, 7, 8, 9, 10, 0, time.UTC)
	s := StartServer(t, Options{Now: at})
	if got, err := s.Now(); err != nil || !got.Equal(at) {
		t.Fatalf("Now = %v, %v; want %v", got, err, at)
	}
	if err := s.Advance(36 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Now(); !got.Equal(at.Add(36 * time.Hour)) {
		t.Fatalf("after advance: %v", got)
	}
	if err := s.Advance(-48 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Now(); !got.Equal(at.Add(-12 * time.Hour)) {
		t.Fatalf("after going back: %v", got)
	}
	// The clock stands still between calls.
	a, _ := s.Now()
	time.Sleep(20 * time.Millisecond)
	if b, _ := s.Now(); !a.Equal(b) {
		t.Errorf("clock moved by itself: %v then %v", a, b)
	}
}

func TestControlEndpointRefusesBadRequests(t *testing.T) {
	s := StartServer(t, Options{})
	c := &http.Client{Timeout: 5 * time.Second}
	for name, body := range map[string]string{
		"empty":        ``,
		"not json":     `now`,
		"both":         `{"now":"2030-01-01T00:00:00Z","advance":"1h"}`,
		"neither":      `{}`,
		"bad time":     `{"now":"tomorrow"}`,
		"bad duration": `{"advance":"a while"}`,
		"unknown key":  `{"later":"1h"}`,
	} {
		resp, err := c.Post(s.ControlURL+"/clock", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %s, want 400", name, resp.Status)
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, s.ControlURL+"/clock", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %s", resp.Status)
	}
}

func TestNonLoopbackControlAddressRefusesToStart(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.5:9000", ":9000", "example.com:80", "[::]:0", "nonsense"} {
		_, err := TryStartServer(t, Options{Env: map[string]string{"JL_TEST_CONTROL_ADDR": addr}})
		var se *StartError
		if !errors.As(err, &se) || !strings.Contains(err.Error(), "JL_TEST_CONTROL_ADDR") {
			t.Errorf("%s: want a start error naming JL_TEST_CONTROL_ADDR, got %v", addr, err)
		}
	}
	// The refusals released the process-wide lock: a good start still works.
	StartServer(t, Options{})
}

func TestMigrationFailureInjectionFailsStartUpAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	v1 := fstest.MapFS{"0001_things.sql": {Data: withSettings(t, "CREATE TABLE things (name TEXT); INSERT INTO things VALUES ('kept')")}}
	mustStop(t, StartServer(t, Options{DataDir: dir, Migrations: v1}))

	v2 := fstest.MapFS{
		"0001_things.sql": v1["0001_things.sql"],
		"0002_more.sql":   {Data: []byte("CREATE TABLE more (x); INSERT INTO things VALUES ('new')")},
	}
	_, err := TryStartServer(t, Options{DataDir: dir, Migrations: v2, Now: DefaultNow.Add(time.Hour), Env: map[string]string{"JL_TEST_FAIL_MIGRATION": "2"}})
	var me *store.MigrationError
	if !errors.As(err, &me) || me.Version != 2 || me.Backup == "" {
		t.Fatalf("want a MigrationError for 2 naming a backup, got %v", err)
	}
	if !strings.Contains(err.Error(), me.Backup) {
		t.Errorf("the error does not name the backup: %v", err)
	}
	if _, err := os.Stat(me.Backup); err != nil {
		t.Errorf("backup missing: %v", err)
	}

	// Rolled back: the old version still starts and sees the old data only.
	mustStop(t, StartServer(t, Options{DataDir: dir, Migrations: v1}))
	st, err := store.Open(t.Context(), dir, store.Options{Migrations: v1})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB().QueryRow("SELECT count(*) FROM things").Scan(&n)
	var more int
	st.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE name='more'").Scan(&more)
	st.Close()
	if n != 1 || more != 0 {
		t.Errorf("after the failed upgrade: %d rows in things (want 1), %d tables named more (want 0)", n, more)
	}

	// Without the injection the same upgrade succeeds, so the failure was the hook's.
	mustStop(t, StartServer(t, Options{DataDir: dir, Migrations: v2, Now: DefaultNow.Add(2 * time.Hour)}))
	if got, _ := filepath.Glob(filepath.Join(dir, "backups", "*")); len(got) == 0 {
		t.Error("no backups at all")
	}
}

func TestServerStartsWithTheClockAtTheTimeGiven(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2029, 3, 4, 5, 6, 7, 0, time.UTC)
	m := fstest.MapFS{"0001_a.sql": {Data: withSettings(t, "CREATE TABLE a (x)")}}
	mustStop(t, StartServer(t, Options{DataDir: dir, Now: at, Migrations: m}))
	st, err := store.Open(t.Context(), dir, store.Options{Migrations: m})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var applied string
	st.DB().QueryRow("SELECT applied_at FROM schema_migrations").Scan(&applied)
	if applied != "2029-03-04T05:06:07Z" {
		t.Errorf("the migration was stamped %q: the store did not use the fake clock", applied)
	}
}

func TestFailMigrationMustBeANumber(t *testing.T) {
	_, err := TryStartServer(t, Options{Env: map[string]string{"JL_TEST_FAIL_MIGRATION": "second"}})
	if err == nil || !strings.Contains(err.Error(), "JL_TEST_FAIL_MIGRATION") {
		t.Fatalf("got %v", err)
	}
}

func TestMetadataURLReachesTheServer(t *testing.T) {
	ol := FakeOpenLibrary(t)
	s := StartServer(t, Options{OpenLibrary: ol})
	_ = s
	if got := testhooks.MetadataURL(); got != ol.URL {
		t.Errorf("metadata URL = %q, want %q", got, ol.URL)
	}
}

func TestSetupCodeParser(t *testing.T) {
	out := "listening\nSETUP CODE: AB12-CD34-EF56-GH78\nopen the trust page\n"
	if code, ok := ParseSetupCode(out); !ok || code != "AB12-CD34-EF56-GH78" {
		t.Errorf("got %q %v", code, ok)
	}
	for _, bad := range []string{"", "SETUP CODE: ab12-cd34-ef56-gh78\n", "the SETUP CODE: AB12-CD34-EF56-GH78\n", "SETUP CODE: AB12-CD34-EF56\n"} {
		if code, ok := ParseSetupCode(bad); ok {
			t.Errorf("%q gave %q", bad, code)
		}
	}
}

func TestEachClientHasItsOwnCookies(t *testing.T) {
	s := StartServer(t, Options{})
	if a, b := s.Client(), s.Client(); a == b || a.Jar == b.Jar || a.Jar == nil {
		t.Fatal("clients share state or have no jar")
	}
}

func mustStop(t *testing.T, s *Server) {
	t.Helper()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}

// withSettings adds the real settings table to a test's own first migration:
// the server reads the settings as it starts.
func withSettings(t *testing.T, sql string) []byte {
	t.Helper()
	settings, err := os.ReadFile("../store/migrations/0002_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	// start-up asks whether an administrator exists, so the users tables are needed too
	users, err := os.ReadFile("../store/migrations/0004_users.sql")
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte(sql+";\n"), settings...), users...)
}

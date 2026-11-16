package e2e

// DEPLOY-T4: an installation made by an older release upgrades when the newer
// one starts on its data directory, keeps a copy of what it had, and a failed
// upgrade changes nothing. The older releases are fixtures: databases built
// from frozen copies of the migrations as they were (testdata/migrations,
// never edited, as the applied files never are) holding hand-written rows, so
// no current code makes them.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/testutil"
)

var fixtureTitles = []string{"The Fixture Hobbit", "Ünïcode Fixture ☕", "Third Fixture"}

// newestMigration is the number of the newest migration the product ships.
func newestMigration(t testing.TB) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "store", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no shipped migrations found: %v", err)
	}
	max := 0
	for _, f := range files {
		n, _ := strconv.Atoi(strings.SplitN(filepath.Base(f), "_", 2)[0])
		if n > max {
			max = n
		}
	}
	return max
}

// frozenMigrations is the migrations 1..upTo as an older release shipped them.
func frozenMigrations(t testing.TB, upTo int) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	for n := 1; n <= upTo; n++ {
		files, _ := filepath.Glob(filepath.Join("testdata", "migrations", fmt.Sprintf("%04d_*.sql", n)))
		if len(files) != 1 {
			t.Fatalf("frozen migration %d: found %v", n, files)
		}
		b, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(files[0])] = &fstest.MapFile{Data: b}
	}
	return out
}

const fixtureAdminUser = "keeper"

// buildFixture makes the data directory an installation at schema version upTo
// would have: the frozen schema and a small library with an administrator
// (password adminPassword), a patron and, from version 7, a loan.
func buildFixture(t testing.TB, dir string, upTo int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "library.db")+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("fixture: %v\n%s", err, q)
		}
	}
	exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`)
	for n := 1; n <= upTo; n++ {
		files, _ := filepath.Glob(filepath.Join("testdata", "migrations", fmt.Sprintf("%04d_*.sql", n)))
		if len(files) != 1 {
			t.Fatalf("frozen migration %d: found %v", n, files)
		}
		b, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		exec(string(b))
		exec(`INSERT INTO schema_migrations VALUES (?, ?, '2026-03-01T09:00:00Z')`, n, filepath.Base(files[0]))
	}
	hash, err := auth.NewPasswords().Hash(context.Background(), "password", adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE settings SET library_name = 'Fixture Library', loan_period_days = 10 WHERE id = 1`)
	for i, title := range fixtureTitles {
		exec(`INSERT INTO books (id, title, description, isbn13, cover, search_text, created_at) VALUES (?, ?, ?, ?, NULL, ?, '2026-03-01T09:00:00Z')`,
			i+1, title, "Fixture description "+title, []any{"9780140328721", "9780261103573", nil}[i], strings.ToLower(title)+"\nfixture author")
		exec(`INSERT INTO book_authors VALUES (?, 0, 'Fixture Author')`, i+1)
		for n := 1; n <= 2; n++ {
			id := i*2 + n
			exec(`INSERT INTO copies (id, book_id, number, condition, location_id, created_at) VALUES (?, ?, ?, 'good', 1, '2026-03-01T09:00:00Z')`, id, i+1, n)
			exec(`INSERT INTO identifiers (code, kind, copy_id) VALUES (?, 'copy', ?)`, fmt.Sprintf("FX-%04d", id), id)
		}
	}
	exec(`INSERT INTO users (id, username, display_name, password_hash, must_change_password, role, active, created_at) VALUES
		(1, ?, 'The Keeper', ?, 0, 'administrator', 1, '2026-03-01T09:00:00Z'),
		(2, 'reader', 'A Reader', ?, 0, 'patron', 1, '2026-03-01T09:00:00Z')`, fixtureAdminUser, hash, hash)
	for _, p := range perm.All {
		exec(`INSERT INTO user_permissions VALUES (1, ?)`, string(p))
	}
	for _, p := range []perm.Permission{perm.CatalogBrowse, perm.LoansOwnView, perm.LoansOwnRenew, perm.ReviewsWrite, perm.SelfCheckout} {
		exec(`INSERT INTO user_permissions VALUES (2, ?)`, string(p))
	}
	exec(`INSERT INTO identifiers (code, kind, user_id) VALUES ('CARD-KEEPER', 'card', 1), ('CARD-READER', 'card', 2)`)
	if upTo >= 7 {
		exec(`INSERT INTO loans (copy_id, patron_id, checked_out_at, checked_out_by, due_date, renewals) VALUES (1, 2, '2026-03-02T10:00:00Z', 1, '2026-03-12', 1)`)
		exec(`INSERT INTO loans (copy_id, patron_id, checked_out_at, checked_out_by, due_date, renewals, returned_at, returned_by) VALUES (3, 2, '2026-02-01T10:00:00Z', 1, '2026-02-11', 0, '2026-02-05T10:00:00Z', 1)`)
	}
	exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
}

// integrity is SQLite's own verdict on a database, and its foreign key check, read
// from a copy of the directory so that the original is left as it is.
func integrity(t testing.TB, dir string) (version int, dump []string) {
	t.Helper()
	cp := t.TempDir()
	copyDir(t, dir, cp)
	_ = os.Remove(filepath.Join(cp, "lock"))
	db, err := sql.Open("sqlite", "file:"+filepath.Join(cp, "library.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var verdict string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&verdict); err != nil || verdict != "ok" {
		t.Fatalf("integrity_check: %q %v", verdict, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign_key_check found a row pointing at nothing")
	}
	rows.Close()
	var v sql.NullInt64
	_ = db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), dumpOf(t, db)
}

func backupsOf(t testing.TB, dir string) []string {
	t.Helper()
	got, _ := filepath.Glob(filepath.Join(dir, "backups", "*"))
	return got
}

func fixtureAdmin(t *testing.T, base string) *pc {
	t.Helper()
	c := newPC(t, base)
	c.signIn(fixtureAdminUser, adminPassword)
	return c
}

var fixtureVersions = []int{4, 7}

// DEPLOY-T4 "Upgrade": a library from an older release is migrated when the new
// release starts, keeps every record, and a copy of the data as it was before
// the upgrade is left in backups/.
func TestOlderSchemaIsMigratedAndLeavesAPreUpgradeCopy(t *testing.T) {
	known := newestMigration(t)
	for _, from := range fixtureVersions {
		t.Run(fmt.Sprintf("from-v%d", from), func(t *testing.T) {
			if from >= known {
				t.Skipf("the shipped schema is %d: nothing to upgrade from %d", known, from)
			}
			dir := filepath.Join(t.TempDir(), "data")
			buildFixture(t, dir, from)
			before := dumpDB(t, filepath.Join(dir, "library.db"))

			p := start(t, dir, nil)
			if strings.Contains(p.Stdout.String(), "SETUP CODE") {
				t.Errorf("the upgraded library offered first-run setup:\n%s", p.Stdout.String())
			}
			bk := backupsOf(t, dir)
			if len(bk) != 1 {
				t.Fatalf("want one pre-upgrade copy, found %v", bk)
			}
			if ok, _ := regexp.MatchString(fmt.Sprintf(`pre-%d-to-%d-\d{8}T\d{6}Z\.db$`, from, known), bk[0]); !ok {
				t.Errorf("the copy's name doesn't say what it is: %s", bk[0])
			}
			if fi, _ := os.Stat(bk[0]); fi == nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("the copy must be readable by its owner only: %v", fi)
			}
			if d := diffLines(before, dumpDB(t, bk[0])); d != "" {
				t.Errorf("the pre-upgrade copy is not the database as it was:\n%s", d)
			}

			c := fixtureAdmin(t, p.Base)
			res := c.must(200, "GET", "/api/books", nil)
			if int(res["total"].(float64)) != len(fixtureTitles) {
				t.Errorf("books after the upgrade: %v", res)
			}
			for i, title := range fixtureTitles {
				d := c.must(200, "GET", fmt.Sprintf("/api/books/%d", i+1), nil)
				if d["title"] != title || len(d["copies"].([]any)) != 2 {
					t.Errorf("book %d after the upgrade: %v", i+1, d)
				}
			}
			if from >= 7 {
				l := c.must(200, "GET", "/api/patron-loans/2", nil)
				if !strings.Contains(fmt.Sprint(l), "2026-03-12") {
					t.Errorf("the open loan lost its due date: %v", l)
				}
			}
			if c.must(200, "GET", "/api/admin/settings", nil)["loan_period_days"].(float64) != 10 {
				t.Error("a setting was lost in the upgrade")
			}
			// the upgraded library works: a new loan
			c.must(200, "POST", "/api/desk/checkout", map[string]string{"patron": "CARD-READER", "code": "FX-0006"})
			p.stop()

			v, after := integrity(t, dir)
			if v != known {
				t.Errorf("schema version after the upgrade is %d, want %d", v, known)
			}
			skip := func(l string) bool {
				return strings.HasPrefix(l, "sessions:") || strings.HasPrefix(l, "schema_migrations:") || strings.HasPrefix(l, "loans:") || strings.HasPrefix(l, "activity:")
			}
			var oldRows, newRows []string
			for _, l := range before {
				if !skip(l) {
					oldRows = append(oldRows, l)
				}
			}
			for _, l := range after {
				if !skip(l) {
					newRows = append(newRows, l)
				}
			}
			// every row the old release held is still there, unchanged (the upgrade may add tables, not alter rows)
			have := map[string]bool{}
			for _, l := range newRows {
				have[l] = true
			}
			for _, l := range oldRows {
				if !strings.HasPrefix(l, "TABLE ") && !have[l] {
					t.Errorf("a row of the old library is gone or changed after the upgrade: %s", l)
				}
			}

			// a second start has nothing to upgrade and makes no second copy
			p2 := start(t, dir, nil)
			p2.stop()
			if got := backupsOf(t, dir); len(got) != 1 {
				t.Errorf("starting an up-to-date library made another copy: %v", got)
			}
		})
	}
}

// DEPLOY-T4 "Failed upgrade": the new release refuses to start, says which
// migration failed and where the copy is, and the data is as it was.
func TestFailedUpgradeRefusesToStartAndChangesNothing(t *testing.T) {
	known := newestMigration(t)
	for _, tc := range []struct{ from, fail int }{{4, 6}, {7, known}} {
		t.Run(fmt.Sprintf("from-v%d-failing-%d", tc.from, tc.fail), func(t *testing.T) {
			if tc.from >= known {
				t.Skipf("the shipped schema is %d: nothing to upgrade from %d", known, tc.from)
			}
			dir := filepath.Join(t.TempDir(), "data")
			buildFixture(t, dir, tc.from)
			pristine := filepath.Join(t.TempDir(), "pristine")
			copyDir(t, dir, pristine)
			before := dumpDB(t, filepath.Join(dir, "library.db"))

			p := launch(t, dir, map[string]string{"JL_TEST_FAIL_MIGRATION": strconv.Itoa(tc.fail)})
			if err := p.waitReady(); err == nil {
				t.Fatal("the server started although its upgrade failed")
			}
			if !p.exited(10 * time.Second) {
				t.Fatal("the server neither started nor stopped")
			}
			if p.err == nil {
				t.Error("the server exited with success after a failed upgrade")
			}
			msg := p.Stderr.String()
			for _, want := range []string{fmt.Sprintf("migration %04d", tc.fail), "failure injected by a test", "Nothing was changed"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the message doesn't say %q:\n%s", want, msg)
				}
			}
			bk := backupsOf(t, dir)
			if len(bk) != 1 {
				t.Fatalf("want one pre-upgrade copy, found %v", bk)
			}
			if !strings.Contains(msg, bk[0]) {
				t.Errorf("the message doesn't name the copy %s:\n%s", bk[0], msg)
			}
			if d := diffLines(before, dumpDB(t, bk[0])); d != "" {
				t.Errorf("the named copy is not the database as it was:\n%s", d)
			}
			v, after := integrity(t, dir)
			if v != tc.from {
				t.Errorf("a failed upgrade left the schema at version %d, want %d", v, tc.from)
			}
			if d := diffLines(before, after); d != "" {
				t.Errorf("a failed upgrade changed the data:\n%s", d)
			}

			if tc.from == 7 {
				olderVersionShowsTheSameLibrary(t, dir, pristine, tc.from)
			}

			// the cause removed, the next start upgrades (a second later: the copy is named by the second)
			time.Sleep(1100 * time.Millisecond)
			p2 := start(t, dir, nil)
			fixtureAdmin(t, p2.Base).must(200, "GET", "/api/books", nil)
			p2.stop()
			if v, _ := integrity(t, dir); v != known {
				t.Errorf("the retried upgrade left version %d, want %d", v, known)
			}
		})
	}
}

// olderVersionShowsTheSameLibrary starts the older release (the migrations it
// had) on the directory the failed upgrade touched and on an untouched copy of
// the fixture, and requires every answer, and the data, to be the same.
func olderVersionShowsTheSameLibrary(t *testing.T, touched, pristine string, version int) {
	t.Helper()
	older := frozenMigrations(t, version)
	view := func(dir string) (map[string]string, []string) {
		s := testutil.StartServer(t, testutil.Options{DataDir: dir, Migrations: older})
		c := newPC(t, s.URL)
		c.signIn(fixtureAdminUser, adminPassword)
		out := export(t, c)
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
		return out, dumpDB(t, filepath.Join(dir, "library.db"), "sessions", "activity")
	}
	want, wantDB := view(pristine)
	got, gotDB := view(touched)
	for _, k := range []string{"activity", "users"} { // sign-in times differ between the two runs
		delete(want, k)
		delete(got, k)
	}
	compareExports(t, want, got)
	if len(want) < 10 {
		t.Errorf("the comparison is too small to prove anything: %d parts", len(want))
	}
	if d := diffLines(wantDB, gotDB); d != "" {
		t.Errorf("the older release finds the library changed by the failed upgrade:\n%s", d)
	}
}

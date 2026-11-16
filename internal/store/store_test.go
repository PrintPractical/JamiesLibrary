package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var ctx = context.Background()

func mig(files ...string) fstest.MapFS {
	m := fstest.MapFS{}
	for i := 0; i+1 < len(files); i += 2 {
		m[files[i]] = &fstest.MapFile{Data: []byte(files[i+1])}
	}
	return m
}

func open(t *testing.T, dir string, opts Options) *Store {
	t.Helper()
	s, err := Open(ctx, dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func hasTable(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestFreshDirectoryIsSetUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "data") // missing, with a missing parent
	s := open(t, dir, Options{Migrations: mig("0001_a.sql", "CREATE TABLE a (x)")})
	for _, p := range []string{"library.db", "lock", "tmp"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s not created: %v", p, err)
		}
	}
	var mode, fk, busy string
	s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode)
	s.DB().QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.DB().QueryRow("PRAGMA busy_timeout").Scan(&busy)
	var temp string
	s.DB().QueryRow("PRAGMA temp_store").Scan(&temp)
	if temp != "2" { // 2 is MEMORY: temporary tables and sorts never need a writable /tmp
		t.Errorf("temp_store=%s, want 2 (memory)", temp)
	}
	if mode != "wal" || fk != "1" || busy != "5000" {
		t.Errorf("pragmas: journal_mode=%s foreign_keys=%s busy_timeout=%s", mode, fk, busy)
	}
	if !hasTable(t, s.DB(), "schema_migrations") {
		t.Error("schema_migrations missing")
	}
}

func TestPermissionsAreOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil { // an existing, too-open directory
		t.Fatal(err)
	}
	old := mig("0001_a.sql", "CREATE TABLE a (x)", "0002_b.sql", "CREATE TABLE b (x)")
	// The second open upgrades, so a backup exists beside the other files.
	open(t, dir, Options{Migrations: mig("0001_a.sql", "CREATE TABLE a (x)")}).Close()
	s := open(t, dir, Options{Migrations: old})
	if _, err := s.DB().Exec("INSERT INTO a VALUES (1)"); err != nil { // the WAL now exists
		t.Fatal(err)
	}
	check := func(path string, want os.FileMode) {
		t.Helper()
		fi, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s is %o, want %o", path, got, want)
		}
	}
	check(dir, 0o700)
	check(filepath.Join(dir, "backups"), 0o700)
	check(filepath.Join(dir, "tmp"), 0o700)
	for _, f := range []string{"library.db", "library.db-wal", "library.db-shm", "lock"} {
		check(filepath.Join(dir, f), 0o600)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "*.db"))
	if len(backups) == 0 {
		t.Fatal("no backup was made")
	}
	for _, b := range backups {
		check(b, 0o600)
	}
}

func TestUnwritableDirectoryIsRefusedNamingThePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 0500 is what prepareDir would leave alone only if it could not chmod; make
	// the parent unwritable instead so MkdirAll of a child fails.
	child := filepath.Join(dir, "child")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	_, err := Open(ctx, child, Options{})
	if err == nil || !strings.Contains(err.Error(), child) {
		t.Fatalf("want an error naming %s, got %v", child, err)
	}
}

func TestReadOnlyFileInPlaceOfDirectoryIsRefused(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ctx, f, Options{})
	if err == nil || !strings.Contains(err.Error(), f) {
		t.Fatalf("want an error naming %s, got %v", f, err)
	}
}

func TestSecondInstanceIsRefused(t *testing.T) {
	dir := t.TempDir()
	first := open(t, dir, Options{})
	_, err := Open(ctx, dir, Options{})
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("want ErrLocked naming %s, got %v", dir, err)
	}
	first.Close()
	again, err := Open(ctx, dir, Options{}) // the lock is given up on Close
	if err != nil {
		t.Fatalf("after Close: %v", err)
	}
	again.Close()
}

func TestUpgradeBacksUpFirstAndTheBackupHasTheOldSchema(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC)
	v1 := mig("0001_things.sql", "CREATE TABLE things (name TEXT)")
	s := open(t, dir, Options{Migrations: v1, Now: func() time.Time { return at }})
	if _, err := s.DB().Exec("INSERT INTO things VALUES ('kept')"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	v2 := mig("0001_things.sql", "CREATE TABLE things (name TEXT)", "0002_more.sql", "CREATE TABLE more (x)")
	s = open(t, dir, Options{Migrations: v2, Now: func() time.Time { return at }})
	if !hasTable(t, s.DB(), "more") {
		t.Fatal("migration 2 not applied")
	}
	backup := filepath.Join(dir, "backups", "pre-1-to-2-20261115T103000Z.db")
	old, err := sql.Open("sqlite", "file:"+backup+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if hasTable(t, old, "more") || count(t, old, "things") != 1 {
		t.Error("backup is not the database as it was before the upgrade")
	}
	var v int
	old.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&v)
	if v != 1 {
		t.Errorf("backup is at version %d, want 1", v)
	}
}

func TestFreshDatabaseAndUpToDateDatabaseMakeNoBackup(t *testing.T) {
	dir := t.TempDir()
	m := mig("0001_a.sql", "CREATE TABLE a (x)")
	open(t, dir, Options{Migrations: m}).Close()
	open(t, dir, Options{Migrations: m}).Close()
	if got, _ := filepath.Glob(filepath.Join(dir, "backups", "*")); len(got) != 0 {
		t.Errorf("unexpected backups: %v", got)
	}
}

func TestFailedMigrationLeavesTheDatabaseAsItWas(t *testing.T) {
	dir := t.TempDir()
	v1 := mig("0001_things.sql", "CREATE TABLE things (name TEXT)")
	s := open(t, dir, Options{Migrations: v1})
	s.DB().Exec("INSERT INTO things VALUES ('one')")
	s.Close()

	bad := mig(
		"0001_things.sql", "CREATE TABLE things (name TEXT)",
		"0002_good.sql", "CREATE TABLE good (x); INSERT INTO things VALUES ('two')",
		"0003_bad.sql", "CREATE TABLE half (x); INSERT INTO no_such_table VALUES (1)",
	)
	_, err := Open(ctx, dir, Options{Migrations: bad})
	var me *MigrationError
	if !errors.As(err, &me) {
		t.Fatalf("want a MigrationError, got %v", err)
	}
	if me.Version != 3 || me.Backup == "" || !strings.Contains(err.Error(), me.Backup) || !strings.Contains(err.Error(), "0003_bad.sql") {
		t.Errorf("error should name the migration and the backup: %v", err)
	}
	if _, statErr := os.Stat(me.Backup); statErr != nil {
		t.Errorf("the named backup does not exist: %v", statErr)
	}

	// The failed attempt released the lock, and the database is exactly the old one.
	s = open(t, dir, Options{Migrations: v1})
	if hasTable(t, s.DB(), "good") || hasTable(t, s.DB(), "half") {
		t.Error("tables from the failed upgrade survived, including migration 2, which had succeeded")
	}
	if count(t, s.DB(), "things") != 1 {
		t.Error("rows changed by the failed upgrade survived")
	}
	var v int
	s.DB().QueryRow("SELECT max(version) FROM schema_migrations").Scan(&v)
	if v != 1 {
		t.Errorf("schema version is %d, want 1", v)
	}
}

func TestFailedFirstMigrationLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(ctx, dir, Options{Migrations: mig("0001_bad.sql", "CREATE TABLE a (x); SELEKT")})
	var me *MigrationError
	if !errors.As(err, &me) || me.Backup != "" {
		t.Fatalf("want a MigrationError without a backup, got %v", err)
	}
	s := open(t, dir, Options{Migrations: mig()})
	if hasTable(t, s.DB(), "a") || hasTable(t, s.DB(), "schema_migrations") {
		t.Error("the failed first upgrade left tables behind")
	}
}

func TestTooNewDatabaseIsRefused(t *testing.T) {
	dir := t.TempDir()
	newer := mig("0001_a.sql", "CREATE TABLE a (x)", "0002_b.sql", "CREATE TABLE b (x)")
	open(t, dir, Options{Migrations: newer}).Close()
	_, err := Open(ctx, dir, Options{Migrations: mig("0001_a.sql", "CREATE TABLE a (x)")})
	var tn *TooNewError
	if !errors.As(err, &tn) || tn.Have != 2 || tn.Known != 1 || !strings.Contains(err.Error(), "older than the data") {
		t.Fatalf("want a TooNewError (2 vs 1), got %v", err)
	}
	if got, _ := filepath.Glob(filepath.Join(dir, "backups", "*")); len(got) != 0 {
		t.Errorf("a refused start must not make backups: %v", got)
	}
}

func TestCopyOfAStoppedDirectoryGivesTheSameData(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	m := mig("0001_things.sql", "CREATE TABLE things (name TEXT)")
	s := open(t, src, Options{Migrations: m})
	for _, n := range []string{"a", "b", "c"} {
		s.DB().Exec("INSERT INTO things VALUES (?)", n)
	}
	s.Close()

	dst := filepath.Join(t.TempDir(), "copy")
	copyTree(t, src, dst)
	c := open(t, dst, Options{Migrations: m})
	rows, err := c.DB().Query("SELECT name FROM things ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		got += n
	}
	if got != "abc" {
		t.Errorf("copy holds %q, want abc", got)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithTx(t *testing.T) {
	s := open(t, t.TempDir(), Options{Migrations: mig("0001_t.sql", "CREATE TABLE t (x INTEGER)")})
	insert := func(tx Tx, n int) error { _, err := tx.Exec("INSERT INTO t VALUES (?)", n); return err }

	if err := s.WithTx(ctx, func(tx Tx) error { return insert(tx, 1) }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := s.WithTx(ctx, func(tx Tx) error { insert(tx, 2); return boom }); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		s.WithTx(ctx, func(tx Tx) error { insert(tx, 3); panic("oops") })
	}()
	if n := count(t, s.DB(), "t"); n != 1 {
		t.Errorf("%d rows, want only the committed one", n)
	}
	// After the panic the write lock was given up.
	if err := s.WithTx(ctx, func(tx Tx) error { return insert(tx, 4) }); err != nil {
		t.Fatalf("a later transaction failed: %v", err)
	}
}

func TestWithTxTakesTheWriteLockAtOnce(t *testing.T) {
	s := open(t, t.TempDir(), Options{Migrations: mig("0001_t.sql", "CREATE TABLE t (x INTEGER)")})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.WithTx(ctx, func(tx Tx) error { close(entered); <-release; return nil })
	}()
	<-entered
	// BEGIN IMMEDIATE means a second transaction cannot even begin (it waits
	// for busy_timeout) while the first holds the write lock, though it only reads.
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	err := s.WithTx(short, func(tx Tx) error { return nil })
	if err == nil {
		t.Error("a second transaction began while the first held the write lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	s := open(t, t.TempDir(), Options{Migrations: mig("0001_fk.sql",
		"CREATE TABLE p (id INTEGER PRIMARY KEY); CREATE TABLE c (p INTEGER REFERENCES p(id))")})
	if _, err := s.DB().Exec("INSERT INTO c VALUES (99)"); err == nil {
		t.Error("a row pointing at nothing was accepted")
	}
}

func TestCleanTmpRemovesOnlyOldFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s := open(t, dir, Options{Now: func() time.Time { return now }})
	write := func(name string, age time.Duration) {
		p := filepath.Join(s.TmpDir(), name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(-age)
		os.Chtimes(p, mt, mt)
	}
	write("old", 2*time.Hour)
	write("fresh", 10*time.Minute)
	os.Mkdir(filepath.Join(s.TmpDir(), "subdir"), 0o700)
	if err := os.Symlink("/nonexistent", filepath.Join(s.TmpDir(), "link")); err != nil {
		t.Fatal(err)
	}
	n, err := s.CleanTmp()
	if err != nil || n != 1 {
		t.Fatalf("CleanTmp = %d, %v; want 1, nil", n, err)
	}
	if _, err := os.Stat(filepath.Join(s.TmpDir(), "old")); !os.IsNotExist(err) {
		t.Error("the old file is still there")
	}
	for _, keep := range []string{"fresh", "subdir"} {
		if _, err := os.Stat(filepath.Join(s.TmpDir(), keep)); err != nil {
			t.Errorf("%s was removed", keep)
		}
	}
}

func TestOpenEmptiesOldTmpFiles(t *testing.T) {
	dir := t.TempDir()
	open(t, dir, Options{}).Close()
	p := filepath.Join(dir, "tmp", "stale")
	os.WriteFile(p, []byte("x"), 0o600)
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(p, old, old)
	open(t, dir, Options{})
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("start-up left an old tmp file")
	}
}

func TestRunTmpCleanerCleansOnATickAndStopsWithItsContext(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	p := filepath.Join(s.TmpDir(), "stale")
	os.WriteFile(p, []byte("x"), 0o600)
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(p, old, old)

	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.RunTmpCleaner(c, 10*time.Millisecond, nil); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cleaner never removed the stale file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cleaner ignored its context")
	}
}

func TestBadMigrationFilesAreRefused(t *testing.T) {
	for name, files := range map[string]fstest.MapFS{
		"bad name":  mig("one.sql", "SELECT 1"),
		"duplicate": mig("0001_a.sql", "SELECT 1", "0001_b.sql", "SELECT 1"),
		"zero":      mig("0000_a.sql", "SELECT 1"),
	} {
		if _, err := Open(ctx, t.TempDir(), Options{Migrations: files}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEmbeddedMigrationsLoad(t *testing.T) {
	// The real directory must parse; domain tasks add their files to it.
	s := open(t, t.TempDir(), Options{})
	if err := s.DB().Ping(); err != nil {
		t.Error(err)
	}
}

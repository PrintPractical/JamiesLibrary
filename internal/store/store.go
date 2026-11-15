// Package store owns the data directory: its lock, the SQLite database in it,
// the numbered migrations that shape the database, and the backup taken before
// an upgrade.
//
// Layout of DATA_DIR (directory 0700, every file 0600):
//
//	lock          held while an instance runs
//	library.db    the database (WAL and SHM files beside it)
//	backups/      pre-<from>-to-<to>-<UTC time>.db, one per upgrade
//	tmp/          short-lived files, emptied of anything over an hour old
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // the database/sql driver "sqlite"
)

// TmpMaxAge is how old a file in tmp/ gets before it is removed.
const TmpMaxAge = time.Hour

// Options change what Open does; the zero value is what the server uses.
type Options struct {
	// Migrations replaces the embedded migrations (tests).
	Migrations fs.FS
	// Now replaces the clock (tests).
	Now func() time.Time
}

// Tx is the transaction a WithTx function works in.
type Tx = *sql.Tx

// Store is an open data directory.
type Store struct {
	dir  string
	db   *sql.DB
	lock *os.File
	now  func() time.Time
}

// Open makes the data directory ready and opens its database: the directory is
// created and checked, locked against a second instance, and the database is
// opened and migrated. Every failure says what is wrong and names the path.
func Open(ctx context.Context, dir string, opts Options) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot use the data directory %q: %w", dir, err)
	}
	dir = abs
	if err := prepareDir(dir); err != nil {
		return nil, err
	}
	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, lock: lock, now: opts.Now}
	if s.now == nil {
		s.now = time.Now
	}
	if err := s.open(ctx, opts); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) open(ctx context.Context, opts Options) error {
	migrations := opts.Migrations
	if migrations == nil {
		sub, err := fs.Sub(embeddedMigrations, "migrations")
		if err != nil {
			return err
		}
		migrations = sub
	}
	all, err := loadMigrations(migrations)
	if err != nil {
		return err
	}
	if err := mkdirPrivate(filepath.Join(s.dir, "tmp")); err != nil {
		return err
	}
	if _, err := s.CleanTmp(); err != nil {
		return err
	}

	// SQLite gives the -wal and -shm files the mode of the database file, so
	// creating it 0600 first keeps all three private whatever the umask is.
	path := filepath.Join(s.dir, "library.db")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return fmt.Errorf("cannot create the database %q: %w", path, err)
	}
	_ = f.Close()
	if err := os.Chmod(path, fileMode); err != nil {
		return fmt.Errorf("cannot restrict %q to its owner (0600): %w", path, err)
	}

	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate") // every transaction starts as BEGIN IMMEDIATE
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("opening the database %q: %w", path, err)
	}
	s.db = db
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("opening the database %q: %w", path, err)
	}
	if err := s.migrate(ctx, all); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, fileMode); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot restrict %q to its owner (0600): %w", path+suffix, err)
		}
	}
	return nil
}

// Dir is the absolute data directory.
func (s *Store) Dir() string { return s.dir }

// DB is the database, for reads outside a transaction.
func (s *Store) DB() *sql.DB { return s.db }

// TmpDir is where short-lived files go.
func (s *Store) TmpDir() string { return filepath.Join(s.dir, "tmp") }

// Close closes the database and gives up the lock. It is safe to call twice.
func (s *Store) Close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
		s.db = nil
	}
	if s.lock != nil {
		_ = s.lock.Close() // closing the file releases the lock
		s.lock = nil
	}
	return err
}

// WithTx runs fn in a transaction that took the write lock when it began
// (BEGIN IMMEDIATE), so concurrent writers queue instead of failing half way.
// It commits when fn returns nil and rolls back when fn returns an error or
// panics; a panic goes on up after the rollback.
func (s *Store) WithTx(ctx context.Context, fn func(Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting a transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// CleanTmp removes files in tmp/ that are over TmpMaxAge old and returns how
// many it removed. Directories and symlinks are left alone.
func (s *Store) CleanTmp() (int, error) {
	dir := s.TmpDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("reading %q: %w", dir, err)
	}
	removed := 0
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // gone already
		}
		if s.now().Sub(info.ModTime()) <= TmpMaxAge {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("removing %q: %w", filepath.Join(dir, e.Name()), err)
		}
		removed++
	}
	return removed, nil
}

// RunTmpCleaner calls CleanTmp every interval until ctx ends. A failure is
// passed to onError and the cleaner carries on.
func (s *Store) RunTmpCleaner(ctx context.Context, interval time.Duration, onError func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.CleanTmp(); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

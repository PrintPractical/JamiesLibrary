package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"jamieslibrary/internal/testhooks"
)

//go:embed migrations
var embeddedMigrations embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// TooNewError means the database was written by a newer program than this one.
type TooNewError struct {
	Have, Known int
}

func (e *TooNewError) Error() string {
	return fmt.Sprintf("this program is older than the data: the database is at schema version %d and this program only knows up to %d. Run a newer Jamie's Library on this data directory", e.Have, e.Known)
}

// MigrationError is a failed upgrade. The database is as it was before.
type MigrationError struct {
	Version int
	Name    string
	Backup  string // "" when there was nothing to back up
	Err     error
}

func (e *MigrationError) Error() string {
	msg := fmt.Sprintf("the upgrade failed at migration %04d (%s): %v. Nothing was changed", e.Version, e.Name, e.Err)
	if e.Backup != "" {
		msg += fmt.Sprintf("; a copy of the data from before the upgrade is at %q", e.Backup)
	}
	return msg
}

func (e *MigrationError) Unwrap() error { return e.Err }

var migrationName = regexp.MustCompile(`^(\d{4,})_[A-Za-z0-9_-]+\.sql$`)

// loadMigrations reads the numbered .sql files in the root of fsys, in order.
// Other files are ignored; a bad or repeated number is an error.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading migrations: %w", err)
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q must be named NNNN_what.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if v < 1 {
			return nil, fmt.Errorf("migration file %q: numbers start at 1", e.Name())
		}
		if other, dup := seen[v]; dup {
			return nil, fmt.Errorf("migration files %q and %q have the same number", other, e.Name())
		}
		seen[v] = e.Name()
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// currentVersion is the highest applied version, 0 for a database that has
// never been migrated.
func currentVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	var v sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// migrate brings the database up to the newest migration. Before it changes
// anything it copies a database that already holds data to backups/, and all
// pending migrations then run in one transaction: all of them apply, or none.
func (s *Store) migrate(ctx context.Context, all []migration) error {
	known := 0
	if len(all) > 0 {
		known = all[len(all)-1].version
	}
	from, err := currentVersion(ctx, s.db)
	if err != nil {
		return fmt.Errorf("reading the schema version: %w", err)
	}
	if from > known {
		return &TooNewError{Have: from, Known: known}
	}
	var pending []migration
	for _, m := range all {
		if m.version > from {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	backup := ""
	if from > 0 {
		backup, err = s.backup(ctx, from, known)
		if err != nil {
			return fmt.Errorf("cannot back up the data before upgrading, so nothing was changed: %w", err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting the upgrade: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return &MigrationError{Version: pending[0].version, Name: "schema_migrations", Backup: backup, Err: err}
	}
	for _, m := range pending {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return &MigrationError{Version: m.version, Name: m.name, Backup: backup, Err: err}
		}
		// A test build can make this migration fail after its statements ran.
		if err := testhooks.FailMigration(m.version); err != nil {
			return &MigrationError{Version: m.version, Name: m.name, Backup: backup, Err: err}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, s.now().UTC().Format(time.RFC3339)); err != nil {
			return &MigrationError{Version: m.version, Name: m.name, Backup: backup, Err: err}
		}
	}
	if err := tx.Commit(); err != nil {
		return &MigrationError{Version: pending[len(pending)-1].version, Name: pending[len(pending)-1].name, Backup: backup, Err: err}
	}
	return nil
}

// backup writes a consistent copy of the database to
// backups/pre-<from>-to-<to>-<UTC time>.db and returns its path. The copy is
// written under a temporary name that is not *.db and given its final name
// only once it is whole and synced, so a killed upgrade leaves no copy or a
// whole one. A name already taken by a finished copy is never overwritten: a
// counter is added to the new name instead.
func (s *Store) backup(ctx context.Context, from, to int) (string, error) {
	dir := filepath.Join(s.dir, "backups")
	if err := mkdirPrivate(dir); err != nil {
		return "", err
	}
	removeLeftoverCopies(dir)
	stem := fmt.Sprintf("pre-%d-to-%d-%s", from, to, s.now().UTC().Format("20060102T150405Z"))
	tmp := filepath.Join(dir, "."+stem+".tmp")
	// VACUUM INTO takes the file name as an SQL string, so it is quoted here.
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO "+quoteSQL(tmp)); err != nil {
		removeCopy(tmp)
		return "", fmt.Errorf("writing %q: %w", tmp, err)
	}
	if err := os.Chmod(tmp, fileMode); err != nil {
		removeCopy(tmp)
		return "", fmt.Errorf("restricting %q to its owner (0600): %w", tmp, err)
	}
	if err := syncFile(tmp); err != nil {
		removeCopy(tmp)
		return "", fmt.Errorf("syncing %q: %w", tmp, err)
	}
	for n := 1; n <= 1000; n++ {
		name := stem + ".db"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.db", stem, n)
		}
		path := filepath.Join(dir, name)
		// A hard link fails when the name exists, so a finished copy is never replaced.
		err := os.Link(tmp, path)
		if err == nil {
			removeCopy(tmp)
			_ = syncDir(dir)
			return path, nil
		}
		if _, statErr := os.Lstat(path); statErr != nil {
			// No hard links here (some FAT, SMB or FUSE mounts): rename, as the name is free.
			if rerr := os.Rename(tmp, path); rerr != nil {
				removeCopy(tmp)
				return "", fmt.Errorf("naming %q: %w", path, rerr)
			}
			_ = syncDir(dir)
			return path, nil
		}
	}
	removeCopy(tmp)
	return "", fmt.Errorf("no free name for a backup in %q", dir)
}

// removeLeftoverCopies deletes temporary copies a killed upgrade left behind.
func removeLeftoverCopies(dir string) {
	tmps, _ := filepath.Glob(filepath.Join(dir, ".pre-*.tmp"))
	for _, t := range tmps {
		removeCopy(t)
	}
}

// removeCopy deletes a temporary copy and the journal SQLite may keep beside it.
func removeCopy(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-journal")
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func quoteSQL(s string) string {
	out := []byte{'\''}
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(append(out, '\''))
}

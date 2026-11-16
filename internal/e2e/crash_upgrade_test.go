package e2e

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// DEPLOY-T4 under kill -9 in the middle of the upgrade, at every moment from
// process start to the library being up: the directory is either the old
// library or the new one, never between; every pre-upgrade copy that exists is a
// whole database; and the next start succeeds.
func TestKillDuringUpgradeLeavesOldOrNewNeverBetween(t *testing.T) {
	known := newestMigration(t)
	for _, from := range fixtureVersions {
		if from >= known {
			continue
		}
		master := filepath.Join(t.TempDir(), "master")
		buildFixture(t, master, from)
		before := dumpDB(t, filepath.Join(master, "library.db"))
		step := time.Duration(rounds(40)/10+1) * time.Millisecond
		for delay := time.Duration(0); delay < 160*time.Millisecond; delay += step {
			t.Run(fmt.Sprintf("v%d-killed-after-%v", from, delay), func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "data")
				copyDir(t, master, dir)
				p := launch(t, dir, nil)
				time.Sleep(delay)
				p.kill()

				v, dump := integrity(t, dir)
				switch v {
				case from:
					if d := diffLines(before, dump); d != "" {
						t.Errorf("killed upgrade changed the old library:\n%s", d)
					}
				case known:
				default:
					t.Fatalf("a killed upgrade left schema version %d: neither the old %d nor the new %d", v, from, known)
				}
				for _, b := range backupsOf(t, dir) {
					if !strings.HasSuffix(b, ".db") {
						continue // SQLite's own side file of a copy being written
					}
					cv, cdump, err := openCopy(t, b)
					if err != nil {
						t.Errorf("a pre-upgrade copy that is not a whole database is left looking like one: %s: %v", filepath.Base(b), err)
					} else if cv != from || diffLines(before, cdump) != "" {
						t.Errorf("a pre-upgrade copy is not the old database: %s (version %d)", filepath.Base(b), cv)
					}
				}

				q := launch(t, dir, nil)
				if err := q.waitReady(); err != nil {
					t.Fatalf("the next start failed after a kill during upgrade: %v", err)
				}
				fixtureAdmin(t, q.Base).must(200, "GET", "/api/books", nil)
				q.stop()
				if v, _ := integrity(t, dir); v != known {
					t.Errorf("after the restart the schema is at %d, want %d", v, known)
				}
			})
		}
	}
}

// openCopy reads a pre-upgrade copy as a database: its schema version and contents, or why it can't be read.
func openCopy(t testing.TB, path string) (int, []string, error) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return 0, nil, err
	}
	defer db.Close()
	var verdict string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&verdict); err != nil {
		return 0, nil, err
	}
	if verdict != "ok" {
		return 0, nil, fmt.Errorf("integrity_check: %s", verdict)
	}
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, nil, fmt.Errorf("no schema version: %w", err)
	}
	return int(v.Int64), dumpOf(t, db), nil
}

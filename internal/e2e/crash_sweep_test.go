package e2e

// kill -9 while covers are being saved, then an immediate restart: the start-up
// sweep of unused images must not remove the cover of a book that was committed.

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestartAfterKillKeepsCoversOfCommittedBooks(t *testing.T) {
	dir, _, _ := crashFixture(t, false)
	p0 := start(t, dir, nil)
	c0 := newPC(t, p0.Base)
	c0.signIn("root", adminPassword)
	ids := bookIDs(t, c0)
	p0.stop()
	for round := 1; round <= rounds(30); round++ {
		p := start(t, dir, nil)
		root := newPC(t, p.Base)
		root.signIn("root", adminPassword)
		stop := make(chan struct{})
		done := make(chan struct{})
		for w := 0; w < 10; w++ {
			go func(w int) {
				defer func() { done <- struct{}{} }()
				for {
					select {
					case <-stop:
						return
					default:
					}
					if rand.Intn(2) == 0 {
						st, m, err := root.bookForm("POST", "/api/books", [][2]string{{"title", fmt.Sprintf("Book %d", rand.Int())}, {"authors", "X"}}, pngCover(rand.Intn(1000)))
						if st != 201 && err == nil {
							t.Log(st, m)
						}
					} else {
						_, _, _ = root.bookForm("PUT", fmt.Sprintf("/api/books/%d", ids[rand.Intn(len(ids))]), [][2]string{{"title", fmt.Sprintf("Edited %d", rand.Int())}, {"authors", "X"}}, pngCover(rand.Intn(1000)))
					}
				}
			}(w)
		}
		sleepRandom()
		p.kill()
		close(stop)
		for w := 0; w < 10; w++ {
			<-done
		}
		// restart at once, as a supervisor would
		q := start(t, dir, nil)
		q.stop()
		t.Logf("round %d: %s", round, strings.TrimSpace(q.Stderr.String()))
		checkCoversPresent(t, dir, round)
		if t.Failed() {
			return
		}
	}
}

func checkCoversPresent(t *testing.T, dir string, round int) {
	t.Helper()
	rows, err := openRO(t, dir).Query(`SELECT title, cover FROM books WHERE cover IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var title, cover string
		_ = rows.Scan(&title, &cover)
		if _, err := os.Stat(filepath.Join(dir, "images", cover)); err != nil {
			t.Errorf("round %d: %q names the cover %s, which is not there", round, title, cover)
		}
	}
}

func sleepRandom() { time.Sleep(time.Duration(5+rand.Intn(80)) * time.Millisecond) }

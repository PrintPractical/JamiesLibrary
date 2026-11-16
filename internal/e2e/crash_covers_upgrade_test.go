package e2e

import (
	"database/sql"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkCovers: every image file is some book's cover or the logo; every cover a book names is a whole image.
func checkCovers(t *testing.T, dir, when string) {
	t.Helper()
	_, _ = integrity(t, dir)
	cp := t.TempDir()
	copyDir(t, dir, cp)
	_ = os.Remove(filepath.Join(cp, "lock"))
	db, err := sql.Open("sqlite", "file:"+filepath.Join(cp, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	used := map[string]bool{}
	rows, err := db.Query(`SELECT cover FROM books WHERE cover IS NOT NULL AND cover != '' UNION SELECT logo_file FROM settings WHERE logo_file != ''`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		used[n] = true
	}
	rows.Close()
	ents, err := os.ReadDir(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	onDisk := map[string]bool{}
	for _, e := range ents {
		onDisk[e.Name()] = true
		if !used[e.Name()] {
			t.Errorf("%s: orphan file images/%s: no book or setting uses it", when, e.Name())
		}
	}
	for n := range used {
		f, err := os.Open(filepath.Join(dir, "images", n))
		if err != nil {
			t.Errorf("%s: a book names the cover %s, which is not there: %v", when, n, err)
			continue
		}
		if _, _, err := image.DecodeConfig(f); err != nil {
			t.Errorf("%s: the cover %s is not a whole image: %v", when, n, err)
		}
		f.Close()
	}
	tmp, _ := filepath.Glob(filepath.Join(dir, "images", ".tmp-*"))
	if len(tmp) > 0 {
		t.Errorf("%s: half-written files left in images/: %v", when, tmp)
	}
}

// CAT-1 / DEPLOY-T3 under kill -9 while covers are saved, replaced and books removed:
// after the next start every image is a book's or the logo's and whole, and no half file is left.
func TestKillDuringCoverSaveLeavesNoOrphanOrHalfImage(t *testing.T) {
	dir, _, _ := crashFixture(t, false)
	work := func(c *pc, rng *rand.Rand, stop <-chan struct{}) {
		var mine []int64
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			switch k := rng.Intn(4); {
			case k == 0 || len(mine) == 0:
				st, b, _ := c.bookForm("POST", "/api/books", [][2]string{{"title", fmt.Sprintf("Cover book %d", rng.Int63())}, {"authors", "Z"}}, pngCover(rng.Intn(1000)))
				if st == 201 {
					mine = append(mine, int64(b["id"].(float64)))
				}
			case k == 1:
				id := mine[rng.Intn(len(mine))]
				_, _, _ = c.bookForm("PUT", fmt.Sprintf("/api/books/%d", id), [][2]string{{"title", "Changed"}, {"authors", "Z"}}, pngCover(rng.Intn(1000)))
			case k == 2:
				j := rng.Intn(len(mine))
				_, _, _ = c.json("DELETE", fmt.Sprintf("/api/books/%d", mine[j]), nil)
				mine = append(mine[:j], mine[j+1:]...)
			default:
				_, _, _ = c.bookForm("POST", "/api/books", [][2]string{{"title", ""}, {"authors", "Z"}}, pngCover(1)) // refused: must leave no file
			}
		}
	}
	killRounds(t, dir, rounds(30), 8, work, func(t *testing.T, dir, when string) {
		// what a kill leaves is removed by the next start, before it serves
		if strings.HasPrefix(when, "round") {
			start(t, dir, nil).stop()
		}
		checkCovers(t, dir, when)
	})
}

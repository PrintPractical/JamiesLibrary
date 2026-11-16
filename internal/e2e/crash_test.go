package e2e

// kill -9 in the middle of writes: whatever the process was doing, the data
// directory it leaves is whole (SQLite's own check passes, nothing points at
// nothing), holds no half-made loan, orphan file or half-switched mode, and the
// next start comes up on it. Each test kills the real process again and again at
// different moments of a busy workload; the moment is random, so the seed is
// logged and the number of rounds is what makes a rare window likely to be hit.

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func rounds(def int) int {
	if v, err := strconv.Atoi(os.Getenv("JL_CRASH_ROUNDS")); err == nil && v > 0 {
		return v
	}
	return def
}

// crashFixture is a library with an administrator, patrons with cards, books with
// copies, and copy codes of the library's own, stopped cleanly, ready for rounds of kills.
func crashFixture(t *testing.T, withISBN bool) (dir string, cards, codes []string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "data")
	p := start(t, dir, nil)
	root := firstAdmin(t, p)
	root.must(200, "PUT", "/api/admin/settings", map[string]any{"max_loans": 100})
	for i := 0; i < 10; i++ {
		fields := [][2]string{{"title", fmt.Sprintf("Crash book %d", i)}, {"authors", "A. Writer"}}
		if withISBN {
			fields = append(fields, [2]string{"isbn", isbn13(i)})
		}
		st, b, err := root.bookForm("POST", "/api/books", fields, pngCover(i))
		if err != nil || st != 201 {
			t.Fatalf("book: %d %v %v", st, b, err)
		}
		for n := 0; n < 3; n++ {
			root.must(201, "POST", fmt.Sprintf("/api/books/%d/copies", int64(b["id"].(float64))), map[string]any{"condition": "good", "location_id": 1})
		}
	}
	if !withISBN {
		root.must(200, "PUT", "/api/admin/copy-id-mode", map[string]string{"mode": "own"})
	}
	for i := 0; i < 4; i++ {
		inv := root.must(201, "POST", "/api/admin/invitations", map[string]string{"role": "patron"})
		c := newPC(t, p.Base)
		c.must(201, "POST", "/api/register", map[string]string{"code": inv["code"].(string), "username": fmt.Sprintf("p%d", i), "display_name": fmt.Sprintf("Patron %d", i), "password": adminPassword})
		cards = append(cards, c.must(200, "GET", "/api/me/card", nil)["number"].(string))
	}
	p.stop()
	if withISBN { // copies of ISBN books have no code of their own in ISBN mode: the book's ISBN is how they are lent
		for i := 0; i < 10; i++ {
			codes = append(codes, isbn13(i))
		}
		return dir, cards, codes
	}
	return dir, cards, ownCodes(t, dir)
}

// isbn13 is a valid ISBN-13 that is different for each n.
func isbn13(n int) string {
	d := fmt.Sprintf("978%09d", 100000+n)
	sum := 0
	for i, c := range d {
		w := 1
		if i%2 == 1 {
			w = 3
		}
		sum += int(c-'0') * w
	}
	return d + strconv.Itoa((10-sum%10)%10)
}

// killRounds runs the workload against a real process and kill -9s it, n times, on one data directory.
// check inspects the directory after each kill (and again after a clean start).
func killRounds(t *testing.T, dir string, n int, workers int, work func(c *pc, rng *rand.Rand, stop <-chan struct{}), check func(t *testing.T, dir string, when string)) {
	t.Helper()
	seed := time.Now().UnixNano()
	t.Logf("seed %d, %d rounds", seed, n)
	rng := rand.New(rand.NewSource(seed))
	for round := 1; round <= n; round++ {
		p := start(t, dir, nil)
		root := newPC(t, p.Base)
		root.signIn("root", adminPassword)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			wr := rand.New(rand.NewSource(rng.Int63()))
			go func() { defer wg.Done(); work(root, wr, stop) }()
		}
		delay := time.Duration(15+rng.Intn(200)) * time.Millisecond
		time.Sleep(delay)
		p.kill()
		close(stop)
		wg.Wait()
		check(t, dir, fmt.Sprintf("round %d, killed after %v", round, delay))
		if t.Failed() {
			return
		}
	}
	// and the library comes up after the last kill and passes the same checks while running
	p := start(t, dir, nil)
	c := newPC(t, p.Base)
	c.signIn("root", adminPassword)
	c.must(200, "GET", "/api/books", nil)
	p.stop()
	check(t, dir, "after a clean restart")
}

// loansHold: no half loan, whatever was happening.
func checkLoans(t *testing.T, dir, when string) {
	t.Helper()
	_, _ = integrity(t, dir) // SQLite's check and foreign keys; fails the test itself
	cp := t.TempDir()
	copyDir(t, dir, cp)
	_ = os.Remove(filepath.Join(cp, "lock"))
	db, err := sql.Open("sqlite", "file:"+filepath.Join(cp, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	one := func(q string) int {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	for _, c := range []struct{ what, q string }{
		{"an open loan on a copy that is not in the catalog", `SELECT count(*) FROM loans l LEFT JOIN copies c ON c.id = l.copy_id WHERE l.returned_at IS NULL AND (c.id IS NULL OR c.removed_at IS NOT NULL)`},
		{"a copy with two open loans", `SELECT count(*) FROM (SELECT copy_id FROM loans WHERE returned_at IS NULL GROUP BY copy_id HAVING count(*) > 1)`},
		{"a returned loan with no one who returned it, or the reverse", `SELECT count(*) FROM loans WHERE (returned_at IS NULL) != (returned_by IS NULL)`},
		{"a loan with no due date or no patron", `SELECT count(*) FROM loans WHERE due_date = '' OR patron_id NOT IN (SELECT id FROM users)`},
		{"a loan returned before it was made", `SELECT count(*) FROM loans WHERE returned_at < checked_out_at`},
	} {
		if n := one(c.q); n != 0 {
			t.Errorf("%s: %d × %s", when, n, c.what)
		}
	}
}

// CIRC-1 / CAT-4 under kill -9 during checkout and check-in.
func TestKillDuringCheckoutLeavesNoHalfLoan(t *testing.T) {
	dir, cards, codes := crashFixture(t, false)
	work := func(c *pc, rng *rand.Rand, stop <-chan struct{}) {
		for {
			select {
			case <-stop:
				return
			default:
			}
			code := codes[rng.Intn(len(codes))]
			if rng.Intn(3) == 0 {
				_, _, _ = c.json("POST", "/api/desk/checkin", map[string]string{"code": code})
			} else {
				_, _, _ = c.json("POST", "/api/desk/checkout", map[string]string{"patron": cards[rng.Intn(len(cards))], "code": code})
			}
		}
	}
	killRounds(t, dir, rounds(25), 6, work, func(t *testing.T, dir, when string) {
		checkLoans(t, dir, when)
	})
	// the loans that exist are the ones the API shows: the available count of every book agrees with the open loans
	p := start(t, dir, nil)
	defer p.stop()
	c := newPC(t, p.Base)
	c.signIn("root", adminPassword)
	open := 0
	if err := openRO(t, dir).QueryRow(`SELECT count(*) FROM loans WHERE returned_at IS NULL`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	avail, total := 0, 0
	for _, id := range bookIDs(t, c) {
		d := c.must(200, "GET", fmt.Sprintf("/api/books/%d", id), nil)
		for _, cp := range d["copies"].([]any) {
			total++
			if cp.(map[string]any)["available"].(bool) {
				avail++
			}
		}
	}
	var made int
	_ = openRO(t, dir).QueryRow(`SELECT count(*) FROM loans`).Scan(&made)
	if made < 10 {
		t.Errorf("only %d loans were ever made: the workload did not exercise checkout", made)
	}
	if total-avail != open {
		t.Errorf("%d copies shown as out but %d open loans", total-avail, open)
	}
}

// checkMode: whichever mode a kill left, copies and codes agree with it.
func checkMode(t *testing.T, dir, when string) {
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
	var mode string
	_ = db.QueryRow(`SELECT copy_id_mode FROM settings WHERE id = 1`).Scan(&mode)
	var bare int
	_ = db.QueryRow(`SELECT count(*) FROM copies c WHERE c.removed_at IS NULL AND NOT EXISTS (SELECT 1 FROM identifiers i WHERE i.copy_id = c.id)`).Scan(&bare)
	if mode == "own" && bare != 0 {
		t.Errorf("%s: the mode is own-barcodes but %d copies have no code", when, bare)
	}
	var loans, gaps int
	_ = db.QueryRow(`SELECT count(*) FROM loans WHERE returned_at IS NULL AND copy_id NOT IN (SELECT id FROM copies WHERE removed_at IS NULL)`).Scan(&loans)
	_ = db.QueryRow(`SELECT count(*) FROM (SELECT book_id, count(*) n, max(number) m FROM copies GROUP BY book_id HAVING n != m)`).Scan(&gaps)
	if loans != 0 {
		t.Errorf("%s: %d open loans lost their copy in a mode switch", when, loans)
	}
	if gaps != 0 {
		t.Errorf("%s: %d books have copy numbers that are not 1..n", when, gaps)
	}
}

// CAT-9 under kill -9 in the middle of a mode switch, while copies are added.
func TestKillDuringModeSwitchLeavesCopiesAndModeAgreeing(t *testing.T) {
	dir, cards, codes := crashFixture(t, true)
	var turn int
	var mu sync.Mutex
	work := func(c *pc, rng *rand.Rand, stop <-chan struct{}) {
		for {
			select {
			case <-stop:
				return
			default:
			}
			switch rng.Intn(4) {
			case 0:
				mu.Lock()
				turn++
				mode := []string{"isbn", "own"}[turn%2]
				mu.Unlock()
				_, _, _ = c.json("PUT", "/api/admin/copy-id-mode", map[string]string{"mode": mode})
			case 1, 2:
				_, _, _ = c.json("POST", fmt.Sprintf("/api/books/%d/copies", 1+rng.Intn(10)), map[string]any{"condition": "good", "location_id": 1})
			default:
				_, _, _ = c.json("POST", "/api/desk/checkout", map[string]string{"patron": cards[rng.Intn(len(cards))], "code": codes[rng.Intn(len(codes))]})
			}
		}
	}
	killRounds(t, dir, rounds(30), 6, work, func(t *testing.T, dir, when string) {
		checkMode(t, dir, when)
		checkLoans(t, dir, when)
	})
}

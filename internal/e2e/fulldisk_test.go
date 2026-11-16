package e2e

// A full disk: an installation whose data folder has almost no room left (a
// size-limited tmpfs in a container) answers every write it can't make with an
// error a person can act on, loses no record, keeps no half-saved cover, and
// keeps serving reads. Skipped unless JL_FULLDISK_URL is set; scripts/fulldisk.sh
// starts the container, runs this test and removes the container.
//
//	JL_FULLDISK_URL        base URL of the library (plain HTTP)
//	JL_FULLDISK_SETUP_CODE the first-run code the container printed
//	JL_FULLDISK_VOLUME     the tmpfs volume mounted at /data, to copy the data out and check it

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"jamieslibrary/internal/testutil"
)

// noisePNG is a picture that does not compress, so each cover costs real space.
func noisePNG(rng *rand.Rand, side int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256)), 255
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestFullDiskGivesACleanErrorAndReadsContinue(t *testing.T) {
	base := strings.TrimRight(os.Getenv("JL_FULLDISK_URL"), "/")
	if base == "" {
		t.Skip("set JL_FULLDISK_URL to a library whose /data is a small tmpfs (scripts/fulldisk.sh does it)")
	}
	volume := os.Getenv("JL_FULLDISK_VOLUME")
	jar, _ := cookiejar.New(nil)
	root := &who{t: t, s: &testutil.Server{URL: base}, c: &http.Client{Jar: jar}}
	root.want(200, "POST", "/api/setup/admin", map[string]string{
		"code": os.Getenv("JL_FULLDISK_SETUP_CODE"), "username": "root", "display_name": "Root", "password": adminPassword})
	rng := rand.New(rand.NewSource(5))

	post := func(title string) reply {
		body, ct := bookMultipart(map[string]string{"title": title, "authors": "Filler"}, "c.png", "image/png", noisePNG(rng, 500))
		return root.raw(rawReq{Method: "POST", Path: "/api/books", Body: body, ContentType: ct})
	}
	first := post("First book, with room")
	if first.Status != 201 {
		t.Fatalf("the first cover on an empty disk: %d %s", first.Status, first.Raw)
	}
	firstCover := root.raw(rawReq{Method: "GET", Path: first.JSON["cover_url"].(string)})
	if firstCover.Status != 200 || len(firstCover.Raw) < 1000 {
		t.Fatalf("the first cover can't be read back: %d, %d bytes", firstCover.Status, len(firstCover.Raw))
	}

	// Fill the disk with covers until one is refused.
	saved := []string{"First book, with room"}
	var failed reply
	for i := 0; i < 200; i++ {
		title := fmt.Sprintf("Filler %03d", i)
		r := post(title)
		if r.Status == 201 {
			saved = append(saved, title)
			continue
		}
		failed = r
		break
	}
	if failed.Status == 0 {
		t.Fatal("200 covers fitted: the disk is not small enough for this test")
	}
	requireCleanError(t, "cover upload on a full disk", failed)

	// Small writes until the database itself cannot grow.
	var smallFail reply
	for i := 0; i < 5000; i++ {
		r := root.do("POST", "/api/locations", map[string]string{"name": fmt.Sprintf("Shelf %04d %s", i, strings.Repeat("x", 80))})
		if r.Status != 201 {
			smallFail = r
			break
		}
	}
	if smallFail.Status == 0 {
		t.Log("the database never ran out of room for small writes; only the cover write was refused")
	} else {
		requireCleanError(t, "database write on a full disk", smallFail)
	}

	// Reads continue: health, the list, a book, a search, the old cover, unchanged.
	root.want(200, "GET", "/api/health", nil)
	list := root.want(200, "GET", "/api/books?q=filler", nil)
	if int(list.JSON["total"].(float64)) != len(saved) { // every book has the author "Filler"
		t.Errorf("the list shows %v books, %d were saved", list.JSON["total"], len(saved))
	}
	if r := root.want(200, "GET", "/api/books?q="+"First", nil); int(r.JSON["total"].(float64)) != 1 {
		t.Errorf("search on a full disk: %s", r.Raw)
	}
	root.want(200, "GET", "/api/books/"+fmt.Sprint(idOf(first, "id")), nil)
	again := root.raw(rawReq{Method: "GET", Path: first.JSON["cover_url"].(string)})
	if again.Status != 200 || again.Raw != firstCover.Raw {
		t.Errorf("the first cover changed or went missing on a full disk (%d)", again.Status)
	}
	// The refused book is not in the list.
	if r := root.want(200, "GET", "/api/books", nil); int(r.JSON["total"].(float64)) != len(saved) {
		t.Errorf("the catalog holds %v books; %d saves succeeded", r.JSON["total"], len(saved))
	}

	if volume == "" {
		return
	}
	// The data left behind: an intact database, and one image for each cover, no more.
	dir := t.TempDir()
	for _, f := range []string{"library.db", "library.db-wal"} {
		data := volumeFile(t, volume, f)
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	imagesOnDisk := strings.Fields(volumeShell(t, volume, "ls /data/images"))
	integrity(t, dir)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT cover FROM books WHERE cover <> ''`)
	if err != nil {
		t.Fatal(err)
	}
	inDB := map[string]bool{}
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		inDB[c] = true
	}
	rows.Close()
	onDisk := map[string]bool{}
	for _, n := range imagesOnDisk {
		onDisk[n] = true
	}
	for n := range onDisk {
		if !inDB[n] {
			t.Errorf("image %s has no book: an orphan left by a refused upload", n)
		}
	}
	for n := range inDB {
		if !onDisk[n] {
			t.Errorf("a book names the image %s, which is not on disk: a half-saved cover", n)
		}
	}
}

// requireCleanError: a refusal a person can act on, in the app's own error
// shape, with no server fault text or paths in it.
func requireCleanError(t *testing.T, what string, r reply) {
	t.Helper()
	if r.Status < 400 || r.Status > 599 {
		t.Errorf("%s: status %d", what, r.Status)
	}
	msg := r.errMessage()
	if msg == "" {
		t.Errorf("%s: no message in the answer (%d): %.300s", what, r.Status, r.Raw)
		return
	}
	low := strings.ToLower(msg)
	if !strings.Contains(low, "space") && !strings.Contains(low, "disk") && !strings.Contains(low, "full") {
		t.Errorf("%s: the message does not say what is wrong: %q", what, msg)
	}
	for _, bad := range []string{"/data", "sqlite", "SQLITE", "goroutine", ".go:", "database or disk is full", "errno"} {
		if strings.Contains(r.Raw, bad) {
			t.Errorf("%s: the answer leaks internals (%q): %s", what, bad, r.Raw)
		}
	}
	t.Logf("%s: %d %q", what, r.Status, msg)
}

// volumeShell runs a shell command in a throwaway container that has the volume at /data, read-only.
func volumeShell(t *testing.T, volume, script string) string {
	t.Helper()
	out, err := exec.Command("docker", "run", "--rm", "-v", volume+":/data:ro", "alpine", "sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("looking in the volume (%s): %v", script, err)
	}
	return string(out)
}

// volumeFile reads a file of the volume. The docker command here is a wrapper
// that mangles binary output and cuts long output, so the file travels as
// base64 in slices.
func volumeFile(t *testing.T, volume, name string) []byte {
	t.Helper()
	const slice = 200_000
	size, err := strconv.Atoi(strings.TrimSpace(volumeShell(t, volume, "stat -c %s /data/"+name)))
	if err != nil {
		t.Fatalf("size of %s: %v", name, err)
	}
	var out []byte
	for off := 0; off < size; off += slice {
		b64 := volumeShell(t, volume, fmt.Sprintf("tail -c +%d /data/%s | head -c %d | base64", off+1, name, slice))
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
		if err != nil {
			t.Fatalf("reading %s from the volume: %v", name, err)
		}
		out = append(out, raw...)
	}
	if len(out) != size {
		t.Fatalf("read %d bytes of %s, which is %d", len(out), name, size)
	}
	return out
}

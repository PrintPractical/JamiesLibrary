package e2e

// CAT-1 and ADMIN-1 at the real interface, with hostile files: the size limits
// to the byte, bytes that are not what the name says, cut-off pictures,
// pictures that would be enormous once opened, SVG, and file names that try to
// leave the images folder. Each refusal is checked three ways: the answer says
// what to do, the book (or logo) is as it was, and the images folder holds no
// new file.

import (
	"bytes"
	"compress/zlib"
	bigendian "encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// bookMultipart is a book form with a cover file part called filename.
func bookMultipart(fields map[string]string, filename, mime string, cover []byte) ([]byte, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_ = mw.WriteField(k, fields[k])
	}
	if cover != nil {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="cover"; filename=%q`, filename))
		h.Set("Content-Type", mime)
		p, _ := mw.CreatePart(h)
		_, _ = p.Write(cover)
	}
	_ = mw.Close()
	return b.Bytes(), mw.FormDataContentType()
}

// imageFiles is the names in DATA/images.
func imageFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// allFiles is every file under dir, relative, so a stray write anywhere shows.
func allFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func bookCount(t *testing.T, w *world) int {
	t.Helper()
	r := w.root.want(200, "GET", "/api/books", nil)
	n, _ := r.JSON["total"].(float64)
	return int(n)
}

// padPNG returns a valid PNG of exactly size bytes: trailing bytes after IEND
// are ignored by every decoder, so the picture is unchanged.
func padPNG(t *testing.T, size int) []byte {
	t.Helper()
	b := pngBytes(color.RGBA{10, 120, 200, 255})
	if len(b) > size {
		t.Fatalf("base PNG is %d bytes", len(b))
	}
	return append(b, make([]byte, size-len(b))...)
}

// bombPNG streams a valid PNG of w x h pixels, 8 or 16 bits per channel RGB, of
// all zeros: a few KB on the wire, a very large picture once opened.
func bombPNG(wpx, hpx, depth int) []byte {
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, data []byte) {
		_ = bigendian.Write(&out, bigendian.BigEndian, uint32(len(data)))
		out.WriteString(kind)
		out.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(data)
		_ = bigendian.Write(&out, bigendian.BigEndian, crc.Sum32())
	}
	ihdr := make([]byte, 13)
	bigendian.BigEndian.PutUint32(ihdr[0:], uint32(wpx))
	bigendian.BigEndian.PutUint32(ihdr[4:], uint32(hpx))
	ihdr[8], ihdr[9] = byte(depth), 2 // RGB
	chunk("IHDR", ihdr)
	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
	row := make([]byte, 1+wpx*3*depth/8)
	for y := 0; y < hpx; y++ {
		_, _ = zw.Write(row)
	}
	_ = zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

func TestHostileCoverUploadsAreRefusedAndLeaveNothing(t *testing.T) {
	w := newWorld(t)
	valid := pngBytes(color.RGBA{200, 30, 30, 255})
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil)

	cases := []struct {
		name, file, mime string
		data             []byte
		status           int
	}{
		{"text labelled png", "cover.png", "image/png", []byte("this is not a picture, it is a letter"), 422},
		{"html labelled jpg", "cover.jpg", "image/jpeg", []byte("<html><script>alert(1)</script></html>"), 422},
		{"svg with script", "cover.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 422},
		{"svg named png", "cover.png", "image/png", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="9" height="9"/>`), 422},
		{"pdf labelled png", "cover.png", "image/png", []byte("%PDF-1.7\n1 0 obj<<>>endobj\n%%EOF"), 422},
		{"three bytes of png magic", "cover.png", "image/png", []byte("\x89PN"), 422},
		{"png magic then nothing", "cover.png", "image/png", []byte("\x89PNG\r\n\x1a\n"), 422},
		{"png cut in half", "cover.png", "image/png", valid[:len(valid)/2], 422},
		{"png cut after the header", "cover.png", "image/png", valid[:40], 422},
		{"jpeg cut in half", "cover.jpg", "image/jpeg", jpg.Bytes()[:jpg.Len()/2], 422},
		{"jpeg magic then junk", "cover.jpg", "image/jpeg", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x41}, 500)...), 422},
		{"zip labelled png", "cover.png", "image/png", append([]byte("PK\x03\x04"), make([]byte, 100)...), 422},
		{"png with a script after IEND is not refused but stripped", "cover.png", "image/png", nil, 201},
		{"49 megapixels", "cover.png", "image/png", bombPNG(7000, 7000, 8), 422},
		{"hundreds of megapixels in a few KB", "cover.png", "image/png", bombPNG(30000, 30000, 8), 422},
		{"16-bit png whose open would hold 200 MB", "cover.png", "image/png", bombPNG(5000, 5000, 16), 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := c.data
			if data == nil { // the polyglot: a real picture with a script behind it
				data = append(append([]byte{}, valid...), []byte("<script>alert('after-iend')</script>")...)
			}
			beforeBooks, beforeFiles := bookCount(t, w), allFiles(t, w.s.DataDir)
			body, ct := bookMultipart(map[string]string{"title": "Hostile " + c.name, "authors": "A. Hacker"}, c.file, c.mime, data)
			start := time.Now()
			r := w.root.raw(rawReq{Method: "POST", Path: "/api/books", Body: body, ContentType: ct})
			if took := time.Since(start); took > 20*time.Second {
				t.Errorf("took %s to answer", took)
			}
			if r.Status != c.status {
				t.Fatalf("want %d, got %d: %.300s", c.status, r.Status, r.Raw)
			}
			if c.status == 201 {
				// Accepted: what is stored is a clean re-encoded picture, not the upload.
				url, _ := r.JSON["cover_url"].(string)
				got := w.root.raw(rawReq{Method: "GET", Path: url})
				if strings.Contains(got.Raw, "<script>") || strings.Contains(got.Raw, "after-iend") {
					t.Errorf("the bytes after the picture were kept")
				}
				if _, err := jpeg.Decode(strings.NewReader(got.Raw)); err != nil {
					if _, err := png.Decode(strings.NewReader(got.Raw)); err != nil {
						t.Errorf("the stored cover is not a picture: %v", err)
					}
				}
				return
			}
			if r.errMessage() == "" {
				t.Errorf("a refusal with no message: %s", r.Raw)
			}
			if n := bookCount(t, w); n != beforeBooks {
				t.Errorf("a refused cover left a book: %d books, was %d", n, beforeBooks)
			}
			if after := allFiles(t, w.s.DataDir); strings.Join(after, "\n") != strings.Join(beforeFiles, "\n") {
				t.Errorf("files changed by a refused upload:\nbefore %v\nafter  %v", beforeFiles, after)
			}
		})
	}

	// The same files as a replacement cover: the book is unchanged.
	t.Run("a bad replacement leaves the book and its cover", func(t *testing.T) {
		before := w.root.want(200, "GET", fmt.Sprintf("/api/books/%d", w.coverBook), nil).Raw
		files := allFiles(t, w.s.DataDir)
		body, ct := bookMultipart(map[string]string{"title": "Changed", "authors": "Nobody"}, "x.png", "image/png", []byte("not a picture"))
		r := w.root.raw(rawReq{Method: "PUT", Path: fmt.Sprintf("/api/books/%d", w.coverBook), Body: body, ContentType: ct})
		if r.Status != 422 {
			t.Fatalf("want 422, got %d: %s", r.Status, r.Raw)
		}
		if after := w.root.want(200, "GET", fmt.Sprintf("/api/books/%d", w.coverBook), nil).Raw; after != before {
			t.Errorf("the book changed:\n%s\n%s", before, after)
		}
		if got := allFiles(t, w.s.DataDir); strings.Join(got, "\n") != strings.Join(files, "\n") {
			t.Errorf("files changed: %v -> %v", files, got)
		}
	})

	// The server is still the same server.
	w.root.want(200, "GET", "/api/health", nil)
}

func TestCoverSizeLimitToTheByte(t *testing.T) {
	w := newWorld(t)
	const limit = 5 << 20
	post := func(size int, title string) (reply, []string) {
		body, ct := bookMultipart(map[string]string{"title": title, "authors": "Edge Case"}, "c.png", "image/png", padPNG(t, size))
		r := w.root.raw(rawReq{Method: "POST", Path: "/api/books", Body: body, ContentType: ct})
		return r, imageFiles(t, w.s.DataDir)
	}
	before := imageFiles(t, w.s.DataDir)
	over, afterOver := post(limit+1, "One byte over")
	if over.Status != 413 || !strings.Contains(over.errMessage(), "5 MB") {
		t.Fatalf("5 MB + 1 byte: want 413 naming the limit, got %d: %.200s", over.Status, over.Raw)
	}
	if strings.Join(afterOver, ",") != strings.Join(before, ",") {
		t.Errorf("the refused cover left a file: %v", afterOver)
	}
	if r := w.root.do("GET", "/api/books?q=One+byte+over", nil); r.JSON["total"].(float64) != 0 {
		t.Errorf("the refused book was saved")
	}
	ok, afterOK := post(limit, "Exactly five")
	if ok.Status != 201 {
		t.Fatalf("exactly 5 MB: want 201, got %d: %.200s", ok.Status, ok.Raw)
	}
	if len(afterOK) != len(before)+1 {
		t.Errorf("exactly 5 MB should save one image: %v -> %v", before, afterOK)
	}
	// 20 MB, as the requirement's own scenario says.
	huge, _ := post(20<<20, "Twenty")
	if huge.Status != 413 {
		t.Fatalf("20 MB: want 413, got %d", huge.Status)
	}
}

func TestLogoSizeLimitToTheByte(t *testing.T) {
	w := newWorld(t)
	const limit = 2 << 20
	put := func(size int) reply {
		body, ct := formFile("logo", "logo.png", padPNG(t, size))
		return w.root.raw(rawReq{Method: "PUT", Path: "/api/admin/branding/logo", Body: body, ContentType: ct})
	}
	before := w.root.want(200, "GET", "/api/library", nil).Raw
	if r := put(limit + 1); r.Status != 413 || !strings.Contains(r.errMessage(), "2 MB") {
		t.Fatalf("2 MB + 1: want 413 naming the limit, got %d: %.200s", r.Status, r.Raw)
	}
	if after := w.root.want(200, "GET", "/api/library", nil).Raw; after != before {
		t.Errorf("a refused logo changed the library:\n%s\n%s", before, after)
	}
	if r := put(limit); r.Status != 200 {
		t.Fatalf("exactly 2 MB: want 200, got %d: %.200s", r.Status, r.Raw)
	}
	for name, data := range map[string][]byte{
		"svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html": []byte("<html></html>"),
		"text": []byte("logo"),
	} {
		body, ct := formFile("logo", "logo.png", data)
		if r := w.root.raw(rawReq{Method: "PUT", Path: "/api/admin/branding/logo", Body: body, ContentType: ct}); r.Status != 422 {
			t.Errorf("%s as a logo: want 422, got %d", name, r.Status)
		}
	}
}

// A picture just under the pixel cap is opened and stored small: memory and
// disk follow the cap, not the upload.
func TestLargeButAllowedPictureIsStoredAtMost1200Pixels(t *testing.T) {
	w := newWorld(t)
	data := bombPNG(6000, 6000, 8) // 36 megapixels
	body, ct := bookMultipart(map[string]string{"title": "A big scan", "authors": "Scanner"}, "scan.png", "image/png", data)
	r := w.root.raw(rawReq{Method: "POST", Path: "/api/books", Body: body, ContentType: ct})
	if r.Status != 201 {
		t.Fatalf("36 megapixels is allowed: got %d: %.300s", r.Status, r.Raw)
	}
	got := w.root.raw(rawReq{Method: "GET", Path: r.JSON["cover_url"].(string)})
	cfg, _, err := image.DecodeConfig(strings.NewReader(got.Raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 1200 || cfg.Height > 1200 {
		t.Errorf("stored cover is %dx%d, over 1200", cfg.Width, cfg.Height)
	}
}

var generatedName = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png)$`)

func TestTraversalFileNamesNeverReachTheDisk(t *testing.T) {
	w := newWorld(t)
	names := []string{
		"../../library.db", `..\..\library.db`, "/etc/passwd", "....//....//x.png",
		"a\x00.png", "cover.png\x00.html", "..%2f..%2fx.png", "%2e%2e/x.png",
		strings.Repeat("a", 5000) + ".png", ".", "..", "",
	}
	valid := pngBytes(color.RGBA{1, 2, 3, 255})
	before := allFiles(t, w.s.DataDir)
	known := map[string]bool{}
	for _, f := range before {
		known[f] = true
	}
	for i, n := range names {
		body, ct := bookMultipart(map[string]string{"title": fmt.Sprintf("Traversal %d", i), "authors": "X"}, n, "image/png", valid)
		r := w.root.raw(rawReq{Method: "POST", Path: "/api/books", Body: body, ContentType: ct})
		if r.Status >= 500 {
			t.Errorf("file name %q: server error %d: %.200s", n, r.Status, r.Raw)
		}
	}
	for _, f := range allFiles(t, w.s.DataDir) {
		if known[f] {
			continue
		}
		dir, name := filepath.Split(f)
		if filepath.Clean(dir) != "images" || !generatedName.MatchString(name) {
			t.Errorf("a file the upload names could have chosen: %s", f)
		}
	}
	// The database was not replaced by the "image".
	w.root.want(200, "GET", "/api/books", nil)
	if got := allFiles(t, w.s.DataDir); !contains(got, "library.db") {
		t.Fatalf("library.db is gone: %v", got)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestMediaPathsCannotLeaveTheImagesFolder(t *testing.T) {
	w := newWorld(t)
	for _, p := range []string{
		"/media/../library.db", "/media/..%2flibrary.db", "/media/%2e%2e/library.db", "/media/..%5clibrary.db",
		"/media/%2e%2e%2f%2e%2e%2flibrary.db", "/media/....//library.db", "/media/library.db",
		"/media/x.jpg%00.png", "/media/" + strings.Repeat("a", 4000), "/media-tmp/..%2f..%2flibrary.db",
		"/media/.", "/media/", "/media/ABCDEF0123456789ABCDEF0123456789.jpg",
	} {
		for _, who := range []*who{w.root, newWho(t, w.s)} {
			r := who.raw(rawReq{Method: "GET", Path: p})
			if r.Status == 200 {
				t.Errorf("GET %s answered 200 (%d bytes)", p, len(r.Raw))
			}
			if strings.HasPrefix(r.Raw, "SQLite format 3") {
				t.Errorf("GET %s served the database", p)
			}
			if r.Status >= 500 {
				t.Errorf("GET %s: %d", p, r.Status)
			}
		}
	}
	// The raw request line, which a Go client would clean up.
	for _, p := range []string{"/media/../library.db", "/media/%2e%2e/%2e%2e/library.db"} {
		if got := rawGet(t, w.s.URL, p); strings.Contains(got, "SQLite format 3") {
			t.Errorf("GET %s over a raw socket served the database", p)
		}
	}
	// A cover token is not a path either.
	for _, tok := range []string{"../library.db", "..%2flibrary.db", "x/../../library.db"} {
		body := map[string]any{"title": "Tok " + tok, "authors": []string{"X"}, "cover_token": tok}
		r := w.root.do("POST", "/api/books", body)
		if r.Status != 422 {
			t.Errorf("cover_token %q: want 422, got %d: %.200s", tok, r.Status, r.Raw)
		}
	}
}

func rawGet(t *testing.T, base, path string) string {
	t.Helper()
	host := strings.TrimPrefix(base, "http://")
	c, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, host)
	b, _ := io.ReadAll(c)
	return string(b)
}

package media

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
)

func newImages(t *testing.T) (*Images, string) {
	t.Helper()
	dir := t.TempDir()
	im, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return im, dir
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngBytes(t *testing.T, w, h int, alpha uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{200, 30, 30, alpha})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// padJPEG makes a valid JPEG of exactly n bytes by adding a comment segment.
func padJPEG(t *testing.T, n int) []byte {
	t.Helper()
	base := jpegBytes(t, 8, 8)
	pad := n - len(base) - 4
	if pad < 0 || pad > 65533 {
		// several comment segments
		var out bytes.Buffer
		out.Write(base[:2])
		left := n - len(base)
		for left > 0 {
			seg := min(left-4, 65533)
			out.Write([]byte{0xFF, 0xFE, byte((seg + 2) >> 8), byte(seg + 2)})
			out.Write(make([]byte, seg))
			left -= seg + 4
		}
		out.Write(base[2:])
		if out.Len() != n {
			t.Fatalf("padded to %d, want %d", out.Len(), n)
		}
		return out.Bytes()
	}
	var out bytes.Buffer
	out.Write(base[:2])
	out.Write([]byte{0xFF, 0xFE, byte((pad + 2) >> 8), byte(pad + 2)})
	out.Write(make([]byte, pad))
	out.Write(base[2:])
	return out.Bytes()
}

func wantCode(t *testing.T, err error, code apperr.Code) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
	return e
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// CAT-1 "Bad cover": a 20 MB file that isn't an image is refused, with a message, and nothing is saved.
func TestBadCoverIsRefusedAndNothingSaved(t *testing.T) {
	im, dir := newImages(t)
	big := bytes.Repeat([]byte("not an image "), 20<<20/13+1)[:20<<20]
	_, err := im.Store(bytes.NewReader(big), CoverLimit, Cover)
	e := wantCode(t, err, apperr.TooLarge)
	if !strings.Contains(e.Message, "5 MB") {
		t.Errorf("message %q", e.Message)
	}
	// Small and not an image.
	_, err = im.Store(strings.NewReader("just some text"), CoverLimit, Cover)
	e = wantCode(t, err, apperr.Invalid)
	if !strings.Contains(e.Message, "JPEG, PNG or WebP") {
		t.Errorf("message %q", e.Message)
	}
	if n := filesIn(t, dir); len(n) != 0 {
		t.Errorf("files left behind: %v", n)
	}
}

// CAT-1: a cover of exactly 5 MB is accepted; one byte more is refused.
func TestCoverSizeBoundary(t *testing.T) {
	im, dir := newImages(t)
	name, err := im.Store(bytes.NewReader(padJPEG(t, CoverLimit)), CoverLimit, Cover)
	if err != nil {
		t.Fatalf("5 MB exactly: %v", err)
	}
	if !ValidName(name) {
		t.Errorf("name %q", name)
	}
	_, err = im.Store(bytes.NewReader(padJPEG(t, CoverLimit+1)), CoverLimit, Cover)
	wantCode(t, err, apperr.TooLarge)
	if n := filesIn(t, dir); len(n) != 1 {
		t.Errorf("files = %v, want just the first", n)
	}
}

// The type comes from the bytes: a PNG called .jpg is stored as the PNG it is.
func TestTypeComesFromTheBytes(t *testing.T) {
	im, dir := newImages(t)
	// A logo with transparency keeps being a PNG, whatever the upload was called.
	name, err := im.Store(bytes.NewReader(pngBytes(t, 20, 20, 128)), LogoLimit, Logo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, ".png") || ContentType(name) != "image/png" {
		t.Errorf("name %q", name)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "images", name))
	if err != nil || !bytes.HasPrefix(raw, []byte("\x89PNG")) {
		t.Errorf("stored bytes are not a PNG: %v", err)
	}
	// An opaque PNG as a cover becomes a JPEG.
	name, err = im.Store(bytes.NewReader(pngBytes(t, 20, 20, 255)), CoverLimit, Cover)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "images", name))
	if !strings.HasSuffix(name, ".jpg") || !bytes.HasPrefix(raw, []byte{0xFF, 0xD8}) {
		t.Errorf("name %q, bytes % x", name, raw[:4])
	}
}

// A WebP is accepted (the decoder is registered) and stored as a JPEG.
func TestWebPIsAccepted(t *testing.T) {
	im, _ := newImages(t)
	webp, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	name, err := im.Store(bytes.NewReader(webp), CoverLimit, Cover)
	if err != nil {
		t.Fatalf("webp: %v", err)
	}
	if !strings.HasSuffix(name, ".jpg") {
		t.Errorf("name %q", name)
	}
}

func TestSVGHTMLAndOtherTypesAreRefused(t *testing.T) {
	im, dir := newImages(t)
	cases := map[string]string{
		"svg":   `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script></svg>`,
		"html":  `<!doctype html><html><script>alert(1)</script></html>`,
		"gif":   "GIF89a\x01\x00\x01\x00\x00\x00\x00;",
		"pdf":   "%PDF-1.4 hello",
		"empty": "",
	}
	for name, body := range cases {
		_, err := im.Store(strings.NewReader(body), CoverLimit, Cover)
		e := wantCode(t, err, apperr.Invalid)
		if !strings.Contains(e.Message, "JPEG, PNG or WebP") {
			t.Errorf("%s: message %q", name, e.Message)
		}
	}
	if n := filesIn(t, dir); len(n) != 0 {
		t.Errorf("files left behind: %v", n)
	}
}

// A damaged image that has a good header is refused, saving nothing.
func TestTruncatedImageIsRefused(t *testing.T) {
	im, dir := newImages(t)
	j := jpegBytes(t, 100, 100)
	_, err := im.Store(bytes.NewReader(j[:len(j)/2]), CoverLimit, Cover)
	wantCode(t, err, apperr.Invalid)
	if n := filesIn(t, dir); len(n) != 0 {
		t.Errorf("files left behind: %v", n)
	}
}

func chunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	c := crc32.NewIEEE()
	c.Write([]byte(typ))
	c.Write(data)
	binary.Write(&b, binary.BigEndian, c.Sum32())
	return b.Bytes()
}

// A 50-megapixel header is refused from the header alone: the body is cut off,
// so a refusal that mentions pixels (not "damaged") proves no decode was tried.
func TestOverFortyMegapixelsRefusedBeforeDecoding(t *testing.T) {
	im, dir := newImages(t)
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 10000)
	binary.BigEndian.PutUint32(ihdr[4:], 5000)
	ihdr[8], ihdr[9] = 8, 2
	data := append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", ihdr)...)
	_, err := im.Store(bytes.NewReader(data), CoverLimit, Cover)
	e := wantCode(t, err, apperr.Invalid)
	if !strings.Contains(e.Message, "40 megapixels") {
		t.Errorf("message %q", e.Message)
	}
	if n := filesIn(t, dir); len(n) != 0 {
		t.Errorf("files left behind: %v", n)
	}
}

// Re-encoding drops EXIF, GPS included.
func TestEXIFGPSIsGoneAfterReencoding(t *testing.T) {
	im, dir := newImages(t)
	j := jpegBytes(t, 40, 40)
	exif := append([]byte("Exif\x00\x00"), []byte("GPSLatitude 51.5007 GPSLongitude -0.1246 SECRETCAMERA")...)
	var withEXIF bytes.Buffer
	withEXIF.Write(j[:2])
	withEXIF.Write([]byte{0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)})
	withEXIF.Write(exif)
	withEXIF.Write(j[2:])
	if _, err := jpeg.Decode(bytes.NewReader(withEXIF.Bytes())); err != nil {
		t.Fatalf("test image: %v", err)
	}
	name, err := im.Store(&withEXIF, CoverLimit, Cover)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "images", name))
	for _, s := range []string{"Exif", "GPS", "SECRETCAMERA"} {
		if bytes.Contains(raw, []byte(s)) {
			t.Errorf("stored image still holds %q", s)
		}
	}
}

func TestCoverIsScaledToAtMost1200(t *testing.T) {
	im, dir := newImages(t)
	name, err := im.Store(bytes.NewReader(jpegBytes(t, 2400, 1800)), CoverLimit, Cover)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(filepath.Join(dir, "images", name))
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width != 1200 || cfg.Height != 900 {
		t.Errorf("size %dx%d, err %v; want 1200x900", cfg.Width, cfg.Height, err)
	}
	// A small one is left at its size.
	name, _ = im.Store(bytes.NewReader(jpegBytes(t, 300, 200)), CoverLimit, Cover)
	f2, _ := os.Open(filepath.Join(dir, "images", name))
	defer f2.Close()
	if cfg, _, _ := image.DecodeConfig(f2); cfg.Width != 300 || cfg.Height != 200 {
		t.Errorf("small cover became %dx%d", cfg.Width, cfg.Height)
	}
}

func TestFilesAreRandomPrivateAndLeaveNoTemporaries(t *testing.T) {
	im, dir := newImages(t)
	a, err := im.Store(bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := im.Store(bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover)
	if a == b {
		t.Error("two stores gave one name")
	}
	if st, _ := os.Stat(filepath.Join(dir, "images", a)); st.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Join(dir, "images")); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
	if n := filesIn(t, dir); len(n) != 2 {
		t.Errorf("files %v", n)
	}
}

func TestDelete(t *testing.T) {
	im, dir := newImages(t)
	name, _ := im.Store(bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover)
	if err := im.Delete(name); err != nil {
		t.Fatal(err)
	}
	if err := im.Delete(name); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if n := filesIn(t, dir); len(n) != 0 {
		t.Errorf("files %v", n)
	}
	for _, bad := range []string{"../library.db", "a/b.jpg", "", "x.jpg"} {
		if err := im.Delete(bad); err == nil {
			t.Errorf("Delete(%q) succeeded", bad)
		}
	}
}

func TestValidName(t *testing.T) {
	ok := strings.Repeat("a1", 16)
	for name, want := range map[string]bool{
		ok + ".jpg": true, ok + ".png": true,
		ok: false, ok + ".svg": false, ok + ".jpg.png": false, "../" + ok + ".jpg": false,
		strings.ToUpper(ok) + ".jpg": false, ok[:30] + ".jpg": false, ok + ".jpg\n": false,
	} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v", name, !want)
		}
	}
}

// bigPNG16 is a w by h 16-bit PNG of one flat colour: a few hundred KB on the
// wire, 8 bytes a pixel once decoded. colorType 2 is RGB, 6 is RGBA.
func bigPNG16(t *testing.T, w, h int, colorType byte) []byte {
	t.Helper()
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = 16, colorType
	bpp := 6
	if colorType == 6 {
		bpp = 8
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	row := make([]byte, 1+w*bpp)
	for i := 1; i < len(row); i++ {
		row[i] = 0x80
	}
	for y := 0; y < h; y++ {
		zw.Write(row)
	}
	zw.Close()
	out := append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", ihdr)...)
	out = append(out, chunk("IDAT", z.Bytes())...)
	return append(out, chunk("IEND", nil)...)
}

// peakSys runs f and returns the most memory the Go runtime held from the OS
// at any moment, the whole test binary included (the runtime keeps what it
// freed, so a later case can't hide behind an earlier one).
func peakSys(f func()) uint64 {
	runtime.GC()
	debug.FreeOSMemory()
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.Sys > peak.Load() {
				peak.Store(m.Sys)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	f()
	close(stop)
	<-done
	return peak.Load()
}

// The worst accepted images (40 megapixels at 8 bits, 20 at 16 bits: 160 MB
// decoded) are stored with the whole test process (earlier tests' heap included) under 512 MB, one at a time, so a
// Raspberry Pi with 1 GB survives.
func TestWorstCaseImageMemoryIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes 40 megapixels")
	}
	const bound = 512 << 20
	for _, tc := range []struct {
		name string
		kind Kind
		ct   byte
	}{{"cover rgb", Cover, 2}, {"cover rgba", Cover, 6}, {"logo rgb", Logo, 2}, {"logo rgba", Logo, 6}} {
		t.Run(tc.name, func(t *testing.T) {
			im, _ := newImages(t)
			data := bigPNG16(t, 5000, 4000, tc.ct)
			if len(data) > CoverLimit {
				t.Fatalf("test image is %d bytes", len(data))
			}
			var err error
			peak := peakSys(func() { _, err = im.Store(bytes.NewReader(data), CoverLimit, tc.kind) })
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("peak %d MB", peak>>20)
			if peak > bound {
				t.Errorf("peak %d MB over the %d MB bound", peak>>20, bound>>20)
			}
		})
	}
}

// A 16-bit PNG over 20 megapixels is refused before it is decoded (the body is
// a valid 50-megapixel PNG; only the header cap can refuse it this cheaply).
func TestHuge16BitPNGIsRefusedBeforeDecoding(t *testing.T) {
	im, dir := newImages(t)
	data := bigPNG16(t, 8000, 5000, 6)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	func() {
		_, err := im.Store(bytes.NewReader(data), CoverLimit, Cover)
		e := wantCode(t, err, apperr.Invalid)
		if !strings.Contains(e.Message, "20 megapixels") {
			t.Errorf("message %q", e.Message)
		}
	}()
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 20<<20 {
		t.Errorf("refusal allocated %d MB", got>>20)
	}
	if n := filesIn(t, dir); len(n) != 0 {
		t.Errorf("files left behind: %v", n)
	}
	// The same size at 8 bits is within the 40-megapixel cap and is not refused for its depth.
	if isPNG16(pngBytes(t, 4, 4, 255)) {
		t.Error("an 8-bit PNG was taken for 16-bit")
	}
}

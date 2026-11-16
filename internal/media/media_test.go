package media

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
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
	_, err := im.Store(context.Background(), bytes.NewReader(big), CoverLimit, Cover)
	e := wantCode(t, err, apperr.TooLarge)
	if !strings.Contains(e.Message, "5 MB") {
		t.Errorf("message %q", e.Message)
	}
	// Small and not an image.
	_, err = im.Store(context.Background(), strings.NewReader("just some text"), CoverLimit, Cover)
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
	name, err := im.Store(context.Background(), bytes.NewReader(padJPEG(t, CoverLimit)), CoverLimit, Cover)
	if err != nil {
		t.Fatalf("5 MB exactly: %v", err)
	}
	if !ValidName(name) {
		t.Errorf("name %q", name)
	}
	_, err = im.Store(context.Background(), bytes.NewReader(padJPEG(t, CoverLimit+1)), CoverLimit, Cover)
	wantCode(t, err, apperr.TooLarge)
	if n := filesIn(t, dir); len(n) != 1 {
		t.Errorf("files = %v, want just the first", n)
	}
}

// The type comes from the bytes: a PNG called .jpg is stored as the PNG it is.
func TestTypeComesFromTheBytes(t *testing.T) {
	im, dir := newImages(t)
	// A logo with transparency keeps being a PNG, whatever the upload was called.
	name, err := im.Store(context.Background(), bytes.NewReader(pngBytes(t, 20, 20, 128)), LogoLimit, Logo)
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
	name, err = im.Store(context.Background(), bytes.NewReader(pngBytes(t, 20, 20, 255)), CoverLimit, Cover)
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
	name, err := im.Store(context.Background(), bytes.NewReader(webp), CoverLimit, Cover)
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
		_, err := im.Store(context.Background(), strings.NewReader(body), CoverLimit, Cover)
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
	_, err := im.Store(context.Background(), bytes.NewReader(j[:len(j)/2]), CoverLimit, Cover)
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
	_, err := im.Store(context.Background(), bytes.NewReader(data), CoverLimit, Cover)
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
	name, err := im.Store(context.Background(), &withEXIF, CoverLimit, Cover)
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
	name, err := im.Store(context.Background(), bytes.NewReader(jpegBytes(t, 2400, 1800)), CoverLimit, Cover)
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
	name, _ = im.Store(context.Background(), bytes.NewReader(jpegBytes(t, 300, 200)), CoverLimit, Cover)
	f2, _ := os.Open(filepath.Join(dir, "images", name))
	defer f2.Close()
	if cfg, _, _ := image.DecodeConfig(f2); cfg.Width != 300 || cfg.Height != 200 {
		t.Errorf("small cover became %dx%d", cfg.Width, cfg.Height)
	}
}

func TestFilesAreRandomPrivateAndLeaveNoTemporaries(t *testing.T) {
	im, dir := newImages(t)
	a, err := im.Store(context.Background(), bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := im.Store(context.Background(), bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover)
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
	name, _ := im.Store(context.Background(), bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover)
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
	type tcase struct {
		name string
		kind Kind
		data func(t *testing.T) []byte
	}
	var cases []tcase
	for _, k := range []struct {
		n    string
		kind Kind
	}{{"cover", Cover}, {"logo", Logo}} {
		kind := k.kind
		for _, ct := range []byte{2, 6} {
			ct := ct
			cases = append(cases, tcase{fmt.Sprintf("%s png16 type %d", k.n, ct), kind,
				func(t *testing.T) []byte { return bigPNG16(t, 5000, 4000, ct) }})
		}
		for _, f := range []string{"progressive-cmyk-8mp.jpg", "progressive-444-10mp.jpg"} {
			f := f
			cases = append(cases, tcase{k.n + " " + f, kind, func(t *testing.T) []byte { return testdata(t, f) }})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			im, _ := newImages(t)
			data := tc.data(t)
			if len(data) > LogoLimit {
				t.Fatalf("test image is %d bytes", len(data))
			}
			var err error
			peak := peakSys(func() { _, err = im.Store(context.Background(), bytes.NewReader(data), LogoLimit, tc.kind) })
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

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A progressive JPEG keeps 256 bytes of coefficients per 8x8 block of each
// component: a 40-megapixel CMYK one (611 KB on the wire) would hold ~800 MB.
// Both are refused from the header alone: the header is all that is sent.
func TestProgressiveJPEGsOverTheDecodeBudgetAreRefusedFromTheHeader(t *testing.T) {
	im, dir := newImages(t)
	for _, f := range []string{"progressive-cmyk-40mp.jpg", "progressive-420-40mp.jpg"} {
		data := testdata(t, f)
		for name, body := range map[string][]byte{"whole": data, "header only": data[:700]} {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := im.Store(context.Background(), bytes.NewReader(body), LogoLimit, Cover)
			runtime.ReadMemStats(&after)
			e := wantCode(t, err, apperr.Invalid)
			if !strings.Contains(e.Message, "too large to open safely") || !strings.Contains(e.Message, "megapixels") {
				t.Errorf("%s %s: message %q", f, name, e.Message)
			}
			if got := after.TotalAlloc - before.TotalAlloc; got > 20<<20 {
				t.Errorf("%s %s: refusal allocated %d MB", f, name, got>>20)
			}
		}
	}
	if n := filesIn(t, dir); len(n) != 0 {
		t.Errorf("files left behind: %v", n)
	}
}

// decodeCost counts what the header says: coefficient blocks for a progressive
// JPEG, none for a baseline one.
func TestDecodeCost(t *testing.T) {
	cmyk := testdata(t, "progressive-cmyk-8mp.jpg")
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(cmyk))
	if got := decodeCost(cmyk, cfg); got < 150<<20 || got > MaxDecodeBytes {
		t.Errorf("progressive CMYK 8 MP cost %d MB", got>>20)
	}
	base := jpegBytes(t, 1000, 1000)
	cfg, _, _ = image.DecodeConfig(bytes.NewReader(base))
	if got := decodeCost(base, cfg); got != 4_000_000 {
		t.Errorf("baseline cost %d", got)
	}
	if _, ok := jpegCoefficientBytes([]byte{0xFF, 0xD8, 0xFF}); ok {
		t.Error("a cut-off header was read")
	}
	if _, known := jpegCoefficientBytes(base); !known {
		t.Error("a baseline header was not recognised")
	}
}

// The decoder skips stray bytes before a marker, so the estimate must not be
// fooled by them into costing a progressive file as baseline: one byte before
// the frame header, or an FF 00, still gets the file refused.
func TestStrayBytesBeforeTheFrameHeaderDoNotHideAProgressiveJPEG(t *testing.T) {
	data := testdata(t, "progressive-cmyk-40mp.jpg")
	sof := bytes.Index(data, []byte{0xFF, 0xC2})
	if sof < 0 {
		t.Fatal("no SOF2 in the test file")
	}
	for name, stray := range map[string][]byte{"one zero": {0x00}, "FF 00": {0xFF, 0x00}, "text": []byte("junk"), "FF FF": {0xFF, 0xFF}} {
		mod := append(append(append([]byte{}, data[:sof]...), stray...), data[sof:]...)
		if _, _, err := image.DecodeConfig(bytes.NewReader(mod)); err != nil {
			t.Fatalf("%s: the decoder itself refuses it (%v): the test proves nothing", name, err)
		}
		im, dir := newImages(t)
		_, err := im.Store(context.Background(), bytes.NewReader(mod), LogoLimit, Logo)
		e := wantCode(t, err, apperr.Invalid)
		if !strings.Contains(e.Message, "too large to open safely") {
			t.Errorf("%s: message %q", name, e.Message)
		}
		if n := filesIn(t, dir); len(n) != 0 {
			t.Errorf("%s: stored %v", name, n)
		}
	}
	// A clean file the estimate can't follow is costed at its worst, not waved through:
	// the 8 MP progressive CMYK file is within budget, and 8 MP at 20 B/px still is.
	ok := testdata(t, "progressive-cmyk-8mp.jpg")
	sof = bytes.Index(ok, []byte{0xFF, 0xC2})
	mod := append(append(append([]byte{}, ok[:sof]...), 0), ok[sof:]...)
	im, _ := newImages(t)
	if _, err := im.Store(context.Background(), bytes.NewReader(mod), LogoLimit, Logo); err != nil {
		t.Errorf("8 MP with a stray byte: %v", err)
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
		_, err := im.Store(context.Background(), bytes.NewReader(data), CoverLimit, Cover)
		e := wantCode(t, err, apperr.Invalid)
		if !strings.Contains(e.Message, "too large to open safely") {
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

// webpWithAlpha is a 16x16 lossy WebP (VP8X, ALPH, VP8) whose alpha plane is a
// everywhere. The VP8 chunk is a solid red picture.
func webpWithAlpha(t *testing.T, a byte) []byte {
	t.Helper()
	vp8, err := base64.StdEncoding.DecodeString("VlA4IC4AAACwAQCdASoQABAAAUAmJaACdLoABDAAAP7x3I/4DdfFtMv/vYL/3YL/3YL/WwAA")
	if err != nil {
		t.Fatal(err)
	}
	alph := append([]byte("ALPH\x01\x01\x00\x00\x00"), bytes.Repeat([]byte{a}, 256)...) // 1 + 256 bytes, uncompressed
	alph = append(alph, 0)                                                              // pad to even
	vp8x := []byte("VP8X\x0a\x00\x00\x00\x10\x00\x00\x00\x0f\x00\x00\x0f\x00\x00")      // alpha flag, 16x16
	body := append(append(append([]byte("WEBP"), vp8x...), alph...), vp8...)
	out := append([]byte("RIFF\x00\x00\x00\x00"), body...)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return out
}

// A lossy WebP with alpha decodes to NYCbCrA: a logo that uses it is kept as
// PNG, and one whose alpha is fully opaque is flattened to JPEG.
func TestLogoWebPAlpha(t *testing.T) {
	for _, tc := range []struct {
		alpha byte
		ext   string
	}{{0x80, ".png"}, {0xff, ".jpg"}} {
		data := webpWithAlpha(t, tc.alpha)
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("alpha %#x: decode: %v", tc.alpha, err)
		}
		if _, ok := img.(*image.NYCbCrA); !ok {
			t.Fatalf("alpha %#x decodes to %T, want *image.NYCbCrA", tc.alpha, img)
		}
		im, _ := newImages(t)
		name, err := im.Store(context.Background(), bytes.NewReader(data), LogoLimit, Logo)
		if err != nil {
			t.Fatalf("alpha %#x: %v", tc.alpha, err)
		}
		if !strings.HasSuffix(name, tc.ext) {
			t.Errorf("alpha %#x stored as %q, want %s", tc.alpha, name, tc.ext)
		}
	}
}

// hasAlpha on NYCbCrA reads the alpha plane without boxing a colour a pixel:
// the one translucent pixel is the last, so every other pixel is looked at first.
func TestHasAlphaNYCbCrAAllocatesNothing(t *testing.T) {
	img := image.NewNYCbCrA(image.Rect(0, 0, 500, 500), image.YCbCrSubsampleRatio420)
	for i := range img.A {
		img.A[i] = 0xff
	}
	if hasAlpha(img) {
		t.Fatal("opaque reported as transparent")
	}
	img.A[img.AStride*499+499] = 0xfe
	var found bool
	if n := testing.AllocsPerRun(3, func() { found = hasAlpha(img) }); n > 0 {
		t.Errorf("%v allocations", n)
	}
	if !found {
		t.Error("one translucent pixel missed")
	}
}

// With the decode slot held, a cancelled Store returns at once, saves nothing
// and leaves the slot as it found it.
func TestStoreLeavesTheQueueWhenCancelled(t *testing.T) {
	im, dir := newImages(t)
	im.sem <- struct{}{} // the slot is taken
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := im.Store(ctx, bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("returned before cancel: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled Store is still waiting")
	}
	if len(im.sem) != 1 {
		t.Errorf("slot count %d, want 1 (still the holder's)", len(im.sem))
	}
	<-im.sem
	if ents, _ := os.ReadDir(filepath.Join(dir, "images")); len(ents) != 0 {
		t.Errorf("%d files saved", len(ents))
	}
	// An already-cancelled context never starts, even with a free slot.
	if _, err := im.Store(ctx, bytes.NewReader(jpegBytes(t, 10, 10)), CoverLimit, Cover); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled before start: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "images")); len(ents) != 0 {
		t.Errorf("%d files saved", len(ents))
	}
}

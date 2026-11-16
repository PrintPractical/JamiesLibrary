package httpapi

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// paddedJPEG makes a valid JPEG of exactly n bytes by adding comment segments.
func paddedJPEG(t *testing.T, n int) []byte {
	t.Helper()
	base := smallJPEG(t)
	var out bytes.Buffer
	out.Write(base[:2])
	for left := n - len(base); left > 0; {
		seg := min(left-4, 65533)
		if seg < 0 {
			t.Fatalf("cannot pad a JPEG to %d bytes", n)
		}
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

// paddedPNG makes a valid PNG of exactly n bytes with one ancillary chunk after the header.
func paddedPNG(t *testing.T, n int) []byte {
	t.Helper()
	base := pngBytes(t)
	const head = 8 + 25 // signature and IHDR
	pad := n - len(base) - 12
	if pad < 0 {
		t.Fatalf("cannot pad a PNG to %d bytes", n)
	}
	var chunk bytes.Buffer
	binary.Write(&chunk, binary.BigEndian, uint32(pad))
	chunk.WriteString("paDd")
	chunk.Write(make([]byte, pad))
	binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(chunk.Bytes()[4:]))
	out := append(append(append([]byte{}, base[:head]...), chunk.Bytes()...), base[head:]...)
	if len(out) != n {
		t.Fatalf("padded to %d, want %d", len(out), n)
	}
	return out
}

const (
	twoMB  = 2 * 1024 * 1024 // the literal limits, not media's constants, so changing those fails here
	fiveMB = 5 * 1024 * 1024
)

// ADMIN-1 "Bad logo": a valid PNG or JPEG over 2 MB is refused as too large and
// nothing changes; one just under 2 MB is accepted.
func TestLogoTwoMBLimit(t *testing.T) {
	s := newSettingsSite(t)
	pad := map[string]func(*testing.T, int) []byte{"png": paddedPNG, "jpeg": paddedJPEG}
	for kind, make := range pad {
		entries := s.logTotal()
		for _, size := range []int{twoMB + 1, 3 << 20} {
			rec := s.upload(s.admin, make(t, size))
			code, field, msg := errField(t, rec)
			if code != 413 || field != "logo" || !strings.Contains(msg, "2 MB") {
				t.Errorf("%s of %d bytes: %d %q %q", kind, size, code, field, msg)
			}
			if b := s.library(); b.LogoURL != "" || s.logTotal() != entries || s.images() != 0 {
				t.Errorf("%s of %d bytes changed something: %+v, %d entries (was %d), %d files", kind, size, b, s.logTotal(), entries, s.images())
			}
		}
		if rec := s.upload(s.admin, make(t, twoMB-1)); rec.Code != 200 {
			t.Errorf("%s just under 2 MB: %d %s", kind, rec.Code, rec.Body)
		}
		if s.library().LogoURL == "" {
			t.Errorf("%s just under 2 MB was not saved", kind)
		}
		s.req("DELETE", "/api/admin/branding/logo", s.admin, "")
	}
}

// CAT-1 "Bad cover": a valid image over 5 MB is refused as too large and
// nothing is saved; one just under 5 MB is accepted.
func TestCoverFiveMBLimit(t *testing.T) {
	c := newCatSite(t)
	fields := [][2]string{{"title", "Book"}, {"authors", "A"}}
	for _, size := range []int{fiveMB + 1, 6 << 20} {
		rec := c.form("POST", "/api/books", c.lib, fields, paddedJPEG(t, size))
		if rec.Code != 413 || !strings.Contains(errMessage(t, rec), "5 MB") {
			t.Errorf("%d bytes: %d %s", size, rec.Code, rec.Body)
		}
		if c.count("books") != 0 || c.images() != 0 {
			t.Errorf("%d bytes saved %d books and %d images", size, c.count("books"), c.images())
		}
	}
	rec := c.form("POST", "/api/books", c.lib, fields, paddedJPEG(t, fiveMB-1))
	if rec.Code != 201 && rec.Code != 200 {
		t.Fatalf("just under 5 MB: %d %s", rec.Code, rec.Body)
	}
	if c.count("books") != 1 || c.images() != 1 {
		t.Errorf("saved %d books and %d images, want 1 and 1", c.count("books"), c.images())
	}
}

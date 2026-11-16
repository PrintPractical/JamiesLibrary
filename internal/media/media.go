// Package media validates, re-encodes and stores the images users upload: book
// covers and the library logo (security design §9, architecture §8).
//
// Images live in DATA_DIR/images/ (directory 0700, files 0600) under random
// names the server chooses. Nothing a user sends survives into a stored file:
// every image is decoded and encoded again, which drops EXIF (phone GPS
// included) and anything hidden in the file.
package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers WebP for image.DecodeConfig and image.Decode

	"jamieslibrary/internal/apperr"
)

// Limits (security design §9, architecture §8).
const (
	CoverLimit = 5 << 20 // CAT-1: a cover is at most 5 MB
	LogoLimit  = 2 << 20 // ADMIN-1: the logo is at most 2 MB

	MaxPixels      = 40_000_000 // refused from the header, before any decoding
	MaxDecodeBytes = 160 << 20  // what a decode may hold, estimated from the header
	MaxCoverSide   = 1200       // covers are scaled down to this on the long side
	JPEGQuality    = 85
	maxConcurrent  = 1 // decodes at once: with the pixel caps a decode holds at most 160 MB, so the process peaks near 350 MB
)

// Kind says what an image is for.
type Kind int

const (
	// Cover is a book cover: scaled to MaxCoverSide, stored as JPEG.
	Cover Kind = iota
	// Logo is the library's logo: stored as PNG when it has transparency, else JPEG.
	Logo
)

// nameRE is the form of every name Store returns. Nothing else is ever opened.
var nameRE = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png)$`)

// ValidName reports whether name has the form Store generates: no path, no dots but the extension.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Images is the image directory.
type Images struct {
	dir string
	sem chan struct{}
}

// Open prepares DATA_DIR/images/ and returns the store for it.
func Open(dataDir string) (*Images, error) {
	dir := filepath.Join(dataDir, "images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create %q: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot restrict %q to its owner (0700): %w", dir, err)
	}
	return &Images{dir: dir, sem: make(chan struct{}, maxConcurrent)}, nil
}

func refuse(msg string) error { return apperr.New(apperr.Invalid, msg) }

const notAnImage = "That file isn't a JPEG, PNG or WebP image. Choose a different file."

// Store reads at most limit bytes from r, checks that they are a JPEG, PNG or
// WebP picture (by the bytes, never by name), re-encodes it and saves it under a
// new random name, which it returns. A refused image saves nothing. If ctx ends
// while the upload waits for its turn to decode, Store gives up with ctx's
// error, saving nothing.
func (im *Images) Store(ctx context.Context, r io.Reader, limit int64, kind Kind) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return "", fmt.Errorf("reading the upload: %w", err)
	}
	if int64(len(data)) > limit {
		return "", apperr.New(apperr.TooLarge, fmt.Sprintf("That image is larger than %d MB. Choose a smaller one.", limit>>20))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", refuse(notAnImage)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return "", refuse("That image has too many pixels (the limit is 40 megapixels). Choose a smaller one.")
	}

	if cost := decodeCost(data, cfg); cost > MaxDecodeBytes {
		return "", refuse(tooBigToOpen(cfg, cost))
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case im.sem <- struct{}{}:
		defer func() { <-im.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", refuse("That image is damaged or can't be read. Choose a different file.")
	}
	out, ext, err := encode(src, kind)
	if err != nil {
		return "", fmt.Errorf("encoding an image: %w", err)
	}
	return im.save(out, ext)
}

// decodeCost estimates, from the header alone, the bytes a full decode holds.
// The standard decoders keep more than the picture: a 16-bit PNG is 8 bytes a
// pixel, and a progressive JPEG keeps a 256-byte coefficient block for every
// 8x8 block of every component until it is done. Everything else is taken as 4.
func decodeCost(data []byte, cfg image.Config) int64 {
	px := int64(cfg.Width) * int64(cfg.Height)
	switch {
	case isPNG16(data):
		return px * 8
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8}):
		if coeff, known := jpegCoefficientBytes(data); known {
			return px*4 + coeff
		}
		// The decoder skips stray bytes the estimate doesn't follow, so a
		// header it can't read cleanly is costed as the worst a decode can be:
		// progressive, four components at full sampling.
		return px * (4 + 4*4)
	}
	return px * 4
}

// isPNG16 reports whether data is a PNG whose header says 16 bits a channel.
func isPNG16(data []byte) bool {
	return len(data) > 24 && bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) && data[24] == 16
}

// jpegCoefficientBytes reads the frame header of a progressive JPEG (SOF2) and
// returns the coefficient storage the decoder will allocate for it, in bytes.
// A baseline frame (SOF0, SOF1) keeps none. It reports known only for a frame
// header found by walking the segments with nothing unexpected between them;
// anything else (stray bytes, a cut-off file, another frame type) is unknown
// and the caller must assume the worst: the check fails closed.
func jpegCoefficientBytes(data []byte) (int64, bool) {
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 0, false
		}
		m := data[i+1]
		if m == 0xFF { // fill byte
			i++
			continue
		}
		if m == 0x01 || (m >= 0xD0 && m <= 0xD8) { // markers without a length
			i += 2
			continue
		}
		n := int(data[i+2])<<8 | int(data[i+3])
		if m == 0xC2 { // SOF2: progressive
			seg := data[i+4:]
			if n < 8 || len(seg) < n-2 {
				return 0, false
			}
			h, w, nc := int(seg[1])<<8|int(seg[2]), int(seg[3])<<8|int(seg[4]), int(seg[5])
			if nc < 1 || nc > 4 || n < 8+3*nc {
				return 0, false
			}
			hmax, vmax := 1, 1
			for c := 0; c < nc; c++ {
				hmax, vmax = max(hmax, int(seg[6+3*c+1]>>4)), max(vmax, int(seg[6+3*c+1]&15))
			}
			mx, my := (int64(w)+int64(8*hmax)-1)/int64(8*hmax), (int64(h)+int64(8*vmax)-1)/int64(8*vmax)
			var blocks int64
			for c := 0; c < nc; c++ {
				blocks += mx * int64(seg[6+3*c+1]>>4) * my * int64(seg[6+3*c+1]&15)
			}
			return blocks * 256, true
		}
		if m == 0xC0 || m == 0xC1 {
			return 0, true // baseline: no coefficient storage
		}
		if m >= 0xC3 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			return 0, false // a frame type this code doesn't cost
		}
		if m == 0xDA { // the scan started with no frame header before it
			return 0, false
		}
		i += 2 + n
	}
	return 0, false
}

// tooBigToOpen is the refusal for an image whose decode would hold too much.
func tooBigToOpen(cfg image.Config, cost int64) string {
	px := int64(cfg.Width) * int64(cfg.Height)
	allowed := px * MaxDecodeBytes / max(cost, 1) / 1_000_000
	return fmt.Sprintf("That image is too large to open safely: %d megapixels, and this kind of file (16-bit PNG, progressive or CMYK JPEG) can be at most about %d. Save it as an ordinary JPEG or PNG, or scale it down, and try again.",
		px/1_000_000, allowed)
}

func encode(src image.Image, kind Kind) ([]byte, string, error) {
	var buf bytes.Buffer
	if kind == Logo {
		if hasAlpha(src) {
			if err := png.Encode(&buf, src); err != nil {
				return nil, "", err
			}
			return buf.Bytes(), ".png", nil
		}
		return encodeJPEG(&buf, flatten(src, src.Bounds().Dx(), src.Bounds().Dy()))
	}
	// A cover is scaled first, so the flattened copy is at most 1200 px a side
	// however large the decoded picture is.
	w, h := fit(src.Bounds(), MaxCoverSide)
	return encodeJPEG(&buf, flatten(src, w, h))
}

func encodeJPEG(buf *bytes.Buffer, img image.Image) ([]byte, string, error) {
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ".jpg", nil
}

// fit is the size b is scaled to so its long side is at most maxSide.
func fit(b image.Rectangle, maxSide int) (w, h int) {
	w, h = b.Dx(), b.Dy()
	if long := max(w, h); long > maxSide {
		w, h = max(1, w*maxSide/long), max(1, h*maxSide/long)
	}
	return w, h
}

// flatten draws src, scaled to w by h, onto white as an 8-bit image: JPEG has
// no transparency, and a 16-bit decode (8 bytes a pixel) is not kept around.
func flatten(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if b := src.Bounds(); b.Dx() == w && b.Dy() == h {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	}
	return dst
}

// hasAlpha reports whether any pixel is not fully opaque. The decoders' own
// types are scanned as bytes: At on a 40-megapixel image allocates per pixel.
func hasAlpha(img image.Image) bool {
	switch m := img.(type) {
	case *image.NRGBA:
		return anyAlpha(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy(), 4, 3, 1)
	case *image.RGBA:
		return anyAlpha(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy(), 4, 3, 1)
	case *image.NRGBA64:
		return anyAlpha(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy(), 8, 6, 2)
	case *image.RGBA64:
		return anyAlpha(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy(), 8, 6, 2)
	case *image.NYCbCrA: // a lossy WebP with alpha: the alpha plane is its own byte slice
		return anyAlpha(m.A, m.AStride, m.Rect.Dx(), m.Rect.Dy(), 1, 0, 1)
	}
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return false
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return true
			}
		}
	}
	return false
}

// anyAlpha scans the alpha bytes of w by h pixels of bpp bytes each, whose
// alpha starts at off and is n bytes of 0xff when opaque.
func anyAlpha(pix []byte, stride, w, h, bpp, off, n int) bool {
	for y := 0; y < h; y++ {
		row := pix[y*stride : y*stride+w*bpp]
		for x := off; x < len(row); x += bpp {
			for k := 0; k < n; k++ {
				if row[x+k] != 0xff {
					return true
				}
			}
		}
	}
	return false
}

// save writes data to a new random name in the directory: a temporary file in
// the same directory, synced, then renamed, so a reader never sees half a file.
func (im *Images) save(data []byte, ext string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("making an image name: %w", err)
	}
	name := hex.EncodeToString(raw[:]) + ext
	tmp, err := os.CreateTemp(im.dir, ".tmp-*") // created 0600
	if err != nil {
		return "", fmt.Errorf("saving an image: %w", err)
	}
	cleanup := func(err error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("saving an image: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(im.dir, name)); err != nil {
		return cleanup(err)
	}
	return name, nil
}

// ErrNotFound is returned by Open for a name that is invalid or has no file.
var ErrNotFound = errors.New("image not found")

// Open returns the stored image, or ErrNotFound. A name not in the generated form is never touched on disk.
func (im *Images) Open(name string) (*os.File, error) {
	if !ValidName(name) {
		return nil, ErrNotFound
	}
	f, err := os.Open(filepath.Join(im.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// ContentType is the type the server serves name with, from the name it chose.
func ContentType(name string) string {
	if filepath.Ext(name) == ".png" {
		return "image/png"
	}
	return "image/jpeg"
}

// Delete removes a stored image. A name already gone is not an error; a name not in the generated form is refused.
func (im *Images) Delete(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("%q is not an image name", name)
	}
	if err := os.Remove(filepath.Join(im.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting an image: %w", err)
	}
	return nil
}

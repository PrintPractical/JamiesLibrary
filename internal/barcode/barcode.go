// Package barcode renders codes: SVG for screens and PDF label sheets for
// printing. It is pure rendering and knows nothing of the database.
package barcode

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	bb "github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/boombuler/barcode/ean"
	"github.com/boombuler/barcode/qr"
)

// Kind is a symbology.
type Kind string

const (
	Code128 Kind = "code128"
	EAN13   Kind = "ean13"
	QR      Kind = "qr"
)

const (
	maxCode128Len = 32
	maxQRLen      = 1024

	// Quiet zones, in modules: the blank margin a scanner needs around a code.
	quietZone1D = 10
	quietZoneQR = 4

	// Height of the bars of a one-dimensional code, in modules.
	barHeightModules = 40
)

// ErrInvalid is wrapped by every error for input that can't be encoded.
var ErrInvalid = errors.New("invalid barcode input")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// NormalizeEAN13 returns the 13 digits of an ISBN-13 given with or without
// hyphens or spaces, or an error when it isn't one with a correct check digit.
func NormalizeEAN13(s string) (string, error) {
	digits := strings.NewReplacer("-", "", " ", "").Replace(s)
	if len(digits) != 13 {
		return "", invalid("an ISBN-13 has 13 digits")
	}
	sum := 0
	for i, r := range digits {
		if r < '0' || r > '9' {
			return "", invalid("an ISBN-13 has digits only")
		}
		w := 1
		if i%2 == 1 {
			w = 3
		}
		sum += int(r-'0') * w
	}
	if sum%10 != 0 {
		return "", invalid("the ISBN-13 check digit is wrong")
	}
	if !strings.HasPrefix(digits, "978") && !strings.HasPrefix(digits, "979") {
		return "", invalid("an ISBN-13 starts with 978 or 979")
	}
	return digits, nil
}

// encode validates content for kind and returns its module matrix.
func encode(kind Kind, content string) (*matrix, error) {
	var (
		bc  bb.Barcode
		err error
	)
	switch kind {
	case Code128:
		if content == "" || len(content) > maxCode128Len {
			return nil, invalid("a Code 128 value has 1 to %d characters", maxCode128Len)
		}
		for i := 0; i < len(content); i++ {
			if content[i] < 0x20 || content[i] > 0x7e {
				return nil, invalid("a Code 128 value has printable ASCII characters only")
			}
		}
		bc, err = code128.Encode(content)
	case EAN13:
		var digits string
		if digits, err = NormalizeEAN13(content); err != nil {
			return nil, err
		}
		bc, err = ean.Encode(digits)
	case QR:
		if content == "" || len(content) > maxQRLen {
			return nil, invalid("a QR value has 1 to %d bytes", maxQRLen)
		}
		bc, err = qr.Encode(content, qr.M, qr.Auto)
	default:
		return nil, invalid("unknown code kind %q", string(kind))
	}
	if err != nil {
		return nil, invalid("%v", err)
	}
	return newMatrix(kind, bc), nil
}

// matrix is a code as rows of dark and light modules.
type matrix struct {
	w, h  int
	oneD  bool
	quiet int
	cells []bool // h rows of w; for a 1D code a single row
}

func newMatrix(kind Kind, bc bb.Barcode) *matrix {
	b := bc.Bounds()
	m := &matrix{w: b.Dx(), h: b.Dy(), oneD: kind != QR, quiet: quietZoneQR}
	if m.oneD {
		m.h = 1
		m.quiet = quietZone1D
	}
	m.cells = make([]bool, m.w*m.h)
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			m.cells[y*m.w+x] = bc.At(b.Min.X+x, b.Min.Y+y) == color.Black
		}
	}
	return m
}

// runs calls fn for each horizontal run of dark modules in row y.
func (m *matrix) runs(y int, fn func(x, n int)) {
	for x := 0; x < m.w; {
		if !m.cells[y*m.w+x] {
			x++
			continue
		}
		start := x
		for x < m.w && m.cells[y*m.w+x] {
			x++
		}
		fn(start, x-start)
	}
}

package httpapi

import (
	"image"
	"image/color"
	"regexp"
	"strconv"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"
	"github.com/makiuchi-d/gozxing/qrcode"

	"jamieslibrary/internal/perm"
)

var (
	svgRect = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v(\d+)h-\d+z`)
	svgBox  = regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`)
)

// decodeSVG draws the SVG the way a scanner would see it and reads it with r.
func decodeSVG(t *testing.T, svg string, r gozxing.Reader) (string, error) {
	t.Helper()
	const scale = 4
	b := svgBox.FindStringSubmatch(svg)
	if b == nil {
		t.Fatalf("no viewBox in %.80s", svg)
	}
	w, _ := strconv.Atoi(b[1])
	h, _ := strconv.Atoi(b[2])
	img := image.NewGray(image.Rect(0, 0, w*scale, h*scale))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for _, m := range svgRect.FindAllStringSubmatch(svg, -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		rw, _ := strconv.Atoi(m[3])
		rh, _ := strconv.Atoi(m[4])
		for py := y * scale; py < (y+rh)*scale; py++ {
			for px := x * scale; px < (x+rw)*scale; px++ {
				img.SetGray(px, py, color.Gray{0})
			}
		}
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Decode(bmp, nil)
	if err != nil {
		return "", err
	}
	return res.GetText(), nil
}

// CARD-1 "View card": the barcode and QR code a patron is shown decode to the
// number on their card, and each patron's decode to their own.
func TestCardImagesDecodeToTheCardNumber(t *testing.T) {
	s := newSite(t)
	s.addUser("pat", goodPassword, perm.Patron)
	s.addUser("quinn", goodPassword, perm.Patron)
	cookies := map[string]string{"pat": s.signIn("pat", goodPassword), "quinn": s.signIn("quinn", goodPassword)}
	readers := map[string]gozxing.Reader{"barcode": oned.NewCode128Reader(), "qr": qrcode.NewQRCodeReader()}

	numbers := map[string]string{}
	for who, cookie := range cookies {
		number := s.cardOf(cookie)
		numbers[who] = number
		for kind, reader := range readers {
			rec := s.req("GET", "/api/me/card/"+kind+".svg", cookie, "")
			if rec.Code != 200 {
				t.Fatalf("%s %s: %d %s", who, kind, rec.Code, rec.Body)
			}
			got, err := decodeSVG(t, rec.Body.String(), reader)
			if err != nil || got != number {
				t.Errorf("%s %s decodes to %q (%v), card is %q", who, kind, got, err, number)
			}
		}
	}
	if numbers["pat"] == numbers["quinn"] {
		t.Fatal("two patrons share a card")
	}
}

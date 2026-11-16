// Package barcodetest reads back what internal/barcode draws, so a test can
// prove that an image says what it is meant to say. It is for tests only.
package barcodetest

import (
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strconv"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// Decode reads img with the reader for kind ("code128", "ean13" or "qr") and
// returns the text it encodes.
func Decode(kind string, img image.Image) (string, error) {
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	var r gozxing.Reader
	switch kind {
	case "code128":
		r = oned.NewCode128Reader()
	case "ean13":
		r = oned.NewEAN13Reader()
	case "qr":
		r = qrcode.NewQRCodeReader()
	default:
		return "", fmt.Errorf("barcodetest: unknown kind %q", kind)
	}
	res, err := r.Decode(bmp, nil)
	if err != nil {
		return "", err
	}
	return res.GetText(), nil
}

var (
	svgPath = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v(\d+)h-\d+z`)
	svgBox  = regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`)
)

// RasterSVG draws an SVG made by internal/barcode, scale pixels per module.
func RasterSVG(svg string, scale int) (image.Image, error) {
	b := svgBox.FindStringSubmatch(svg)
	if b == nil {
		return nil, fmt.Errorf("barcodetest: no viewBox in %.80s", svg)
	}
	w, _ := strconv.Atoi(b[1])
	h, _ := strconv.Atoi(b[2])
	img := image.NewGray(image.Rect(0, 0, w*scale, h*scale))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for _, m := range svgPath.FindAllStringSubmatch(svg, -1) {
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
	return img, nil
}

// DecodeSVG is RasterSVG then Decode, at a scale every reader copes with.
func DecodeSVG(kind, svg string) (string, error) {
	img, err := RasterSVG(svg, 4)
	if err != nil {
		return "", err
	}
	return Decode(kind, img)
}

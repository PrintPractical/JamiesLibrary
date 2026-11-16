// Package barcodetest reads back what internal/barcode draws, so a test can
// prove that an image says what it is meant to say. It is for tests only.
package barcodetest

import (
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"

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

// decodeScales are the pixel sizes DecodeSVG tries, in order.
var decodeScales = []int{4, 2, 1, 6, 3, 8}

// DecodeSVG is RasterSVG then Decode. The reader's finder-pattern search is
// sensitive to the pixel scale: about one QR in 300 fails at one scale while
// the same modules decode at another, so it tries several and fails only when
// none reads the code. The image's modules are the same at every scale.
func DecodeSVG(kind, svg string) (string, error) {
	var text string
	var err error
	for _, scale := range decodeScales {
		img, rerr := RasterSVG(svg, scale)
		if rerr != nil {
			return "", rerr
		}
		if text, err = Decode(kind, img); err == nil {
			return text, nil
		}
	}
	return text, err
}

// PDFPage is what a PDF made by internal/barcode shows on one page.
type PDFPage struct {
	Width, Height float64  // mm
	Texts         []string // each line of text drawn, in drawing order
	Bars          int      // filled rectangles: the bars of every code
}

var (
	pdfPageObj = regexp.MustCompile(`/Type /Page[^s]`)
	pdfBox     = regexp.MustCompile(`/MediaBox \[0 0 ([\d.]+) ([\d.]+)\]`)
	pdfStream  = regexp.MustCompile(`(?s)stream\n(.*?)endstream`)
	pdfText    = regexp.MustCompile(`\(((?:[^()\\]|\\.)*)\) Tj`)
	pdfRect    = regexp.MustCompile(`-?[\d.]+ -?[\d.]+ -?[\d.]+ -?[\d.]+ re f`)
)

// ReadPDF reads back an uncompressed PDF drawn by internal/barcode: its pages'
// sizes, text and bars. The text is read from the 16-bit characters the PDF
// holds, so only text in the Basic Latin to Latin Extended range is read right.
func ReadPDF(pdf []byte) ([]PDFPage, error) {
	s := string(pdf)
	if !strings.HasPrefix(s, "%PDF-") {
		return nil, fmt.Errorf("barcodetest: not a PDF")
	}
	box := pdfBox.FindStringSubmatch(s)
	if box == nil {
		return nil, fmt.Errorf("barcodetest: no MediaBox")
	}
	w, _ := strconv.ParseFloat(box[1], 64)
	h, _ := strconv.ParseFloat(box[2], 64)
	n := len(pdfPageObj.FindAllString(s, -1))
	streams := pdfStream.FindAllStringSubmatch(s, -1)
	if len(streams) < n {
		return nil, fmt.Errorf("barcodetest: %d pages but %d content streams", n, len(streams))
	}
	pages := make([]PDFPage, n)
	for i := range pages {
		p := PDFPage{Width: w / 72 * 25.4, Height: h / 72 * 25.4, Bars: len(pdfRect.FindAllString(streams[i][1], -1))}
		for _, m := range pdfText.FindAllStringSubmatch(streams[i][1], -1) {
			p.Texts = append(p.Texts, decodePDFText(m[1]))
		}
		pages[i] = p
	}
	return pages, nil
}

// decodePDFText unescapes a PDF string and reads its UTF-16BE characters.
func decodePDFText(s string) string {
	var raw []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				raw = append(raw, '\n')
			case 'r':
				raw = append(raw, '\r')
			case 't':
				raw = append(raw, '\t')
			case '0', '1', '2', '3', '4', '5', '6', '7':
				j := i
				for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
					j++
				}
				v, _ := strconv.ParseUint(s[i:j], 8, 8)
				raw = append(raw, byte(v))
				i = j - 1
			default:
				raw = append(raw, s[i])
			}
			continue
		}
		raw = append(raw, s[i])
	}
	var out []rune
	for i := 0; i+1 < len(raw); i += 2 {
		out = append(out, rune(raw[i])<<8|rune(raw[i+1]))
	}
	return string(out)
}

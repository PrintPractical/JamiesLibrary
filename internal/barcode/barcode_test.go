package barcode

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"
	"github.com/makiuchi-d/gozxing/qrcode"
	"golang.org/x/image/font/gofont/goregular"
)

// decode reads an image with the reader for kind and returns the text.
func decode(t *testing.T, kind Kind, img image.Image) (string, error) {
	t.Helper()
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	var r gozxing.Reader
	switch kind {
	case Code128:
		r = oned.NewCode128Reader()
	case EAN13:
		r = oned.NewEAN13Reader()
	default:
		r = qrcode.NewQRCodeReader()
	}
	res, err := r.Decode(bmp, nil)
	if err != nil {
		return "", err
	}
	return res.GetText(), nil
}

var svgPath = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v(\d+)h-\d+z`)
var svgBox = regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`)

// rasterSVG draws the SVG our renderer produced, scale pixels per module.
func rasterSVG(t *testing.T, svg string, scale int) image.Image {
	t.Helper()
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
	return img
}

func TestSVGDecodesBackToItsInput(t *testing.T) {
	cases := []struct {
		kind    Kind
		content string
	}{
		{Code128, "123456789012"},
		{Code128, "C000042"},
		{Code128, "Hello, World ~!"},
		{Code128, strings.Repeat("A", 32)},
		{Code128, "x"},
		{EAN13, "9780306406157"},
		{EAN13, "978-0-306-40615-7"},
		{QR, "123456789012"},
		{QR, "https://library.example/card?x=1&y=<2>"},
	}
	for _, c := range cases {
		t.Run(string(c.kind)+"/"+c.content, func(t *testing.T) {
			svg, err := SVG(c.kind, c.content)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(c.content, "-", "")
			if c.kind == Code128 || c.kind == QR {
				want = c.content
			}
			got, err := decode(t, c.kind, rasterSVG(t, svg, 4))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != want {
				t.Fatalf("decoded %q, want %q", got, want)
			}
		})
	}
}

func TestSVGIsScaledByTheViewer(t *testing.T) {
	for _, k := range []Kind{Code128, EAN13, QR} {
		content := map[Kind]string{Code128: "ABC123", EAN13: "9780306406157", QR: "ABC123"}[k]
		svg, err := SVG(k, content)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(svg, "viewBox=") || strings.Contains(svg, " width=") && !strings.Contains(svg, `<rect width=`) {
			t.Errorf("%s: want a viewBox and no fixed size: %.120s", k, svg)
		}
		root := svg[:strings.Index(svg, ">")]
		if strings.Contains(root, "width=") || strings.Contains(root, "height=") {
			t.Errorf("%s: root element has a fixed size: %s", k, root)
		}
		if !strings.Contains(svg, `fill="#fff"`) {
			t.Errorf("%s: no white background", k)
		}
	}
}

func TestSVGQuietZone(t *testing.T) {
	svg, _ := SVG(Code128, "A")
	// The first dark module starts after the quiet zone.
	first := svgPath.FindStringSubmatch(svg)
	if x, _ := strconv.Atoi(first[1]); x < quietZone1D {
		t.Errorf("1D bars start at %d, inside the %d-module quiet zone", x, quietZone1D)
	}
	svg, _ = SVG(QR, "A")
	for _, m := range svgPath.FindAllStringSubmatch(svg, -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		if x < quietZoneQR || y < quietZoneQR {
			t.Fatalf("QR module at %d,%d is inside the quiet zone", x, y)
		}
	}
}

func TestSVGRefusesInvalidInput(t *testing.T) {
	long := strings.Repeat("A", 33)
	cases := []struct {
		name    string
		kind    Kind
		content string
	}{
		{"code128 empty", Code128, ""},
		{"code128 too long", Code128, long},
		{"code128 non-ASCII", Code128, "café"},
		{"code128 control", Code128, "a\nb"},
		{"code128 DEL", Code128, "a\x7f"},
		{"ean13 short", EAN13, "978030640615"},
		{"ean13 letters", EAN13, "97803064061X7"},
		{"ean13 bad check digit", EAN13, "9780306406158"},
		{"ean13 not an ISBN prefix", EAN13, "4006381333931"},
		{"ean13 isbn-10", EAN13, "0306406152"},
		{"qr empty", QR, ""},
		{"qr too long", QR, strings.Repeat("a", maxQRLen+1)},
		{"unknown kind", Kind("pdf417"), "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svg, err := SVG(c.kind, c.content)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if svg != "" {
				t.Errorf("returned an image with the error")
			}
		})
	}
}

// pdfRect is a filled rectangle of a PDF page, in mm from the top-left.
type pdfRect struct{ x, y, w, h float64 }

var reOp = regexp.MustCompile(`(-?[\d.]+) (-?[\d.]+) (-?[\d.]+) (-?[\d.]+) re f`)
var rePage = regexp.MustCompile(`/Type /Page[^s]`)
var reBox = regexp.MustCompile(`/MediaBox \[0 0 ([\d.]+) ([\d.]+)\]`)

// pdfPages splits an uncompressed PDF into its pages' filled rectangles and
// page sizes (mm).
func pdfPages(t *testing.T, r io.Reader) (rects [][]pdfRect, sizes [][2]float64) {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "%PDF-") {
		t.Fatal("not a PDF")
	}
	// One MediaBox in the page tree gives every page its size.
	box := reBox.FindStringSubmatch(s)
	if box == nil {
		t.Fatal("no MediaBox")
	}
	w, _ := strconv.ParseFloat(box[1], 64)
	h, _ := strconv.ParseFloat(box[2], 64)
	for range rePage.FindAllString(s, -1) {
		sizes = append(sizes, [2]float64{w / 72 * 25.4, h / 72 * 25.4})
	}
	// Content streams come in page order.
	streams := regexp.MustCompile(`(?s)stream\n(.*?)endstream`).FindAllStringSubmatch(s, -1)
	for i, st := range streams {
		if i >= len(sizes) {
			break
		}
		var page []pdfRect
		for _, m := range reOp.FindAllStringSubmatch(st[1], -1) {
			v := [4]float64{}
			for j := range v {
				v[j], _ = strconv.ParseFloat(m[j+1], 64)
			}
			// Operands are points with y up from the page's bottom; fpdf gives
			// the top edge and a negative height.
			const mm = 25.4 / 72
			page = append(page, pdfRect{v[0] * mm, sizes[i][1] - v[1]*mm, v[2] * mm, -v[3] * mm})
		}
		rects = append(rects, page)
	}
	return rects, sizes
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

func TestLayoutGeometry(t *testing.T) {
	cases := []struct {
		l                   Layout
		perPage             int
		pageW, pageH        float64
		firstX, firstY      float64
		lastX, lastY        float64 // last label of a page
		labelW, labelH      float64
		secondX, secondRowY float64
	}{
		{Avery5160, 30, 215.9, 279.4, 5.58165, 12.7, 5.58165 + 2*69.85, 12.7 + 9*25.4, 66.675, 25.4, 5.58165 + 69.85, 12.7 + 25.4},
		{AveryL7160, 21, 210, 297, 7.25, 15.15, 7.25 + 2*66, 15.15 + 6*38.1, 63.5, 38.1, 7.25 + 66, 15.15 + 38.1},
		{Single, 1, 62, 29, 0, 0, 0, 0, 62, 29, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.l.Name, func(t *testing.T) {
			if c.l.PerPage() != c.perPage {
				t.Errorf("per page = %d, want %d", c.l.PerPage(), c.perPage)
			}
			if !near(c.l.PageW, c.pageW) || !near(c.l.PageH, c.pageH) {
				t.Errorf("page = %vx%v, want %vx%v", c.l.PageW, c.l.PageH, c.pageW, c.pageH)
			}
			if !near(c.l.LabelW, c.labelW) || !near(c.l.LabelH, c.labelH) {
				t.Errorf("label = %vx%v, want %vx%v", c.l.LabelW, c.l.LabelH, c.labelW, c.labelH)
			}
			p, x, y := c.l.Origin(0)
			if p != 0 || !near(x, c.firstX) || !near(y, c.firstY) {
				t.Errorf("first origin = %d,%v,%v want 0,%v,%v", p, x, y, c.firstX, c.firstY)
			}
			p, x, y = c.l.Origin(c.perPage - 1)
			if p != 0 || !near(x, c.lastX) || !near(y, c.lastY) {
				t.Errorf("last origin = %d,%v,%v want 0,%v,%v", p, x, y, c.lastX, c.lastY)
			}
			if x+c.l.LabelW > c.l.PageW+0.01 || y+c.l.LabelH > c.l.PageH+0.01 {
				t.Errorf("last label runs off the page")
			}
			p, x, y = c.l.Origin(c.perPage)
			if p != 1 || !near(x, c.firstX) || !near(y, c.firstY) {
				t.Errorf("label %d origin = %d,%v,%v want page 1 at the first origin", c.perPage, p, x, y)
			}
			if c.perPage > 1 {
				_, x, _ = c.l.Origin(1)
				if !near(x, c.secondX) {
					t.Errorf("second label x = %v, want %v", x, c.secondX)
				}
				_, x, y = c.l.Origin(c.l.Cols)
				if !near(x, c.firstX) || !near(y, c.secondRowY) {
					t.Errorf("second row origin = %v,%v want %v,%v", x, y, c.firstX, c.secondRowY)
				}
			}
		})
	}
}

func TestAverySheetsMatchTheirPrintedSpecs(t *testing.T) {
	// 5160 is specified in inches: 0.21975 in left, 0.5 in top, 2.75 x 1 in pitch.
	_, x, y := Avery5160.Origin(0)
	if !near(x/25.4, 0.21975) || !near(y/25.4, 0.5) {
		t.Errorf("5160 first label at %v,%v in", x/25.4, y/25.4)
	}
	if !near(Avery5160.PitchX/25.4, 2.75) || !near(Avery5160.PitchY/25.4, 1) {
		t.Errorf("5160 pitch %v,%v in", Avery5160.PitchX/25.4, Avery5160.PitchY/25.4)
	}
}

func TestLabelsPageCounts(t *testing.T) {
	mk := func(n int) []Label {
		out := make([]Label, n)
		for i := range out {
			out[i] = Label{Code: "C000001", CodeKind: Code128, Lines: []string{"Title"}}
		}
		return out
	}
	cases := []struct {
		l     Layout
		n     int
		pages int
	}{
		{Avery5160, 1, 1}, {Avery5160, 30, 1}, {Avery5160, 31, 2}, {Avery5160, 60, 2}, {Avery5160, 61, 3},
		{AveryL7160, 21, 1}, {AveryL7160, 22, 2},
		{Single, 1, 1}, {Single, 3, 3},
	}
	for _, c := range cases {
		r, err := Labels(c.l, mk(c.n))
		if err != nil {
			t.Fatalf("%s %d: %v", c.l.Name, c.n, err)
		}
		_, sizes := pdfPages(t, r)
		if len(sizes) != c.pages {
			t.Errorf("%s with %d labels: %d pages, want %d", c.l.Name, c.n, len(sizes), c.pages)
		}
		for _, s := range sizes {
			if !near(s[0], c.l.PageW) || !near(s[1], c.l.PageH) {
				t.Errorf("%s: page is %.2fx%.2f mm, want %vx%v", c.l.Name, s[0], s[1], c.l.PageW, c.l.PageH)
			}
		}
	}
}

// rasterPDFLabel draws the filled rectangles of a PDF page that lie inside the
// label at (x,y), 10 px per mm, with a white margin.
func rasterPDFLabel(rects []pdfRect, l Layout, x, y float64) image.Image {
	const px = 10
	img := image.NewGray(image.Rect(0, 0, int(l.LabelW*px), int(l.LabelH*px)))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for _, r := range rects {
		if r.x < x-0.01 || r.y < y-0.01 || r.x+r.w > x+l.LabelW+0.01 || r.y+r.h > y+l.LabelH+0.01 {
			continue
		}
		for py := int(math.Round((r.y - y) * px)); py < int(math.Round((r.y+r.h-y)*px)); py++ {
			for qx := int(math.Round((r.x - x) * px)); qx < int(math.Round((r.x+r.w-x)*px)); qx++ {
				img.SetGray(qx, py, color.Gray{0})
			}
		}
	}
	return img
}

func TestLabelBarsDecodeAtTheirPlace(t *testing.T) {
	cases := []struct {
		l    Layout
		lb   Label
		want string
	}{
		{Avery5160, Label{Code: "123456789012", CodeKind: Code128, Lines: []string{"A Title"}}, "123456789012"},
		{Avery5160, Label{Code: "9780306406157", CodeKind: EAN13, Lines: []string{"A Title", "copy 2", "Shelf 3"}}, "9780306406157"},
		{AveryL7160, Label{Code: "C000042", CodeKind: Code128, Lines: []string{"A Title"}}, "C000042"},
		{Single, Label{Code: "9780306406157", CodeKind: EAN13, Lines: []string{"A Title", "copy 2", "Main shelf"}}, "9780306406157"},
	}
	for _, c := range cases {
		t.Run(c.l.Name+"/"+c.lb.Code, func(t *testing.T) {
			// Put the label last on its page, to prove the origin used.
			labels := make([]Label, c.l.PerPage())
			for i := range labels {
				labels[i] = Label{Code: "C000001", CodeKind: Code128}
			}
			labels[len(labels)-1] = c.lb
			r, err := Labels(c.l, labels)
			if err != nil {
				t.Fatal(err)
			}
			pages, _ := pdfPages(t, r)
			_, x, y := c.l.Origin(len(labels) - 1)
			got, err := decode(t, c.lb.CodeKind, rasterPDFLabel(pages[0], c.l, x, y))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != c.want {
				t.Fatalf("decoded %q, want %q", got, c.want)
			}
		})
	}
}

func TestBarsStayInsideTheirLabel(t *testing.T) {
	for _, l := range []Layout{Avery5160, AveryL7160, Single} {
		labels := make([]Label, l.PerPage())
		for i := range labels {
			labels[i] = Label{Code: strings.Repeat("W", 12), CodeKind: Code128}
		}
		r, err := Labels(l, labels)
		if err != nil {
			t.Fatalf("%s: %v", l.Name, err)
		}
		pages, _ := pdfPages(t, r)
		for _, rc := range pages[0] {
			inside := false
			for i := range labels {
				_, x, y := l.Origin(i)
				if rc.x >= x-0.01 && rc.y >= y-0.01 && rc.x+rc.w <= x+l.LabelW+0.01 && rc.y+rc.h <= y+l.LabelH+0.01 {
					inside = true
				}
			}
			if !inside {
				t.Fatalf("%s: bar %+v is outside every label", l.Name, rc)
			}
		}
	}
}

func TestLabelModulesAreAtLeastTheMinimum(t *testing.T) {
	for _, l := range []Layout{Avery5160, AveryL7160, Single} {
		r, err := Labels(l, []Label{{Code: strings.Repeat("a", 12), CodeKind: Code128}})
		if err != nil {
			t.Fatalf("%s: %v", l.Name, err)
		}
		pages, _ := pdfPages(t, r)
		for _, rc := range pages[0] {
			if rc.w < minModuleMM-0.01 {
				t.Errorf("%s: a bar %.3f mm wide is under the %.2f mm minimum", l.Name, rc.w, minModuleMM)
			}
		}
	}
}

func TestLabelsRefuseACodeThatCannotBeScanned(t *testing.T) {
	// 32 characters of Code 128 needs far more than a 62 mm label's width at the minimum module.
	long := strings.Repeat("a", 32)
	for _, l := range []Layout{Avery5160, AveryL7160, Single} {
		if _, err := Labels(l, []Label{{Code: long, CodeKind: Code128}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", l.Name, err)
		}
	}
	// The same code is fine as an SVG, and a short one is fine on a label.
	if _, err := SVG(Code128, long); err != nil {
		t.Error(err)
	}
	if _, err := Labels(Single, []Label{{Code: "C000001", CodeKind: Code128}}); err != nil {
		t.Error(err)
	}
}

func TestLabelsRefuseBadInput(t *testing.T) {
	ok := Label{Code: "C1", CodeKind: Code128}
	cases := map[string]struct {
		l  Layout
		in []Label
	}{
		"none":            {Avery5160, nil},
		"bad ean":         {Avery5160, []Label{ok, {Code: "9780306406158", CodeKind: EAN13}}},
		"qr label":        {Avery5160, []Label{{Code: "C1", CodeKind: QR}}},
		"empty code":      {Avery5160, []Label{{CodeKind: Code128}}},
		"non-ASCII code":  {Avery5160, []Label{{Code: "é", CodeKind: Code128}}},
		"too many lines":  {Avery5160, []Label{{Code: "C1", CodeKind: Code128, Lines: make([]string, 9)}}},
		"zero layout":     {Layout{}, []Label{ok}},
		"too many labels": {Single, make([]Label, MaxLabels+1)},
		"kind unset":      {Avery5160, []Label{{Code: "C1"}}},
		"short ean":       {Avery5160, []Label{{Code: "978-0-306", CodeKind: EAN13}}},
	}
	for name, c := range cases {
		if r, err := Labels(c.l, c.in); err == nil || r != nil {
			t.Errorf("%s: want an error and no PDF, got %v", name, err)
		}
	}
}

func TestLabelsAcceptAnISBNWithHyphens(t *testing.T) {
	if _, err := Labels(Avery5160, []Label{{Code: "978-0-306-40615-7", CodeKind: EAN13}}); err != nil {
		t.Fatal(err)
	}
}

func TestLongTextIsCutWithAnEllipsis(t *testing.T) {
	long := strings.Repeat("The Very Long Title ", 20)
	for _, l := range []Layout{Avery5160, AveryL7160, Single} {
		doc := newTextProbe(l)
		got := fit(doc, long, l.innerW())
		if !strings.HasSuffix(got, ellipsis) || len(got) >= len(long) {
			t.Errorf("%s: %q is not cut with an ellipsis", l.Name, got)
		}
		if w := doc.GetStringWidth(got); w > l.innerW() {
			t.Errorf("%s: cut text is %.2f mm, wider than %.2f", l.Name, w, l.innerW())
		}
		// A title that fits is untouched.
		if got := fit(doc, "Short", l.innerW()); got != "Short" {
			t.Errorf("%s: %q", l.Name, got)
		}
		// Control characters can't break a line.
		if got := fit(doc, "a\nb\tc", l.innerW()); got != "a b c" {
			t.Errorf("%s: %q", l.Name, got)
		}
	}
	// And a label with such a title renders.
	if _, err := Labels(Avery5160, []Label{{Code: "C1", CodeKind: Code128, Lines: []string{long, "x\ny"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestTheFontIsBundled(t *testing.T) {
	// The PDF carries its own font, not a reference to one on the machine.
	r, err := Labels(Single, []Label{{Code: "C1", CodeKind: Code128, Lines: []string{"Title"}}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	if !strings.Contains(string(b), "/FontFile2") {
		t.Error("the font isn't embedded in the PDF")
	}
}

func TestLayoutByName(t *testing.T) {
	for _, n := range []string{"avery5160", "averyl7160", "single"} {
		if l, ok := LayoutByName(n); !ok || l.Name != n {
			t.Errorf("%s: %v %v", n, l.Name, ok)
		}
	}
	if _, ok := LayoutByName("nope"); ok {
		t.Error("unknown layout found")
	}
}

func newTextProbe(l Layout) *fpdf.Fpdf {
	pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "mm", Size: fpdf.SizeType{Wd: l.PageW, Ht: l.PageH}})
	pdf.AddUTF8FontFromBytes(fontName, "", goregular.TTF)
	pdf.SetFont(fontName, "", lineFontPt)
	return pdf
}

func TestTextTheFontCannotDrawDoesNotBreakLabels(t *testing.T) {
	for _, text := range []string{
		"The Very Hungry Caterpillar 🐛", "𝔘", "\U0010FFFF", "￿", "￾", "‮", "bad\xffutf8", "nul\x00", "日本語の本",
	} {
		r, err := Labels(Avery5160, []Label{{Code: "C1", CodeKind: Code128, Lines: []string{text}}})
		if err != nil || r == nil {
			t.Errorf("%q: %v", text, err)
		}
	}
	doc := newTextProbe(Avery5160)
	if got := fit(doc, "Cat 🐛 𝔘", 100); got != "Cat ? ?" {
		t.Errorf("fit = %q, want placeholders", got)
	}
	// Characters can't reach the code's own text either: the code is validated first.
	if _, err := Labels(Avery5160, []Label{{Code: "C🐛", CodeKind: Code128}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("emoji code: %v", err)
	}
}

func TestLabelsNeverPanicOnAnyText(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		var sb strings.Builder
		for n := rng.Intn(60); n > 0; n-- {
			var r rune
			switch rng.Intn(4) {
			case 0:
				r = rune(rng.Intn(0x80))
			case 1:
				r = rune(rng.Intn(0x3000))
			case 2:
				r = rune(rng.Intn(0x110000))
			default:
				r = rune(0xD800 + rng.Intn(0x900)) // surrogates, invalid
			}
			sb.WriteRune(r)
		}
		if _, err := Labels(Single, []Label{{Code: "C1", CodeKind: Code128, Lines: []string{sb.String()}}}); err != nil {
			t.Fatalf("%q: %v", sb.String(), err)
		}
	}
}

func TestFitIsFastOnLongText(t *testing.T) {
	doc := newTextProbe(Avery5160)
	long := strings.Repeat("W", maxLineRune)
	start := time.Now()
	for i := 0; i < 1000; i++ {
		fit(doc, long, Avery5160.innerW())
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("1000 cuts of a %d-rune line took %v", maxLineRune, d)
	}
}

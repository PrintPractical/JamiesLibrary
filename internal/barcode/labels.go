package barcode

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

const (
	inch = 25.4 // mm

	// The narrowest bar a label may be drawn with, and the widest (so a short
	// code isn't stretched across the label). Below the minimum a code can't
	// be scanned reliably, so it is refused.
	minModuleMM = 0.25
	maxModuleMM = 0.5

	// MaxLabels bounds one PDF.
	MaxLabels = 5000

	paddingMM   = 1.5
	barsGapMM   = 0.5 // between the bars and the code's text
	lineFontPt  = 8.0
	codeFontPt  = 6.5
	lineGap     = 1.15 // line height as a multiple of the font size
	barsShare   = 0.30 // of the label's height
	ellipsis    = "…"
	fontName    = "gosans"
	ptToMM      = 25.4 / 72
	maxLineRune = 500
)

// Layout is a sheet (or page) of labels: where the first label sits and the
// pitch between labels, all in millimetres.
type Layout struct {
	Name         string
	PageW, PageH float64
	Cols, Rows   int
	LabelW       float64
	LabelH       float64
	MarginLeft   float64 // page edge to the first label's left edge
	MarginTop    float64
	PitchX       float64 // left edge to left edge of the next column
	PitchY       float64
}

// The supported layouts.
var (
	// Avery 5160: US Letter, 3 x 10 labels of 2.625 x 1 in.
	Avery5160 = Layout{Name: "avery5160", PageW: 8.5 * inch, PageH: 11 * inch, Cols: 3, Rows: 10,
		LabelW: 2.625 * inch, LabelH: 1 * inch, MarginLeft: 0.21975 * inch, MarginTop: 0.5 * inch,
		PitchX: 2.75 * inch, PitchY: 1 * inch}
	// Avery L7160: A4, 3 x 7 labels of 63.5 x 38.1 mm.
	AveryL7160 = Layout{Name: "averyl7160", PageW: 210, PageH: 297, Cols: 3, Rows: 7,
		LabelW: 63.5, LabelH: 38.1, MarginLeft: 7.25, MarginTop: 15.15, PitchX: 66, PitchY: 38.1}
	// Single: one label per page, the page being the label.
	Single = Layout{Name: "single", PageW: 62, PageH: 29, Cols: 1, Rows: 1, LabelW: 62, LabelH: 29,
		PitchX: 62, PitchY: 29}
)

// LayoutByName returns the layout with that name ("avery5160", "averyl7160", "single").
func LayoutByName(name string) (Layout, bool) {
	for _, l := range []Layout{Avery5160, AveryL7160, Single} {
		if l.Name == name {
			return l, true
		}
	}
	return Layout{}, false
}

// PerPage is how many labels one page holds.
func (l Layout) PerPage() int { return l.Cols * l.Rows }

// Pages is how many pages n labels take.
func (l Layout) Pages(n int) int { return (n + l.PerPage() - 1) / l.PerPage() }

// Origin returns the page (from 0) and the top-left corner, in mm, of label i
// (from 0), filling each row left to right and each page top to bottom.
func (l Layout) Origin(i int) (page int, x, y float64) {
	page = i / l.PerPage()
	pos := i % l.PerPage()
	return page, l.MarginLeft + float64(pos%l.Cols)*l.PitchX, l.MarginTop + float64(pos/l.Cols)*l.PitchY
}

// Label is one label's content: its code, drawn as bars with the code's text
// beneath, and lines of text below that.
type Label struct {
	Code     string
	CodeKind Kind // Code128 or EAN13
	Lines    []string
}

// Labels renders labels onto pages of layout. More labels than a page holds
// continue on the next page. It refuses an invalid code, a code that can't be
// drawn at a scannable width on this layout's labels, and more lines than fit.
func Labels(layout Layout, labels []Label) (out io.Reader, err error) {
	// fpdf is unmaintained: a fault in it must not take down the caller.
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("barcode: drawing the labels failed: %v", r)
		}
	}()
	if layout.PerPage() < 1 {
		return nil, errors.New("barcode: unknown label layout")
	}
	if len(labels) == 0 {
		return nil, invalid("there are no labels to print")
	}
	if len(labels) > MaxLabels {
		return nil, invalid("at most %d labels can be printed at once", MaxLabels)
	}

	// Validate everything before drawing anything.
	codes := make([]*matrix, len(labels))
	for i, lb := range labels {
		if lb.CodeKind != Code128 && lb.CodeKind != EAN13 {
			return nil, invalid("label %d: labels carry Code 128 or EAN-13 codes", i+1)
		}
		m, err := encode(lb.CodeKind, lb.Code)
		if err != nil {
			return nil, fmt.Errorf("label %d: %w", i+1, err)
		}
		if layout.moduleWidth(m) < minModuleMM {
			return nil, invalid("label %d: the code is too long to be scannable at this label size", i+1)
		}
		if len(lb.Lines) > layout.maxLines() {
			return nil, invalid("label %d: at most %d lines of text fit on this label", i+1, layout.maxLines())
		}
		codes[i] = m
	}

	pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "mm", Size: fpdf.SizeType{Wd: layout.PageW, Ht: layout.PageH}})
	pdf.SetCompression(false)
	pdf.SetCreationDate(time.Unix(0, 0).UTC())
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(0, 0, 0)
	pdf.AddUTF8FontFromBytes(fontName, "", goregular.TTF)
	pdf.SetFillColor(0, 0, 0)
	pdf.SetTextColor(0, 0, 0)

	for i, lb := range labels {
		_, x, y := layout.Origin(i)
		if i%layout.PerPage() == 0 {
			pdf.AddPage()
		}
		layout.drawLabel(pdf, x, y, lb, codes[i])
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("barcode: writing the PDF: %w", err)
	}
	return &buf, nil
}

func (l Layout) innerW() float64 { return l.LabelW - 2*paddingMM }

// moduleWidth is the width of one module: as wide as the label allows, up to
// the maximum. The quiet zones count against the label's inner width.
func (l Layout) moduleWidth(m *matrix) float64 {
	w := l.innerW() / float64(m.w+2*m.quiet)
	if w > maxModuleMM {
		w = maxModuleMM
	}
	return w
}

func (l Layout) barsHeight() float64 { return l.LabelH * barsShare }

func lineHeight(pt float64) float64 { return pt * ptToMM * lineGap }

// maxLines is how many lines of text fit beneath the bars and the code's text.
func (l Layout) maxLines() int {
	free := l.LabelH - 2*paddingMM - l.barsHeight() - barsGapMM - lineHeight(codeFontPt)
	if free <= 0 {
		return 0
	}
	return int(free / lineHeight(lineFontPt))
}

func (l Layout) drawLabel(pdf *fpdf.Fpdf, x, y float64, lb Label, m *matrix) {
	mw := l.moduleWidth(m)
	barsW := float64(m.w) * mw
	bx := x + (l.LabelW-barsW)/2
	by := y + paddingMM
	m.runs(0, func(start, n int) {
		pdf.Rect(bx+float64(start)*mw, by, float64(n)*mw, l.barsHeight(), "F")
	})

	cy := by + l.barsHeight() + barsGapMM
	pdf.SetFont(fontName, "", codeFontPt)
	text := lb.Code
	if lb.CodeKind == EAN13 {
		text, _ = NormalizeEAN13(lb.Code)
	}
	l.drawLine(pdf, x, cy, text, lineHeight(codeFontPt), true)

	cy += lineHeight(codeFontPt)
	pdf.SetFont(fontName, "", lineFontPt)
	for _, line := range lb.Lines {
		l.drawLine(pdf, x, cy, line, lineHeight(lineFontPt), false)
		cy += lineHeight(lineFontPt)
	}
}

// drawLine draws text in the label whose left edge is x, cut with an ellipsis
// to the label's inner width.
func (l Layout) drawLine(pdf *fpdf.Fpdf, x, y float64, text string, h float64, center bool) {
	text = fit(pdf, text, l.innerW())
	w := pdf.GetStringWidth(text)
	tx := x + paddingMM
	if center {
		tx = x + (l.LabelW-w)/2
	}
	pdf.Text(tx, y+h*0.75, text)
}

// placeholder stands in for a character the bundled font has no glyph for.
const placeholder = '?'

var (
	fontOnce sync.Once
	goFont   *sfnt.Font
)

// drawable reports whether the bundled font has a glyph for r.
func drawable(r rune) bool {
	fontOnce.Do(func() { goFont, _ = sfnt.Parse(goregular.TTF) })
	if goFont == nil || r > 0xFFFF {
		return false
	}
	var b sfnt.Buffer
	idx, err := goFont.GlyphIndex(&b, r)
	return err == nil && idx != 0
}

// clean replaces control characters with spaces and characters the font can't
// draw (and invalid UTF-8) with a placeholder, so nothing reaches the PDF
// writer that it can't handle.
func clean(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return ' '
		case !drawable(r):
			return placeholder
		}
		return r
	}, text)
}

// fit cleans text and shortens it, ending in an ellipsis, until it is no wider
// than width in the current font.
func fit(pdf *fpdf.Fpdf, text string, width float64) string {
	r := []rune(strings.TrimSpace(clean(text)))
	if len(r) > maxLineRune {
		r = r[:maxLineRune]
	}
	if pdf.GetStringWidth(string(r)) <= width {
		return string(r)
	}
	// The longest prefix whose text with an ellipsis fits (width grows with length).
	cut := func(n int) string { return strings.TrimRight(string(r[:n]), " ") + ellipsis }
	lo, hi := 0, len(r)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if pdf.GetStringWidth(cut(mid)) <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if s := cut(lo); pdf.GetStringWidth(s) <= width {
		return s
	}
	return ""
}

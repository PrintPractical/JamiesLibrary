package e2e

// CAT-7 from outside: the PDF the server gives for a choice of copies is read
// back the way a printer and a scanner would use it. The sheet geometry below
// is the published Avery specification, written out here and not taken from
// the code under test: 5160 is US Letter, 3 x 10 labels of 2 5/8 x 1 in; L7160
// is A4, 3 x 7 labels of 63.5 x 38.1 mm. The PDF is parsed with no PDF
// library: its bars are drawn into a bitmap and decoded by a barcode reader.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"jamieslibrary/internal/barcode/barcodetest"
)

const mm = 72 / 25.4

type sheet struct {
	Name           string
	W, H           float64 // page, in points
	Cols, Rows     int
	LW, LH         float64 // a label
	X0, Y0         float64 // first label's top left corner
	PitchX, PitchY float64
}

var (
	avery5160  = sheet{"avery5160", 8.5 * 72, 11 * 72, 3, 10, 2.625 * 72, 72, 0.21975 * 72, 0.5 * 72, 2.75 * 72, 72}
	averyL7160 = sheet{"averyl7160", 210 * mm, 297 * mm, 3, 7, 63.5 * mm, 38.1 * mm, 7.25 * mm, 15.15 * mm, 66 * mm, 38.1 * mm}
	oneUp      = sheet{"single", 62 * mm, 29 * mm, 1, 1, 62 * mm, 29 * mm, 0, 0, 62 * mm, 29 * mm}
)

type bar struct{ x0, y0, x1, y1 float64 } // top-left origin, points
type text struct {
	x, y float64
	s    string
}

// pdfPage is one page's bars and text, and the size the page declares.
type pdfPage struct {
	W, H  float64
	Bars  []bar
	Texts []text
}

var (
	rePage    = regexp.MustCompile(`(?s)/Type /Page\n.*?/Contents (\d+) 0 R`)
	reBox     = regexp.MustCompile(`/MediaBox \[0 0 ([\d.]+) ([\d.]+)\]`)
	reRect    = regexp.MustCompile(`(?m)^(-?[\d.]+) (-?[\d.]+) (-?[\d.]+) (-?[\d.]+) re f$`)
	reText    = regexp.MustCompile(`(?s)BT (-?[\d.]+) (-?[\d.]+) Td \((.*?)\) Tj ET`)
	reEscaped = regexp.MustCompile(`\\([\\()rn])`)
)

func parsePDF(t *testing.T, pdf []byte) []pdfPage {
	t.Helper()
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %.40q", pdf)
	}
	box := reBox.FindSubmatch(pdf)
	if box == nil {
		t.Fatal("the PDF declares no page size")
	}
	w, _ := strconv.ParseFloat(string(box[1]), 64)
	h, _ := strconv.ParseFloat(string(box[2]), 64)
	var pages []pdfPage
	for _, m := range rePage.FindAllSubmatch(pdf, -1) {
		obj := regexp.MustCompile(`\n` + string(m[1]) + ` 0 obj\n<</Length (\d+)>>\nstream\n`).FindSubmatchIndex(pdf)
		if obj == nil {
			t.Fatalf("no content for the page (object %s)", m[1])
		}
		n, _ := strconv.Atoi(string(pdf[obj[2]:obj[3]]))
		body := pdf[obj[1] : obj[1]+n]
		p := pdfPage{W: w, H: h}
		for _, r := range reRect.FindAllSubmatch(body, -1) {
			f := func(i int) float64 { v, _ := strconv.ParseFloat(string(r[i]), 64); return v }
			x, y, rw, rh := f(1), f(2), f(3), f(4)
			// PDF's origin is the bottom left; a negative height runs downwards.
			ya, yb := h-y, h-(y+rh)
			if ya > yb {
				ya, yb = yb, ya
			}
			p.Bars = append(p.Bars, bar{x, ya, x + rw, yb})
		}
		for _, tx := range reText.FindAllSubmatch(body, -1) {
			x, _ := strconv.ParseFloat(string(tx[1]), 64)
			y, _ := strconv.ParseFloat(string(tx[2]), 64)
			raw := reEscaped.ReplaceAllFunc(tx[3], func(b []byte) []byte {
				return map[byte][]byte{'\\': {'\\'}, '(': {'('}, ')': {')'}, 'r': {'\r'}, 'n': {'\n'}}[b[1]]
			})
			var u []uint16
			for i := 0; i+1 < len(raw); i += 2 { // text is two bytes a character
				u = append(u, uint16(raw[i])<<8|uint16(raw[i+1]))
			}
			p.Texts = append(p.Texts, text{x, h - y, string(utf16.Decode(u))})
		}
		pages = append(pages, p)
	}
	if len(pages) == 0 {
		t.Fatal("the PDF has no pages")
	}
	return pages
}

// label is what one cell of a sheet holds.
type label struct {
	Col, Row int
	Bars     []bar
	Lines    []string // top to bottom
}

// cells assigns a page's bars and text to the sheet's grid, and fails when
// something lies outside every label (it would print on the gap between them).
func (s sheet) cells(t *testing.T, p pdfPage) []label {
	t.Helper()
	const slop = 0.6 // points: the PDF rounds to 0.01
	byCell := map[[2]int]*label{}
	at := func(x, y float64) (int, int, bool) {
		for c := 0; c < s.Cols; c++ {
			for r := 0; r < s.Rows; r++ {
				l, tp := s.X0+float64(c)*s.PitchX, s.Y0+float64(r)*s.PitchY
				if x >= l-slop && x <= l+s.LW+slop && y >= tp-slop && y <= tp+s.LH+slop {
					return c, r, true
				}
			}
		}
		return 0, 0, false
	}
	get := func(c, r int) *label {
		if byCell[[2]int{c, r}] == nil {
			byCell[[2]int{c, r}] = &label{Col: c, Row: r}
		}
		return byCell[[2]int{c, r}]
	}
	for _, b := range p.Bars {
		c, r, ok := at(b.x0, b.y0)
		c2, r2, ok2 := at(b.x1, b.y1)
		if !ok || !ok2 || c != c2 || r != r2 {
			t.Errorf("a bar at (%.1f..%.1f, %.1f..%.1f) is not inside one %s label", b.x0, b.x1, b.y0, b.y1, s.Name)
			continue
		}
		get(c, r).Bars = append(get(c, r).Bars, b)
	}
	type placed struct {
		y float64
		s string
	}
	lines := map[[2]int][]placed{}
	for _, tx := range p.Texts {
		c, r, ok := at(tx.x, tx.y)
		if !ok {
			t.Errorf("the text %q at (%.1f, %.1f) is not inside a %s label", tx.s, tx.x, tx.y, s.Name)
			continue
		}
		get(c, r)
		lines[[2]int{c, r}] = append(lines[[2]int{c, r}], placed{tx.y, tx.s})
	}
	var out []label
	for k, l := range byCell {
		ls := lines[k]
		sort.Slice(ls, func(i, j int) bool { return ls[i].y < ls[j].y })
		for _, x := range ls {
			l.Lines = append(l.Lines, x.s)
		}
		out = append(out, *l)
	}
	// reading order: rows, then columns
	sort.Slice(out, func(i, j int) bool {
		if out[i].Row != out[j].Row {
			return out[i].Row < out[j].Row
		}
		return out[i].Col < out[j].Col
	})
	return out
}

// placed checks the bars sit where a label's owner would expect: centred across
// the label and a little below its top edge, not run into the next label or onto
// the backing sheet. 1.5 mm either way is allowed for the label's own padding.
func (s sheet) placed(t *testing.T, l label, where string) {
	t.Helper()
	if len(l.Bars) == 0 {
		t.Errorf("%s: no bars", where)
		return
	}
	left, top := s.X0+float64(l.Col)*s.PitchX, s.Y0+float64(l.Row)*s.PitchY
	minX, maxX, minY := l.Bars[0].x0, l.Bars[0].x1, l.Bars[0].y0
	for _, b := range l.Bars {
		minX, maxX, minY = min(minX, b.x0), max(maxX, b.x1), min(minY, b.y0)
	}
	if off := (minX+maxX)/2 - (left + s.LW/2); off > 1.5*mm || off < -1.5*mm {
		t.Errorf("%s: the bars are %.1f mm off the label's centre", where, off/mm)
	}
	if gap := (minY - top) / mm; gap < 0.5 || gap > 5 {
		t.Errorf("%s: the bars start %.1f mm below the label's top edge, want 0.5 to 5", where, gap)
	}
}

// decode draws a label's bars as a bitmap, as a printer would put ink, and reads it.
func (s sheet) decode(l label, kind string) (string, error) {
	const scale = 4 // pixels to the point
	const pad = 30
	left, top := s.X0+float64(l.Col)*s.PitchX, s.Y0+float64(l.Row)*s.PitchY
	img := image.NewGray(image.Rect(0, 0, int(s.LW*scale)+2*pad, int(s.LH*scale)+2*pad))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for _, b := range l.Bars {
		for y := int((b.y0-top)*scale+0.5) + pad; y < int((b.y1-top)*scale+0.5)+pad; y++ {
			for x := int((b.x0-left)*scale+0.5) + pad; x < int((b.x1-left)*scale+0.5)+pad; x++ {
				img.SetGray(x, y, color.Gray{0})
			}
		}
	}
	return barcodetest.Decode(kind, img)
}

func approx(a, b float64) bool { d := a - b; return d < 0.6 && d > -0.6 }

// labelsPDF asks for the labels of copies as w.root and returns the parsed pages.
func (w *world) labelsPDF(copies []int64, layout string) []pdfPage {
	w.t.Helper()
	r := w.root.do("POST", "/api/catalog/labels", map[string]any{"copy_ids": copies, "layout": layout})
	if r.Status != 200 || r.Header.Get("Content-Type") != "application/pdf" {
		w.t.Fatalf("labels (%s, %d copies): %d %s: %.200s", layout, len(copies), r.Status, r.Header.Get("Content-Type"), r.Raw)
	}
	return parsePDF(w.t, []byte(r.Raw))
}

// copiesOf adds n copies to book and returns their ids and numbers.
func (w *world) copiesOf(book int64, n int) (ids []int64, numbers []int, codes []string) {
	w.t.Helper()
	for i := 0; i < n; i++ {
		r := w.root.want(201, "POST", fmt.Sprintf("/api/books/%d/copies", book), map[string]any{"location_id": w.locID, "condition": "good"})
		ids = append(ids, idOf(r, "id"))
		numbers = append(numbers, int(idOf(r, "number")))
		c, _ := r.JSON["barcode"].(string)
		codes = append(codes, c)
	}
	return
}

func TestLabelSheetsHoldTheirCountAndFillInOrder(t *testing.T) {
	w := newWorld(t)
	// By-ISBN mode is the default: the book's ISBN barcode, its title, the copy number and the shelf.
	ids, numbers, _ := w.copiesOf(w.coverBook, 34) // with the one the fixture made: 35 copies
	first := []int64{w.firstCopyOf(w.coverBook)}
	all := append(first, ids...)
	nums := append([]int{1}, numbers...)

	cases := []struct {
		sh     sheet
		n      int
		counts []int
	}{
		{avery5160, 35, []int{30, 5}},
		{avery5160, 30, []int{30}},
		{avery5160, 31, []int{30, 1}},
		{averyL7160, 25, []int{21, 4}},
		{averyL7160, 21, []int{21}},
		{averyL7160, 22, []int{21, 1}},
		{oneUp, 3, []int{1, 1, 1}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%d", c.sh.Name, c.n), func(t *testing.T) {
			w.t = t
			pages := w.labelsPDF(all[:c.n], c.sh.Name)
			if len(pages) != len(c.counts) {
				t.Fatalf("%d pages, want %d", len(pages), len(c.counts))
			}
			k := 0
			for pi, p := range pages {
				if !approx(p.W, c.sh.W) || !approx(p.H, c.sh.H) {
					t.Errorf("page %d is %.2f x %.2f pt, want %.2f x %.2f", pi+1, p.W, p.H, c.sh.W, c.sh.H)
				}
				cells := c.sh.cells(t, p)
				if len(cells) != c.counts[pi] {
					t.Errorf("page %d holds %d labels, want %d", pi+1, len(cells), c.counts[pi])
				}
				for i, l := range cells {
					// Labels fill the page from the top left, a row at a time.
					if l.Row*c.sh.Cols+l.Col != i {
						t.Errorf("page %d: label %d is at column %d row %d: the grid is not filled in order", pi+1, i+1, l.Col, l.Row)
					}
					c.sh.placed(t, l, fmt.Sprintf("page %d label %d", pi+1, i+1))
					got, err := c.sh.decode(l, "ean13")
					if err != nil || got != "9780140328721" {
						t.Errorf("page %d label %d: the bars decode to %q (%v), want the book's ISBN", pi+1, i+1, got, err)
					}
					want := []string{"9780140328721", "Fantastic Mr Fox", fmt.Sprintf("Copy %d", nums[k]), "Fixture shelf"}
					if strings.Join(l.Lines, "|") != strings.Join(want, "|") {
						t.Errorf("page %d label %d: text %q, want %q", pi+1, i+1, l.Lines, want)
					}
					k++
				}
			}
		})
	}
}

func TestLabelsInOwnBarcodeMode(t *testing.T) {
	w := newWorld(t)
	// Copies added before the switch have no sticker; the switch gives each a code.
	book := w.newBook()
	ids, _, _ := w.copiesOf(w.coverBook, 2)
	hand, _, _ := w.copiesOf(book, 3)
	w.root.want(200, "PUT", "/api/admin/copy-id-mode", map[string]string{"mode": "own"})
	// One just added, in own mode, with no sticker.
	fresh, _, _ := w.copiesOf(book, 1)
	chosen := append(append(ids, hand...), fresh...)
	title := map[int64]string{}
	for _, b := range []int64{w.coverBook, book} {
		r := w.root.want(200, "GET", fmt.Sprintf("/api/books/%d", b), nil)
		for _, c := range r.JSON["copies"].([]any) {
			title[int64(c.(map[string]any)["id"].(float64))], _ = r.JSON["title"].(string)
		}
	}
	for _, sh := range []sheet{avery5160, averyL7160, oneUp} {
		t.Run(sh.Name, func(t *testing.T) {
			w.t = t
			pages := w.labelsPDF(chosen, sh.Name)
			var cells []label
			for _, p := range pages {
				cells = append(cells, sh.cells(t, p)...)
			}
			if len(cells) != len(chosen) {
				t.Fatalf("%d labels for %d copies", len(cells), len(chosen))
			}
			for i, l := range cells {
				id := chosen[i]
				sh.placed(t, l, fmt.Sprintf("label %d", i+1))
				got, err := sh.decode(l, "code128")
				if err != nil || got == "" {
					t.Errorf("label %d: the bars do not decode: %q (%v)", i+1, got, err)
					continue
				}
				// Content: the barcode's text and the book's title, and no copy number or shelf (those are the ISBN label's).
				if want := []string{got, title[id]}; strings.Join(l.Lines, "|") != strings.Join(want, "|") {
					t.Errorf("label %d: text %q, want its barcode and its book's title %q", i+1, l.Lines, want)
				}
				// Used as a librarian would use it: scanned at the desk, the sticker finds this copy.
				out := w.root.want(200, "POST", "/api/desk/checkout", map[string]string{"patron": w.pa.Card, "code": got})
				if idOf(out, "copy_id") != id {
					t.Errorf("label %d: scanning %q at the desk found copy %d, want %d", i+1, got, idOf(out, "copy_id"), id)
				}
				w.root.want(200, "POST", "/api/desk/checkin", map[string]any{"code": got, "loan_id": idOf(out, "id")})
			}
		})
	}
}

func TestByISBNLabelOfABookWithoutAnISBNUsesTheCopysOwnBarcode(t *testing.T) {
	w := newWorld(t)
	book := w.newBook()
	ids, nums, codes := w.copiesOf(book, 2)
	pages := w.labelsPDF(ids[1:], "single")
	if len(pages) != 1 {
		t.Fatalf("%d pages", len(pages))
	}
	cells := oneUp.cells(t, pages[0])
	if len(cells) != 1 {
		t.Fatalf("%d labels", len(cells))
	}
	if codes[1] == "" {
		t.Fatalf("a hand-catalogued copy has no barcode")
	}
	if got, err := oneUp.decode(cells[0], "code128"); err != nil || got != codes[1] {
		t.Errorf("decodes to %q (%v), want %q", got, err, codes[1])
	}
	title, _ := w.root.want(200, "GET", fmt.Sprintf("/api/books/%d", book), nil).JSON["title"].(string)
	wantLines := []string{codes[1], title, fmt.Sprintf("Copy %d", nums[1]), "Fixture shelf"}
	if strings.Join(cells[0].Lines, "|") != strings.Join(wantLines, "|") {
		t.Errorf("text %q, want %q", cells[0].Lines, wantLines)
	}
}

// The labels made from the screen are the same as the ones asked of the server directly.
func TestLabelsFromTheScreen(t *testing.T) {
	w := newWorld(t)
	w.copiesOf(w.coverBook, 3) // four copies of the book
	for _, width := range widths {
		for _, c := range []struct {
			layout string
			sh     sheet
		}{{"averyl7160", averyL7160}, {"avery5160", avery5160}, {"single", oneUp}} {
			t.Run(fmt.Sprintf("%d/%s", width, c.layout), func(t *testing.T) {
				// The page's policy forbids fetching a blob: address, so keep what it makes and read that.
				keep := `(() => { window.__blobs = []; const make = URL.createObjectURL.bind(URL); URL.createObjectURL = (b) => { window.__blobs.push(b); return make(b); }; return true; })()`
				read := `new Promise((res) => { const b = window.__blobs.at(-1); if (!b) return res(""); const f = new FileReader(); f.onload = () => res(String(f.result).split(",")[1] ?? ""); f.readAsDataURL(b); })`
				rep := browseAt(t, w.s.URL, "root", browseOpts{Width: width, Height: 800}, []browserStep{{
					Path: "/catalog/labels?book=" + fmt.Sprint(w.coverBook),
					Actions: []map[string]any{
						{"waitText": "All copies"},
						{"eval": keep},
						{"click": `.mg-copies-pick input[type=checkbox]`}, // all copies
						{"eval": fmt.Sprintf(`(() => { const s = document.querySelector("#lb-layout"); Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value").set.call(s, %q); s.dispatchEvent(new Event("change", { bubbles: true })); return s.value; })()`, c.layout)},
						{"clickText": "Make the labels"},
						{"waitText": "The labels are ready"},
						{"eval": read},
					},
				}})
				v := rep.Visits[0]
				if v.Notes["The labels are ready"].Found == false {
					t.Fatalf("the screen never said the labels were ready:\n%.600s", v.Text)
				}
				b64, _ := v.Evals[len(v.Evals)-1].(string)
				pdf, err := base64.StdEncoding.DecodeString(b64)
				if err != nil || len(pdf) == 0 {
					t.Fatalf("no PDF behind the screen's link (%v)", err)
				}
				pages := parsePDF(t, pdf)
				n := 0
				for _, p := range pages {
					if !approx(p.W, c.sh.W) || !approx(p.H, c.sh.H) {
						t.Errorf("page is %.2f x %.2f pt, want %.2f x %.2f", p.W, p.H, c.sh.W, c.sh.H)
					}
					for _, l := range c.sh.cells(t, p) {
						n++
						if got, err := c.sh.decode(l, "ean13"); err != nil || got != "9780140328721" {
							t.Errorf("label %d decodes to %q (%v)", n, got, err)
						}
					}
				}
				if n != 4 {
					t.Errorf("%d labels, want one for each of the 4 copies", n)
				}
			})
		}
	}
}

// firstCopyOf is the id of a book's first copy.
func (w *world) firstCopyOf(book int64) int64 {
	w.t.Helper()
	r := w.root.want(200, "GET", fmt.Sprintf("/api/books/%d", book), nil)
	cs, _ := r.JSON["copies"].([]any)
	if len(cs) == 0 {
		w.t.Fatalf("book %d has no copies", book)
	}
	return int64(cs[0].(map[string]any)["id"].(float64))
}

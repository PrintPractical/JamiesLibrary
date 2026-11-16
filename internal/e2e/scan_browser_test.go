package e2e

// Scanning in a real browser (SCAN-1, SCAN-3, SCAN-4, CARD-1): headless
// Chromium with a fake camera, a video file it plays as its camera. The frames
// show the real things: an EAN-13 for an ISBN, a Code 128 for a copy's own
// barcode, and the library card's own barcode and QR as the server serves them.
// The browser has no BarcodeDetector of its own (as on iOS Safari), so what
// reads them is the decoder the web app bundles, from the installation's own
// origin, under the server's own Content-Security-Policy. Nothing outside the
// installation is reachable: a request anywhere else is recorded and refused.
//
// A keyboard-style hand scanner is sent as real key events a few milliseconds
// apart with an Enter at the end, and a typist as the same keys slowly; both
// must give what the camera gives. They need Node 22+ and Chromium (see
// browser_test.go) and a built web app; without them they skip.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"jamieslibrary/internal/barcode"
	"jamieslibrary/internal/barcode/barcodetest"
)

type scanVisit struct {
	Path  string
	URL   string
	Text  string
	Notes map[string]struct {
		Found   bool
		Seconds float64
	}
	Evals  map[string]any
	Fields [][]string
	Clips  map[string]string
}

type scanReport struct {
	Requests []string
	Blocked  []string
	Console  []string
	Dialogs  []string
	Visits   []scanVisit
	Errors   []string
}

// camera says what the browser's camera is: a video file, none at all, or one
// the user refuses.
type camera struct {
	File   string // frames played as the camera ("" with Denied false: no device)
	Denied bool   // a camera exists, the permission is refused
}

type step map[string]any

func act(actions ...map[string]any) step { return step{"actions": actions} }

func at(path string, actions ...map[string]any) step { return step{"path": path, "actions": actions} }

func (s step) clips(sel ...string) step { s["clips"] = sel; return s }

func click(sel string) map[string]any     { return map[string]any{"click": sel} }
func clickText(txt string) map[string]any { return map[string]any{"clickText": txt} }
func waitText(txt string) map[string]any  { return map[string]any{"waitText": txt, "seconds": 20} }
func sleepMs(ms int) map[string]any       { return map[string]any{"sleepMs": ms} }
func absent(txt string, ms int) map[string]any {
	return map[string]any{"absentText": txt, "sleepMs": ms}
}
func eval(as, expr string) map[string]any {
	return map[string]any{"eval": map[string]string{"as": as, "expr": expr}}
}

// wedge types code the way a keyboard-style hand scanner does: every key a few
// milliseconds after the last, then Enter. typed is a person: slow keys.
func wedge(sel, code string) map[string]any {
	return map[string]any{"type": map[string]any{"selector": sel, "text": code, "delayMs": 2}}
}
func typed(sel, code string) map[string]any {
	return map[string]any{"type": map[string]any{"selector": sel, "text": code, "delayMs": 90}}
}

// burst is a scanner reading several labels one straight after another.
func burst(sel string, codes ...string) map[string]any {
	return map[string]any{"burst": map[string]any{"selector": sel, "codes": codes, "delayMs": 1}}
}

const scanInput = "input.input"

func scanBrowse(t *testing.T, base, user string, cam camera, steps ...step) scanReport {
	t.Helper()
	if testing.Short() {
		t.Skip("browser tests are skipped with -short")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("browser test needs node on PATH (22 or newer)")
	}
	chrome := findChrome()
	if chrome == "" {
		t.Skip("browser test needs Chromium: set JL_CHROME")
	}
	var args []string
	if cam.File != "" || cam.Denied {
		args = append(args, "--use-fake-device-for-media-stream")
		if cam.File != "" {
			args = append(args, "--use-file-for-fake-video-capture="+cam.File)
		}
		if cam.Denied {
			args = append(args, "--use-fake-ui-for-media-stream=deny") // the user taps Block
		} else {
			args = append(args, "--use-fake-ui-for-media-stream") // the user taps Allow
		}
	}
	sc := map[string]any{"chrome": chrome, "base": base, "steps": steps, "args": args, "noNativeDetector": true, "timeoutSeconds": 150}
	if user != "" {
		sc["signIn"] = map[string]string{"username": user, "password": adminPassword}
	}
	b, _ := json.Marshal(sc)
	file := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	cmd := exec.Command(node, filepath.Join("browser", "scan.mjs"), file)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("the browser driver failed: %v\n%s\n%s", err, errb.String(), out.String())
	}
	var rep scanReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("the driver's report: %v\n%s", err, out.String())
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("the driver reported: %v", rep.Errors)
	}
	for _, v := range rep.Visits {
		for what, n := range v.Notes {
			if !strings.HasPrefix(what, "absent:") && !n.Found {
				t.Errorf("%s: waited %.0f s and the screen never said %q. It said:\n%s", v.Path, n.Seconds, what, v.Text)
			}
		}
	}
	return rep
}

// ---- the camera's pictures ----------------------------------------------------

var viewBox = regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`)

// frames writes an MJPEG file of count frames of svg centred on white at 640x480,
// as large as fits: what a phone camera shows when the label fills the frame.
func frames(t *testing.T, svg string) string {
	t.Helper()
	vb := viewBox.FindStringSubmatch(svg)
	if vb == nil {
		t.Fatalf("no viewBox in %.80s", svg)
	}
	w, _ := strconv.Atoi(vb[1])
	h, _ := strconv.Atoi(vb[2])
	scale := min(560/w, 400/h, 10)
	if scale < 1 {
		scale = 1
	}
	pic, err := barcodetest.RasterSVG(svg, scale)
	if err != nil {
		t.Fatal(err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 640, 480))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	b := pic.Bounds()
	off := image.Pt((640-b.Dx())/2, (480-b.Dy())/2)
	draw.Draw(canvas, b.Add(off), pic, b.Min, draw.Src)
	return mjpeg(t, canvas)
}

func mjpeg(t *testing.T, img image.Image) string {
	t.Helper()
	var one bytes.Buffer
	if err := jpeg.Encode(&one, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	var all bytes.Buffer
	for i := 0; i < 30; i++ {
		all.Write(one.Bytes())
	}
	f := filepath.Join(t.TempDir(), "camera.mjpeg")
	if err := os.WriteFile(f, all.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func svgOf(t *testing.T, kind barcode.Kind, content string) string {
	t.Helper()
	s, err := barcode.SVG(kind, content)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// blank is a camera looking at nothing: a white wall.
func blank(t *testing.T) string {
	canvas := image.NewRGBA(image.Rect(0, 0, 640, 480))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	return mjpeg(t, canvas)
}

// ---- what every scan must show ----------------------------------------------

// decoderCameFromHome checks that the camera read what it read with the
// bundled decoder: the wasm was asked for from our own origin, nothing went
// anywhere else, the page's policy was the server's (and allows exactly that),
// and the browser didn't complain.
func decoderCameFromHome(t *testing.T, w *world, rep scanReport, v scanVisit, expected ...string) {
	t.Helper()
	if len(rep.Blocked) > 0 {
		t.Errorf("the page asked for hosts outside the installation: %v", rep.Blocked)
	}
	wasm := false
	for _, r := range rep.Requests {
		if strings.HasSuffix(strings.SplitN(r, "?", 2)[0], ".wasm") {
			wasm = true
			if !strings.HasPrefix(r, w.s.URL+"/") {
				t.Errorf("the decoder's wasm came from %s, not the installation", r)
			}
		}
	}
	if !wasm {
		t.Errorf("the decoder's wasm was never fetched: nothing here proves the bundled decoder read the code (requests: %v)", rep.Requests)
	}
	if v.Evals["native"] != "undefined" {
		t.Errorf("the browser had a BarcodeDetector of its own (%v): the test meant to use the bundled decoder", v.Evals["native"])
	}
	csp, _ := v.Evals["csp"].(string)
	for _, want := range []string{"'wasm-unsafe-eval'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the page's Content-Security-Policy lacks %s: %q", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval'") && !strings.Contains(csp, "wasm-unsafe-eval") {
		t.Errorf("the policy allows script eval: %q", csp)
	}
	for _, c := range rep.Console {
		ok := false
		for _, e := range expected {
			ok = ok || strings.Contains(c, e)
		}
		if !ok {
			t.Errorf("the browser complained: %s", c)
		}
	}
	if len(rep.Dialogs) > 0 {
		t.Errorf("a dialog opened: %v", rep.Dialogs)
	}
}

func csp() map[string]any {
	return eval("csp", `fetch("/", {method: "HEAD"}).then((r) => r.headers.get("content-security-policy"))`)
}
func native() map[string]any { return eval("native", `typeof window.BarcodeDetector`) }

func (v scanVisit) must(t *testing.T, texts ...string) {
	t.Helper()
	for _, x := range texts {
		if !v.Notes[x].Found {
			t.Errorf("%s never showed %q. It showed:\n%s", v.Path, x, v.Text)
		}
	}
}

// ---- SCAN-1: the camera reads an ISBN on the add-book screen ---------------

func TestCameraReadsAnISBNAndStartsTheLookup(t *testing.T) {
	w := newWorld(t)
	books := func() int { return scalar(t, openRO(t, w.s.DataDir), `SELECT count(*) FROM books`) }
	before := books()
	const isbn = "9780261103573"
	cam := camera{File: frames(t, svgOf(t, barcode.EAN13, isbn))}

	rep := scanBrowse(t, w.s.URL, "root", cam,
		at("/catalog/new", click(".scan-cam"), waitText("Found on Open Library"), sleepMs(2500), native(), csp()))
	v := rep.Visits[0]
	v.must(t, "Found on Open Library")
	got := map[string]string{}
	for _, f := range v.Fields {
		got[f[0]] = f[1]
	}
	foundISBN, foundTitle := false, false
	for _, val := range got {
		foundISBN = foundISBN || val == isbn
		foundTitle = foundTitle || val == "The Fellowship of the Ring"
	}
	if !foundISBN || !foundTitle {
		t.Errorf("the form should hold the ISBN %s and the title; its fields are %v", isbn, v.Fields)
	}
	decoderCameFromHome(t, w, rep, v)
	// Looking is not saving; and held in front of the camera for seconds it is looked up once, not once a frame.
	if n := books(); n != before {
		t.Errorf("a scan saved a book: %d books before, %d after", before, n)
	}
	lookups := 0
	for _, r := range w.ol.Requests() {
		if strings.Contains(r, "9780261103573") && strings.Contains(r, "bibkeys") {
			lookups++
		}
	}
	if lookups != 1 {
		t.Errorf("the provider was asked %d times for one scan: %v", lookups, w.ol.Requests())
	}
}

// ---- SCAN-1: a barcode that can't be read, a camera that is refused or absent --

func TestWhenTheCameraCannotHelpTheISBNCanBeTyped(t *testing.T) {
	w := newWorld(t)
	tolkien := "9780261103573"
	cases := []struct {
		name string
		cam  camera
		act  []map[string]any
		said string // what the screen says about the camera
	}{
		{"nothing readable in view", camera{File: blank(t)}, []map[string]any{click(".scan-cam"), absent("Found on Open Library", 3000)}, ""},
		{"permission refused", camera{File: blank(t), Denied: true}, []map[string]any{click(".scan-cam"), waitText("The camera is blocked for this site")}, "You can still type the code."},
		{"no camera at all", camera{}, nil, "This device has no camera the app can use. You can still type the code."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps := append(append([]map[string]any{}, c.act...), typed(scanInput, tolkien), waitText("Found on Open Library"))
			rep := scanBrowse(t, w.s.URL, "root", c.cam, at("/catalog/new", steps...))
			v := rep.Visits[0]
			v.must(t, "Found on Open Library")
			if c.said != "" && !strings.Contains(v.Text, c.said) {
				t.Errorf("the screen should say %q:\n%s", c.said, v.Text)
			}
			if !hasField(v, "The Fellowship of the Ring") {
				t.Errorf("typing the ISBN after the camera failed did not fill the book in: %v", v.Fields)
			}
			if v.Notes["absent:Found on Open Library"].Found {
				t.Errorf("a blank picture was taken for a barcode")
			}
			if len(rep.Blocked) > 0 {
				t.Errorf("asked for hosts outside the installation: %v", rep.Blocked)
			}
		})
	}
}

// ---- SCAN-1/3/4: hand scanner and typing give what the camera gives ----------

func TestAHandScannerAndATypistGetTheSameAnswerAsTheCamera(t *testing.T) {
	w := newWorld(t)
	m := w.patron("scanner-sam")
	w.root.want(200, "PUT", "/api/admin/settings", map[string]any{"max_loans": 50})

	t.Run("add a book", func(t *testing.T) {
		const isbn = "9780261103573"
		// What the screen holds once it has looked the ISBN up: every field, and the words.
		// (Only what is about the camera itself differs by device.)
		var got [3]string
		shape := func(v scanVisit) string {
			f, _ := json.Marshal(v.Fields)
			text := strings.NewReplacer(
				"This device has no camera the app can use. You can still type the code.", "", // no camera here
				"Point at the book's ISBN barcode", "", // the viewfinder's hint
			).Replace(v.Text)
			return strings.Join(strings.Fields(text), " ") + string(f)
		}
		for i, a := range []map[string]any{wedge(scanInput, isbn), typed(scanInput, isbn)} {
			rep := scanBrowse(t, w.s.URL, "root", camera{}, at("/catalog/new", a, waitText("Found on Open Library"), sleepMs(500)))
			got[i] = shape(rep.Visits[0])
		}
		rep := scanBrowse(t, w.s.URL, "root", camera{File: frames(t, svgOf(t, barcode.EAN13, isbn))},
			at("/catalog/new", click(".scan-cam"), waitText("Found on Open Library"), sleepMs(500)))
		got[2] = shape(rep.Visits[0])
		if got[0] != got[1] || got[0] != got[2] {
			t.Errorf("the same ISBN gave different screens by hand scanner, typing and camera:\n--- scanner\n%s\n--- typed\n%s\n--- camera\n%s", got[0], got[1], got[2])
		}
	})

	t.Run("check in a copy", func(t *testing.T) {
		var latest [3]string
		for i := range latest {
			code, loan := w.lend(m)
			var a map[string]any
			var cam camera
			switch i {
			case 0:
				a = wedge(scanInput, code)
			case 1:
				a = typed(scanInput, code)
			case 2:
				cam = camera{File: frames(t, svgOf(t, barcode.Code128, code))}
				a = click(".scan-cam")
			}
			rep := scanBrowse(t, w.s.URL, "root", cam, at("/desk/checkin", a, waitText("Checked in"), sleepMs(300)))
			v := rep.Visits[0]
			v.must(t, "Checked in")
			if n := scalar(t, openRO(t, w.s.DataDir), `SELECT count(*) FROM loans WHERE id = ? AND returned_at IS NOT NULL`, loan); n != 1 {
				t.Errorf("round %d: the loan was not returned", i)
			}
			// The part of the screen about this copy: the large card with its code stripped,
			// since each round has its own copy and book.
			latest[i] = strings.NewReplacer(code, "CODE").Replace(afterLine(v.Text, "Checked in"))
			latest[i] = regexp.MustCompile(`Fixture book \d+`).ReplaceAllString(latest[i], "BOOK")
		}
		if latest[0] != latest[1] || latest[0] != latest[2] {
			t.Errorf("a returned copy gave different screens by hand scanner, typing and camera:\n--- scanner\n%s\n--- typed\n%s\n--- camera\n%s", latest[0], latest[1], latest[2])
		}
	})

	t.Run("a scanner reading many labels without a pause", func(t *testing.T) {
		var codes []string
		for i := 0; i < 6; i++ {
			c, _ := w.lend(m)
			codes = append(codes, c)
		}
		rep := scanBrowse(t, w.s.URL, "root", camera{}, at("/desk/checkin", burst(scanInput, codes...), waitText("This session · 6 scans"), sleepMs(500)))
		v := rep.Visits[0]
		v.must(t, "This session · 6 scans")
		// The order is kept: the list shows the newest first, so the codes appear in reverse.
		list := v.Text[strings.Index(v.Text, "This session"):]
		pos := -1
		for i := len(codes) - 1; i >= 0; i-- {
			p := strings.Index(list, codes[i])
			if p <= pos {
				t.Errorf("the session list is out of order at %s:\n%s", codes[i], list)
			}
			pos = p
		}
		for _, c := range codes {
			if n := scalar(t, openRO(t, w.s.DataDir), `SELECT count(*) FROM loans l JOIN identifiers i ON i.copy_id = l.copy_id WHERE i.code = ? AND l.returned_at IS NULL`, c); n != 0 {
				t.Errorf("%s is still out after being scanned in a burst", c)
			}
		}
	})
}

func hasField(v scanVisit, value string) bool {
	for _, f := range v.Fields {
		if f[1] == value {
			return true
		}
	}
	return false
}

// afterLine is the text from the first line that has marker.
func afterLine(text, marker string) string {
	i := strings.Index(text, marker)
	if i < 0 {
		return text
	}
	return text[i:]
}

// ---- SCAN-4, CARD-1: the library card, scanned at the desk -------------------

func TestTheLibraryCardIsReadByCameraFromItsQRAndItsBarcode(t *testing.T) {
	w := newWorld(t)
	m := w.patron("card-carla")
	for _, c := range []struct {
		name string
		path string
		kind barcode.Kind
	}{{"QR", "qr.svg", barcode.QR}, {"barcode", "barcode.svg", barcode.Code128}} {
		t.Run(c.name, func(t *testing.T) {
			// The picture the patron's own screen shows, as the server serves it.
			r := m.W.do("GET", "/api/me/card/"+c.path, nil)
			if r.Status != 200 {
				t.Fatalf("the card image: %d", r.Status)
			}
			cam := camera{File: frames(t, string(r.Raw))}
			rep := scanBrowse(t, w.s.URL, "root", cam,
				at("/desk/checkout", click(".scan-cam"), waitText("Check out"), waitText("Scan a copy to begin"), native(), csp()))
			v := rep.Visits[0]
			v.must(t, "Scan a copy to begin")
			for _, want := range []string{"card-carla", m.Card, "out"} {
				if !strings.Contains(v.Text, want) {
					t.Errorf("the patron wasn't selected by scanning their card's %s: missing %q\n%s", c.name, want, v.Text)
				}
			}
			decoderCameFromHome(t, w, rep, v)
		})
	}
}

// What the patron holds up is what the screen draws: the card page's own
// pictures, at phone size, read back by an independent decoder.
func TestTheCardOnThePatronsScreenCanBeReadBack(t *testing.T) {
	w := newWorld(t)
	m := w.patron("card-cleo")
	rep := scanBrowse(t, w.s.URL, "card-cleo", camera{}, at("/card", sleepMs(800)).clips(".lcard-qr img", ".lcard-bc img"))
	v := rep.Visits[0]
	for sel, kind := range map[string]string{".lcard-qr img": "qr", ".lcard-bc img": "code128"} {
		raw, err := base64.StdEncoding.DecodeString(v.Clips[sel])
		if err != nil || len(raw) == 0 {
			t.Fatalf("no picture of %s", sel)
		}
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		got, err := barcodetest.Decode(kind, img)
		if err != nil {
			t.Errorf("%s on the card screen can't be read: %v", sel, err)
		} else if got != m.Card {
			t.Errorf("%s on the card screen reads %q, the card is %q", sel, got, m.Card)
		}
	}
	for _, want := range []string{"card-cleo", m.Card} {
		if !strings.Contains(v.Text, want) {
			t.Errorf("the card screen lacks %q:\n%s", want, v.Text)
		}
	}
}

func TestAReissuedCardsOldCodeIsToldNoSuchPatron(t *testing.T) {
	w := newWorld(t)
	m := w.patron("card-dev")
	old := string(m.W.want(200, "GET", "/api/me/card/qr.svg", nil).Raw)
	w.root.want(200, "POST", fmt.Sprintf("/api/users/%d/card/reissue", m.ID), nil)
	fresh := string(m.W.want(200, "GET", "/api/me/card/qr.svg", nil).Raw)
	if old == fresh {
		t.Fatal("the new card has the old picture")
	}
	rep := scanBrowse(t, w.s.URL, "root", camera{File: frames(t, old)},
		at("/desk/checkout", click(".scan-cam"), waitText("No such patron"), sleepMs(500)))
	v := rep.Visits[0]
	v.must(t, "No such patron")
	if strings.Contains(v.Text, "Scan a copy to begin") || strings.Contains(v.Text, "card-dev") {
		t.Errorf("the old card selected the patron:\n%s", v.Text)
	}
	rep = scanBrowse(t, w.s.URL, "root", camera{File: frames(t, fresh)},
		at("/desk/checkout", click(".scan-cam"), waitText("Scan a copy to begin")))
	rep.Visits[0].must(t, "Scan a copy to begin")
	if !strings.Contains(rep.Visits[0].Text, "card-dev") {
		t.Errorf("the new card didn't select the patron:\n%s", rep.Visits[0].Text)
	}
}

// ---- SCAN-3, SCAN-4: check out and check in, by card and copy or ISBN, in both modes

func TestCheckOutAndCheckInByCameraInBothCopyIDModes(t *testing.T) {
	w := newWorld(t)
	const isbn = "9780140328721" // the fixture book with a cover, one copy
	book := w.coverBook
	m := w.patron("mode-mia")
	db := openRO(t, w.s.DataDir)
	copyID := scalar(t, db, `SELECT id FROM copies WHERE book_id = ? AND removed_at IS NULL`, int(book))
	out := func() int {
		return scalar(t, db, `SELECT count(*) FROM loans WHERE copy_id = ? AND patron_id = ? AND returned_at IS NULL`, copyID, int(m.ID))
	}

	checkout := func(t *testing.T, cam camera, a map[string]any) scanReport {
		t.Helper()
		return scanBrowse(t, w.s.URL, "root", cam, at("/desk/checkout",
			wedge(scanInput, m.Card), waitText("Scan a copy to begin"), a, waitText("This checkout · 1 book"), sleepMs(1500), native(), csp()))
	}

	for _, mode := range []string{"isbn", "own"} {
		t.Run(mode, func(t *testing.T) {
			w.root.want(200, "PUT", "/api/admin/copy-id-mode", map[string]string{"mode": mode})
			label := isbn
			kind := barcode.EAN13
			if mode == "own" {
				var code string
				if err := db.QueryRow(`SELECT code FROM identifiers WHERE copy_id = ?`, copyID).Scan(&code); err != nil || code == "" {
					t.Fatalf("own-barcode mode gave the copy no barcode: %v", err)
				}
				label, kind = code, barcode.Code128
			}
			cam := camera{File: frames(t, svgOf(t, kind, label))}
			if out() != 0 {
				t.Fatal("the copy starts out")
			}

			// Check out: a patron's card typed, the copy by camera.
			rep := checkout(t, cam, click(".scan-cam"))
			v := rep.Visits[0]
			if out() != 1 {
				t.Errorf("the camera's scan of %s did not check the copy out:\n%s", label, v.Text)
			}
			if got := strings.Count(v.Text, "Fantastic Mr Fox"); got != 1 {
				t.Errorf("the copy held in view for seconds was checked out %d times:\n%s", got, v.Text)
			}
			decoderCameFromHome(t, w, rep, v)

			// Check in by camera. By ISBN it is the book that is read.
			rep = scanBrowse(t, w.s.URL, "root", cam, at("/desk/checkin", click(".scan-cam"), waitText("Checked in"), sleepMs(2500), native(), csp()))
			v = rep.Visits[0]
			v.must(t, "Checked in")
			if out() != 0 {
				t.Errorf("the copy is still out after the camera checked it in")
			}
			if !strings.Contains(v.Text, "This session · 1 scan") {
				t.Errorf("a code held in view was read more than once:\n%s", v.Text)
			}
			decoderCameFromHome(t, w, rep, v)

			// The same by hand scanner and by typing, the card by hand scanner too.
			for _, in := range []func(string, string) map[string]any{wedge, typed} {
				scanBrowse(t, w.s.URL, "root", camera{}, at("/desk/checkout",
					in(scanInput, m.Card), waitText("Scan a copy to begin"), in(scanInput, label), waitText("This checkout · 1 book")))
				if out() != 1 {
					t.Errorf("%s was not checked out by keyboard entry", label)
				}
				rep = scanBrowse(t, w.s.URL, "root", camera{}, at("/desk/checkin", in(scanInput, label), waitText("Checked in")))
				if out() != 0 {
					t.Errorf("%s was not checked in by keyboard entry", label)
				}
			}
		})
	}

	// In own-barcode mode an ISBN names no copy; the screen says to scan the copy's own barcode.
	t.Run("an ISBN in own-barcode mode", func(t *testing.T) {
		rep := scanBrowse(t, w.s.URL, "root", camera{}, at("/desk/checkout",
			wedge(scanInput, m.Card), waitText("Scan a copy to begin"), wedge(scanInput, isbn), waitText("Not checked out")))
		v := rep.Visits[0]
		v.must(t, "Not checked out")
		if !strings.Contains(v.Text, "own barcode") {
			t.Errorf("the refusal should say to scan the copy's own barcode:\n%s", v.Text)
		}
		if out() != 0 {
			t.Errorf("an ISBN checked a copy out in own-barcode mode")
		}
	})
}

// ---- SCAN-3, SCAN-4: a code nothing has --------------------------------------

func TestACodeNothingHasIsSaidSoHoweverItArrives(t *testing.T) {
	w := newWorld(t)
	w.patron("nobody-nell")
	const ghost = "JL-9999"
	cam := camera{File: frames(t, svgOf(t, barcode.Code128, ghost))}

	t.Run("check in", func(t *testing.T) {
		for name, a := range map[string]map[string]any{"scanner": wedge(scanInput, ghost), "typed": typed(scanInput, ghost)} {
			rep := scanBrowse(t, w.s.URL, "root", camera{}, at("/desk/checkin", a, waitText("Nothing matches that code")))
			v := rep.Visits[0]
			v.must(t, "Nothing matches that code")
			if !strings.Contains(v.Text, ghost) || !strings.Contains(v.Text, "Not found") {
				t.Errorf("%s: the screen should show the code and 'Not found':\n%s", name, v.Text)
			}
		}
		rep := scanBrowse(t, w.s.URL, "root", cam, at("/desk/checkin", click(".scan-cam"), waitText("Nothing matches that code"), sleepMs(2500), native(), csp()))
		v := rep.Visits[0]
		v.must(t, "Nothing matches that code")
		if !strings.Contains(v.Text, "This session · 1 scan") {
			t.Errorf("an unknown code held in view was reported again and again:\n%s", v.Text)
		}
		// The server's answer to a code nothing has is a 404: the browser says so in its log.
		decoderCameFromHome(t, w, rep, v, "status of 404")
	})

	t.Run("check out: card", func(t *testing.T) {
		for name, a := range map[string]map[string]any{"scanner": wedge(scanInput, "000000000000"), "typed": typed(scanInput, "000000000000")} {
			rep := scanBrowse(t, w.s.URL, "root", camera{}, at("/desk/checkout", a, waitText("No such patron")))
			v := rep.Visits[0]
			v.must(t, "No such patron")
			if strings.Contains(v.Text, "Scan a copy to begin") {
				t.Errorf("%s: an unknown card began a checkout", name)
			}
		}
		rep := scanBrowse(t, w.s.URL, "root", camera{File: frames(t, svgOf(t, barcode.QR, "000000000000"))},
			at("/desk/checkout", click(".scan-cam"), waitText("No such patron")))
		rep.Visits[0].must(t, "No such patron")
	})

	t.Run("check out: copy", func(t *testing.T) {
		m := w.patron("nell-two")
		rep := scanBrowse(t, w.s.URL, "root", cam, at("/desk/checkout",
			wedge(scanInput, m.Card), waitText("Scan a copy to begin"), click(".scan-cam"), waitText("Nothing matches that code")))
		v := rep.Visits[0]
		v.must(t, "Nothing matches that code")
		if !strings.Contains(v.Text, "Nothing checked out yet") {
			t.Errorf("an unknown copy appears to have been checked out:\n%s", v.Text)
		}
	})
}

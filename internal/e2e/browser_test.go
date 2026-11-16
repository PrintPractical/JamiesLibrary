package e2e

// Browser tests: a real headless Chromium opens the built web app served by a
// real installation. They need Node 22+ (its own WebSocket client; no packages)
// and a Chromium (set JL_CHROME, or Playwright's headless shell in
// ~/.cache/ms-playwright is used; the full Chromium needs a crash handler
// that sandboxed machines may not allow); without either they skip and say so. The web app must be built
// (cd web && npm run build) into internal/webui/dist.
//
// What they prove, which no API test can: markup in any text field is shown as
// text, not run; the pages ask nothing of any host but the installation, and a
// cover fetched once is still shown when the provider has gone, with no request
// to it; and a librarian whose lookup can't reach the provider is told so
// within ten seconds.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

type visitReport struct {
	Path     string
	URL      string
	Text     string
	Pwned    any
	Injected [][]any
	Images   []struct {
		Src      string
		Complete bool
		W        int
	}
	Notes map[string]struct {
		Found   bool
		Seconds float64
	}
	Seconds float64
	Evals   []any
	Console []string // errors the page reported: uncaught, console.error, failed loads
	Shot    string   // PNG, base64, when the step asked for one
	Layout  *layoutReport
}

// layoutReport is what visit.mjs measured in the page (see LAYOUT there).
type layoutReport struct {
	VW              int // the page's width, without a scroll bar
	IW              int // the window's
	ScrollWidth     int
	BodyScrollWidth int
	SticksOut       []string
	InnerScroll     []string
	Covered         []string
	Controls        int
}

type browserReport struct {
	Requests []string
	Blocked  []string
	Dialogs  []string
	Visits   []visitReport
	Errors   []string
}

type browserStep struct {
	Path    string           `json:"path,omitempty"`
	Actions []map[string]any `json:"actions,omitempty"`
	Probes  []string         `json:"probes,omitempty"`
	Layout  bool             `json:"layout,omitempty"` // measure the page's width and reach
	Shot    bool             `json:"shot,omitempty"`   // take a picture (kept when JL_SHOTS names a folder)
}

// browseOpts is the window the browser opens: 1280x900 by default.
type browseOpts struct {
	Width, Height int
}

func findChrome() string {
	if p := os.Getenv("JL_CHROME"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, pat := range []string{
		filepath.Join(home, ".cache/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-linux64/chrome-headless-shell"),
		filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux64/chrome"),
		filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux/chrome"),
	} {
		m, _ := filepath.Glob(pat)
		sort.Strings(m)
		if len(m) > 0 {
			return m[len(m)-1]
		}
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// browse runs steps in a fresh browser, signed in as user (or not, when user is
// empty), and returns what it saw. Anything off the installation's origin is
// recorded in Blocked and refused.
func browse(t *testing.T, base, user string, steps []browserStep) browserReport {
	t.Helper()
	return browseAt(t, base, user, browseOpts{}, steps)
}

func browseAt(t *testing.T, base, user string, o browseOpts, steps []browserStep) browserReport {
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
	sc := map[string]any{"chrome": chrome, "base": base, "steps": steps}
	if o.Width > 0 {
		sc["width"] = o.Width
	}
	if o.Height > 0 {
		sc["height"] = o.Height
	}
	if user != "" {
		sc["signIn"] = map[string]string{"username": user, "password": adminPassword}
	}
	dir := t.TempDir()
	sc["out"] = filepath.Join(dir, "report.json")
	b, _ := json.Marshal(sc)
	file := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd := exec.Command(node, filepath.Join("browser", "visit.mjs"), file)
	cmd.Stderr = &errb
	runErr := cmd.Run()
	out, readErr := os.ReadFile(filepath.Join(dir, "report.json"))
	if runErr != nil && readErr != nil {
		t.Fatalf("the browser driver failed: %v\n%s", runErr, errb.String())
	}
	var rep browserReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("the driver's report: %v\n%.2000s", err, out)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("the driver reported: %v", rep.Errors)
	}
	return rep
}

// ---- markup in every text field ---------------------------------------------

func TestMarkupInEveryTextFieldIsShownAsText(t *testing.T) {
	w := newWorld(t)
	mark := func(tag string) string { return `<img src=x onerror="window.__pwned=1">` + tag }
	script := func(tag string) string { return `<script>window.__pwned=1</script>` + tag }
	svg := func(tag string) string { return `"><svg/onload=window.__pwned=1>` + tag }

	title, author, desc, shelf, libName, review, display := mark("TITLE-X"), script("AUTHOR-X"), svg("DESC-X"), mark("SHELF-X"), script("LIBNAME-X"), svg("REVIEW-X"), mark("NAME-X")

	loc := idOf(w.root.want(201, "POST", "/api/locations", map[string]string{"name": shelf}), "id")
	book := idOf(w.root.want(201, "POST", "/api/books", map[string]any{"title": title, "authors": []string{author, "Second Author"}, "description": desc}), "id")
	w.root.want(201, "POST", fmt.Sprintf("/api/books/%d/copies", book), map[string]any{"location_id": loc, "condition": "good"})
	w.root.want(200, "PUT", "/api/admin/branding", map[string]any{"name": libName, "scheme": "hearth", "accent": ""})

	_, invite := w.root.invite("patron")
	mal := newWho(t, w.s)
	if r := mal.do("POST", "/api/register", map[string]string{"code": invite, "username": "mallory", "display_name": display, "password": adminPassword}); r.Status != 201 {
		t.Fatalf("a display name with markup was refused (%d): %s", r.Status, r.Raw)
	}
	mal.want(200, "PUT", fmt.Sprintf("/api/books/%d/review", book), map[string]any{"rating": 3, "text": review})

	probes := []string{`img[src="x"]`, `svg[onload]`, `[onerror]`, `script:not([src])`, `svg`}
	id := fmt.Sprint(book)
	// The staff and a patron's view of the same pages.
	staff := browse(t, w.s.URL, "root", []browserStep{
		{Path: "/browse?q=TITLE-X", Probes: probes},
		{Path: "/books/" + id, Probes: probes},
		{Path: "/desk/patrons", Probes: probes, Actions: []map[string]any{{"type": map[string]string{"selector": "input", "text": "mallory"}}, {"sleepMs": 1200}}},
		{Path: "/admin/people", Probes: probes},
		{Path: "/admin/activity", Probes: probes},
		{Path: "/admin/settings", Probes: probes},
		{Path: "/catalog/new", Probes: probes},
		{Path: "/account", Probes: probes},
	})
	patron := browse(t, w.s.URL, "mallory", []browserStep{
		{Path: "/books/" + id, Probes: probes},
		{Path: "/account", Probes: probes},
		{Path: "/browse", Probes: probes},
	})
	anon := browse(t, w.s.URL, "", []browserStep{{Path: "/signin", Probes: probes}})

	for who, rep := range map[string]browserReport{"staff": staff, "patron": patron, "visitor": anon} {
		if len(rep.Dialogs) > 0 {
			t.Errorf("%s: a script ran and opened a dialog: %v", who, rep.Dialogs)
		}
		for _, v := range rep.Visits {
			if v.Pwned != nil {
				t.Errorf("%s %s: window.__pwned was set: the markup ran", who, v.Path)
			}
			for _, in := range v.Injected {
				// Own page markup may include an svg icon: only the other probes must be zero.
				if in[0] != "svg" && in[1].(float64) != 0 {
					t.Errorf("%s %s: %v elements match %s: markup became elements", who, v.Path, in[1], in[0])
				}
			}
		}
	}
	// Each field was displayed, as the text the user typed, somewhere it belongs.
	want := []struct {
		rep   browserReport
		path  string
		texts []string
	}{
		{staff, "/browse?q=TITLE-X", []string{title, author}},
		{staff, "/books/" + id, []string{title, author, desc, shelf, review, display}},
		{staff, "/admin/people", []string{display}},
		{patron, "/account", []string{display}},
		{patron, "/books/" + id, []string{libName, title}},
	}
	for _, c := range want {
		var got *visitReport
		for i := range c.rep.Visits {
			if c.rep.Visits[i].Path == c.path {
				got = &c.rep.Visits[i]
			}
		}
		if got == nil {
			t.Fatalf("no visit of %s", c.path)
		}
		for _, s := range c.texts {
			if !strings.Contains(got.Text, s) {
				t.Errorf("%s does not show %q as text:\n%.600s", c.path, s, got.Text)
			}
		}
	}
	if len(staff.Blocked)+len(patron.Blocked) > 0 {
		t.Errorf("requests left the installation: %v %v", staff.Blocked, patron.Blocked)
	}
}

// ---- offline -----------------------------------------------------------------

func TestOfflinePagesLoadAndTheCoverOutlivesTheProvider(t *testing.T) {
	w := newWorld(t) // its first book has a cover fetched from the fake provider
	book := w.coverBook
	reqsBefore := len(w.ol.Requests())
	w.ol.Close() // the provider is gone

	pages := []browserStep{
		{Path: "/browse"}, {Path: fmt.Sprintf("/books/%d", book)}, {Path: "/desk/checkout"}, {Path: "/desk/checkin"},
		{Path: "/desk/patrons"}, {Path: "/desk/overdue"}, {Path: "/catalog/new"}, {Path: "/admin/settings"},
		{Path: "/admin/people"}, {Path: "/admin/statistics"}, {Path: "/admin/activity"}, {Path: "/account"}, {Path: "/more"},
	}
	rep := browse(t, w.s.URL, "root", pages)
	checkOffline(t, "staff", rep, w.s.URL)
	patron := browse(t, w.s.URL, "alice-a", []browserStep{{Path: "/browse"}, {Path: fmt.Sprintf("/books/%d", book)}, {Path: "/account"}, {Path: "/loans"}, {Path: "/card"}})
	checkOffline(t, "patron", patron, w.s.URL)
	anon := browse(t, w.s.URL, "", []browserStep{{Path: "/signin"}, {Path: "/register"}, {Path: "/setup"}})
	checkOffline(t, "visitor", anon, w.s.URL)

	// CAT-T1: the book page shows its cover, served by the installation.
	var shown bool
	for _, rp := range []browserReport{rep, patron} {
		for _, v := range rp.Visits {
			if v.Path != fmt.Sprintf("/books/%d", book) {
				continue
			}
			shown = false
			for _, im := range v.Images {
				if strings.HasPrefix(im.Src, w.s.URL+"/media/") && im.Complete && im.W > 1 {
					shown = true
				}
			}
			if !shown {
				t.Errorf("the book page does not show its cover with the provider gone: images %+v", v.Images)
			}
		}
	}
	if n := len(w.ol.Requests()); n != reqsBefore {
		t.Errorf("the provider was asked %d times while the pages were browsed", n-reqsBefore)
	}
}

func checkOffline(t *testing.T, who string, rep browserReport, base string) {
	t.Helper()
	if len(rep.Blocked) > 0 {
		t.Errorf("%s: requests to other hosts (DEPLOY-T5, CAT-T1): %v", who, rep.Blocked)
	}
	if len(rep.Requests) == 0 {
		t.Errorf("%s: the browser recorded no requests: the check proved nothing", who)
	}
	for _, v := range rep.Visits {
		if strings.TrimSpace(v.Text) == "" {
			t.Errorf("%s %s: the page is blank", who, v.Path)
		}
		for _, im := range v.Images {
			if !im.Complete || (im.W == 0 && !strings.HasSuffix(im.Src, ".svg")) {
				t.Errorf("%s %s: an image did not load: %+v", who, v.Path, im)
			}
		}
	}
}

// DEPLOY-T5 on the screen: the provider never answers, and the librarian is
// told within 10 seconds, with the form open for the details by hand.
func TestAddBookScreenSaysLookupIsUnavailableWithinTenSeconds(t *testing.T) {
	ol := testutil.FakeOpenLibrary(t)
	ol.SetMode(testutil.ModeSlow)
	ol.SetSlowDelay(30 * time.Second)
	s, root := installWith(t, ol)
	_ = root
	rep := browse(t, s.URL, "root", []browserStep{{
		Path: "/catalog/new",
		Actions: []map[string]any{
			{"type": map[string]string{"selector": "input", "text": "9780140328721"}},
			{"clickText": "Look up"},
			{"waitText": "could not be reached", "seconds": 14},
		},
	}})
	v := rep.Visits[0]
	n := v.Notes["could not be reached"]
	if !n.Found {
		t.Fatalf("the screen never said the lookup was unavailable:\n%.800s", v.Text)
	}
	if n.Seconds > 11.5 {
		t.Errorf("told after %.1f s, over the 10 s limit", n.Seconds)
	}
	if n.Seconds < 8 {
		t.Errorf("told after %.1f s: the provider was meant to hold the request; is the fake slow?", n.Seconds)
	}
	if !strings.Contains(v.Text, "Enter the details") && !strings.Contains(v.Text, "enter the details") {
		t.Errorf("the screen doesn't offer entering the details by hand:\n%.800s", v.Text)
	}
	if len(rep.Blocked) > 0 {
		t.Errorf("requests left the installation: %v", rep.Blocked)
	}
}

func installWith(t *testing.T, ol *testutil.FakeOL) (*testutil.Server, *who) {
	t.Helper()
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: ol})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatal("no setup code")
	}
	root := newWho(t, s)
	root.want(200, "POST", "/api/setup/admin", map[string]string{"code": code, "username": "root", "display_name": "Root", "password": adminPassword})
	return s, root
}

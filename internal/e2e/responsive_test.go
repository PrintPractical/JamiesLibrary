package e2e

// UX-1 and UX-2 in a real browser: every screen, for every kind of user, at the
// width of a small phone (360 px) and of a desktop (1280 px).
//
// For each screen the page is measured from inside (browser/visit.mjs, LAYOUT):
// the document is no wider than the window, nothing sticks out of either edge,
// nothing is scrolled sideways inside the page, and every button, link and
// field can be reached: after it is scrolled into view, nothing else (such as
// the tab bar pinned to the bottom) is on top of it. The page must also have
// raised no error of its own.
//
// Set JL_SHOTS to a folder to keep a picture of each screen (role-width-path.png)
// to read.

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var widths = []int{360, 1280}

// screen is one page to open. Redirect is where it is expected to end up, when
// that isn't the page itself; Unbuilt marks a page the app still shows as a placeholder.
type screen struct {
	Path     string
	Redirect string
	WideTo   string // where a window of 900 px or more ends up instead (/more has no use beside a side bar)
	Actions  []map[string]any
}

// sweepWorld is a library with enough in it to fill every screen: overdue loans
// (time has moved on), reviews, covers, a logo, patrons and an unknown copy.
func sweepWorld(t *testing.T) (*world, int64) {
	w := newWorld(t)
	// A few more books, copies and patrons, so lists have rows and wrap.
	for i := 0; i < 6; i++ {
		b := w.newBook()
		w.newCopy(b)
	}
	w.account("librarian", "lib-l")
	if err := w.s.Advance(45 * 24 * time.Hour); err != nil { // both loans are now overdue
		t.Fatal(err)
	}
	return w, w.bookID
}

func shotPath(role string, width int, path string) string {
	slug := regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(path, "-")
	return fmt.Sprintf("%s-%d-%s.png", role, width, strings.Trim(slug, "-"))
}

// sweep opens screens as user at each width and fails on any layout fault.
func sweep(t *testing.T, w *world, role, user string, screens []screen) {
	t.Helper()
	dir := os.Getenv("JL_SHOTS")
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	for _, width := range widths {
		steps := make([]browserStep, len(screens))
		for i, sc := range screens {
			steps[i] = browserStep{Path: sc.Path, Layout: true, Shot: dir != "", Actions: sc.Actions}
		}
		rep := browseAt(t, w.s.URL, user, browseOpts{Width: width, Height: 800}, steps)
		if len(rep.Visits) != len(screens) {
			t.Fatalf("%s at %d: %d screens opened of %d", role, width, len(rep.Visits), len(screens))
		}
		for i, v := range rep.Visits {
			sc := screens[i]
			who := fmt.Sprintf("%s at %d px, %s", role, width, sc.Path)
			if dir != "" && v.Shot != "" {
				png, _ := base64.StdEncoding.DecodeString(v.Shot)
				_ = os.WriteFile(filepath.Join(dir, shotPath(role, width, sc.Path)), png, 0o644)
			}
			want := sc.Path
			if sc.Redirect != "" {
				want = sc.Redirect
			}
			if sc.WideTo != "" && width >= 900 {
				want = sc.WideTo
			}
			if i := strings.Index(want, "?"); i >= 0 && sc.Redirect == "" {
				want = want[:i]
			}
			if got := strings.SplitN(v.URL, "?", 2)[0]; got != want {
				t.Errorf("%s: ended at %s, expected %s", who, v.URL, want)
			}
			if strings.TrimSpace(v.Text) == "" {
				t.Errorf("%s: the page is blank", who)
			}
			if strings.Contains(v.Text, "Something went wrong") {
				t.Errorf("%s: the page shows an error:\n%.300s", who, v.Text)
			}
			l := v.Layout
			if l == nil {
				t.Fatalf("%s: no measurements", who)
			}
			if l.IW != width {
				t.Errorf("%s: the window is %d px wide, not %d", who, l.IW, width)
			}
			if l.ScrollWidth > l.VW || l.BodyScrollWidth > l.VW {
				t.Errorf("%s: the page scrolls sideways: document %d, body %d, window %d", who, l.ScrollWidth, l.BodyScrollWidth, l.VW)
			}
			if len(l.SticksOut) > 0 {
				t.Errorf("%s: sticks out of the window: %v", who, head(l.SticksOut))
			}
			if len(l.InnerScroll) > 0 {
				t.Errorf("%s: content scrolls sideways inside the page: %v", who, head(l.InnerScroll))
			}
			if len(l.Covered) > 0 {
				t.Errorf("%s: %d of %d controls cannot be reached: %v", who, len(l.Covered), l.Controls, head(l.Covered))
			}
			if l.Controls == 0 {
				t.Errorf("%s: no controls were found: the measurement proved nothing", who)
			}
			for _, c := range v.Console {
				// A visitor is told "not signed in" by the browser's own log: the app asks who it is first.
				if strings.Contains(c, "401 (Unauthorized)") && strings.HasSuffix(c, "/api/me") && user == "" {
					continue
				}
				// Sign in and register ask whether setup is open: once an administrator exists the server answers 404.
				if strings.Contains(c, "404 (Not Found)") && strings.HasSuffix(c, "/api/setup/status") && user == "" {
					continue
				}
				t.Errorf("%s: the page reported: %s", who, c)
			}
		}
		if len(rep.Blocked) > 0 {
			t.Errorf("%s at %d: requests left the installation: %v", role, width, rep.Blocked)
		}
	}
}

func head(s []string) []string {
	if len(s) > 6 {
		return append(s[:6:6], fmt.Sprintf("... and %d more", len(s)-6))
	}
	return s
}

func TestEveryScreenFitsAPhoneAndADesktop(t *testing.T) {
	w, book := sweepWorld(t)
	bk := fmt.Sprint(book)
	cover := fmt.Sprint(w.coverBook)

	patron := []screen{
		{Path: "/"}, {Path: "/browse"}, {Path: "/browse?q=fixture"}, {Path: "/books/" + bk}, {Path: "/books/" + cover},
		{Path: "/card"}, {Path: "/loans"}, {Path: "/checkout"}, {Path: "/account"},
		{Path: "/no/such/page"},
		// Staff screens are not a patron's: they end at the patron's home.
		{Path: "/desk", Redirect: "/"}, {Path: "/more", Redirect: "/"},
	}
	staff := []screen{
		{Path: "/desk", Redirect: "/desk/checkout"}, {Path: "/desk/checkout"}, {Path: "/desk/checkin"}, {Path: "/desk/patrons"},
		{Path: fmt.Sprintf("/desk/patrons/%d", w.pa.ID)}, {Path: "/desk/overdue"},
		{Path: "/catalog"}, {Path: "/catalog/new"}, {Path: "/catalog/books/" + bk}, {Path: "/catalog/locations"},
		{Path: "/catalog/labels?book=" + bk}, {Path: "/more", WideTo: "/desk/checkout"}, {Path: "/account"},
		{Path: "/browse"}, {Path: "/books/" + bk}, {Path: "/books/" + cover}, {Path: "/loans"}, {Path: "/card"}, {Path: "/"},
	}
	admin := append(append([]screen{}, staff...),
		screen{Path: "/admin/settings"}, screen{Path: "/admin/people"}, screen{Path: fmt.Sprintf("/admin/people/%d", w.pa.ID)},
		screen{Path: "/admin/statistics"}, screen{Path: "/admin/activity"})

	t.Run("patron", func(t *testing.T) { sweep(t, w, "patron", "alice-a", patron) })
	t.Run("librarian", func(t *testing.T) { sweep(t, w, "librarian", "lib-l", staff) })
	t.Run("admin", func(t *testing.T) { sweep(t, w, "admin", "root", admin) })

	t.Run("visitor", func(t *testing.T) {
		sweep(t, w, "visitor", "", []screen{
			{Path: "/signin"}, {Path: "/register"},
			{Path: "/browse", Redirect: "/signin"}, {Path: "/desk", Redirect: "/signin"},
		})
		w.root.mustSignIn("root", adminPassword) // the sessions ran out when time moved on
		w.root.want(200, "PUT", "/api/admin/settings", map[string]any{"public_catalog": true})
		sweep(t, w, "visitor-public", "", []screen{
			{Path: "/browse"}, {Path: "/browse?q=fox"}, {Path: "/books/" + cover}, screen{Path: "/"}.redirect("/browse"),
			{Path: "/signin"}, {Path: "/desk", Redirect: "/signin"},
		})
	})
}

func (s screen) redirect(to string) screen { s.Redirect = to; return s }

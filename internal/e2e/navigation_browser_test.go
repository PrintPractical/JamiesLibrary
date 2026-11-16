package e2e

// UX-2 in a real browser: each kind of user gets their own navigation, and
// staff pages are not a patron's to open. Checked at the phone and desktop
// widths, because the navigation is a different element in each (a tab bar
// and a side bar).

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// navLinks lists the links of the visible main navigation, as "label>href".
const navLinks = `[...document.querySelectorAll('nav[aria-label="Main"] a')]
  .filter((a) => { const r = a.getBoundingClientRect(), s = getComputedStyle(a); return r.width > 0 && r.height > 0 && s.visibility !== "hidden"; })
  .map((a) => a.textContent.trim() + ">" + a.getAttribute("href"))`

// everyHref lists every link on the page, shown or not.
const everyHref = `[...document.querySelectorAll("a[href]")].map((a) => a.getAttribute("href"))`

const heading = `(document.querySelector("h1")?.textContent ?? "").trim()`

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

var staffPaths = []string{"/desk", "/desk/checkout", "/desk/checkin", "/desk/patrons", "/desk/overdue", "/catalog", "/catalog/new",
	"/catalog/locations", "/catalog/labels", "/admin/settings", "/admin/people", "/admin/statistics", "/admin/activity", "/more"}

func TestPatronNavigationHasNoStaffLinksAndStaffPagesAreRefused(t *testing.T) {
	w := newWorld(t)
	book := fmt.Sprint(w.bookID)
	patronNav := []string{"Home>/", "Browse>/browse", "My card>/card", "My books>/loans", "Account>/account"}
	for _, width := range widths {
		var steps []browserStep
		for _, p := range []string{"/", "/browse", "/books/" + book, "/card", "/loans", "/checkout", "/account", "/nowhere"} {
			steps = append(steps, browserStep{Path: p, Actions: []map[string]any{{"eval": navLinks}, {"eval": everyHref}}})
		}
		for _, p := range staffPaths {
			steps = append(steps, browserStep{Path: p, Actions: []map[string]any{{"eval": heading}, {"eval": everyHref}}})
		}
		rep := browseAt(t, w.s.URL, "alice-a", browseOpts{Width: width, Height: 800}, steps)
		for _, v := range rep.Visits {
			where := fmt.Sprintf("patron at %d px, %s", width, v.Path)
			hrefs := strs(v.Evals[len(v.Evals)-1])
			for _, h := range hrefs {
				for _, staff := range []string{"/desk", "/catalog", "/admin", "/more"} {
					if h == staff || strings.HasPrefix(h, staff+"/") || strings.HasPrefix(h, staff+"?") {
						t.Errorf("%s: a link to the staff area %q is on the page", where, h)
					}
				}
			}
			if strings.HasPrefix(v.Path, "/") && !contains(staffPaths, v.Path) {
				if got := strs(v.Evals[0]); !reflect.DeepEqual(got, patronNav) {
					t.Errorf("%s: navigation is %v, want %v", where, got, patronNav)
				}
				continue
			}
			// A staff page: the patron ends at their own home, and sees none of the staff page.
			if v.URL != "/" {
				t.Errorf("%s: a patron stayed at %s", where, v.URL)
			}
			if h, _ := v.Evals[0].(string); !strings.Contains(h, "evening") && !strings.Contains(h, "morning") && !strings.Contains(h, "afternoon") {
				t.Errorf("%s: the patron's home was not shown; heading %q", where, h)
			}
		}
	}

	// The server refuses the same pages' data, whatever the browser does.
	for _, call := range []struct{ method, path string }{
		{"GET", "/api/admin/settings"}, {"GET", "/api/admin/users"}, {"GET", "/api/desk/overdue"}, {"POST", "/api/catalog/labels"},
	} {
		if r := w.pa.W.do(call.method, call.path, map[string]any{}); r.Status != 403 {
			t.Errorf("patron %s %s: status %d, want 403", call.method, call.path, r.Status)
		}
	}
}

func TestStaffHomeIsOneTapFromCheckOutAndCheckIn(t *testing.T) {
	w := newWorld(t)
	w.account("librarian", "lib-l")
	for _, user := range []string{"lib-l", "root"} {
		for _, width := range widths {
			click := func(label string) []map[string]any {
				// Wait for the new screen's own heading, not just the new address: the old heading stays until it draws.
				return []map[string]any{{"clickText": label}, {"waitFor": fmt.Sprintf(`%s === %q`, heading, label), "seconds": 10}}
			}
			rep := browseAt(t, w.s.URL, user, browseOpts{Width: width, Height: 800}, []browserStep{
				{Actions: []map[string]any{{"eval": navLinks}}}, // where signing in left them
				{Path: "/desk", Actions: append(click("Check out"), map[string]any{"eval": heading})},
				{Path: "/desk", Actions: append(click("Check in"), map[string]any{"eval": heading})},
			})
			where := fmt.Sprintf("%s at %d px", user, width)
			home := rep.Visits[0]
			if home.URL != "/desk" {
				t.Errorf("%s: signing in led to %s, want the staff home /desk", where, home.URL)
			}
			nav := strs(home.Evals[0])
			for _, want := range []string{"Check out>/desk/checkout", "Check in>/desk/checkin"} {
				if !contains(nav, want) {
					t.Errorf("%s: the staff home's navigation %v lacks %q", where, nav, want)
				}
			}
			for i, c := range []struct{ url, h string }{{"/desk/checkout", "Check out"}, {"/desk/checkin", "Check in"}} {
				v := rep.Visits[i+1]
				if v.URL != c.url || v.Evals[len(v.Evals)-1] != c.h {
					t.Errorf("%s: one tap on %q from the home led to %s with heading %v, want %s and %q", where, c.h, v.URL, v.Evals[len(v.Evals)-1], c.url, c.h)
				}
			}
			if len(nav) > 0 && strings.Contains(strings.Join(nav, " "), "Settings") != (user == "root" && width >= 900) {
				// On a phone the admin items are under More; on a desktop only an administrator has them.
				t.Errorf("%s: navigation %v shows the admin group wrongly", where, nav)
			}
		}
	}
}

func TestLibrarianHasNoAdminNavigationAndAdminPagesSayNoAccess(t *testing.T) {
	w := newWorld(t)
	w.account("librarian", "lib-l")
	adminPaths := []string{"/admin/settings", "/admin/people", "/admin/statistics", "/admin/activity"}
	var steps []browserStep
	for _, p := range adminPaths {
		steps = append(steps, browserStep{Path: p, Actions: []map[string]any{{"eval": `document.body.innerText`}, {"eval": navLinks}}})
	}
	rep := browseAt(t, w.s.URL, "lib-l", browseOpts{Width: 1280, Height: 800}, steps)
	for _, v := range rep.Visits {
		body := v.Evals[0].(string)
		if strings.Contains(strings.Join(strs(v.Evals[1]), " "), "/admin") {
			t.Errorf("a librarian's navigation links to the admin area: %v", v.Evals[1])
		}
		// The people page and the others are an administrator's: a librarian gets the refusal, not the page.
		// (The page's own check says "access"; without it the server's refusal says "permission".)
		if !strings.Contains(body, "don't have access") && !strings.Contains(body, "don't have permission") {
			t.Errorf("%s as a librarian: no refusal shown:\n%.300s", v.Path, body)
		}
		for _, form := range []string{"Save name and look", "Create invitation", "Activity log"} {
			if strings.Contains(body, form) && !strings.Contains(strings.Join(strs(v.Evals[1]), " "), form) {
				t.Errorf("%s as a librarian shows the admin page's %q", v.Path, form)
			}
		}
	}
}

func TestVisitorNavigation(t *testing.T) {
	w := newWorld(t)
	rep := browseAt(t, w.s.URL, "", browseOpts{Width: 360, Height: 800}, []browserStep{{Path: "/desk"}, {Path: "/catalog/new"}, {Path: "/admin/settings"}, {Path: "/loans"}})
	for _, v := range rep.Visits {
		if v.URL != "/signin" {
			t.Errorf("a visitor opening %s ended at %s, want /signin", v.Path, v.URL)
		}
	}
	w.root.want(200, "PUT", "/api/admin/settings", map[string]any{"public_catalog": true})
	for _, width := range widths {
		rep := browseAt(t, w.s.URL, "", browseOpts{Width: width, Height: 800}, []browserStep{
			{Path: "/browse", Actions: []map[string]any{{"eval": navLinks}, {"eval": everyHref}}},
		})
		got := strs(rep.Visits[0].Evals[0])
		if want := []string{"Browse>/browse", "Sign in>/signin"}; !reflect.DeepEqual(got, want) {
			t.Errorf("visitor navigation at %d px is %v, want %v", width, got, want)
		}
		for _, h := range strs(rep.Visits[0].Evals[1]) {
			if strings.HasPrefix(h, "/desk") || strings.HasPrefix(h, "/admin") || strings.HasPrefix(h, "/catalog") || h == "/loans" || h == "/card" {
				t.Errorf("a visitor sees a link to %s", h)
			}
		}
	}
}

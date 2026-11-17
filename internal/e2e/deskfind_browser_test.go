package e2e

// Finding a patron by name at Check out (CIRC-8) in a real browser: headless
// Chromium, a real installation, and a librarian who holds circ.desk and
// nothing else, at a phone's width and a desktop's. The keys are real key
// events; a choice is a real click. Where a test needs the network to be slow or
// gone it is the browser's own network that is held or refused (see "rules" in
// browser/scan.mjs), so the page meets exactly what it would meet.
//
// They need Node 22+ and Chromium and a built web app (see browser_test.go);
// without them they skip.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"jamieslibrary/internal/perm"
)

// matchRows is what the list under the patron field holds, one entry per match.
const matchRows = `[...document.querySelectorAll("section.matches li")].map((li) => ({
  text: li.innerText.replace(/\s+/g, " ").trim(),
  button: !!li.querySelector("button"),
  controls: li.querySelectorAll("button, a, input, [role=button], [tabindex]").length,
}))`

const matchHead = `document.querySelector("section.matches h3")?.textContent ?? ""`

// patronPanel is the selected patron's card on the screen, or "" when nobody is selected.
const patronPanel = `document.querySelector('section[aria-label="Patron"]')?.innerText.replace(/\s+/g, " ").trim() ?? ""`

// copyField says whether the screen is asking for a copy (which it does only once a patron is selected).
const copyField = `[...document.querySelectorAll("label")].some((l) => l.textContent.trim() === "Copy barcode")`

const noSideways = `document.documentElement.scrollWidth <= innerWidth`

func chooseMatch(name string) map[string]any {
	return eval("chose", fmt.Sprintf(`(() => {
  const b = [...document.querySelectorAll("section.matches button.match")].find((b) => b.innerText.includes(%q));
  if (!b) return false;
  b.click();
  return true;
})()`, name))
}

// search types q at the patron field as a person does, and presses Enter.
func search(q string) map[string]any {
	return map[string]any{"type": map[string]any{"selector": scanInput, "text": q, "delayMs": 15}}
}

// typeOnly types without pressing Enter.
func typeOnly(q string) map[string]any {
	return map[string]any{"type": map[string]any{"selector": scanInput, "text": q, "delayMs": 15, "enter": false}}
}

type matchRow struct {
	Text     string
	Button   bool
	Controls int
}

func (v scanVisit) rows(t *testing.T, as string) []matchRow {
	t.Helper()
	b, _ := json.Marshal(v.Evals[as])
	var out []matchRow
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("the rows %v: %v", v.Evals[as], err)
	}
	return out
}

func (v scanVisit) str(as string) string { s, _ := v.Evals[as].(string); return s }

// shop is a library with the people to find, and a librarian who may only check out.
type shop struct {
	*deskFindWorld
}

// newShop: Ann Lee (one book out) and Anna Ko; two Sam Readers; a librarian, Marla Staff;
// a deactivated Dee Gone; Nora Nocard, who has no card; the searchers: "deskonly" (circ.desk
// and nothing else), "lib" (a librarian) and the administrator "root".
//
// These tests share the package's ten minutes with everything else, so they run side by side
// (a few at a time: each is a server and a browser). The test that depends on timing does not.
func newShop(t *testing.T) *shop {
	t.Helper()
	t.Parallel()
	shopSlots <- struct{}{}
	t.Cleanup(func() { <-shopSlots })
	return newShopNow(t)
}

var shopSlots = make(chan struct{}, 4)

func newShopNow(t *testing.T) *shop {
	t.Helper()
	s, root := install(t, nil)
	d := &deskFindWorld{t: t, s: s, root: root, ids: map[string]int64{}, web: map[string]*who{}, card: map[string]string{}}
	d.ids["root"] = 1
	d.add("patron", "alee", "Ann Lee")
	d.add("patron", "annko", "Anna Ko")
	d.add("patron", "sam.r1", "Sam Reader")
	d.add("patron", "sam.r2", "Sam Reader")
	d.add("librarian", "marla", "Marla Staff")
	d.add("patron", "deegone", "Dee Gone")
	d.add("patron", "nora", "Nora Nocard")
	d.add("librarian", "lib", "Lib Rarian")
	d.add("patron", "deskonly", "Desk Only")
	d.only("deskonly", perm.CircDesk)
	root.want(200, "POST", fmt.Sprintf("/api/admin/users/%d/deactivate", d.ids["deegone"]), nil)
	db, err := sql.Open("sqlite", "file:"+s.DataDir+"/library.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`DELETE FROM identifiers WHERE user_id = ?`, d.ids["nora"]); err != nil {
		t.Fatalf("making a user with no card: %v", err)
	}
	sh := &shop{d}
	// Ann Lee has a book out.
	d.web["deskonly"].want(200, "POST", "/api/desk/checkout", map[string]string{"patron": d.card["alee"], "code": d.copyCode("Already out")})
	return sh
}

// at opens Check out at the width as user, with the network rules, and runs the steps.
func (sh *shop) at(t *testing.T, user string, width int, rules []map[string]any, steps ...step) scanReport {
	t.Helper()
	rep := scanBrowseWith(t, sh.s.URL, user, camera{}, map[string]any{"width": width, "height": 800, "rules": rules}, steps...)
	if len(rep.Blocked) > 0 {
		t.Errorf("the page asked for hosts outside the installation: %v", rep.Blocked)
	}
	if len(rep.Dialogs) > 0 {
		t.Errorf("a dialog opened: %v", rep.Dialogs)
	}
	return rep
}

func (sh *shop) forWidths(t *testing.T, f func(t *testing.T, width int)) {
	t.Helper()
	for _, w := range widths {
		t.Run(fmt.Sprintf("%dpx", w), func(t *testing.T) { f(t, w) })
	}
}

func findCalls(rep scanReport) []string {
	var out []string
	for _, c := range rep.Calls {
		if strings.Contains(c, "/api/desk/find") {
			out = append(out, c)
		}
	}
	return out
}

const checkout = "/desk/checkout"

// ---- CIRC-8 scenarios -------------------------------------------------------------

// "Find by name": both are listed with name, username, card and books out; choosing
// Ann Lee selects her exactly as scanning her card does, and a checkout then works.
func TestBrowserFindByNameListsBothAndChoosingSelectsLikeAScan(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		before := openLoansOfPatron(t, openRO(t, sh.s.DataDir), sh.ids["alee"])
		copyCode := sh.copyCode(fmt.Sprintf("Checked out at %d", width))
		rep := sh.at(t, "deskonly", width, nil,
			at(checkout, wedge(scanInput, sh.card["alee"]), waitText("Copy barcode"), eval("scanPanel", patronPanel)),
			at(checkout, search("ann"), waitText("Matches for"), eval("head", matchHead), eval("rows", matchRows), eval("panel", patronPanel),
				eval("fits", noSideways), eval("copy", copyField), chooseMatch("Ann Lee"), waitText("Copy barcode"),
				eval("chosenPanel", patronPanel), eval("listGone", matchHead),
				wedge(scanInput, copyCode), waitText("This checkout · 1 book")),
		)
		v := rep.Visits[1]
		if got := v.str("head"); got != "Matches for ‘ann’" {
			t.Errorf("the list's heading is %q", got)
		}
		rows := v.rows(t, "rows")
		if len(rows) != 2 {
			t.Fatalf("%d matches for ann, want Ann Lee and Anna Ko: %+v", len(rows), rows)
		}
		for i, want := range []struct{ name, user, card, out string }{
			{"Ann Lee", "alee", sh.card["alee"], strings.Replace(fmt.Sprintf("%d books out", before), "1 books", "1 book", 1)},
			{"Anna Ko", "annko", sh.card["annko"], "0 books out"},
		} {
			for _, s := range []string{want.name, want.user, want.card, want.out} {
				if !strings.Contains(rows[i].Text, s) {
					t.Errorf("match %d is %q, it should show %q", i, rows[i].Text, s)
				}
			}
			if !rows[i].Button {
				t.Errorf("match %d (%s) is not a button", i, want.name)
			}
		}
		if v.str("panel") != "" || v.Evals["copy"] != false {
			t.Errorf("a list of matches selected someone: panel %q, asking for a copy: %v", v.str("panel"), v.Evals["copy"])
		}
		if v.Evals["fits"] != true {
			t.Errorf("the list makes the page scroll sideways at %d px", width)
		}
		if v.str("listGone") != "" {
			t.Errorf("the list stayed on screen after one was chosen: %q", v.str("listGone"))
		}
		// Choosing gives what scanning the card gives.
		if chosen, scanned := v.str("chosenPanel"), rep.Visits[0].str("scanPanel"); chosen == "" || chosen != scanned {
			t.Errorf("choosing Ann Lee selected %q; scanning her card selected %q", chosen, scanned)
		}
		if !strings.Contains(v.str("chosenPanel"), "Ann Lee") || !strings.Contains(v.str("chosenPanel"), sh.card["alee"]) {
			t.Errorf("the selected patron is %q", v.str("chosenPanel"))
		}
		if n := openLoansOfPatron(t, openRO(t, sh.s.DataDir), sh.ids["alee"]); n != before+1 {
			t.Errorf("Ann Lee has %d open loans, want %d: the checkout after choosing her did not lend", n, before+1)
		}
		if len(rep.Console) > 0 {
			t.Errorf("the browser complained: %v", rep.Console)
		}
	})
}

// "Search runs on Enter": typing shows no list and asks the server nothing; Enter, and the
// Find button, do.
func TestBrowserNoListWhileTypingOnlyOnEnterOrFind(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		typing := sh.at(t, "deskonly", width, nil,
			at(checkout, typeOnly("ann"), sleepMs(1200), eval("head", matchHead), eval("rows", matchRows), eval("busy", `document.body.innerText.includes("Searching")`)))
		if h := typing.Visits[0].str("head"); h != "" || len(typing.Visits[0].rows(t, "rows")) != 0 {
			t.Errorf("a list of matches is on screen before Enter or Find: %q", typing.Visits[0].Text)
		}
		if c := findCalls(typing); len(c) != 0 || typing.Visits[0].Evals["busy"] == true {
			t.Errorf("typing alone searched: %v", c)
		}

		enter := sh.at(t, "deskonly", width, nil, at(checkout, search("ann"), waitText("Matches for")))
		if c := findCalls(enter); len(c) != 1 || !strings.Contains(c[0], `"q":"ann"`) {
			t.Errorf("Enter made these find calls: %v, want one for ann", c)
		}

		find := sh.at(t, "deskonly", width, nil, at(checkout, typeOnly("ann"), clickText("Find"), waitText("Matches for")))
		find.Visits[0].must(t, "Matches for")
		if c := findCalls(find); len(c) != 1 {
			t.Errorf("the Find button made these find calls: %v, want one", c)
		}
	})
}

// "Find by username".
func TestBrowserFindByUsername(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, nil, at(checkout, search("alee"), waitText("Matches for"), eval("rows", matchRows)))
		rows := rep.Visits[0].rows(t, "rows")
		if len(rows) != 1 || !strings.Contains(rows[0].Text, "Ann Lee") || !strings.Contains(rows[0].Text, "alee") {
			t.Errorf("searching the username alee gave %+v, want Ann Lee", rows)
		}
	})
}

// "One match is still chosen deliberately".
func TestBrowserOneMatchIsListedNotSelectedAndNothingCanBeCheckedOutUntilChosen(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, nil,
			at(checkout, search("lee"), waitText("Matches for"), sleepMs(800), eval("rows", matchRows), eval("panel", patronPanel), eval("copy", copyField),
				eval("inputs", `document.querySelectorAll("input.input").length`), chooseMatch("Ann Lee"), waitText("Copy barcode"), eval("after", patronPanel)))
		v := rep.Visits[0]
		if rows := v.rows(t, "rows"); len(rows) != 1 || !strings.Contains(rows[0].Text, "Ann Lee") {
			t.Fatalf("searching lee gave %+v, want only Ann Lee", rows)
		}
		if v.str("panel") != "" || v.Evals["copy"] != false {
			t.Errorf("the one match was selected without being chosen: panel %q, copy field %v", v.str("panel"), v.Evals["copy"])
		}
		if v.Evals["inputs"] != float64(1) {
			t.Errorf("%v fields on screen before choosing, want only the patron field", v.Evals["inputs"])
		}
		if !strings.Contains(v.str("after"), "Ann Lee") {
			t.Errorf("choosing the one match did not select it: %q", v.str("after"))
		}
	})
}

// "Same name": two Sam Readers told apart by username and card; each is the one chosen.
func TestBrowserSameNameIsToldApartByUsernameAndCard(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, nil,
			at(checkout, search("sam reader"), waitText("Matches for"), eval("rows", matchRows), chooseMatch("sam.r2"), waitText("Copy barcode"), eval("panel", patronPanel)))
		v := rep.Visits[0]
		rows := v.rows(t, "rows")
		if len(rows) != 2 {
			t.Fatalf("%d matches for sam reader, want 2: %+v", len(rows), rows)
		}
		for i, u := range []string{"sam.r1", "sam.r2"} {
			if !strings.Contains(rows[i].Text, u) || !strings.Contains(rows[i].Text, sh.card[u]) || !rows[i].Button {
				t.Errorf("row %d is %q (button %v), want username %s and card %s", i, rows[i].Text, rows[i].Button, u, sh.card[u])
			}
		}
		if sh.card["sam.r1"] == sh.card["sam.r2"] {
			t.Fatal("the two patrons share a card")
		}
		if p := v.str("panel"); !strings.Contains(p, sh.card["sam.r2"]) || strings.Contains(p, sh.card["sam.r1"]) {
			t.Errorf("choosing the second Sam Reader selected %q, want card %s", p, sh.card["sam.r2"])
		}
	})
}

// "No match".
func TestBrowserNoMatchSaysSoAndSelectsNobody(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, nil,
			at(checkout, search("zzz"), waitText("No patron matches ‘zzz’."), eval("rows", matchRows), eval("panel", patronPanel), eval("copy", copyField)))
		v := rep.Visits[0]
		v.must(t, "No patron matches ‘zzz’.")
		if len(v.rows(t, "rows")) != 0 || v.str("panel") != "" || v.Evals["copy"] != false {
			t.Errorf("no match, yet something was listed or selected: %q", v.Text)
		}
	})
}

// "Too many matches": 60 match, 50 are shown, and the librarian is told to type more.
func TestBrowserSixtyMatchesShowFiftyAndSayThereAreMore(t *testing.T) {
	sh := newShop(t)
	for i := 1; i <= 60; i++ {
		sh.add("patron", fmt.Sprintf("bulk%02d", i), fmt.Sprintf("Bulk %02d", i))
	}
	const more = "More than 50 patrons match. Type more of the name."
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, nil,
			at(checkout, search("bulk"), waitText(more), eval("rows", matchRows), eval("fits", noSideways),
				search("bulk 60"), waitText("Matches for ‘bulk 60’"), eval("rows2", matchRows), eval("text2", `document.body.innerText`)))
		v := rep.Visits[0]
		rows := v.rows(t, "rows")
		if len(rows) != 50 || !strings.Contains(rows[0].Text, "Bulk 01") || !strings.Contains(rows[49].Text, "Bulk 50") {
			t.Errorf("%d matches shown, want Bulk 01 to Bulk 50", len(rows))
		}
		if strings.Contains(v.Text, "Bulk 51") {
			t.Errorf("a 51st match is shown")
		}
		if v.Evals["fits"] != true {
			t.Errorf("the long list makes the page scroll sideways at %d px", width)
		}
		if rows2 := v.rows(t, "rows2"); len(rows2) != 1 || !strings.Contains(rows2[0].Text, "Bulk 60") || strings.Contains(v.str("text2"), "More than 50") {
			t.Errorf("typing more of the name left %+v, and the message %v", rows2, strings.Contains(v.str("text2"), "More than 50"))
		}
	})
	// Exactly 50 is not "more".
	rep := sh.at(t, "deskonly", 1280, nil, at(checkout, search("bulk 1"), waitText("Matches for"), eval("rows", matchRows), eval("t", `document.body.innerText`)))
	if n := len(rep.Visits[0].rows(t, "rows")); n != 10 || strings.Contains(rep.Visits[0].str("t"), "More than 50") { // Bulk 10 to Bulk 19
		t.Errorf("bulk 1 gave %d matches (want 10) and the message %v", n, strings.Contains(rep.Visits[0].str("t"), "More than 50"))
	}
}

// "A scanned or typed card still selects at once": no list in between.
func TestBrowserTypedOrScannedCardSelectsAtOnce(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		for name, enter := range map[string]func(string) map[string]any{
			"scanner": func(c string) map[string]any { return wedge(scanInput, c) },
			"typed":   func(c string) map[string]any { return typed(scanInput, c) },
		} {
			rep := sh.at(t, "deskonly", width, nil,
				at(checkout, enter(sh.card["annko"]), waitText("Copy barcode"), eval("panel", patronPanel), eval("head", matchHead)))
			v := rep.Visits[0]
			if p := v.str("panel"); !strings.Contains(p, "Anna Ko") || !strings.Contains(p, sh.card["annko"]) || v.str("head") != "" {
				t.Errorf("%s: panel %q, list %q: a card should select Anna Ko with no list", name, p, v.str("head"))
			}
		}
	})
}

// "Staff account": another librarian's search lists a librarian, who can be chosen.
func TestBrowserStaffAccountIsListedAndChoosable(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, nil,
			at(checkout, search("marla"), waitText("Matches for"), eval("rows", matchRows), chooseMatch("Marla Staff"), waitText("Copy barcode"), eval("panel", patronPanel)))
		v := rep.Visits[0]
		if rows := v.rows(t, "rows"); len(rows) != 1 || !rows[0].Button || !strings.Contains(rows[0].Text, "Marla Staff") {
			t.Errorf("the librarian's account is listed as %+v", rows)
		}
		if p := v.str("panel"); !strings.Contains(p, "Marla Staff") || !strings.Contains(p, sh.card["marla"]) {
			t.Errorf("choosing the librarian selected %q", p)
		}
	})
}

// "Deactivated patron" and "Patron with no card": listed, with the reason, and nothing to press.
func TestBrowserDeactivatedAndCardlessMatchesAreShownButCannotBeChosen(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		for _, c := range []struct{ q, name, why string }{{"dee gone", "Dee Gone", "Deactivated"}, {"nora", "Nora Nocard", "No library card"}} {
			rep := sh.at(t, "deskonly", width, nil,
				at(checkout, search(c.q), waitText("Matches for"), eval("rows", matchRows),
					eval("poke", `(() => { document.querySelector("section.matches li > *").click(); return true; })()`), sleepMs(800),
					eval("panel", patronPanel), eval("copy", copyField)))
			v := rep.Visits[0]
			rows := v.rows(t, "rows")
			if len(rows) != 1 || !strings.Contains(rows[0].Text, c.name) || !strings.Contains(rows[0].Text, c.why) {
				t.Errorf("%s: listed as %+v, want the name and %q", c.q, rows, c.why)
				continue
			}
			if rows[0].Button || rows[0].Controls != 0 {
				t.Errorf("%s: the row has %d controls (button %v): it can be pressed", c.q, rows[0].Controls, rows[0].Button)
			}
			if v.str("panel") != "" || v.Evals["copy"] != false {
				t.Errorf("%s: pressing the row selected %q", c.q, v.str("panel"))
			}
			if c.why == "No library card" && strings.Contains(rows[0].Text, "undefined") {
				t.Errorf("a card number is shown for someone with none: %q", rows[0].Text)
			}
		}
	})
}

// "Permission to check out only": the same list as any librarian, and as the administrator.
func TestBrowserCircDeskOnlyLibrarianSeesWhatAnyLibrarianSees(t *testing.T) {
	sh := newShop(t)
	sh.forWidths(t, func(t *testing.T, width int) {
		var seen = map[string][]matchRow{}
		for _, user := range []string{"deskonly", "lib", "root"} {
			rep := sh.at(t, user, width, nil, at(checkout, search("ann"), waitText("Matches for"), eval("rows", matchRows)))
			seen[user] = rep.Visits[0].rows(t, "rows")
		}
		if len(seen["deskonly"]) != 2 {
			t.Fatalf("circ.desk only saw %+v", seen["deskonly"])
		}
		for _, u := range []string{"lib", "root"} {
			if !reflect.DeepEqual(seen["deskonly"], seen[u]) {
				t.Errorf("circ.desk only saw %+v, %s saw %+v", seen["deskonly"], u, seen[u])
			}
		}
	})
}

// ---- hard cases ---------------------------------------------------------------------

// The server unreachable after submit: an error to act on, and nobody selected.
func TestBrowserSearchWithTheServerGoneSaysSoAndSelectsNobody(t *testing.T) {
	sh := newShop(t)
	gone := []map[string]any{{"url": "/api/desk/find", "fail": true}}
	sh.forWidths(t, func(t *testing.T, width int) {
		for _, q := range []string{"ann", sh.card["annko"]} {
			rep := sh.at(t, "deskonly", width, gone,
				at(checkout, search(q), waitText("The search couldn't be done"), eval("panel", patronPanel), eval("copy", copyField), eval("rows", matchRows), eval("busy", `document.body.innerText.includes("Searching")`)))
			v := rep.Visits[0]
			for _, say := range []string{"Couldn't search", "The search couldn't be done: can't reach the library. Try again."} {
				if !strings.Contains(v.Text, say) {
					t.Errorf("q=%q: the screen does not say %q:\n%s", q, say, v.Text)
				}
			}
			if v.str("panel") != "" || v.Evals["copy"] != false || len(v.rows(t, "rows")) != 0 {
				t.Errorf("q=%q: the search failed, yet %q is selected or a list is shown", q, v.str("panel"))
			}
			if v.Evals["busy"] == true {
				t.Errorf("q=%q: the screen still says Searching… after the failure", q)
			}
		}
	})
}

// Two searches in quick succession: the earlier one's late answer never replaces the later.
// The first answer is held 1.5 s and the second 3 s, so for a while only the first has been
// answered: that is when a broken screen would show its list.
func TestBrowserALateAnswerNeverReplacesALaterSearch(t *testing.T) {
	sh := newShopNow(t)
	slow := []map[string]any{
		{"url": "/api/desk/find", "body": `"q":"ann"`, "delayMs": 1500},
		{"url": "/api/desk/find", "body": `"q":"lee"`, "delayMs": 3000},
	}
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, slow,
			at(checkout, search("ann"), search("lee"), sleepMs(2300), eval("mid", matchHead), eval("midText", `document.body.innerText`),
				waitText("Matches for ‘lee’"), sleepMs(1500), eval("end", matchHead), eval("rows", matchRows)))
		v := rep.Visits[0]
		if h := v.str("mid"); h == "Matches for ‘ann’" {
			t.Errorf("the first search's answer replaced the screen while the later search was still waiting:\n%s", v.str("midText"))
		}
		if h := v.str("end"); h != "Matches for ‘lee’" {
			t.Errorf("the final list is %q, want the later search's", h)
		}
		if rows := v.rows(t, "rows"); len(rows) != 1 || !strings.Contains(rows[0].Text, "Ann Lee") {
			t.Errorf("the final matches are %+v", rows)
		}
		if c := findCalls(rep); len(c) != 2 {
			t.Errorf("%d find calls, want 2: %v", len(c), c)
		}
	})
}

// Names with markup are text on this screen: the list and the selected patron.
func TestBrowserMarkupInANameIsShownAsText(t *testing.T) {
	sh := newShop(t)
	const hostile = `<img src=x onerror="window.__pwned=1"><b>Bold</b>`
	sh.add("patron", "markup", hostile)
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "deskonly", width, nil,
			at(checkout, search("markup"), waitText("Matches for"), eval("rows", matchRows), eval("pwned", `window.__pwned ?? null`),
				eval("injected", `document.querySelectorAll("section.matches img, section.matches b:not(.m b)").length + document.querySelectorAll("section.matches li img").length`),
				chooseMatch("markup"), waitText("Copy barcode"), eval("panel", patronPanel), eval("pwned2", `window.__pwned ?? null`),
				eval("img", `document.querySelectorAll("main img, .patron img").length`)))
		v := rep.Visits[0]
		rows := v.rows(t, "rows")
		if len(rows) != 1 || !strings.Contains(rows[0].Text, hostile) {
			t.Fatalf("the name is shown as %+v, want the literal text %q", rows, hostile)
		}
		if !strings.Contains(v.str("panel"), hostile) {
			t.Errorf("the selected patron's name is shown as %q", v.str("panel"))
		}
		if v.Evals["pwned"] != nil || v.Evals["pwned2"] != nil || v.Evals["injected"] != float64(0) || v.Evals["img"] != float64(0) {
			t.Errorf("markup in a name ran or made elements: pwned %v/%v, injected %v, images %v", v.Evals["pwned"], v.Evals["pwned2"], v.Evals["injected"], v.Evals["img"])
		}
	})
}

// A patron's own self-checkout screen has no search and never calls the find route.
func TestBrowserSelfCheckoutAsAPatronHasNoNameSearch(t *testing.T) {
	sh := newShop(t)
	sh.root.want(200, "PUT", "/api/admin/settings", map[string]any{"self_checkout": true})
	sh.forWidths(t, func(t *testing.T, width int) {
		rep := sh.at(t, "alee", width, nil,
			at("/checkout", eval("labels", `[...document.querySelectorAll("label")].map((l) => l.textContent.trim())`),
				eval("buttons", `[...document.querySelectorAll("button")].map((b) => b.textContent.trim())`),
				search("ann"), sleepMs(1500), eval("text", `document.body.innerText`)))
		v := rep.Visits[0]
		if v.URL != "/checkout" {
			t.Fatalf("the patron ended at %s, not the self-checkout screen", v.URL)
		}
		for _, l := range strs2(v.Evals["labels"]) {
			if strings.Contains(strings.ToLower(l), "patron") || strings.Contains(strings.ToLower(l), "name") {
				t.Errorf("a patron's checkout has a field labelled %q", l)
			}
		}
		if b := strs2(v.Evals["buttons"]); contains(b, "Find") {
			t.Errorf("a patron's checkout has a Find button: %v", b)
		}
		if strings.Contains(v.str("text"), "Matches for") || strings.Contains(v.str("text"), "No patron matches") {
			t.Errorf("a patron's checkout shows a list of patrons:\n%s", v.str("text"))
		}
		if c := findCalls(rep); len(c) != 0 {
			t.Errorf("a patron's checkout called the find route: %v", c)
		}
		for _, c := range rep.Calls {
			if strings.Contains(c, "/api/desk/") || strings.Contains(c, "/api/patrons") {
				t.Errorf("a patron's checkout called a staff route: %s", c)
			}
		}
	})
}

func strs2(v any) []string {
	if v == nil {
		return nil
	}
	return strs(v)
}

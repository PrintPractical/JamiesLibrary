package e2e

// ADMIN-1 in a real browser: every preset colour scheme, once chosen on the
// settings screen, shows text that meets WCAG AA (4.5:1) in the colours the
// browser really draws with, for every user; and an accent that fails either
// contrast check is refused on the screen with its measured ratio, leaving the
// scheme as it was. The contrast arithmetic is WCAG 2's, written out here.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var reRGB = regexp.MustCompile(`^rgba?\((\d+), (\d+), (\d+)`)

// luminance is the WCAG relative luminance of a colour written #rrggbb or rgb(r, g, b).
func luminance(t *testing.T, c string) float64 {
	t.Helper()
	c = strings.TrimSpace(c)
	var r, g, b float64
	if m := reRGB.FindStringSubmatch(c); m != nil {
		r, _ = strconv.ParseFloat(m[1], 64)
		g, _ = strconv.ParseFloat(m[2], 64)
		b, _ = strconv.ParseFloat(m[3], 64)
	} else if len(c) == 7 && c[0] == '#' {
		v, err := strconv.ParseUint(c[1:], 16, 32)
		if err != nil {
			t.Fatalf("not a colour: %q", c)
		}
		r, g, b = float64(v>>16&255), float64(v>>8&255), float64(v&255)
	} else {
		t.Fatalf("not a colour: %q", c)
	}
	lin := func(x float64) float64 {
		x /= 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func ratio(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// colours reads, in the page, the colours of the scheme in force and the colours
// of what is really drawn: a primary button, the app bar or side bar, the page.
const colours = `(() => {
  const root = getComputedStyle(document.documentElement);
  const v = (n) => root.getPropertyValue("--" + n).trim();
  const px = (sel) => { const e = document.querySelector(sel); if (!e) return null; const s = getComputedStyle(e); return { color: s.color, bg: s.backgroundColor }; };
  return { vars: Object.fromEntries(["paper", "card", "ink", "ink-2", "primary", "on-primary", "wood", "on-wood", "on-wood-2"].map((n) => [n, v(n)])),
    button: px(".btn-primary:not(:disabled)"), bar: px(".appbar") ?? px(".side"), page: px("body") };
})()`

type pageColours struct {
	Vars              map[string]string
	Button, Bar, Page *struct{ Color, Bg string }
}

func readColours(t *testing.T, v any) pageColours {
	t.Helper()
	m := v.(map[string]any)
	pc := pageColours{Vars: map[string]string{}}
	for k, x := range m["vars"].(map[string]any) {
		pc.Vars[k] = x.(string)
	}
	get := func(k string) *struct{ Color, Bg string } {
		o, ok := m[k].(map[string]any)
		if !ok {
			return nil
		}
		return &struct{ Color, Bg string }{o["color"].(string), o["bg"].(string)}
	}
	pc.Button, pc.Bar, pc.Page = get("button"), get("bar"), get("page")
	return pc
}

// readable fails the test for any text colour in use that is under 4.5:1 on its background.
func readable(t *testing.T, where string, pc pageColours) {
	t.Helper()
	v := pc.Vars
	for _, c := range []struct{ name, fg, bg string }{
		{"button text on the accent", v["on-primary"], v["primary"]},
		{"accent as text on the page", v["primary"], v["paper"]},
		{"accent as text on a card", v["primary"], v["card"]},
		{"body text on the page", v["ink"], v["paper"]},
		{"secondary text on the page", v["ink-2"], v["paper"]},
		{"navigation text on the bar", v["on-wood"], v["wood"]},
		{"secondary navigation text on the bar", v["on-wood-2"], v["wood"]},
	} {
		if r := ratio(t, c.fg, c.bg); r < 4.5 {
			t.Errorf("%s: %s is %s on %s: %.2f:1, under 4.5:1", where, c.name, c.fg, c.bg, r)
		}
	}
	// What the browser really paints must agree with the variables, and be readable.
	for name, p := range map[string]*struct{ Color, Bg string }{"primary button": pc.Button, "bar": pc.Bar, "page": pc.Page} {
		if p == nil {
			t.Errorf("%s: no %s on the page to measure", where, name)
			continue
		}
		if r := ratio(t, p.Color, p.Bg); r < 4.5 {
			t.Errorf("%s: the drawn %s text %s on %s is %.2f:1, under 4.5:1", where, name, p.Color, p.Bg, r)
		}
	}
	if pc.Button != nil && math.Abs(luminance(t, pc.Button.Bg)-luminance(t, v["primary"])) > 0.001 {
		t.Errorf("%s: the primary button is drawn %s, not the scheme's accent %s", where, pc.Button.Bg, v["primary"])
	}
}

const pick = `((name) => { const b = [...document.querySelectorAll('[role=radio]')].find((e) => e.textContent.trim().startsWith(name)); if (!b) throw new Error("no scheme " + name); b.click(); return true; })`

func save() map[string]any { return map[string]any{"clickText": "Save name and look"} }

func waitVar(name, hex string) map[string]any {
	return map[string]any{"waitFor": fmt.Sprintf(`getComputedStyle(document.documentElement).getPropertyValue("--%s").trim().toUpperCase() === %q`, name, strings.ToUpper(hex)), "seconds": 10}
}

func TestEveryPresetSchemeIsReadableWhereItIsUsed(t *testing.T) {
	w := newWorld(t)
	settings := w.root.want(200, "GET", "/api/admin/settings", nil)
	type preset struct{ ID, Name, Primary string }
	var presets []preset
	for _, s := range settings.JSON["schemes"].([]any) {
		m := s.(map[string]any)
		presets = append(presets, preset{m["id"].(string), m["name"].(string), m["colours"].(map[string]any)["--primary"].(string)})
	}
	if len(presets) < 4 {
		t.Fatalf("%d preset schemes; the requirement and the setup mockup have 4", len(presets))
	}
	if presets[0].ID != "hearth" {
		t.Errorf("the default scheme is first: %q, want hearth", presets[0].ID)
	}

	for _, width := range widths {
		// Fresh installation: the warm default, drawn.
		def := browseAt(t, w.s.URL, "alice-a", browseOpts{Width: width, Height: 800}, []browserStep{{Path: "/", Actions: []map[string]any{{"eval": colours}}}})
		pc := readColours(t, def.Visits[0].Evals[0])
		if !strings.EqualFold(pc.Vars["primary"], presets[0].Primary) {
			t.Errorf("a new installation draws with %s, not Hearth's %s", pc.Vars["primary"], presets[0].Primary)
		}
		readable(t, fmt.Sprintf("default scheme at %d px", width), pc)

		actions := []map[string]any{{"waitFor": `!!document.querySelector("[role=radio]")`}}
		for _, p := range presets {
			actions = append(actions,
				map[string]any{"eval": fmt.Sprintf("(%s)(%q)", pick, p.Name)},
				save(), waitVar("primary", p.Primary),
				map[string]any{"waitFor": `[...document.querySelectorAll("button")].some((b) => b.textContent.trim() === "Save name and look" && !b.disabled)`, "seconds": 10},
				map[string]any{"eval": colours})
		}
		rep := browseAt(t, w.s.URL, "root", browseOpts{Width: width, Height: 800}, []browserStep{{Path: "/admin/settings", Actions: actions}})
		v := rep.Visits[0]
		for i, p := range presets {
			if !v.Notes[fmt.Sprintf("getComputedStyle(document.documentElement).getPropertyValue(\"--primary\").trim().toUpperCase() === %q", strings.ToUpper(p.Primary))].Found && i > 0 {
				t.Errorf("%s at %d px: the screen never drew with it", p.Name, width)
			}
			// the evals: [true, colours] per preset
			pc := readColours(t, v.Evals[2*i+1])
			readable(t, fmt.Sprintf("%s at %d px (administrator)", p.Name, width), pc)
			if !strings.EqualFold(pc.Vars["primary"], p.Primary) {
				t.Errorf("%s at %d px: drawn with %s, want %s", p.Name, width, pc.Vars["primary"], p.Primary)
			}
		}
		// Anyone opening the app afterwards gets the last one chosen (the scheme is the library's, not the administrator's).
		last := presets[len(presets)-1]
		for _, user := range []string{"alice-a", ""} {
			rep := browseAt(t, w.s.URL, user, browseOpts{Width: width, Height: 800}, []browserStep{{Path: "/signin", Actions: []map[string]any{{"eval": colours}}}})
			pc := readColours(t, rep.Visits[0].Evals[0])
			if !strings.EqualFold(pc.Vars["primary"], last.Primary) {
				t.Errorf("%q at %d px sees %s, want %s's %s", user, width, pc.Vars["primary"], last.Name, last.Primary)
			}
		}
		// back to the default for the next width
		w.root.want(200, "PUT", "/api/admin/branding", map[string]any{"name": "Jamie's Library", "scheme": "hearth", "accent": ""})
	}
}

func TestAnAccentThatCannotBeReadIsRefusedOnTheScreen(t *testing.T) {
	w := newWorld(t)
	w.root.want(200, "PUT", "/api/admin/branding", map[string]any{"name": "Jamie's Library", "scheme": "inkwell", "accent": ""})
	before := w.root.want(200, "GET", "/api/library", nil)

	typeAccent := func(s string) map[string]any {
		return map[string]any{"type": map[string]string{"selector": "#accent", "text": s}}
	}
	const hardToRead = "Text would be hard to read on this colour"
	const saveDisabled = `[...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "Save name and look").disabled`
	const alert = `(document.querySelector("#accent-msg [role=alert]")?.innerText ?? "")`
	for _, width := range widths {
		rep := browseAt(t, w.s.URL, "root", browseOpts{Width: width, Height: 900}, []browserStep{
			// A light yellow: white button labels cannot be read on it, nor it on the page.
			{Path: "/admin/settings", Layout: true, Actions: []map[string]any{typeAccent("#F5D142"), {"waitText": hardToRead}, {"eval": alert}, {"eval": saveDisabled}, {"eval": colours}}},
			// A grey that button text can be read on but that is too faint as text on the page.
			{Path: "/admin/settings", Actions: []map[string]any{typeAccent("#707070"), {"waitText": hardToRead}, {"eval": alert}, {"eval": saveDisabled}}},
			// Its suggested shade is accepted, and passes.
			{Actions: []map[string]any{{"clickText": "Use #6D6D6D"}, {"sleepMs": 600}, {"eval": saveDisabled}, {"eval": `document.querySelector("#accent").value`}}},
		})
		yellow, grey, shade := rep.Visits[0], rep.Visits[1], rep.Visits[2]
		where := fmt.Sprintf("at %d px", width)

		if !yellow.Notes[hardToRead].Found || !grey.Notes[hardToRead].Found {
			t.Fatalf("%s: the screen did not say the accent was hard to read\n%.500s", where, yellow.Text)
		}
		ya, _ := yellow.Evals[0].(string)
		for _, want := range []string{"Button text on #F5D142", "1.4 : 1", "used as text on the page", "1.3 : 1", "at least 4.5 : 1", "Your scheme hasn't changed"} {
			if !strings.Contains(ya, want) {
				t.Errorf("%s: the refusal of the light yellow does not say %q:\n%s", where, want, ya)
			}
		}
		if yellow.Evals[1] != true {
			t.Errorf("%s: Save is still on offer for the light yellow", where)
		}
		// The ratios shown are the true ones, by our own arithmetic (Inkwell: its on-primary and paper).
		pcYellow := readColours(t, yellow.Evals[2])
		if r := ratio(t, "#F5D142", pcYellow.Vars["on-primary"]); r > 4.5 {
			t.Errorf("%s: test fixture is wrong: the yellow reads at %.2f:1", where, r)
		}
		ga, _ := grey.Evals[0].(string)
		if !strings.Contains(ga, "used as text on the page has a contrast of 4.4 : 1") || strings.Contains(ga, "Button text on") {
			t.Errorf("%s: the grey should fail only as text, at 4.4 : 1:\n%s", where, ga)
		}
		if r := ratio(t, "#707070", "#F3F1EC"); r >= 4.5 || r < 4.3 {
			t.Errorf("%s: by WCAG arithmetic #707070 on Inkwell's paper is %.2f:1; the fixture needs 4.3 to 4.5", where, r)
		}
		if r := ratio(t, "#707070", "#F8FAFC"); r < 4.5 {
			t.Errorf("%s: by WCAG arithmetic button text on #707070 is %.2f:1; the fixture needs it to pass", where, r)
		}
		if grey.Evals[1] != true {
			t.Errorf("%s: Save is still on offer for the grey", where)
		}
		if shade.Evals[0] != false {
			t.Errorf("%s: the suggested shade was not accepted", where)
		}
		if r := ratio(t, "#6D6D6D", "#F3F1EC"); r < 4.5 {
			t.Errorf("%s: the suggested shade reads at %.2f:1, under 4.5:1", where, r)
		}
		// Nothing was saved: the library is as it was.
		after := w.root.want(200, "GET", "/api/library", nil)
		if after.Raw != before.Raw {
			t.Errorf("%s: the library's branding changed after refusals:\nbefore %s\nafter  %s", where, before.Raw, after.Raw)
		}
	}
	// And the server alone says no, whatever the screen does.
	for _, accent := range []string{"#F5D142", "#707070"} {
		if r := w.root.do("PUT", "/api/admin/branding", map[string]any{"name": "Jamie's Library", "scheme": "inkwell", "accent": accent}); r.Status < 400 || r.Status > 499 {
			t.Errorf("saving %s: status %d, want a refusal", accent, r.Status)
		}
	}
	if after := w.root.want(200, "GET", "/api/library", nil); after.Raw != before.Raw {
		t.Errorf("the library's branding changed after the server refused: %s", after.Raw)
	}
}

func TestAnAcceptedAccentIsReadableOnScreen(t *testing.T) {
	w := newWorld(t)
	for _, width := range widths {
		rep := browseAt(t, w.s.URL, "root", browseOpts{Width: width, Height: 900}, []browserStep{{Path: "/admin/settings", Actions: []map[string]any{
			{"type": map[string]string{"selector": "#accent", "text": "#6B2D1F"}}, {"sleepMs": 600}, save(), waitVar("primary", "#6B2D1F"), {"eval": colours},
		}}})
		readable(t, fmt.Sprintf("accent #6B2D1F at %d px", width), readColours(t, rep.Visits[0].Evals[0]))
		w.root.want(200, "PUT", "/api/admin/branding", map[string]any{"name": "Jamie's Library", "scheme": "hearth", "accent": ""})
	}
}

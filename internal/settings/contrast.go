package settings

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// MinContrast is WCAG AA for normal text.
const MinContrast = 4.5

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// rgb is a colour with each channel in 0..255.
type rgb struct{ r, g, b uint8 }

func parseHex(s string) (rgb, bool) {
	if !hexColour.MatchString(s) {
		return rgb{}, false
	}
	n, _ := strconv.ParseUint(s[1:], 16, 32)
	return rgb{uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
}

func (c rgb) hex() string { return fmt.Sprintf("#%02X%02X%02X", c.r, c.g, c.b) }

func linear(v uint8) float64 {
	s := float64(v) / 255
	if s <= 0.03928 {
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
}

// luminance is the WCAG 2.x relative luminance.
func (c rgb) luminance() float64 {
	return 0.2126*linear(c.r) + 0.7152*linear(c.g) + 0.0722*linear(c.b)
}

// Contrast is the WCAG 2.x contrast ratio (1..21) of two #RRGGBB colours.
func Contrast(a, b string) (float64, error) {
	ca, ok := parseHex(a)
	if !ok {
		return 0, fmt.Errorf("%q is not a #RRGGBB colour", a)
	}
	cb, ok := parseHex(b)
	if !ok {
		return 0, fmt.Errorf("%q is not a #RRGGBB colour", b)
	}
	return ratio(ca, cb), nil
}

func ratio(a, b rgb) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func (c rgb) hsl() (h, s, l float64) {
	r, g, b := float64(c.r)/255, float64(c.g)/255, float64(c.b)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, l
}

func fromHSL(h, s, l float64) rgb {
	if s == 0 {
		v := uint8(math.Round(l * 255))
		return rgb{v, v, v}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	ch := func(t float64) uint8 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		var v float64
		switch {
		case t < 1.0/6:
			v = p + (q-p)*6*t
		case t < 0.5:
			v = q
		case t < 2.0/3:
			v = p + (q-p)*(2.0/3-t)*6
		default:
			v = p
		}
		return uint8(math.Round(v * 255))
	}
	return rgb{ch(h + 1.0/3), ch(h), ch(h - 1.0/3)}
}

// Check names one of the two contrast tests an accent must pass.
type Check string

const (
	// CheckOnAccent: the scheme's text colour shown on the accent (button labels).
	CheckOnAccent Check = "on_accent"
	// CheckAsText: the accent used as text or link colour on the scheme's background.
	CheckAsText Check = "as_text"
)

// AccentResult is the verdict on an accent colour for a scheme.
type AccentResult struct {
	OK bool
	// Failed lists the checks that did not reach MinContrast, in a fixed order.
	Failed []Check
	// OnAccentRatio and AsTextRatio are the two measured ratios.
	OnAccentRatio, AsTextRatio float64
	// Suggestion is a darker shade of the accent that passes both checks. It is
	// "" when the accent passes.
	Suggestion string
}

// CheckAccent tests accent (#RRGGBB) against the scheme: the accent against the
// scheme's on-accent text colour, and the accent as text against the scheme's
// background. Both must reach 4.5:1. On failure it says which did not, and
// suggests the accent darkened in steps of lightness until both pass.
func CheckAccent(scheme Scheme, accent string) (AccentResult, error) {
	ac, ok := parseHex(accent)
	if !ok {
		return AccentResult{}, fmt.Errorf("%q is not a #RRGGBB colour", accent)
	}
	on, _ := parseHex(scheme.OnPrimary)
	bg, _ := parseHex(scheme.Paper)
	res := measure(ac, on, bg)
	if res.OK {
		return res, nil
	}
	h, s, l := ac.hsl()
	for l > 0 {
		l = math.Max(0, l-0.01)
		cand := fromHSL(h, s, l)
		if r := measure(cand, on, bg); r.OK {
			res.Suggestion = cand.hex()
			return res, nil
		}
	}
	// Black is the end of the search; it passes against any light scheme.
	res.Suggestion = rgb{}.hex()
	return res, nil
}

func measure(ac, on, bg rgb) AccentResult {
	r := AccentResult{OnAccentRatio: ratio(ac, on), AsTextRatio: ratio(ac, bg)}
	if r.OnAccentRatio < MinContrast {
		r.Failed = append(r.Failed, CheckOnAccent)
	}
	if r.AsTextRatio < MinContrast {
		r.Failed = append(r.Failed, CheckAsText)
	}
	r.OK = len(r.Failed) == 0
	return r
}

// normaliseHex upper-cases a valid #RRGGBB colour.
func normaliseHex(s string) (string, bool) {
	if !hexColour.MatchString(s) {
		return "", false
	}
	return "#" + strings.ToUpper(s[1:]), true
}

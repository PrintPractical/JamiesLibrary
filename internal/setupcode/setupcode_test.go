package setupcode

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintWritesTheLineAndPatternFindsIt(t *testing.T) {
	var b bytes.Buffer
	if err := Print(&b, "ABCD-1234-WXYZ-0987"); err != nil {
		t.Fatal(err)
	}
	if b.String() != "SETUP CODE: ABCD-1234-WXYZ-0987\n" {
		t.Fatalf("got %q", b.String())
	}
	m := Pattern.FindStringSubmatch("noise\n" + b.String() + "more\n")
	if m == nil || m[1] != "ABCD-1234-WXYZ-0987" {
		t.Fatalf("pattern found %v", m)
	}
}

func TestPrintRefusesABadCode(t *testing.T) {
	for _, code := range []string{"", "abcd-1234-wxyz-0987", "ABCD-1234-WXYZ", "ABCD-1234-WXYZ-0987-AAAA", "ABCD1234WXYZ0987", "ABCD-1234-WXYZ-0987\nX"} {
		var b bytes.Buffer
		if err := Print(&b, code); err == nil || b.Len() != 0 {
			t.Errorf("code %q: err %v, wrote %q", code, err, b.String())
		}
	}
}

func TestNewDrawsValidDistinctCodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, err := New()
		if err != nil || !Valid(c) {
			t.Fatalf("code %q: %v", c, err)
		}
		if strings.ContainsAny(c, "ILOU") {
			t.Fatalf("code %q has a character Crockford leaves out", c)
		}
		if seen[c] {
			t.Fatalf("code %q drawn twice", c)
		}
		seen[c] = true
	}
}

func TestNormalizeIgnoresCaseDashesSpacesAndReadsLookalikes(t *testing.T) {
	for _, typed := range []string{"ab01-cdef-ghjk-mnpq", "AB01 CDEF GHJK MNPQ", "abolcdefghjkmnpq", "  ab0i-cdef-ghjk-mnpq"} {
		if got := Normalize(typed); got != "AB01CDEFGHJKMNPQ" {
			t.Errorf("Normalize(%q) = %q", typed, got)
		}
	}
}

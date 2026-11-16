package invitations

import (
	"regexp"
	"testing"
)

func TestNewCodeShapeAndDistinct(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewCode()
		if err != nil || !shape.MatchString(c) || seen[c] {
			t.Fatalf("code %q err %v", c, err)
		}
		seen[c] = true
		if n, ok := Normalise(c); !ok || n != c {
			t.Fatalf("a code doesn't normalise to itself: %q -> %q", c, n)
		}
	}
}

func TestNormalise(t *testing.T) {
	for in, want := range map[string]string{
		"abcd-efgh-jkmn-pqrs":   "ABCD-EFGH-JKMN-PQRS",
		"abcdefghjkmnpqrs":      "ABCD-EFGH-JKMN-PQRS",
		" abcd efgh jkmn pqrs ": "ABCD-EFGH-JKMN-PQRS",
		"OIL0-0000-0000-0000":   "0110-0000-0000-0000",
		"oil0000000000000":      "0110-0000-0000-0000",
	} {
		if got, ok := Normalise(in); !ok || got != want {
			t.Errorf("Normalise(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "ABCD", "ABCD-EFGH-JKMN-PQR", "ABCD-EFGH-JKMN-PQRSS", "ABCD-EFGH-JKMN-PQRU", "ABCD-EFGH-JKMN-PQéS", "ABCD-EFGH-JKMN-PQR\x00"} {
		if got, ok := Normalise(bad); ok {
			t.Errorf("Normalise(%q) = %q, accepted", bad, got)
		}
	}
}

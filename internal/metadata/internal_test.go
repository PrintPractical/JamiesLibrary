package metadata

import (
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCheckAllowsOnlyHttpsOnTheListedHosts(t *testing.T) {
	c := newClient("test", nil)
	for u, want := range map[string]bool{
		"https://openlibrary.org/api/books":             true,
		"https://covers.openlibrary.org/b/isbn/1-L.jpg": true,
		"https://archive.org/download/x":                true,
		"https://ia800000.us.archive.org/x.jpg":         true,
		"https://OpenLibrary.org./x":                    true,
		"https://openlibrary.org:443/x":                 true,
		"http://openlibrary.org/x":                      false,
		"ftp://openlibrary.org/x":                       false,
		"https://openlibrary.org:8443/x":                false,
		"https://evilarchive.org/x":                     false,
		"https://openlibrary.org.evil.example/x":        false,
		"https://evil.example/openlibrary.org":          false,
		"https://user:pw@openlibrary.org/x":             false,
		"https://example.com/":                          false,
		"https://127.0.0.1/":                            false,
		"https://www.openlibrary.org/":                  false,
		"https://notarchive.org/":                       false,
		"https://archive.org.example.com/":              false,
	} {
		p, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.check(p) == nil; got != want {
			t.Errorf("check(%q) allowed = %v, want %v", u, got, want)
		}
	}
}

func TestTestOriginIsAllowedOnlyWhenGiven(t *testing.T) {
	origin, _ := url.Parse("http://127.0.0.1:4000")
	other, _ := url.Parse("http://127.0.0.1:4001/x")
	same, _ := url.Parse("http://127.0.0.1:4000/x")
	if newClient("t", nil).check(same) == nil {
		t.Error("a plain-http local origin is allowed with no test origin")
	}
	c := newClient("t", origin)
	if c.check(same) != nil || c.check(other) == nil {
		t.Error("the test origin alone should be allowed")
	}
}

func TestCleanTextBoundsAndStripsUnsafeCharacters(t *testing.T) {
	if got := cleanText("  Ti\x00tle‮​  \n x", 100, false); got != "Title    x" {
		t.Errorf("single line = %q", got)
	}
	if got := cleanText("a\r\nb\tc\x07", 100, true); got != "a\r\nb\tc" {
		t.Errorf("multiline = %q", got)
	}
	long := strings.Repeat("é", 700)
	got := cleanText(long, 500, false)
	if utf8.RuneCountInString(got) != 500 || !utf8.ValidString(got) {
		t.Errorf("cut to %d characters, valid=%v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
}

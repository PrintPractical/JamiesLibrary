// Package setupcode is the format of the line the server prints at start-up
// when no administrator exists yet. People and tests find the code by it.
package setupcode

import (
	"crypto/rand"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// alphabet is Crockford's base32: 32 symbols, so a random byte masked to five
// bits picks one without bias.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New draws a code from crypto/rand: 16 symbols (80 bits) as XXXX-XXXX-XXXX-XXXX.
func New() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("drawing a setup code: %w", err)
	}
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[v&31])
	}
	return sb.String(), nil
}

// Normalize is how a typed code is compared: upper case, without dashes and
// spaces, with Crockford's O read as 0 and I or L as 1.
func Normalize(typed string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch r {
		case '-', ' ':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// Prefix starts the line; the code follows it.
const Prefix = "SETUP CODE: "

// Format is the line's shape, for documentation: four groups of four.
const Format = Prefix + "XXXX-XXXX-XXXX-XXXX"

// pattern is what the code looks like: each X is an upper-case letter or a digit.
const pattern = `[A-Z0-9]{4}(?:-[A-Z0-9]{4}){3}`

var valid = regexp.MustCompile(`^` + pattern + `$`)

// Valid reports whether code has the form XXXX-XXXX-XXXX-XXXX.
func Valid(code string) bool { return valid.MatchString(code) }

// Line is the whole output line for code, without a newline.
func Line(code string) string { return Prefix + code }

// Print writes the setup-code line, with its newline, to w. It refuses a
// code of the wrong form, so the line can always be found again.
func Print(w io.Writer, code string) error {
	if !Valid(code) {
		return fmt.Errorf("setup code %q is not of the form XXXX-XXXX-XXXX-XXXX", code)
	}
	_, err := fmt.Fprintln(w, Line(code))
	return err
}

// Pattern is the regular expression a line of output is matched with; its one
// group is the code.
var Pattern = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(Prefix) + `(` + pattern + `)\s*$`)

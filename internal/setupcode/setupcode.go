// Package setupcode is the format of the line the server prints at start-up
// when no administrator exists yet. People and tests find the code by it.
package setupcode

import (
	"fmt"
	"io"
	"regexp"
)

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

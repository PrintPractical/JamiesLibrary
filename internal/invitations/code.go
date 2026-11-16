package invitations

import (
	"crypto/rand"
	"strings"
)

// CodeLength is the number of characters in a code: 16 of Crockford base32, 80 bits.
const CodeLength = 16

// alphabet is Crockford base32: no I, L, O or U.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewCode draws a code from crypto/rand and returns it as XXXX-XXXX-XXXX-XXXX.
func NewCode() (string, error) {
	b := make([]byte, CodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[c&31])
	}
	return sb.String(), nil
}

// Normalise puts what a person typed into the stored form, or reports false
// when it can't be a code. Case, dashes and spaces are ignored, and Crockford's
// look-alike mapping applies (O is 0, I and L are 1).
func Normalise(typed string) (string, bool) {
	if len(typed) > 4*CodeLength { // far more than any code with its dashes and spaces
		return "", false
	}
	var sb strings.Builder
	n := 0
	for _, r := range strings.ToUpper(typed) {
		switch {
		case r == '-' || r == ' ':
			continue
		case r == 'O':
			r = '0'
		case r == 'I' || r == 'L':
			r = '1'
		}
		if r > 127 || !strings.ContainsRune(alphabet, r) {
			return "", false
		}
		if n > 0 && n%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteRune(r)
		n++
		if n > CodeLength {
			return "", false
		}
	}
	if n != CodeLength {
		return "", false
	}
	return sb.String(), true
}

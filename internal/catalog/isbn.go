package catalog

import (
	"strings"

	"jamieslibrary/internal/apperr"
)

// ParseISBN reads an ISBN-10 or ISBN-13, with or without hyphens and spaces,
// checks its check digit and returns it as 13 digits. The error is a field
// error on "isbn".
func ParseISBN(s string) (string, error) {
	isbn, ok := normaliseISBN(s)
	if !ok {
		return "", apperr.OnField(apperr.Invalid, "isbn", "That is not a valid ISBN. Check the digits (an ISBN has 10 or 13) and try again.")
	}
	return isbn, nil
}

// lastIndex is the index of the last character of s that is not a hyphen or space.
func lastIndex(s string) int {
	return len(strings.TrimRight(s, "- ")) - 1
}

func normaliseISBN(s string) (string, bool) {
	var b []byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '-' || c == ' ':
		case c >= '0' && c <= '9':
			b = append(b, c)
		case (c == 'X' || c == 'x') && len(b) == 9 && i == lastIndex(s):
			b = append(b, 'X') // only as the last character of an ISBN-10
		default:
			return "", false
		}
	}
	switch len(b) {
	case 10:
		sum := 0
		for i, c := range b {
			v := int(c - '0')
			if c == 'X' {
				v = 10
			}
			sum += v * (10 - i)
		}
		if sum%11 != 0 {
			return "", false
		}
		body := "978" + string(b[:9])
		return body + string('0'+byte(isbn13Check(body))), true
	case 13:
		if !strings.HasPrefix(string(b), "978") && !strings.HasPrefix(string(b), "979") {
			return "", false
		}
		if isbn13Check(string(b[:12])) != int(b[12]-'0') {
			return "", false
		}
		return string(b), true
	}
	return "", false
}

// isbn13Check is the check digit for the first 12 digits.
func isbn13Check(first12 string) int {
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(first12[i] - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return (10 - sum%10) % 10
}

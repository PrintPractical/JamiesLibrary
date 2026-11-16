package catalog

import (
	"testing"

	"jamieslibrary/internal/apperr"
)

func TestISBNsNormaliseToThirteenDigits(t *testing.T) {
	// The Hobbit: the same ISBN written five ways.
	for _, in := range []string{"9780547928227", "978-0-547-92822-7", " 978 0 547 92822 7 ", "054792822X", "0-547-92822-X"} {
		got, err := ParseISBN(in)
		if err != nil || got != "9780547928227" {
			t.Errorf("ParseISBN(%q) = %q, %v; want 9780547928227", in, got, err)
		}
	}
}

func TestISBNTenWithXCheckDigit(t *testing.T) {
	for _, in := range []string{"080442957X", "0-8044-2957-x"} {
		got, err := ParseISBN(in)
		if err != nil || got != "9780804429573" {
			t.Errorf("ParseISBN(%q) = %q, %v; want 9780804429573", in, got, err)
		}
	}
}

func TestBadISBNsAreFieldErrors(t *testing.T) {
	for _, in := range []string{
		"9780547928228",  // bad ISBN-13 check digit
		"0547928225",     // bad ISBN-10 check digit
		"9770547928222",  // not 978/979, whatever the check digit
		"12345",          // wrong length
		"97805479282271", // too long
		"978054792822X",  // X in an ISBN-13
		"0547X28224",     // X not last
		"abcdefghij",
		"9780547928227\n9",
	} {
		_, err := ParseISBN(in)
		e, ok := apperr.As(err)
		if !ok || e.Field != "isbn" || e.Code != apperr.Invalid {
			t.Errorf("ParseISBN(%q) error = %v; want an invalid field error on isbn", in, err)
		}
	}
}

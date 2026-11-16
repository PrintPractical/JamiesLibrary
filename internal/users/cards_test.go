package users

import (
	"regexp"
	"strconv"
	"testing"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

var cardForm = regexp.MustCompile(`^[0-9]{12}$`)

func cardOf(t *testing.T, s *Service, id int64) string {
	t.Helper()
	c, err := s.Card(ctx, id)
	if err != nil {
		t.Fatalf("Card(%d): %v", id, err)
	}
	return c
}

// CARD-1: every new account has a card of 12 digits.
func TestNewAccountGetsATwelveDigitCard(t *testing.T) {
	s := newService(t)
	u := create(t, s, "pat", perm.Patron)
	if c := cardOf(t, s, u.ID); !cardForm.MatchString(c) {
		t.Fatalf("card %q", c)
	}
}

func TestFiveHundredPatronsGetFiveHundredDistinctCards(t *testing.T) {
	s := newService(t)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		u := create(t, s, "p"+strconv.Itoa(i), perm.Patron)
		c := cardOf(t, s, u.ID)
		if seen[c] {
			t.Fatalf("card %s issued twice", c)
		}
		seen[c] = true
	}
}

// A card number is redrawn when a copy's identifier, or another card, already holds it.
func TestACardNeverEqualsAnExistingIdentifier(t *testing.T) {
	s := newService(t)
	first := create(t, s, "first", perm.Patron)
	taken := cardOf(t, s, first.ID)
	addCopyCode(t, s, "123456789012")
	draws := []string{"123456789012", taken, "000000000042"}
	defer func(old func() (string, error)) { drawCode = old }(drawCode)
	drawCode = func() (string, error) { c := draws[0]; draws = draws[1:]; return c, nil }
	u := create(t, s, "second", perm.Patron)
	if got := cardOf(t, s, u.ID); got != "000000000042" || len(draws) != 0 {
		t.Fatalf("card %s, %d draws left", got, len(draws))
	}
}

func TestCardIssuingGivesUpWhenEveryDrawIsTaken(t *testing.T) {
	s := newService(t)
	u := create(t, s, "first", perm.Patron)
	taken := cardOf(t, s, u.ID)
	defer func(old func() (string, error)) { drawCode = old }(drawCode)
	drawCode = func() (string, error) { return taken, nil }
	if _, err := s.Create(ctx, NewUser{Username: "second", DisplayName: "s", PasswordHash: "x", Role: perm.Patron}); err == nil {
		t.Fatal("an account was made with no card")
	}
	if _, ok, _ := s.CredentialsByUsername(ctx, "second"); ok {
		t.Fatal("the account stayed although its card failed")
	}
}

// CARD-1 "Re-issue": the new card identifies the patron and the old code says no such patron.
func TestReissueReplacesTheCard(t *testing.T) {
	s := newService(t)
	lib := create(t, s, "lib", perm.Librarian)
	pat := create(t, s, "pat", perm.Patron)
	old := cardOf(t, s, pat.ID)
	var fresh string
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		var err error
		fresh, err = s.ReissueCard(ctx, tx, lib.Permissions, pat.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old || cardOf(t, s, pat.ID) != fresh {
		t.Fatalf("old %s new %s", old, fresh)
	}
	if p, err := s.PatronByCard(ctx, fresh); err != nil || p.ID != pat.ID || p.DisplayName != "pat" {
		t.Fatalf("new card: %+v %v", p, err)
	}
	_, err = s.PatronByCard(ctx, old)
	if e, ok := apperr.As(err); !ok || e.Code != apperr.NotFound || e.Message != "No such patron." {
		t.Fatalf("old card: %v", err)
	}
}

func TestReissueRefusesAUserWithMorePermissionsThanTheActor(t *testing.T) {
	s := newService(t)
	lib := create(t, s, "lib", perm.Librarian)
	admin := create(t, s, "admin", perm.Administrator)
	before := cardOf(t, s, admin.ID)
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		_, err := s.ReissueCard(ctx, tx, lib.Permissions, admin.ID)
		return err
	})
	if e, ok := apperr.As(err); !ok || e.Code != apperr.Forbidden {
		t.Fatalf("got %v", err)
	}
	if cardOf(t, s, admin.ID) != before {
		t.Fatal("the card changed")
	}
}

func TestReissueOfAnUnknownUserIsNotFound(t *testing.T) {
	s := newService(t)
	lib := create(t, s, "lib", perm.Librarian)
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		_, err := s.ReissueCard(ctx, tx, lib.Permissions, 999)
		return err
	})
	if e, ok := apperr.As(err); !ok || e.Code != apperr.NotFound {
		t.Fatalf("got %v", err)
	}
}

func TestPatronByCardNormalisesAndRefusesJunk(t *testing.T) {
	s := newService(t)
	u := create(t, s, "pat", perm.Patron)
	card := cardOf(t, s, u.ID)
	if p, err := s.PatronByCard(ctx, "  "+card+"\n"); err != nil || p.ID != u.ID {
		t.Fatalf("padded card: %+v %v", p, err)
	}
	for _, bad := range []string{"", "   ", "000000000000", "12 34", "%", string(make([]byte, 500))} {
		if _, err := s.PatronByCard(ctx, bad); err == nil {
			t.Errorf("%q identified someone", bad)
		}
	}
}

func TestACopyCodeIsNotACard(t *testing.T) {
	s := newService(t)
	create(t, s, "pat", perm.Patron)
	addCopyCode(t, s, "C0000001")
	if _, err := s.PatronByCard(ctx, "c0000001"); err == nil {
		t.Fatal("a copy code identified a patron")
	}
}

// addCopyCode puts a copy with the given identifier in the catalog.
func addCopyCode(t *testing.T, s *Service, code string) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO books (id, title, search_text, created_at) VALUES (1, 'B', 'b', 'x')`,
		`INSERT INTO copies (id, book_id, number, condition, location_id, created_at) VALUES (1, 1, 1, 'good', 1, 'x')`,
	} {
		if _, err := s.st.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.st.DB().ExecContext(ctx, `INSERT INTO identifiers (code, kind, copy_id) VALUES (?, 'copy', 1)`, code); err != nil {
		t.Fatal(err)
	}
}

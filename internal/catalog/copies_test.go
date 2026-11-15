package catalog

import (
	"errors"
	"strings"
	"testing"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/settings"
)

func addISBNBook(t *testing.T, s *Service, title string) Book {
	t.Helper()
	b, err := s.CreateBook(ctx, 1, BookInput{Title: title, Authors: []string{"A"}, ISBN: "9780306406157"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func location(t *testing.T, s *Service, name string) Location {
	t.Helper()
	l, err := s.CreateLocation(ctx, 1, name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func addCopy(t *testing.T, s *Service, book int64, in CopyInput) Copy {
	t.Helper()
	if in.LocationID == 0 {
		in.LocationID = 1 // Main shelf
	}
	c, err := s.AddCopy(ctx, 1, book, in)
	if err != nil {
		t.Fatalf("AddCopy: %v", err)
	}
	return c
}

func setMode(t *testing.T, s *Service, mode string) {
	t.Helper()
	if err := s.SetCopyIDMode(ctx, 1, mode); err != nil {
		t.Fatal(err)
	}
}

func insertCard(t *testing.T, s *Service, code string, user int64) {
	t.Helper()
	if _, err := s.st.DB().Exec(`INSERT INTO identifiers (code, kind, user_id) VALUES (?, 'card', ?)`, code, user); err != nil {
		t.Fatal(err)
	}
}

func message(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Message
	}
	return ""
}

// CAT-3: Two copies.
func TestTwoCopiesInDifferentLocations(t *testing.T) {
	s := newService(t, fakeLoans{onLoan: map[int64]bool{2: true}})
	b := addISBNBook(t, s, "Two")
	study := location(t, s, "Study")
	addCopy(t, s, b.ID, CopyInput{LocationID: 1, Condition: "good"})
	addCopy(t, s, b.ID, CopyInput{LocationID: study.ID, Condition: "worn"})
	cs, err := s.CopiesOf(ctx, b.ID)
	if err != nil || len(cs) != 2 {
		t.Fatalf("%+v %v", cs, err)
	}
	if cs[0].Number != 1 || cs[0].LocationName != "Main shelf" || cs[0].Condition != "good" || cs[0].OnLoan {
		t.Errorf("copy 1 = %+v", cs[0])
	}
	if cs[1].Number != 2 || cs[1].LocationName != "Study" || cs[1].Condition != "worn" || !cs[1].OnLoan {
		t.Errorf("copy 2 = %+v", cs[1])
	}
	r, _ := s.Search(ctx, "", study.ID, 1)
	if r.Total != 1 {
		t.Errorf("search by location found %d books", r.Total)
	}
	_, err = s.CopiesOf(ctx, 999)
	wantField(t, err, apperr.NotFound, "")
}

// CAT-9: Default (no barcode asked, shown as copy 2).
func TestDefaultModeAsksNoBarcodeForABookWithAnISBN(t *testing.T) {
	s := newService(t, nil)
	b := addISBNBook(t, s, "Isbn")
	addCopy(t, s, b.ID, CopyInput{})
	c := addCopy(t, s, b.ID, CopyInput{})
	if c.Number != 2 || c.Code != "" {
		t.Fatalf("%+v", c)
	}
	if got, ok, _ := s.LookupCode(ctx, "C0000002"); ok {
		t.Fatalf("a copy has a code nobody gave it: %+v", got)
	}
}

// CAT-9: No ISBN.
func TestBookWithoutISBNGetsItsOwnBarcode(t *testing.T) {
	s := newService(t, nil)
	b := add(t, s, "No isbn", "A")
	c := addCopy(t, s, b.ID, CopyInput{})
	if !strings.HasPrefix(c.Code, "C") || len(c.Code) < 8 {
		t.Fatalf("code = %q", c.Code)
	}
	// An ISBN book in the same library still gets none.
	if c2 := addCopy(t, s, addISBNBook(t, s, "Has").ID, CopyInput{}); c2.Code != "" {
		t.Fatalf("code = %q", c2.Code)
	}
}

// CAT-3: Generated barcode.
func TestGeneratedBarcodeIsUniqueAndNotACard(t *testing.T) {
	s := newService(t, nil)
	setMode(t, s, settings.CopyIDOwn)
	b := addISBNBook(t, s, "Own")
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		c := addCopy(t, s, b.ID, CopyInput{})
		if c.Code == "" || seen[c.Code] {
			t.Fatalf("code %q empty or repeated", c.Code)
		}
		seen[c.Code] = true
		var kind string
		if err := s.st.DB().QueryRow(`SELECT kind FROM identifiers WHERE code = ?`, c.Code).Scan(&kind); err != nil || kind != KindCopy {
			t.Fatalf("kind = %q %v", kind, err)
		}
	}
}

func TestGeneratedCodeSkipsACodeSomeoneTyped(t *testing.T) {
	s := newService(t, nil)
	setMode(t, s, settings.CopyIDOwn)
	b := addISBNBook(t, s, "Skip")
	addCopy(t, s, b.ID, CopyInput{Sticker: "c0000002"}) // copy 1, a sticker that looks generated
	c := addCopy(t, s, b.ID, CopyInput{})               // copy 2 would be C0000002
	if c.Code == "C0000002" || c.Code == "" {
		t.Fatalf("code = %q", c.Code)
	}
}

// CAT-3: Existing sticker.
func TestStickerIsKeptNormalised(t *testing.T) {
	s := newService(t, nil)
	setMode(t, s, settings.CopyIDOwn)
	c := addCopy(t, s, addISBNBook(t, s, "Sticker").ID, CopyInput{Sticker: "  ab-123 "})
	if c.Code != "AB-123" {
		t.Fatalf("code = %q", c.Code)
	}
	got, ok, err := s.LookupCode(ctx, "ab-123\n")
	if err != nil || !ok || got.ID != c.ID {
		t.Fatalf("lookup: %+v %v %v", got, ok, err)
	}
}

// In by-ISBN mode a sticker is still kept when given.
func TestStickerGivenInISBNModeIsKept(t *testing.T) {
	s := newService(t, nil)
	c := addCopy(t, s, addISBNBook(t, s, "Sticker").ID, CopyInput{Sticker: "X1"})
	if c.Code != "X1" {
		t.Fatalf("code = %q", c.Code)
	}
}

// CAT-3: Duplicate copy barcode.
func TestDuplicateStickerRefused(t *testing.T) {
	s := newService(t, nil)
	setMode(t, s, settings.CopyIDOwn)
	b := addISBNBook(t, s, "Dup")
	addCopy(t, s, b.ID, CopyInput{Sticker: "X"})
	_, err := s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1, Sticker: " x "})
	wantField(t, err, apperr.Conflict, "sticker")
	if strings.Contains(message(err), "card") {
		t.Errorf("message = %q", message(err))
	}
	if cs, _ := s.CopiesOf(ctx, b.ID); len(cs) != 1 {
		t.Fatalf("a refused copy was added: %+v", cs)
	}
	// Changing another copy's sticker to X is refused too; keeping its own is not.
	c2 := addCopy(t, s, b.ID, CopyInput{Sticker: "Y"})
	_, err = s.UpdateCopy(ctx, 1, c2.ID, CopyInput{LocationID: 1, Sticker: "X"})
	wantField(t, err, apperr.Conflict, "sticker")
	if _, err := s.UpdateCopy(ctx, 1, c2.ID, CopyInput{LocationID: 1, Sticker: "y"}); err != nil {
		t.Fatalf("a copy's own code: %v", err)
	}
}

// CAT-3: Sticker equals a card number.
func TestStickerEqualToACardRefusedWithThatMessage(t *testing.T) {
	s := newService(t, nil)
	b := addISBNBook(t, s, "Card")
	insertCard(t, s, "123456789012", 7)
	for _, mode := range []string{settings.CopyIDISBN, settings.CopyIDOwn} {
		setMode(t, s, mode)
		_, err := s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1, Sticker: " 123456789012 "})
		wantField(t, err, apperr.Conflict, "sticker")
		if !strings.Contains(message(err), "library card number") {
			t.Errorf("message = %q", message(err))
		}
	}
	c := addCopy(t, s, b.ID, CopyInput{Sticker: "S1"})
	_, err := s.UpdateCopy(ctx, 1, c.ID, CopyInput{LocationID: 1, Sticker: "123456789012"})
	wantField(t, err, apperr.Conflict, "sticker")
	if got, _ := s.GetCopy(ctx, c.ID); got.Code != "S1" {
		t.Fatalf("code = %q", got.Code)
	}
	// A card is not a copy.
	if _, ok, _ := s.LookupCode(ctx, "123456789012"); ok {
		t.Fatal("a card number found a copy")
	}
}

func TestBadInputIsRefused(t *testing.T) {
	s := newService(t, nil)
	b := addISBNBook(t, s, "Bad")
	for _, st := range []string{strings.Repeat("A", 33), "A B", "A\x00", "é", "A\tB"} {
		_, err := s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1, Sticker: st})
		wantField(t, err, apperr.Invalid, "sticker")
	}
	_, err := s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1, Condition: "shiny"})
	wantField(t, err, apperr.Invalid, "condition")
	_, err = s.AddCopy(ctx, 1, b.ID, CopyInput{})
	wantField(t, err, apperr.Invalid, "location")
	_, err = s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 999})
	wantField(t, err, apperr.Invalid, "location")
	_, err = s.AddCopy(ctx, 1, 999, CopyInput{LocationID: 1})
	wantField(t, err, apperr.NotFound, "")
	_, err = s.UpdateCopy(ctx, 1, 999, CopyInput{LocationID: 1})
	wantField(t, err, apperr.NotFound, "")
	if cs, _ := s.CopiesOf(ctx, b.ID); len(cs) != 0 {
		t.Fatalf("copies = %+v", cs)
	}
}

func TestNormaliseCode(t *testing.T) {
	ok := map[string]string{" abc ": "ABC", "a-1.b_2/c": "A-1.B_2/C", strings.Repeat("z", 32): strings.Repeat("Z", 32), "9": "9"}
	for in, want := range ok {
		if got, err := NormaliseCode(in); err != nil || got != want {
			t.Errorf("NormaliseCode(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "   ", strings.Repeat("a", 33), "a b", "a\nb", "ä", "a\x7f"} {
		if got, err := NormaliseCode(in); err == nil {
			t.Errorf("NormaliseCode(%q) = %q, want an error", in, got)
		}
	}
}

func TestUpdateCopy(t *testing.T) {
	s := newService(t, nil)
	b := addISBNBook(t, s, "Upd")
	study := location(t, s, "Study")
	c := addCopy(t, s, b.ID, CopyInput{Condition: "new"})
	got, err := s.UpdateCopy(ctx, 1, c.ID, CopyInput{LocationID: study.ID, Condition: "Poor", Sticker: "st1"})
	if err != nil || got.Condition != "poor" || got.LocationID != study.ID || got.Code != "ST1" || got.Number != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	// No sticker leaves the code; no condition leaves the condition.
	got, err = s.UpdateCopy(ctx, 1, c.ID, CopyInput{LocationID: 1})
	if err != nil || got.Code != "ST1" || got.Condition != "poor" {
		t.Fatalf("%+v %v", got, err)
	}
	// The old code is free again once replaced.
	if _, err := s.UpdateCopy(ctx, 1, c.ID, CopyInput{LocationID: 1, Sticker: "ST2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1, Sticker: "ST1"}); err != nil {
		t.Fatalf("freed code refused: %v", err)
	}
	// In own mode, an update never leaves a copy without a code.
	setMode(t, s, settings.CopyIDOwn)
	nb := add(t, s, "plain", "A")
	pc := addCopy(t, s, nb.ID, CopyInput{})
	if got, _ := s.UpdateCopy(ctx, 1, pc.ID, CopyInput{LocationID: 1}); got.Code != pc.Code {
		t.Fatalf("code changed: %q -> %q", pc.Code, got.Code)
	}
}

// CAT-5: remove a copy; refused while on loan.
func TestRemoveCopyRefusedWhileOnLoan(t *testing.T) {
	loans := fakeLoans{onLoan: map[int64]bool{}}
	s := newService(t, loans)
	b := addISBNBook(t, s, "Rm")
	c1 := addCopy(t, s, b.ID, CopyInput{Sticker: "KEEP"})
	c2 := addCopy(t, s, b.ID, CopyInput{Sticker: "GONE"})
	loans.onLoan[c1.ID] = true
	err := s.RemoveCopy(ctx, 1, c1.ID)
	wantField(t, err, apperr.Conflict, "")
	if !strings.Contains(message(err), "on loan") {
		t.Errorf("message = %q", message(err))
	}
	if got, ok, _ := s.LookupCode(ctx, "KEEP"); !ok || got.ID != c1.ID {
		t.Fatal("a refused removal changed the copy")
	}
	if err := s.RemoveCopy(ctx, 1, c2.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LookupCode(ctx, "GONE"); ok {
		t.Error("a removed copy can still be scanned")
	}
	cs, _ := s.CopiesOf(ctx, b.ID)
	if len(cs) != 1 || cs[0].ID != c1.ID {
		t.Fatalf("copies = %+v", cs)
	}
	var removed string
	if err := s.st.DB().QueryRow(`SELECT removed_at FROM copies WHERE id = ?`, c2.ID).Scan(&removed); err != nil || removed == "" {
		t.Fatalf("the row should stay, marked: %q %v", removed, err)
	}
	// Its sticker is free again, and its number is not reused.
	n := addCopy(t, s, b.ID, CopyInput{Sticker: "GONE"})
	if n.Number != 3 {
		t.Errorf("number = %d", n.Number)
	}
	wantField(t, s.RemoveCopy(ctx, 1, c2.ID), apperr.NotFound, "")
}

func TestRemoveBookRemovesItsCopiesAndFreesTheirCodes(t *testing.T) {
	loans := fakeLoans{onLoan: map[int64]bool{}, loaned: map[int64]int{}}
	s := newService(t, loans)
	b := addISBNBook(t, s, "Whole")
	c := addCopy(t, s, b.ID, CopyInput{Sticker: "W1"})
	loans.loaned[b.ID] = 1
	wantField(t, s.RemoveBook(ctx, 1, b.ID), apperr.Conflict, "")
	if _, ok, _ := s.LookupCode(ctx, "W1"); !ok {
		t.Fatal("a refused removal freed the code")
	}
	loans.loaned[b.ID] = 0
	if err := s.RemoveBook(ctx, 1, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LookupCode(ctx, "W1"); ok {
		t.Error("a copy of a removed book can be scanned")
	}
	_, err := s.GetCopy(ctx, c.ID)
	wantField(t, err, apperr.NotFound, "")
	_, err = s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1})
	wantField(t, err, apperr.NotFound, "")
}

// CAT-9: Switch to own barcodes.
func TestSwitchToOwnGivesEveryCopyACodeAndKeepsLoans(t *testing.T) {
	loans := fakeLoans{onLoan: map[int64]bool{}}
	s := newService(t, loans)
	b := addISBNBook(t, s, "Three")
	c1 := addCopy(t, s, b.ID, CopyInput{})
	addCopy(t, s, b.ID, CopyInput{})
	addCopy(t, s, b.ID, CopyInput{Sticker: "MINE"})
	other := add(t, s, "Other", "A")
	addCopy(t, s, other.ID, CopyInput{}) // already has one
	loans.onLoan[c1.ID] = true
	removed := addCopy(t, s, b.ID, CopyInput{})
	if err := s.RemoveCopy(ctx, 1, removed.ID); err != nil {
		t.Fatal(err)
	}

	setMode(t, s, settings.CopyIDOwn)
	if m := s.settings.Get().CopyIDMode; m != "own" {
		t.Fatalf("mode = %q", m)
	}
	seen := map[string]bool{}
	cs, _ := s.CopiesOf(ctx, b.ID)
	if len(cs) != 3 {
		t.Fatalf("copies = %+v", cs)
	}
	for _, c := range cs {
		if c.Code == "" || seen[c.Code] {
			t.Fatalf("copy %d has code %q", c.Number, c.Code)
		}
		seen[c.Code] = true
	}
	if cs[2].Code != "MINE" || !cs[0].OnLoan {
		t.Errorf("%+v", cs)
	}
	var n int
	s.st.DB().QueryRow(`SELECT count(*) FROM identifiers`).Scan(&n)
	if n != 4 {
		t.Errorf("identifiers = %d, want 4 (the removed copy gets none)", n)
	}
}

// CAT-9: Switch back.
func TestSwitchBackKeepsCodesAndScanningStillWorks(t *testing.T) {
	s := newService(t, nil)
	b := addISBNBook(t, s, "Back")
	setMode(t, s, settings.CopyIDOwn)
	c := addCopy(t, s, b.ID, CopyInput{})
	setMode(t, s, settings.CopyIDISBN)
	if m := s.settings.Get().CopyIDMode; m != "isbn" {
		t.Fatalf("mode = %q", m)
	}
	got, ok, err := s.LookupCode(ctx, c.Code)
	if err != nil || !ok || got.ID != c.ID || got.Code != c.Code {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	// And the ISBN still identifies the book.
	if r := search(t, s, "9780306406157"); r.Total != 1 {
		t.Fatalf("isbn search found %d", r.Total)
	}
}

func TestSwitchRejectsAnUnknownModeAndChangesNothing(t *testing.T) {
	s := newService(t, nil)
	addCopy(t, s, addISBNBook(t, s, "X").ID, CopyInput{})
	wantField(t, s.SetCopyIDMode(ctx, 1, "both"), apperr.Invalid, "copy_id_mode")
	var n int
	s.st.DB().QueryRow(`SELECT count(*) FROM identifiers`).Scan(&n)
	if n != 0 || s.settings.Get().CopyIDMode != "isbn" {
		t.Fatalf("identifiers = %d, mode = %q", n, s.settings.Get().CopyIDMode)
	}
}

func TestIdentifierRowsAreOneOfCardOrCopy(t *testing.T) {
	s := newService(t, nil)
	c := addCopy(t, s, addISBNBook(t, s, "Chk").ID, CopyInput{})
	for _, q := range []string{
		`INSERT INTO identifiers (code, kind) VALUES ('A', 'card')`,
		`INSERT INTO identifiers (code, kind, user_id, copy_id) VALUES ('B', 'card', 1, ` + itoa(int(c.ID)) + `)`,
		`INSERT INTO identifiers (code, kind, user_id) VALUES ('C', 'copy', 1)`,
		`INSERT INTO identifiers (code, kind, copy_id) VALUES ('D', 'other', 1)`,
	} {
		if _, err := s.st.DB().Exec(q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}
	insertCard(t, s, "1", 1)
	if _, err := s.st.DB().Exec(`INSERT INTO identifiers (code, kind, user_id) VALUES ('1', 'card', 2)`); err == nil {
		t.Error("a duplicate code was accepted")
	}
}

// CAT-8 / CAT-5: delete a location holding copies is refused with the count, then move, then delete.
func TestDeleteLocationRefusedThenMoveThenDelete(t *testing.T) {
	s := newService(t, nil)
	b1, b2 := addISBNBook(t, s, "One"), add(t, s, "Two", "A")
	busy, dest := location(t, s, "Busy"), location(t, s, "Dest")
	addCopy(t, s, b1.ID, CopyInput{LocationID: busy.ID})
	addCopy(t, s, b2.ID, CopyInput{LocationID: busy.ID})
	gone := addCopy(t, s, b2.ID, CopyInput{LocationID: busy.ID})
	if err := s.RemoveCopy(ctx, 1, gone.ID); err != nil {
		t.Fatal(err)
	}
	other := addCopy(t, s, b2.ID, CopyInput{LocationID: dest.ID})

	locs, _ := s.Locations(ctx)
	for _, l := range locs {
		want := map[int64]int{busy.ID: 2, dest.ID: 1}[l.ID]
		if l.Copies != want {
			t.Errorf("%s holds %d, want %d", l.Name, l.Copies, want)
		}
	}
	err := s.DeleteLocation(ctx, 1, busy.ID)
	var inUse *LocationInUse
	if !errors.As(err, &inUse) || inUse.Copies != 2 {
		t.Fatalf("error = %v", err)
	}
	wantField(t, err, apperr.Conflict, "")
	if !strings.Contains(message(err), "2 copies are there") {
		t.Errorf("message = %q", message(err))
	}

	if _, err := s.MoveCopies(ctx, 1, busy.ID, busy.ID); err == nil {
		t.Error("moving to the same location was accepted")
	}
	_, err = s.MoveCopies(ctx, 1, busy.ID, 999)
	wantField(t, err, apperr.Invalid, "location")
	_, err = s.MoveCopies(ctx, 1, 999, dest.ID)
	wantField(t, err, apperr.NotFound, "")
	n, err := s.MoveCopies(ctx, 1, busy.ID, dest.ID)
	if err != nil || n != 2 {
		t.Fatalf("moved %d, %v", n, err)
	}
	if c, _ := s.GetCopy(ctx, other.ID); c.LocationID != dest.ID {
		t.Error("an unrelated copy moved")
	}
	// The removed copy still points at the old location, which doesn't stop its deletion.
	if err := s.DeleteLocation(ctx, 1, busy.ID); err != nil {
		t.Fatal(err)
	}
	locs, _ = s.Locations(ctx)
	for _, l := range locs {
		if l.ID == busy.ID {
			t.Fatal("the location is still listed")
		}
		if l.ID == dest.ID && l.Copies != 3 {
			t.Errorf("destination holds %d", l.Copies)
		}
	}
	cs, _ := s.CopiesOf(ctx, b2.ID)
	if len(cs) != 2 || cs[0].LocationName != "Dest" {
		t.Fatalf("%+v", cs)
	}
}

package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
)

var ctx = context.Background()

// fakeLoans says which copies and books are on loan.
type fakeLoans struct {
	NoLoans
	onLoan map[int64]bool
	loaned map[int64]int
}

func (f fakeLoans) LoanedCopies(_ context.Context, _ DB, id int64) (int, error) {
	return f.loaned[id], nil
}
func (f fakeLoans) OnLoan(_ context.Context, _ DB, id int64) (bool, error) {
	return f.onLoan[id], nil
}

func newService(t *testing.T, l Loans) *Service {
	t.Helper()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	set, err := settings.New(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, Options{Settings: set, Loans: l})
}

func add(t *testing.T, s *Service, title string, authors ...string) Book {
	t.Helper()
	b, err := s.CreateBook(ctx, 1, BookInput{Title: title, Authors: authors})
	if err != nil {
		t.Fatalf("CreateBook(%q): %v", title, err)
	}
	return b
}

func titles(r SearchResult) []string {
	var out []string
	for _, b := range r.Books {
		out = append(out, b.Title)
	}
	return out
}

func search(t *testing.T, s *Service, q string) SearchResult {
	t.Helper()
	r, err := s.Search(ctx, q, 0, 1)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return r
}

func wantField(t *testing.T, err error, code apperr.Code, field string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code || e.Field != field {
		t.Fatalf("error = %v; want %s on field %q", err, code, field)
	}
}

// CAT-1: Add a full book (the parts that need no cover upload).
func TestAddFullBook(t *testing.T) {
	s := newService(t, nil)
	b, err := s.CreateBook(ctx, 1, BookInput{
		Title: "  Good Omens ", Authors: []string{"Terry Pratchett", "", "Neil Gaiman"},
		ISBN: "0-575-04800-X", Description: "The end of the world.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Good Omens" || len(b.Authors) != 2 || b.Authors[0] != "Terry Pratchett" || b.Authors[1] != "Neil Gaiman" ||
		b.ISBN13 != "9780575048003" || b.Description != "The end of the world." || b.ItemType != "book" || b.Cover != "" {
		t.Fatalf("book = %+v", b)
	}
	got, err := s.GetBook(ctx, b.ID)
	if err != nil || fmt.Sprint(got) != fmt.Sprint(b) {
		t.Fatalf("GetBook = %+v, %v", got, err)
	}
	if r := search(t, s, "good omens"); r.Total != 1 {
		t.Fatalf("book is not in the catalog: %+v", r)
	}
}

// CAT-1: Old book with no ISBN or cover.
func TestOldBookWithTitleAndAuthorOnly(t *testing.T) {
	s := newService(t, nil)
	b := add(t, s, "Old Tales", "A. Nonymous")
	if b.ISBN13 != "" {
		t.Fatalf("ISBN = %q", b.ISBN13)
	}
	if r := search(t, s, "old"); r.Total != 1 {
		t.Fatalf("not listed: %+v", r)
	}
}

// CAT-1: Missing title.
func TestMissingTitleIsRefused(t *testing.T) {
	s := newService(t, nil)
	for _, title := range []string{"", "   \t"} {
		_, err := s.CreateBook(ctx, 1, BookInput{Title: title, Authors: []string{"A"}})
		wantField(t, err, apperr.Invalid, "title")
		if e, _ := apperr.As(err); !strings.Contains(e.Message, "required") {
			t.Errorf("message %q does not say the title is required", e.Message)
		}
	}
	if r := search(t, s, ""); r.Total != 0 {
		t.Fatal("a refused book was saved")
	}
}

func TestBookNeedsAnAuthorAndWithinLimits(t *testing.T) {
	s := newService(t, nil)
	_, err := s.CreateBook(ctx, 1, BookInput{Title: "T"})
	wantField(t, err, apperr.Invalid, "authors")
	_, err = s.CreateBook(ctx, 1, BookInput{Title: "T", Authors: []string{" "}})
	wantField(t, err, apperr.Invalid, "authors")
	_, err = s.CreateBook(ctx, 1, BookInput{Title: strings.Repeat("é", MaxTitle+1), Authors: []string{"A"}})
	wantField(t, err, apperr.Invalid, "title")
	_, err = s.CreateBook(ctx, 1, BookInput{Title: "T", Authors: []string{strings.Repeat("a", MaxAuthorName+1)}})
	wantField(t, err, apperr.Invalid, "authors")
	_, err = s.CreateBook(ctx, 1, BookInput{Title: "T", Authors: make([]string, 0), Description: "x"})
	wantField(t, err, apperr.Invalid, "authors")
	_, err = s.CreateBook(ctx, 1, BookInput{Title: "T", Authors: []string{"A"}, Description: strings.Repeat("d", MaxDescription+1)})
	wantField(t, err, apperr.Invalid, "description")
	_, err = s.CreateBook(ctx, 1, BookInput{Title: "T", Authors: []string{"A"}, ISBN: "9780547928228"})
	wantField(t, err, apperr.Invalid, "isbn")
	if r := search(t, s, ""); r.Total != 0 {
		t.Fatal("a refused book was saved")
	}
}

func TestDuplicateISBNConflictCarriesExistingBook(t *testing.T) {
	s := newService(t, nil)
	first, err := s.CreateBook(ctx, 1, BookInput{Title: "The Hobbit", Authors: []string{"J. R. R. Tolkien"}, ISBN: "9780547928227"})
	if err != nil {
		t.Fatal(err)
	}
	// The same ISBN written as an ISBN-10.
	_, err = s.CreateBook(ctx, 1, BookInput{Title: "Again", Authors: []string{"X"}, ISBN: "0-547-92822-X"})
	var dup *DuplicateISBN
	if !errors.As(err, &dup) || dup.BookID != first.ID {
		t.Fatalf("error = %v; want DuplicateISBN for book %d", err, first.ID)
	}
	wantField(t, err, apperr.Conflict, "isbn")

	other := add(t, s, "Other", "O")
	_, err = s.UpdateBook(ctx, 1, other.ID, BookInput{Title: "Other", Authors: []string{"O"}, ISBN: "9780547928227"})
	if !errors.As(err, &dup) {
		t.Fatalf("update to a taken ISBN: %v", err)
	}
	// Saving a book with its own ISBN is no conflict.
	if _, err := s.UpdateBook(ctx, 1, first.ID, BookInput{Title: "The Hobbit, 2nd", Authors: []string{"J. R. R. Tolkien"}, ISBN: "9780547928227"}); err != nil {
		t.Fatal(err)
	}
}

func TestRemovedBookGivesItsISBNBack(t *testing.T) {
	s := newService(t, nil)
	b, _ := s.CreateBook(ctx, 1, BookInput{Title: "A", Authors: []string{"A"}, ISBN: "9780547928227"})
	if err := s.RemoveBook(ctx, 1, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBook(ctx, 1, BookInput{Title: "A again", Authors: []string{"A"}, ISBN: "9780547928227"}); err != nil {
		t.Fatalf("re-adding after removal: %v", err)
	}
}

func TestUpdateBookChangesWhatSearchFinds(t *testing.T) {
	s := newService(t, nil)
	b := add(t, s, "Old Title", "Ann Smith")
	if _, err := s.UpdateBook(ctx, 1, b.ID, BookInput{Title: "New Title", Authors: []string{"Bob Jones", "Cy Brown"}}); err != nil {
		t.Fatal(err)
	}
	if r := search(t, s, "smith"); r.Total != 0 {
		t.Error("old author still found")
	}
	if r := search(t, s, "old title"); r.Total != 0 {
		t.Error("old title still found")
	}
	r := search(t, s, "brown")
	if r.Total != 1 || r.Books[0].Title != "New Title" || len(r.Books[0].Authors) != 2 {
		t.Fatalf("result = %+v", r)
	}
	_, err := s.UpdateBook(ctx, 1, 999, BookInput{Title: "T", Authors: []string{"A"}})
	wantField(t, err, apperr.NotFound, "")
	_, err = s.UpdateBook(ctx, 1, b.ID, BookInput{Authors: []string{"A"}})
	wantField(t, err, apperr.Invalid, "title")
	if got, _ := s.GetBook(ctx, b.ID); got.Title != "New Title" {
		t.Error("a refused update changed the book")
	}
}

func TestCoverIsAStoredFileName(t *testing.T) {
	s := newService(t, nil)
	b := add(t, s, "T", "A")
	if err := s.SetCover(ctx, 1, b.ID, "abc123.jpg"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBook(ctx, b.ID); got.Cover != "abc123.jpg" {
		t.Fatalf("cover = %q", got.Cover)
	}
	// Updating the text leaves the cover.
	if got, _ := s.UpdateBook(ctx, 1, b.ID, BookInput{Title: "T2", Authors: []string{"A"}}); got.Cover != "abc123.jpg" {
		t.Fatalf("cover lost on update: %q", got.Cover)
	}
	for _, bad := range []string{"../etc/passwd", "a/b.png", `a\b.png`, ".hidden", strings.Repeat("a", 201)} {
		wantField(t, s.SetCover(ctx, 1, b.ID, bad), apperr.Invalid, "cover")
	}
	if err := s.SetCover(ctx, 1, b.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBook(ctx, b.ID); got.Cover != "" {
		t.Fatal("cover not cleared")
	}
	wantField(t, s.SetCover(ctx, 1, 999, "a.png"), apperr.NotFound, "")
}

func TestRemoveBookIsSoftAndRefusedWhileOnLoan(t *testing.T) {
	c := fakeLoans{loaned: map[int64]int{}}
	s := newService(t, c)
	b := add(t, s, "Loaned", "A")
	c.loaned[b.ID] = 2
	err := s.RemoveBook(ctx, 1, b.ID)
	wantField(t, err, apperr.Conflict, "")
	if e, _ := apperr.As(err); e.Message != "This book cannot be removed while 2 copies are on loan: check them in first." {
		t.Errorf("message = %q", e.Message)
	}
	if _, err := s.GetBook(ctx, b.ID); err != nil {
		t.Fatal("a refused removal removed the book")
	}
	c.loaned[b.ID] = 0
	if err := s.RemoveBook(ctx, 1, b.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetBook(ctx, b.ID)
	wantField(t, err, apperr.NotFound, "")
	if r := search(t, s, "loaned"); r.Total != 0 {
		t.Fatal("a removed book is in search")
	}
	var removedAt string
	if err := s.st.DB().QueryRow(`SELECT removed_at FROM books WHERE id = ?`, b.ID).Scan(&removedAt); err != nil || removedAt == "" {
		t.Fatalf("the row should stay, marked removed: %q %v", removedAt, err)
	}
	wantField(t, s.RemoveBook(ctx, 1, b.ID), apperr.NotFound, "")
}

// CAT-2: Search by author, partial title, no match.
func TestSearchByAuthorAndTitle(t *testing.T) {
	s := newService(t, nil)
	add(t, s, "Gardens", "Ann Smith")
	add(t, s, "Rivers", "Bob SMITH")
	add(t, s, "Hills", "Cy Jones")
	add(t, s, "The Hobbit", "J. R. R. Tolkien")

	r := search(t, s, "smith")
	if r.Total != 2 || strings.Join(titles(r), ",") != "Gardens,Rivers" {
		t.Fatalf("smith -> %v", titles(r))
	}
	if r := search(t, s, "hobb"); r.Total != 1 || r.Books[0].Title != "The Hobbit" {
		t.Fatalf("hobb -> %v", titles(r))
	}
	if r := search(t, s, "Ann Smith"); r.Total != 1 {
		t.Fatalf("two words -> %v", titles(r))
	}
	if r := search(t, s, "nothing like this"); r.Total != 0 || len(r.Books) != 0 || r.Books == nil {
		t.Fatalf("no match -> %+v", r)
	}
	if r := search(t, s, ""); r.Total != 4 {
		t.Fatalf("empty query lists everything: %d", r.Total)
	}
}

func TestSearchIgnoresCaseBeyondASCII(t *testing.T) {
	s := newService(t, nil)
	add(t, s, "Émile ou De l'éducation", "Jean-Jacques Rousseau")
	if r := search(t, s, "ÉMILE"); r.Total != 1 {
		t.Fatal("É not matched ignoring case")
	}
	if r := search(t, s, "éducation"); r.Total != 1 {
		t.Fatal("é not matched")
	}
}

func TestSearchWordsMayBeSpreadAcrossTitleAndAuthors(t *testing.T) {
	s := newService(t, nil)
	hobbit := add(t, s, "The Hobbit", "J. R. R. Tolkien")
	add(t, s, "The Silmarillion", "Christopher Tolkien")
	add(t, s, "Hobbit Cooking", "Ann Cook")
	for _, q := range []string{"hobbit tolkien", "tolkien hobbit", "the hobbit tolkien", "  HOBBIT   tolkien ", "J.R.R. Tolkien", "j r r hobbit"} {
		r := search(t, s, q)
		if r.Total != 1 || r.Books[0].ID != hobbit.ID {
			t.Errorf("search %q -> %v; want only The Hobbit", q, titles(r))
		}
	}
	if r := search(t, s, "tolkien"); r.Total != 2 {
		t.Errorf("one word -> %v", titles(r))
	}
	// Every word must appear.
	for _, q := range []string{"hobbit rowling", "rowling hobbit", "hobbit tolkien rowling"} {
		if r := search(t, s, q); r.Total != 0 {
			t.Errorf("search %q -> %v; want nothing", q, titles(r))
		}
	}
	// A single word does not match across the title and an author's name.
	add(t, s, "Alpha", "Beta")
	if r := search(t, s, "phabeta"); r.Total != 0 {
		t.Error("a word matched across the title and author")
	}
	if r := search(t, s, "..."); r.Total != 0 {
		t.Error("periods only should be searched as written")
	}
	_, err := s.Search(ctx, "a b c d e f g h i j k", 0, 1)
	wantField(t, err, apperr.Invalid, "q")
}

func TestControlAndDirectionCharactersAreRefused(t *testing.T) {
	s := newService(t, nil)
	for _, bad := range []string{"a\x00b", "a\x1bb", "a\u202eb", "a\u2067b", "a\x7fb", "line\nbreak"} {
		_, err := s.CreateBook(ctx, 1, BookInput{Title: bad, Authors: []string{"A"}})
		wantField(t, err, apperr.Invalid, "title")
		_, err = s.CreateBook(ctx, 1, BookInput{Title: "T", Authors: []string{bad}})
		wantField(t, err, apperr.Invalid, "authors")
		_, err = s.CreateLocation(ctx, 1, bad)
		wantField(t, err, apperr.Invalid, "name")
		if bad != "line\nbreak" {
			_, err = s.Search(ctx, bad, 0, 1)
			wantField(t, err, apperr.Invalid, "q")
		}
	}
	add(t, s, "T", "A")
	if _, err := s.Search(ctx, "\x00", 0, 1); err == nil {
		t.Error("NUL in a search was accepted")
	}
	// A description keeps its line breaks.
	b, err := s.CreateBook(ctx, 1, BookInput{Title: "T2", Authors: []string{"A"}, Description: "one\n\ttwo"})
	if err != nil || b.Description != "one\n\ttwo" {
		t.Fatalf("%+v %v", b, err)
	}
	_, err = s.CreateBook(ctx, 1, BookInput{Title: "T3", Authors: []string{"A"}, Description: "a\x00b"})
	wantField(t, err, apperr.Invalid, "description")
}

func TestSearchByISBN(t *testing.T) {
	s := newService(t, nil)
	b, _ := s.CreateBook(ctx, 1, BookInput{Title: "The Hobbit", Authors: []string{"T"}, ISBN: "9780547928227"})
	add(t, s, "9780547928227 in a title", "T") // text of an ISBN is not an ISBN match
	for _, q := range []string{"9780547928227", "978-0-547-92822-7", "054792822X", "0-547-92822-X"} {
		r := search(t, s, q)
		if r.Total != 1 || r.Books[0].ID != b.ID {
			t.Errorf("search %q -> %v", q, titles(r))
		}
	}
	// A number that fails the check digit is searched as text.
	if r := search(t, s, "9780547928228"); r.Total != 0 {
		t.Errorf("bad-check ISBN found %v", titles(r))
	}
}

func TestSearchTermIsLiteralAndBounded(t *testing.T) {
	s := newService(t, nil)
	add(t, s, "50% Off", "A")
	add(t, s, "500 Ways", "A")
	add(t, s, "a_b", "A")
	add(t, s, "axb", "A")
	add(t, s, `back\slash`, "A")
	add(t, s, "backXslash", "A")
	for q, want := range map[string]string{"50%": "50% Off", "a_b": "a_b", `k\s`: `back\slash`} {
		r := search(t, s, q)
		if r.Total != 1 || r.Books[0].Title != want {
			t.Errorf("search %q -> %v; want only %q", q, titles(r), want)
		}
	}
	if r := search(t, s, "%"); r.Total != 1 {
		t.Errorf("'%%' matched %v, want only the one with a literal %%", titles(r))
	}
	if r := search(t, s, strings.Repeat("a", MaxSearchTerm)); r.Total != 0 {
		t.Error("a 200-character term should be accepted")
	}
	_, err := s.Search(ctx, strings.Repeat("é", MaxSearchTerm+1), 0, 1)
	wantField(t, err, apperr.Invalid, "q")
}

func TestSearchPagesAreFiftyWithATotal(t *testing.T) {
	s := newService(t, nil)
	for i := 0; i < 120; i++ {
		add(t, s, fmt.Sprintf("Book %03d", i), "A")
	}
	r1 := search(t, s, "book")
	r3, _ := s.Search(ctx, "book", 0, 3)
	r4, _ := s.Search(ctx, "book", 0, 4)
	r0, _ := s.Search(ctx, "book", 0, -5)
	if r1.Total != 120 || len(r1.Books) != 50 || r1.Books[0].Title != "Book 000" || r1.PageSize != 50 {
		t.Fatalf("page 1: total %d, %d books", r1.Total, len(r1.Books))
	}
	if len(r3.Books) != 20 || r3.Books[0].Title != "Book 100" || r3.Total != 120 {
		t.Fatalf("page 3: %d books", len(r3.Books))
	}
	if len(r4.Books) != 0 || r4.Total != 120 {
		t.Fatalf("page 4: %d books", len(r4.Books))
	}
	if r0.Page != 1 || r0.Books[0].Title != "Book 000" {
		t.Fatal("a page below 1 is page 1")
	}
	_, err := s.Search(ctx, "", 0, 1<<40)
	wantField(t, err, apperr.Invalid, "page")
}

// CAT-8
func TestMainShelfExistsOnAFreshDatabase(t *testing.T) {
	s := newService(t, nil)
	locs, err := s.Locations(ctx)
	if err != nil || len(locs) != 1 || locs[0].Name != "Main shelf" || locs[0].Copies != 0 {
		t.Fatalf("%+v %v", locs, err)
	}
}

func TestDuplicateLocationNameIgnoringCase(t *testing.T) {
	s := newService(t, nil)
	if _, err := s.CreateLocation(ctx, 1, "Study"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"study", " STUDY ", "main SHELF"} {
		_, err := s.CreateLocation(ctx, 1, name)
		wantField(t, err, apperr.Conflict, "name")
	}
	_, err := s.CreateLocation(ctx, 1, "  ")
	wantField(t, err, apperr.Invalid, "name")
	_, err = s.CreateLocation(ctx, 1, strings.Repeat("x", MaxLocation+1))
	wantField(t, err, apperr.Invalid, "name")
	if locs, _ := s.Locations(ctx); len(locs) != 2 {
		t.Fatalf("locations = %+v", locs)
	}
}

func TestCreateLocationWithPunctuation(t *testing.T) {
	s := newService(t, nil)
	l, err := s.CreateLocation(ctx, 1, "Study, top shelf")
	if err != nil || l.Name != "Study, top shelf" || l.ID == 0 {
		t.Fatalf("%+v %v", l, err)
	}
	locs, _ := s.Locations(ctx)
	if len(locs) != 2 || locs[1].Name != "Study, top shelf" {
		t.Fatalf("%+v", locs)
	}
}

func TestRenameLocationReportsItsCopies(t *testing.T) {
	s := newService(t, nil)
	b := add(t, s, "Kept", "A")
	loc, _ := s.CreateLocation(ctx, 1, "Kitchen")
	addCopy(t, s, b.ID, CopyInput{LocationID: loc.ID})
	l, err := s.RenameLocation(ctx, 1, loc.ID, "Kitchen shelf")
	if err != nil || l.Copies != 1 {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestRenameLocation(t *testing.T) {
	s := newService(t, nil)
	locs, _ := s.Locations(ctx)
	main := locs[0]
	study, _ := s.CreateLocation(ctx, 1, "Study")
	if l, err := s.RenameLocation(ctx, 1, main.ID, "Living room"); err != nil || l.Name != "Living room" {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := s.RenameLocation(ctx, 1, study.ID, "study"); err != nil { // only the case changes
		t.Fatalf("changing own case: %v", err)
	}
	_, err := s.RenameLocation(ctx, 1, study.ID, "LIVING ROOM")
	wantField(t, err, apperr.Conflict, "name")
	_, err = s.RenameLocation(ctx, 1, 999, "X")
	wantField(t, err, apperr.NotFound, "")
	_, err = s.RenameLocation(ctx, 1, study.ID, "")
	wantField(t, err, apperr.Invalid, "name")
	// The name Main shelf is free again after the rename.
	if _, err := s.CreateLocation(ctx, 1, "Main shelf"); err != nil {
		t.Fatal(err)
	}
}

// DEPLOY-T6: a search over 20,000 books answers within 200 ms.
func TestSearchOver20000BooksIsFast(t *testing.T) {
	s := newService(t, nil)
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		for i := 0; i < 20000; i++ {
			title := fmt.Sprintf("Volume %d of the Collected Works", i)
			author := fmt.Sprintf("Author%d Surname%d", i%700, i%1300)
			res, err := tx.ExecContext(ctx, `INSERT INTO books (title, search_text, created_at) VALUES (?, ?, 'x')`, title, strings.ToLower(title+"\n"+author))
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			if _, err := tx.ExecContext(ctx, `INSERT INTO book_authors (book_id, position, name) VALUES (?, 0, ?)`, id, author); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"collected", "surname17", "zzz", "volume 19999", ""} {
		start := time.Now()
		r, err := s.Search(ctx, q, 0, 1)
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("search %q: %d found in %v", q, r.Total, took)
		if want := map[string]int{"collected": 20000, "zzz": 0}; q == "collected" || q == "zzz" {
			if r.Total != want[q] {
				t.Errorf("search %q found %d, want %d", q, r.Total, want[q])
			}
		}
		// The race detector slows the code several times, so wall time is only asserted without it.
		if !raceEnabled && took > 200*time.Millisecond {
			t.Errorf("search %q (%d found) took %v, over 200ms", q, r.Total, took)
		}
	}
}

func TestRemoveBookRefusalWordingForOneCopy(t *testing.T) {
	c := fakeLoans{loaned: map[int64]int{}}
	s := newService(t, c)
	b := add(t, s, "Loaned once", "A")
	c.loaned[b.ID] = 1
	err := s.RemoveBook(ctx, 1, b.ID)
	wantField(t, err, apperr.Conflict, "")
	if e, _ := apperr.As(err); e.Message != "This book cannot be removed while 1 copy is on loan: check it in first." {
		t.Errorf("message = %q", e.Message)
	}
}

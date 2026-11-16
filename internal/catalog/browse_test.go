package catalog

import "testing"

func TestBookInputValidateAndCover(t *testing.T) {
	s := newService(t, nil)
	if err := (BookInput{Title: "", Authors: []string{"A"}}).Validate(); err == nil {
		t.Error("a book with no title validated")
	}
	if err := (BookInput{Title: "T", Authors: []string{"A"}, Cover: "../x"}).Validate(); err == nil {
		t.Error("a cover name with a path validated")
	}
	b, err := s.CreateBook(ctx, 1, BookInput{Title: "T", Authors: []string{"A"}, Cover: "abc.jpg"})
	if err != nil || b.Cover != "abc.jpg" {
		t.Fatalf("%+v %v", b, err)
	}
	// Updating without a cover keeps it; with one replaces it.
	if b, err = s.UpdateBook(ctx, 1, b.ID, BookInput{Title: "T2", Authors: []string{"A"}}); err != nil || b.Cover != "abc.jpg" {
		t.Fatalf("%+v %v", b, err)
	}
	if b, err = s.UpdateBook(ctx, 1, b.ID, BookInput{Title: "T2", Authors: []string{"A"}, Cover: "def.jpg"}); err != nil || b.Cover != "def.jpg" {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestCountsAndDueDates(t *testing.T) {
	s := newService(t, fakeLoans{loaned: map[int64]int{1: 1}})
	b := add(t, s, "T", "A")
	for i := 0; i < 3; i++ {
		if _, err := s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if n, avail, err := s.Counts(ctx, b.ID); err != nil || n != 3 || avail != 2 {
		t.Errorf("%d %d %v", n, avail, err)
	}
	if _, _, err := s.Counts(ctx, 99); err == nil {
		t.Error("counts of an unknown book")
	}
	if d, err := s.DueDates(ctx, b.ID); err != nil || len(d) != 0 {
		t.Errorf("%v %v", d, err)
	}
}

func TestMoveAndDeleteLocation(t *testing.T) {
	s := newService(t, nil)
	b := add(t, s, "T", "A")
	if _, err := s.AddCopy(ctx, 1, b.ID, CopyInput{LocationID: 1}); err != nil {
		t.Fatal(err)
	}
	to, err := s.CreateLocation(ctx, 1, "Attic")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveAndDeleteLocation(ctx, 1, 1, 1); err == nil {
		t.Error("moved to itself")
	}
	if err := s.MoveAndDeleteLocation(ctx, 1, 1, 99); err == nil {
		t.Error("moved to a location that does not exist")
	}
	if c, _ := s.CopiesOf(ctx, b.ID); c[0].LocationID != 1 {
		t.Errorf("a refused move moved the copy to %d", c[0].LocationID)
	}
	if err := s.MoveAndDeleteLocation(ctx, 1, 1, to.ID); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.CopiesOf(ctx, b.ID); c[0].LocationID != to.ID {
		t.Errorf("the copy is in %d", c[0].LocationID)
	}
	if ls, _ := s.Locations(ctx); len(ls) != 1 || ls[0].ID != to.ID {
		t.Errorf("%+v", ls)
	}
}

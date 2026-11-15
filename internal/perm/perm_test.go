package perm

import "testing"

func TestSixteenDistinctStablePermissions(t *testing.T) {
	want := []string{"catalog.browse", "loans.own.view", "loans.own.renew", "reviews.write", "circ.self_checkout",
		"patrons.lookup", "circ.desk", "cards.reissue", "catalog.manage", "reviews.remove", "reports.view",
		"activity.view", "settings.manage", "invitations.manage", "users.manage", "users.permissions"}
	if len(All) != len(want) {
		t.Fatalf("%d permissions", len(All))
	}
	for i, p := range All {
		if string(p) != want[i] {
			t.Errorf("All[%d] = %s, want %s", i, p, want[i])
		}
	}
}

func TestPresetsNest(t *testing.T) {
	p, l, a := Preset(Patron), Preset(Librarian), Preset(Administrator)
	if len(p) != 5 || len(l) != 10 || len(a) != 16 {
		t.Fatalf("sizes %d %d %d", len(p), len(l), len(a))
	}
	if !p.SubsetOf(l) || !l.SubsetOf(a) || l.SubsetOf(p) {
		t.Fatal("presets don't nest")
	}
	if len(Preset("nobody")) != 0 {
		t.Fatal("unknown role has permissions")
	}
}

func TestPresetIsACopy(t *testing.T) {
	Preset(Patron)[UsersManage] = struct{}{}
	if Preset(Patron).Has(UsersManage) {
		t.Fatal("changing a preset changed the next one")
	}
}

func TestSetHelpers(t *testing.T) {
	var nilSet Set
	if nilSet.Has(CatalogBrowse) || !nilSet.SubsetOf(Set{}) {
		t.Fatal("nil set")
	}
	if Preset(Patron).Equal(Preset(Librarian)) || !Preset(Patron).Equal(Preset(Patron)) {
		t.Fatal("Equal")
	}
	got := Preset(Librarian).Minus(Preset(Patron)).List()
	if len(got) != 5 || got[0] != PatronsLookup {
		t.Fatalf("minus = %v", got)
	}
}

func TestDiffFromPreset(t *testing.T) {
	if !DiffFromPreset(Preset(Librarian), Librarian).Empty() {
		t.Fatal("preset differs from itself")
	}
	s := Preset(Patron)
	delete(s, SelfCheckout)
	s[ReportsView] = struct{}{}
	d := DiffFromPreset(s, Patron)
	if len(d.Added) != 1 || d.Added[0] != ReportsView || len(d.Removed) != 1 || d.Removed[0] != SelfCheckout {
		t.Fatalf("diff = %+v", d)
	}
}

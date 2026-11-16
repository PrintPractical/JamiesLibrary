package users

import (
	"context"
	"strings"
	"testing"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

var ctx = context.Background()

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st)
}

func create(t *testing.T, s *Service, name string, role perm.Role) User {
	t.Helper()
	u, err := s.Create(ctx, NewUser{Username: name, DisplayName: name, PasswordHash: "x", Role: role})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	return u
}

func wantSet(t *testing.T, got, want perm.Set) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("permissions = %v, want exactly %v", got.List(), want.List())
	}
}

// ROLE-2 "New account": registering for the patron role gives exactly the patron permissions.
func TestNewAccountHasExactlyThePatronPermissions(t *testing.T) {
	s := newService(t)
	u := create(t, s, "ann", perm.Patron)
	wantSet(t, u.Permissions, perm.NewSet(perm.CatalogBrowse, perm.LoansOwnView, perm.LoansOwnRenew, perm.ReviewsWrite, perm.SelfCheckout))
	if u.Role != perm.Patron || !u.Active {
		t.Fatalf("role %q active %v", u.Role, u.Active)
	}
}

// ROLE-2 "Assign preset": a patron made librarian has exactly the librarian set.
func TestAssignLibrarianGivesExactlyTheLibrarianPermissions(t *testing.T) {
	s := newService(t)
	u := create(t, s, "ann", perm.Patron)
	u, err := s.AssignRole(ctx, u.ID, perm.Librarian)
	if err != nil {
		t.Fatal(err)
	}
	want := perm.NewSet(perm.CatalogBrowse, perm.LoansOwnView, perm.LoansOwnRenew, perm.ReviewsWrite, perm.SelfCheckout,
		perm.PatronsLookup, perm.CircDesk, perm.CardsReissue, perm.CatalogManage, perm.ReviewsRemove)
	wantSet(t, u.Permissions, want)
	for _, p := range []perm.Permission{perm.ReportsView, perm.ActivityView, perm.SettingsManage, perm.InvitationsManage, perm.UsersManage, perm.UsersPermissions} {
		if u.Permissions.Has(p) {
			t.Errorf("librarian has administrator-only %s", p)
		}
	}
}

// Assigning replaces earlier switches and, going down, removes what the old role had.
func TestAssignReplacesTheSet(t *testing.T) {
	s := newService(t)
	u := create(t, s, "ann", perm.Administrator)
	u, _ = s.SetPermission(ctx, u.ID, perm.ReportsView, false)
	u, _ = s.AssignRole(ctx, u.ID, perm.Administrator)
	wantSet(t, u.Permissions, perm.Preset(perm.Administrator))
	u, _ = s.AssignRole(ctx, u.ID, perm.Patron)
	wantSet(t, u.Permissions, perm.Preset(perm.Patron))
}

func TestAdministratorHasAllSixteen(t *testing.T) {
	if len(perm.All) != 16 {
		t.Fatalf("%d permissions", len(perm.All))
	}
	wantSet(t, create(t, newService(t), "root", perm.Administrator).Permissions, perm.NewSet(perm.All...))
}

// ROLE-1 "Permissions shown": all 16 listed, on or off.
func TestPermissionsShownListsAllSixteenOnOrOff(t *testing.T) {
	s := newService(t)
	u := create(t, s, "ann", perm.Patron)
	u, err := s.SetPermission(ctx, u.ID, perm.CatalogManage, true)
	if err != nil {
		t.Fatal(err)
	}
	states := u.PermissionStates()
	if len(states) != 16 {
		t.Fatalf("listed %d permissions", len(states))
	}
	on := 0
	for i, st := range states {
		if st.Permission != perm.All[i] {
			t.Fatalf("entry %d is %s", i, st.Permission)
		}
		if st.On != u.Permissions.Has(st.Permission) {
			t.Errorf("%s shown %v", st.Permission, st.On)
		}
		if st.On {
			on++
		}
	}
	if on != 6 || !states[8].On {
		t.Fatalf("%d on, want patron's 5 plus manage catalog", on)
	}
}

func TestSwitchingOnePermissionKeepsTheRoleAndShowsTheDifference(t *testing.T) {
	s := newService(t)
	u := create(t, s, "lib", perm.Librarian)
	u, _ = s.SetPermission(ctx, u.ID, perm.CatalogManage, false)
	u, _ = s.SetPermission(ctx, u.ID, perm.ReportsView, true)
	if u.Role != perm.Librarian {
		t.Fatalf("role = %s", u.Role)
	}
	d := u.DiffFromPreset()
	if len(d.Added) != 1 || d.Added[0] != perm.ReportsView || len(d.Removed) != 1 || d.Removed[0] != perm.CatalogManage {
		t.Fatalf("diff = %+v", d)
	}
}

func TestUsernameIsUniqueIgnoringCase(t *testing.T) {
	s := newService(t)
	create(t, s, "Ann", perm.Patron)
	_, err := s.Create(ctx, NewUser{Username: "ANN", DisplayName: "x", PasswordHash: "x", Role: perm.Patron})
	if e, ok := apperr.As(err); !ok || e.Code != apperr.Conflict || e.Field != "username" {
		t.Fatalf("err = %v", err)
	}
}

func TestBadInput(t *testing.T) {
	s := newService(t)
	for name, n := range map[string]NewUser{
		"no username": {DisplayName: "a", Role: perm.Patron},
		"no name":     {Username: "a", Role: perm.Patron},
		"bad role":    {Username: "a", DisplayName: "a", Role: "root"},
	} {
		if _, err := s.Create(ctx, n); err == nil {
			t.Errorf("%s: no error", name)
		} else if e, ok := apperr.As(err); !ok || e.Code != apperr.Invalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	u := create(t, s, "ann", perm.Patron)
	if _, err := s.AssignRole(ctx, u.ID, "root"); err == nil {
		t.Error("bad role assigned")
	}
	if _, err := s.SetPermission(ctx, u.ID, "nope", true); err == nil {
		t.Error("unknown permission switched")
	}
	if _, err := s.AssignRole(ctx, 999, perm.Patron); err == nil {
		t.Error("assigned to a missing user")
	} else if e, _ := apperr.As(err); e == nil || e.Code != apperr.NotFound {
		t.Errorf("missing user: %v", err)
	}
	if _, err := s.SetPermission(ctx, 999, perm.CatalogBrowse, true); err == nil {
		t.Error("switched for a missing user")
	}
}

// Security design §8: names that impersonate or smuggle control characters are refused.
func TestNamesThatLookLikeAnotherPersonAreRefused(t *testing.T) {
	s := newService(t)
	create(t, s, "jamie", perm.Patron)
	cases := map[string]struct {
		n     NewUser
		field string
	}{
		"bidi override in display name": {NewUser{Username: "a1", DisplayName: "\u202eeimaJ"}, "display_name"},
		"bidi isolate in display name":  {NewUser{Username: "a2", DisplayName: "Ja\u2066mie"}, "display_name"},
		"zero-width in display name":    {NewUser{Username: "a3", DisplayName: "Ja\u200bmie"}, "display_name"},
		"control in display name":       {NewUser{Username: "a4", DisplayName: "x\x1b[31m"}, "display_name"},
		"display name over 64":          {NewUser{Username: "a5", DisplayName: strings.Repeat("a", 65)}, "display_name"},
		"zero-width in username":        {NewUser{Username: "jamie\u200b", DisplayName: "Jamie"}, "username"},
		"bidi in username":              {NewUser{Username: "\u202ejamie", DisplayName: "Jamie"}, "username"},
		"NUL in username":               {NewUser{Username: "ad\x00min", DisplayName: "x"}, "username"},
		"space in username":             {NewUser{Username: "ja mie", DisplayName: "x"}, "username"},
		"no-break space in username":    {NewUser{Username: "ja\u00a0mie", DisplayName: "x"}, "username"},
		"username over 64":              {NewUser{Username: strings.Repeat("a", 65), DisplayName: "x"}, "username"},
		"blank username":                {NewUser{Username: " \t ", DisplayName: "x"}, "username"},
	}
	for name, c := range cases {
		c.n.Role = perm.Patron
		_, err := s.Create(ctx, c.n)
		if e, ok := apperr.As(err); !ok || e.Code != apperr.Invalid || e.Field != c.field {
			t.Errorf("%s: err = %v, want invalid on %s", name, err, c.field)
		}
	}
	var n int
	s.st.DB().QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	if n != 1 {
		t.Errorf("%d accounts exist, want only the first", n)
	}
}

func TestOrdinaryNamesAreAccepted(t *testing.T) {
	s := newService(t)
	u, err := s.Create(ctx, NewUser{Username: " zoë.o'neil ", DisplayName: "  Zoë O’Neil 山田  ", PasswordHash: "x", Role: perm.Patron})
	if err != nil || u.Username != "zoë.o'neil" || u.DisplayName != "Zoë O’Neil 山田" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.Create(ctx, NewUser{Username: "b", DisplayName: strings.Repeat("é", 64), PasswordHash: "x", Role: perm.Patron}); err != nil {
		t.Fatalf("64 code points refused: %v", err)
	}
}

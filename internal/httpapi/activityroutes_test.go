package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

func (s *site) logAction(actor activity.Actor, action, target string, details map[string]any) {
	s.t.Helper()
	ctx := context.Background()
	if err := s.st.WithTx(ctx, func(tx store.Tx) error { return activity.Record(ctx, tx, actor, action, target, details) }); err != nil {
		s.t.Fatal(err)
	}
}

// ADMIN-4 "Role change logged": an administrator reads the entry with its actor's display name.
func TestAdministratorReadsTheActivityLog(t *testing.T) {
	s := newSite(t)
	admin := s.addUser("ada", goodPassword, perm.Administrator)
	cookie := s.signIn("ada", goodPassword)
	s.logAction(activity.ByUser(admin.ID), activity.UserRoleChanged, "user:7", map[string]any{"from": "patron", "to": "librarian"})
	s.logAction(activity.ServerConsole, activity.UserPasswordReset, "user:7", nil)

	rec := s.req("GET", "/api/admin/activity", cookie, "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got activityPage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || got.Page != 1 || got.PerPage != 50 || len(got.Entries) != 2 {
		t.Fatalf("page %s", rec.Body)
	}
	console, change := got.Entries[0], got.Entries[1]
	if console.Action != "user.password_reset" || console.Actor.ID != nil || console.Actor.DisplayName != "Server console" {
		t.Fatalf("newest entry %+v", console)
	}
	if change.Action != "user.role_changed" || change.Actor.ID == nil || *change.Actor.ID != admin.ID || change.Actor.DisplayName != "ada" ||
		change.Target != "user:7" || string(change.Details) != `{"from":"patron","to":"librarian"}` || !change.At.Equal(s.now) {
		t.Fatalf("entry %+v", change)
	}
}

func TestActivityLogPaging(t *testing.T) {
	s := newSite(t)
	admin := s.addUser("ada", goodPassword, perm.Administrator)
	cookie := s.signIn("ada", goodPassword)
	for i := 0; i < 3; i++ {
		s.logAction(activity.ByUser(admin.ID), activity.SettingsChanged, "settings", nil)
	}
	var got activityPage
	rec := s.req("GET", "/api/admin/activity?page=2&per_page=2", cookie, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || len(got.Entries) != 1 || got.Total != 3 || got.Page != 2 || got.PerPage != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"per_page=101", "per_page=0x", "page=0", "page=abc", "per_page=-1"} {
		if rec := s.req("GET", "/api/admin/activity?"+q, cookie, ""); rec.Code != 422 {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
}

// ROLE-3: a caller without activity.view is refused, and so is one who isn't signed in.
func TestActivityLogRefusesCallersWithoutThePermission(t *testing.T) {
	s := newSite(t)
	s.addUser("lib", goodPassword, perm.Librarian)
	s.addUser("pat", goodPassword, perm.Patron)
	for _, name := range []string{"lib", "pat"} {
		if rec := s.req("GET", "/api/admin/activity", s.signIn(name, goodPassword), ""); rec.Code != 403 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := s.req("GET", "/api/admin/activity", "", ""); rec.Code != 401 {
		t.Errorf("anonymous: %d", rec.Code)
	}
}

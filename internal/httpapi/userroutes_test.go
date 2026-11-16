package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/users"
)

var ctxBG = context.Background()

func (s *site) user(id int64) users.User {
	s.t.Helper()
	u, err := s.users.Get(ctxBG, id)
	if err != nil {
		s.t.Fatal(err)
	}
	return u
}

func (s *site) sessions(id int64) int {
	s.t.Helper()
	var n int
	if err := s.st.DB().QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, id).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func decodeUser(t *testing.T, rec *httptest.ResponseRecorder) adminUserBody {
	t.Helper()
	var u adminUserBody
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &u) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	return u
}

func path(id int64, rest string) string { return fmt.Sprintf("/api/admin/users/%d/%s", id, rest) }

// ADMIN-2 "Per-user override": switching off a librarian's permission to manage the catalog leaves the rest and shows the difference.
func TestPerUserOverride(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	lib := s.addUser("lib", goodPassword, perm.Librarian)
	libTok := s.signIn("lib", goodPassword)
	cookie := s.signIn("ada", goodPassword)

	u := decodeUser(t, s.req("PUT", path(lib.ID, "permissions/catalog.manage"), cookie, `{"on":false}`))
	if u.Role != perm.Librarian || !u.FromPreset.Differs || len(u.FromPreset.Removed) != 1 || u.FromPreset.Removed[0] != "catalog.manage" || len(u.FromPreset.Added) != 0 {
		t.Fatalf("after: %+v", u)
	}
	got := s.user(lib.ID).Permissions
	if got.Has(perm.CatalogManage) || !got.Has(perm.CircDesk) || len(got) != len(perm.Preset(perm.Librarian))-1 {
		t.Fatalf("permissions %v", got.List())
	}
	// a permission taken away ends the user's sessions
	if s.req("GET", "/api/me", libTok, "").Code != 401 {
		t.Fatal("the librarian's session survived losing a permission")
	}
	// and the list shows it differs
	var page adminUserPage
	rec := s.req("GET", "/api/admin/users", cookie, "")
	if json.Unmarshal(rec.Body.Bytes(), &page) != nil || rec.Code != 200 || page.Total != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, e := range page.Users {
		if e.Username == "lib" && (!e.FromPreset.Differs || !e.Active) {
			t.Errorf("list entry %+v", e)
		}
		if e.Username == "ada" && e.FromPreset.Differs {
			t.Errorf("admin differs: %+v", e)
		}
	}
	// switching it back on shows no difference
	u = decodeUser(t, s.req("PUT", path(lib.ID, "permissions/catalog.manage"), cookie, `{"on":true}`))
	if u.FromPreset.Differs {
		t.Fatalf("still differs: %+v", u)
	}
}

// ADMIN-2 "Last admin": the only user who can manage users and permissions can't switch either off or deactivate themselves.
func TestLastAdminCannotBeReduced(t *testing.T) {
	s := newSite(t)
	ada := s.addUser("ada", goodPassword, perm.Administrator)
	cookie := s.signIn("ada", goodPassword)
	for _, tc := range []struct{ method, p, body string }{
		{"PUT", path(ada.ID, "permissions/users.manage"), `{"on":false}`},
		{"PUT", path(ada.ID, "permissions/users.permissions"), `{"on":false}`},
		{"POST", path(ada.ID, "deactivate"), ``},
		{"PUT", path(ada.ID, "role"), `{"role":"librarian"}`},
	} {
		if rec := s.req(tc.method, tc.p, cookie, tc.body); rec.Code != 409 {
			t.Errorf("%s %s: %d %s", tc.method, tc.p, rec.Code, rec.Body)
		}
	}
	if u := s.user(ada.ID); !u.Active || !u.Permissions.Equal(perm.Preset(perm.Administrator)) || s.sessions(ada.ID) == 0 {
		t.Fatalf("the last administrator changed: %+v", u)
	}
	// with a second administrator it is allowed
	s.addUser("bo", goodPassword, perm.Administrator)
	if rec := s.req("PUT", path(ada.ID, "permissions/users.manage"), cookie, `{"on":false}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// ADMIN-2 "Last admin": two administrators removing each other at the same moment leave one.
func TestTwoAdministratorsRemovingEachOtherLeaveOne(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := newSite(t)
		a := s.addUser("ada", goodPassword, perm.Administrator)
		b := s.addUser("bo", goodPassword, perm.Administrator)
		ca, cb := s.signIn("ada", goodPassword), s.signIn("bo", goodPassword)
		var wg sync.WaitGroup
		codes := make([]int, 2)
		wg.Add(2)
		go func() { defer wg.Done(); codes[0] = s.req("POST", path(b.ID, "deactivate"), ca, "").Code }()
		go func() { defer wg.Done(); codes[1] = s.req("POST", path(a.ID, "deactivate"), cb, "").Code }()
		wg.Wait()
		var active int
		if err := s.st.DB().QueryRow(`SELECT count(*) FROM users WHERE active = 1`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != 1 {
			t.Fatalf("round %d: %d active administrators (statuses %v)", i, active, codes)
		}
	}
}

// ADMIN-2 "Deactivate patron with a loan": the session ends and sign-in is refused. (The loan and the refused checkout are the circulation task's.)
func TestDeactivatePatronEndsSessionAndRefusesSignIn(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	patTok := s.signIn("pat", goodPassword)
	cookie := s.signIn("ada", goodPassword)

	if u := decodeUser(t, s.req("POST", path(pat.ID, "deactivate"), cookie, "")); u.Active {
		t.Fatalf("still active: %+v", u)
	}
	if s.req("GET", "/api/me", patTok, "").Code != 401 || s.sessions(pat.ID) != 0 {
		t.Fatal("the patron's session did not end")
	}
	rec := s.req("POST", "/api/auth/signin", "", fmt.Sprintf(`{"username":"pat","password":%q}`, goodPassword))
	if rec.Code != 403 || !strings.Contains(errMessage(t, rec), "deactivated") || sessionCookie(rec) != nil {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body)
	}
	if ok, err := s.users.IsActive(ctxBG, pat.ID); err != nil || ok {
		t.Fatalf("IsActive = %v, %v", ok, err)
	}
	// reactivating lets them in again, with the same password
	decodeUser(t, s.req("POST", path(pat.ID, "reactivate"), cookie, ""))
	s.signIn("pat", goodPassword)
}

// ACCESS-7 "Reset": the patron signs in with the temporary password and is made to choose a new one.
func TestAdministratorResetsAPassword(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	patTok := s.signIn("pat", goodPassword)
	cookie := s.signIn("ada", goodPassword)

	rec := s.req("POST", path(pat.ID, "reset-password"), cookie, "")
	var got struct {
		User              adminUserBody `json:"user"`
		TemporaryPassword string        `json:"temporary_password"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.TemporaryPassword) != 16 || !got.User.MustChangePassword {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
	if strings.Contains(s.logs.String(), got.TemporaryPassword) {
		t.Error("the temporary password is in the log")
	}
	// the old password and the old session are gone
	if s.req("GET", "/api/me", patTok, "").Code != 401 {
		t.Fatal("the old session survived the reset")
	}
	if rec := s.req("POST", "/api/auth/signin", "", fmt.Sprintf(`{"username":"pat","password":%q}`, goodPassword)); rec.Code != 401 {
		t.Fatalf("old password: %d", rec.Code)
	}
	tok := s.signIn("pat", got.TemporaryPassword)
	if rec := s.req("GET", "/api/me", tok, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"must_change_password":true`) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	change := fmt.Sprintf(`{"current_password":%q,"new_password":"a brand new passphrase"}`, got.TemporaryPassword)
	if rec := s.req("POST", "/api/me/password", tok, change); rec.Code != 204 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	s.signIn("pat", "a brand new passphrase")
	if s.user(pat.ID).MustChangePassword {
		t.Fatal("must-change still set")
	}
	// every reset gives a different password
	rec2 := s.req("POST", path(pat.ID, "reset-password"), cookie, "")
	if strings.Contains(rec2.Body.String(), got.TemporaryPassword) {
		t.Fatal("two resets gave the same password")
	}
}

// ADMIN-4 "Role change logged": a role change by an administrator shows in the log another administrator reads, with actor, target and time.
func TestRoleChangeIsLoggedEndToEnd(t *testing.T) {
	s := newSite(t)
	ada := s.addUser("ada", goodPassword, perm.Administrator)
	s.addUser("bo", goodPassword, perm.Administrator)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	ca := s.signIn("ada", goodPassword)
	cb := s.signIn("bo", goodPassword)

	if u := decodeUser(t, s.req("PUT", path(pat.ID, "role"), ca, `{"role":"librarian"}`)); u.Role != perm.Librarian || u.FromPreset.Differs {
		t.Fatalf("%+v", u)
	}
	decodeUser(t, s.req("POST", path(pat.ID, "deactivate"), ca, ""))
	decodeUser(t, s.req("POST", path(pat.ID, "reset-password"), ca, ""))
	var log activityPage
	rec := s.req("GET", "/api/admin/activity", cb, "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &log) != nil || len(log.Entries) != 3 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := log.Entries[2]
	if got.Action != "user.role_changed" || got.Actor.ID == nil || *got.Actor.ID != ada.ID || got.Target != fmt.Sprintf("user:%d", pat.ID) || !got.At.Equal(s.now) {
		t.Fatalf("entry %+v", got)
	}
	var d map[string]any
	if json.Unmarshal(got.Details, &d) != nil || d["from"] != "patron" || d["to"] != "librarian" {
		t.Fatalf("details %s", got.Details)
	}
	if log.Entries[1].Action != "user.deactivated" || log.Entries[0].Action != "user.password_reset" {
		t.Fatalf("entries %+v", log.Entries)
	}
	if strings.Contains(rec.Body.String(), "assword\":\"") {
		t.Fatalf("the log holds a password: %s", rec.Body)
	}
}

// ROLE-3 / no escalation (security design §4): who can't do what.
func TestNoEscalation(t *testing.T) {
	s := newSite(t)
	adm := s.addUser("ada", goodPassword, perm.Administrator)
	s.addUser("bo", goodPassword, perm.Administrator)
	lib := s.addUser("lib", goodPassword, perm.Librarian)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	// a librarian who may manage users and their permissions, but holds nothing more
	for _, p := range []perm.Permission{perm.UsersManage, perm.UsersPermissions} {
		if err := s.setPerm(lib.ID, p); err != nil {
			t.Fatal(err)
		}
	}
	libTok := s.signIn("lib", goodPassword)
	before := s.user(adm.ID)

	// (b) can't act on an administrator
	for _, tc := range []struct{ method, p, body string }{
		{"POST", path(adm.ID, "reset-password"), ""},
		{"POST", path(adm.ID, "deactivate"), ""},
		{"PUT", path(adm.ID, "role"), `{"role":"patron"}`},
		{"PUT", path(adm.ID, "permissions/catalog.browse"), `{"on":false}`},
	} {
		if rec := s.req(tc.method, tc.p, libTok, tc.body); rec.Code != 403 {
			t.Errorf("%s %s: %d %s", tc.method, tc.p, rec.Code, rec.Body)
		}
	}
	after := s.user(adm.ID)
	if !after.Active || !after.Permissions.Equal(before.Permissions) || after.MustChangePassword {
		t.Fatalf("the administrator changed: %+v", after)
	}
	// (a) can't grant what they don't hold, nor a role whose preset isn't within theirs
	for _, tc := range []struct{ p, body string }{
		{path(pat.ID, "permissions/settings.manage"), `{"on":true}`},
		{path(pat.ID, "role"), `{"role":"administrator"}`},
	} {
		if rec := s.req("PUT", tc.p, libTok, tc.body); rec.Code != 403 {
			t.Errorf("%s: %d %s", tc.p, rec.Code, rec.Body)
		}
	}
	if u := s.user(pat.ID); u.Role != perm.Patron || u.Permissions.Has(perm.SettingsManage) {
		t.Fatalf("the patron was raised: %+v", u)
	}
	// but they may do what is within their own: reset a patron's password
	if rec := s.req("POST", path(pat.ID, "reset-password"), libTok, ""); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func (s *site) setPerm(id int64, p perm.Permission) error {
	_, err := s.users.SetPermission(ctxBG, id, p, true)
	return err
}

// ROLE-3: each user-administration route refuses a caller without its permission, and changes nothing.
func TestUserRoutesRefuseCallersWithoutThePermission(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	lib := s.addUser("lib", goodPassword, perm.Librarian)
	// manage-users without manage-permissions, and the other way round
	s.setPerm(lib.ID, perm.UsersManage)
	other := s.addUser("other", goodPassword, perm.Librarian)
	s.setPerm(other.ID, perm.UsersPermissions)
	cases := []struct {
		who, method, p, body string
	}{
		{"pat", "GET", "/api/admin/users", ""},
		{"pat", "POST", path(pat.ID, "reset-password"), ""},
		{"pat", "POST", path(pat.ID, "deactivate"), ""},
		{"pat", "PUT", path(pat.ID, "role"), `{"role":"librarian"}`},
		{"lib", "PUT", path(pat.ID, "role"), `{"role":"librarian"}`},
		{"lib", "PUT", path(pat.ID, "permissions/reports.view"), `{"on":true}`},
		{"other", "GET", "/api/admin/users", ""},
		{"other", "POST", path(pat.ID, "reset-password"), ""},
		{"other", "POST", path(pat.ID, "deactivate"), ""},
		{"", "GET", "/api/admin/users", ""},
	}
	for _, tc := range cases {
		tok := ""
		if tc.who != "" {
			tok = s.signIn(tc.who, goodPassword)
		}
		want := 403
		if tc.who == "" {
			want = 401
		}
		if rec := s.req(tc.method, tc.p, tok, tc.body); rec.Code != want {
			t.Errorf("%s %s %s: %d %s", tc.who, tc.method, tc.p, rec.Code, rec.Body)
		}
	}
	if u := s.user(pat.ID); u.Role != perm.Patron || !u.Active || u.MustChangePassword {
		t.Fatalf("the patron changed: %+v", u)
	}
}

// Bad input and unknown users give errors the caller can act on, and change nothing.
func TestUserRoutesRefuseBadInput(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	cookie := s.signIn("ada", goodPassword)
	for _, tc := range []struct {
		method, p, body string
		want            int
	}{
		{"PUT", path(pat.ID, "role"), `{"role":"root"}`, 422},
		{"PUT", path(pat.ID, "role"), `{}`, 422},
		{"PUT", path(pat.ID, "role"), `{"role":"patron","extra":1}`, 400},
		{"PUT", path(pat.ID, "permissions/nope"), `{"on":true}`, 422},
		{"PUT", path(pat.ID, "permissions/catalog.browse"), `{}`, 422},
		{"POST", path(999, "deactivate"), "", 404},
		{"POST", "/api/admin/users/abc/reset-password", "", 404},
		{"POST", "/api/admin/users/-1/deactivate", "", 404},
		{"PUT", path(999, "role"), `{"role":"patron"}`, 404},
		{"GET", "/api/admin/users?per_page=101", "", 422},
		{"GET", "/api/admin/users?page=0", "", 422},
	} {
		if rec := s.req(tc.method, tc.p, cookie, tc.body); rec.Code != tc.want {
			t.Errorf("%s %s %s: %d %s", tc.method, tc.p, tc.body, rec.Code, rec.Body)
		}
	}
	if u := s.user(pat.ID); u.Role != perm.Patron || !u.Permissions.Equal(perm.Preset(perm.Patron)) {
		t.Fatalf("the patron changed: %+v", u)
	}
}

// A permission switched on by one administrator for a patron shows as an addition to the preset.
func TestPermissionAddedShowsAsDifference(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	pat := s.addUser("pat", goodPassword, perm.Patron)
	cookie := s.signIn("ada", goodPassword)
	u := decodeUser(t, s.req("PUT", path(pat.ID, "permissions/reports.view"), cookie, `{"on":true}`))
	if !u.FromPreset.Differs || len(u.FromPreset.Added) != 1 || u.FromPreset.Added[0] != "reports.view" {
		t.Fatalf("%+v", u)
	}
	// switching on what is already on changes nothing and logs nothing
	var n1, n2 int
	s.st.DB().QueryRow(`SELECT count(*) FROM activity`).Scan(&n1)
	decodeUser(t, s.req("PUT", path(pat.ID, "permissions/reports.view"), cookie, `{"on":true}`))
	s.st.DB().QueryRow(`SELECT count(*) FROM activity`).Scan(&n2)
	if n1 != n2 {
		t.Fatalf("an unchanged permission was logged: %d then %d", n1, n2)
	}
}

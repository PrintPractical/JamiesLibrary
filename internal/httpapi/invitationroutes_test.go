package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/users"
)

var codeShape = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`)

// invite makes an invitation for role as the signed-in cookie and returns its ID and code.
func (s *site) invite(cookie string, role perm.Role) (int64, string) {
	s.t.Helper()
	rec := s.req("POST", "/api/admin/invitations", cookie, fmt.Sprintf(`{"role":%q}`, role))
	var b invitationBody
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &b) != nil {
		s.t.Fatalf("create invitation: %d %s", rec.Code, rec.Body)
	}
	return b.ID, *b.Code
}

func (s *site) register(code, username string) *httptest.ResponseRecorder {
	return s.req("POST", "/api/register", "",
		fmt.Sprintf(`{"code":%q,"username":%q,"display_name":%q,"password":%q}`, code, username, "New "+username, goodPassword))
}

func (s *site) userCount() int {
	s.t.Helper()
	var n int
	if err := s.st.DB().QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

// ACCESS-2 "Valid code" and ROLE-2 "New account": the code makes an account with exactly the preset, a card and a session, and works once.
func TestValidCode(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	admin := s.signIn("ada", goodPassword)
	_, code := s.invite(admin, perm.Patron)
	if !codeShape.MatchString(code) {
		t.Fatalf("code %q", code)
	}
	rec := s.register(code, "pat")
	c := sessionCookie(rec)
	if rec.Code != 201 || c == nil {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	var me meBody
	if err := json.Unmarshal(s.req("GET", "/api/me", c.Value, "").Body.Bytes(), &me); err != nil || me.User.Username != "pat" || me.User.Role != perm.Patron {
		t.Fatalf("the new session isn't the new user: %+v %v", me, err)
	}
	want := perm.Strings(perm.Preset(perm.Patron).List())
	got := append([]string(nil), me.Permissions...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("permissions %v, want exactly %v", me.Permissions, want)
	}
	if rec := s.req("GET", "/api/me/card", c.Value, ""); rec.Code != 200 {
		t.Fatalf("no card: %d %s", rec.Code, rec.Body)
	}
	s.signIn("pat", goodPassword) // the password was stored
	before := s.userCount()
	if rec := s.register(code, "pat2"); rec.Code != 422 || s.userCount() != before {
		t.Fatalf("second use: %d %s", rec.Code, rec.Body)
	}
}

func TestCodeIsMatchedLoosely(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	_, code := s.invite(s.signIn("ada", goodPassword), perm.Patron)
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	if rec := s.register(typed, "pat"); rec.Code != 201 {
		t.Fatalf("%q: %d %s", typed, rec.Code, rec.Body)
	}
}

// ACCESS-2 "Invalid or used code": a wrong, used, revoked or malformed code is refused with the one message.
func TestInvalidOrUsedCode(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	admin := s.signIn("ada", goodPassword)
	_, used := s.invite(admin, perm.Patron)
	revokedID, revoked := s.invite(admin, perm.Patron)
	if rec := s.register(used, "first"); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	if rec := s.req("POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", revokedID), admin, ""); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	before := s.userCount()
	var msgs []string
	for _, code := range []string{"0000-0000-0000-0000", used, revoked, "nonsense", "", strings.Repeat("A", 5000)} {
		rec := s.register(code, "other")
		if rec.Code != 422 || sessionCookie(rec) != nil {
			t.Fatalf("code %.20q: %d %s", code, rec.Code, rec.Body)
		}
		msgs = append(msgs, errMessage(t, rec))
	}
	for _, m := range msgs {
		if m != msgs[0] || m == "" {
			t.Fatalf("the messages differ: %q", msgs)
		}
	}
	if s.userCount() != before {
		t.Fatal("an account was made")
	}
}

// ACCESS-3 "Revoke": an unused code, once revoked, can't be used.
func TestRevoke(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	admin := s.signIn("ada", goodPassword)
	id, code := s.invite(admin, perm.Patron)
	rec := s.req("POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", id), admin, "")
	var b invitationBody
	if json.Unmarshal(rec.Body.Bytes(), &b); rec.Code != 200 || b.Status != "revoked" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := s.register(code, "pat"); rec.Code != 422 {
		t.Fatalf("revoked code: %d", rec.Code)
	}
	if rec := s.req("POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", id), admin, ""); rec.Code != 200 {
		t.Fatalf("revoking twice: %d", rec.Code)
	}
	if rec := s.req("POST", "/api/admin/invitations/999/revoke", admin, ""); rec.Code != 404 {
		t.Fatalf("unknown id: %d", rec.Code)
	}
	if rec := s.req("POST", "/api/admin/invitations/abc/revoke", admin, ""); rec.Code != 404 {
		t.Fatalf("bad id: %d", rec.Code)
	}
	usedID, used := s.invite(admin, perm.Patron)
	s.register(used, "pat")
	if rec := s.req("POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", usedID), admin, ""); rec.Code != 409 {
		t.Fatalf("revoking a used code: %d", rec.Code)
	}
}

// ACCESS-3 "No escalation" (TestInvitationNoEscalation): a librarian with invitations.manage but not users.manage can offer only roles they hold.
func TestInvitationNoEscalation(t *testing.T) {
	s := newSite(t)
	admin := s.addUser("ada", goodPassword, perm.Administrator)
	lib := s.addUser("lib", goodPassword, perm.Librarian)
	if _, err := s.users.SetPermission(ctxBG, lib.ID, perm.InvitationsManage, true); err != nil {
		t.Fatal(err)
	}
	cookie := s.signIn("lib", goodPassword)
	_ = admin
	rec := s.req("POST", "/api/admin/invitations", cookie, `{"role":"administrator"}`)
	if rec.Code != 403 {
		t.Fatalf("administrator role: %d %s", rec.Code, rec.Body)
	}
	var n int
	s.st.DB().QueryRow(`SELECT count(*) FROM invitations`).Scan(&n)
	if n != 0 {
		t.Fatal("an invitation was stored")
	}
	s.invite(cookie, perm.Librarian)
	s.invite(cookie, perm.Patron)
	if rec := s.req("POST", "/api/admin/invitations", cookie, `{"role":"root"}`); rec.Code != 422 {
		t.Fatalf("unknown role: %d", rec.Code)
	}
}

// A role with a permission the creator holds only in part is refused: the whole preset must be held.
func TestCreatorMustHoldTheWholePreset(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	lib := s.addUser("lib", goodPassword, perm.Librarian)
	s.users.SetPermission(ctxBG, lib.ID, perm.InvitationsManage, true)
	s.users.SetPermission(ctxBG, lib.ID, perm.CardsReissue, false)
	cookie := s.signIn("lib", goodPassword)
	if rec := s.req("POST", "/api/admin/invitations", cookie, `{"role":"librarian"}`); rec.Code != 403 {
		t.Fatalf("%d", rec.Code)
	}
	s.invite(cookie, perm.Patron)
}

// The management routes are for holders of invitations.manage only, and nothing changes for anyone else.
func TestInvitationRoutesNeedThePermission(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	id, _ := s.invite(s.signIn("ada", goodPassword), perm.Patron)
	s.addUser("pat", goodPassword, perm.Patron)
	pat := s.signIn("pat", goodPassword)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/admin/invitations", `{"role":"patron"}`},
		{"GET", "/api/admin/invitations", ""},
		{"POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", id), ""},
	} {
		if rec := s.req(c.method, c.path, pat, c.body); rec.Code != 403 || strings.Contains(rec.Body.String(), "code") && strings.Contains(rec.Body.String(), "-") {
			t.Fatalf("%s %s as a patron: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
		if rec := s.req(c.method, c.path, "", c.body); rec.Code != 401 {
			t.Fatalf("%s %s signed out: %d", c.method, c.path, rec.Code)
		}
	}
	var n int
	var revoked *string
	s.st.DB().QueryRow(`SELECT count(*), max(revoked_at) FROM invitations`).Scan(&n, &revoked)
	if n != 1 || revoked != nil {
		t.Fatalf("changed: %d %v", n, revoked)
	}
}

func TestListShowsCodesToHoldersOnly(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	admin := s.signIn("ada", goodPassword)
	_, a := s.invite(admin, perm.Patron)
	_, b := s.invite(admin, perm.Librarian)
	rec := s.req("GET", "/api/admin/invitations?per_page=1", admin, "")
	var page struct {
		Invitations []invitationBody `json:"invitations"`
		Total       int              `json:"total"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &page); rec.Code != 200 || page.Total != 2 || len(page.Invitations) != 1 ||
		page.Invitations[0].Code == nil || *page.Invitations[0].Code != b || page.Invitations[0].Status != "unused" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	_ = a
	if rec := s.req("GET", "/api/admin/invitations?per_page=101", admin, ""); rec.Code != 422 {
		t.Fatalf("page size over the limit: %d", rec.Code)
	}
}

// The activity log has the invitation's ID, role and creator, and never the code.
func TestActivityLogNeverHasTheCode(t *testing.T) {
	s := newSite(t)
	ada := s.addUser("ada", goodPassword, perm.Administrator)
	admin := s.signIn("ada", goodPassword)
	id, code := s.invite(admin, perm.Librarian)
	s.req("POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", id), admin, "")
	s.register(code, "x")
	rec := s.req("GET", "/api/admin/activity", admin, "")
	body := rec.Body.String()
	if strings.Contains(body, code) || strings.Contains(body, strings.ReplaceAll(code, "-", "")) {
		t.Fatalf("the code is in the log: %s", body)
	}
	for _, want := range []string{"invitation.created", "invitation.revoked", fmt.Sprintf("invitation:%d", id), `"role":"librarian"`, fmt.Sprintf(`"creator":%d`, ada.ID)} {
		if !strings.Contains(body, want) {
			t.Fatalf("log lacks %q: %s", want, body)
		}
	}
	if strings.Contains(s.logs.String(), code) {
		t.Fatal("the code is in the server log")
	}
}

// SEC-T1 "Guessing invitation codes": 10 wrong codes from one address block registration, valid code included, for 15 minutes.
func TestGuessingInvitationCodes(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	_, code := s.invite(s.signIn("ada", goodPassword), perm.Patron)
	for i := 0; i < 10; i++ {
		if rec := s.register("0000-0000-0000-000"+fmt.Sprint(i), "g"); rec.Code != 422 {
			t.Fatalf("guess %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := s.register(code, "pat")
	if rec.Code != 429 || sessionCookie(rec) != nil || !strings.Contains(errMessage(t, rec), "try again later") || rec.Header().Get("Retry-After") != "900" {
		t.Fatalf("valid code while blocked: %d %s", rec.Code, rec.Body)
	}
	s.remote = "203.0.113.9:5000"
	if rec := s.register("0000-0000-0000-0000", "g"); rec.Code != 422 {
		t.Fatalf("another address: %d", rec.Code)
	}
	s.remote = ""
	s.now = s.now.Add(15*time.Minute + time.Second)
	if rec := s.register(code, "pat"); rec.Code != 201 {
		t.Fatalf("after the block: %d %s", rec.Code, rec.Body)
	}
}

// A valid code never resets the count: nine guesses, a real registration, and the next wrong guess is the tenth.
func TestValidCodeDoesNotResetTheCount(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	admin := s.signIn("ada", goodPassword)
	_, one := s.invite(admin, perm.Patron)
	_, two := s.invite(admin, perm.Patron)
	for i := 0; i < 9; i++ {
		s.register("0000-0000-0000-0000", "g")
	}
	if rec := s.register(one, "pat"); rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	s.register("0000-0000-0000-0000", "g") // the tenth
	if rec := s.register(two, "pat2"); rec.Code != 429 {
		t.Fatalf("a valid code reset the count: %d", rec.Code)
	}
}

// A mistake in the rest of the form, with a valid code, is told apart, keeps the code usable and isn't a guess.
func TestFormProblemsKeepTheCode(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	_, code := s.invite(s.signIn("ada", goodPassword), perm.Patron)
	s.addUser("taken", goodPassword, perm.Patron)
	for _, c := range []struct {
		user, name, pw string
		status         int
		field          string
	}{
		{"pat", "Pat", "short", 422, "password"},
		{"pat", "", goodPassword, 422, "display_name"},
		{"has space", "Pat", goodPassword, 422, "username"},
		{"TAKEN", "Pat", goodPassword, 409, "username"},
	} {
		for i := 0; i < 12; i++ { // far more than the limit: none counts
			rec := s.req("POST", "/api/register", "", fmt.Sprintf(`{"code":%q,"username":%q,"display_name":%q,"password":%q}`, code, c.user, c.name, c.pw))
			var e errResp
			json.Unmarshal(rec.Body.Bytes(), &e)
			if rec.Code != c.status || e.Error.Field != c.field {
				t.Fatalf("%+v: %d %s", c, rec.Code, rec.Body)
			}
		}
	}
	if rec := s.register(code, "pat"); rec.Code != 201 {
		t.Fatalf("the code was spent: %d %s", rec.Code, rec.Body)
	}
}

// The bad code is told before anything about the rest of the form.
func TestWrongCodeSaysNothingAboutTheForm(t *testing.T) {
	s := newSite(t)
	s.addUser("taken", goodPassword, perm.Patron)
	rec := s.req("POST", "/api/register", "", `{"code":"0000-0000-0000-0000","username":"taken","display_name":"","password":"x"}`)
	var e errResp
	json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 422 || e.Error.Field != "code" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Two registrations at once on one code make exactly one account.
func TestSimultaneousRegistrationsOnOneCode(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	_, code := s.invite(s.signIn("ada", goodPassword), perm.Patron)
	before := s.userCount()
	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// httptest recorders and the handler are safe to use from several goroutines
			codes[i] = s.register(code, fmt.Sprintf("racer%d", i)).Code
		}()
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		if c == 201 {
			created++
		} else if c != 422 && c != 429 {
			t.Fatalf("statuses %v", codes)
		}
	}
	if created != 1 || s.userCount() != before+1 {
		t.Fatalf("statuses %v, users %d -> %d", codes, before, s.userCount())
	}
}

// An invitation whose creator lost a permission, or was deactivated, is refused at use.
func TestCreatorWhoLostAPermissionOrWasDeactivated(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	lib := s.addUser("lib", goodPassword, perm.Librarian)
	s.users.SetPermission(ctxBG, lib.ID, perm.InvitationsManage, true)
	cookie := s.signIn("lib", goodPassword)
	_, lost := s.invite(cookie, perm.Librarian)
	_, gone := s.invite(cookie, perm.Patron)
	_, kept := s.invite(cookie, perm.Patron)

	s.users.SetPermission(ctxBG, lib.ID, perm.CircDesk, false)
	if rec := s.register(lost, "a"); rec.Code != 422 {
		t.Fatalf("creator lost circ.desk: %d %s", rec.Code, rec.Body)
	}
	if rec := s.register(kept, "b"); rec.Code != 201 { // the patron preset is still held
		t.Fatalf("patron code of the same creator: %d %s", rec.Code, rec.Body)
	}
	admin := s.signIn("ada", goodPassword)
	if rec := s.req("POST", path(lib.ID, "deactivate"), admin, ""); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec := s.register(gone, "c"); rec.Code != 422 {
		t.Fatalf("deactivated creator: %d %s", rec.Code, rec.Body)
	}
	_ = users.User{}
}

// A registration made while signed in ends the old session and signs in as the new user.
func TestRegisteringReplacesTheOldSession(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	admin := s.signIn("ada", goodPassword)
	_, code := s.invite(admin, perm.Patron)
	rec := s.req("POST", "/api/register", admin, fmt.Sprintf(`{"code":%q,"username":"pat","display_name":"Pat","password":%q}`, code, goodPassword))
	if rec.Code != 201 || sessionCookie(rec).Value == admin {
		t.Fatalf("%d", rec.Code)
	}
	if rec := s.req("GET", "/api/me", admin, ""); rec.Code != 401 {
		t.Fatalf("the old session lives: %d", rec.Code)
	}
}

// ACCESS-3 / sec §4(a): a librarian who may manage invitations sees no code, and can't revoke one, for a role above theirs.
func TestLibrarianCannotUseOrRevokeAnAdministratorsCode(t *testing.T) {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	lib := s.addUser("lib", goodPassword, perm.Librarian)
	s.users.SetPermission(ctxBG, lib.ID, perm.InvitationsManage, true)
	adminID, adminCode := s.invite(s.signIn("ada", goodPassword), perm.Administrator)
	libCookie := s.signIn("lib", goodPassword)
	libID, libCode := s.invite(libCookie, perm.Librarian)
	patID, patCode := s.invite(libCookie, perm.Patron)

	rec := s.req("GET", "/api/admin/invitations", libCookie, "")
	var page struct {
		Invitations []invitationBody `json:"invitations"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &page); rec.Code != 200 || len(page.Invitations) != 3 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), adminCode) || strings.Contains(rec.Body.String(), strings.ReplaceAll(adminCode, "-", "")) {
		t.Fatalf("the administrator's code is in the response: %s", rec.Body)
	}
	for _, i := range page.Invitations {
		switch i.ID {
		case adminID:
			if i.Code != nil || i.Role != perm.Administrator {
				t.Fatalf("administrator row: %+v", i)
			}
		case libID, patID:
			if i.Code == nil || (*i.Code != libCode && *i.Code != patCode) {
				t.Fatalf("row %+v", i)
			}
		}
	}
	if rec := s.req("POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", adminID), libCookie, ""); rec.Code != 403 {
		t.Fatalf("revoking: %d %s", rec.Code, rec.Body)
	}
	var revoked *string
	s.st.DB().QueryRow(`SELECT revoked_at FROM invitations WHERE id = ?`, adminID).Scan(&revoked)
	if revoked != nil {
		t.Fatal("the code was revoked")
	}
	for _, id := range []int64{libID, patID} {
		if rec := s.req("POST", fmt.Sprintf("/api/admin/invitations/%d/revoke", id), libCookie, ""); rec.Code != 200 {
			t.Fatalf("revoking own-level code: %d", rec.Code)
		}
	}
	// the administrator still sees every code
	rec = s.req("GET", "/api/admin/invitations", s.signIn("ada", goodPassword), "")
	if !strings.Contains(rec.Body.String(), adminCode) || !strings.Contains(rec.Body.String(), libCode) {
		t.Fatalf("administrator's list: %s", rec.Body)
	}
}

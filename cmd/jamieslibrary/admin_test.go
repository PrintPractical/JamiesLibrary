package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/testutil"
	"jamieslibrary/internal/users"
)

const oldPassword = "the old password"

var tempLine = regexp.MustCompile(`(?m)^Temporary password for (\S+): (\S+)$`)

func admin(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = run(append([]string{"admin"}, args...), func(string) string { return "" }, &o, &e)
	return code, o.String(), e.String()
}

func do(t *testing.T, c *http.Client, method, url string, body any) (int, string) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// setUpOwner starts a fresh server, creates the first administrator through
// setup, leaves the server running and returns it with a signed-in client.
func setUpOwner(t *testing.T) (*testutil.Server, *http.Client) {
	t.Helper()
	s := testutil.StartServer(t, testutil.Options{})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatal("no setup code")
	}
	c := s.Client()
	if st, body := do(t, c, "POST", s.URL+"/api/setup/admin",
		map[string]string{"code": code, "username": "jamie", "display_name": "Jamie", "password": oldPassword}); st != 200 {
		t.Fatalf("setup: %d %s", st, body)
	}
	return s, c
}

// DEPLOY-4 "Recovery": the only administrator forgot their password; the owner
// runs the command on the server's data directory, and the administrator signs
// in with the temporary password and has to choose a new one.
func TestRecovery(t *testing.T) {
	s, signedIn := setUpOwner(t)
	dir := s.DataDir
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := admin("reset-password", "--data", dir, "jamie")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	m := tempLine.FindStringSubmatch(out)
	if m == nil || len(m[2]) != 16 {
		t.Fatalf("no 16-character temporary password in:\n%s", out)
	}
	if strings.Contains(errOut, "not an administrator") {
		t.Fatalf("an administrator was told they aren't one: %s", errOut)
	}
	temp := m[2]

	s2 := testutil.StartServer(t, testutil.Options{DataDir: dir})
	if _, ok := s2.SetupCode(); ok {
		t.Fatal("setup reopened")
	}
	c := s2.Client()
	if st, _ := do(t, c, "POST", s2.URL+"/api/auth/signin", map[string]string{"username": "jamie", "password": oldPassword}); st != 401 {
		t.Fatalf("the old password still works: %d", st)
	}
	st, body := do(t, c, "POST", s2.URL+"/api/auth/signin", map[string]string{"username": "jamie", "password": temp})
	if st != 200 || !strings.Contains(body, `"must_change_password":true`) {
		t.Fatalf("sign in with the temporary password: %d %s", st, body)
	}
	if st, _ := do(t, c, "GET", s2.URL+"/api/me/card", nil); st != 403 {
		t.Fatalf("a forced change doesn't hold: %d", st)
	}
	if st, body := do(t, c, "POST", s2.URL+"/api/me/password", map[string]string{"current_password": temp, "new_password": "a brand new password"}); st != 204 {
		t.Fatalf("change: %d %s", st, body)
	}
	if st, _ := do(t, c, "GET", s2.URL+"/api/me/card", nil); st != 200 {
		t.Fatalf("after the change: %d", st)
	}
	// the reset is in the activity log as the server console's, with no password in it
	st, body = do(t, c, "GET", s2.URL+"/api/admin/activity", nil)
	if st != 200 || !strings.Contains(body, `"action":"user.password_reset"`) || !strings.Contains(body, "Server console") || strings.Contains(body, temp) {
		t.Fatalf("activity: %d %s", st, body)
	}
	_ = signedIn
}

// The command works beside a running server (it takes no lock) and ends the user's sessions.
func TestResetEndsSessionsOnARunningServer(t *testing.T) {
	s, signedIn := setUpOwner(t)
	if st, _ := do(t, signedIn, "GET", s.URL+"/api/me", nil); st != 200 {
		t.Fatalf("not signed in: %d", st)
	}
	code, out, errOut := admin("reset-password", "jamie", "--data", s.DataDir) // flag after the name
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if st, _ := do(t, signedIn, "GET", s.URL+"/api/me", nil); st != 401 {
		t.Fatalf("the old session survived: %d", st)
	}
	temp := tempLine.FindStringSubmatch(out)[2]
	if st, _ := do(t, s.Client(), "POST", s.URL+"/api/auth/signin", map[string]string{"username": "JAMIE", "password": temp}); st != 200 {
		t.Fatalf("sign in: %d", st)
	}
}

func TestResetOfAnUnknownUserFails(t *testing.T) {
	s, _ := setUpOwner(t)
	code, out, errOut := admin("reset-password", "--data", s.DataDir, "nobody")
	if code == 0 || !strings.Contains(errOut, "nobody") || tempLine.MatchString(out) {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
}

func TestResetWithoutADataDirectoryDoesNotMakeOne(t *testing.T) {
	dir := t.TempDir() + "/empty"
	code, _, errOut := admin("reset-password", "--data", dir, "jamie")
	if code == 0 || !strings.Contains(errOut, dir) {
		t.Fatalf("exit %d err %q", code, errOut)
	}
}

func TestResetUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"reset-password"}, {"reset-password", "a", "b"}, {"frob"}} {
		if code, _, errOut := admin(args...); code != 2 || !strings.Contains(errOut, "usage") {
			t.Errorf("%v: exit %d err %q", args, code, errOut)
		}
	}
}

// A user who isn't an administrator is told so but still reset, and not made one; --make-admin
// applies the administrator preset, sets must-change and logs it as the server console's.
func TestResetOfAMemberAndMakeAdmin(t *testing.T) {
	s, _ := setUpOwner(t)
	dir := s.DataDir
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	us := users.New(st)
	ann, err := us.Create(ctx, users.NewUser{Username: "ann", DisplayName: "Ann", PasswordHash: "x", Role: perm.Patron})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	role := func() perm.Role {
		st, err := store.Open(ctx, dir, store.Options{NoLock: true})
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		u, err := users.New(st).Get(ctx, ann.ID)
		if err != nil {
			t.Fatal(err)
		}
		return u.Role
	}

	code, out, errOut := admin("reset-password", "--data", dir, "ann")
	if code != 0 || !strings.Contains(errOut, "not an administrator") || !tempLine.MatchString(out) {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
	if role() != perm.Patron {
		t.Fatal("a plain reset changed the role")
	}

	code, out, errOut = admin("reset-password", "--data", dir, "--make-admin", "ann")
	if code != 0 || !tempLine.MatchString(out) || strings.Contains(errOut, "not an administrator") {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
	if role() != perm.Administrator {
		t.Fatal("--make-admin did not make an administrator")
	}
	temp := tempLine.FindStringSubmatch(out)[2]

	s2 := testutil.StartServer(t, testutil.Options{DataDir: dir})
	if _, ok := s2.SetupCode(); ok {
		t.Fatal("setup open though an administrator exists")
	}
	c := s2.Client()
	if st, body := do(t, c, "POST", s2.URL+"/api/auth/signin", map[string]string{"username": "ann", "password": temp}); st != 200 ||
		!strings.Contains(body, `"must_change_password":true`) || !strings.Contains(body, `"administrator"`) {
		t.Fatalf("sign in: %d %s", st, body)
	}
	do(t, c, "POST", s2.URL+"/api/me/password", map[string]string{"current_password": temp, "new_password": "a brand new password"})
	st2, body := do(t, c, "GET", s2.URL+"/api/admin/activity", nil)
	if st2 != 200 || strings.Count(body, "Server console") != 3 || !strings.Contains(body, `"action":"user.role_changed"`) {
		t.Fatalf("activity: %d %s", st2, body)
	}
}

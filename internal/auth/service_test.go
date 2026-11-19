package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

var ctx = context.Background()

type env struct {
	st    *store.Store
	users *users.Service
	svc   *Service
	now   time.Time
	idle  time.Duration
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{st: st, users: users.New(st), now: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), idle: 8 * time.Hour}
	e.svc = NewService(st, e.users, func() time.Duration { return e.idle }, nil)
	e.svc.now = func() time.Time { return e.now }
	return e
}

func (e *env) addUser(t *testing.T, name, password string, role perm.Role) users.User {
	t.Helper()
	hash := ""
	if password != "" {
		var err error
		if hash, err = e.svc.Passwords.Hash(ctx, "password", password); err != nil {
			t.Fatal(err)
		}
	}
	u, err := e.users.Create(ctx, users.NewUser{Username: name, DisplayName: name, PasswordHash: hash, Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) lastSeen(t *testing.T, token string) string {
	t.Helper()
	var s string
	if err := e.st.DB().QueryRow(`SELECT last_seen_at FROM sessions WHERE token_hash = ?`, hashToken(token)).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func wantCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	if e, ok := apperr.As(err); !ok || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestSignInStoresOnlyTheTokensHash(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "correct horse battery", perm.Patron)
	token, id, err := e.svc.SignIn(ctx, "ann", "correct horse battery")
	if err != nil || id == 0 {
		t.Fatal(err)
	}
	var stored string
	if err := e.st.DB().QueryRow(`SELECT token_hash FROM sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != hashToken(token) || len(stored) != 64 {
		t.Fatalf("stored %q for token %q", stored, token)
	}
	other, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	if other == token {
		t.Fatal("two sign-ins share a token")
	}
}

// SEC-T1 "Idle session": a session unused for longer than the idle time is gone, and idle time is the setting.
func TestIdleSessionEnds(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "correct horse battery", perm.Patron)
	token, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")

	e.now = e.now.Add(8 * time.Hour) // exactly the idle time: still in
	if c, err := e.svc.Authenticate(ctx, token); err != nil || c == nil {
		t.Fatalf("at 8h: %v %v", c, err)
	}
	e.now = e.now.Add(8*time.Hour + time.Minute) // longer than the idle time since the last request
	if c, err := e.svc.Authenticate(ctx, token); err != nil || c != nil {
		t.Fatalf("after 8h01 idle: %v %v", c, err)
	}
	var n int
	e.st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sessions left: an expired one must be deleted", n)
	}
	// and it stays dead even if the clock were to go back
	e.now = e.now.Add(-24 * time.Hour)
	if c, _ := e.svc.Authenticate(ctx, token); c != nil {
		t.Fatal("an expired session came back")
	}
}

func TestIdleTimeFollowsTheSettingAtEachRequest(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "correct horse battery", perm.Patron)
	token, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	e.idle = 15 * time.Minute
	e.now = e.now.Add(16 * time.Minute)
	if c, _ := e.svc.Authenticate(ctx, token); c != nil {
		t.Fatal("still signed in after 16 minutes with a 15-minute setting")
	}
}

func TestLastSeenIsWrittenAtMostOncePerMinute(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "correct horse battery", perm.Patron)
	token, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	first := e.lastSeen(t, token)
	e.now = e.now.Add(59 * time.Second)
	e.svc.Authenticate(ctx, token)
	if got := e.lastSeen(t, token); got != first {
		t.Fatalf("written after 59 s: %s -> %s", first, got)
	}
	e.now = e.now.Add(time.Second)
	e.svc.Authenticate(ctx, token)
	if got := e.lastSeen(t, token); got == first {
		t.Fatal("not written after a minute")
	}
}

func TestEachRequestLoadsTheUserAndPermissionsFromTheDatabase(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "ann", "correct horse battery", perm.Librarian)
	token, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	c, _ := e.svc.Authenticate(ctx, token)
	if c == nil || !c.Has(perm.CircDesk) || c.UserID != u.ID || c.SessionID != hashToken(token) {
		t.Fatalf("caller %+v", c)
	}
	e.users.SetPermission(ctx, u.ID, perm.CircDesk, false)
	if c, _ := e.svc.Authenticate(ctx, token); c == nil || c.Has(perm.CircDesk) {
		t.Fatalf("a switched-off permission is still there: %+v", c)
	}
	e.st.DB().Exec(`UPDATE users SET active = 0 WHERE id = ?`, u.ID)
	if c, _ := e.svc.Authenticate(ctx, token); c != nil {
		t.Fatal("a deactivated user still has a caller")
	}
	e.st.DB().Exec(`UPDATE users SET active = 1, must_change_password = 1 WHERE id = ?`, u.ID)
	if c, _ := e.svc.Authenticate(ctx, token); c == nil || !c.MustChangePassword {
		t.Fatalf("must-change not seen: %+v", c)
	}
}

func TestUnknownTokenHasNoCaller(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"", "nope", strings.Repeat("A", 43)} {
		if c, err := e.svc.Authenticate(ctx, tok); c != nil || err != nil {
			t.Errorf("%q: %v %v", tok, c, err)
		}
	}
}

func TestSignInRefusals(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "Ann", "correct horse battery", perm.Patron)
	e.addUser(t, "nopass", "", perm.Patron) // an account made with an empty hash
	off := e.addUser(t, "off", "correct horse battery", perm.Patron)
	e.st.DB().Exec(`UPDATE users SET active = 0 WHERE id = ?`, off.ID)

	if _, _, err := e.svc.SignIn(ctx, " aNN ", "correct horse battery"); err != nil {
		t.Fatalf("case-insensitive, trimmed username: %v", err)
	}
	generic := map[string]error{}
	for name, c := range map[string][2]string{
		"wrong password":     {"ann", "wrong horse battery"},
		"unknown user":       {"nobody", "correct horse battery"},
		"empty hash, empty":  {"nopass", ""},
		"empty hash, a word": {"nopass", "correct horse battery"},
		"deactivated, wrong": {"off", "wrong horse battery"},
		"over-long password": {"ann", strings.Repeat("a", 300)},
	} {
		_, _, err := e.svc.SignIn(ctx, c[0], c[1])
		wantCode(t, err, apperr.Unauthorized)
		generic[name] = err
	}
	for name, err := range generic {
		if err.Error() != generic["wrong password"].Error() {
			t.Errorf("%s says %q, not the one generic message", name, err)
		}
	}
	// "deactivated" only after the right password
	_, _, err := e.svc.SignIn(ctx, "off", "correct horse battery")
	wantCode(t, err, apperr.Forbidden)
	if !strings.Contains(err.Error(), "deactivated") {
		t.Fatalf("message %q", err)
	}
	var n int
	e.st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, off.ID).Scan(&n)
	if n != 0 {
		t.Fatal("a session was made for a deactivated account")
	}
}

func TestSignInRehashesAnOldHash(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "ann", "", perm.Patron)
	old := hashWith("correct horse battery", 8192, 1, 1)
	e.st.DB().Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, old, u.ID)
	if _, _, err := e.svc.SignIn(ctx, "ann", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	cred, _, _ := e.users.CredentialsByID(ctx, u.ID)
	if cred.PasswordHash == old || !strings.HasPrefix(cred.PasswordHash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash not updated: %q", cred.PasswordHash)
	}
	if _, _, err := e.svc.SignIn(ctx, "ann", "correct horse battery"); err != nil {
		t.Fatalf("the rehashed password no longer works: %v", err)
	}
}

func TestEndSessions(t *testing.T) {
	e := newEnv(t)
	a := e.addUser(t, "ann", "correct horse battery", perm.Patron)
	b := e.addUser(t, "bob", "correct horse battery", perm.Patron)
	t1, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	t2, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	tb, _, _ := e.svc.SignIn(ctx, "bob", "correct horse battery")
	live := func(tok string) bool { c, _ := e.svc.Authenticate(ctx, tok); return c != nil }

	err := e.st.WithTx(ctx, func(tx store.Tx) error { return e.svc.EndOthersTx(ctx, tx, a.ID, hashToken(t1)) })
	if err != nil || !live(t1) || live(t2) || !live(tb) {
		t.Fatalf("others ended: t1 %v t2 %v bob %v err %v", live(t1), live(t2), live(tb), err)
	}
	if err := e.svc.EndAll(ctx, a.ID); err != nil || live(t1) || !live(tb) {
		t.Fatalf("all ended: t1 %v bob %v err %v", live(t1), live(tb), err)
	}
	e.svc.SignOut(ctx, hashToken(tb))
	if live(tb) {
		t.Fatal("signed out, still live")
	}
	_ = b
}

func TestExpiredSessionsAreSweptAtSignIn(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "correct horse battery", perm.Patron)
	e.svc.SignIn(ctx, "ann", "correct horse battery")
	e.now = e.now.Add(9 * time.Hour)
	e.svc.SignIn(ctx, "ann", "correct horse battery")
	var n int
	e.st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d sessions, want only the new one", n)
	}
}

// ACCESS-6.
func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "ann", "correct horse battery", perm.Patron)
	e.st.DB().Exec(`UPDATE users SET must_change_password = 1 WHERE id = ?`, u.ID)
	t1, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	t2, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")

	wantInvalid(t, e.svc.ChangePassword(ctx, u.ID, hashToken(t1), "wrong wrong wrong", "brand new password"), "current_password")
	wantInvalid(t, e.svc.ChangePassword(ctx, u.ID, hashToken(t1), "correct horse battery", "short"), "new_password")
	wantInvalid(t, e.svc.ChangePassword(ctx, u.ID, hashToken(t1), "correct horse battery", "correct horse battery"), "new_password")
	if _, _, err := e.svc.SignIn(ctx, "ann", "correct horse battery"); err != nil {
		t.Fatalf("a refused change altered the password: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, u.ID, hashToken(t1), "correct horse battery", "brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.SignIn(ctx, "ann", "correct horse battery"); err == nil {
		t.Fatal("the old password still works")
	}
	if _, _, err := e.svc.SignIn(ctx, "ann", "brand new password"); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.svc.Authenticate(ctx, t1); c == nil || c.MustChangePassword {
		t.Fatalf("the session that changed it: %+v", c)
	}
	if c, _ := e.svc.Authenticate(ctx, t2); c != nil {
		t.Fatal("another session survived the change")
	}
}

// reset does what an administrator's reset writes: a new hash that must be
// changed, and no sessions left.
func (e *env) reset(t *testing.T, userID int64, temp string) {
	t.Helper()
	hash, err := e.svc.Passwords.Hash(ctx, "password", temp)
	if err != nil {
		t.Fatal(err)
	}
	err = e.st.WithTx(ctx, func(tx store.Tx) error {
		if err := e.users.SetPasswordHashTx(ctx, tx, userID, hash, true); err != nil {
			return err
		}
		return e.svc.EndAllTx(ctx, tx, userID)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordOverlappingAResetIsRefusedAndKeepsTheReset(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "ann", "correct horse battery", perm.Patron)
	tok, _, _ := e.svc.SignIn(ctx, "ann", "correct horse battery")
	e.svc.afterCredentialCheck = func() { e.reset(t, u.ID, "temporary-password") }

	err := e.svc.ChangePassword(ctx, u.ID, hashToken(tok), "correct horse battery", "brand new password")
	wantCode(t, err, apperr.Conflict)
	e.svc.afterCredentialCheck = nil

	cred, _, _ := e.users.CredentialsByID(ctx, u.ID)
	if !cred.MustChangePassword {
		t.Fatal("the stale change cleared the forced password change")
	}
	if _, _, err := e.svc.SignIn(ctx, "ann", "brand new password"); err == nil {
		t.Fatal("the stale change's password was stored over the reset")
	}
	if _, _, err := e.svc.SignIn(ctx, "ann", "temporary-password"); err != nil {
		t.Fatalf("the temporary password no longer works: %v", err)
	}
}

func TestSignInOverlappingAResetStartsNoSession(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "ann", "correct horse battery", perm.Patron)
	e.svc.afterCredentialCheck = func() { e.reset(t, u.ID, "temporary-password") }

	token, _, err := e.svc.SignIn(ctx, "ann", "correct horse battery")
	wantCode(t, err, apperr.Unauthorized)
	e.svc.afterCredentialCheck = nil
	if token != "" {
		t.Fatal("a token was returned")
	}
	var n int
	e.st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, u.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d session(s) survive the reset", n)
	}
}

func TestSignInOverlappingADeactivationStartsNoSession(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "ann", "correct horse battery", perm.Patron)
	e.svc.afterCredentialCheck = func() { e.st.DB().Exec(`UPDATE users SET active = 0 WHERE id = ?`, u.ID) }
	_, _, err := e.svc.SignIn(ctx, "ann", "correct horse battery")
	wantCode(t, err, apperr.Forbidden)
}

func TestRehashOverlappingAResetKeepsTheReset(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "ann", "", perm.Patron)
	e.st.DB().Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hashWith("correct horse battery", 8192, 1, 1), u.ID)
	e.svc.afterCredentialCheck = func() { e.reset(t, u.ID, "temporary-password") }
	if _, _, err := e.svc.SignIn(ctx, "ann", "correct horse battery"); err == nil {
		t.Fatal("signed in over a reset")
	}
	e.svc.afterCredentialCheck = nil
	if _, _, err := e.svc.SignIn(ctx, "ann", "temporary-password"); err != nil {
		t.Fatalf("the rehash overwrote the reset: %v", err)
	}
}

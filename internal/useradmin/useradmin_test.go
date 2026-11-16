package useradmin

import (
	"context"
	"strings"
	"testing"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

var ctx = context.Background()

type env struct {
	st    *store.Store
	users *users.Service
	svc   *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u := users.New(st)
	a := auth.NewService(st, u, nil, nil)
	return &env{st: st, users: u, svc: New(st, u, a)}
}

func (e *env) add(t *testing.T, name string, role perm.Role) users.User {
	t.Helper()
	u, err := e.users.Create(ctx, users.NewUser{Username: name, DisplayName: name, PasswordHash: "x", Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func wantCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	if e, ok := apperr.As(err); !ok || e.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
}

// The service refuses these itself, whatever called it (security design §4).
func TestServiceEnforcesNoEscalation(t *testing.T) {
	e := newEnv(t)
	adm := e.add(t, "ada", perm.Administrator)
	e.add(t, "bo", perm.Administrator)
	lib := e.add(t, "lib", perm.Librarian)
	e.users.SetPermission(ctx, lib.ID, perm.UsersManage, true)
	e.users.SetPermission(ctx, lib.ID, perm.UsersPermissions, true)
	pat := e.add(t, "pat", perm.Patron)

	_, err := e.svc.ChangeRole(ctx, lib.ID, pat.ID, perm.Administrator)
	wantCode(t, err, apperr.Forbidden)
	_, err = e.svc.SetPermission(ctx, lib.ID, pat.ID, perm.SettingsManage, true)
	wantCode(t, err, apperr.Forbidden)
	_, err = e.svc.Deactivate(ctx, lib.ID, adm.ID)
	wantCode(t, err, apperr.Forbidden)
	_, _, err = e.svc.ResetPassword(ctx, lib.ID, adm.ID)
	wantCode(t, err, apperr.Forbidden)
	_, err = e.svc.SetPermission(ctx, pat.ID, pat.ID, perm.ReviewsWrite, false)
	wantCode(t, err, apperr.Forbidden) // a patron has no users.permissions

	if u, _ := e.users.Get(ctx, adm.ID); !u.Active {
		t.Fatal("the administrator was deactivated")
	}
	var n int
	e.st.DB().QueryRow(`SELECT count(*) FROM activity`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d refused changes were logged", n)
	}
}

// An actor who has been deactivated or doesn't exist can do nothing, even with a stale caller.
func TestInactiveOrUnknownActorIsRefused(t *testing.T) {
	e := newEnv(t)
	adm := e.add(t, "ada", perm.Administrator)
	e.add(t, "bo", perm.Administrator)
	pat := e.add(t, "pat", perm.Patron)
	if _, err := e.svc.Deactivate(ctx, e.add(t, "cy", perm.Administrator).ID, adm.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Deactivate(ctx, adm.ID, pat.ID)
	wantCode(t, err, apperr.Forbidden)
	_, err = e.svc.Deactivate(ctx, 999, pat.ID)
	wantCode(t, err, apperr.Forbidden)
}

func TestTempPasswordsAreSixteenReadableCharactersAndDiffer(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := NewTempPassword()
		if err != nil || len(p) != TempPasswordLength {
			t.Fatalf("%q %v", p, err)
		}
		for _, r := range p {
			if !strings.ContainsRune(crockford, r) {
				t.Fatalf("%q has %q", p, r)
			}
		}
		if seen[p] {
			t.Fatalf("%q twice", p)
		}
		seen[p] = true
	}
}

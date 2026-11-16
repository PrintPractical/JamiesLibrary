// Package recovery is what the owner does at the server when the only
// administrator cannot sign in (DEPLOY-4, security design §6). It works on the
// data directory directly, with no network route, so whoever can run it
// already controls the data.
package recovery

import (
	"context"
	"fmt"
	"time"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

// Result is what a reset did.
type Result struct {
	Username          string
	TemporaryPassword string
	// WasAdministrator is whether the account held both manage-users permissions before the reset.
	WasAdministrator bool
	// MadeAdministrator is whether the administrator preset was applied.
	MadeAdministrator bool
	// Inactive is true for a deactivated account: the reset doesn't reactivate it.
	Inactive bool
}

// ResetPassword gives the user a new temporary password that must be changed
// at the next sign-in, ends their sessions, and records it in the activity log
// as the server console's. With makeAdmin it also applies the administrator
// preset, for an owner whose administrator was demoted. The data directory is
// opened without its lock, so it works beside a running server.
func ResetPassword(ctx context.Context, dataDir, username string, makeAdmin bool) (Result, error) {
	st, err := store.Open(ctx, dataDir, store.Options{NoLock: true, MustExist: true})
	if err != nil {
		return Result{}, err
	}
	defer st.Close()
	us := users.New(st)
	as := auth.NewService(st, us, func() time.Duration { return time.Hour }, nil)

	cred, found, err := us.CredentialsByUsername(ctx, username)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("there is no user named %q", username)
	}
	u, err := us.Get(ctx, cred.ID)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		Username:         u.Username,
		WasAdministrator: u.Permissions.Has(perm.UsersManage) && u.Permissions.Has(perm.UsersPermissions),
		Inactive:         !u.Active,
	}
	if res.TemporaryPassword, err = auth.TemporaryPassword(); err != nil {
		return Result{}, err
	}
	hash, err := as.Passwords.Hash(ctx, "password", res.TemporaryPassword)
	if err != nil {
		return Result{}, err
	}
	target := fmt.Sprintf("user:%d", u.ID)
	err = st.WithTx(ctx, func(tx store.Tx) error {
		if err := us.SetPasswordHashTx(ctx, tx, u.ID, hash, true); err != nil {
			return err
		}
		if err := as.EndAllTx(ctx, tx, u.ID); err != nil {
			return err
		}
		if err := activity.Record(ctx, tx, activity.ServerConsole, activity.UserPasswordReset, target, nil); err != nil {
			return err
		}
		if !makeAdmin {
			return nil
		}
		if err := us.AssignRoleTx(ctx, tx, u.ID, perm.Administrator); err != nil {
			return err
		}
		res.MadeAdministrator = true
		return activity.Record(ctx, tx, activity.ServerConsole, activity.UserRoleChanged, target,
			map[string]any{"from": string(u.Role), "to": string(perm.Administrator)})
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

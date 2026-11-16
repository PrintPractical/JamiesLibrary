// Package useradmin is what an administrator does to other people's accounts:
// change a role, switch a permission, deactivate or reactivate, reset a
// password (ADMIN-2, ACCESS-7). Every rule of security design §4 is checked
// here, inside the transaction of the change, never only in a handler: the
// actor may grant only what they hold, may act only on someone whose
// permissions are within theirs, and the last user who can manage users and
// permissions can't lose either or be deactivated. Each change is logged
// (ADMIN-4) and ends the target's sessions when it takes something away.
package useradmin

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

// TempPasswordLength is the length of a temporary password (security design §2).
const TempPasswordLength = 16

// crockford is the base32 alphabet without I, L, O and U, easy to read out loud.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Service makes the changes.
type Service struct {
	st    *store.Store
	users *users.Service
	auth  *auth.Service
}

// New makes a Service. auth hashes temporary passwords and ends sessions.
func New(st *store.Store, u *users.Service, a *auth.Service) *Service {
	return &Service{st: st, users: u, auth: a}
}

var (
	errNotAllowed = apperr.New(apperr.Forbidden, "You don't have permission to do that.")
	errAbove      = apperr.New(apperr.Forbidden, "That person has permissions you don't have, so you can't change their account.")
	errLastAdmin  = apperr.New(apperr.Conflict, "That would leave nobody able to manage users and their permissions. Make someone else an administrator first.")
)

// plan is one change: what it writes, what the user is after it, and what to log.
type plan struct {
	action      string
	details     map[string]any
	write       func() error
	after       perm.Set
	afterActive bool
	endSessions bool
	noop        bool // already so: nothing is written, ended or logged
}

// change runs one change on target by actor, with every check and its writes in
// one transaction. need is the permission the actor must hold for this kind of change.
func (s *Service) change(ctx context.Context, actorID, targetID int64, need perm.Permission,
	prepare func(tx store.Tx, actor, target users.User) (plan, error)) (users.User, error) {
	var out users.User
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		actor, err := s.users.GetTx(ctx, tx, actorID)
		if err != nil {
			if isNotFound(err) {
				return errNotAllowed
			}
			return err
		}
		if !actor.Active || !actor.Permissions.Has(need) {
			return errNotAllowed
		}
		target, err := s.users.GetTx(ctx, tx, targetID)
		if err != nil {
			return err
		}
		if !target.Permissions.SubsetOf(actor.Permissions) {
			return errAbove
		}
		p, err := prepare(tx, actor, target)
		if err != nil {
			return err
		}
		if p.noop {
			out = target
			return nil
		}
		if err := p.write(); err != nil {
			return err
		}
		if isManager(target.Permissions, target.Active) && !isManager(p.after, p.afterActive) {
			n, err := s.users.ActiveManagersTx(ctx, tx)
			if err != nil {
				return err
			}
			if n == 0 {
				return errLastAdmin
			}
		}
		if p.endSessions {
			if err := s.auth.EndAllTx(ctx, tx, targetID); err != nil {
				return err
			}
		}
		if err := activity.Record(ctx, tx, activity.ByUser(actorID), p.action, targetRef(targetID), p.details); err != nil {
			return err
		}
		out, err = s.users.GetTx(ctx, tx, targetID)
		return err
	})
	return out, err
}

func isManager(set perm.Set, active bool) bool {
	return active && set.Has(perm.UsersManage) && set.Has(perm.UsersPermissions)
}

func isNotFound(err error) bool {
	var e *apperr.Error
	return errors.As(err, &e) && e.Code == apperr.NotFound
}

func targetRef(id int64) string { return "user:" + strconv.FormatInt(id, 10) }

// ChangeRole gives target the role and replaces their permissions with its
// preset. The whole preset must be within the actor's own permissions.
func (s *Service) ChangeRole(ctx context.Context, actorID, targetID int64, role perm.Role) (users.User, error) {
	if !perm.ValidRole(role) {
		return users.User{}, apperr.OnField(apperr.Invalid, "role", "Choose patron, librarian or administrator.")
	}
	return s.change(ctx, actorID, targetID, perm.UsersPermissions, func(tx store.Tx, actor, target users.User) (plan, error) {
		preset := perm.Preset(role)
		if !preset.SubsetOf(actor.Permissions) {
			return plan{}, apperr.OnField(apperr.Forbidden, "role", "You can't give a role with permissions you don't have yourself.")
		}
		lost := target.Permissions.Minus(preset)
		return plan{
			action: activity.UserRoleChanged,
			details: map[string]any{
				"from": string(target.Role), "to": string(role),
				"added": perm.Strings(preset.Minus(target.Permissions).List()), "removed": perm.Strings(lost.List()),
			},
			write:       func() error { return s.users.SetRoleTx(ctx, tx, target.ID, role) },
			after:       preset,
			afterActive: target.Active,
			endSessions: len(lost) > 0,
		}, nil
	})
}

// SetPermission switches one permission of target on or off. Switching on needs
// the actor to hold it. The role stays, so the user may now differ from its preset.
func (s *Service) SetPermission(ctx context.Context, actorID, targetID int64, p perm.Permission, on bool) (users.User, error) {
	if !perm.Valid(p) {
		return users.User{}, apperr.OnField(apperr.Invalid, "permission", "That is not a permission.")
	}
	return s.change(ctx, actorID, targetID, perm.UsersPermissions, func(tx store.Tx, actor, target users.User) (plan, error) {
		if on && !actor.Permissions.Has(p) {
			return plan{}, apperr.OnField(apperr.Forbidden, "permission", "You can't give a permission you don't have yourself.")
		}
		if target.Permissions.Has(p) == on {
			return plan{noop: true}, nil
		}
		after := perm.NewSet(target.Permissions.List()...)
		if on {
			after[p] = struct{}{}
		} else {
			delete(after, p)
		}
		return plan{
			action:      activity.UserPermissionChanged,
			details:     map[string]any{"permission": string(p), "on": on, "role": string(target.Role)},
			write:       func() error { return s.users.SetPermissionTx(ctx, tx, target.ID, p, on) },
			after:       after,
			afterActive: target.Active,
			endSessions: !on,
		}, nil
	})
}

// Deactivate stops target signing in and ends their sessions. Their loans are untouched.
func (s *Service) Deactivate(ctx context.Context, actorID, targetID int64) (users.User, error) {
	return s.setActive(ctx, actorID, targetID, false)
}

// Reactivate lets target sign in again.
func (s *Service) Reactivate(ctx context.Context, actorID, targetID int64) (users.User, error) {
	return s.setActive(ctx, actorID, targetID, true)
}

func (s *Service) setActive(ctx context.Context, actorID, targetID int64, active bool) (users.User, error) {
	action := activity.UserReactivated
	if !active {
		action = activity.UserDeactivated
	}
	return s.change(ctx, actorID, targetID, perm.UsersManage, func(tx store.Tx, actor, target users.User) (plan, error) {
		if target.Active == active {
			return plan{noop: true}, nil
		}
		return plan{
			action:      action,
			write:       func() error { return s.users.SetActiveTx(ctx, tx, target.ID, active) },
			after:       target.Permissions,
			afterActive: active,
			endSessions: !active,
		}, nil
	})
}

// ResetPassword replaces target's password by a random temporary one that they
// must change at their next sign-in, ends their sessions and returns the
// temporary password. It is shown once: only its hash is kept.
func (s *Service) ResetPassword(ctx context.Context, actorID, targetID int64) (users.User, string, error) {
	temp, err := NewTempPassword()
	if err != nil {
		return users.User{}, "", err
	}
	hash, err := s.auth.Passwords.Hash(ctx, "password", temp)
	if err != nil {
		return users.User{}, "", err
	}
	u, err := s.change(ctx, actorID, targetID, perm.UsersManage, func(tx store.Tx, actor, target users.User) (plan, error) {
		return plan{
			action:      activity.UserPasswordReset,
			write:       func() error { return s.users.SetPasswordHashTx(ctx, tx, target.ID, hash, true) },
			after:       target.Permissions,
			afterActive: target.Active,
			endSessions: true,
		}, nil
	})
	if err != nil {
		return users.User{}, "", err
	}
	return u, temp, nil
}

// NewTempPassword returns TempPasswordLength characters of Crockford base32
// from crypto/rand (80 bits).
func NewTempPassword() (string, error) {
	b := make([]byte, TempPasswordLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = crockford[b[i]&31]
	}
	return string(b), nil
}

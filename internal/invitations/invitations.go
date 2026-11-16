// Package invitations owns the invitation codes and registration with one
// (ACCESS-2, ACCESS-3, security design §5). A code gives an account a role's
// preset and no permission its creator lacks, checked when the code is made and
// again when it is used.
package invitations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

// Page sizes of List.
const (
	DefaultPageSize = 50
	MaxPageSize     = 100
)

// maxDraws bounds the redraws when a new code is already taken, which at 80 bits never happens.
const maxDraws = 5

// ErrInvalidCode is what every wrong, used, revoked or no-longer-valid code
// gets, so the answer says nothing about which it was. The caller counts it
// against the registration limit.
var ErrInvalidCode = apperr.OnField(apperr.Invalid, "code", "That invitation code isn't valid. Check it, or ask for a new one.")

var errNotAllowed = apperr.New(apperr.Forbidden, "You don't have permission to do that.")

// Service makes, lists and revokes invitations and registers accounts with them.
type Service struct {
	st    *store.Store
	users *users.Service
	auth  *auth.Service
}

// New makes a Service. auth hashes passwords and starts the new user's session.
func New(st *store.Store, u *users.Service, a *auth.Service) *Service {
	return &Service{st: st, users: u, auth: a}
}

// Invitation is one code and what became of it.
type Invitation struct {
	ID        int64
	Code      string
	Role      perm.Role
	CreatorID int64
	CreatedAt time.Time
	UsedBy    int64 // 0 while unused
	UsedAt    time.Time
	RevokedAt time.Time
}

// Status is "used", "revoked" or "unused".
func (i Invitation) Status() string {
	switch {
	case !i.UsedAt.IsZero():
		return "used"
	case !i.RevokedAt.IsZero():
		return "revoked"
	}
	return "unused"
}

func target(id int64) string { return "invitation:" + strconv.FormatInt(id, 10) }

// Create makes a code for role. The actor must hold invitations.manage and the
// whole preset of the role: no code grants what its creator doesn't have.
func (s *Service) Create(ctx context.Context, actorID int64, role perm.Role) (Invitation, error) {
	if !perm.ValidRole(role) {
		return Invitation{}, apperr.OnField(apperr.Invalid, "role", "Choose patron, librarian or administrator.")
	}
	var id int64
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		actor, err := s.users.GetTx(ctx, tx, actorID)
		if err != nil || !actor.Active || !actor.Permissions.Has(perm.InvitationsManage) {
			if err != nil && !isNotFound(err) {
				return err
			}
			return errNotAllowed
		}
		if !perm.Preset(role).SubsetOf(actor.Permissions) {
			return apperr.OnField(apperr.Forbidden, "role", "You can only invite someone to a role whose permissions you hold yourself.")
		}
		now := clock.Now().UTC().Format(time.RFC3339)
		for i := 0; i < maxDraws && id == 0; i++ {
			code, err := NewCode()
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO invitations (code, role, creator_id, created_at) VALUES (?, ?, ?, ?)`,
				code, string(role), actorID, now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				if id, err = res.LastInsertId(); err != nil {
					return err
				}
			}
		}
		if id == 0 {
			return errors.New("invitations: could not draw an unused code")
		}
		// the code itself is never logged (security design §5)
		return activity.Record(ctx, tx, activity.ByUser(actorID), activity.InvitationCreated, target(id),
			map[string]any{"role": string(role), "creator": actorID})
	})
	if err != nil {
		return Invitation{}, err
	}
	return s.get(ctx, s.st.DB(), id)
}

// Revoke ends an unused code. Revoking a revoked one changes nothing; a used one can't be revoked.
func (s *Service) Revoke(ctx context.Context, actorID, id int64) (Invitation, error) {
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		actor, err := s.users.GetTx(ctx, tx, actorID)
		if err != nil || !actor.Active || !actor.Permissions.Has(perm.InvitationsManage) {
			if err != nil && !isNotFound(err) {
				return err
			}
			return errNotAllowed
		}
		inv, err := s.get(ctx, tx, id)
		if err != nil {
			return err
		}
		switch inv.Status() {
		case "revoked":
			return nil
		case "used":
			return apperr.New(apperr.Conflict, "That code has already been used, so it can't be revoked.")
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE invitations SET revoked_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL`,
			clock.Now().UTC().Format(time.RFC3339), id); err != nil {
			return err
		}
		return activity.Record(ctx, tx, activity.ByUser(actorID), activity.InvitationRevoked, target(id),
			map[string]any{"role": string(inv.Role), "creator": inv.CreatorID})
	})
	if err != nil {
		return Invitation{}, err
	}
	return s.get(ctx, s.st.DB(), id)
}

// Page is one page of invitations, newest first.
type Page struct {
	Invitations []Invitation
	Page        int
	PageSize    int
	Total       int
}

// List returns page number page (from 1), newest first. A size of 0 means DefaultPageSize.
func (s *Service) List(ctx context.Context, page, size int) (Page, error) {
	if size == 0 {
		size = DefaultPageSize
	}
	if size < 1 || size > MaxPageSize {
		return Page{}, apperr.OnField(apperr.Invalid, "per_page", fmt.Sprintf("Ask for between 1 and %d invitations per page.", MaxPageSize))
	}
	if page < 1 || page > 1_000_000 {
		return Page{}, apperr.OnField(apperr.Invalid, "page", "The page number is from 1 to 1000000.")
	}
	out := Page{Invitations: []Invitation{}, Page: page, PageSize: size}
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM invitations`).Scan(&out.Total); err != nil {
		return Page{}, fmt.Errorf("counting invitations: %w", err)
	}
	rows, err := s.st.DB().QueryContext(ctx, selectInvitation+` ORDER BY id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return Page{}, fmt.Errorf("listing invitations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		inv, err := scan(rows)
		if err != nil {
			return Page{}, err
		}
		out.Invitations = append(out.Invitations, inv)
	}
	return out, rows.Err()
}

const selectInvitation = `SELECT id, code, role, creator_id, created_at, used_by, used_at, revoked_at FROM invitations`

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (Invitation, error) {
	var (
		inv               Invitation
		role, created     string
		usedBy            sql.NullInt64
		usedAt, revokedAt sql.NullString
	)
	if err := r.Scan(&inv.ID, &inv.Code, &role, &inv.CreatorID, &created, &usedBy, &usedAt, &revokedAt); err != nil {
		return Invitation{}, err
	}
	inv.Role = perm.Role(role)
	inv.CreatedAt, _ = time.Parse(time.RFC3339, created)
	inv.UsedBy = usedBy.Int64
	if usedAt.Valid {
		inv.UsedAt, _ = time.Parse(time.RFC3339, usedAt.String)
	}
	if revokedAt.Valid {
		inv.RevokedAt, _ = time.Parse(time.RFC3339, revokedAt.String)
	}
	return inv, nil
}

func (s *Service) get(ctx context.Context, q querier, id int64) (Invitation, error) {
	inv, err := scan(q.QueryRowContext(ctx, selectInvitation+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, apperr.New(apperr.NotFound, "There is no such invitation.")
	}
	return inv, err
}

func isNotFound(err error) bool {
	e, ok := apperr.As(err)
	return ok && e.Code == apperr.NotFound
}

// Registration is what someone gives to make an account.
type Registration struct {
	Code        string
	Username    string
	DisplayName string
	Password    string
}

// redeemable finds the invitation for a normalised code and checks that it can
// be used now: unused, unrevoked, and its creator still active and still
// holding every permission of its role. Every failure is ErrInvalidCode.
func (s *Service) redeemable(ctx context.Context, q querier, get func(int64) (users.User, error), code string) (Invitation, error) {
	inv, err := scan(q.QueryRowContext(ctx, selectInvitation+` WHERE code = ?`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrInvalidCode
	}
	if err != nil {
		return Invitation{}, err
	}
	if inv.Status() != "unused" {
		return Invitation{}, ErrInvalidCode
	}
	creator, err := get(inv.CreatorID)
	if err != nil {
		if isNotFound(err) {
			return Invitation{}, ErrInvalidCode
		}
		return Invitation{}, err
	}
	if !creator.Active || !perm.Preset(inv.Role).SubsetOf(creator.Permissions) {
		return Invitation{}, ErrInvalidCode
	}
	return inv, nil
}

// Register makes an account with an invitation code and starts its session,
// returning the new user's ID and the session token. A code that is wrong, used,
// revoked or no longer valid gives ErrInvalidCode and nothing else is said or
// done. The code is checked first, so what is wrong with the rest of the
// registration is told only to someone holding a usable code.
func (s *Service) Register(ctx context.Context, in Registration) (userID int64, token string, err error) {
	code, ok := Normalise(in.Code)
	if !ok {
		return 0, "", ErrInvalidCode
	}
	// A cheap read first, so a wrong code costs no password hashing.
	if _, err := s.redeemable(ctx, s.st.DB(), func(id int64) (users.User, error) { return s.users.Get(ctx, id) }, code); err != nil {
		return 0, "", err
	}
	if in.Username, in.DisplayName, err = users.CheckNames(in.Username, in.DisplayName); err != nil {
		return 0, "", err
	}
	hash, err := s.auth.Passwords.Hash(ctx, "password", in.Password)
	if err != nil {
		return 0, "", err
	}
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		inv, err := s.redeemable(ctx, tx, func(id int64) (users.User, error) { return s.users.GetTx(ctx, tx, id) }, code)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE invitations SET used_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL`,
			clock.Now().UTC().Format(time.RFC3339), inv.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrInvalidCode
		}
		userID, err = s.users.CreateTx(ctx, tx, users.NewUser{
			Username: in.Username, DisplayName: in.DisplayName, PasswordHash: hash, Role: inv.Role,
		})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE invitations SET used_by = ? WHERE id = ?`, userID, inv.ID)
		return err
	})
	if err != nil {
		return 0, "", err
	}
	token, err = s.auth.StartSession(ctx, userID)
	if err != nil {
		return 0, "", err
	}
	return userID, token, nil
}

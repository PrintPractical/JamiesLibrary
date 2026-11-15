// Package users owns accounts and their permissions: the users and
// user_permissions tables. A user's permissions are stored as an effective
// set; the role is the preset applied last (ROLE-1, ROLE-2).
package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

// Limits on the text of an account.
const (
	MaxUsernameLength    = 64
	MaxDisplayNameLength = 80
)

// Service reads and writes accounts.
type Service struct {
	st  *store.Store
	now func() time.Time
}

// New makes a Service over st.
func New(st *store.Store) *Service { return &Service{st: st, now: clock.Now} }

// User is an account with its permissions.
type User struct {
	ID                 int64
	Username           string
	DisplayName        string
	MustChangePassword bool
	Role               perm.Role
	Active             bool
	CreatedAt          time.Time
	Permissions        perm.Set
}

// PermissionState is one permission and whether the user has it.
type PermissionState struct {
	Permission perm.Permission
	On         bool
}

// PermissionStates lists all 16 permissions, each on or off for the user
// (ROLE-1, "Permissions shown").
func (u User) PermissionStates() []PermissionState {
	out := make([]PermissionState, len(perm.All))
	for i, p := range perm.All {
		out[i] = PermissionState{Permission: p, On: u.Permissions.Has(p)}
	}
	return out
}

// DiffFromPreset is how the user's permissions differ from their role's preset.
func (u User) DiffFromPreset() perm.Diff { return perm.DiffFromPreset(u.Permissions, u.Role) }

// NewUser describes an account to create.
type NewUser struct {
	Username     string
	DisplayName  string
	PasswordHash string
	Role         perm.Role
	// MustChangePassword makes the user choose a password before anything else.
	MustChangePassword bool
}

func invalid(field, msg string) error { return apperr.OnField(apperr.Invalid, field, msg) }

// Create adds an account with exactly the permissions of its role's preset.
func (s *Service) Create(ctx context.Context, n NewUser) (User, error) {
	n.Username = strings.TrimSpace(n.Username)
	n.DisplayName = strings.TrimSpace(n.DisplayName)
	switch {
	case n.Username == "":
		return User{}, invalid("username", "Enter a username.")
	case len([]rune(n.Username)) > MaxUsernameLength:
		return User{}, invalid("username", "That username is too long.")
	case n.DisplayName == "":
		return User{}, invalid("display_name", "Enter a name.")
	case len([]rune(n.DisplayName)) > MaxDisplayNameLength:
		return User{}, invalid("display_name", "That name is too long.")
	case !perm.ValidRole(n.Role):
		return User{}, invalid("role", "Choose patron, librarian or administrator.")
	}
	var id int64
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (username, display_name, password_hash, must_change_password, role, active, created_at)
			 VALUES (?, ?, ?, ?, ?, 1, ?)`,
			n.Username, n.DisplayName, n.PasswordHash, n.MustChangePassword, string(n.Role),
			s.now().UTC().Format(time.RFC3339))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return apperr.OnField(apperr.Conflict, "username", "That username is taken.")
			}
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return setPermissions(ctx, tx, id, perm.Preset(n.Role))
	})
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, id)
}

// AssignRole gives the user the role and replaces their permissions with its preset.
func (s *Service) AssignRole(ctx context.Context, userID int64, role perm.Role) (User, error) {
	if !perm.ValidRole(role) {
		return User{}, invalid("role", "Choose patron, librarian or administrator.")
	}
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, string(role), userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNotFound
		}
		return setPermissions(ctx, tx, userID, perm.Preset(role))
	})
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, userID)
}

// SetPermission switches one permission on or off. The role stays as it was.
func (s *Service) SetPermission(ctx context.Context, userID int64, p perm.Permission, on bool) (User, error) {
	if !perm.Valid(p) {
		return User{}, invalid("permission", "That is not a permission.")
	}
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNotFound
			}
			return err
		}
		q := `DELETE FROM user_permissions WHERE user_id = ? AND permission = ?`
		if on {
			q = `INSERT OR IGNORE INTO user_permissions (user_id, permission) VALUES (?, ?)`
		}
		_, err := tx.ExecContext(ctx, q, userID, string(p))
		return err
	})
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, userID)
}

var errNotFound = apperr.New(apperr.NotFound, "There is no such user.")

func setPermissions(ctx context.Context, tx store.Tx, userID int64, set perm.Set) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_permissions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, p := range set.List() {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_permissions (user_id, permission) VALUES (?, ?)`, userID, string(p)); err != nil {
			return err
		}
	}
	return nil
}

// Get returns the user with their permissions.
func (s *Service) Get(ctx context.Context, id int64) (User, error) {
	var (
		u       User
		role    string
		created string
	)
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id, username, display_name, must_change_password, role, active, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.MustChangePassword, &role, &u.Active, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.Role = perm.Role(role)
	u.CreatedAt, _ = time.Parse(time.RFC3339, created)
	rows, err := s.st.DB().QueryContext(ctx, `SELECT permission FROM user_permissions WHERE user_id = ?`, id)
	if err != nil {
		return User{}, err
	}
	defer rows.Close()
	u.Permissions = perm.Set{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return User{}, err
		}
		// an ID this version doesn't know grants nothing
		if perm.Valid(perm.Permission(p)) {
			u.Permissions[perm.Permission(p)] = struct{}{}
		}
	}
	return u, rows.Err()
}

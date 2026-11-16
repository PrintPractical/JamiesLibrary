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
	"unicode"
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

// Limits on the text of an account.
const (
	MaxUsernameLength    = 64
	MaxDisplayNameLength = 64
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

// checkUsername trims name and refuses one that is empty, over the limit, or
// holds whitespace, control or format characters, so no two accounts can look
// the same to staff (security design §8).
func checkUsername(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("username", "Enter a username.")
	}
	if utf8.RuneCountInString(name) > MaxUsernameLength {
		return "", invalid("username", "That username is too long.")
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.Is(unicode.Zs, r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", invalid("username", "A username can't contain spaces or hidden characters.")
		}
	}
	return name, nil
}

// checkDisplayName trims name and refuses one that is empty, over the limit, or
// holds control characters or format characters (which include the bidi
// controls that make a name read backwards).
func checkDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("display_name", "Enter a name.")
	}
	if utf8.RuneCountInString(name) > MaxDisplayNameLength {
		return "", invalid("display_name", "That name is too long.")
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", invalid("display_name", "A name can't contain hidden or control characters.")
		}
	}
	return name, nil
}

func invalid(field, msg string) error { return apperr.OnField(apperr.Invalid, field, msg) }

// Create adds an account with exactly the permissions of its role's preset.
func (s *Service) Create(ctx context.Context, n NewUser) (User, error) {
	var err error
	if n.Username, err = checkUsername(n.Username); err != nil {
		return User{}, err
	}
	if n.DisplayName, err = checkDisplayName(n.DisplayName); err != nil {
		return User{}, err
	}
	if !perm.ValidRole(n.Role) {
		return User{}, invalid("role", "Choose patron, librarian or administrator.")
	}
	var id int64
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
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
		if err := setPermissions(ctx, tx, id, perm.Preset(n.Role)); err != nil {
			return err
		}
		// every account gets its library card when it is made (CARD-1)
		_, err = s.IssueCard(ctx, tx, id)
		return err
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

// Credentials is what sign-in and a password change need of an account.
type Credentials struct {
	ID                 int64
	PasswordHash       string
	Active             bool
	MustChangePassword bool
}

// CredentialsByUsername finds the account for a username, ignoring case. The
// second result is false when there is none.
func (s *Service) CredentialsByUsername(ctx context.Context, username string) (Credentials, bool, error) {
	return s.credentials(ctx, `SELECT id, password_hash, active, must_change_password FROM users WHERE username = ?`,
		strings.TrimSpace(username))
}

// CredentialsByID is CredentialsByUsername for an ID.
func (s *Service) CredentialsByID(ctx context.Context, id int64) (Credentials, bool, error) {
	return s.credentials(ctx, `SELECT id, password_hash, active, must_change_password FROM users WHERE id = ?`, id)
}

func (s *Service) credentials(ctx context.Context, query string, arg any) (Credentials, bool, error) {
	var c Credentials
	err := s.st.DB().QueryRowContext(ctx, query, arg).Scan(&c.ID, &c.PasswordHash, &c.Active, &c.MustChangePassword)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, false, nil
	}
	return c, err == nil, err
}

// SetPasswordHashTx stores a new password hash for the user inside the
// caller's transaction, and sets or clears must-change. The hash is made by
// internal/auth, which also checks the password's rules.
func (s *Service) SetPasswordHashTx(ctx context.Context, tx store.Tx, userID int64, hash string, mustChange bool) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, must_change_password = ? WHERE id = ?`,
		hash, mustChange, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// Rehash replaces a stored hash by an equivalent one with newer parameters,
// only if the stored hash is still old. It reports nothing when it lost a race.
func (s *Service) Rehash(ctx context.Context, userID int64, oldHash, newHash string) error {
	_, err := s.st.DB().ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ?`,
		newHash, userID, oldHash)
	return err
}

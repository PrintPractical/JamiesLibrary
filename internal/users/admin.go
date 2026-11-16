package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
)

// MaxListPageSize is the most users one page of List holds (security design §12).
const MaxListPageSize = 100

// DefaultListPageSize is the page size when the caller names none.
const DefaultListPageSize = 50

// UserPage is one page of users, by username.
type UserPage struct {
	Users    []User
	Page     int
	PageSize int
	Total    int
}

// List returns page number page (from 1) of all users ordered by username. A
// size of 0 means DefaultListPageSize.
func (s *Service) List(ctx context.Context, page, size int) (UserPage, error) {
	if size == 0 {
		size = DefaultListPageSize
	}
	if size < 1 || size > MaxListPageSize {
		return UserPage{}, apperr.OnField(apperr.Invalid, "per_page", fmt.Sprintf("Ask for between 1 and %d users per page.", MaxListPageSize))
	}
	if page < 1 {
		return UserPage{}, apperr.OnField(apperr.Invalid, "page", "The page number starts at 1.")
	}
	out := UserPage{Users: []User{}, Page: page, PageSize: size}
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&out.Total); err != nil {
		return UserPage{}, fmt.Errorf("counting users: %w", err)
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id FROM users ORDER BY username COLLATE NOCASE, id LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return UserPage{}, fmt.Errorf("listing users: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return UserPage{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return UserPage{}, err
	}
	for _, id := range ids {
		u, err := s.Get(ctx, id)
		if err != nil {
			return UserPage{}, err
		}
		out.Users = append(out.Users, u)
	}
	return out, nil
}

// IsActive reports whether the user exists and is active. Checkout uses it to
// refuse a deactivated patron (ADMIN-2).
func (s *Service) IsActive(ctx context.Context, id int64) (bool, error) {
	return isActive(ctx, s.st.DB(), id)
}

// IsActiveTx is IsActive inside the caller's transaction.
func (s *Service) IsActiveTx(ctx context.Context, tx store.Tx, id int64) (bool, error) {
	return isActive(ctx, tx, id)
}

func isActive(ctx context.Context, q querier, id int64) (bool, error) {
	var active bool
	err := q.QueryRowContext(ctx, `SELECT active FROM users WHERE id = ?`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active, err
}

// SetRoleTx records the role and replaces the user's permissions with its
// preset, inside the caller's transaction. It checks nothing about who asks:
// internal/useradmin does, for every change a person makes.
func (s *Service) SetRoleTx(ctx context.Context, tx store.Tx, userID int64, role perm.Role) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, string(role), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return setPermissions(ctx, tx, userID, perm.Preset(role))
}

// SetPermissionTx switches one permission for the user, inside the caller's
// transaction, with the same absence of checks as SetRoleTx.
func (s *Service) SetPermissionTx(ctx context.Context, tx store.Tx, userID int64, p perm.Permission, on bool) error {
	q := `DELETE FROM user_permissions WHERE user_id = ? AND permission = ?`
	if on {
		q = `INSERT OR IGNORE INTO user_permissions (user_id, permission) VALUES (?, ?)`
	}
	_, err := tx.ExecContext(ctx, q, userID, string(p))
	return err
}

// SetActiveTx deactivates or reactivates the user inside the caller's transaction.
func (s *Service) SetActiveTx(ctx context.Context, tx store.Tx, userID int64, active bool) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET active = ? WHERE id = ?`, active, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// ActiveManagersTx counts the active users who hold both users.manage and
// users.permissions, inside the caller's transaction (the last-administrator rule).
func (s *Service) ActiveManagersTx(ctx context.Context, tx store.Tx) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM users u
		WHERE u.active = 1
		  AND EXISTS (SELECT 1 FROM user_permissions p WHERE p.user_id = u.id AND p.permission = ?)
		  AND EXISTS (SELECT 1 FROM user_permissions p WHERE p.user_id = u.id AND p.permission = ?)`,
		string(perm.UsersManage), string(perm.UsersPermissions)).Scan(&n)
	return n, err
}

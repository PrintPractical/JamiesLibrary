// Package setup is the first administrator's set-up (DEPLOY-3): while no
// active user holds both manage-users permissions, the server holds a one-time
// code in memory that only whoever reads its console knows, and the owner
// presents it to create the first administrator.
package setup

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"strconv"
	"sync"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/setupcode"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

// SetupCompleted is the activity-log action for the first administrator's creation.
const SetupCompleted = "setup.completed"

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// HasAdministrator reports whether an active user holds both users.manage and
// users.permissions: the question that decides whether setup is open. The
// recovery command asks it too.
func HasAdministrator(ctx context.Context, q querier) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users u
		WHERE u.active = 1
		  AND EXISTS (SELECT 1 FROM user_permissions p WHERE p.user_id = u.id AND p.permission = ?)
		  AND EXISTS (SELECT 1 FROM user_permissions p WHERE p.user_id = u.id AND p.permission = ?)`,
		string(perm.UsersManage), string(perm.UsersPermissions)).Scan(&n)
	return n > 0, err
}

var errClosed = apperr.New(apperr.NotFound, "There is nothing here.")

// ErrClosed is what every setup route answers once setup is over (a 404).
func ErrClosed() error { return errClosed }

// Service holds the setup code and creates the first administrator.
type Service struct {
	st    *store.Store
	users *users.Service
	auth  *auth.Service

	mu   sync.Mutex
	code string // normalised; "" while setup is closed
}

// New makes a Service with setup closed: call Start to open it when it is needed.
func New(st *store.Store, u *users.Service, a *auth.Service) *Service {
	return &Service{st: st, users: u, auth: a}
}

// Start opens setup when no administrator exists, with a new code that it
// returns for printing. With an administrator it returns "" and setup stays
// closed. The code is kept only here, in memory.
func (s *Service) Start(ctx context.Context) (string, error) {
	has, err := HasAdministrator(ctx, s.st.DB())
	if err != nil || has {
		return "", err
	}
	code, err := setupcode.New()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.code = setupcode.Normalize(code)
	s.mu.Unlock()
	return code, nil
}

// Open says whether setup is open.
func (s *Service) Open() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code != ""
}

// IsOpen is Open that also asks the database: when an administrator has appeared
// since start-up (the recovery command's --make-admin on a running server),
// setup closes and its code is forgotten.
func (s *Service) IsOpen(ctx context.Context) (bool, error) {
	if !s.Open() {
		return false, nil
	}
	has, err := HasAdministrator(ctx, s.st.DB())
	if err != nil {
		return false, err
	}
	if has {
		s.clear()
	}
	return !has, nil
}

// CodeMatches compares a typed code with the setup code in constant time. It
// is false when setup is closed.
func (s *Service) CodeMatches(typed string) bool {
	s.mu.Lock()
	code := s.code
	s.mu.Unlock()
	got := setupcode.Normalize(typed)
	if len(got) > 64 {
		got = got[:64] // bound the work; a longer code is wrong anyway
	}
	return code != "" && subtle.ConstantTimeCompare([]byte(got), []byte(code)) == 1
}

func (s *Service) clear() {
	s.mu.Lock()
	s.code = ""
	s.mu.Unlock()
}

// NewAdmin is the first administrator to create.
type NewAdmin struct {
	Username, DisplayName, Password string
}

// CreateAdmin creates the first administrator, with the administrator preset
// and a card, and closes setup. In one transaction it re-checks that no
// administrator exists, so of two setups that run at once only one succeeds
// and the other is told setup is closed. The caller has already checked the code.
func (s *Service) CreateAdmin(ctx context.Context, n NewAdmin) (int64, error) {
	if !s.Open() {
		return 0, errClosed
	}
	if err := auth.CheckPassword("password", n.Password); err != nil {
		return 0, err
	}
	hash, err := s.auth.Passwords.Hash(ctx, "password", n.Password)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		has, err := HasAdministrator(ctx, tx)
		if err != nil {
			return err
		}
		if has {
			return errClosed
		}
		if id, err = s.users.CreateTx(ctx, tx, users.NewUser{
			Username: n.Username, DisplayName: n.DisplayName, PasswordHash: hash, Role: perm.Administrator,
		}); err != nil {
			return err
		}
		return activity.Record(ctx, tx, activity.ByUser(id), SetupCompleted, "user:"+strconv.FormatInt(id, 10), nil)
	})
	if err == errClosed {
		s.clear()
	}
	if err != nil {
		return 0, err
	}
	s.clear()
	return id, nil
}

// StartSession signs the new administrator in.
func (s *Service) StartSession(ctx context.Context, userID int64) (string, error) {
	return s.auth.StartSession(ctx, userID)
}

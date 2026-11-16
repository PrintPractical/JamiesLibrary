package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

// CookieName is the session cookie: the __Host- prefix makes a browser accept
// it only as Secure, with Path=/ and no Domain (security design §2).
const CookieName = "__Host-jl_session"

// SeenWriteInterval is how seldom a session's last_seen_at is written.
const SeenWriteInterval = time.Minute

const tokenBytes = 32

// Service signs users in, keeps their sessions and tells each request who is making it.
type Service struct {
	// Passwords hashes every password the server stores: registration, setup,
	// reset and recovery use it too.
	Passwords *Passwords

	st    *store.Store
	users *users.Service
	idle  func() time.Duration
	now   func() time.Time
	log   *slog.Logger
}

// NewService makes a Service. idle says how long a session may sit unused (the
// session-idle setting); log may be nil.
func NewService(st *store.Store, u *users.Service, idle func() time.Duration, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Passwords: NewPasswords(), st: st, users: u, idle: idle, now: clock.Now, log: log}
}

var (
	errWrongCredentials = apperr.New(apperr.Unauthorized, "The username or password is wrong.")
	errDeactivated      = apperr.New(apperr.Forbidden, "This account is deactivated. Ask a librarian or an administrator.")
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) stamp() string { return s.now().UTC().Format(time.RFC3339) }

// SignIn checks the credentials and starts a new session, returning its token
// and the user's ID. A wrong password, an unknown name and an account whose
// stored hash is empty or damaged all give the same error, after the same
// hashing work. "Deactivated" is said only after the right password.
func (s *Service) SignIn(ctx context.Context, username, password string) (token string, userID int64, err error) {
	cred, found, err := s.users.CredentialsByUsername(ctx, username)
	if err != nil {
		return "", 0, err
	}
	if utf8.RuneCountInString(password) > MaxPasswordLength {
		// No password this long was ever stored: refuse without hashing megabytes.
		return "", 0, errWrongCredentials
	}
	var match, rehash bool
	if found {
		if match, rehash, err = s.Passwords.Verify(ctx, password, cred.PasswordHash); err != nil {
			return "", 0, err
		}
	} else if err = s.Passwords.VerifyDummy(ctx, password); err != nil {
		return "", 0, err
	}
	if !match {
		return "", 0, errWrongCredentials
	}
	if !cred.Active {
		return "", 0, errDeactivated
	}
	if rehash {
		s.rehash(ctx, cred, password)
	}
	token, err = s.create(ctx, cred.ID)
	return token, cred.ID, err
}

// rehash replaces a hash made with older parameters. A failure is logged and
// the sign-in goes on: the old hash still works.
func (s *Service) rehash(ctx context.Context, cred users.Credentials, password string) {
	newHash, err := s.Passwords.hash(ctx, password)
	if err == nil {
		err = s.users.Rehash(ctx, cred.ID, cred.PasswordHash, newHash)
	}
	if err != nil {
		s.log.Warn("could not update a password hash to the current parameters", "user_id", cred.ID, "err", err)
	}
}

// StartSession starts a session for a user who has just been let in by other
// means than a password check, such as the first administrator's setup, and
// returns its token.
func (s *Service) StartSession(ctx context.Context, userID int64) (string, error) {
	return s.create(ctx, userID)
}

func (s *Service) create(ctx context.Context, userID int64) (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	now := s.stamp()
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		// Sessions nobody has used for the idle time are dead anyway: drop them here so the table stays small.
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE last_seen_at < ?`,
			s.now().Add(-s.idle()).UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at) VALUES (?, ?, ?, ?)`,
			hashToken(token), userID, now, now)
		return err
	})
	return token, err
}

// Authenticate loads the caller for a session token: the user, whether they
// are active and their permissions are read from the database now. It returns
// nil for an unknown or expired session, and for a user who isn't active.
func (s *Service) Authenticate(ctx context.Context, token string) (*CallerInfo, error) {
	id := hashToken(token)
	var (
		userID   int64
		lastSeen string
	)
	err := s.st.DB().QueryRowContext(ctx, `SELECT user_id, last_seen_at FROM sessions WHERE token_hash = ?`, id).
		Scan(&userID, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	seen, perr := time.Parse(time.RFC3339, lastSeen)
	if perr != nil || now.Sub(seen) > s.idle() {
		_, err := s.st.DB().ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, id)
		return nil, err
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == apperr.NotFound {
			return nil, nil
		}
		return nil, err
	}
	if !u.Active {
		return nil, nil
	}
	if now.Sub(seen) >= SeenWriteInterval {
		if _, err := s.st.DB().ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, s.stamp(), id); err != nil {
			s.log.Warn("could not record a session's last use", "err", err)
		}
	}
	return &CallerInfo{UserID: u.ID, Permissions: u.Permissions, MustChangePassword: u.MustChangePassword, SessionID: id}, nil
}

// Middleware puts the caller in the request's context when it carries a valid
// session cookie. Only API and media (and fetched-cover preview) requests are looked up; the app's own
// files need no session. A database failure answers 500 rather than letting
// the request through unauthenticated.
func (s *Service) Middleware(onError func(http.ResponseWriter, *http.Request, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie(CookieName); err == nil && c.Value != "" &&
				(strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/media/") || strings.HasPrefix(r.URL.Path, "/media-tmp/")) {
				caller, err := s.Authenticate(r.Context(), c.Value)
				if err != nil {
					onError(w, r, err)
					return
				}
				if caller != nil {
					r = r.WithContext(WithCaller(r.Context(), caller))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SignOut ends the session the caller came with.
func (s *Service) SignOut(ctx context.Context, sessionID string) error {
	_, err := s.st.DB().ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, sessionID)
	return err
}

// EndAll ends every session of the user: deactivation, an administrator's
// reset, recovery, a cut in permissions.
func (s *Service) EndAll(ctx context.Context, userID int64) error {
	_, err := s.st.DB().ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// EndAllTx is EndAll inside the caller's transaction, so the change that calls
// for it and the end of the sessions happen together.
func (s *Service) EndAllTx(ctx context.Context, tx store.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// EndOthersTx ends every session of the user but keep (a Caller's SessionID).
func (s *Service) EndOthersTx(ctx context.Context, tx store.Tx, userID int64, keep string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, userID, keep)
	return err
}

// ChangePassword changes the user's own password (ACCESS-6): it needs the
// current one, applies the password rule to the new one, stores it, clears
// must-change and ends the user's other sessions, all in one transaction.
func (s *Service) ChangePassword(ctx context.Context, userID int64, keepSession, current, next string) error {
	if err := CheckPassword("new_password", next); err != nil {
		return err
	}
	cred, found, err := s.users.CredentialsByID(ctx, userID)
	if err != nil {
		return err
	}
	if !found {
		return apperr.New(apperr.Unauthorized, "Sign in to continue.")
	}
	match := false
	if utf8.RuneCountInString(current) <= MaxPasswordLength {
		if match, _, err = s.Passwords.Verify(ctx, current, cred.PasswordHash); err != nil {
			return err
		}
	}
	if !match {
		return apperr.OnField(apperr.Invalid, "current_password", "The current password is wrong.")
	}
	if current == next {
		return apperr.OnField(apperr.Invalid, "new_password", "Choose a password different from the current one.")
	}
	hash, err := s.Passwords.hash(ctx, next)
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := s.users.SetPasswordHashTx(ctx, tx, userID, hash, false); err != nil {
			return err
		}
		return s.EndOthersTx(ctx, tx, userID, keepSession)
	})
}

// SetCookie sends the session cookie. It has no Max-Age: the browser drops it
// with the session, and the server's idle expiry is what ends a session.
func SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// ClearCookie tells the browser to forget the session cookie.
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

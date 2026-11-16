// Package activity owns the administrative activity log (ADMIN-4): who did
// what, to what, and when. An entry is written with Record inside the
// transaction of the change it describes, so a refused or rolled-back change
// leaves no entry. Details never hold a secret (security design §1, §4, §5).
package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/store"
)

// Actions that are logged. The name is what the log shows and stores.
const (
	SettingsChanged       = "settings.changed"
	UserRoleChanged       = "user.role_changed"
	UserPermissionChanged = "user.permission_changed"
	UserDeactivated       = "user.deactivated"
	UserReactivated       = "user.reactivated"
	UserPasswordReset     = "user.password_reset"
	InvitationCreated     = "invitation.created"
	InvitationRevoked     = "invitation.revoked"
)

// Limits on what is stored and on a page of the log.
const (
	MaxActionLength = 64
	MaxTargetLength = 200
	MaxDetailsBytes = 4096
	DefaultPageSize = 50
	MaxPageSize     = 100
	ConsoleName     = "Server console"
)

// Actor is who did the thing: an account, or the server console.
type Actor struct {
	UserID  int64
	Console bool
}

// ByUser is the actor for a signed-in account.
func ByUser(id int64) Actor { return Actor{UserID: id} }

// ServerConsole is the actor for a command run on the server itself.
var ServerConsole = Actor{Console: true}

// forbiddenKeys are detail names that would hold a secret. Record refuses
// them, so a mistake in a caller cannot put a code or password in the log.
var forbiddenKeys = []string{"password", "passcode", "code", "token", "secret", "hash"}

// Record writes one entry inside tx. target says what was acted on, such as
// "user:7" or "invitation:3"; details is a small JSON-encodable map of what
// changed (nil for none) and must hold no code, password or token.
func Record(ctx context.Context, tx store.Tx, actor Actor, action, target string, details map[string]any) error {
	if action == "" || len(action) > MaxActionLength {
		return fmt.Errorf("activity: action %q must be 1 to %d characters", action, MaxActionLength)
	}
	if len(target) > MaxTargetLength {
		return fmt.Errorf("activity: target is over %d characters", MaxTargetLength)
	}
	if !actor.Console && actor.UserID <= 0 {
		return fmt.Errorf("activity: %s has no actor", action)
	}
	for k := range details {
		lk := strings.ToLower(k)
		for _, bad := range forbiddenKeys {
			if strings.Contains(lk, bad) {
				return fmt.Errorf("activity: detail %q may hold a secret and is not logged", k)
			}
		}
	}
	if details == nil {
		details = map[string]any{}
	}
	body, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("activity: details of %s: %w", action, err)
	}
	if len(body) > MaxDetailsBytes {
		return fmt.Errorf("activity: details of %s are over %d bytes", action, MaxDetailsBytes)
	}
	var actorID any
	if !actor.Console {
		actorID = actor.UserID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO activity (at, actor_id, action, target, details) VALUES (?, ?, ?, ?, ?)`,
		clock.Now().UTC().Format(time.RFC3339), actorID, action, target, string(body)); err != nil {
		return fmt.Errorf("activity: recording %s: %w", action, err)
	}
	return nil
}

// Service reads the log.
type Service struct{ st *store.Store }

// New makes a Service over st.
func New(st *store.Store) *Service { return &Service{st: st} }

// Entry is one logged action. ActorID is 0 and ActorName is ConsoleName for
// the server console.
type Entry struct {
	ID        int64
	At        time.Time
	ActorID   int64
	ActorName string
	Action    string
	Target    string
	Details   json.RawMessage
}

// Page is one page of the log, newest first.
type Page struct {
	Entries  []Entry
	Page     int
	PageSize int
	Total    int
}

// List returns page number page (from 1) of the log, newest first. A page
// size of 0 means DefaultPageSize; one over MaxPageSize, or a page below 1, is refused.
func (s *Service) List(ctx context.Context, page, size int) (Page, error) {
	if size == 0 {
		size = DefaultPageSize
	}
	if size < 1 || size > MaxPageSize {
		return Page{}, apperr.OnField(apperr.Invalid, "per_page", fmt.Sprintf("Ask for between 1 and %d entries per page.", MaxPageSize))
	}
	if page < 1 {
		return Page{}, apperr.OnField(apperr.Invalid, "page", "The page number starts at 1.")
	}
	out := Page{Entries: []Entry{}, Page: page, PageSize: size}
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM activity`).Scan(&out.Total); err != nil {
		return Page{}, fmt.Errorf("counting the activity log: %w", err)
	}
	rows, err := s.st.DB().QueryContext(ctx, `
		SELECT a.id, a.at, a.actor_id, u.display_name, a.action, a.target, a.details
		FROM activity a LEFT JOIN users u ON u.id = a.actor_id
		ORDER BY a.at DESC, a.id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return Page{}, fmt.Errorf("reading the activity log: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			e       Entry
			at      string
			actorID sql.NullInt64
			name    sql.NullString
			details string
		)
		if err := rows.Scan(&e.ID, &at, &actorID, &name, &e.Action, &e.Target, &details); err != nil {
			return Page{}, fmt.Errorf("reading the activity log: %w", err)
		}
		if e.At, err = time.Parse(time.RFC3339, at); err != nil {
			return Page{}, fmt.Errorf("activity %d has a bad time %q: %w", e.ID, at, err)
		}
		e.ActorID, e.ActorName = actorID.Int64, name.String
		if !actorID.Valid {
			e.ActorName = ConsoleName
		}
		e.Details = json.RawMessage(details)
		out.Entries = append(out.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("reading the activity log: %w", err)
	}
	return out, nil
}

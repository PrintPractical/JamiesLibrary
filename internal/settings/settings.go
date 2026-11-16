// Package settings holds the library's settings: its name, logo, colour scheme,
// time zone, borrowing rules and switches. It owns the settings table, checks
// every value before it is stored, and answers from memory.
package settings

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // zones work in a minimal image with no zoneinfo
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/store"
)

// Copy-ID modes (CAT-9).
const (
	CopyIDISBN = "isbn"
	CopyIDOwn  = "own"
)

// Limits on what the setters accept.
const (
	MaxNameLength       = 80
	MinLoanPeriodDays   = 1
	MaxLoanPeriodDays   = 365
	MinMaxLoans         = 1
	MaxMaxLoans         = 100
	MinMaxRenewals      = 0
	MaxMaxRenewals      = 20
	MinSessionIdleMins  = 15
	MaxSessionIdleMins  = 43200 // 30 days
	maxTimeZoneNameSize = 64
)

// Settings is the library's settings as stored.
type Settings struct {
	Name               string
	LogoFile           string // "" for no logo
	Scheme             string // a preset's ID
	Accent             string // #RRGGBB, or "" to use the scheme's own colour
	TimeZone           string // an IANA name
	LoanPeriodDays     int
	MaxLoans           int
	MaxRenewals        int
	SelfCheckout       bool
	PublicCatalog      bool
	CopyIDMode         string // CopyIDISBN or CopyIDOwn
	SessionIdleMinutes int
}

// Service reads and writes the settings.
type Service struct {
	st *store.Store

	wmu sync.Mutex // one write (and its cache refresh) at a time

	mu  sync.RWMutex
	cur Settings
	loc *time.Location
}

// New loads the settings row and returns a Service that caches it.
func New(ctx context.Context, st *store.Store) (*Service, error) {
	s := &Service{st: st}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

const columns = `library_name, logo_file, scheme, accent, time_zone, loan_period_days, max_loans,
	max_renewals, self_checkout, public_catalog, copy_id_mode, session_idle_minutes`

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func read(ctx context.Context, q queryer) (Settings, error) {
	var v Settings
	err := q.QueryRowContext(ctx, `SELECT `+columns+` FROM settings WHERE id = 1`).Scan(
		&v.Name, &v.LogoFile, &v.Scheme, &v.Accent, &v.TimeZone, &v.LoanPeriodDays, &v.MaxLoans,
		&v.MaxRenewals, &v.SelfCheckout, &v.PublicCatalog, &v.CopyIDMode, &v.SessionIdleMinutes)
	if err != nil {
		return v, fmt.Errorf("reading the settings: %w", err)
	}
	return v, nil
}

// Reload re-reads the row into the cache. A caller that changed the settings
// through a transaction of its own (SetCopyIDModeTx) calls it after commit.
func (s *Service) Reload(ctx context.Context) error {
	v, err := read(ctx, s.st.DB())
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(v.TimeZone)
	if err != nil {
		return fmt.Errorf("the stored time zone %q cannot be loaded: %w", v.TimeZone, err)
	}
	s.mu.Lock()
	s.cur, s.loc = v, loc
	s.mu.Unlock()
	return nil
}

// Get returns the current settings, from memory.
func (s *Service) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Location is the library's time zone.
func (s *Service) Location() *time.Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loc
}

// Today is the current calendar date, YYYY-MM-DD, in the library's time zone.
// "Now" comes from internal/clock, so a test can move it.
func (s *Service) Today() string {
	return clock.Now().In(s.Location()).Format(time.DateOnly)
}

// update runs one column change in its own transaction, then refreshes the cache.
func (s *Service) update(ctx context.Context, column string, value any) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	err := s.st.WithTx(ctx, func(tx store.Tx) error { return exec(ctx, tx, column, value) })
	if err != nil {
		return err
	}
	return s.Reload(ctx)
}

func exec(ctx context.Context, tx store.Tx, column string, value any) error {
	// column is always one of this package's own constants, never input.
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET `+column+` = ? WHERE id = 1`, value); err != nil {
		return fmt.Errorf("saving the setting %s: %w", column, err)
	}
	return nil
}

func invalid(field, msg string) error { return apperr.OnField(apperr.Invalid, field, msg) }

func inRange(field, label string, v, lo, hi int, unit string) error {
	if v < lo || v > hi {
		return invalid(field, fmt.Sprintf("%s must be between %d and %d %s.", label, lo, hi, unit))
	}
	return nil
}

// SetName sets the library's name: 1 to 80 characters, not blank.
func (s *Service) SetName(ctx context.Context, name string) error {
	name, err := checkName(name)
	if err != nil {
		return err
	}
	return s.update(ctx, "library_name", name)
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameLength {
		return "", invalid("name", fmt.Sprintf("The library's name must be between 1 and %d characters.", MaxNameLength))
	}
	if strings.ContainsFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return "", invalid("name", "The library's name can't contain control characters.")
	}
	return name, nil
}

// SetLogo records the stored logo's file name; "" removes the logo. The image
// itself is checked and stored by internal/media.
func (s *Service) SetLogo(ctx context.Context, file string) error {
	return s.update(ctx, "logo_file", file)
}

// SetBranding sets the colour scheme and the optional custom accent together,
// because whether an accent is readable depends on the scheme. An accent that
// fails is refused with an error (field "accent") whose message says which
// check failed and offers a darker shade; the result carries the numbers and
// the shade, and nothing is stored.
func (s *Service) SetBranding(ctx context.Context, schemeID, accent string) (AccentResult, error) {
	scheme, accent, res, err := checkBranding(schemeID, accent)
	if err != nil {
		return res, err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := exec(ctx, tx, "scheme", scheme.ID); err != nil {
			return err
		}
		return exec(ctx, tx, "accent", accent)
	})
	if err != nil {
		return AccentResult{}, err
	}
	return AccentResult{OK: true}, s.Reload(ctx)
}

// checkBranding validates a scheme and accent together and returns the
// normalised accent. A failing accent gives the result with the numbers.
func checkBranding(schemeID, accent string) (Scheme, string, AccentResult, error) {
	scheme, ok := SchemeByID(schemeID)
	if !ok {
		return scheme, "", AccentResult{}, invalid("scheme", "Choose one of the colour schemes on offer.")
	}
	if accent == "" {
		return scheme, "", AccentResult{OK: true}, nil
	}
	norm, ok := normaliseHex(accent)
	if !ok {
		return scheme, "", AccentResult{}, invalid("accent", "The accent colour must be written like #7A2E22.")
	}
	res, err := CheckAccent(scheme, norm)
	if err != nil {
		return scheme, "", res, invalid("accent", err.Error())
	}
	if !res.OK {
		return scheme, "", res, invalid("accent", accentMessage(scheme, norm, res))
	}
	return scheme, norm, res, nil
}

func accentMessage(scheme Scheme, accent string, r AccentResult) string {
	var parts []string
	for _, c := range r.Failed {
		switch c {
		case CheckOnAccent:
			parts = append(parts, fmt.Sprintf("text on %s (such as button labels) has a contrast of %.1f : 1", accent, r.OnAccentRatio))
		case CheckAsText:
			parts = append(parts, fmt.Sprintf("%s as text on the %s background has a contrast of %.1f : 1", accent, scheme.Name, r.AsTextRatio))
		}
	}
	return fmt.Sprintf("Text would be hard to read on this colour: %s, and needs at least 4.5 : 1. Try a darker shade, such as %s. Your scheme hasn't changed.",
		strings.Join(parts, "; "), r.Suggestion)
}

// SetTimeZone sets the library's time zone to an IANA name such as
// "America/Chicago". "Local" is refused: the server's own zone is not the library's.
func (s *Service) SetTimeZone(ctx context.Context, name string) error {
	if err := checkTimeZone(name); err != nil {
		return err
	}
	return s.update(ctx, "time_zone", name)
}

func checkTimeZone(name string) error {
	if name == "" || name == "Local" || len(name) > maxTimeZoneNameSize {
		return invalid("time_zone", "Choose a time zone such as Europe/London or America/Chicago.")
	}
	if _, err := time.LoadLocation(name); err != nil {
		return invalid("time_zone", fmt.Sprintf("%q is not a time zone this server knows. Choose one such as Europe/London or America/Chicago.", name))
	}
	return nil
}

// SetLoanPeriod sets how many days a loan lasts: 1 to 365.
func (s *Service) SetLoanPeriod(ctx context.Context, days int) error {
	if err := checkLoanPeriod(days); err != nil {
		return err
	}
	return s.update(ctx, "loan_period_days", days)
}

func checkLoanPeriod(days int) error {
	return inRange("loan_period_days", "The loan period", days, MinLoanPeriodDays, MaxLoanPeriodDays, "days")
}

// SetMaxLoans sets how many items a patron may have out: 1 to 100.
func (s *Service) SetMaxLoans(ctx context.Context, n int) error {
	if err := checkMaxLoans(n); err != nil {
		return err
	}
	return s.update(ctx, "max_loans", n)
}

func checkMaxLoans(n int) error {
	return inRange("max_loans", "The most items a patron may have out", n, MinMaxLoans, MaxMaxLoans, "items")
}

// SetMaxRenewals sets how many times a loan can be renewed: 0 to 20.
func (s *Service) SetMaxRenewals(ctx context.Context, n int) error {
	if err := checkMaxRenewals(n); err != nil {
		return err
	}
	return s.update(ctx, "max_renewals", n)
}

func checkMaxRenewals(n int) error {
	return inRange("max_renewals", "The number of renewals", n, MinMaxRenewals, MaxMaxRenewals, "times")
}

// SetSessionIdle sets how long a signed-in browser may sit idle: 15 minutes to 30 days.
func (s *Service) SetSessionIdle(ctx context.Context, minutes int) error {
	if err := checkSessionIdle(minutes); err != nil {
		return err
	}
	return s.update(ctx, "session_idle_minutes", minutes)
}

func checkSessionIdle(minutes int) error {
	return inRange("session_idle_minutes", "The idle time before signing out", minutes, MinSessionIdleMins, MaxSessionIdleMins, "minutes")
}

// SetSelfCheckout turns patrons' self-checkout on or off.
func (s *Service) SetSelfCheckout(ctx context.Context, on bool) error {
	return s.update(ctx, "self_checkout", on)
}

// SetPublicCatalog turns the signed-out catalog on or off.
func (s *Service) SetPublicCatalog(ctx context.Context, on bool) error {
	return s.update(ctx, "public_catalog", on)
}

// SetCopyIDModeTx records the copy-ID mode inside the caller's transaction, for
// the switch that gives copies their barcodes. It only stores the mode: after
// the transaction commits the caller calls Reload.
func (s *Service) SetCopyIDModeTx(ctx context.Context, tx store.Tx, mode string) error {
	if mode != CopyIDISBN && mode != CopyIDOwn {
		return invalid("copy_id_mode", "The copy ID mode must be \"isbn\" or \"own\".")
	}
	return exec(ctx, tx, "copy_id_mode", mode)
}

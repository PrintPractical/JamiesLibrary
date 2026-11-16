package settings

import (
	"context"
	"errors"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/store"
)

var errNoAudit = errors.New("settings: a change needs an activity record")

// Update is a change to the borrowing rules, time zone, session length and
// switches. A nil field is left as it is.
type Update struct {
	TimeZone           *string
	LoanPeriodDays     *int
	MaxLoans           *int
	MaxRenewals        *int
	SessionIdleMinutes *int
	SelfCheckout       *bool
	PublicCatalog      *bool
}

// Audit writes the activity entry for a change, inside the transaction that
// makes it, so a change and its record stand or fall together. details says
// what changed. Every method that changes settings for an administrator
// requires one.
type Audit func(ctx context.Context, tx store.Tx, details map[string]any) error

// change is one column to write and what the activity entry says about it.
type change struct {
	column   string
	field    string
	from, to any // what the activity entry shows
	value    any // what is stored; nil when it is to
}

// Apply checks every field of u, then stores the ones that differ and records
// one activity entry for them in the same transaction. A field that fails
// leaves every field unchanged. It returns the settings as they are afterwards.
func (s *Service) Apply(ctx context.Context, audit Audit, u Update) (Settings, error) {
	if audit == nil {
		return s.Get(), errNoAudit
	}
	for _, c := range []struct {
		v     *int
		check func(int) error
	}{
		{u.LoanPeriodDays, checkLoanPeriod}, {u.MaxLoans, checkMaxLoans}, {u.MaxRenewals, checkMaxRenewals},
		{u.SessionIdleMinutes, checkSessionIdle},
	} {
		if c.v != nil {
			if err := c.check(*c.v); err != nil {
				return s.Get(), err
			}
		}
	}
	if u.TimeZone != nil {
		if err := checkTimeZone(*u.TimeZone); err != nil {
			return s.Get(), err
		}
	}
	err := s.commit(ctx, audit, func(cur Settings) []change {
		var cs []change
		add := func(column string, from, to any) {
			if from != to {
				cs = append(cs, change{column: column, field: column, from: from, to: to})
			}
		}
		if u.TimeZone != nil {
			add("time_zone", cur.TimeZone, *u.TimeZone)
		}
		if u.LoanPeriodDays != nil {
			add("loan_period_days", cur.LoanPeriodDays, *u.LoanPeriodDays)
		}
		if u.MaxLoans != nil {
			add("max_loans", cur.MaxLoans, *u.MaxLoans)
		}
		if u.MaxRenewals != nil {
			add("max_renewals", cur.MaxRenewals, *u.MaxRenewals)
		}
		if u.SessionIdleMinutes != nil {
			add("session_idle_minutes", cur.SessionIdleMinutes, *u.SessionIdleMinutes)
		}
		if u.SelfCheckout != nil {
			add("self_checkout", cur.SelfCheckout, *u.SelfCheckout)
		}
		if u.PublicCatalog != nil {
			add("public_catalog", cur.PublicCatalog, *u.PublicCatalog)
		}
		return cs
	})
	return s.Get(), err
}

// ApplyBranding sets the library's name, colour scheme and accent together
// (ADMIN-1), checking the accent against the scheme first. Nothing is stored
// when any part is refused. For a failing accent the result carries the
// measured ratios and the suggested shade.
func (s *Service) ApplyBranding(ctx context.Context, audit Audit, name, schemeID, accent string) (AccentResult, error) {
	if audit == nil {
		return AccentResult{}, errNoAudit
	}
	name, err := checkName(name)
	if err != nil {
		return AccentResult{}, err
	}
	scheme, accent, res, err := checkBranding(schemeID, accent)
	if err != nil {
		return res, err
	}
	err = s.commit(ctx, audit, func(cur Settings) []change {
		var cs []change
		if cur.Name != name {
			cs = append(cs, change{column: "library_name", field: "name", from: cur.Name, to: name})
		}
		if cur.Scheme != scheme.ID {
			cs = append(cs, change{column: "scheme", field: "scheme", from: cur.Scheme, to: scheme.ID})
		}
		if cur.Accent != accent {
			cs = append(cs, change{column: "accent", field: "accent", from: cur.Accent, to: accent})
		}
		return cs
	})
	return res, err
}

// ReplaceLogo records file as the logo ("" removes it) and returns the file it
// replaced, "" if there was none, for the caller to delete.
func (s *Service) ReplaceLogo(ctx context.Context, audit Audit, file string) (string, error) {
	if audit == nil {
		return "", errNoAudit
	}
	var old string
	err := s.commit(ctx, audit, func(cur Settings) []change {
		old = cur.LogoFile
		if old == file {
			return nil
		}
		// The entry says only that the logo changed: stored names mean nothing to a reader.
		return []change{{column: "logo_file", field: "logo", from: logoState(old), to: logoState(file), value: file}}
	})
	return old, err
}

func logoState(file string) string {
	if file == "" {
		return "none"
	}
	return "set"
}

// commit reads the current settings under the write lock, asks plan what to
// change, and writes the columns and one activity entry in one transaction. A
// plan with nothing to change writes nothing.
func (s *Service) commit(ctx context.Context, audit Audit, plan func(cur Settings) []change) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	cs := plan(s.Get())
	if len(cs) == 0 {
		return nil
	}
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		changes := make(map[string]any, len(cs))
		for _, c := range cs {
			value := c.value
			if value == nil {
				value = c.to
			}
			if err := exec(ctx, tx, c.column, value); err != nil {
				return err
			}
			changes[c.field] = map[string]any{"from": c.from, "to": c.to}
		}
		return audit(ctx, tx, map[string]any{"changes": changes})
	})
	if err != nil {
		return err
	}
	return s.Reload(ctx)
}

// PreviewBranding is the verdict for the live preview: the checks of
// ApplyBranding without storing anything. A scheme or accent that isn't even
// well formed is an error; a well-formed accent that fails is a result with
// OK false, the measured ratios and a suggested shade. The tokens are the
// CSS custom properties to draw with: the accent's when it passes, the
// scheme's own when it doesn't.
func PreviewBranding(schemeID, accent string) (AccentResult, map[string]string, error) {
	scheme, norm, res, err := checkBranding(schemeID, accent)
	if err == nil {
		return res, scheme.Tokens(norm), nil
	}
	if e, ok := apperr.As(err); ok && e.Field == "accent" && res.Suggestion != "" {
		return res, scheme.Tokens(""), nil
	}
	return res, nil, err
}

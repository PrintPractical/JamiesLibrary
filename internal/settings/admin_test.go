package settings

import (
	"context"
	"errors"
	"testing"

	"jamieslibrary/internal/store"
)

func noAudit(context.Context, store.Tx, map[string]any) error { return nil }

// A change and its activity record stand or fall together: when the record
// fails, the setting isn't changed and the cache keeps the old value.
func TestFailedAuditRollsTheChangeBack(t *testing.T) {
	svc, _ := newService(t)
	boom := errors.New("log full")
	fail := func(context.Context, store.Tx, map[string]any) error { return boom }
	on := true
	if _, err := svc.Apply(ctx, fail, Update{SelfCheckout: &on}); !errors.Is(err, boom) {
		t.Fatalf("Apply error = %v", err)
	}
	if _, err := svc.ApplyBranding(ctx, fail, "Renamed", "inkwell", ""); !errors.Is(err, boom) {
		t.Fatalf("ApplyBranding error = %v", err)
	}
	if _, err := svc.ReplaceLogo(ctx, fail, "0123456789abcdef0123456789abcdef.png"); !errors.Is(err, boom) {
		t.Fatalf("ReplaceLogo error = %v", err)
	}
	got := svc.Get()
	if got.SelfCheckout || got.Name != "Jamie's Library" || got.Scheme != DefaultScheme || got.LogoFile != "" {
		t.Errorf("settings after failed audits: %+v", got)
	}
	if err := svc.Reload(ctx); err != nil || svc.Get().SelfCheckout {
		t.Errorf("the database changed: %+v, %v", svc.Get(), err)
	}
}

// Without an audit nothing is changed.
func TestChangeWithoutAuditIsRefused(t *testing.T) {
	svc, _ := newService(t)
	on := true
	if _, err := svc.Apply(ctx, nil, Update{SelfCheckout: &on}); err == nil {
		t.Error("Apply without an audit succeeded")
	}
	if _, err := svc.ApplyBranding(ctx, nil, "X", DefaultScheme, ""); err == nil {
		t.Error("ApplyBranding without an audit succeeded")
	}
	if _, err := svc.ReplaceLogo(ctx, nil, ""); err == nil {
		t.Error("ReplaceLogo without an audit succeeded")
	}
	if svc.Get().SelfCheckout || svc.Get().Name != "Jamie's Library" {
		t.Errorf("settings %+v", svc.Get())
	}
}

// An accent that fails is refused with the numbers and a suggestion, and none of the branding is stored.
func TestApplyBrandingRefusesAFailingAccentWhole(t *testing.T) {
	svc, _ := newService(t)
	res, err := svc.ApplyBranding(ctx, noAudit, "Renamed", "inkwell", "#FFEB3B")
	wantInvalid(t, err, "accent")
	if res.OK || res.Suggestion == "" {
		t.Errorf("result %+v", res)
	}
	if got := svc.Get(); got.Name != "Jamie's Library" || got.Scheme != DefaultScheme {
		t.Errorf("settings %+v", got)
	}
}

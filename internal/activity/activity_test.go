package activity

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/users"
)

var ctx = context.Background()

type fixture struct {
	st    *store.Store
	svc   *Service
	admin users.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	admin, err := users.New(st).Create(ctx, users.NewUser{Username: "ada", DisplayName: "Ada Admin", PasswordHash: "x", Role: perm.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{st: st, svc: New(st), admin: admin}
}

func (f *fixture) record(t *testing.T, actor Actor, action, target string, details map[string]any) {
	t.Helper()
	err := f.st.WithTx(ctx, func(tx store.Tx) error { return Record(ctx, tx, actor, action, target, details) })
	if err != nil {
		t.Fatalf("Record %s: %v", action, err)
	}
}

// ADMIN-4 "Role change logged": a role change appears with who did it, what changed and when.
func TestRoleChangeIsLoggedWithActorTargetAndTime(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 3, 4, 10, 30, 0, 0, time.UTC)
	t.Cleanup(clock.SetSource(func() time.Time { return at }))
	f.record(t, ByUser(f.admin.ID), UserRoleChanged, "user:7", map[string]any{"from": "patron", "to": "librarian"})

	p, err := f.svc.List(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 1 || len(p.Entries) != 1 {
		t.Fatalf("page %+v", p)
	}
	e := p.Entries[0]
	if e.ActorID != f.admin.ID || e.ActorName != "Ada Admin" || e.Action != "user.role_changed" || e.Target != "user:7" || !e.At.Equal(at) {
		t.Fatalf("entry %+v", e)
	}
	if string(e.Details) != `{"from":"patron","to":"librarian"}` {
		t.Fatalf("details %s", e.Details)
	}
}

func TestRolledBackChangeLeavesNoEntry(t *testing.T) {
	f := newFixture(t)
	boom := errors.New("refused")
	err := f.st.WithTx(ctx, func(tx store.Tx) error {
		if err := Record(ctx, tx, ByUser(f.admin.ID), UserDeactivated, "user:2", nil); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if p, _ := f.svc.List(ctx, 1, 0); p.Total != 0 || len(p.Entries) != 0 {
		t.Fatalf("a rolled-back action was logged: %+v", p)
	}
}

func TestServerConsoleActor(t *testing.T) {
	f := newFixture(t)
	f.record(t, ServerConsole, UserPasswordReset, "user:2", nil)
	p, _ := f.svc.List(ctx, 1, 0)
	if e := p.Entries[0]; e.ActorID != 0 || e.ActorName != ConsoleName || string(e.Details) != "{}" {
		t.Fatalf("entry %+v", e)
	}
}

func TestNewestFirstAndPaged(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Cleanup(clock.SetSource(func() time.Time { return now }))
	for i := 0; i < 5; i++ {
		f.record(t, ByUser(f.admin.ID), SettingsChanged, "settings", map[string]any{"n": i})
		now = now.Add(time.Minute)
	}
	p, err := f.svc.List(ctx, 1, 2)
	if err != nil || p.Total != 5 || len(p.Entries) != 2 || string(p.Entries[0].Details) != `{"n":4}` || string(p.Entries[1].Details) != `{"n":3}` {
		t.Fatalf("page 1: %+v %v", p, err)
	}
	p, _ = f.svc.List(ctx, 3, 2)
	if len(p.Entries) != 1 || string(p.Entries[0].Details) != `{"n":0}` {
		t.Fatalf("page 3: %+v", p)
	}
	p, err = f.svc.List(ctx, 9, 2)
	if err != nil || len(p.Entries) != 0 || p.Total != 5 {
		t.Fatalf("past the end: %+v %v", p, err)
	}
}

func TestSameSecondKeepsInsertionOrder(t *testing.T) {
	f := newFixture(t)
	for _, a := range []string{InvitationCreated, InvitationRevoked} {
		f.record(t, ByUser(f.admin.ID), a, "invitation:1", nil)
	}
	p, _ := f.svc.List(ctx, 1, 0)
	if p.Entries[0].Action != InvitationRevoked {
		t.Fatalf("order %+v", p.Entries)
	}
}

func TestListRefusesBadPaging(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct{ page, size int }{{0, 10}, {-1, 10}, {1, 101}, {1, -5}, {MaxPage + 1, 10}, {math.MaxInt, 10}} {
		_, err := f.svc.List(ctx, c.page, c.size)
		if e, ok := apperr.As(err); !ok || e.Code != apperr.Invalid {
			t.Errorf("page %d size %d: %v", c.page, c.size, err)
		}
	}
	if _, err := f.svc.List(ctx, 1, MaxPageSize); err != nil {
		t.Errorf("the largest page: %v", err)
	}
}

func TestRecordRefusesWhatItMustNotStore(t *testing.T) {
	f := newFixture(t)
	cases := map[string]struct {
		actor   Actor
		action  string
		target  string
		details map[string]any
	}{
		"password":    {ByUser(1), UserPasswordReset, "user:1", map[string]any{"new_password": "x"}},
		"code":        {ByUser(1), InvitationCreated, "invitation:1", map[string]any{"Code": "ABCD"}},
		"nested map":  {ByUser(1), SettingsChanged, "settings", map[string]any{"changes": map[string]any{"smtp_password": "hunter2"}}},
		"in a list":   {ByUser(1), SettingsChanged, "settings", map[string]any{"changes": []any{map[string]any{"token": "t"}}}},
		"no actor":    {Actor{}, SettingsChanged, "settings", nil},
		"no action":   {ByUser(1), "", "settings", nil},
		"long target": {ByUser(1), SettingsChanged, strings.Repeat("x", MaxTargetLength+1), nil},
		"big details": {ByUser(1), SettingsChanged, "settings", map[string]any{"note": strings.Repeat("x", MaxDetailsBytes)}},
		"unencodable": {ByUser(1), SettingsChanged, "settings", map[string]any{"f": func() {}}},
	}
	for name, c := range cases {
		err := f.st.WithTx(ctx, func(tx store.Tx) error { return Record(ctx, tx, c.actor, c.action, c.target, c.details) })
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if p, _ := f.svc.List(ctx, 1, 0); p.Total != 0 {
		t.Fatalf("refused entries were stored: %+v", p)
	}
}

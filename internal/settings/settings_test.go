package settings

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/store"
)

var ctx = context.Background()

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc, err := New(ctx, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, st
}

func wantInvalid(t *testing.T, err error, field string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != apperr.Invalid || e.Field != field {
		t.Fatalf("error = %v, want an invalid-input error on %q", err, field)
	}
}

func TestDefaultsOnAFreshDatabase(t *testing.T) {
	svc, _ := newService(t)
	want := Settings{
		Name: "Jamie's Library", Scheme: "hearth", TimeZone: "UTC",
		LoanPeriodDays: 14, MaxLoans: 5, MaxRenewals: 2, CopyIDMode: "isbn", SessionIdleMinutes: 480,
	}
	if got := svc.Get(); got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestSettingsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(ctx, dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := New(ctx, st)
	if err := svc.SetName(ctx, "Elm Street Library"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = store.Open(ctx, dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, _ = New(ctx, st)
	if got := svc.Get().Name; got != "Elm Street Library" {
		t.Fatalf("name after reopen = %q", got)
	}
}

func TestSettersStoreAndRefreshTheCache(t *testing.T) {
	svc, st := newService(t)
	steps := []struct {
		name string
		set  func() error
	}{
		{"name", func() error { return svc.SetName(ctx, "  Elm Street Library ") }},
		{"logo", func() error { return svc.SetLogo(ctx, "abc123.png") }},
		{"zone", func() error { return svc.SetTimeZone(ctx, "America/Chicago") }},
		{"loan", func() error { return svc.SetLoanPeriod(ctx, 21) }},
		{"loans", func() error { return svc.SetMaxLoans(ctx, 8) }},
		{"renewals", func() error { return svc.SetMaxRenewals(ctx, 0) }},
		{"idle", func() error { return svc.SetSessionIdle(ctx, 60) }},
		{"self", func() error { return svc.SetSelfCheckout(ctx, true) }},
		{"public", func() error { return svc.SetPublicCatalog(ctx, true) }},
		{"branding", func() error { _, err := svc.SetBranding(ctx, "inkwell", "#2c4a6e"); return err }},
	}
	for _, s := range steps {
		if err := s.set(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
	}
	want := Settings{
		Name: "Elm Street Library", LogoFile: "abc123.png", Scheme: "inkwell", Accent: "#2C4A6E",
		TimeZone: "America/Chicago", LoanPeriodDays: 21, MaxLoans: 8, MaxRenewals: 0,
		SelfCheckout: true, PublicCatalog: true, CopyIDMode: "isbn", SessionIdleMinutes: 60,
	}
	if got := svc.Get(); got != want {
		t.Fatalf("cache = %+v, want %+v", got, want)
	}
	fromDB, err := read(ctx, st.DB())
	if err != nil || fromDB != want {
		t.Fatalf("database = %+v (%v), want %+v", fromDB, err, want)
	}
}

func TestValidation(t *testing.T) {
	svc, _ := newService(t)
	before := svc.Get()
	cases := []struct {
		name  string
		field string
		set   func() error
	}{
		{"empty name", "name", func() error { return svc.SetName(ctx, "") }},
		{"blank name", "name", func() error { return svc.SetName(ctx, "   ") }},
		{"81-char name", "name", func() error { return svc.SetName(ctx, strings.Repeat("a", 81)) }},
		{"control char name", "name", func() error { return svc.SetName(ctx, "a\x00b") }},
		{"unknown zone", "time_zone", func() error { return svc.SetTimeZone(ctx, "Mars/Olympus") }},
		{"empty zone", "time_zone", func() error { return svc.SetTimeZone(ctx, "") }},
		{"Local zone", "time_zone", func() error { return svc.SetTimeZone(ctx, "Local") }},
		{"path zone", "time_zone", func() error { return svc.SetTimeZone(ctx, "../../etc/passwd") }},
		{"loan 0", "loan_period_days", func() error { return svc.SetLoanPeriod(ctx, 0) }},
		{"loan 366", "loan_period_days", func() error { return svc.SetLoanPeriod(ctx, 366) }},
		{"loans 0", "max_loans", func() error { return svc.SetMaxLoans(ctx, 0) }},
		{"loans 101", "max_loans", func() error { return svc.SetMaxLoans(ctx, 101) }},
		{"renewals -1", "max_renewals", func() error { return svc.SetMaxRenewals(ctx, -1) }},
		{"renewals 21", "max_renewals", func() error { return svc.SetMaxRenewals(ctx, 21) }},
		{"idle 14", "session_idle_minutes", func() error { return svc.SetSessionIdle(ctx, 14) }},
		{"idle 43201", "session_idle_minutes", func() error { return svc.SetSessionIdle(ctx, 43201) }},
		{"unknown scheme", "scheme", func() error { _, err := svc.SetBranding(ctx, "neon", ""); return err }},
		{"malformed accent", "accent", func() error { _, err := svc.SetBranding(ctx, "hearth", "red"); return err }},
		{"short accent", "accent", func() error { _, err := svc.SetBranding(ctx, "hearth", "#fff"); return err }},
	}
	for _, c := range cases {
		wantInvalid(t, c.set(), c.field)
	}
	if got := svc.Get(); got != before {
		t.Fatalf("a refused value changed the settings: %+v", got)
	}
}

func TestBoundariesAreAccepted(t *testing.T) {
	svc, _ := newService(t)
	for _, err := range []error{
		svc.SetName(ctx, "a"), svc.SetName(ctx, strings.Repeat("é", 80)),
		svc.SetLoanPeriod(ctx, 1), svc.SetLoanPeriod(ctx, 365),
		svc.SetMaxLoans(ctx, 1), svc.SetMaxLoans(ctx, 100),
		svc.SetMaxRenewals(ctx, 0), svc.SetMaxRenewals(ctx, 20),
		svc.SetSessionIdle(ctx, 15), svc.SetSessionIdle(ctx, 43200),
	} {
		if err != nil {
			t.Fatalf("boundary refused: %v", err)
		}
	}
}

func TestLightYellowAccentFailsTheOnAccentCheck(t *testing.T) {
	svc, _ := newService(t)
	res, err := svc.SetBranding(ctx, "hearth", "#E8B94A")
	wantInvalid(t, err, "accent")
	if res.OK || !hasCheck(res, CheckOnAccent) {
		t.Fatalf("result = %+v, want the on-accent check to fail", res)
	}
	if !strings.Contains(err.Error(), res.Suggestion) || res.Suggestion == "" {
		t.Fatalf("message %q should offer the suggestion %q", err, res.Suggestion)
	}
	assertPasses(t, "hearth", res.Suggestion)
	if got := svc.Get(); got.Accent != "" || got.Scheme != "hearth" {
		t.Fatalf("a refused accent was stored: %+v", got)
	}
}

func TestFaintAccentFailsAsText(t *testing.T) {
	// Dark enough for the light label, too faint for text on the background:
	// a pale scheme's own text colour against its own background is the case.
	scheme := Scheme{ID: "x", Name: "X", OnPrimary: "#000000", Paper: "#FFFFFF"}
	res, err := CheckAccent(scheme, "#B0B0B0")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || hasCheck(res, CheckOnAccent) || !hasCheck(res, CheckAsText) {
		t.Fatalf("result = %+v, want only the as-text check to fail", res)
	}
	got, _ := CheckAccent(scheme, res.Suggestion)
	if !got.OK {
		t.Fatalf("suggestion %s does not pass: %+v", res.Suggestion, got)
	}
}

func TestFaintAccentOnAPresetIsRefused(t *testing.T) {
	svc, _ := newService(t)
	res, err := svc.SetBranding(ctx, "hearth", "#D8CBB0")
	wantInvalid(t, err, "accent")
	if !hasCheck(res, CheckAsText) {
		t.Fatalf("result = %+v, want the as-text check to fail", res)
	}
	assertPasses(t, "hearth", res.Suggestion)
}

func TestSuggestionAlwaysPasses(t *testing.T) {
	for _, sc := range Schemes() {
		for _, accent := range []string{"#FFFFFF", "#FFFF00", "#E8B94A", "#00FFFF", "#FF69B4", "#CCCCCC", "#9ACD32", "#F4A460", "#000000", "#777777"} {
			res, err := CheckAccent(sc, accent)
			if err != nil {
				t.Fatal(err)
			}
			if res.OK {
				continue
			}
			assertPasses(t, sc.ID, res.Suggestion)
		}
	}
}

func TestAccentThatPassesIsStoredAndCanBeCleared(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.SetBranding(ctx, "reading-room", "#1a4d7a"); err != nil {
		t.Fatal(err)
	}
	if got := svc.Get(); got.Scheme != "reading-room" || got.Accent != "#1A4D7A" {
		t.Fatalf("got %+v", got)
	}
	if _, err := svc.SetBranding(ctx, "reading-room", ""); err != nil {
		t.Fatal(err)
	}
	if got := svc.Get().Accent; got != "" {
		t.Fatalf("accent after clearing = %q", got)
	}
}

func TestChangingSchemeChecksTheExistingAccentAgainstIt(t *testing.T) {
	svc, _ := newService(t)
	// SetBranding takes scheme and accent together, so a new scheme can't be
	// stored under an accent that fails on it.
	if _, err := svc.SetBranding(ctx, "hearth", "#7A2E22"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetBranding(ctx, "inkwell", "#B0B0B0"); err == nil {
		t.Fatal("expected the faint accent to be refused")
	}
	if got := svc.Get(); got.Scheme != "hearth" || got.Accent != "#7A2E22" {
		t.Fatalf("a refused change altered the branding: %+v", got)
	}
}

func hasCheck(r AccentResult, c Check) bool {
	for _, f := range r.Failed {
		if f == c {
			return true
		}
	}
	return false
}

func assertPasses(t *testing.T, scheme, accent string) {
	t.Helper()
	sc, _ := SchemeByID(scheme)
	res, err := CheckAccent(sc, accent)
	if err != nil || !res.OK {
		t.Fatalf("%s on %s should pass: %+v (%v)", accent, scheme, res, err)
	}
}

func TestEveryPresetPasses(t *testing.T) {
	all := Schemes()
	if len(all) != 4 || all[0].ID != DefaultScheme || all[0].Name != "Hearth" {
		t.Fatalf("presets = %+v, want Hearth first of four", all)
	}
	seen := map[string]bool{}
	for _, sc := range all {
		if seen[sc.ID] {
			t.Fatalf("duplicate scheme %s", sc.ID)
		}
		seen[sc.ID] = true
		res, err := CheckAccent(sc, sc.Primary)
		if err != nil || !res.OK {
			t.Errorf("%s: its own accent fails: %+v (%v)", sc.ID, res, err)
		}
		for token, v := range sc.Tokens("") {
			if _, ok := parseHex(v); !ok {
				t.Errorf("%s %s = %q is not #RRGGBB", sc.ID, token, v)
			}
		}
		if r, _ := Contrast(sc.Ink, sc.Paper); r < MinContrast {
			t.Errorf("%s: ink on paper is %.2f", sc.ID, r)
		}
		if r, _ := Contrast(sc.OnWood, sc.Wood); r < MinContrast {
			t.Errorf("%s: on-wood on wood is %.2f", sc.ID, r)
		}
	}
}

func TestContrastKnownValues(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"#000000", "#FFFFFF", 21},
		{"#FFFFFF", "#FFFFFF", 1},
		{"#767676", "#FFFFFF", 4.54},
		{"#FFFFFF", "#E8B94A", 1.83},
	} {
		got, err := Contrast(c.a, c.b)
		if err != nil || got < c.want-0.02 || got > c.want+0.02 {
			t.Errorf("Contrast(%s,%s) = %.3f (%v), want %.2f", c.a, c.b, got, err, c.want)
		}
	}
	if _, err := Contrast("nope", "#FFFFFF"); err == nil {
		t.Error("a bad colour should be an error")
	}
}

func TestAccentTokens(t *testing.T) {
	sc, _ := SchemeByID("hearth")
	if got := sc.Tokens("")["--primary"]; got != "#7A2E22" {
		t.Fatalf("--primary = %s", got)
	}
	tk := sc.Tokens("#1A4D7A")
	if tk["--primary"] != "#1A4D7A" || tk["--primary-hover"] == tk["--primary"] || tk["--on-primary"] != "#FFF8F0" {
		t.Fatalf("tokens with accent = %v", tk)
	}
}

func TestTodayAcrossMidnightInANonUTCZone(t *testing.T) {
	svc, _ := newService(t)
	if err := svc.SetTimeZone(ctx, "America/Chicago"); err != nil { // UTC-6 in January
		t.Fatal(err)
	}
	at := func(s string) { // set the clock
		now, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(clock.SetSource(func() time.Time { return now }))
	}
	at("2031-01-15T05:59:00Z") // 23:59 on the 14th in Chicago
	if got := svc.Today(); got != "2031-01-14" {
		t.Fatalf("Today = %s, want 2031-01-14", got)
	}
	at("2031-01-15T06:00:00Z") // midnight in Chicago
	if got := svc.Today(); got != "2031-01-15" {
		t.Fatalf("Today = %s, want 2031-01-15", got)
	}
	if err := svc.SetTimeZone(ctx, "Pacific/Auckland"); err != nil { // UTC+13 in January
		t.Fatal(err)
	}
	if got := svc.Today(); got != "2031-01-15" {
		t.Fatalf("Today in Auckland = %s, want 2031-01-15", got)
	}
	at("2031-01-15T11:00:00Z") // 00:00 on the 16th in Auckland
	if got := svc.Today(); got != "2031-01-16" {
		t.Fatalf("Today in Auckland = %s, want 2031-01-16", got)
	}
}

func TestCopyIDModeInTheCallersTransaction(t *testing.T) {
	svc, st := newService(t)
	wantInvalid(t, st.WithTx(ctx, func(tx store.Tx) error { return svc.SetCopyIDModeTx(ctx, tx, "barcode") }), "copy_id_mode")

	boom := errors.New("boom")
	err := st.WithTx(ctx, func(tx store.Tx) error {
		if err := svc.SetCopyIDModeTx(ctx, tx, "own"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := svc.Reload(ctx); err != nil || svc.Get().CopyIDMode != "isbn" {
		t.Fatalf("a rolled-back switch changed the mode: %q (%v)", svc.Get().CopyIDMode, err)
	}

	if err := st.WithTx(ctx, func(tx store.Tx) error { return svc.SetCopyIDModeTx(ctx, tx, "own") }); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(ctx); err != nil || svc.Get().CopyIDMode != "own" {
		t.Fatalf("mode = %q (%v)", svc.Get().CopyIDMode, err)
	}
}

func TestOneRowOnly(t *testing.T) {
	_, st := newService(t)
	if _, err := st.DB().Exec(`INSERT INTO settings (id, library_name, logo_file, scheme, accent, time_zone, loan_period_days, max_loans, max_renewals, self_checkout, public_catalog, copy_id_mode, session_idle_minutes) VALUES (2,'x','','hearth','','UTC',1,1,1,0,0,'isbn',15)`); err == nil {
		t.Fatal("a second settings row was accepted")
	}
}

func TestConcurrentWritesAndReads(t *testing.T) {
	svc, _ := newService(t)
	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := svc.SetMaxLoans(ctx, i); err != nil {
				t.Error(err)
			}
		}()
		go func() { defer wg.Done(); _ = svc.Get(); _ = svc.Today() }()
	}
	wg.Wait()
	if got, _ := read(ctx, svc.st.DB()); got != svc.Get() {
		t.Fatalf("cache %+v differs from database %+v", svc.Get(), got)
	}
}

package httpapi

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
)

// settingsSite is a site with an administrator and a librarian signed in.
type settingsSite struct {
	*site
	admin, lib string
}

func newSettingsSite(t *testing.T) settingsSite {
	s := newSite(t)
	s.addUser("ada", goodPassword, perm.Administrator)
	s.addUser("lib", goodPassword, perm.Librarian)
	return settingsSite{s, s.signIn("ada", goodPassword), s.signIn("lib", goodPassword)}
}

func (s settingsSite) library() libraryBody {
	s.t.Helper()
	rec := s.req("GET", "/api/library", "", "")
	var b libraryBody
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &b) != nil {
		s.t.Fatalf("GET /api/library = %d %s", rec.Code, rec.Body)
	}
	return b
}

func (s settingsSite) adminSettings() adminSettingsBody {
	s.t.Helper()
	rec := s.req("GET", "/api/admin/settings", s.admin, "")
	var b adminSettingsBody
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &b) != nil {
		s.t.Fatalf("GET /api/admin/settings = %d %s", rec.Code, rec.Body)
	}
	return b
}

// upload sends a multipart logo upload with the given file bytes.
func (s settingsSite) upload(cookie string, data []byte) *httptest.ResponseRecorder {
	s.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("logo", "logo.bin")
	if err != nil {
		s.t.Fatal(err)
	}
	part.Write(data)
	mw.Close()
	r := httptest.NewRequest("PUT", "/api/admin/branding/logo", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngOfSize is a valid PNG of exactly size bytes: a small picture with a text
// chunk after its header that holds the padding.
func pngOfSize(t *testing.T, size int) []byte {
	t.Helper()
	small := pngBytes(t)
	const header = 8 + 25 // the signature and the IHDR chunk
	pad := size - len(small) - 12 - len("Comment\x00")
	if pad < 0 {
		t.Fatalf("%d bytes is too small for a PNG with a text chunk", size)
	}
	data := append([]byte("tEXtComment\x00"), bytes.Repeat([]byte{'x'}, pad)...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)-4))
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(data))
	out := append(append(append([]byte{}, small[:header]...), chunk...), small[header:]...)
	if len(out) != size {
		t.Fatalf("built %d bytes, want %d", len(out), size)
	}
	return out
}

func (s settingsSite) images() int {
	s.t.Helper()
	files, err := filepath.Glob(filepath.Join(s.dir, "images", "*"))
	if err != nil {
		s.t.Fatal(err)
	}
	return len(files)
}

func errField(t *testing.T, rec *httptest.ResponseRecorder) (code int, field, msg string) {
	t.Helper()
	var e struct {
		Error struct{ Message, Field string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Error.Message == "" {
		var flat struct{ Message, Field string }
		_ = json.Unmarshal(rec.Body.Bytes(), &flat)
		return rec.Code, flat.Field, flat.Message
	}
	return rec.Code, e.Error.Field, e.Error.Message
}

// ADMIN-1 "Defaults", CIRC-7 "Off", ACCESS-4 "Off by default": a fresh installation shows the default name and warm scheme to anyone, with self-checkout and the public catalog off.
func TestSettingsDefaults(t *testing.T) {
	s := newSettingsSite(t)
	b := s.library()
	if b.Name != "Jamie's Library" || b.Scheme != "hearth" || b.Accent != "" || b.LogoURL != "" || b.PublicCatalog {
		t.Errorf("library %+v", b)
	}
	if b.Colours["--primary"] != "#7A2E22" {
		t.Errorf("default colours %v", b.Colours)
	}
	a := s.adminSettings()
	if a.SelfCheckout || a.PublicCatalog {
		t.Errorf("self-checkout %v, public catalog %v: both must be off", a.SelfCheckout, a.PublicCatalog)
	}
	if a.LoanPeriodDays < 1 || a.MaxLoans < 1 || len(a.Schemes) < 2 || a.Schemes[0].ID != "hearth" {
		t.Errorf("admin settings %+v", a)
	}
}

// ADMIN-1 "Rename": the new name is what any user sees, and the change is logged by who made it.
func TestRename(t *testing.T) {
	s := newSettingsSite(t)
	rec := s.req("PUT", "/api/admin/branding", s.admin, `{"name":"  Elm Street Library ","scheme":"hearth"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := s.library().Name; got != "Elm Street Library" {
		t.Errorf("name %q", got)
	}
	if n := s.logTotal(); n != 1 {
		t.Errorf("%d activity entries, want 1", n)
	}
	log := s.req("GET", "/api/admin/activity", s.admin, "").Body.String()
	if !strings.Contains(log, "settings.changed") || !strings.Contains(log, "Elm Street Library") {
		t.Errorf("activity: %s", log)
	}
	for _, bad := range []string{`{"name":"","scheme":"hearth"}`, `{"name":"   ","scheme":"hearth"}`, `{"name":"` + strings.Repeat("x", 81) + `","scheme":"hearth"}`, `{"name":"a\u0000b","scheme":"hearth"}`} {
		if rec := s.req("PUT", "/api/admin/branding", s.admin, bad); rec.Code != 422 {
			t.Errorf("%s = %d, want 422", bad, rec.Code)
		}
	}
	if s.library().Name != "Elm Street Library" || s.logTotal() != 1 {
		t.Error("a refused name changed something")
	}
}

// ADMIN-1 "Preset scheme": another preset is what anyone sees; an unknown one is refused and nothing changes.
func TestPresetScheme(t *testing.T) {
	s := newSettingsSite(t)
	rec := s.req("PUT", "/api/admin/branding", s.admin, `{"name":"Jamie's Library","scheme":"inkwell"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	b := s.library()
	if b.Scheme != "inkwell" || b.Colours["--primary"] != "#2C4A6E" {
		t.Errorf("library %+v", b)
	}
	if rec := s.req("PUT", "/api/admin/branding", s.admin, `{"name":"Jamie's Library","scheme":"neon"}`); rec.Code != 422 {
		t.Errorf("unknown scheme = %d", rec.Code)
	}
	if s.library().Scheme != "inkwell" || s.logTotal() != 1 {
		t.Error("a refused scheme changed something")
	}
}

// ADMIN-1 "Accent fails on its text": a light yellow is refused with a message, with a darker suggestion, and the scheme is unchanged.
func TestAccentFailsOnItsText(t *testing.T) {
	s := newSettingsSite(t)
	body := `{"name":"Jamie's Library","scheme":"hearth","accent":"#FFEB3B"}`
	code, field, msg := errField(t, s.req("PUT", "/api/admin/branding", s.admin, body))
	if code != 422 || field != "accent" || !strings.Contains(msg, "button labels") || !strings.Contains(msg, "#") {
		t.Errorf("%d %q %q", code, field, msg)
	}
	if b := s.library(); b.Accent != "" || b.Colours["--primary"] != "#7A2E22" || s.logTotal() != 0 {
		t.Errorf("a refused accent changed something: %+v", b)
	}
}

// ADMIN-1 "Accent fails as text": a faint accent is refused for the link text on the scheme's background.
func TestAccentFailsAsText(t *testing.T) {
	s := newSettingsSite(t)
	body := `{"name":"Jamie's Library","scheme":"hearth","accent":"#E0E0E0"}`
	code, field, msg := errField(t, s.req("PUT", "/api/admin/branding", s.admin, body))
	if code != 422 || field != "accent" || !strings.Contains(msg, "background") {
		t.Errorf("%d %q %q", code, field, msg)
	}
	if b := s.library(); b.Accent != "" || s.logTotal() != 0 {
		t.Errorf("a refused accent changed something: %+v", b)
	}
}

// ADMIN-1: the accent's form is checked before its contrast; a good dark accent is stored.
func TestAccentFormatThenContrast(t *testing.T) {
	s := newSettingsSite(t)
	for _, bad := range []string{"red", "#12345", "#12345G", "#7A2E22\n", "7A2E22", "#7A2E2200", "<b>"} {
		raw, _ := json.Marshal(map[string]string{"name": "X", "scheme": "hearth", "accent": bad})
		code, field, _ := errField(t, s.req("PUT", "/api/admin/branding", s.admin, string(raw)))
		if code != 422 || field != "accent" {
			t.Errorf("accent %q = %d on %q", bad, code, field)
		}
	}
	if s.logTotal() != 0 || s.library().Name == "X" {
		t.Error("a refused accent changed something, the name included")
	}
	if rec := s.req("PUT", "/api/admin/branding", s.admin, `{"name":"X","scheme":"hearth","accent":"#1a3a5c"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if b := s.library(); b.Accent != "#1A3A5C" || b.Colours["--primary"] != "#1A3A5C" {
		t.Errorf("library %+v", b)
	}
	// An empty accent goes back to the scheme's own colour.
	s.req("PUT", "/api/admin/branding", s.admin, `{"name":"X","scheme":"hearth"}`)
	if b := s.library(); b.Accent != "" || b.Colours["--primary"] != "#7A2E22" {
		t.Errorf("library %+v", b)
	}
}

// The live preview asks the server: a failing accent gets the verdict, the ratios and a suggestion that itself passes.
func TestBrandingCheck(t *testing.T) {
	s := newSettingsSite(t)
	var v checkBody
	rec := s.req("POST", "/api/admin/branding/check", s.admin, `{"scheme":"hearth","accent":"#FFEB3B"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if v.OK || len(v.Failed) == 0 || v.Suggestion == "" || v.OnAccentRatio >= 4.5 {
		t.Errorf("verdict %+v", v)
	}
	again := s.req("POST", "/api/admin/branding/check", s.admin, `{"scheme":"hearth","accent":"`+v.Suggestion+`"}`)
	var v2 checkBody
	if json.Unmarshal(again.Body.Bytes(), &v2) != nil || !v2.OK || v2.Colours["--primary"] != v.Suggestion {
		t.Errorf("the suggestion %s doesn't pass: %s", v.Suggestion, again.Body)
	}
	if rec := s.req("POST", "/api/admin/branding/check", s.admin, `{"scheme":"hearth","accent":"nope"}`); rec.Code != 422 {
		t.Errorf("malformed accent = %d", rec.Code)
	}
	if s.library().Accent != "" || s.logTotal() != 0 {
		t.Error("a check changed something")
	}
}

// ADMIN-1 "Bad logo": 10 MB, a non-image and an SVG are refused with a message and the branding and files are unchanged; a PNG works and can be removed.
// ADMIN-1 "Logo": a picture over 2 MB is refused as too large and changes
// nothing, one just under 2 MB is accepted. The sizes are written out, not
// taken from the server's constant, so changing the limit fails this.
func TestLogoLimitIsTwoMegabytes(t *testing.T) {
	s := newSettingsSite(t)
	const twoMB = 2 * 1024 * 1024
	rec := s.upload(s.admin, pngOfSize(t, twoMB+1024))
	code, field, msg := errField(t, rec)
	if code != 413 || field != "logo" || msg == "" {
		t.Fatalf("just over 2 MB: %d %q %q", code, field, msg)
	}
	if b := s.library(); b.LogoURL != "" || s.logTotal() != 0 || s.images() != 0 {
		t.Fatalf("a refused logo changed something: %+v, %d entries, %d files", b, s.logTotal(), s.images())
	}
	if rec := s.upload(s.admin, pngOfSize(t, twoMB-1024)); rec.Code != 200 {
		t.Fatalf("just under 2 MB: %d %s", rec.Code, rec.Body)
	}
	if !strings.HasPrefix(s.library().LogoURL, "/media/") {
		t.Errorf("logo %q", s.library().LogoURL)
	}
}

func TestBadLogoThenGoodLogo(t *testing.T) {
	s := newSettingsSite(t)
	for name, data := range map[string][]byte{
		"10 MB":     bytes.Repeat([]byte{0xFF}, 10<<20),
		"text":      []byte("hello, I am not a picture"),
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"empty":     {},
		"truncated": pngBytes(t)[:30],
	} {
		rec := s.upload(s.admin, data)
		code, field, msg := errField(t, rec)
		if (code != 413 && code != 422) || field != "logo" || msg == "" {
			t.Errorf("%s: %d %q %q", name, code, field, msg)
		}
	}
	if b := s.library(); b.LogoURL != "" || s.logTotal() != 0 || s.images() != 0 {
		t.Errorf("a refused logo changed something: %+v, %d entries, %d files", b, s.logTotal(), s.images())
	}

	rec := s.upload(s.admin, pngBytes(t))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	first := s.library().LogoURL
	if !strings.HasPrefix(first, "/media/") || s.images() != 1 || s.logTotal() != 1 {
		t.Fatalf("logo %q, %d files, %d entries", first, s.images(), s.logTotal())
	}
	if got := s.req("GET", first, "", ""); got.Code != 200 {
		t.Errorf("anonymous logo = %d", got.Code)
	}
	// A second logo replaces the first file instead of piling up beside it.
	s.upload(s.admin, pngBytes(t))
	if second := s.library().LogoURL; second == first || s.images() != 1 {
		t.Errorf("replacement: %q then %q, %d files", first, second, s.images())
	}
	if rec := s.req("DELETE", "/api/admin/branding/logo", s.admin, ""); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if s.library().LogoURL != "" || s.images() != 0 || s.logTotal() != 3 {
		t.Errorf("after delete: %q, %d files, %d entries", s.library().LogoURL, s.images(), s.logTotal())
	}
}

// A body that isn't multipart, or has no file part, is refused and changes nothing.
func TestLogoNeedsAFilePart(t *testing.T) {
	s := newSettingsSite(t)
	if rec := s.req("PUT", "/api/admin/branding/logo", s.admin, `{"logo":"x"}`); rec.Code != 400 {
		t.Errorf("JSON body = %d", rec.Code)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("logo", "not a file")
	mw.Close()
	r := httptest.NewRequest("PUT", "/api/admin/branding/logo", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: s.admin})
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	if rec.Code != 422 || s.logTotal() != 0 {
		t.Errorf("field without a file = %d", rec.Code)
	}
}

// CIRC-4, CIRC-7, ACCESS-4: the borrowing rules, time zone, session length and switches are saved, and each change is logged.
func TestSettingsChange(t *testing.T) {
	s := newSettingsSite(t)
	rec := s.req("PUT", "/api/admin/settings", s.admin, `{"loan_period_days":28,"max_loans":3,"max_renewals":0,"time_zone":"America/Chicago","session_idle_minutes":60,"self_checkout":true,"public_catalog":true}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	a := s.adminSettings()
	if a.LoanPeriodDays != 28 || a.MaxLoans != 3 || a.MaxRenewals != 0 || a.TimeZone != "America/Chicago" || a.SessionIdleMinutes != 60 || !a.SelfCheckout || !a.PublicCatalog {
		t.Errorf("settings %+v", a)
	}
	if !s.library().PublicCatalog {
		t.Error("the public library body doesn't show the public catalog on")
	}
	if s.logTotal() != 1 {
		t.Errorf("%d entries, want one for the one request", s.logTotal())
	}
	// Setting what is already set records nothing; an empty body changes nothing.
	s.req("PUT", "/api/admin/settings", s.admin, `{"loan_period_days":28}`)
	s.req("PUT", "/api/admin/settings", s.admin, `{}`)
	if s.logTotal() != 1 {
		t.Errorf("%d entries after no-op requests", s.logTotal())
	}
}

// Every bad value is refused with its field, and the other values sent with it are not saved either.
func TestSettingsRefusals(t *testing.T) {
	s := newSettingsSite(t)
	before := s.adminSettings()
	for field, body := range map[string]string{
		"session_idle_minutes": `{"max_loans":9,"session_idle_minutes":14}`,
		"loan_period_days":     `{"max_loans":9,"loan_period_days":0}`,
		"max_loans":            `{"max_loans":101}`,
		"max_renewals":         `{"max_loans":9,"max_renewals":-1}`,
		"time_zone":            `{"max_loans":9,"time_zone":"Mars/Olympus"}`,
	} {
		code, got, msg := errField(t, s.req("PUT", "/api/admin/settings", s.admin, body))
		if code != 422 || got != field || msg == "" {
			t.Errorf("%s: %d %q %q", body, code, got, msg)
		}
	}
	if s.req("PUT", "/api/admin/settings", s.admin, `{"session_idle_minutes":43201}`).Code != 422 {
		t.Error("31 days accepted")
	}
	if rec := s.req("PUT", "/api/admin/settings", s.admin, `{"session_idle_minutes":43200}`); rec.Code != 200 {
		t.Errorf("30 days refused: %s", rec.Body)
	}
	for _, bad := range []string{`{"copy_id_mode":"own"}`, `{"max_loans":"many"}`, `{"nope":1}`, `nonsense`} {
		if rec := s.req("PUT", "/api/admin/settings", s.admin, bad); rec.Code != 400 {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
	after := s.adminSettings()
	after.SessionIdleMinutes = before.SessionIdleMinutes
	if a, b := mustJSON(t, before), mustJSON(t, after); a != b || s.logTotal() != 1 {
		t.Errorf("a refusal changed something:\n%s\n%s (%d entries)", a, b, s.logTotal())
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ROLE-3: a librarian (no settings.manage) is refused on every settings and branding route, and nothing changes; a signed-out caller gets 401 on the admin ones.
func TestSettingsRefusedWithoutThePermission(t *testing.T) {
	s := newSettingsSite(t)
	before := mustJSON(t, s.adminSettings())
	routes := []struct{ method, path, body string }{
		{"GET", "/api/admin/settings", ""},
		{"PUT", "/api/admin/settings", `{"self_checkout":true,"public_catalog":true,"max_loans":2}`},
		{"PUT", "/api/admin/branding", `{"name":"Hijacked","scheme":"inkwell"}`},
		{"POST", "/api/admin/branding/check", `{"scheme":"hearth"}`},
		{"DELETE", "/api/admin/branding/logo", ""},
	}
	for _, r := range routes {
		if rec := s.req(r.method, r.path, s.lib, r.body); rec.Code != 403 {
			t.Errorf("librarian %s %s = %d, want 403", r.method, r.path, rec.Code)
		}
		if rec := s.req(r.method, r.path, "", r.body); rec.Code != 401 {
			t.Errorf("anonymous %s %s = %d, want 401", r.method, r.path, rec.Code)
		}
	}
	if rec := s.upload(s.lib, pngBytes(t)); rec.Code != 403 {
		t.Errorf("librarian logo upload = %d, want 403", rec.Code)
	}
	if mustJSON(t, s.adminSettings()) != before || s.logTotal() != 0 || s.images() != 0 {
		t.Error("a refused request changed something")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "images")); err != nil {
		t.Fatal(err)
	}
}

// GET /api/library shows no staff-only setting.
func TestLibraryBodyHoldsOnlyBranding(t *testing.T) {
	s := newSettingsSite(t)
	body := s.req("GET", "/api/library", "", "").Body.String()
	for _, secret := range []string{"time_zone", "loan_period", "max_loans", "session_idle", "self_checkout", "copy_id_mode"} {
		if strings.Contains(body, secret) {
			t.Errorf("the public body shows %s: %s", secret, body)
		}
	}
}

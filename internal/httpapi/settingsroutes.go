package httpapi

import (
	"bytes"
	"context"
	"net/http"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/media"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
)

// registerSettingsRoutes registers the library's branding for everyone and the
// settings and branding changes for those who manage settings (ADMIN-1,
// CIRC-4, CIRC-7, ACCESS-4). The rules live in internal/settings and the image
// checks in internal/media.
func registerSettingsRoutes(rt *Router, o Options) {
	a := &settingsRoutes{set: o.Settings, img: o.Media}
	rt.HandleFunc("GET /api/library", Public(), a.library)
	rt.HandleFunc("GET /api/admin/settings", Require(perm.SettingsManage), a.get)
	rt.HandleFunc("PUT /api/admin/settings", Require(perm.SettingsManage), a.put)
	rt.HandleFunc("PUT /api/admin/branding", Require(perm.SettingsManage), a.putBranding)
	rt.HandleFunc("POST /api/admin/branding/check", Require(perm.SettingsManage), a.checkBranding)
	rt.HandleFunc("PUT /api/admin/branding/logo", Require(perm.SettingsManage).Multipart(), a.putLogo)
	rt.HandleFunc("DELETE /api/admin/branding/logo", Require(perm.SettingsManage), a.deleteLogo)
}

// auditOf writes the signed-in caller's change to the activity log.
func auditOf(r *http.Request) settings.Audit {
	actor := activity.ByUser(auth.Caller(r.Context()).UserID)
	return func(ctx context.Context, tx store.Tx, details map[string]any) error {
		return activity.Record(ctx, tx, actor, activity.SettingsChanged, "settings", details)
	}
}

type settingsRoutes struct {
	set *settings.Service
	img *media.Images
}

// libraryBody is what anyone may see: the sign-in page draws itself from it.
type libraryBody struct {
	Name          string            `json:"name"`
	Scheme        string            `json:"scheme"`
	Accent        string            `json:"accent"`
	Colours       map[string]string `json:"colours"`
	LogoURL       string            `json:"logo_url"`
	PublicCatalog bool              `json:"public_catalog"`
}

func logoURL(file string) string {
	if file == "" {
		return ""
	}
	return "/media/" + file
}

func libraryOf(s settings.Settings) libraryBody {
	scheme, _ := settings.SchemeByID(s.Scheme)
	return libraryBody{
		Name: s.Name, Scheme: s.Scheme, Accent: s.Accent, Colours: scheme.Tokens(s.Accent),
		LogoURL: logoURL(s.LogoFile), PublicCatalog: s.PublicCatalog,
	}
}

func (a *settingsRoutes) library(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, libraryOf(a.set.Get()))
}

type schemeBody struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Colours     map[string]string `json:"colours"`
}

// adminSettingsBody is the whole of the settings an administrator sees.
type adminSettingsBody struct {
	libraryBody
	Schemes            []schemeBody `json:"schemes"`
	TimeZone           string       `json:"time_zone"`
	LoanPeriodDays     int          `json:"loan_period_days"`
	MaxLoans           int          `json:"max_loans"`
	MaxRenewals        int          `json:"max_renewals"`
	SessionIdleMinutes int          `json:"session_idle_minutes"`
	SelfCheckout       bool         `json:"self_checkout"`
	CopyIDMode         string       `json:"copy_id_mode"` // read-only here: the catalog API sets it
}

func adminOf(s settings.Settings) adminSettingsBody {
	b := adminSettingsBody{
		libraryBody: libraryOf(s), TimeZone: s.TimeZone, LoanPeriodDays: s.LoanPeriodDays, MaxLoans: s.MaxLoans,
		MaxRenewals: s.MaxRenewals, SessionIdleMinutes: s.SessionIdleMinutes, SelfCheckout: s.SelfCheckout,
		CopyIDMode: s.CopyIDMode,
	}
	for _, sc := range settings.Schemes() {
		b.Schemes = append(b.Schemes, schemeBody{ID: sc.ID, Name: sc.Name, Description: sc.Description, Colours: sc.Tokens("")})
	}
	return b
}

func (a *settingsRoutes) get(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, adminOf(a.set.Get()))
}

func (a *settingsRoutes) put(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TimeZone           *string `json:"time_zone"`
		LoanPeriodDays     *int    `json:"loan_period_days"`
		MaxLoans           *int    `json:"max_loans"`
		MaxRenewals        *int    `json:"max_renewals"`
		SessionIdleMinutes *int    `json:"session_idle_minutes"`
		SelfCheckout       *bool   `json:"self_checkout"`
		PublicCatalog      *bool   `json:"public_catalog"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	got, err := a.set.Apply(r.Context(), auditOf(r), settings.Update(in))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, adminOf(got))
}

// brandingIn is the body of the branding change and of its check.
type brandingIn struct {
	Name   string `json:"name"`
	Scheme string `json:"scheme"`
	Accent string `json:"accent"`
}

func (a *settingsRoutes) putBranding(w http.ResponseWriter, r *http.Request) {
	var in brandingIn
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	if _, err := a.set.ApplyBranding(r.Context(), auditOf(r), in.Name, in.Scheme, in.Accent); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, adminOf(a.set.Get()))
}

type checkBody struct {
	OK            bool              `json:"ok"`
	Failed        []settings.Check  `json:"failed"`
	OnAccentRatio float64           `json:"on_accent_ratio"`
	AsTextRatio   float64           `json:"as_text_ratio"`
	Suggestion    string            `json:"suggestion"`
	Colours       map[string]string `json:"colours"`
}

// checkBranding gives the verdict on a scheme and accent without storing them,
// so the live preview asks the server instead of repeating the rule.
func (a *settingsRoutes) checkBranding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Scheme string `json:"scheme"`
		Accent string `json:"accent"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	res, colours, err := settings.PreviewBranding(in.Scheme, in.Accent)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	failed := res.Failed
	if failed == nil {
		failed = []settings.Check{}
	}
	WriteJSON(w, http.StatusOK, checkBody{
		OK: res.OK, Failed: failed, OnAccentRatio: res.OnAccentRatio, AsTextRatio: res.AsTextRatio,
		Suggestion: res.Suggestion, Colours: colours,
	})
}

func (a *settingsRoutes) putLogo(w http.ResponseWriter, r *http.Request) {
	data, err := ReadFilePart(w, r, "logo", media.LogoLimit)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	name, err := a.img.Store(bytes.NewReader(data), media.LogoLimit, media.Logo)
	if err != nil {
		WriteError(w, r, withField(err, "logo"))
		return
	}
	a.replaceLogo(w, r, name)
}

func (a *settingsRoutes) deleteLogo(w http.ResponseWriter, r *http.Request) {
	a.replaceLogo(w, r, "")
}

// replaceLogo records the new logo, then deletes the old file. When recording
// fails the new file is deleted and the old logo stays.
func (a *settingsRoutes) replaceLogo(w http.ResponseWriter, r *http.Request, name string) {
	old, err := a.set.ReplaceLogo(r.Context(), auditOf(r), name)
	if err != nil {
		if name != "" {
			_ = a.img.Delete(name)
		}
		WriteError(w, r, err)
		return
	}
	if old != "" && old != name {
		_ = a.img.Delete(old) // a file left behind is harmless: nothing refers to it
	}
	WriteJSON(w, http.StatusOK, adminOf(a.set.Get()))
}

// withField names the form field on a refusal that has none.
func withField(err error, field string) error {
	if e, ok := apperr.As(err); ok && e.Field == "" {
		return apperr.OnField(e.Code, field, e.Message)
	}
	return err
}

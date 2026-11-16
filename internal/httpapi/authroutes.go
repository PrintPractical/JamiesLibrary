package httpapi

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/ratelimit"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/users"
)

// maxLoggedUsername is how much of a username a failed sign-in logs (sec §12).
const maxLoggedUsername = 64

// registerAuthRoutes registers sign-in, sign-out, "who am I" and the change of
// one's own password.
func registerAuthRoutes(rt *Router, o Options) {
	a := &authRoutes{auth: o.Auth, users: o.Users, settings: o.Settings, log: o.Logger, clients: o.Clients, limiter: o.Limiter}
	if a.clients == nil {
		a.clients = ratelimit.NewClients(nil)
	}
	if a.limiter == nil {
		a.limiter = ratelimit.New()
	}
	rt.HandleFunc("POST /api/auth/signin", Public(), a.signIn)
	rt.HandleFunc("POST /api/auth/signout", SignedIn().AllowWhileMustChange(), a.signOut)
	rt.HandleFunc("GET /api/me", SignedIn().AllowWhileMustChange(), a.me)
	rt.HandleFunc("POST /api/me/password", SignedIn().AllowWhileMustChange(), a.changePassword)
}

type authRoutes struct {
	auth     *auth.Service
	users    *users.Service
	settings *settings.Service
	log      *slog.Logger
	clients  *ratelimit.Clients
	limiter  *ratelimit.Limiter
}

// attempt counts a sign-in attempt, or a guess at the current password, against
// the client's address before any password work. When the address is blocked it
// writes the refusal and returns false. An allowed attempt is already counted
// as a failure: the caller gives that back (Forgive, Succeed) if it wasn't one.
func (a *authRoutes) attempt(w http.ResponseWriter, r *http.Request, name string) (key string, ok bool) {
	key = a.clients.Key(r)
	ok, retry := a.limiter.Allow(ratelimit.SignIn, key, name)
	if !ok {
		tooMany(w, r, retry)
	}
	return key, ok
}

// tooMany refuses a request from a blocked address, saying when to come back.
func tooMany(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
	WriteError(w, r, apperr.New(apperr.Throttled, tooManyAttempts))
}

// tooManyAttempts is what a blocked address is told, by every limited route.
const tooManyAttempts = "Too many attempts, try again later."

type userBody struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        perm.Role `json:"role"`
}

type publicSettings struct {
	Name           string `json:"name"`
	LogoFile       string `json:"logo_file"`
	Scheme         string `json:"scheme"`
	Accent         string `json:"accent"`
	TimeZone       string `json:"time_zone"`
	LoanPeriodDays int    `json:"loan_period_days"`
	MaxLoans       int    `json:"max_loans"`
	MaxRenewals    int    `json:"max_renewals"`
	SelfCheckout   bool   `json:"self_checkout"`
	PublicCatalog  bool   `json:"public_catalog"`
	CopyIDMode     string `json:"copy_id_mode"`
}

type meBody struct {
	User               userBody       `json:"user"`
	Permissions        []string       `json:"permissions"`
	MustChangePassword bool           `json:"must_change_password"`
	Settings           publicSettings `json:"settings"`
}

func (a *authRoutes) meBody(r *http.Request, id int64) (meBody, error) {
	u, err := a.users.Get(r.Context(), id)
	if err != nil {
		return meBody{}, err
	}
	s := a.settings.Get()
	perms := make([]string, 0, len(u.Permissions))
	for _, p := range u.Permissions.List() {
		perms = append(perms, string(p))
	}
	return meBody{
		User:               userBody{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role},
		Permissions:        perms,
		MustChangePassword: u.MustChangePassword,
		Settings: publicSettings{
			Name: s.Name, LogoFile: s.LogoFile, Scheme: s.Scheme, Accent: s.Accent, TimeZone: s.TimeZone,
			LoanPeriodDays: s.LoanPeriodDays, MaxLoans: s.MaxLoans, MaxRenewals: s.MaxRenewals,
			SelfCheckout: s.SelfCheckout, PublicCatalog: s.PublicCatalog, CopyIDMode: s.CopyIDMode,
		},
	}, nil
}

func (a *authRoutes) me(w http.ResponseWriter, r *http.Request) {
	body, err := a.meBody(r, auth.Caller(r.Context()).UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, body)
}

func (a *authRoutes) signIn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	key, ok := a.attempt(w, r, in.Username)
	if !ok {
		return
	}
	token, id, err := a.auth.SignIn(r.Context(), in.Username, in.Password)
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == apperr.Unauthorized {
			a.log.Warn("sign-in failed", "remote", a.clientHost(r), "username", truncate(in.Username, maxLoggedUsername))
		}
		if e, ok := apperr.As(err); !ok || e.Code != apperr.Unauthorized {
			a.limiter.Forgive(ratelimit.SignIn, key) // not a wrong password: a fault, or a cancelled request
		}
		WriteError(w, r, err)
		return
	}
	a.limiter.Succeed(ratelimit.SignIn, key, in.Username)
	// The browser's old session, if it had one, ends: a token it presents is never adopted.
	if c := auth.Caller(r.Context()); c != nil {
		_ = a.auth.SignOut(r.Context(), c.SessionID)
	}
	body, err := a.meBody(r, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	auth.SetCookie(w, token)
	WriteJSON(w, http.StatusOK, body)
}

func (a *authRoutes) signOut(w http.ResponseWriter, r *http.Request) {
	if err := a.auth.SignOut(r.Context(), auth.Caller(r.Context()).SessionID); err != nil {
		WriteError(w, r, err)
		return
	}
	auth.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *authRoutes) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	c := auth.Caller(r.Context())
	caller, err := a.users.Get(r.Context(), c.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	key, ok := a.attempt(w, r, caller.Username)
	if !ok {
		return
	}
	if err := a.auth.ChangePassword(r.Context(), c.UserID, c.SessionID, in.Current, in.New); err != nil {
		// Only a wrong current password is a failed attempt.
		if e, ok := apperr.As(err); !ok || e.Field != "current_password" {
			a.limiter.Forgive(ratelimit.SignIn, key)
		}
		WriteError(w, r, err)
		return
	}
	a.limiter.Forgive(ratelimit.SignIn, key)
	w.WriteHeader(http.StatusNoContent)
}

// clientHost is the client's address as the proxy setting finds it, for logs.
func (a *authRoutes) clientHost(r *http.Request) string {
	if addr := a.clients.Addr(r); addr.IsValid() {
		return addr.String()
	}
	return remoteHost(r)
}

// remoteHost is the connection's own address, without the port.
func remoteHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

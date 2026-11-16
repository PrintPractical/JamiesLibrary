package httpapi

import (
	"net/http"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/ratelimit"
	"jamieslibrary/internal/setup"
)

// registerSetupRoutes registers the first administrator's set-up (DEPLOY-3,
// security design §6). Every route answers 404 once setup is closed.
func registerSetupRoutes(rt *Router, o Options) {
	s := &setupRoutes{setup: o.Setup, authRoutes: authRoutes{
		auth: o.Auth, users: o.Users, settings: o.Settings, log: o.Logger, clients: o.Clients, limiter: o.Limiter}}
	rt.HandleFunc("GET /api/setup/status", Public(), s.status)
	rt.HandleFunc("POST /api/setup/admin", Public(), s.createAdmin)
}

// setupRoutes shares the sign-in routes' services, so the new administrator
// is answered exactly as a sign-in is.
type setupRoutes struct {
	setup *setup.Service
	authRoutes
}

func (s *setupRoutes) status(w http.ResponseWriter, r *http.Request) {
	if !s.setup.Open() {
		WriteError(w, r, setup.ErrClosed())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"open": true})
}

func (s *setupRoutes) createAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.setup.Open() {
		WriteError(w, r, setup.ErrClosed())
		return
	}
	var in struct {
		Code        string `json:"code"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	// A wrong code counts against the address with registration's failures
	// (security design §3); the attempt is counted before it is judged.
	key := s.clients.Key(r)
	ok, retry := s.limiter.Allow(ratelimit.Registration, key, "")
	if !ok {
		tooMany(w, r, retry)
		return
	}
	if !s.setup.CodeMatches(in.Code) {
		WriteError(w, r, apperr.OnField(apperr.Forbidden, "code", "That setup code is wrong. Copy it from the server's console output."))
		return
	}
	// A right code never clears the count: it only takes back its own attempt.
	s.limiter.Forgive(ratelimit.Registration, key)
	id, err := s.setup.CreateAdmin(r.Context(), setup.NewAdmin{Username: in.Username, DisplayName: in.DisplayName, Password: in.Password})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	token, err := s.setup.StartSession(r.Context(), id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// The browser's old session, if it had one, ends: a token it presents is never adopted.
	if c := auth.Caller(r.Context()); c != nil {
		_ = s.auth.SignOut(r.Context(), c.SessionID)
	}
	body, err := s.meBody(r, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	auth.SetCookie(w, token)
	WriteJSON(w, http.StatusOK, body)
}

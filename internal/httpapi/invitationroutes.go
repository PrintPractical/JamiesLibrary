package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/invitations"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/ratelimit"
)

// registerInvitationRoutes registers the management of invitation codes
// (ACCESS-3) and registration with one (ACCESS-2). The rules live in the
// invitations service.
func registerInvitationRoutes(rt *Router, o Options) {
	a := &invitationRoutes{
		inv: o.Invitations, me: &authRoutes{auth: o.Auth, users: o.Users, settings: o.Settings, log: o.Logger},
		clients: o.Clients, limiter: o.Limiter,
	}
	rt.HandleFunc("POST /api/admin/invitations", Require(perm.InvitationsManage), a.create)
	rt.HandleFunc("GET /api/admin/invitations", Require(perm.InvitationsManage), a.list)
	rt.HandleFunc("POST /api/admin/invitations/{id}/revoke", Require(perm.InvitationsManage), a.revoke)
	rt.HandleFunc("POST /api/register", Public(), a.register)
}

type invitationRoutes struct {
	inv     *invitations.Service
	me      *authRoutes // for the signed-in answer
	clients *ratelimit.Clients
	limiter *ratelimit.Limiter
}

// invitationBody carries the code: only the routes that need invitations.manage send it.
type invitationBody struct {
	ID        int64      `json:"id"`
	Code      string     `json:"code"`
	Role      perm.Role  `json:"role"`
	CreatorID int64      `json:"creator_id"`
	CreatedAt time.Time  `json:"created_at"`
	Status    string     `json:"status"`
	UsedBy    *int64     `json:"used_by"`
	UsedAt    *time.Time `json:"used_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

func toInvitationBody(i invitations.Invitation) invitationBody {
	b := invitationBody{ID: i.ID, Code: i.Code, Role: i.Role, CreatorID: i.CreatorID, CreatedAt: i.CreatedAt, Status: i.Status()}
	if i.UsedBy != 0 {
		b.UsedBy = &i.UsedBy
	}
	if !i.UsedAt.IsZero() {
		b.UsedAt = &i.UsedAt
	}
	if !i.RevokedAt.IsZero() {
		b.RevokedAt = &i.RevokedAt
	}
	return b
}

func (a *invitationRoutes) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role perm.Role `json:"role"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	inv, err := a.inv.Create(r.Context(), auth.Caller(r.Context()).UserID, in.Role)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, toInvitationBody(inv))
}

func (a *invitationRoutes) list(w http.ResponseWriter, r *http.Request) {
	page, err := intParam(r, "page", 1)
	var size int
	if err == nil {
		size, err = intParam(r, "per_page", invitations.DefaultPageSize)
	}
	var p invitations.Page
	if err == nil {
		p, err = a.inv.List(r.Context(), page, size)
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := struct {
		Invitations []invitationBody `json:"invitations"`
		Page        int              `json:"page"`
		PerPage     int              `json:"per_page"`
		Total       int              `json:"total"`
	}{make([]invitationBody, 0, len(p.Invitations)), p.Page, p.PageSize, p.Total}
	for _, i := range p.Invitations {
		out.Invitations = append(out.Invitations, toInvitationBody(i))
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *invitationRoutes) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		id = 0 // no such invitation
	}
	inv, err := a.inv.Revoke(r.Context(), auth.Caller(r.Context()).UserID, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toInvitationBody(inv))
}

// register makes an account with an invitation code and signs it in. Each
// attempt counts against the address before the code is looked at; a valid
// code gives its count back but never resets it (security design §3).
func (a *invitationRoutes) register(w http.ResponseWriter, r *http.Request) {
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
	key := a.clients.Key(r)
	if ok, retry := a.limiter.Allow(ratelimit.Registration, key, ""); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		WriteError(w, r, apperr.New(apperr.Throttled, tooManyAttempts))
		return
	}
	id, token, err := a.inv.Register(r.Context(), invitations.Registration{
		Code: in.Code, Username: in.Username, DisplayName: in.DisplayName, Password: in.Password,
	})
	if err != nil {
		// Only a code that didn't work is a failed guess.
		if !errors.Is(err, invitations.ErrInvalidCode) {
			a.limiter.Forgive(ratelimit.Registration, key)
		}
		WriteError(w, r, err)
		return
	}
	a.limiter.Forgive(ratelimit.Registration, key)
	// The browser's old session, if it had one, ends: a token it presents is never adopted.
	if c := auth.Caller(r.Context()); c != nil {
		_ = a.me.auth.SignOut(r.Context(), c.SessionID)
	}
	body, err := a.me.meBody(r, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	auth.SetCookie(w, token)
	WriteJSON(w, http.StatusCreated, body)
}

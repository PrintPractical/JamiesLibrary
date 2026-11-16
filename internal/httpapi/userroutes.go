package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/useradmin"
	"jamieslibrary/internal/users"
)

// registerUserRoutes registers user administration (ADMIN-2, ACCESS-7). The
// rules about who may change whom live in useradmin, not here.
func registerUserRoutes(rt *Router, o Options) {
	a := &userRoutes{users: o.Users, admin: o.UserAdmin}
	rt.HandleFunc("GET /api/admin/users", Require(perm.UsersManage), a.list)
	rt.HandleFunc("PUT /api/admin/users/{id}/role", Require(perm.UsersPermissions), a.changeRole)
	rt.HandleFunc("PUT /api/admin/users/{id}/permissions/{permission}", Require(perm.UsersPermissions), a.setPermission)
	rt.HandleFunc("POST /api/admin/users/{id}/deactivate", Require(perm.UsersManage), a.deactivate)
	rt.HandleFunc("POST /api/admin/users/{id}/reactivate", Require(perm.UsersManage), a.reactivate)
	rt.HandleFunc("POST /api/admin/users/{id}/reset-password", Require(perm.UsersManage), a.resetPassword)
}

type userRoutes struct {
	users *users.Service
	admin *useradmin.Service
}

// differsBody says how a user's permissions differ from their role's preset.
type differsBody struct {
	Differs bool     `json:"differs"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

type adminUserBody struct {
	ID                 int64       `json:"id"`
	Username           string      `json:"username"`
	DisplayName        string      `json:"display_name"`
	Role               perm.Role   `json:"role"`
	Active             bool        `json:"active"`
	MustChangePassword bool        `json:"must_change_password"`
	Permissions        []string    `json:"permissions"`
	FromPreset         differsBody `json:"from_preset"`
}

func toAdminUser(u users.User) adminUserBody {
	d := u.DiffFromPreset()
	return adminUserBody{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role, Active: u.Active,
		MustChangePassword: u.MustChangePassword,
		Permissions:        perm.Strings(u.Permissions.List()),
		FromPreset:         differsBody{Differs: !d.Empty(), Added: perm.Strings(d.Added), Removed: perm.Strings(d.Removed)},
	}
}

type adminUserPage struct {
	Users   []adminUserBody `json:"users"`
	Page    int             `json:"page"`
	PerPage int             `json:"per_page"`
	Total   int             `json:"total"`
}

func (a *userRoutes) list(w http.ResponseWriter, r *http.Request) {
	page, err := intParam(r, "page", 1)
	if err == nil {
		var size int
		if size, err = intParam(r, "per_page", users.DefaultListPageSize); err == nil {
			var p users.UserPage
			if p, err = a.users.List(r.Context(), page, size); err == nil {
				out := adminUserPage{Users: make([]adminUserBody, 0, len(p.Users)), Page: p.Page, PerPage: p.PageSize, Total: p.Total}
				for _, u := range p.Users {
					out.Users = append(out.Users, toAdminUser(u))
				}
				WriteJSON(w, http.StatusOK, out)
				return
			}
		}
	}
	WriteError(w, r, err)
}

// targetID reads the user ID in the path. One that isn't a number is a user who doesn't exist.
func targetID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.New(apperr.NotFound, "There is no such user.")
	}
	return id, nil
}

func (a *userRoutes) changeRole(w http.ResponseWriter, r *http.Request) {
	id, err := targetID(r)
	var in struct {
		Role perm.Role `json:"role"`
	}
	if err == nil {
		err = DecodeJSON(w, r, &in)
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	u, err := a.admin.ChangeRole(r.Context(), auth.Caller(r.Context()).UserID, id, in.Role)
	a.respond(w, r, u, err)
}

func (a *userRoutes) setPermission(w http.ResponseWriter, r *http.Request) {
	id, err := targetID(r)
	var in struct {
		On *bool `json:"on"`
	}
	if err == nil {
		if err = DecodeJSON(w, r, &in); err == nil && in.On == nil {
			err = apperr.OnField(apperr.Invalid, "on", "Say whether the permission is on or off.")
		}
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	u, err := a.admin.SetPermission(r.Context(), auth.Caller(r.Context()).UserID, id, perm.Permission(r.PathValue("permission")), *in.On)
	a.respond(w, r, u, err)
}

func (a *userRoutes) deactivate(w http.ResponseWriter, r *http.Request) {
	a.activeChange(w, r, a.admin.Deactivate)
}

func (a *userRoutes) reactivate(w http.ResponseWriter, r *http.Request) {
	a.activeChange(w, r, a.admin.Reactivate)
}

func (a *userRoutes) activeChange(w http.ResponseWriter, r *http.Request,
	f func(ctx context.Context, actor, target int64) (users.User, error)) {
	id, err := targetID(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	u, err := f(r.Context(), auth.Caller(r.Context()).UserID, id)
	a.respond(w, r, u, err)
}

func (a *userRoutes) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := targetID(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	u, temp, err := a.admin.ResetPassword(r.Context(), auth.Caller(r.Context()).UserID, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// Shown once, to the administrator only: the response is never cached (sec §8).
	WriteJSON(w, http.StatusOK, struct {
		User              adminUserBody `json:"user"`
		TemporaryPassword string        `json:"temporary_password"`
	}{toAdminUser(u), temp})
}

func (a *userRoutes) respond(w http.ResponseWriter, r *http.Request, u users.User, err error) {
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toAdminUser(u))
}

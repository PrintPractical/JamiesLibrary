package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/perm"
)

// registerActivityRoutes registers the administrative activity log (ADMIN-4).
func registerActivityRoutes(rt *Router, o Options) {
	a := &activityRoutes{svc: o.Activity}
	rt.HandleFunc("GET /api/admin/activity", Require(perm.ActivityView), a.list)
}

type activityRoutes struct{ svc *activity.Service }

type activityActor struct {
	ID          *int64 `json:"id"` // null for the server console
	DisplayName string `json:"display_name"`
}

type activityEntry struct {
	ID     int64         `json:"id"`
	At     time.Time     `json:"at"`
	Actor  activityActor `json:"actor"`
	Action string        `json:"action"`
	Target string        `json:"target"`
	// TargetName is the display name when the target is a person ("" otherwise).
	TargetName string          `json:"target_name"`
	Details    json.RawMessage `json:"details"`
}

type activityPage struct {
	Entries []activityEntry `json:"entries"`
	Page    int             `json:"page"`
	PerPage int             `json:"per_page"`
	Total   int             `json:"total"`
}

// intParam reads an optional whole-number query parameter.
func intParam(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, apperr.OnField(apperr.Invalid, name, "That should be a whole number.")
	}
	return n, nil
}

func (a *activityRoutes) list(w http.ResponseWriter, r *http.Request) {
	page, err := intParam(r, "page", 1)
	if err == nil {
		var size int
		if size, err = intParam(r, "per_page", activity.DefaultPageSize); err == nil {
			var p activity.Page
			if p, err = a.svc.List(r.Context(), page, size); err == nil {
				WriteJSON(w, http.StatusOK, toActivityPage(p))
				return
			}
		}
	}
	WriteError(w, r, err)
}

func toActivityPage(p activity.Page) activityPage {
	out := activityPage{Entries: make([]activityEntry, 0, len(p.Entries)), Page: p.Page, PerPage: p.PageSize, Total: p.Total}
	for _, e := range p.Entries {
		actor := activityActor{DisplayName: e.ActorName}
		if e.ActorID != 0 {
			id := e.ActorID
			actor.ID = &id
		}
		out.Entries = append(out.Entries, activityEntry{ID: e.ID, At: e.At, Actor: actor, Action: e.Action, Target: e.Target, TargetName: e.TargetName, Details: e.Details})
	}
	return out
}

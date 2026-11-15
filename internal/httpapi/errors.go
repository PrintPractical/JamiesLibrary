package httpapi

import (
	"log/slog"
	"net/http"

	"jamieslibrary/internal/apperr"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      apperr.Code `json:"code"`
	Message   string      `json:"message"`
	Field     string      `json:"field,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}

// WriteError sends err as the API's error body. An *apperr.Error is shown as
// it is; anything else is logged with the request ID and answered with a
// generic 500 that carries only that ID.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Code != apperr.Internal {
		WriteJSON(w, apperr.Status(e.Code), errorBody{errorDetail{Code: e.Code, Message: e.Message, Field: e.Field}})
		return
	}
	id := RequestID(r.Context())
	logger(r).Error("unexpected error", "request_id", id, "error", err)
	WriteJSON(w, http.StatusInternalServerError, errorBody{errorDetail{
		Code:      apperr.Internal,
		Message:   "Something went wrong on our side. If it keeps happening, quote the request ID " + id + ".",
		RequestID: id,
	}})
}

func logger(r *http.Request) *slog.Logger {
	if l, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

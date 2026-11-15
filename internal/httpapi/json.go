package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"jamieslibrary/internal/apperr"
)

// MaxJSONBody is the largest JSON request body the API reads.
const MaxJSONBody = 1 << 20

// WriteJSON sends v as the response body with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON reads one JSON value from the request body into dst. The body is
// limited to MaxJSONBody, unknown fields are refused, and anything after the
// value is refused. The returned error is always an *apperr.Error.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return decodeError(err)
		}
		return apperr.New(apperr.BadRequest, "The request body must hold a single JSON value.")
	}
	return nil
}

func decodeError(err error) error {
	var tooBig *http.MaxBytesError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooBig):
		return apperr.New(apperr.TooLarge, "The request is too large.")
	case errors.Is(err, io.EOF):
		return apperr.New(apperr.BadRequest, "The request body is empty.")
	case errors.As(err, &typeErr):
		return apperr.OnField(apperr.BadRequest, typeErr.Field, "A value has the wrong type.")
	}
	var syn *json.SyntaxError
	if errors.As(err, &syn) || errors.Is(err, io.ErrUnexpectedEOF) {
		return apperr.New(apperr.BadRequest, "The request body is not valid JSON.")
	}
	// DisallowUnknownFields reports a plain error: json: unknown field "x".
	return apperr.New(apperr.BadRequest, "The request body has a field that isn't allowed.")
}

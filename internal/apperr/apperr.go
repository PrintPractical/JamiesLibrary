// Package apperr defines the typed error services return and its HTTP status mapping.
package apperr

import (
	"errors"
	"net/http"
)

// Code is a stable, machine-readable error identifier sent to API clients.
type Code string

const (
	BadRequest      Code = "bad_request"            // malformed input (400)
	Unauthorized    Code = "unauthorized"           // not signed in (401)
	Forbidden       Code = "forbidden"              // signed in but not allowed (403)
	NotFound        Code = "not_found"              // unknown thing (404)
	Conflict        Code = "conflict"               // duplicate or state clash (409)
	TooLarge        Code = "too_large"              // body over its limit (413)
	UnsupportedType Code = "unsupported_media_type" // body is not JSON (415)
	Invalid         Code = "invalid"                // well-formed but unacceptable input (422)
	Throttled       Code = "throttled"              // too many attempts (429)
	Internal        Code = "internal"               // unexpected failure (500)
	Unavailable     Code = "unavailable"            // a dependency is down (503)
)

// Error is what services return for anything a caller can act on.
// Message is a sentence a user can act on; Field names the input at fault, if one is.
type Error struct {
	Code    Code
	Message string
	Field   string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// New returns an Error with no field.
func New(code Code, message string) *Error { return &Error{Code: code, Message: message} }

// OnField returns an Error about the named input.
func OnField(code Code, field, message string) *Error {
	return &Error{Code: code, Message: message, Field: field}
}

// As reports whether err is, or wraps, an *Error.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Status maps a code to its HTTP status. Unknown codes are 500.
func Status(c Code) int {
	switch c {
	case BadRequest:
		return http.StatusBadRequest
	case Unauthorized:
		return http.StatusUnauthorized
	case Forbidden:
		return http.StatusForbidden
	case NotFound:
		return http.StatusNotFound
	case Conflict:
		return http.StatusConflict
	case TooLarge:
		return http.StatusRequestEntityTooLarge
	case UnsupportedType:
		return http.StatusUnsupportedMediaType
	case Invalid:
		return http.StatusUnprocessableEntity
	case Throttled:
		return http.StatusTooManyRequests
	case Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

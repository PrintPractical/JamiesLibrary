package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

type (
	requestIDKey struct{}
	loggerKey    struct{}
)

// RequestID returns the ID of the request the context belongs to.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // never fails on supported platforms
	return hex.EncodeToString(b)
}

// statusWriter records the status a handler sent.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// withRequest gives every request a fresh ID (also sent as X-Request-ID),
// recovers from panics with the API's 500, and logs one line per request.
// It logs the method, path, status and duration, and never the query or body.
func withRequest(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		rl := log.With("request_id", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		ctx = context.WithValue(ctx, loggerKey{}, rl)
		r = r.WithContext(ctx)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		defer func() {
			if p := recover(); p != nil {
				rl.Error("panic in handler", "panic", p)
				if !sw.wrote {
					WriteError(sw, r, errPanic)
				}
			}
			rl.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "remote", remoteHost(r),
				"duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(sw, r)
	})
}

type panicError struct{}

func (panicError) Error() string { return "handler panicked" }

var errPanic error = panicError{}

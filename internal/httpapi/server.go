// Package httpapi is the HTTP layer: the router, JSON and error helpers, and handlers.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"jamieslibrary/internal/apperr"
)

// Options are what the router needs from the program around it.
type Options struct {
	Version string
	Logger  *slog.Logger
	// Static serves the web app for every path that isn't /api or /media.
	Static http.Handler
}

// NewHandler builds the router: /api/..., /media/..., and the web app.
func NewHandler(o Options) http.Handler {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": o.Version})
	})
	// Unknown API and media paths are JSON 404s, never the web app.
	mux.HandleFunc("/api/", notFound)
	mux.HandleFunc("/media/", notFound)
	if o.Static != nil {
		mux.Handle("/", o.Static)
	}
	return withRequest(o.Logger, mux)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, apperr.New(apperr.NotFound, "There is nothing at "+cleanPath(r)+"."))
}

func cleanPath(r *http.Request) string {
	p := r.URL.Path
	if len(p) > 100 {
		p = p[:100] + "…"
	}
	return strings.ReplaceAll(p, "\n", " ")
}

// Server bounds: header size and timeouts (r1-architecture §13, security design §12).
const (
	MaxHeaderBytes    = 64 << 10
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 60 * time.Second
	WriteTimeout      = 60 * time.Second
	IdleTimeout       = 120 * time.Second
	ShutdownTimeout   = 10 * time.Second
)

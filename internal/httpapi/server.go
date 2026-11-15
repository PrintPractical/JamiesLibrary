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
	// PublicCatalogOn says whether the public-catalog setting is on; when nil it is off.
	PublicCatalogOn func() bool
	// TrustPort is the plain-HTTP port that serves the one-time trust step (0 when
	// there is none). The web app reads it to link the camera setup guide.
	TrustPort int
	// Authenticate, when set, wraps the routes and puts the caller in the
	// request context (auth.WithCaller). Without it no request has a caller.
	Authenticate func(http.Handler) http.Handler
	// Routes registers the API's routes, each with its access.
	Routes func(*Router)
}

// NewHandler builds the router: /api/..., /media/..., and the web app. It
// fails when any route has no valid access declaration, so the server doesn't
// start with a route nobody decided about.
func NewHandler(o Options) (http.Handler, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	rt, err := buildRouter(o)
	if err != nil {
		return nil, err
	}
	var h http.Handler = rt
	if o.Authenticate != nil {
		h = o.Authenticate(h)
	}
	return withRequest(o.Logger, h), nil
}

// buildRouter registers every route, the API's own first. It is separate from
// NewHandler so a test can walk the routes the server really has.
func buildRouter(o Options) (*Router, error) {
	rt := newRouter(o.PublicCatalogOn)
	rt.HandleFunc("GET /api/health", Public(), func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": o.Version})
	})
	rt.HandleFunc("GET /api/client-config", Public(), func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]int{"trust_port": o.TrustPort})
	})
	registerAPI(rt, o)
	if o.Routes != nil {
		o.Routes(rt)
	}
	// Unknown API and media paths are JSON 404s, never the web app.
	rt.HandleFunc("/api/", Public(), notFound)
	rt.HandleFunc("/media/", Public(), notFound)
	if o.Static != nil {
		rt.Handle("/", Public(), o.Static)
	}
	if err := rt.Err(); err != nil {
		return nil, err
	}
	return rt, nil
}

// registerAPI is where each feature registers its routes, every one with its access.
func registerAPI(rt *Router, o Options) {}

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

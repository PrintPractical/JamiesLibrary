package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
)

type accessKind int

const (
	undeclared accessKind = iota // the zero Access: no declaration
	requirePerm
	signedIn
	public
	publicCatalog
	mixed
)

// Access is what a route says about who may call it (ROLE-3, r1-architecture §4).
// Every route is registered with one; the zero Access is refused when the
// server builds its routes. Make one with Require, SignedIn, Public or PublicCatalog.
type Access struct {
	kind      accessKind
	perm      perm.Permission
	whileMust bool
	multipart bool
}

// Require lets in a signed-in caller who holds p, and answers 401 to a request
// with no caller and 403 to a caller without p.
func Require(p perm.Permission) Access { return Access{kind: requirePerm, perm: p} }

// SignedIn lets in any signed-in caller, whatever their permissions: the routes
// that are about the caller themselves (me, change password, sign out).
func SignedIn() Access { return Access{kind: signedIn} }

// Public lets anyone in: sign-in, registration, setup, branding, health and the
// app's static files.
func Public() Access { return Access{kind: public} }

// PublicCatalog lets anyone in while the public-catalog setting is on, and
// answers 404 as if the route did not exist while it is off (sec §7).
func PublicCatalog() Access { return Access{kind: publicCatalog} }

// Mixed lets the request reach the handler, which decides who may see what: the
// one route whose access depends on the thing asked for (/media/{name}: the logo
// is public, other images follow the catalog's rules).
func Mixed() Access { return Access{kind: mixed} }

// AllowWhileMustChange marks a Require or SignedIn route a caller who must
// change their password may still reach (ACCESS-7): only me, change password
// and sign out carry it.
func (a Access) AllowWhileMustChange() Access { a.whileMust = true; return a }

// Multipart lets the route take a multipart/form-data body (an image upload).
// Every other state-changing route takes only application/json (sec §8).
func (a Access) Multipart() Access { a.multipart = true; return a }

// String describes the access for logs and tests.
func (a Access) String() string {
	switch a.kind {
	case requirePerm:
		return "require " + string(a.perm)
	case signedIn:
		return "signed in"
	case public:
		return "public"
	case publicCatalog:
		return "public catalog"
	case mixed:
		return "decided by the handler"
	}
	return "undeclared"
}

// RouteInfo is one registered route and its declaration.
type RouteInfo struct {
	Pattern string
	Access  Access
}

// Router is the one route table. It is an http.Handler once built.
type Router struct {
	mux      *http.ServeMux
	routes   []RouteInfo
	problems []string
	publicOn func() bool
}

func newRouter(publicCatalogOn func() bool) *Router {
	if publicCatalogOn == nil {
		publicCatalogOn = func() bool { return false }
	}
	return &Router{mux: http.NewServeMux(), publicOn: publicCatalogOn}
}

// Handle registers pattern (a net/http ServeMux pattern) with its access. A
// route with no declaration, a nil handler or a pattern the mux refuses is
// recorded, and the server does not start (fail closed).
func (r *Router) Handle(pattern string, a Access, h http.Handler) {
	if h == nil {
		r.problems = append(r.problems, fmt.Sprintf("route %q has no handler", pattern))
		return
	}
	switch {
	case a.kind == undeclared:
		r.problems = append(r.problems, fmt.Sprintf("route %q declares no access: use Require, SignedIn, Public or PublicCatalog", pattern))
		return
	case a.whileMust && a.kind != requirePerm && a.kind != signedIn:
		r.problems = append(r.problems, fmt.Sprintf("route %q: AllowWhileMustChange only fits a route that needs a signed-in caller", pattern))
		return
	case a.kind == requirePerm && !perm.Valid(a.perm):
		r.problems = append(r.problems, fmt.Sprintf("route %q requires %q, which is not a permission", pattern, a.perm))
		return
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				r.problems = append(r.problems, fmt.Sprintf("route %q: %v", pattern, p))
			}
		}()
		r.mux.Handle(pattern, r.guard(a, h))
		r.routes = append(r.routes, RouteInfo{Pattern: pattern, Access: a})
	}()
}

// HandleFunc is Handle for a function.
func (r *Router) HandleFunc(pattern string, a Access, h http.HandlerFunc) {
	if h == nil {
		r.Handle(pattern, a, nil)
		return
	}
	r.Handle(pattern, a, h)
}

// Routes lists every registered route, in order.
func (r *Router) Routes() []RouteInfo { return append([]RouteInfo(nil), r.routes...) }

// Err is nil when every route was registered with a valid declaration.
func (r *Router) Err() error {
	if len(r.problems) == 0 {
		return nil
	}
	sort.Strings(r.problems)
	return errors.New("cannot build the routes: " + strings.Join(r.problems, "; "))
}

// ServeHTTP routes the request.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

// guard is the one place a route's declaration is enforced.
func (r *Router) guard(a Access, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch a.kind {
		case public, mixed:
		case publicCatalog:
			if !r.publicOn() {
				notFound(w, req)
				return
			}
		case requirePerm, signedIn:
			c := auth.Caller(req.Context())
			switch {
			case c == nil:
				WriteError(w, req, apperr.New(apperr.Unauthorized, "Sign in to continue."))
				return
			case c.MustChangePassword && !a.whileMust:
				WriteError(w, req, apperr.New(apperr.Forbidden, "Choose a new password before you do anything else."))
				return
			case a.kind == requirePerm && !c.Has(a.perm):
				WriteError(w, req, apperr.New(apperr.Forbidden, "You don't have permission to do that."))
				return
			}
		default:
			WriteError(w, req, apperr.New(apperr.Forbidden, "You don't have permission to do that."))
			return
		}
		if !safeMethod(req.Method) && !bodyAllowed(req, a.multipart) {
			WriteError(w, req, apperr.New(apperr.UnsupportedType, "The request body must be JSON."))
			return
		}
		h.ServeHTTP(w, req)
	})
}

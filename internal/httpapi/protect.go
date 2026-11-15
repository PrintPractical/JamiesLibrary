package httpapi

import (
	"mime"
	"net/http"
	"strings"
)

// The headers every response carries (security design §8). HSTS is added only
// to responses sent over HTTPS: sent over HTTP it would mean nothing.
const (
	contentSecurityPolicy = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; " +
		"img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; font-src 'self'; " +
		"worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	permissionsPolicy = "camera=(self), microphone=(), geolocation=()"
	hsts              = "max-age=31536000"
)

// withProtections sets the browser-protection headers, and refuses a
// cross-origin state-changing request (Go's CrossOriginProtection, with no
// origin trusted). No CORS header is ever sent.
func withProtections(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	guarded := cop.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", permissionsPolicy)
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", hsts)
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		guarded.ServeHTTP(w, r)
	})
}

// safeMethod is a method that changes nothing, so it needs no body check.
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// bodyAllowed says whether a state-changing request's body has an acceptable
// type: application/json, or multipart/form-data where the route allows it. A
// request with no body at all passes (sign-out has none).
func bodyAllowed(r *http.Request, multipart bool) bool {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return true
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mt == "application/json" || (multipart && mt == "multipart/form-data")
}

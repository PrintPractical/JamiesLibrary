package tlsca

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// PlainHandler answers on the plain-HTTP port when the app is served over
// HTTPS (security design §11). It serves only /trust/ (the CA downloads and a
// page), and sends everything else to HTTPS on the host the request used:
// there is no API, sign-in or cookie here. ca is nil when the owner brings
// their own certificate, and then there is nothing to download.
func PlainHandler(ca *CA, httpsPort int) http.Handler {
	h := &plain{ca: ca, httpsPort: httpsPort}
	// Not a ServeMux: it would tidy paths with redirects of its own over plain HTTP.
	return h.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/trust/ca.crt" && ca != nil:
			h.download("application/x-x509-ca-cert", "jamies-library-ca.crt", ca.RootDER)(w, r)
		case p == "/trust/ca.mobileconfig" && ca != nil:
			h.download("application/x-apple-aspen-config", "jamies-library-ca.mobileconfig", ca.mobileconfig)(w, r)
		case p == "/trust/":
			h.guide(w, r)
		case strings.HasPrefix(p, "/trust/assets/"):
			h.asset(w, r, strings.TrimPrefix(p, "/trust/assets/"))
		default:
			h.redirect(w, r)
		}
	}))
}

type plain struct {
	ca        *CA
	httpsPort int
}

// guard lets only GET and HEAD through, so nothing a person types is ever
// read from a connection that isn't encrypted, and marks every answer as
// something a browser must not interpret.
func (h *plain) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Only GET is answered over plain HTTP. Use https:// instead.", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *plain) download(contentType, filename string, body func() []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := body()
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != http.MethodHead {
			_, _ = w.Write(b)
		}
	}
}

// redirect sends the request to the same path on HTTPS, on the host name or
// address the request used.
func (h *plain) redirect(w http.ResponseWriter, r *http.Request) {
	host, ok := redirectHost(r.Host)
	if !ok {
		http.Error(w, "Open this library with its address or name, such as https://192.168.1.20:8443/.", http.StatusBadRequest)
		return
	}
	if h.httpsPort != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(h.httpsPort))
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u := url.URL{Scheme: "https", Host: host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusTemporaryRedirect)
}

// redirectHost is the host part of a Host header without its port, as it goes
// into a URL (IPv6 addresses keep no brackets: JoinHostPort adds them). Anything
// that is not a host name or an IP address is refused.
func redirectHost(hostport string) (string, bool) {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if len(host) > 1 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	if ip := net.ParseIP(host); ip != nil {
		return host, true
	}
	if sniName(host) != "" {
		return host, true
	}
	return "", false
}

// mobileconfig is an iOS configuration profile holding the root certificate
// and nothing else.
func (c *CA) mobileconfig() []byte {
	fp := sha256.Sum256(c.root.Raw)
	id := fmt.Sprintf("%x", fp[:6])
	name := html.EscapeString(c.root.Subject.CommonName)
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>jamies-library-ca.crt</string>
			<key>PayloadContent</key>
			<data>` + base64.StdEncoding.EncodeToString(c.root.Raw) + `</data>
			<key>PayloadDescription</key>
			<string>Adds the certificate of this Jamie's Library installation.</string>
			<key>PayloadDisplayName</key>
			<string>` + name + `</string>
			<key>PayloadIdentifier</key>
			<string>library.jamie.ca.` + id + `.root</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>` + uuidFrom(fp[:], 1) + `</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>Lets this device open Jamie's Library over HTTPS without a warning. It trusts only this installation's own names and local-network addresses.</string>
	<key>PayloadDisplayName</key>
	<string>` + name + `</string>
	<key>PayloadIdentifier</key>
	<string>library.jamie.ca.` + id + `</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>` + uuidFrom(fp[:], 2) + `</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`)
}

// uuidFrom makes a stable UUID-shaped identifier from the fingerprint, so
// downloading the profile again names the same profile.
func uuidFrom(sum []byte, n byte) string {
	h := sha256.Sum256(append([]byte{n}, sum...))
	return fmt.Sprintf("%X-%X-%X-%X-%X", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

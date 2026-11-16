package tlsca

import (
	"crypto/x509"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func get(h http.Handler, method, target, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func plainHandler(t *testing.T) (http.Handler, *CA) {
	now := t0
	ca := testCA(t, t.TempDir(), &now, nil)
	return PlainHandler(ca, 8443), ca
}

func TestPlainListenerServesTheRootAsDER(t *testing.T) {
	h, ca := plainHandler(t)
	w := get(h, "GET", "/trust/ca.crt", "192.168.1.20:8080")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	cert, err := x509.ParseCertificate(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(cert) != ca.Fingerprint() || !cert.IsCA || cert.MaxPathLen != 1 {
		t.Fatal("not the root")
	}
	if w := get(h, "HEAD", "/trust/ca.crt", "x"); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %d with %d bytes", w.Code, w.Body.Len())
	}
}

func TestMobileconfigHoldsOnlyTheRootPayload(t *testing.T) {
	h, ca := plainHandler(t)
	w := get(h, "GET", "/trust/ca.mobileconfig", "192.168.1.20:8080")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	body := w.Body.String()
	if n := strings.Count(body, "<key>PayloadType</key>"); n != 2 {
		t.Errorf("%d payload types, want the profile and one certificate", n)
	}
	if n := strings.Count(body, "<string>com.apple.security.root</string>"); n != 1 {
		t.Errorf("%d root payloads", n)
	}
	datas := regexp.MustCompile(`<data>([^<]*)</data>`).FindAllStringSubmatch(body, -1)
	if len(datas) != 1 {
		t.Fatalf("%d data blocks, want only the root certificate", len(datas))
	}
	der, err := base64.StdEncoding.DecodeString(datas[0][1])
	if err != nil || string(der) != string(ca.RootDER()) {
		t.Fatal("the payload is not the root certificate")
	}
	if strings.Contains(body, "PRIVATE") || strings.Contains(body, "intermediate") {
		t.Fatal("more than the root in the profile")
	}
	if again := get(h, "GET", "/trust/ca.mobileconfig", "x").Body.String(); again != body {
		t.Fatal("the profile differs between downloads, so a device would see two profiles")
	}
}

func TestPlainListenerRedirectsEverythingElseToHTTPS(t *testing.T) {
	h, _ := plainHandler(t)
	for _, tc := range []struct{ target, host, want string }{
		{"/", "192.168.1.20:8080", "https://192.168.1.20:8443/"},
		{"/books/3?q=a%20b&x=1", "library.local", "https://library.local:8443/books/3?q=a%20b&x=1"},
		{"/signin", "[fe80::1]:8080", "https://[fe80::1]:8443/signin"},
		{"/a%2Fb", "NAS.lan:80", "https://NAS.lan:8443/a%2Fb"},
		{"//evil.example/x", "library.local", "https://library.local:8443//evil.example/x"},
		{"/trust/other", "library.local", "https://library.local:8443/trust/other"},
		{"/trust", "library.local", "https://library.local:8443/trust"},
	} {
		w := get(h, "GET", tc.target, tc.host)
		if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != tc.want {
			t.Errorf("%s on %s: %d %q, want %q", tc.target, tc.host, w.Code, w.Header().Get("Location"), tc.want)
		}
	}
	// Port 443 is left off.
	if w := get(PlainHandler(nil, 443), "GET", "/x", "[::1]:8080"); w.Header().Get("Location") != "https://[::1]/x" {
		t.Errorf("got %q", w.Header().Get("Location"))
	}
}

func TestPlainListenerRefusesOddHostsAndMethods(t *testing.T) {
	h, _ := plainHandler(t)
	for _, host := range []string{"", "evil.com/x", "a b", "user@evil.com", "x_y.local", "[fe80::1%25eth0]:80"} {
		if w := get(h, "GET", "/", host); w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
			t.Errorf("host %q: %d %q", host, w.Code, w.Header().Get("Location"))
		}
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		w := get(h, m, "/api/sessions", "library.local")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Location") != "" {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
}

// The plain-HTTP port has no API, no sign-in and no cookies.
func TestPlainListenerServesNoAPI(t *testing.T) {
	h, _ := plainHandler(t)
	for _, p := range []string{"/api/health", "/api/me", "/api/sessions", "/api/", "/media/logo.png", "/trust/../api/health"} {
		w := get(h, "GET", p, "library.local")
		b, _ := io.ReadAll(w.Body)
		if w.Code != http.StatusTemporaryRedirect || !strings.HasPrefix(w.Header().Get("Location"), "https://") ||
			len(b) > 200 || strings.Contains(string(b), `"status"`) || w.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s: %d %q %q", p, w.Code, w.Header().Get("Location"), b)
		}
	}
}

func TestTrustPageIsHTMLAndNeedsNoCAInOwnersMode(t *testing.T) {
	h, _ := plainHandler(t)
	w := get(h, "GET", "/trust/", "library.local")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("no nosniff")
	}
	// With the owner's own certificate there is no CA to download.
	own := PlainHandler(nil, 8443)
	if w := get(own, "GET", "/trust/ca.crt", "x"); w.Code != http.StatusTemporaryRedirect {
		t.Errorf("ca.crt without a CA: %d", w.Code)
	}
}

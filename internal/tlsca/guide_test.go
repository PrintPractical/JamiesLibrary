package tlsca

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const (
	uaIPhone  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	uaAndroid = "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36"
	uaWindows = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
)

func guideAs(h http.Handler, ua string) string {
	r := httptest.NewRequest("GET", "/trust/", nil)
	r.Host = "192.168.1.20:8080"
	r.Header.Set("User-Agent", ua)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Body.String()
}

// sectionOrder lists the device sections in the order they appear.
func sectionOrder(page string) []string {
	var keys []string
	for _, m := range regexp.MustCompile(`<(?:section|details) class="card" id="(ios|android|desktop)"`).FindAllStringSubmatch(page, -1) {
		keys = append(keys, m[1])
	}
	return keys
}

func TestGuideShowsTheDevicesStepsFirstAndTheOthersOneTapAway(t *testing.T) {
	h, _ := plainHandler(t)
	for _, tc := range []struct {
		name, ua, first, step string
	}{
		{"iPhone", uaIPhone, "ios", "Certificate Trust Settings"},
		{"Android", uaAndroid, "android", "CA certificate"},
		{"desktop", uaWindows, "desktop", "Trusted Root Certification Authorities"},
		{"no User-Agent", "", "desktop", "Trusted Root Certification Authorities"},
	} {
		page := guideAs(h, tc.ua)
		order := sectionOrder(page)
		if len(order) != 3 || order[0] != tc.first {
			t.Errorf("%s: sections %v, want %s first of three", tc.name, order, tc.first)
		}
		if !strings.Contains(page, `<section class="card" id="`+tc.first+`"`) {
			t.Errorf("%s: the first section is not open", tc.name)
		}
		if n := strings.Count(page, "<details"); n != 2 {
			t.Errorf("%s: %d collapsed sections, want the other two", tc.name, n)
		}
		if !strings.Contains(page, tc.step) {
			t.Errorf("%s: missing step %q", tc.name, tc.step)
		}
	}
	ios := guideAs(h, uaIPhone)
	for _, want := range []string{"/trust/ca.mobileconfig", "General › About › Certificate Trust Settings", "first 8", "last 8", "https://192.168.1.20:8443/"} {
		if !strings.Contains(ios, want) {
			t.Errorf("iOS guide lacks %q", want)
		}
	}
	if !strings.Contains(guideAs(h, uaAndroid), "/trust/ca.crt") {
		t.Error("Android guide does not link the certificate")
	}
}

func TestGuideFingerprintIsTheRoots(t *testing.T) {
	h, ca := plainHandler(t)
	page := guideAs(h, uaIPhone)
	m := regexp.MustCompile(`(?s)<code class="fp" id="fingerprint">(.*?)</code>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no fingerprint on the page")
	}
	shown := regexp.MustCompile(`</?b>|<wbr>`).ReplaceAllString(m[1], "")
	if shown != ca.Fingerprint() {
		t.Fatalf("shown %q, root %q", shown, ca.Fingerprint())
	}
	if n := strings.Count(m[1], "<b>"); n != 8 {
		t.Errorf("%d bold groups, want 4 pairs at each end", n)
	}
	// What the button downloads is the certificate that fingerprint names.
	w := get(h, "GET", "/trust/ca.crt", "x")
	cert, err := x509.ParseCertificate(w.Body.Bytes())
	if err != nil || Fingerprint(cert) != shown {
		t.Fatal("the downloaded root is not the one the guide names")
	}
	if blk, _ := pem.Decode(w.Body.Bytes()); blk != nil {
		t.Fatal("ca.crt should be DER, as Android installs it")
	}
}

func TestGuideDownloadsWorkOverPlainHTTP(t *testing.T) {
	h, _ := plainHandler(t)
	page := guideAs(h, uaIPhone)
	for _, path := range []string{"/trust/ca.crt", "/trust/ca.mobileconfig"} {
		if !strings.Contains(page, `href="`+path+`"`) {
			t.Errorf("the guide does not link %s", path)
		}
		w := get(h, "GET", path, "192.168.1.20:8080")
		if w.Code != 200 || w.Body.Len() == 0 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
			t.Errorf("%s: %d %v", path, w.Code, w.Header())
		}
	}
}

func TestGuideLoadsNothingFromAnotherHost(t *testing.T) {
	h, _ := plainHandler(t)
	for _, ua := range []string{uaIPhone, uaAndroid, uaWindows} {
		page := guideAs(h, ua)
		// The one link to another origin is the button to the library itself, on the host the request used.
		own := strings.ReplaceAll(page, `href="https://192.168.1.20:8443/"`, "")
		if refs := regexp.MustCompile(`(?:src|href|action)="(?:https?:)?//[^"]*"|https?://[^"\s<]*`).FindAllString(own, -1); len(refs) > 0 {
			t.Errorf("%v", refs)
		}
		for _, ref := range regexp.MustCompile(`(?:src|href)="(/[^"]*)"`).FindAllStringSubmatch(page, -1) {
			if strings.HasPrefix(ref[1], "/trust/") && get(h, "GET", ref[1], "x").Code != 200 {
				t.Errorf("%s does not load", ref[1])
			}
		}
		if strings.Contains(page, "<script") || strings.Contains(page, "style=") || strings.Contains(page, "<style") {
			t.Error("inline script or style")
		}
	}
	css := get(h, "GET", "/trust/assets/guide.css", "x")
	if css.Code != 200 || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("css %d %v", css.Code, css.Header())
	}
	if strings.Contains(css.Body.String(), "http") {
		t.Error("the stylesheet names another host")
	}
	for _, u := range regexp.MustCompile(`url\("([^"]+)"\)`).FindAllStringSubmatch(css.Body.String(), -1) {
		w := get(h, "GET", "/trust/assets/"+u[1], "x")
		if w.Code != 200 || w.Header().Get("Content-Type") != "font/woff2" || w.Body.Len() < 1000 {
			t.Errorf("font %s: %d", u[1], w.Code)
		}
	}
	if csp := get(h, "GET", "/trust/", "x").Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("csp %q", csp)
	}
}

func TestGuideAssetsAreOnlyTheStylesheetAndFonts(t *testing.T) {
	h, _ := plainHandler(t)
	for _, p := range []string{"/trust/assets/guide.html", "/trust/assets/", "/trust/assets/fonts/", "/trust/assets/fonts/nope.woff2", "/trust/assets/../ca.go"} {
		if w := get(h, "GET", p, "x"); w.Code == 200 && !strings.Contains(w.Header().Get("Location"), "https://") {
			t.Errorf("%s answered 200", p)
		}
	}
}

func TestGuideSaysWhatTheDeviceTrusts(t *testing.T) {
	h, _ := plainHandler(t)
	page := guideAs(h, uaAndroid)
	for _, want := range []string{"What your device will trust", "this library installation", "private network addresses", "public website"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestGuideInOwnersCertificateModeNeedsNoStep(t *testing.T) {
	h := PlainHandler(nil, 443)
	page := guideAs(h, uaIPhone)
	if !strings.Contains(page, "No step is needed") || !strings.Contains(page, `href="https://192.168.1.20/"`) {
		t.Fatalf("%s", page)
	}
	if strings.Contains(page, "ca.crt") || strings.Contains(page, "ca.mobileconfig") || strings.Contains(page, "fingerprint") {
		t.Error("offers a download in owner's certificate mode")
	}
}

func TestGuideEscapesTheHostAndRefusesNoOtherInput(t *testing.T) {
	h, _ := plainHandler(t)
	r := httptest.NewRequest("GET", "/trust/", nil)
	r.Host = `"><script>x</script>`
	r.Header.Set("User-Agent", `<script>alert(1)</script>`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `href="https://`) {
		t.Error("a button for a host that is not one")
	}
}

func TestGuideHEADHasNoBody(t *testing.T) {
	h, _ := plainHandler(t)
	if w := get(h, "HEAD", "/trust/", "x"); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("%d with %d bytes", w.Code, w.Body.Len())
	}
}

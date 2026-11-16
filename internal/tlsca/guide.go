package tlsca

import (
	"embed"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The guide's page, stylesheet and fonts: bundled, because the page is served
// over plain HTTP by a machine that may have no internet, and loads nothing
// from any other host.
//
//go:embed guide/guide.html guide/guide.css guide/fonts/*.woff2
var guideFS embed.FS

var guideTmpl = template.Must(template.ParseFS(guideFS, "guide/guide.html"))

// guideSection is the steps for one kind of device.
type guideSection struct {
	Key, Title, Intro, Download, DownloadLabel string
	// ExtraDownload, ExtraLabel and Command are a second way in for the
	// system: another file, and a line to paste into a terminal.
	ExtraDownload, ExtraLabel, Command string
	Steps                              []string
	Open                               bool
}

// macCommand checks the downloaded root's SHA-256 (printed before the
// password is asked for, so the person can stop with Control-C) and then makes
// it trusted for the whole Mac in the System keychain, which every Mac has,
// unlike a login keychain that is missing or not the default (error -25294).
const macCommand = "openssl x509 -inform der -in ~/Downloads/jamies-library-ca.crt -noout -fingerprint -sha256 && " +
	"sudo security add-trusted-cert -d -r trustRoot -p ssl -k /Library/Keychains/System.keychain ~/Downloads/jamies-library-ca.crt"

var guideSections = []guideSection{
	{
		Key: "ios", Title: "iPhone or iPad",
		Intro:         "Use Safari.",
		Download:      "/trust/ca.mobileconfig",
		DownloadLabel: "Download the profile",
		Steps: []string{
			"Tap “Download the profile” above, then tap Allow when Safari asks to download a configuration profile.",
			"Open the Settings app and tap “Profile Downloaded” near the top (or General › VPN & Device Management), then tap the profile.",
			"Before you tap Install, check it: tap More Details, tap the certificate, and compare its SHA-256 under Fingerprints with the one on the library's server console, or on the Settings page opened on another device that already trusts the library (never sign in on the device you are setting up). If they differ, don't install the profile, and tap Cancel.",
			"If they match, tap Install, enter your passcode and tap Install again.",
			"Go to Settings › General › About › Certificate Trust Settings. Under “Enable full trust for root certificates”, switch on the certificate named Jamie's Library local CA. Tap Continue.",
			"Come back to this page and use the button at the bottom to open the library.",
		},
	},
	{
		Key: "android", Title: "Android phone or tablet",
		Intro:         "Use Chrome.",
		Download:      "/trust/ca.crt",
		DownloadLabel: "Download the certificate",
		Steps: []string{
			"Tap “Download the certificate” above. If Chrome asks to keep the file, tap Download or Keep.",
			"Open Settings and search for “CA certificate” (it is usually under Security & privacy › More security settings › Encryption & credentials › Install a certificate). Choose CA certificate. Tap Install anyway when it warns you.",
			"Pick the file you just downloaded, jamies-library-ca.crt, from Downloads. Your phone may ask for your screen lock.",
			"Check it: go to Settings › Security (or Security & privacy) › Encryption & credentials › Trusted credentials › User, tap the Jamie's Library certificate, and compare its SHA-256 under Fingerprints with the one on the library's server console, or on the Settings page opened on another device that already trusts the library (never sign in on the device you are setting up). If they differ, tap Remove.",
			"Come back to Chrome and use the button at the bottom to open the library. If it still shows a warning, close Chrome and open it again.",
		},
	},
	{
		Key: "desktop", Title: "Windows, Mac or Linux computer",
		Intro:         "Chrome, Edge and Safari use the computer's own certificate list. Firefox keeps its own.",
		Download:      "/trust/ca.crt",
		DownloadLabel: "Download the certificate",
		ExtraDownload: "/trust/ca.mobileconfig",
		ExtraLabel:    "Mac: download the profile",
		Command:       macCommand,
		Steps: []string{
			"Tap or click “Download the certificate” above. On a Mac, you may use “Mac: download the profile” instead (see the next step).",
			"On a Mac, don't double-click the certificate: Keychain Access can refuse it with “Error: -25294” (no such keychain) when the login keychain is missing or isn't the default one. Instead, either (a) open the downloaded profile (jamies-library-ca.mobileconfig), then open System Settings › Privacy & Security › Profiles (on older macOS, System Preferences › Profiles), choose it and click Install, and enter your password; or (b) open Terminal and paste the command shown above the steps: it prints the certificate's SHA-256, then asks for your password to trust it, so press Control-C if the fingerprint isn't the one on the library's server console, or on the Settings page opened on another device that already trusts the library (never sign in on the device you are setting up). For (a), afterwards open Keychain Access, choose the System keychain, open the Jamie's Library certificate and compare its SHA-256 under Fingerprints with the same one; if they differ, remove the profile in System Settings.",
			"In Firefox on any computer: open Settings › Privacy & Security › View Certificates › Authorities › Import. Before you tick “Trust this CA to identify websites”, choose the certificate and View it, and compare its SHA-256 fingerprint with the one on the console, or on the Settings page opened on another device that already trusts the library (never sign in on the device you are setting up).",
			"On Windows: double-click the file, choose Install Certificate, then Current User, then “Place all certificates in the following store” and pick Trusted Root Certification Authorities. Windows' own window shows only a SHA-1 thumbprint, so check after the next step: open the library in Chrome or Edge, select the padlock (or site information) › certificate, open Details, select the top entry of the hierarchy and compare its SHA-256 fingerprint with the one on the console, or on the Settings page opened on another device that already trusts the library (never sign in on the device you are setting up). If they differ, remove the certificate from Trusted Root Certification Authorities.",
			"Restart your browser, then use the button at the bottom to open the library.",
		},
	},
}

// guideData is what the guide's page shows.
type guideData struct {
	Own         bool // the owner's own certificate: no step is needed
	Sections    []guideSection
	Fingerprint template.HTML
	HTTPSURL    string
	IPad        bool // the User-Agent could be an iPad that asks for desktop sites
}

// detectSystem names the kind of device a User-Agent describes: ios,
// android or desktop. An iPad that asks for desktop sites sends a Mac
// User-Agent and gets the desktop steps; guide opens the iPhone steps too for it.
func detectSystem(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		return "ios"
	case strings.Contains(ua, "Android"):
		return "android"
	}
	return "desktop"
}

// fingerprintHTML shows a colon-separated fingerprint with its first 8 and
// last 8 hex characters in bold, so a person knows what to compare. The text
// is the fingerprint unchanged: <wbr> lets it wrap and adds no character.
func fingerprintHTML(fp string) template.HTML {
	pairs := strings.Split(fp, ":")
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteString(":<wbr>")
		}
		bold := i < 4 || i >= len(pairs)-4
		if bold {
			b.WriteString("<b>")
		}
		b.WriteString(template.HTMLEscapeString(p))
		if bold {
			b.WriteString("</b>")
		}
	}
	return template.HTML(b.String())
}

// httpsURL is the library's address over HTTPS on the host the request used,
// or "" when the Host header names no host.
func (h *plain) httpsURL(r *http.Request) string {
	host, ok := redirectHost(r.Host)
	if !ok {
		return ""
	}
	if h.httpsPort != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(h.httpsPort))
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: "https", Host: host, Path: "/"}).String()
}

// guide serves the trust guide: the steps for the device's system first and
// open, the others one tap away.
func (h *plain) guide(w http.ResponseWriter, r *http.Request) {
	d := guideData{Own: h.ca == nil, HTTPSURL: h.httpsURL(r)}
	if h.ca != nil {
		d.Fingerprint = fingerprintHTML(h.ca.Fingerprint())
		ua := r.UserAgent()
		first := detectSystem(ua)
		// A Mac User-Agent may be an iPad asking for desktop sites, so its steps are open too.
		mac := strings.Contains(ua, "Macintosh")
		d.IPad = mac
		for _, s := range guideSections {
			s.Open = s.Key == first || (mac && s.Key == "ios")
			if s.Key == first {
				d.Sections = append([]guideSection{s}, d.Sections...)
			} else {
				d.Sections = append(d.Sections, s)
			}
		}
	}
	var b strings.Builder
	if err := guideTmpl.Execute(&b, d); err != nil {
		http.Error(w, "The guide could not be shown.", http.StatusInternalServerError)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-cache")
	hd.Set("Vary", "User-Agent")
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	hd.Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(b.String()))
	}
}

// asset serves the guide's stylesheet and fonts, only by exact name.
func (h *plain) asset(w http.ResponseWriter, r *http.Request, name string) {
	var data []byte
	var err error
	if name == "guide.css" || strings.HasPrefix(name, "fonts/") {
		data, err = guideFS.ReadFile("guide/" + name)
	} else {
		err = fs.ErrNotExist
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := "text/css; charset=utf-8"
	if strings.HasSuffix(name, ".woff2") {
		ct = "font/woff2"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

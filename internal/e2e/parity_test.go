package e2e

// DEPLOY-1: the library behaves the same on a home network and on the internet.
// One script (set-up, cataloguing by hand and by ISBN against the fake provider,
// covers, search, check-out, check-in) runs three ways:
//
//	lan          local CA, HTTPS from the first start, the client trusts only the
//	             CA it downloaded from the plain-HTTP guide port, no internet
//	proxy        plain HTTP behind a TLS-terminating reverse proxy that holds the
//	             owner's certificate, the app believing its X-Forwarded-For
//	owncert      the owner's certificate and key given to the app (JL_TLS_CERT)
//
// Each run writes down what it saw as a transcript (status, normalised body,
// cover hashes). The transcripts must be equal, and the lan one must also be the
// right answer, so three runs that fail the same way don't pass.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

// ownerCert is a certificate the owner brought (a stand-in for a public CA's):
// a root, and a leaf for 127.0.0.1 signed by it.
type ownerCert struct {
	pool              *x509.CertPool
	certFile, keyFile string
	pair              tls.Certificate
}

func newOwnerCert(t *testing.T) ownerCert {
	t.Helper()
	now := time.Now()
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Owner Test Root"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := x509.ParseCertificate(rootDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "library.example.org"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"library.example.org"},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(leafKey)
	dir := t.TempDir()
	o := ownerCert{pool: x509.NewCertPool(), certFile: filepath.Join(dir, "cert.pem"), keyFile: filepath.Join(dir, "key.pem")}
	o.pool.AddCert(root)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(o.certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if o.pair, err = tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	return o
}

// site is one way of running the library, as the person using it sees it.
type site struct {
	name   string
	base   string // what a browser would type
	client *http.Client
	setup  string // the first-run code
}

func httpsClient(pool *x509.CertPool) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// startLAN is an installation as the README describes it, on a network with no
// internet: only the downloaded CA is trusted.
func startLAN(t *testing.T, ol *testutil.FakeOL) *site {
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: ol, Env: map[string]string{"JL_HTTP_ONLY": ""}})
	_, path := downloadRoot(t, s)
	pemBytes, _ := os.ReadFile(path)
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("the downloaded CA is not a certificate")
	}
	code, ok := s.SetupCode()
	if !ok {
		t.Fatalf("no setup code:\n%s", s.Stdout())
	}
	return &site{name: "lan", base: s.URL, client: httpsClient(pool), setup: code}
}

// startOwnCert gives the app the owner's certificate and key directly.
func startOwnCert(t *testing.T, ol *testutil.FakeOL, oc ownerCert) *site {
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: ol, Env: map[string]string{
		"JL_HTTP_ONLY": "", "JL_TLS_CERT": oc.certFile, "JL_TLS_KEY": oc.keyFile}})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatalf("no setup code:\n%s", s.Stdout())
	}
	return &site{name: "owncert", base: s.URL, client: httpsClient(oc.pool), setup: code}
}

// startProxy is the compose.public.yml shape: the app speaks plain HTTP and trusts
// the proxy's address; the proxy holds the certificate and sets X-Forwarded-For.
func startProxy(t *testing.T, ol *testutil.FakeOL, oc ownerCert) *site {
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: ol, Env: map[string]string{
		"JL_HTTP_ONLY": "1", "JL_TRUSTED_PROXIES": "127.0.0.1/32"}})
	target, _ := url.Parse(s.URL)
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host
			r.SetXForwarded()
		},
	}
	front := httptest.NewUnstartedServer(rp)
	front.TLS = &tls.Config{Certificates: []tls.Certificate{oc.pair}, MinVersion: tls.VersionTLS12}
	front.StartTLS()
	t.Cleanup(front.Close)
	code, ok := s.SetupCode()
	if !ok {
		t.Fatalf("no setup code:\n%s", s.Stdout())
	}
	return &site{name: "proxy", base: front.URL, client: httpsClient(oc.pool), setup: code}
}

// transcript is what a run saw, step by step.
type transcript struct {
	t     *testing.T
	site  *site
	lines []string
	ids   map[string]string // volatile values, numbered in order of first sight
}

var (
	volatileKeys = map[string]bool{"code": true, "barcode": true, "copy_code": true, "card": true, "cover_token": true, "token": true}
	mediaName    = regexp.MustCompile(`/media(-tmp)?/[A-Za-z0-9._-]+`)
)

func (tr *transcript) alias(v string) string {
	if n, ok := tr.ids[v]; ok {
		return n
	}
	n := fmt.Sprintf("<v%d>", len(tr.ids)+1)
	tr.ids[v] = n
	return n
}

func (tr *transcript) scrub(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k) // sorted, so the same facts get the same aliases every run
		}
		sort.Strings(keys)
		for _, k := range keys {
			e := x[k]
			if s, ok := e.(string); ok && volatileKeys[k] && s != "" {
				x[k] = tr.alias(s)
				continue
			}
			if s, ok := e.(string); ok && k == "number" {
				x[k] = tr.alias(s) // a card number; a copy's number is a JSON number
				continue
			}
			x[k] = tr.scrub(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = tr.scrub(e)
		}
		return x
	case string:
		return mediaName.ReplaceAllStringFunc(x, func(m string) string { return tr.alias(m) })
	}
	return v
}

// call makes one request, records it, and returns the decoded answer.
func (tr *transcript) call(step, method, path string, body any, wantStatus int) (int, map[string]any) {
	tr.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, tr.site.base+path, rd)
	if err != nil {
		tr.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := tr.site.client.Do(req)
	if err != nil {
		tr.t.Fatalf("[%s] %s: %s %s: %v", tr.site.name, step, method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if resp.StatusCode != wantStatus {
		tr.t.Fatalf("[%s] %s: %s %s: want %d, got %d: %s", tr.site.name, step, method, path, wantStatus, resp.StatusCode, raw)
	}
	var cp map[string]any // scrub works on a copy: the script still needs the real values
	_ = json.Unmarshal(raw, &cp)
	norm, _ := json.Marshal(tr.scrub(cp)) // map keys come out sorted
	tr.lines = append(tr.lines, fmt.Sprintf("%s: %d %s", step, resp.StatusCode, norm))
	for _, ck := range resp.Cookies() { // the cookie's name and flags, never its value
		tr.lines = append(tr.lines, fmt.Sprintf("%s: cookie %s secure=%t httponly=%t samesite=%d path=%s",
			step, ck.Name, ck.Secure, ck.HttpOnly, ck.SameSite, ck.Path))
	}
	return resp.StatusCode, m
}

// fetch records a binary answer by its status, type and hash.
func (tr *transcript) fetch(step, path string, wantType string, hash bool) []byte {
	tr.t.Helper()
	resp, err := tr.site.client.Get(tr.site.base + path)
	if err != nil {
		tr.t.Fatalf("[%s] %s: %v", tr.site.name, step, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		tr.t.Fatalf("[%s] %s: GET %s: %d", tr.site.name, step, path, resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, wantType) {
		tr.t.Fatalf("[%s] %s: content type %q, want %s", tr.site.name, step, ct, wantType)
	}
	sum := "(random content)" // a card's image holds its random number
	if hash {
		h := sha256.Sum256(data)
		sum = "sha256 " + hex.EncodeToString(h[:8])
	}
	tr.lines = append(tr.lines, fmt.Sprintf("%s: 200 %s %d bytes %s", step, ct, len(data), sum))
	return data
}

func str(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

func num(m map[string]any, key string) int64 {
	f, _ := m[key].(float64)
	return int64(f)
}

// script is the one script every site runs.
func script(t *testing.T, st *site, ol *testutil.FakeOL) []string {
	t.Helper()
	tr := &transcript{t: t, site: st, ids: map[string]string{}}
	patron := &transcript{t: t, site: &site{name: st.name, base: st.base, client: cloneClient(st.client)}, ids: tr.ids}

	tr.call("setup admin", "POST", "/api/setup/admin", map[string]string{
		"code": st.setup, "username": "root", "display_name": "Root", "password": adminPassword}, 200)
	_, me := tr.call("who am I", "GET", "/api/me", nil, 200)
	if str(me, "user", "username") != "root" {
		t.Fatalf("[%s] signed in as %v", st.name, me)
	}

	// Cataloguing by hand, with a copy in a new location.
	_, hand := tr.call("add book by hand", "POST", "/api/books", map[string]any{
		"title": "The Gruffalo", "authors": []string{"Julia Donaldson"}, "description": "A mouse takes a walk."}, 201)
	handID := num(hand, "id")
	_, c1 := tr.call("add copy 1", "POST", fmt.Sprintf("/api/books/%d/copies", handID),
		map[string]any{"new_location": "Nursery shelf", "condition": "good"}, 201)
	_, c2 := tr.call("add copy 2", "POST", fmt.Sprintf("/api/books/%d/copies", handID),
		map[string]any{"location_id": num(c1, "location_id"), "condition": "good"}, 201)
	_ = c2

	// Cataloguing by ISBN against the fake provider: look up, then save with the cover.
	_, found := tr.call("lookup by isbn", "POST", "/api/catalog/lookup", map[string]string{"isbn": "978-0-14-032872-1"}, 200)
	if str(found, "status") != "found" || str(found, "title") != "Fantastic Mr Fox" || str(found, "cover_token") == "" {
		t.Fatalf("[%s] lookup: %v", st.name, found)
	}
	tr.fetch("cover preview", str(found, "cover_url"), "image/", true)
	_, isbnBook := tr.call("save looked-up book", "POST", "/api/books", map[string]any{
		"title": str(found, "title"), "authors": found["authors"], "isbn": str(found, "isbn"),
		"description": str(found, "description"), "cover_token": str(found, "cover_token")}, 201)
	isbnID := num(isbnBook, "id")
	cover := str(isbnBook, "cover_url")
	if cover == "" {
		t.Fatalf("[%s] saved book has no cover: %v", st.name, isbnBook)
	}
	_, ic := tr.call("add isbn copy", "POST", fmt.Sprintf("/api/books/%d/copies", isbnID),
		map[string]any{"location_id": num(c1, "location_id"), "condition": "new"}, 201)
	tr.call("lookup again says exists", "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780140328721"}, 200)
	tr.call("lookup unknown isbn", "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780306406157"}, 200)
	tr.fetch("saved cover", cover, "image/", true)
	tr.call("bad isbn", "POST", "/api/catalog/lookup", map[string]string{"isbn": "9780140328720"}, 422)

	// A patron registers with an invitation.
	_, inv := tr.call("invite patron", "POST", "/api/admin/invitations", map[string]string{"role": "patron"}, 201)
	patron.call("register patron", "POST", "/api/register", map[string]string{
		"code": str(inv, "code"), "username": "pat", "display_name": "Pat", "password": adminPassword}, 201)
	_, card := patron.call("patron card", "GET", "/api/me/card", nil, 200)
	number := str(card, "number")
	patron.fetch("patron card barcode", "/api/me/card/barcode.svg", "image/svg", false)

	// Search, as staff and as the patron.
	_, res := tr.call("search gruffalo", "GET", "/api/books?q=gruffalo", nil, 200)
	if num(res, "total") != 1 {
		t.Fatalf("[%s] search for gruffalo: %v", st.name, res)
	}
	tr.call("search fox, any case", "GET", "/api/books?q=FANTASTIC+mr", nil, 200)
	tr.call("search nothing", "GET", "/api/books?q=zzzzqq", nil, 200)
	patron.call("patron searches", "GET", "/api/books?q=fox", nil, 200)
	tr.call("book page", "GET", fmt.Sprintf("/api/books/%d", isbnID), nil, 200)
	patron.fetch("patron sees the cover", cover, "image/", true)

	// Check-out and check-in at the desk.
	_, res1 := tr.call("resolve the card", "POST", "/api/desk/resolve", map[string]string{"code": number, "step": "patron"}, 200)
	if str(res1, "kind") != "patron" {
		t.Fatalf("[%s] resolve: %v", st.name, res1)
	}
	copyCode := str(ic, "barcode")
	if copyCode == "" {
		copyCode = "9780140328721" // ISBN mode: the book's ISBN stands for its copy
	}
	_, loan := tr.call("check out", "POST", "/api/desk/checkout", map[string]string{"patron": number, "code": copyCode}, 200)
	if str(loan, "title") != "Fantastic Mr Fox" || str(loan, "due_date") == "" {
		t.Fatalf("[%s] checkout: %v", st.name, loan)
	}
	tr.call("availability after check-out", "GET", fmt.Sprintf("/api/books/%d", isbnID), nil, 200)
	patron.call("patron sees the loan", "GET", "/api/me/loans", nil, 200)
	tr.call("second check-out of the only copy is refused", "POST", "/api/desk/checkout",
		map[string]string{"patron": number, "code": copyCode}, 409)
	_, back := tr.call("check in", "POST", "/api/desk/checkin", map[string]string{"code": copyCode}, 200)
	if str(back, "status") != "returned" {
		t.Fatalf("[%s] checkin: %v", st.name, back)
	}
	tr.call("check in again", "POST", "/api/desk/checkin", map[string]string{"code": copyCode}, 200)
	tr.call("availability after check-in", "GET", fmt.Sprintf("/api/books/%d", isbnID), nil, 200)
	patron.call("patron history", "GET", "/api/me/loans/history", nil, 200)

	// Staying out: a patron can't catalogue, and a signed-out visitor sees nothing.
	patron.call("patron cannot add a book", "POST", "/api/books", map[string]any{"title": "x"}, 403)
	anon := &transcript{t: t, site: &site{name: st.name, base: st.base, client: cloneClient(st.client)}, ids: tr.ids}
	anon.call("anonymous search", "GET", "/api/books?q=fox", nil, 401)

	tr.lines = append(tr.lines, patron.lines...)
	tr.lines = append(tr.lines, anon.lines...)
	_ = ol
	return tr.lines
}

func cloneClient(c *http.Client) *http.Client {
	jar, _ := cookiejar.New(nil)
	n := *c
	n.Jar = jar
	return &n
}

func TestLANAndPublicRunTheSameLibraryTheSameWay(t *testing.T) {
	oc := newOwnerCert(t)
	runs := map[string][]string{}
	starts := map[string]func(*testing.T, *testutil.FakeOL) *site{
		"lan":     startLAN,
		"proxy":   func(t *testing.T, ol *testutil.FakeOL) *site { return startProxy(t, ol, oc) },
		"owncert": func(t *testing.T, ol *testutil.FakeOL) *site { return startOwnCert(t, ol, oc) },
	}
	// One server at a time per test binary: each run is a subtest, whose server stops
	// when it ends.
	for _, name := range []string{"lan", "proxy", "owncert"} {
		t.Run(name, func(t *testing.T) {
			ol := testutil.FakeOpenLibrary(t)
			runs[name] = script(t, starts[name](t, ol), ol)
			// Nothing but the fake provider may have been asked for anything: no internet.
			for _, r := range ol.Requests() {
				if !strings.Contains(r, "9780140328721") && !strings.Contains(r, "9780306406157") && !strings.Contains(r, "/authors/") {
					t.Errorf("unexpected request to the provider: %s", r)
				}
			}
		})
	}
	if len(runs) == 0 {
		return // every run skipped (no testhooks tag): nothing to compare
	}
	lan := runs["lan"]
	if len(lan) < 30 {
		t.Fatalf("the script recorded only %d steps", len(lan))
	}
	names := make([]string, 0, len(runs))
	for n := range runs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if n == "lan" {
			continue
		}
		if len(runs[n]) != len(lan) {
			t.Errorf("%s ran %d steps, lan ran %d", n, len(runs[n]), len(lan))
			continue
		}
		for i := range lan {
			if runs[n][i] != lan[i] {
				t.Errorf("%s differs from lan at step %d:\n  lan: %s\n  %s: %s", n, i, lan[i], n, runs[n][i])
			}
		}
	}
}

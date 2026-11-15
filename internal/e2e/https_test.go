// Package e2e holds tests that cross parts of the product and judge it from
// outside: through its real listeners, with OpenSSL as a verifier that shares no
// code with the server. They need the testhooks build tag (go test -tags testhooks).
package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
)

// startHTTPS starts the server as an installation does: local CA, HTTPS, and the
// plain-HTTP trust listener.
func startHTTPS(t *testing.T, dir string, now time.Time, extra map[string]string) *testutil.Server {
	t.Helper()
	env := map[string]string{"JL_HTTP_ONLY": ""}
	for k, v := range extra {
		env[k] = v
	}
	return testutil.StartServer(t, testutil.Options{DataDir: dir, Now: now, Env: env})
}

func openssl(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	path, err := exec.LookPath("openssl")
	if err != nil {
		t.Fatalf("these tests need the openssl command as the independent verifier: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return out.String(), err
}

func mustOpenssl(t *testing.T, args ...string) string {
	t.Helper()
	out, err := openssl(t, "", args...)
	if err != nil {
		t.Fatalf("openssl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// downloadRoot is the trust step: what a phone saves from the plain-HTTP port.
// It returns the file as downloaded (DER) and a PEM copy on disk for OpenSSL.
func downloadRoot(t *testing.T, s *testutil.Server) ([]byte, string) {
	t.Helper()
	resp, err := http.Get(s.TrustURL + "/trust/ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	der, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("/trust/ca.crt: %s", resp.Status)
	}
	if _, err := x509.ParseCertificate(der); err != nil {
		t.Fatalf("/trust/ca.crt is not a certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return der, path
}

func hostPort(t *testing.T, s *testutil.Server) string {
	t.Helper()
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// sClient connects with openssl s_client trusting only caFile, verifying the
// certificate for host (an IP or a name) at the given instant. It returns OpenSSL's
// output and whether the handshake with verification succeeded.
func sClient(t *testing.T, s *testutil.Server, caFile, host string, at time.Time) (string, bool) {
	t.Helper()
	args := []string{"s_client", "-connect", hostPort(t, s), "-CAfile", caFile, "-no-CApath", "-no-CAstore",
		"-verify_return_error", "-showcerts", "-attime", fmt.Sprint(at.Unix())}
	if net.ParseIP(host) != nil {
		args = append(args, "-verify_ip", host)
	} else {
		args = append(args, "-servername", host, "-verify_hostname", host)
	}
	out, err := openssl(t, "", args...)
	return out, err == nil && strings.Contains(out, "Verify return code: 0 (ok)")
}

func serverNow(t *testing.T, s *testutil.Server) time.Time {
	t.Helper()
	n, err := s.Now()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func fingerprintOf(t *testing.T, der []byte) string {
	t.Helper()
	pemPath := filepath.Join(t.TempDir(), "c.pem")
	if err := os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	out := mustOpenssl(t, "x509", "-in", pemPath, "-noout", "-fingerprint", "-sha256")
	return strings.TrimSpace(out)
}

// chainPEMs splits the certificates s_client -showcerts printed, leaf first.
func chainPEMs(out string) []string {
	return regexp.MustCompile(`(?s)-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----`).FindAllString(out, -1)
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A fresh install, with no proxy, no domain and nothing but the machine itself,
// serves HTTPS that a client trusting only the downloaded CA file validates.
func TestFirstStartServesHTTPSAClientTrustingOnlyTheDownloadedCAValidates(t *testing.T) {
	s := startHTTPS(t, "", testutil.DefaultNow, nil)
	if !strings.HasPrefix(s.URL, "https://") {
		t.Fatalf("the app's URL is %s, not https", s.URL)
	}
	_, ca := downloadRoot(t, s)
	now := serverNow(t, s)
	out, ok := sClient(t, s, ca, "127.0.0.1", now)
	if !ok {
		t.Fatalf("openssl does not validate the server with only the downloaded CA:\n%s", out)
	}

	t.Run("a client without the step is refused, so the success above is the CA's doing", func(t *testing.T) {
		other := startOtherRoot(t)
		if out, ok := sClient(t, s, other, "127.0.0.1", now); ok {
			t.Fatalf("a different root validated the server:\n%s", out)
		}
	})
	t.Run("a name the certificate does not carry is rejected", func(t *testing.T) {
		if out, ok := sClient(t, s, ca, "example.com", now); ok {
			t.Fatalf("the certificate validated for example.com:\n%s", out)
		}
	})
	t.Run("the app itself answers over that HTTPS", func(t *testing.T) {
		out, err := openssl(t, "GET /api/health HTTP/1.0\r\nHost: 127.0.0.1\r\n\r\n",
			"s_client", "-quiet", "-connect", hostPort(t, s), "-CAfile", ca, "-no-CApath", "-no-CAstore",
			"-verify_return_error", "-verify_ip", "127.0.0.1", "-attime", fmt.Sprint(now.Unix()))
		if err != nil || !strings.Contains(out, `"status":"ok"`) {
			t.Fatalf("health over verified HTTPS: %v\n%s", err, out)
		}
	})
}

// startOtherRoot is an unrelated self-signed root, made by OpenSSL.
func startOtherRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	key, crt := filepath.Join(dir, "k.pem"), filepath.Join(dir, "c.pem")
	mustOpenssl(t, "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
		"-keyout", key, "-out", crt, "-subj", "/CN=Some other root", "-days", "30")
	return crt
}

// The CA can vouch only for local and private names. A certificate for a public
// name signed with the CA's own key is rejected by OpenSSL.
func TestCAIsNameConstrainedAndAPublicNameSignedWithItsKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	s := startHTTPS(t, dir, testutil.DefaultNow, nil)
	_, ca := downloadRoot(t, s)
	tlsDir := filepath.Join(dir, "tls")

	txt := mustOpenssl(t, "x509", "-in", ca, "-noout", "-text")
	if !strings.Contains(txt, "X509v3 Name Constraints") || !strings.Contains(txt, "CA:TRUE") {
		t.Fatalf("the root has no name constraints:\n%s", txt)
	}
	sign := func(name, san string) string {
		d := t.TempDir()
		key, csr, crt, ext := filepath.Join(d, "k.pem"), filepath.Join(d, "r.csr"), filepath.Join(d, "c.pem"), filepath.Join(d, "ext.cnf")
		mustOpenssl(t, "req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
			"-keyout", key, "-out", csr, "-subj", "/CN=device.local")
		if err := os.WriteFile(ext, []byte("subjectAltName="+san+"\nextendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// the thief holds the data directory: the intermediate and its key
		mustOpenssl(t, "x509", "-req", "-in", csr, "-CA", filepath.Join(tlsDir, "intermediate.crt"),
			"-CAkey", filepath.Join(tlsDir, "intermediate.key"), "-CAcreateserial", "-out", crt,
			"-days", "30", "-extfile", ext)
		return crt
	}
	verify := func(leaf, purposeName string) (string, error) {
		flag := "-verify_hostname"
		if net.ParseIP(purposeName) != nil {
			flag = "-verify_ip"
		}
		args := []string{"verify", "-CAfile", ca, "-no-CApath", "-no-CAstore", "-untrusted", filepath.Join(tlsDir, "intermediate.crt"),
			"-purpose", "sslserver", "-attime", fmt.Sprint(time.Now().Add(time.Hour).Unix()), flag, purposeName, leaf}
		return openssl(t, "", args...)
	}

	for _, tc := range []struct{ name, san string }{
		{"example.com", "DNS:example.com"},
		{"www.bank.org", "DNS:www.bank.org"},
		{"8.8.8.8", "IP:8.8.8.8"},
		{"mixed local and public", "DNS:shop.local,DNS:example.com"},
	} {
		leaf := sign(tc.name, tc.san)
		host := strings.TrimPrefix(strings.Split(tc.san, ",")[0], "DNS:")
		host = strings.TrimPrefix(host, "IP:")
		out, err := verify(leaf, host)
		if err == nil || !strings.Contains(out, "permitted subtree violation") {
			t.Errorf("%s: want OpenSSL to reject with a permitted subtree violation, got err=%v\n%s", tc.name, err, out)
		}
	}
	// Control: the same key and procedure for a local name verifies, so the
	// failures above come from the constraints and not from a broken harness.
	for _, tc := range []struct{ name, san, host string }{
		{"shop.local", "DNS:shop.local", "shop.local"},
		{"192.168.9.9", "IP:192.168.9.9", "192.168.9.9"},
		{"10.1.2.3", "IP:10.1.2.3", "10.1.2.3"},
	} {
		out, err := verify(sign(tc.name, tc.san), tc.host)
		if err != nil {
			t.Errorf("control %s should verify: %v\n%s", tc.name, err, out)
		}
	}
}

// The same root keeps validating the server's certificate when the server is
// restarted at another local address or reached by another local host name, and
// downloading it again gives the very same file.
func TestRestartAtAnotherAddressOrNameKeepsTheSameCAValid(t *testing.T) {
	dir := t.TempDir()
	s1 := startHTTPS(t, dir, testutil.DefaultNow, map[string]string{"JL_ADDRESSES": "192.168.1.50"})
	der1, ca := downloadRoot(t, s1)
	now := serverNow(t, s1)
	if out, ok := sClient(t, s1, ca, "192.168.1.50", now); !ok {
		t.Fatalf("first address:\n%s", out)
	}
	if err := s1.Stop(); err != nil {
		t.Fatal(err)
	}

	s2 := startHTTPS(t, dir, testutil.DefaultNow, map[string]string{"JL_ADDRESSES": "10.20.30.40"})
	der2, _ := downloadRoot(t, s2)
	if !bytes.Equal(der1, der2) {
		t.Fatal("a restart on another address changed the CA file: every phone would have to repeat the step")
	}
	for _, host := range []string{"10.20.30.40", "library.local", "jamies-library.lan"} {
		if out, ok := sClient(t, s2, ca, host, now); !ok {
			t.Errorf("with the first download of the CA, %s is not valid after the restart:\n%s", host, out)
		}
	}
	if out, ok := sClient(t, s2, ca, "172.30.5.5", now); ok {
		t.Errorf("an address nobody configured validated:\n%s", out)
	}
}

// Restart and renewal: once the clock passes the server certificate's expiry the
// server presents a new one, and the CA a phone already holds still validates it.
func TestRenewalPastLeafExpiryKeepsTheSameCAWithNoNewDownload(t *testing.T) {
	dir := t.TempDir()
	s := startHTTPS(t, dir, testutil.DefaultNow, map[string]string{"JL_ADDRESSES": "192.168.1.50"})
	der, ca := downloadRoot(t, s)
	t0 := serverNow(t, s)
	before, ok := sClient(t, s, ca, "192.168.1.50", t0)
	if !ok {
		t.Fatalf("before:\n%s", before)
	}
	leafBefore := chainPEMs(before)[0]
	leafBeforeFile := writeTemp(t, "leaf.pem", leafBefore)
	notAfter := strings.TrimPrefix(strings.TrimSpace(mustOpenssl(t, "x509", "-in", leafBeforeFile, "-noout", "-enddate")), "notAfter=")
	expiry, err := time.Parse("Jan _2 15:04:05 2006 MST", notAfter)
	if err != nil {
		t.Fatalf("parsing %q: %v", notAfter, err)
	}

	// Past the leaf's expiry, so the old leaf is invalid at that instant.
	if err := s.SetNow(expiry.Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	later := serverNow(t, s)
	if out, err := openssl(t, "", "verify", "-CAfile", ca, "-no-CApath", "-no-CAstore", "-untrusted", writeTemp(t, "chain.pem", strings.Join(chainPEMs(before)[1:], "\n")),
		"-attime", fmt.Sprint(later.Unix()), leafBeforeFile); err == nil {
		t.Fatalf("control: the old leaf should be expired at %s:\n%s", later, out)
	}
	after, ok := sClient(t, s, ca, "192.168.1.50", later)
	if !ok {
		t.Fatalf("after the clock passed the leaf's expiry, the original root no longer validates:\n%s", after)
	}
	if chainPEMs(after)[0] == leafBefore {
		t.Fatal("the leaf was not renewed")
	}
	if again, _ := downloadRoot(t, s); !bytes.Equal(again, der) {
		t.Fatal("renewal changed the CA file")
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}

	// Restart (the same build is the upgrade case: the data directory is all that carries over).
	s2 := startHTTPS(t, dir, later, map[string]string{"JL_ADDRESSES": "192.168.1.50"})
	if again, _ := downloadRoot(t, s2); !bytes.Equal(again, der) {
		t.Fatal("restart changed the CA file")
	}
	if out, ok := sClient(t, s2, ca, "192.168.1.50", later); !ok {
		t.Fatalf("after restart:\n%s", out)
	}
}

// Where the private keys are, how they are protected, and that they never reach a log.
func TestKeysAre0600OnlyInTheDataDirectoryAndNeverLogged(t *testing.T) {
	dir := t.TempDir()
	s := startHTTPS(t, dir, testutil.DefaultNow, map[string]string{"JL_ADDRESSES": "192.168.1.50"})
	_, ca := downloadRoot(t, s)
	// exercise the stack: every code path that touches keys has run
	sClient(t, s, ca, "192.168.1.50", serverNow(t, s))
	sClient(t, s, ca, "library.local", serverNow(t, s))
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, _ = hc.Get(s.TrustURL + "/anything")
	_ = s.Advance(400 * 24 * time.Hour)
	sClient(t, s, ca, "192.168.1.50", serverNow(t, s))
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	logs := s.Stdout() + s.Log()

	rootCert := loadCert(t, filepath.Join(dir, "tls", "root.crt"))
	interCert := loadCert(t, filepath.Join(dir, "tls", "intermediate.crt"))
	var keys []*ecdsa.PrivateKey
	var keyDERs [][]byte
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := os.ReadFile(p)
		if !bytes.Contains(data, []byte("PRIVATE KEY")) {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if !strings.HasPrefix(rel, "tls"+string(filepath.Separator)) {
			t.Errorf("private key material outside tls/: %s", rel)
		}
		if info, _ := d.Info(); info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %o, want 600", rel, info.Mode().Perm())
		}
		for rest := data; ; {
			var b *pem.Block
			b, rest = pem.Decode(rest)
			if b == nil {
				break
			}
			if !strings.Contains(b.Type, "PRIVATE KEY") {
				continue
			}
			k, err := parseKey(b)
			if err != nil {
				t.Errorf("%s: unreadable private key: %v", rel, err)
				continue
			}
			keys = append(keys, k)
			keyDERs = append(keyDERs, b.Bytes)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The root key must be nowhere, whatever the file is called: no key found
	// anywhere in the data directory is the root's.
	sawIntermediate := false
	for _, k := range keys {
		if k.PublicKey.Equal(rootCert.PublicKey) {
			t.Error("the root's private key is on disk")
		}
		if k.PublicKey.Equal(interCert.PublicKey) {
			sawIntermediate = true
		}
	}
	if !sawIntermediate {
		t.Fatal("the intermediate's key was not found on disk: the root-key check would pass for the wrong reason")
	}
	if info, err := os.Stat(filepath.Join(dir, "tls")); err == nil && info.Mode().Perm()&0o007 != 0 {
		t.Errorf("tls/ is world-accessible: %o", info.Mode().Perm())
	}

	// No log carries any key's secret, in any common spelling.
	if len(logs) < 100 {
		t.Fatalf("logs are suspiciously short (%d bytes): the scan proves nothing", len(logs))
	}
	if strings.Contains(logs, "PRIVATE KEY") {
		t.Error("a log contains PEM private key text")
	}
	for i, k := range keys {
		scalar := k.D.FillBytes(make([]byte, (k.Curve.Params().BitSize+7)/8))
		for _, raw := range [][]byte{scalar, keyDERs[i]} {
			for name, form := range map[string]string{
				"hex":       hex.EncodeToString(raw),
				"HEX":       strings.ToUpper(hex.EncodeToString(raw)),
				"base64":    base64.StdEncoding.EncodeToString(raw),
				"rawbase64": base64.RawStdEncoding.EncodeToString(raw),
				"url64":     base64.URLEncoding.EncodeToString(raw),
				"rawurl64":  base64.RawURLEncoding.EncodeToString(raw),
				"raw":       string(raw),
			} {
				if strings.Contains(logs, form) {
					t.Errorf("a log contains a private key (%s form)", name)
				}
			}
		}
		if strings.Contains(logs, k.D.String()) {
			t.Error("a log contains a private scalar in decimal")
		}
		if strings.Contains(logs, strings.ToLower(k.D.Text(16))) || strings.Contains(logs, strings.ToUpper(k.D.Text(16))) {
			t.Error("a log contains a private scalar in hex")
		}
	}
}

func loadCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(data)
	if b == nil {
		t.Fatalf("%s is not PEM", path)
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func parseKey(b *pem.Block) (*ecdsa.PrivateKey, error) {
	if k, err := x509.ParsePKCS8PrivateKey(b.Bytes); err == nil {
		if ek, ok := k.(*ecdsa.PrivateKey); ok {
			return ek, nil
		}
		return nil, fmt.Errorf("not an EC key: %T", k)
	}
	return x509.ParseECPrivateKey(b.Bytes)
}

// The owner's own certificate, as an install behind or without a proxy uses it:
// served as given, validated by a client trusting the owner's CA, and no local
// CA is made.
func TestOwnCertificateModeIsServedAndMakesNoLocalCA(t *testing.T) {
	d := t.TempDir()
	caKey, caCrt := filepath.Join(d, "ca.key"), filepath.Join(d, "ca.crt")
	mustOpenssl(t, "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
		"-keyout", caKey, "-out", caCrt, "-subj", "/CN=Owner CA", "-days", "30",
		"-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=keyCertSign")
	key, csr, crt, ext := filepath.Join(d, "k.pem"), filepath.Join(d, "r.csr"), filepath.Join(d, "c.pem"), filepath.Join(d, "ext.cnf")
	mustOpenssl(t, "req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-keyout", key, "-out", csr, "-subj", "/CN=library.example.org")
	os.WriteFile(ext, []byte("subjectAltName=DNS:library.example.org,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n"), 0o600)
	mustOpenssl(t, "x509", "-req", "-in", csr, "-CA", caCrt, "-CAkey", caKey, "-CAcreateserial", "-out", crt, "-days", "30", "-extfile", ext)

	dir := t.TempDir()
	now := time.Now() // the owner's certificate is dated by the real clock
	s := testutil.StartServer(t, testutil.Options{DataDir: dir, Now: now, Env: map[string]string{
		"JL_HTTP_ONLY": "", "JL_TLS_CERT": crt, "JL_TLS_KEY": key}})
	if out, ok := sClient(t, s, caCrt, "library.example.org", now); !ok {
		t.Fatalf("the owner's certificate is not served:\n%s", out)
	}
	if out, ok := sClient(t, s, caCrt, "127.0.0.1", now); !ok {
		t.Fatalf("by address:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tls")); err == nil {
		t.Error("a local CA directory was made although the owner brought a certificate")
	}
	if resp, err := http.Get(s.TrustURL + "/trust/ca.crt"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Error("a CA download is offered in the owner's certificate mode")
		}
	}

	t.Run("behind a proxy: plain HTTP only, no CA", func(t *testing.T) {
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
		p := testutil.StartServer(t, testutil.Options{DataDir: t.TempDir()})
		resp, err := http.Get(p.URL + "/api/health")
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("health over plain HTTP: %v", err)
		}
		resp.Body.Close()
		if p.TrustURL != "" {
			t.Error("a trust listener runs in proxy mode")
		}
		if _, err := os.Stat(filepath.Join(p.DataDir, "tls")); err == nil {
			t.Error("proxy mode made a CA")
		}
	})
}

// The guide a phone's owner opens over plain HTTP, before any trust exists.
func TestTrustGuideOverPlainHTTPHasEachSystemsStepsAndTheTypedFallback(t *testing.T) {
	s := startHTTPS(t, "", testutil.DefaultNow, nil)
	if !strings.HasPrefix(s.TrustURL, "http://") {
		t.Fatalf("trust listener %q is not plain HTTP", s.TrustURL)
	}
	resp, err := http.Get(s.TrustURL + "/trust/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("guide: %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{"iPhone", "Safari", "Android", "Chrome", "/trust/ca.mobileconfig", "/trust/ca.crt",
		"Certificate Trust Settings", "Install a certificate", "SHA-256", "Typing a code"} {
		if !strings.Contains(page, want) {
			t.Errorf("guide lacks %q", want)
		}
	}
	for _, link := range []string{"/trust/ca.crt", "/trust/ca.mobileconfig"} {
		r, err := http.Get(s.TrustURL + link)
		if err != nil || r.StatusCode != 200 {
			t.Errorf("%s did not download: %v", link, err)
			continue
		}
		n, _ := io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if n == 0 {
			t.Errorf("%s is empty", link)
		}
	}
	// the page loads nothing from anywhere else: it must work with no internet
	for _, m := range regexp.MustCompile(`(?:src|href)="(https?://[^"]+)"`).FindAllStringSubmatch(page, -1) {
		if !strings.HasPrefix(m[1], s.TrustURL) && !strings.HasPrefix(m[1], "https://127.0.0.1") {
			t.Errorf("the guide refers to %s", m[1])
		}
	}
}

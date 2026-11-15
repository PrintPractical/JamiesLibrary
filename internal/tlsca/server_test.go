package tlsca_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/testutil"
	"jamieslibrary/internal/tlsca"
)

// tlsEnv starts the server as an installation does: the local CA, HTTPS, the
// plain-HTTP trust listener.
func tlsEnv(addresses string) map[string]string {
	return map[string]string{"JL_HTTP_ONLY": "", "JL_ADDRESSES": addresses}
}

// phone is a device that did the trust step: it holds only the root, which it
// downloaded from the plain-HTTP port.
type phone struct {
	t    *testing.T
	root *x509.CertPool
	cert *x509.Certificate
}

func trustStep(t *testing.T, s *testutil.Server) *phone {
	t.Helper()
	resp, err := http.Get(s.TrustURL + "/trust/ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	der, _ := io.ReadAll(resp.Body)
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ca.crt is not DER: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &phone{t: t, root: pool, cert: cert}
}

// open connects to the server's HTTPS port as a client that reaches it at host
// (an address or a name), with the clock of the real world: a certificate that
// fails here shows the phone a warning.
func (p *phone) open(s *testutil.Server, host string) (*tls.ConnectionState, error) {
	u, _ := url.Parse(s.URL)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", u.Host,
		&tls.Config{RootCAs: p.root, ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	st := conn.ConnectionState()
	return &st, nil
}

func TestDEPLOYT7(t *testing.T) {
	t.Run("The server's address changes", func(t *testing.T) {
		dir := t.TempDir()
		s := testutil.StartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("192.168.1.50")})
		p := trustStep(t, s)
		if _, err := p.open(s, "192.168.1.50"); err != nil {
			t.Fatalf("at the first address: %v", err)
		}
		if _, err := p.open(s, "10.20.30.40"); err == nil {
			t.Fatal("the certificate vouched for an address nobody configured")
		}
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}

		// The server gets a new address; the same phone, holding only the root.
		s2 := testutil.StartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("10.20.30.40,192.168.1.50")})
		if _, err := p.open(s2, "10.20.30.40"); err != nil {
			t.Fatalf("at the new address: %v", err)
		}
		if _, err := p.open(s2, "192.168.1.50"); err != nil {
			t.Fatalf("at the old address: %v", err)
		}
		if _, err := p.open(s2, "library.local"); err != nil {
			t.Fatalf("by a .local name: %v", err)
		}
		// The phone downloads the root again and finds the same one.
		if fp := tlsca.Fingerprint(trustStep(t, s2).cert); fp != tlsca.Fingerprint(p.cert) {
			t.Fatal("the root changed")
		}
	})

	t.Run("Upgrade", func(t *testing.T) {
		dir := t.TempDir()
		s := testutil.StartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
		p := trustStep(t, s)
		fp := tlsca.Fingerprint(p.cert)
		if !strings.Contains(s.Stdout(), "fingerprint: "+fp) {
			t.Fatalf("the root's fingerprint was not printed at start: %q", s.Stdout())
		}
		first, err := p.open(s, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
		s2 := testutil.StartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
		if !strings.Contains(s2.Stdout(), "fingerprint: "+fp) {
			t.Fatalf("the restarted server has another root: %q", s2.Stdout())
		}
		second, err := p.open(s2, "127.0.0.1")
		if err != nil {
			t.Fatalf("the phone that did the step is warned after a restart: %v", err)
		}
		if first.PeerCertificates[0].SerialNumber.Cmp(second.PeerCertificates[0].SerialNumber) != 0 {
			t.Error("the stored server certificate was not reused")
		}
	})

	t.Run("A leaked key can't impersonate other sites", func(t *testing.T) {
		dir := t.TempDir()
		s := testutil.StartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
		p := trustStep(t, s)
		// The thief has the data directory: the intermediate and its key.
		inter := readCert(t, filepath.Join(dir, "tls", "intermediate.crt"))
		key := readKey(t, filepath.Join(dir, "tls", "intermediate.key"))
		now := time.Now()
		forge := func(dns []string, ips []net.IP) *x509.Certificate {
			k, _ := ecdsa.GenerateKey(key.Curve, rand.Reader)
			der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
				SerialNumber: big.NewInt(7), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
				DNSNames: dns, IPAddresses: ips, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}, inter, &k.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			c, _ := x509.ParseCertificate(der)
			return c
		}
		im := x509.NewCertPool()
		im.AddCert(inter)
		for host, c := range map[string]*x509.Certificate{
			"example.com": forge([]string{"example.com"}, nil),
			"8.8.8.8":     forge(nil, []net.IP{net.ParseIP("8.8.8.8")}),
		} {
			if _, err := c.Verify(x509.VerifyOptions{Roots: p.root, Intermediates: im, DNSName: host}); err == nil {
				t.Errorf("a certificate for %s signed by the leaked key is accepted", host)
			}
		}
		if _, err := forge([]string{"shop.local"}, nil).Verify(x509.VerifyOptions{Roots: p.root, Intermediates: im, DNSName: "shop.local"}); err != nil {
			t.Errorf("control: the key can still sign a local name: %v", err)
		}
	})
}

func TestRenewalKeepsTheRoot(t *testing.T) {
	dir := t.TempDir()
	s := testutil.StartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
	p := trustStep(t, s)
	before, err := p.open(s, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(370 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	conn := dialRaw(t, s)
	leaf := conn.PeerCertificates[0]
	if leaf.SerialNumber.Cmp(before.PeerCertificates[0].SerialNumber) == 0 {
		t.Fatal("the certificate was not renewed")
	}
	now, _ := s.Now()
	inter := x509.NewCertPool()
	for _, c := range conn.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: p.root, Intermediates: inter, DNSName: "127.0.0.1", CurrentTime: now}); err != nil {
		t.Fatalf("a phone holding the original root rejects the renewed certificate: %v", err)
	}
	if fp := tlsca.Fingerprint(trustStep(t, s).cert); fp != tlsca.Fingerprint(p.cert) {
		t.Fatal("renewal changed the root")
	}
}

func TestStartupWithoutTLSDirectoryOfAnExistingLibrary(t *testing.T) {
	dir := t.TempDir()
	s := testutil.StartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
	s.Stop()
	if err := os.RemoveAll(filepath.Join(dir, "tls")); err != nil {
		t.Fatal(err)
	}
	_, err := testutil.TryStartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
	if err == nil {
		t.Fatal("the server made a new CA over an existing library")
	}
	for _, want := range []string{filepath.Join(dir, "tls"), "backup", "trust step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(dir, "tls")); statErr == nil {
		t.Error("tls/ was made anyway")
	}
}

func TestSwitchingFromProxyModeToLocalCAIsNotALostCA(t *testing.T) {
	dir := t.TempDir()
	s := testutil.StartServer(t, testutil.Options{DataDir: dir}) // JL_HTTP_ONLY=1
	s.Stop()
	s2, err := testutil.TryStartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
	if err != nil {
		t.Fatalf("a library that never had a CA could not get one: %v", err)
	}
	s2.Stop()
}

// A first start that fails after the database exists (a typo in the certificate
// settings) must not leave an install that thinks its CA was lost.
func TestFailedFirstStartThenLocalCA(t *testing.T) {
	dir := t.TempDir()
	_, err := testutil.TryStartServer(t, testutil.Options{DataDir: dir, Env: map[string]string{
		"JL_HTTP_ONLY": "", "JL_TLS_CERT": "/nope", "JL_TLS_KEY": "/nope"}})
	if err == nil {
		t.Fatal("bad certificate files started")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "library.db")); statErr != nil {
		t.Fatal("the failed start was meant to leave a database behind")
	}
	s, err := testutil.TryStartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")})
	if err != nil {
		t.Fatalf("the local CA was refused after a failed first start: %v", err)
	}
	s.Stop()
	// Once a CA was made, losing tls/ is an error again.
	os.RemoveAll(filepath.Join(dir, "tls"))
	if _, err := testutil.TryStartServer(t, testutil.Options{DataDir: dir, Env: tlsEnv("")}); err == nil {
		t.Fatal("a lost tls/ was not noticed")
	}
}

func TestHTTPOnlyServesTheAppOverPlainHTTPAndNoTrustPort(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{})
	if !strings.HasPrefix(s.URL, "http://") || s.TrustURL != "" {
		t.Fatalf("%q %q", s.URL, s.TrustURL)
	}
	resp, err := http.Get(s.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "tls")); err == nil {
		t.Error("a CA was made in proxy mode")
	}
	if strings.Contains(s.Stdout(), "fingerprint") {
		t.Error("a fingerprint was printed with no CA")
	}
}

func TestHTTPSServesTheAppAndPlainPortServesNoAPI(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{Env: tlsEnv("")})
	p := trustStep(t, s)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: p.root, ServerName: "127.0.0.1"}}, Timeout: 10 * time.Second}
	resp, err := client.Get(s.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 10 * time.Second}
	for _, method := range []string{"GET", "POST"} {
		req, _ := http.NewRequest(method, s.TrustURL+"/api/health", strings.NewReader("{}"))
		r, err := plain.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if strings.Contains(string(b), `"status"`) || r.Header.Get("Set-Cookie") != "" || (r.StatusCode != 307 && r.StatusCode != 405) {
			t.Errorf("%s /api/health over plain HTTP: %d %q", method, r.StatusCode, b)
		}
	}
	u, _ := url.Parse(s.TrustURL)
	r, _ := plain.Get(s.TrustURL + "/books/1?x=1")
	if want := "https://" + u.Hostname() + ":"; !strings.HasPrefix(r.Header.Get("Location"), want) || !strings.HasSuffix(r.Header.Get("Location"), "/books/1?x=1") {
		t.Errorf("redirect %q", r.Header.Get("Location"))
	}
}

func TestFingerprintPrintedAtEachStartMatchesTheDownload(t *testing.T) {
	s := testutil.StartServer(t, testutil.Options{Env: tlsEnv("")})
	resp, err := http.Get(s.TrustURL + "/trust/ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	der, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	sum := sha256.Sum256(der)
	m := regexp.MustCompile(`fingerprint: ([0-9A-F]{2}(?::[0-9A-F]{2}){31})`).FindStringSubmatch(s.Stdout())
	if m == nil {
		t.Fatalf("no fingerprint line in %q", s.Stdout())
	}
	var want []string
	for _, b := range sum {
		want = append(want, fmt.Sprintf("%02X", b))
	}
	if m[1] != strings.Join(want, ":") {
		t.Fatalf("printed %s, root is %s", m[1], strings.Join(want, ":"))
	}
}

func TestOwnCertificateMode(t *testing.T) {
	dir := t.TempDir()
	cp, kp := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"library.example.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	kd, _ := x509.MarshalPKCS8PrivateKey(k)
	os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), 0o600)

	s := testutil.StartServer(t, testutil.Options{Env: map[string]string{"JL_HTTP_ONLY": "", "JL_TLS_CERT": cp, "JL_TLS_KEY": kp}})
	if _, err := os.Stat(filepath.Join(s.DataDir, "tls")); err == nil {
		t.Error("a local CA was made although the owner brought a certificate")
	}
	if conn := dialRaw(t, s); string(conn.PeerCertificates[0].Raw) != string(der) {
		t.Error("not the owner's certificate")
	}
	// A bad file is a start-up error that names the file.
	os.WriteFile(kp, []byte("junk"), 0o600)
	s.Stop()
	_, err := testutil.TryStartServer(t, testutil.Options{Env: map[string]string{"JL_HTTP_ONLY": "", "JL_TLS_CERT": cp, "JL_TLS_KEY": kp}})
	if err == nil || !strings.Contains(err.Error(), kp) {
		t.Fatalf("want an error naming %s, got %v", kp, err)
	}
}

// dialRaw connects without verifying, to look at what the server presents.
func dialRaw(t *testing.T, s *testutil.Server) *tls.ConnectionState {
	t.Helper()
	u, _ := url.Parse(s.URL)
	conn, err := tls.Dial("tcp", u.Host, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	st := conn.ConnectionState()
	return &st
}

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, _ := os.ReadFile(path)
	b, _ := pem.Decode(data)
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readKey(t *testing.T, path string) *ecdsa.PrivateKey {
	t.Helper()
	data, _ := os.ReadFile(path)
	b, _ := pem.Decode(data)
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return k.(*ecdsa.PrivateKey)
}

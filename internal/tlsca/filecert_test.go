package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writePair writes a self-signed certificate for name and its key.
func writePair(t *testing.T, certPath, keyPath, name string) *x509.Certificate {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalPKCS8PrivateKey(k)
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), 0o600)
	c, _ := x509.ParseCertificate(der)
	return c
}

func TestOwnCertificateIsServedAndReloaded(t *testing.T) {
	dir := t.TempDir()
	cp, kp := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	writePair(t, cp, kp, "one.example.org")
	f, err := LoadFiles(cp, kp, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.recheck = 0
	c, _ := f.GetCertificate(nil)
	if c.Leaf.Subject.CommonName != "one.example.org" {
		t.Fatalf("serving %q", c.Leaf.Subject.CommonName)
	}
	// A renewed pair replaces it with no restart.
	writePair(t, cp, kp, "two.example.org")
	future := time.Now().Add(time.Minute)
	os.Chtimes(cp, future, future)
	os.Chtimes(kp, future, future)
	if c, _ := f.GetCertificate(nil); c.Leaf.Subject.CommonName != "two.example.org" {
		t.Fatalf("still serving %q after the files changed", c.Leaf.Subject.CommonName)
	}
	// A bad replacement keeps the good one.
	os.WriteFile(cp, []byte("junk"), 0o600)
	later := future.Add(time.Minute)
	os.Chtimes(cp, later, later)
	if c, _ := f.GetCertificate(nil); c.Leaf.Subject.CommonName != "two.example.org" {
		t.Fatalf("a bad file replaced the certificate: %q", c.Leaf.Subject.CommonName)
	}
}

func TestOwnCertificateStartUpErrorsNameTheFile(t *testing.T) {
	dir := t.TempDir()
	cp, kp := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	other := filepath.Join(dir, "other.pem")
	writePair(t, cp, kp, "a.example.org")
	writePair(t, other, filepath.Join(dir, "otherkey.pem"), "b.example.org")
	junk := filepath.Join(dir, "junk.pem")
	os.WriteFile(junk, []byte("not pem"), 0o600)
	for name, tc := range map[string]struct{ cert, key, want string }{
		"cert missing": {filepath.Join(dir, "nope.pem"), kp, "JL_TLS_CERT"},
		"key missing":  {cp, filepath.Join(dir, "nope.pem"), "JL_TLS_KEY"},
		"cert is junk": {junk, kp, junk},
		"key is junk":  {cp, junk, junk},
		"mismatched":   {other, kp, "the key must belong to the certificate"},
	} {
		if _, err := LoadFiles(tc.cert, tc.key, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

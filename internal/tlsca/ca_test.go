package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// testCA opens a CA in dir with a clock the test moves through *now.
func testCA(t *testing.T, dir string, now *time.Time, mod func(*Options)) *CA {
	t.Helper()
	o := Options{
		Now:            func() time.Time { return *now },
		InterfaceAddrs: func() ([]net.IP, error) { return []net.IP{net.ParseIP("192.168.1.20")}, nil },
		Logger:         slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if mod != nil {
		mod(&o)
	}
	ca, err := Open(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func rootPool(ca *CA) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.RootCertificate())
	return p
}

// verify checks a served certificate the way a device that trusts only the root does.
func verify(t *testing.T, root *x509.CertPool, cert *tls.Certificate, name string, at time.Time) error {
	t.Helper()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	inter := x509.NewCertPool()
	for _, der := range cert.Certificate[1:] {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		inter.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots: root, Intermediates: inter, DNSName: name, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

func hello(sni string) *tls.ClientHelloInfo { return &tls.ClientHelloInfo{ServerName: sni} }

func isCritical(c *x509.Certificate, oid asn1.ObjectIdentifier) (found, critical bool) {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return true, e.Critical
		}
	}
	return false, false
}

var oidNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}

func TestFirstStartMakesConstrainedRootAndIntermediate(t *testing.T) {
	dir := t.TempDir()
	now := t0
	ca := testCA(t, dir, &now, func(o *Options) { o.Hostnames = []string{"Library.Example.org"} })
	if !ca.Created() {
		t.Fatal("first start did not say it made the CA")
	}
	root, inter := ca.root, ca.inter
	for name, c := range map[string]*x509.Certificate{"root": root, "intermediate": inter} {
		if !c.IsCA || !c.BasicConstraintsValid {
			t.Errorf("%s: not a CA with basic constraints", name)
		}
		if c.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
			t.Errorf("%s: key usage %v", name, c.KeyUsage)
		}
		if len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
			t.Errorf("%s: EKU %v", name, c.ExtKeyUsage)
		}
		if c.PublicKeyAlgorithm != x509.ECDSA || c.PublicKey.(*ecdsa.PublicKey).Curve != elliptic.P256() {
			t.Errorf("%s: not ECDSA P-256", name)
		}
		if !c.PermittedDNSDomainsCritical {
			t.Errorf("%s: name constraints not critical", name)
		}
		if found, crit := isCritical(c, oidNameConstraints); !found || !crit {
			t.Errorf("%s: name constraints extension found=%v critical=%v", name, found, crit)
		}
		if got := c.NotAfter.Sub(c.NotBefore); got < 20*365*24*time.Hour || got > 20*365*24*time.Hour+2*time.Hour {
			t.Errorf("%s: valid for %v, want 20 years", name, got)
		}
		wantDNS := []string{"local", "lan", "home.arpa", "internal", "localhost", "library.example.org"}
		if fmt.Sprint(c.PermittedDNSDomains) != fmt.Sprint(wantDNS) {
			t.Errorf("%s: DNS constraints %v, want %v", name, c.PermittedDNSDomains, wantDNS)
		}
		var ranges []string
		for _, n := range c.PermittedIPRanges {
			ranges = append(ranges, n.String())
		}
		wantIP := "10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 127.0.0.0/8 fc00::/7 fe80::/10 ::1/128"
		if strings.Join(ranges, " ") != wantIP {
			t.Errorf("%s: IP constraints %v, want %s", name, ranges, wantIP)
		}
	}
	if root.MaxPathLen != 1 || root.MaxPathLenZero {
		t.Errorf("root path length %d zero=%v, want 1", root.MaxPathLen, root.MaxPathLenZero)
	}
	if inter.MaxPathLen != 0 || !inter.MaxPathLenZero {
		t.Errorf("intermediate path length %d zero=%v, want 0", inter.MaxPathLen, inter.MaxPathLenZero)
	}
	if !strings.HasPrefix(root.Subject.CommonName, "Jamie's Library local CA (") {
		t.Errorf("root CN %q", root.Subject.CommonName)
	}
	if err := inter.CheckSignatureFrom(root); err != nil {
		t.Error(err)
	}
	st, err := os.Stat(filepath.Join(dir, "tls", "intermediate.key"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("intermediate key: %v %v", st, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "tls")); st.Mode().Perm() != 0o700 {
		t.Errorf("tls directory mode %v", st.Mode().Perm())
	}
}

func TestLaterStartsLoadTheSameCA(t *testing.T) {
	dir := t.TempDir()
	now := t0
	first := testCA(t, dir, &now, nil)
	// Hostnames given later are not added: the constraints are fixed at first start.
	second := testCA(t, dir, &now, func(o *Options) { o.Hostnames = []string{"new.example.org"}; o.ExistingData = true })
	if second.Created() {
		t.Fatal("the second start made a new CA")
	}
	if first.Fingerprint() != second.Fingerprint() || string(first.inter.Raw) != string(second.inter.Raw) {
		t.Fatal("the CA changed across a restart")
	}
	for _, d := range second.inter.PermittedDNSDomains {
		if d == "new.example.org" {
			t.Fatal("JL_HOSTNAMES was applied after first start")
		}
	}
	if _, err := second.GetCertificate(hello("new.example.org")); err != nil {
		t.Fatal(err) // falls back to the address certificate, which still serves
	}
}

func TestMissingTLSDirectoryWithExistingLibraryIsAnError(t *testing.T) {
	dir := t.TempDir()
	now := t0
	_, err := Open(dir, Options{ExistingData: true, Now: func() time.Time { return now }})
	if err == nil {
		t.Fatal("a new CA was made silently")
	}
	for _, want := range []string{filepath.Join(dir, "tls"), "Restore tls/", "backup", "trust step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(dir, "tls")); statErr == nil {
		t.Error("a tls directory was made anyway")
	}
	// The owner's explicit choice: an empty tls directory means "start a new CA".
	if err := os.Mkdir(filepath.Join(dir, "tls"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ca, err := Open(dir, Options{ExistingData: true, Now: func() time.Time { return now }}); err != nil || !ca.Created() {
		t.Fatalf("empty tls directory: %v", err)
	}
}

func TestDamagedTLSDirectoryIsAnError(t *testing.T) {
	damage := map[string]func(t *testing.T, tls string){
		"root missing": func(t *testing.T, d string) { os.Remove(filepath.Join(d, "root.crt")) },
		"key missing":  func(t *testing.T, d string) { os.Remove(filepath.Join(d, "intermediate.key")) },
		"intermediate garbage": func(t *testing.T, d string) {
			os.WriteFile(filepath.Join(d, "intermediate.crt"), []byte("junk"), 0o600)
		},
		"root truncated": func(t *testing.T, d string) {
			b, _ := os.ReadFile(filepath.Join(d, "root.crt"))
			os.WriteFile(filepath.Join(d, "root.crt"), b[:len(b)/2], 0o600)
		},
		"key of another CA": func(t *testing.T, d string) {
			other := t.TempDir()
			now := t0
			testCA(t, other, &now, nil)
			b, _ := os.ReadFile(filepath.Join(other, "tls", "intermediate.key"))
			os.WriteFile(filepath.Join(d, "intermediate.key"), b, 0o600)
		},
		"root of another CA": func(t *testing.T, d string) {
			other := t.TempDir()
			now := t0
			testCA(t, other, &now, nil)
			b, _ := os.ReadFile(filepath.Join(other, "tls", "root.crt"))
			os.WriteFile(filepath.Join(d, "root.crt"), b, 0o600)
		},
	}
	for name, f := range damage {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			now := t0
			testCA(t, dir, &now, nil)
			f(t, filepath.Join(dir, "tls"))
			_, err := Open(dir, Options{ExistingData: true, Now: func() time.Time { return now }})
			if err == nil || !strings.Contains(err.Error(), "damaged") || !strings.Contains(err.Error(), "backup") {
				t.Fatalf("want a damaged-CA error that points to the backup, got %v", err)
			}
		})
	}
}

func TestExpiredIntermediateIsAnError(t *testing.T) {
	dir := t.TempDir()
	now := t0
	testCA(t, dir, &now, nil)
	now = t0.AddDate(21, 0, 0)
	if _, err := Open(dir, Options{Now: func() time.Time { return now }}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("got %v", err)
	}
}

// The root's key is made in memory and discarded: nothing under tls/ holds it.
func TestRootKeyIsNeverOnDisk(t *testing.T) {
	dir := t.TempDir()
	now := t0
	ca := testCA(t, dir, &now, nil)
	for _, sni := range []string{"a.local", "b.lan", ""} { // leaves are stored too
		if _, err := ca.GetCertificate(hello(sni)); err != nil {
			t.Fatal(err)
		}
	}
	rootPub := ca.root.PublicKey.(*ecdsa.PublicKey)
	keys := 0
	err := filepath.WalkDir(filepath.Join(dir, "tls"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := os.ReadFile(path)
		for _, blk := range pemBlocks(data) {
			if strings.Contains(blk.Type, "PRIVATE KEY") {
				keys++
				k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
				if err != nil {
					t.Errorf("%s: %v", path, err)
					continue
				}
				if k.(*ecdsa.PrivateKey).PublicKey.Equal(rootPub) {
					t.Errorf("%s holds the root's private key", path)
				}
			}
		}
		if st, _ := d.Info(); st.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v, want 0600", path, st.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys < 3 {
		t.Fatalf("scanned %d private keys, want the intermediate's and the leaves': the scan didn't look", keys)
	}
}

func TestLeafCertificateShape(t *testing.T) {
	now := t0
	ca := testCA(t, t.TempDir(), &now, nil)
	c, err := ca.GetCertificate(hello("Library.local."))
	if err != nil {
		t.Fatal(err)
	}
	leaf := c.Leaf
	if fmt.Sprint(leaf.DNSNames) != "[library.local]" || len(leaf.IPAddresses) != 0 {
		t.Errorf("names %v %v", leaf.DNSNames, leaf.IPAddresses)
	}
	if leaf.Subject.CommonName != "" || leaf.IsCA {
		t.Errorf("subject %q, CA %v", leaf.Subject.CommonName, leaf.IsCA)
	}
	if leaf.PublicKey.(*ecdsa.PublicKey).Curve != elliptic.P256() {
		t.Error("not P-256")
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("EKU %v", leaf.ExtKeyUsage)
	}
	if got := leaf.NotAfter.Sub(now); got != 397*24*time.Hour {
		t.Errorf("valid %v after now, want 397 days", got)
	}
	if total := leaf.NotAfter.Sub(leaf.NotBefore); total > 398*24*time.Hour {
		t.Errorf("valid %v in all: Apple refuses over 398 days", total)
	}
	if err := verify(t, rootPool(ca), c, "library.local", now); err != nil {
		t.Fatal(err)
	}
	if len(c.Certificate) != 2 {
		t.Errorf("chain of %d, want leaf and intermediate", len(c.Certificate))
	}
}

func TestNoSNIGetsTheMachinesAddressesAndConfiguredOnes(t *testing.T) {
	now := t0
	ca := testCA(t, t.TempDir(), &now, func(o *Options) {
		o.Addresses = []net.IP{net.ParseIP("10.9.8.7"), net.ParseIP("8.8.8.8"), net.ParseIP("192.168.1.20")}
		o.InterfaceAddrs = func() ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.168.1.20"), net.ParseIP("127.0.0.1"), net.ParseIP("203.0.113.5"), net.ParseIP("fe80::1")}, nil
		}
	})
	for _, sni := range []string{"", "192.168.1.20", "example.com", "bad name!"} {
		c, err := ca.GetCertificate(hello(sni))
		if err != nil {
			t.Fatalf("sni %q: %v", sni, err)
		}
		var got []string
		for _, ip := range c.Leaf.IPAddresses {
			got = append(got, ip.String())
		}
		want := "[10.9.8.7 127.0.0.1 192.168.1.20 fe80::1]" // no 8.8.8.8 or 203.0.113.5: outside the constraints
		if fmt.Sprint(got) != want || len(c.Leaf.DNSNames) != 0 {
			t.Errorf("sni %q: addresses %v, dns %v, want %s", sni, got, c.Leaf.DNSNames, want)
		}
		if err := verify(t, rootPool(ca), c, "10.9.8.7", now); err != nil {
			t.Errorf("sni %q: %v", sni, err)
		}
	}
}

func TestLeafIsReusedThenRenewedBeforeExpiry(t *testing.T) {
	dir := t.TempDir()
	now := t0
	ca := testCA(t, dir, &now, nil)
	first, err := ca.GetCertificate(hello("x.local"))
	if err != nil {
		t.Fatal(err)
	}
	now = t0.Add(360 * 24 * time.Hour) // 37 days left
	again, _ := ca.GetCertificate(hello("x.local"))
	if again.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) != 0 {
		t.Fatal("a certificate with more than 30 days left was replaced")
	}
	now = t0.Add(368 * 24 * time.Hour) // 29 days left
	renewed, err := ca.GetCertificate(hello("x.local"))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) == 0 {
		t.Fatal("the certificate was not renewed 30 days before expiry")
	}
	if !renewed.Leaf.NotAfter.After(first.Leaf.NotAfter) {
		t.Fatal("renewed certificate does not last longer")
	}
	// Same root: a device that trusts the original root still verifies, and the root is untouched.
	if err := verify(t, rootPool(ca), renewed, "x.local", now); err != nil {
		t.Fatal(err)
	}
	reopened := testCA(t, dir, &now, func(o *Options) { o.ExistingData = true })
	if reopened.Fingerprint() != ca.Fingerprint() {
		t.Fatal("renewal changed the root")
	}
	// A restart reuses the stored certificate.
	again, _ = reopened.GetCertificate(hello("x.local"))
	if again.Leaf.SerialNumber.Cmp(renewed.Leaf.SerialNumber) != 0 {
		t.Fatal("a restart did not reuse the stored certificate")
	}
}

func TestAtMost64LeavesCachedAndStored(t *testing.T) {
	dir := t.TempDir()
	now := t0
	ca := testCA(t, dir, &now, nil)
	c0, _ := ca.GetCertificate(hello("host0.local"))
	for i := 1; i < MaxLeaves; i++ {
		ca.GetCertificate(hello(fmt.Sprintf("host%d.local", i)))
	}
	ca.GetCertificate(hello("host0.local")) // host0 is now the most recently used
	for i := MaxLeaves; i < MaxLeaves+10; i++ {
		ca.GetCertificate(hello(fmt.Sprintf("host%d.local", i)))
	}
	if n := ca.leaves.Len(); n != MaxLeaves {
		t.Fatalf("%d cached, want %d", n, MaxLeaves)
	}
	files, _ := os.ReadDir(filepath.Join(dir, "tls", "leaves"))
	if len(files) != MaxLeaves {
		t.Fatalf("%d stored, want %d", len(files), MaxLeaves)
	}
	if again, _ := ca.GetCertificate(hello("host0.local")); again.Leaf.SerialNumber.Cmp(c0.Leaf.SerialNumber) != 0 {
		t.Fatal("the most recently used certificate was evicted")
	}
	if _, ok := ca.leaves.entries["dns:host1.local"]; ok {
		t.Fatal("the least recently used certificate was kept")
	}
}

func TestStoredLeafFromAnotherCAIsNotUsed(t *testing.T) {
	dir := t.TempDir()
	now := t0
	ca := testCA(t, dir, &now, nil)
	orig, _ := ca.GetCertificate(hello("x.local"))
	other := t.TempDir()
	otherCA := testCA(t, other, &now, nil)
	// Put another CA's file where this CA's leaf for x.local is stored.
	foreign, _ := otherCA.GetCertificate(hello("x.local"))
	otherCA.leaves.write("dns:x.local", foreign)
	b, _ := os.ReadFile(otherCA.leaves.path("dns:x.local"))
	os.WriteFile(ca.leaves.path("dns:x.local"), b, 0o600)
	reopened := testCA(t, dir, &now, func(o *Options) { o.ExistingData = true })
	c, err := reopened.GetCertificate(hello("x.local"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, rootPool(reopened), c, "x.local", now); err != nil {
		t.Fatalf("served a certificate this CA didn't sign: %v", err)
	}
	if c.Leaf.SerialNumber.Cmp(orig.Leaf.SerialNumber) == 0 {
		t.Fatal("unexpected: the original leaf is gone from disk and should have been reissued")
	}
}

// A leaked intermediate key can sign anything, but no certificate it signs for
// a public name or address is accepted by a device that trusts the root.
func TestLeakedKeyCannotVouchForPublicSites(t *testing.T) {
	now := t0
	ca := testCA(t, t.TempDir(), &now, nil)
	probe := func(dns []string, ips []net.IP) *tls.Certificate {
		c, err := ca.issue(dns, ips, now) // issue doesn't check the constraints: it is the thief's signing
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	pool := rootPool(ca)
	for name, tc := range map[string]struct {
		c    *tls.Certificate
		host string
	}{
		"example.com": {probe([]string{"example.com"}, nil), "example.com"},
		"8.8.8.8":     {probe(nil, []net.IP{net.ParseIP("8.8.8.8")}), "8.8.8.8"},
		"2001:db8::1": {probe(nil, []net.IP{net.ParseIP("2001:db8::1")}), "2001:db8::1"},
		"mixed":       {probe([]string{"shop.local", "example.com"}, nil), "shop.local"},
	} {
		err := verify(t, pool, tc.c, tc.host, now)
		var inv x509.CertificateInvalidError
		if err == nil || !asInvalid(err, &inv) || inv.Reason != x509.CANotAuthorizedForThisName {
			t.Errorf("%s: want a name-constraint failure, got %v", name, err)
		}
	}
	// The same probe for a name the CA may vouch for verifies, so the failures above are the constraints at work.
	if err := verify(t, pool, probe([]string{"shop.local"}, nil), "shop.local", now); err != nil {
		t.Fatal(err)
	}
	if err := verify(t, pool, probe(nil, []net.IP{net.ParseIP("192.168.9.9")}), "192.168.9.9", now); err != nil {
		t.Fatal(err)
	}
}

func asInvalid(err error, target *x509.CertificateInvalidError) bool {
	e, ok := err.(x509.CertificateInvalidError)
	if ok {
		*target = e
	}
	return ok
}

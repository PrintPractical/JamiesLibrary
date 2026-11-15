// Package tlsca is the installation's own certificate authority, so it can
// serve HTTPS on a local network with no domain name and no internet
// (security design §11).
//
// First start makes a root and a name-constrained intermediate. The root's key
// exists only in memory, long enough to sign the intermediate. Layout of
// DATA_DIR/tls/ (directory 0700, files 0600):
//
//	root.crt           the root certificate (public): what a device is asked to trust
//	intermediate.crt   signed by the root
//	intermediate.key   the only CA key on disk; it can only sign what the constraints permit
//	leaves/*.pem       server certificates and their keys, at most MaxLeaves
//
// Both CA certificates carry the same critical name constraints, DNS and IP,
// so what a device trusts can never vouch for a public site.
package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/config"
)

// File names inside the tls directory.
const (
	DirName          = "tls"
	rootFile         = "root.crt"
	intermediateFile = "intermediate.crt"
	intermediateKey  = "intermediate.key"
	leavesDir        = "leaves"

	dirMode  = 0o700
	fileMode = 0o600
)

// Lifetimes and bounds (security design §11).
const (
	caLifetime     = 20 * 365 * 24 * time.Hour
	LeafLifetime   = 397 * 24 * time.Hour
	RenewBefore    = 30 * 24 * time.Hour
	backdate       = time.Hour // for clocks a little behind ours
	MaxLeaves      = 64
	maxLeafAddress = 64 // addresses named in one certificate
)

// defaultDNSConstraints and defaultIPConstraints are what the CA may vouch for
// besides the owner's own names. The IP list MUST be present: RFC 5280 leaves
// a name type that is left out unconstrained.
var defaultDNSConstraints = []string{"local", "lan", "home.arpa", "internal", "localhost"}

var defaultIPConstraints = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "127.0.0.0/8",
	"fc00::/7", "fe80::/10", "::1/128",
}

// Options say how Open sets the CA up.
type Options struct {
	// Hostnames are the owner's own names: they join the constraints when the
	// CA is made, and are ignored (with a warning) afterwards.
	Hostnames []string
	// Addresses are extra addresses the certificate for IP connections names.
	Addresses []net.IP
	// ExistingData says the data directory already holds a library. A missing
	// or damaged CA is then an error, never a silent new one.
	ExistingData bool
	// Now replaces clock.Now (tests).
	Now func() time.Time
	// InterfaceAddrs replaces the machine's interface addresses (tests).
	InterfaceAddrs func() ([]net.IP, error)
	Logger         *slog.Logger
}

// CA is the local certificate authority of one installation.
type CA struct {
	dir     string
	opts    Options
	now     func() time.Time
	log     *slog.Logger
	root    *x509.Certificate
	inter   *x509.Certificate
	key     *ecdsa.PrivateKey // the intermediate's
	created bool

	mu     sync.Mutex
	leaves *leafCache
}

// Open loads the CA from dataDir/tls, or makes it at the first start. A tls
// directory that is missing or damaged where a library already exists is an
// error that says how to put it right; so is anything that can't be read or written.
func Open(dataDir string, opts Options) (*CA, error) {
	c := &CA{dir: filepath.Join(dataDir, DirName), opts: opts, now: opts.Now, log: opts.Logger}
	if c.now == nil {
		c.now = clock.Now
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.opts.InterfaceAddrs == nil {
		c.opts.InterfaceAddrs = interfaceIPs
	}
	empty, err := dirEmptyOrMissing(c.dir)
	if err != nil {
		return nil, err
	}
	if empty {
		if opts.ExistingData && !dirExists(c.dir) {
			return nil, fmt.Errorf("the tls directory %q is missing, but %q already holds a library. "+
				"Restore tls/ from your backup of the data directory: that way every phone and computer "+
				"that already trusts this installation keeps trusting it and nobody has to do the trust step again. "+
				"A new CA is not made on its own, because it would force exactly that. "+
				"If there is no backup and you accept that every device repeats the trust step, "+
				"create an empty directory %q and start again", c.dir, dataDir, c.dir)
		}
		if err := c.create(); err != nil {
			return nil, err
		}
		c.created = true
	} else if err := c.load(); err != nil {
		return nil, err
	}
	c.leaves = newLeafCache(filepath.Join(c.dir, leavesDir), c.log)
	if err := c.leaves.prepare(c.now()); err != nil {
		return nil, err
	}
	if !c.created {
		c.warnIgnoredHostnames()
	}
	return c, nil
}

// Created says whether this start made the CA.
func (c *CA) Created() bool { return c.created }

// RootCertificate is the root, the one certificate a device is asked to trust.
func (c *CA) RootCertificate() *x509.Certificate { return c.root }

// RootDER is the root certificate in DER form (what /trust/ca.crt serves).
func (c *CA) RootDER() []byte { return c.root.Raw }

// Fingerprint is the root's SHA-256 fingerprint as upper-case hex pairs
// separated by colons, the form the trust guide and settings page show.
func (c *CA) Fingerprint() string { return Fingerprint(c.root) }

// Fingerprint is the SHA-256 fingerprint of cert, as AB:CD:….
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	pairs := make([]string, 0, len(sum))
	for i := 0; i < len(h); i += 2 {
		pairs = append(pairs, h[i:i+2])
	}
	return strings.Join(pairs, ":")
}

// permitsDNS and permitsIP report whether the CA's name constraints allow it
// to vouch for a host name or an address.
func (c *CA) permitsDNS(name string) bool {
	for _, d := range c.inter.PermittedDNSDomains {
		if name == d || strings.HasSuffix(name, "."+d) {
			return true
		}
	}
	return false
}

func (c *CA) permitsIP(ip net.IP) bool {
	for _, n := range c.inter.PermittedIPRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (c *CA) warnIgnoredHostnames() {
	for _, h := range c.opts.Hostnames {
		if !c.permitsDNS(strings.ToLower(h)) {
			c.log.Warn("JL_HOSTNAMES is read when the CA is first made, and this name is not covered by it, so it gets no certificate; "+
				"a name must be added by making a new CA, which every device would have to trust again", "name", h)
		}
	}
}

// create makes the root and the intermediate in a scratch directory next to
// tls/ and moves it into place, so a crash leaves no half-made CA.
func (c *CA) create() error {
	dns := append([]string(nil), defaultDNSConstraints...)
	seen := map[string]bool{}
	for _, d := range dns {
		seen[d] = true
	}
	for _, h := range c.opts.Hostnames {
		h = strings.ToLower(strings.TrimSuffix(h, "."))
		if err := config.CheckOwnerHostname(h); err != nil {
			return fmt.Errorf("JL_HOSTNAMES: %q: %w", h, err)
		}
		if !seen[h] {
			seen[h] = true
			dns = append(dns, h)
		}
	}
	var ips []*net.IPNet
	for _, s := range defaultIPConstraints {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return err
		}
		ips = append(ips, n)
	}
	id, err := randomHex(4)
	if err != nil {
		return err
	}
	cn := "Jamie's Library local CA (" + id + ")"
	now := c.now()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("making the CA root key: %w", err)
	}
	template := func(name string, pathLen int, zero bool) (*x509.Certificate, error) {
		serial, err := serialNumber()
		if err != nil {
			return nil, err
		}
		return &x509.Certificate{
			SerialNumber:                serial,
			Subject:                     pkix.Name{CommonName: name},
			NotBefore:                   now.Add(-backdate),
			NotAfter:                    now.Add(caLifetime),
			IsCA:                        true,
			BasicConstraintsValid:       true,
			MaxPathLen:                  pathLen,
			MaxPathLenZero:              zero,
			KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
			ExtKeyUsage:                 []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			PermittedDNSDomainsCritical: true,
			PermittedDNSDomains:         dns,
			PermittedIPRanges:           ips,
		}, nil
	}
	rootTpl, err := template(cn, 1, false)
	if err != nil {
		return err
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTpl, rootTpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("making the CA root certificate: %w", err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return err
	}
	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("making the CA intermediate key: %w", err)
	}
	interTpl, err := template(cn+" intermediate", 0, true)
	if err != nil {
		return err
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTpl, root, &interKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("making the CA intermediate certificate: %w", err)
	}
	// rootKey goes out of scope here and is never marshalled: it is not written anywhere.
	rootKey = nil
	inter, err := x509.ParseCertificate(interDER)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(interKey)
	if err != nil {
		return err
	}
	if err := c.install(map[string][]byte{
		rootFile:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		intermediateFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER}),
		intermediateKey:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}); err != nil {
		return err
	}
	c.root, c.inter, c.key = root, inter, interKey
	return nil
}

// install writes files into a scratch directory beside tls/ and renames it to tls/.
func (c *CA) install(files map[string][]byte) error {
	parent := filepath.Dir(c.dir)
	tmp, err := os.MkdirTemp(parent, ".tls-new-")
	if err != nil {
		return fmt.Errorf("cannot create the CA in %q: %w", parent, err)
	}
	defer os.RemoveAll(tmp) // does nothing once renamed
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), data, fileMode); err != nil {
			return fmt.Errorf("cannot write the CA file %q: %w", filepath.Join(c.dir, name), err)
		}
	}
	if err := os.Chmod(tmp, dirMode); err != nil {
		return err
	}
	// An empty tls/ the owner made on purpose is replaced (Remove refuses a directory with anything in it).
	if err := os.Remove(c.dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot replace the empty directory %q: %w", c.dir, err)
	}
	if err := os.Rename(tmp, c.dir); err != nil {
		return fmt.Errorf("cannot create the CA directory %q: %w", c.dir, err)
	}
	return nil
}

// load reads the CA back and checks it is whole and consistent.
func (c *CA) load() error {
	damaged := func(why string, args ...any) error {
		return fmt.Errorf("the CA in %q is damaged (%s). Restore tls/ from your backup of the data directory: "+
			"a new CA would make every device that trusts this installation do the trust step again, so none is made on its own",
			c.dir, fmt.Sprintf(why, args...))
	}
	rootCert, err := readCert(filepath.Join(c.dir, rootFile))
	if err != nil {
		return damaged("%v", err)
	}
	interCert, err := readCert(filepath.Join(c.dir, intermediateFile))
	if err != nil {
		return damaged("%v", err)
	}
	key, err := readKey(filepath.Join(c.dir, intermediateKey))
	if err != nil {
		return damaged("%v", err)
	}
	if err := interCert.CheckSignatureFrom(rootCert); err != nil {
		return damaged("the intermediate is not signed by the root: %v", err)
	}
	pub, ok := interCert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return damaged("the intermediate key does not belong to the intermediate certificate")
	}
	if len(interCert.PermittedIPRanges) == 0 || len(interCert.PermittedDNSDomains) == 0 {
		return damaged("the intermediate has no name constraints")
	}
	if now := c.now(); now.After(interCert.NotAfter) {
		return damaged("the intermediate expired on %s", interCert.NotAfter.Format("2006-01-02"))
	}
	c.root, c.inter, c.key = rootCert, interCert, key
	return nil
}

func readCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no certificate", filepath.Base(path))
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s is not a certificate: %w", filepath.Base(path), err)
	}
	return cert, nil
}

func readKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s holds no private key", filepath.Base(path))
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s is not a private key: %w", filepath.Base(path), err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an ECDSA key", filepath.Base(path))
	}
	return ec, nil
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// dirEmptyOrMissing is true for a directory that isn't there or has nothing in it.
func dirEmptyOrMissing(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot read the tls directory %q: %w", path, err)
	}
	return len(entries) == 0, nil
}

func serialNumber() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	return n.Add(n, big.NewInt(1)), nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// interfaceIPs are the addresses of the machine's network interfaces.
func interfaceIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			out = append(out, n.IP)
		}
	}
	return out, nil
}

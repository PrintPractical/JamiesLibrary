package tlsca

import (
	"container/list"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"jamieslibrary/internal/config"
)

// GetCertificate is the tls.Config callback of the HTTPS listener.
//
// A client that names a host inside the CA's constraints gets a certificate
// for that name. Anything else (an address, which carries no name, or a name
// outside the constraints) gets the certificate for the machine's own
// addresses plus JL_ADDRESSES, so a device that reaches the installation by
// address or by a name the CA can't vouch for sees the usual certificate
// warning and can click through (SCAN-T2).
func (c *CA) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if name := sniName(hello.ServerName); name != "" && c.permitsDNS(name) {
		return c.leafFor("dns:"+name, []string{name}, nil)
	}
	ips, err := c.serverIPs()
	if err != nil {
		return nil, err
	}
	key := make([]string, len(ips))
	for i, ip := range ips {
		key[i] = ip.String()
	}
	return c.leafFor("ip:"+strings.Join(key, ","), nil, ips)
}

// sniName is the host name a client asked for, in lower case, or "" when there
// is none or it is an address or isn't a host name.
func sniName(sni string) string {
	name := strings.ToLower(strings.TrimSuffix(sni, "."))
	if name == "" || net.ParseIP(name) != nil || !config.ValidHostname(name) {
		return ""
	}
	return name
}

// serverIPs are the addresses the certificate for IP connections names: the
// machine's interface addresses and JL_ADDRESSES, those the CA may vouch for.
func (c *CA) serverIPs() ([]net.IP, error) {
	local, err := c.opts.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("cannot list this machine's addresses: %w", err)
	}
	seen := map[string]bool{}
	var out []net.IP
	for _, ip := range append(append([]net.IP(nil), c.opts.Addresses...), local...) {
		if ip == nil || !c.permitsIP(ip) || seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		out = append(out, ip)
	}
	if len(out) == 0 {
		return nil, errors.New("this machine has no local address the CA can vouch for")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if len(out) > maxLeafAddress {
		out = out[:maxLeafAddress]
	}
	return out, nil
}

// leafFor returns the certificate for the names, from the cache, from disk,
// or newly issued: renewed once it is within RenewBefore of expiry.
func (c *CA) leafFor(key string, dns []string, ips []net.IP) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if e := c.leaves.get(key); e != nil {
		if c.usable(e.leaf, dns, ips, now) {
			return e.cert, nil
		}
	} else if cert := c.leaves.read(key, c); cert != nil && c.usable(cert.Leaf, dns, ips, now) {
		c.leaves.put(key, cert)
		return cert, nil
	}
	cert, err := c.issue(dns, ips, now)
	if err != nil {
		return nil, err
	}
	c.leaves.put(key, cert)
	if err := c.leaves.write(key, cert); err != nil {
		c.log.Warn("could not store a server certificate; it is kept in memory", "err", err)
	}
	return cert, nil
}

// usable says whether leaf still serves for these names: signed by this
// intermediate, naming exactly them, and not yet due for renewal.
func (c *CA) usable(leaf *x509.Certificate, dns []string, ips []net.IP, now time.Time) bool {
	if now.Add(RenewBefore).After(leaf.NotAfter) || now.Before(leaf.NotBefore) {
		return false
	}
	if leaf.CheckSignatureFrom(c.inter) != nil || !sameStrings(leaf.DNSNames, dns) || len(leaf.IPAddresses) != len(ips) {
		return false
	}
	for i, ip := range ips {
		if !leaf.IPAddresses[i].Equal(ip) {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// issue signs a new leaf: ECDSA P-256, serverAuth, names in the SAN, empty subject.
func (c *CA) issue(dns []string, ips []net.IP, now time.Time) (*tls.Certificate, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("making a server key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    now.Add(-backdate),
		NotAfter:     now.Add(LeafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.inter, &k.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("signing a server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, c.inter.Raw}, PrivateKey: k, Leaf: leaf}, nil
}

// leafCache holds at most MaxLeaves certificates, least recently used out
// first, and keeps each one in a file in dir so a restart reuses it.
type leafCache struct {
	dir     string
	log     *slog.Logger
	order   *list.List // front = most recently used; values are *leafEntry
	entries map[string]*list.Element
}

type leafEntry struct {
	key  string
	cert *tls.Certificate
	leaf *x509.Certificate
}

func newLeafCache(dir string, log *slog.Logger) *leafCache {
	return &leafCache{dir: dir, log: log, order: list.New(), entries: map[string]*list.Element{}}
}

// prepare makes the directory and keeps what an earlier run left to MaxLeaves
// files (the newest) and certificates that can still be read.
func (l *leafCache) prepare(now time.Time) error {
	if err := os.MkdirAll(l.dir, dirMode); err != nil {
		return fmt.Errorf("cannot create %q: %w", l.dir, err)
	}
	if err := os.Chmod(l.dir, dirMode); err != nil {
		return fmt.Errorf("cannot restrict %q to its owner (0700): %w", l.dir, err)
	}
	ents, err := os.ReadDir(l.dir)
	if err != nil {
		return fmt.Errorf("cannot read %q: %w", l.dir, err)
	}
	type file struct {
		name string
		mod  time.Time
	}
	var files []file
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".pem") {
			continue
		}
		files = append(files, file{e.Name(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i, f := range files {
		if i >= MaxLeaves {
			_ = os.Remove(filepath.Join(l.dir, f.name))
		}
	}
	return nil
}

func (l *leafCache) get(key string) *leafEntry {
	el := l.entries[key]
	if el == nil {
		return nil
	}
	l.order.MoveToFront(el)
	return el.Value.(*leafEntry)
}

func (l *leafCache) put(key string, cert *tls.Certificate) {
	if el := l.entries[key]; el != nil {
		l.order.Remove(el)
	}
	l.entries[key] = l.order.PushFront(&leafEntry{key: key, cert: cert, leaf: cert.Leaf})
	for l.order.Len() > MaxLeaves {
		oldest := l.order.Back()
		e := l.order.Remove(oldest).(*leafEntry)
		delete(l.entries, e.key)
		_ = os.Remove(l.path(e.key))
	}
}

// Len is how many certificates are held in memory.
func (l *leafCache) Len() int { return l.order.Len() }

func (l *leafCache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(l.dir, hex.EncodeToString(sum[:16])+".pem")
}

// write stores the chain and the key in one 0600 file, replacing any older one.
func (l *leafCache) write(key string, cert *tls.Certificate) error {
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return err
	}
	var data []byte
	for _, der := range cert.Certificate {
		data = append(data, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	data = append(data, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)
	tmp, err := os.CreateTemp(l.dir, ".leaf-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), l.path(key))
}

// read loads a stored certificate, or nil when there is none or it can't be
// trusted: it must hold a leaf signed by this CA's intermediate and its key.
func (l *leafCache) read(key string, c *CA) *tls.Certificate {
	data, err := os.ReadFile(l.path(key))
	if err != nil {
		return nil
	}
	var chain [][]byte
	var k *ecdsa.PrivateKey
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			break
		}
		switch b.Type {
		case "CERTIFICATE":
			chain = append(chain, b.Bytes)
		case "PRIVATE KEY":
			if pk, err := x509.ParsePKCS8PrivateKey(b.Bytes); err == nil {
				k, _ = pk.(*ecdsa.PrivateKey)
			}
		}
	}
	if len(chain) == 0 || k == nil {
		return nil
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil || leaf.CheckSignatureFrom(c.inter) != nil {
		return nil
	}
	if pub, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&k.PublicKey) {
		return nil
	}
	return &tls.Certificate{Certificate: [][]byte{chain[0], c.inter.Raw}, PrivateKey: k, Leaf: leaf}
}

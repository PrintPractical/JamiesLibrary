package tlsca

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// recheckEvery is how often the owner's certificate files are looked at for a change.
const recheckEvery = time.Second

// FileCert serves the owner's own certificate (JL_TLS_CERT, JL_TLS_KEY) and
// picks up a renewed one when the files change, with no restart. A renewal
// that can't be read keeps the old certificate in use and is logged.
type FileCert struct {
	certPath, keyPath string
	log               *slog.Logger
	recheck           time.Duration // how often to look at the files

	mu       sync.Mutex
	cert     *tls.Certificate
	certSeen stamp
	keySeen  stamp
	checked  time.Time
	lastErr  string
}

type stamp struct {
	mod  time.Time
	size int64
}

// LoadFiles reads the owner's certificate and key. A file that is missing,
// isn't PEM, or a key that doesn't belong to the certificate is an error that
// names the file.
func LoadFiles(certPath, keyPath string, log *slog.Logger) (*FileCert, error) {
	if log == nil {
		log = slog.Default()
	}
	f := &FileCert{certPath: certPath, keyPath: keyPath, log: log, recheck: recheckEvery}
	if err := f.reload(); err != nil {
		return nil, err
	}
	if leaf := f.cert.Leaf; leaf != nil && time.Now().After(leaf.NotAfter) {
		log.Warn("the certificate in JL_TLS_CERT has expired; browsers will refuse it", "expired", leaf.NotAfter.Format(time.RFC3339))
	}
	return f, nil
}

func statOf(path string) (stamp, error) {
	st, err := os.Stat(path)
	if err != nil {
		return stamp{}, err
	}
	return stamp{st.ModTime(), st.Size()}, nil
}

func (f *FileCert) reload() error {
	cs, err := statOf(f.certPath)
	if err != nil {
		return fmt.Errorf("JL_TLS_CERT: cannot read %q: %w", f.certPath, err)
	}
	ks, err := statOf(f.keyPath)
	if err != nil {
		return fmt.Errorf("JL_TLS_KEY: cannot read %q: %w", f.keyPath, err)
	}
	cert, err := tls.LoadX509KeyPair(f.certPath, f.keyPath)
	if err != nil {
		return fmt.Errorf("the certificate %q and key %q can't be used (both must be PEM, and the key must belong to the certificate): %w", f.certPath, f.keyPath, err)
	}
	if cert.Leaf == nil {
		if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return fmt.Errorf("the certificate %q can't be used: %w", f.certPath, err)
		}
	}
	f.cert, f.certSeen, f.keySeen = &cert, cs, ks
	return nil
}

// GetCertificate is the tls.Config callback.
func (f *FileCert) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if now := time.Now(); now.Sub(f.checked) >= f.recheck {
		f.checked = now
		cs, cerr := statOf(f.certPath)
		ks, kerr := statOf(f.keyPath)
		if cerr == nil && kerr == nil && (cs != f.certSeen || ks != f.keySeen) {
			if err := f.reload(); err != nil {
				if msg := err.Error(); msg != f.lastErr {
					f.lastErr = msg
					f.log.Warn("the certificate files changed but can't be used; still serving the previous certificate", "err", err)
				}
			} else {
				f.lastErr = ""
				f.log.Info("reloaded the certificate", "cert", f.certPath)
			}
		}
	}
	return f.cert, nil
}

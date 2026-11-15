// Package server is the server's entry point: the same Run serves for the
// jamieslibrary command and for tests that start it in-process.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"jamieslibrary/internal/config"
	"jamieslibrary/internal/httpapi"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/testhooks"
	"jamieslibrary/internal/tlsca"
	"jamieslibrary/internal/webui"
)

// Options is everything Run needs from outside.
type Options struct {
	Args    []string
	Getenv  func(string) string
	Stdout  io.Writer // what people and tests read: the setup code line goes here
	Stderr  io.Writer // the log
	Version string

	// Migrations replaces the embedded migrations (tests).
	Migrations fs.FS

	// Listener, when set, is where the app is served instead of the configured
	// port (tests, which take a free port): over HTTPS, or over plain HTTP in
	// JL_HTTP_ONLY mode.
	Listener net.Listener
	// TrustListener, when set, is the plain-HTTP listener (trust downloads and
	// redirect) instead of JL_HTTP_PORT.
	TrustListener net.Listener
	// Ready, when set, is called once the server answers, with the addresses it is on.
	Ready func(Ready)
}

// Ready says where a running server can be reached.
type Ready struct {
	HTTPAddr    string // host:port of the web server: HTTPS unless TLS is false
	TLS         bool   // the web server speaks HTTPS
	TrustAddr   string // host:port of the plain-HTTP listener, "" when there is none
	Fingerprint string // the local CA root's SHA-256 fingerprint, "" without a local CA
	ControlAddr string // host:port of the test control listener, "" when there is none
}

// Run starts the server and returns when ctx ends and it has shut down, or
// when it cannot start or fails. An error is one a person can act on.
func Run(ctx context.Context, o Options) error {
	cfg, err := config.Parse(o.Args, o.Getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(o.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	control, err := testhooks.Start(o.Getenv)
	if err != nil {
		return err
	}
	defer control.Close()
	// A library that already exists must find its CA again (tlsca.Open).
	dbExisted := exists(filepath.Join(cfg.DataDir, "library.db"))
	st, err := store.Open(ctx, cfg.DataDir, store.Options{Migrations: o.Migrations})
	if err != nil {
		return err
	}
	defer st.Close()
	tlsCfg, ca, err := tlsSetup(cfg, st.Dir(), dbExisted, log)
	if err != nil {
		return err
	}
	if ca != nil {
		fmt.Fprintf(o.Stdout, "Local CA root SHA-256 fingerprint: %s\n", ca.Fingerprint())
		log.Info("local CA ready", "created", ca.Created(), "fingerprint", ca.Fingerprint())
	}
	go st.RunTmpCleaner(ctx, time.Hour, func(err error) { log.Warn("cleaning tmp failed", "err", err) })

	ln, plainLn := o.Listener, o.TrustListener
	defer func() {
		// Run returns before Serve has taken a listener only on an error; Close of a used one is harmless.
		for _, l := range []net.Listener{ln, plainLn} {
			if l != nil {
				_ = l.Close()
			}
		}
	}()
	appPort := cfg.HTTPSPort
	if cfg.HTTPOnly {
		appPort = cfg.HTTPPort
	}
	if ln == nil {
		if ln, err = net.Listen("tcp", ":"+strconv.Itoa(appPort)); err != nil {
			return fmt.Errorf("cannot listen on port %d: %w", appPort, err)
		}
	}
	if !cfg.HTTPOnly && plainLn == nil {
		if plainLn, err = net.Listen("tcp", ":"+strconv.Itoa(cfg.HTTPPort)); err != nil {
			return fmt.Errorf("cannot listen on port %d: %w", cfg.HTTPPort, err)
		}
	}
	h := httpapi.NewHandler(httpapi.Options{Version: o.Version, Logger: log, Static: webui.Handler()})
	r := Ready{HTTPAddr: ln.Addr().String(), TLS: tlsCfg != nil, ControlAddr: control.Addr()}
	if ca != nil {
		r.Fingerprint = ca.Fingerprint()
	}
	if plainLn != nil {
		r.TrustAddr = plainLn.Addr().String()
	}
	log.Info("listening", "addr", r.HTTPAddr, "tls", r.TLS, "trust_addr", r.TrustAddr, "version", o.Version)
	if o.Ready != nil {
		o.Ready(r)
	}

	// Both listeners stop together: when ctx ends, or when either fails.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	appServe := ln
	if tlsCfg != nil {
		appServe = tls.NewListener(ln, tlsCfg)
	}
	n := 1
	errs := make(chan error, 2)
	go func() { errs <- httpapi.Serve(ctx, appServe, h, log) }()
	if plainLn != nil {
		n++
		go func() { errs <- httpapi.Serve(ctx, plainLn, tlsca.PlainHandler(ca, cfg.HTTPSPort), log) }()
	}
	var first error
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
		cancel()
	}
	return first
}

// tlsSetup chooses how the app is served: with no TLS configuration (plain
// HTTP for a proxy), with the owner's certificate, or with the local CA.
func tlsSetup(cfg config.Config, dataDir string, dbExisted bool, log *slog.Logger) (*tls.Config, *tlsca.CA, error) {
	// Without a local CA the data directory records that none was ever made, so
	// a later switch to the local CA isn't taken for a lost tls/ directory.
	marker := filepath.Join(dataDir, noCAMarker)
	newConfig := func(get func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
		return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: get, NextProtos: []string{"h2", "http/1.1"}}
	}
	switch {
	case cfg.HTTPOnly:
		return nil, nil, noteNoCA(dataDir, marker)
	case cfg.TLSCert != "":
		fc, err := tlsca.LoadFiles(cfg.TLSCert, cfg.TLSKey, log)
		if err != nil {
			return nil, nil, err
		}
		return newConfig(fc.GetCertificate), nil, noteNoCA(dataDir, marker)
	}
	ca, err := tlsca.Open(dataDir, tlsca.Options{
		Hostnames:    cfg.Hostnames,
		Addresses:    cfg.Addresses,
		ExistingData: dbExisted && !exists(marker),
		Logger:       log,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("cannot remove %q: %w", marker, err)
	}
	return newConfig(ca.GetCertificate), ca, nil
}

// noCAMarker is the file in the data directory that says the installation has
// run without a local CA.
const noCAMarker = "no-local-ca"

func noteNoCA(dataDir, marker string) error {
	if exists(filepath.Join(dataDir, tlsca.DirName)) {
		return nil
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		return fmt.Errorf("cannot write %q: %w", marker, err)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

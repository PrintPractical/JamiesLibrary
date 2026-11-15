// Package server is the server's entry point: the same Run serves for the
// jamieslibrary command and for tests that start it in-process.
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/config"
	"jamieslibrary/internal/httpapi"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/testhooks"
	"jamieslibrary/internal/tlsca"
	"jamieslibrary/internal/users"
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
	st, err := store.Open(ctx, cfg.DataDir, store.Options{Migrations: o.Migrations})
	if err != nil {
		return err
	}
	defer st.Close()
	tlsCfg, ca, err := tlsSetup(cfg, st.Dir(), log)
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
	if cfg.HTTPOnly && cfg.BindAddress == "" {
		log.Warn("the app, sign-in included, is served over plain HTTP on every network interface; " +
			"behind a reverse proxy on the same host set JL_BIND_ADDRESS=127.0.0.1 (or --bind), " +
			"or the address of the network the proxy is on, so only the proxy can reach it")
	}
	appPort := cfg.HTTPSPort
	if cfg.HTTPOnly {
		appPort = cfg.HTTPPort
	}
	if ln == nil {
		if ln, err = net.Listen("tcp", net.JoinHostPort(cfg.BindAddress, strconv.Itoa(appPort))); err != nil {
			return fmt.Errorf("cannot listen on port %d: %w", appPort, err)
		}
	}
	if !cfg.HTTPOnly && plainLn == nil {
		if plainLn, err = net.Listen("tcp", net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.HTTPPort))); err != nil {
			return fmt.Errorf("cannot listen on port %d: %w", cfg.HTTPPort, err)
		}
	}
	set, err := settings.New(ctx, st)
	if err != nil {
		return err
	}
	usersSvc := users.New(st)
	authSvc := auth.NewService(st, usersSvc,
		func() time.Duration { return time.Duration(set.Get().SessionIdleMinutes) * time.Minute }, log)
	h, err := httpapi.NewHandler(httpapi.Options{
		Version: o.Version, Logger: log, Static: webui.Handler(),
		Auth: authSvc, Users: usersSvc, Settings: set,
		PublicCatalogOn: func() bool { return set.Get().PublicCatalog },
	})
	if err != nil {
		return err
	}
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
func tlsSetup(cfg config.Config, dataDir string, log *slog.Logger) (*tls.Config, *tlsca.CA, error) {
	newConfig := func(get func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
		return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: get, NextProtos: []string{"h2", "http/1.1"}}
	}
	switch {
	case cfg.HTTPOnly:
		return nil, nil, nil
	case cfg.TLSCert != "":
		fc, err := tlsca.LoadFiles(cfg.TLSCert, cfg.TLSKey, log)
		if err != nil {
			return nil, nil, err
		}
		return newConfig(fc.GetCertificate), nil, nil
	}
	// A CA that was made once must be found again: its marker outlives tls/, so
	// a lost tls/ is an error. A data directory where no start ever got as far
	// as making a CA (a failed first start, a proxy install) just gets one.
	marker := filepath.Join(dataDir, caMadeMarker)
	ca, err := tlsca.Open(dataDir, tlsca.Options{
		Hostnames:    cfg.Hostnames,
		Addresses:    cfg.Addresses,
		ExistingData: exists(marker),
		Logger:       log,
	})
	if err != nil {
		return nil, nil, err
	}
	if !exists(marker) {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			return nil, nil, fmt.Errorf("cannot write %q: %w", marker, err)
		}
	}
	return newConfig(ca.GetCertificate), ca, nil
}

// caMadeMarker is the file in the data directory that says a local CA exists
// for this installation, so tls/ going missing is noticed.
const caMadeMarker = "local-ca-made"

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

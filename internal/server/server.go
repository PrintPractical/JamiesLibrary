// Package server is the server's entry point: the same Run serves for the
// jamieslibrary command and for tests that start it in-process.
package server

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"strconv"
	"time"

	"jamieslibrary/internal/config"
	"jamieslibrary/internal/httpapi"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/testhooks"
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

	// Listener, when set, is used instead of listening on the configured port
	// (tests, which take a free port).
	Listener net.Listener
	// Ready, when set, is called once the server answers, with the addresses it is on.
	Ready func(Ready)
}

// Ready says where a running server can be reached.
type Ready struct {
	HTTPAddr    string // host:port of the web server
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
	go st.RunTmpCleaner(ctx, time.Hour, func(err error) { log.Warn("cleaning tmp failed", "err", err) })
	ln := o.Listener
	if ln == nil {
		if ln, err = net.Listen("tcp", ":"+strconv.Itoa(cfg.HTTPPort)); err != nil {
			return fmt.Errorf("cannot listen on port %d: %w", cfg.HTTPPort, err)
		}
	}
	log.Info("listening", "addr", ln.Addr().String(), "version", o.Version)
	set, err := settings.New(ctx, st)
	if err != nil {
		return err
	}
	h, err := httpapi.NewHandler(httpapi.Options{
		Version: o.Version, Logger: log, Static: webui.Handler(),
		PublicCatalogOn: func() bool { return set.Get().PublicCatalog },
	})
	if err != nil {
		return err
	}
	if o.Ready != nil {
		o.Ready(Ready{HTTPAddr: ln.Addr().String(), ControlAddr: control.Addr()})
	}
	return httpapi.Serve(ctx, ln, h, log)
}

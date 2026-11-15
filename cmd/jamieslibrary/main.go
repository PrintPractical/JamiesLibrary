// Command jamieslibrary runs Jamie's Library: a lending-library server with the web app built in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"jamieslibrary/internal/config"
	"jamieslibrary/internal/httpapi"
	"jamieslibrary/internal/webui"
)

// version is set at build time: -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		if err := serve(ctx, args, getenv, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(stderr, "usage: jamieslibrary [serve] [--data DIR] [--port N] [--log-level LEVEL]")
				return 0
			}
			fmt.Fprintln(stderr, "jamieslibrary:", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "jamieslibrary: unknown command %q (use serve or version)\n", cmd)
		return 2
	}
}

func serve(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) error {
	cfg, err := config.Parse(args, getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("cannot use the data directory %q: %w", cfg.DataDir, err)
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.HTTPPort))
	if err != nil {
		return fmt.Errorf("cannot listen on port %d: %w", cfg.HTTPPort, err)
	}
	log.Info("listening", "addr", ln.Addr().String(), "version", version)
	h := httpapi.NewHandler(httpapi.Options{Version: version, Logger: log, Static: webui.Handler()})
	return httpapi.Serve(ctx, ln, h, log)
}

// Command jamieslibrary runs Jamie's Library: a lending-library server with the web app built in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"jamieslibrary/internal/server"
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
		if err := server.Run(ctx, server.Options{Args: args, Getenv: getenv, Stdout: stdout, Stderr: stderr, Version: version}); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(stderr, "usage: jamieslibrary [serve] [--data DIR] [--port N] [--https-port N] [--log-level LEVEL]")
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

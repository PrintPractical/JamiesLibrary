// Package config reads the server's settings from flags and JL_* environment variables.
package config

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// Config is the validated server configuration.
type Config struct {
	DataDir  string
	HTTPPort int
	LogLevel slog.Level
}

const (
	defaultDataDir  = "./data"
	defaultHTTPPort = "8080"
	defaultLogLevel = "info"
)

var levels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// Parse builds a Config from command-line flags and the environment (getenv).
// A flag beats its environment variable, which beats the default. A bad value
// is reported by the name of the flag or variable it came from.
func Parse(args []string, getenv func(string) string) (Config, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	env := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return def
	}
	dataDir := fs.String("data", env("JL_DATA_DIR", defaultDataDir), "data directory (JL_DATA_DIR)")
	port := fs.String("port", env("JL_HTTP_PORT", defaultHTTPPort), "HTTP port (JL_HTTP_PORT)")
	level := fs.String("log-level", env("JL_LOG_LEVEL", defaultLogLevel), "log level: debug, info, warn, error (JL_LOG_LEVEL)")
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("invalid command line: %w", err)
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	source := func(flagName, envName string) string {
		if set[flagName] {
			return "--" + flagName
		}
		return envName
	}

	var c Config
	c.DataDir = strings.TrimSpace(*dataDir)
	if c.DataDir == "" {
		return Config{}, fmt.Errorf("%s must not be empty", source("data", "JL_DATA_DIR"))
	}
	p, err := strconv.Atoi(strings.TrimSpace(*port))
	if err != nil || p < 1 || p > 65535 {
		return Config{}, fmt.Errorf("%s must be a port number from 1 to 65535, got %q", source("port", "JL_HTTP_PORT"), *port)
	}
	c.HTTPPort = p
	lvl, ok := levels[strings.ToLower(strings.TrimSpace(*level))]
	if !ok {
		return Config{}, fmt.Errorf("%s must be one of debug, info, warn, error, got %q", source("log-level", "JL_LOG_LEVEL"), *level)
	}
	c.LogLevel = lvl
	return c, nil
}

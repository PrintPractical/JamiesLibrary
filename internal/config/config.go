// Package config reads the server's settings from flags and JL_* environment variables.
package config

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
)

// Config is the validated server configuration.
type Config struct {
	DataDir string
	// HTTPPort is the plain-HTTP port: the trust downloads and the redirect to
	// HTTPS, or the whole app when HTTPOnly is set.
	HTTPPort int
	// HTTPSPort is where the app is served over HTTPS (unused when HTTPOnly).
	HTTPSPort int
	// Addresses are extra local addresses the HTTPS certificate must name
	// (JL_ADDRESSES), for installs that can't see their real address.
	Addresses []net.IP
	// Hostnames are the owner's own host names (JL_HOSTNAMES), added to the
	// local CA's name constraints at its first start only.
	Hostnames []string
	// TLSCert and TLSKey are the owner's own certificate and key files; both or neither.
	TLSCert, TLSKey string
	// HTTPOnly serves the app over plain HTTP, for a reverse proxy.
	HTTPOnly bool
	LogLevel slog.Level
}

const (
	defaultDataDir  = "./data"
	defaultHTTPPort = "8080"

	defaultHTTPSPort = "8443"
	defaultLogLevel  = "info"
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
	httpsPort := fs.String("https-port", env("JL_HTTPS_PORT", defaultHTTPSPort), "HTTPS port (JL_HTTPS_PORT)")
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
	sp, err := strconv.Atoi(strings.TrimSpace(*httpsPort))
	if err != nil || sp < 1 || sp > 65535 {
		return Config{}, fmt.Errorf("%s must be a port number from 1 to 65535, got %q", source("https-port", "JL_HTTPS_PORT"), *httpsPort)
	}
	c.HTTPSPort = sp
	if c.Addresses, err = parseAddresses(env("JL_ADDRESSES", "")); err != nil {
		return Config{}, err
	}
	if c.Hostnames, err = parseHostnames(env("JL_HOSTNAMES", "")); err != nil {
		return Config{}, err
	}
	c.TLSCert, c.TLSKey = env("JL_TLS_CERT", ""), env("JL_TLS_KEY", "")
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return Config{}, fmt.Errorf("JL_TLS_CERT and JL_TLS_KEY go together: set both, or neither")
	}
	switch strings.ToLower(env("JL_HTTP_ONLY", "")) {
	case "", "0", "false", "no":
	case "1", "true", "yes":
		c.HTTPOnly = true
	default:
		return Config{}, fmt.Errorf("JL_HTTP_ONLY must be 1 or 0, got %q", env("JL_HTTP_ONLY", ""))
	}
	if c.HTTPOnly && c.TLSCert != "" {
		return Config{}, fmt.Errorf("JL_HTTP_ONLY and JL_TLS_CERT conflict: the app is served either over plain HTTP for a proxy, or over HTTPS with your certificate")
	}
	if !c.HTTPOnly && c.HTTPPort == c.HTTPSPort {
		return Config{}, fmt.Errorf("JL_HTTP_PORT and JL_HTTPS_PORT must differ, both are %d", c.HTTPPort)
	}
	lvl, ok := levels[strings.ToLower(strings.TrimSpace(*level))]
	if !ok {
		return Config{}, fmt.Errorf("%s must be one of debug, info, warn, error, got %q", source("log-level", "JL_LOG_LEVEL"), *level)
	}
	c.LogLevel = lvl
	return c, nil
}

// splitList splits a comma- or space-separated list, dropping empty items.
func splitList(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

func parseAddresses(v string) ([]net.IP, error) {
	var out []net.IP
	for _, item := range splitList(v) {
		ip := net.ParseIP(item)
		if ip == nil {
			return nil, fmt.Errorf("JL_ADDRESSES must be a list of IP addresses, %q is not one", item)
		}
		out = append(out, ip)
	}
	return out, nil
}

// parseHostnames checks and lower-cases JL_HOSTNAMES. A name is dot-separated
// labels of letters, digits and hyphens: no wildcard, port or address.
func parseHostnames(v string) ([]string, error) {
	var out []string
	for _, item := range splitList(v) {
		name := strings.ToLower(strings.TrimSuffix(item, "."))
		if err := CheckOwnerHostname(name); err != nil {
			return nil, fmt.Errorf("JL_HOSTNAMES: %q: %w", item, err)
		}
		out = append(out, name)
	}
	return out, nil
}

// ValidHostname reports whether name is a plain DNS host name (lower or upper
// case): 1 to 253 characters in labels of 1 to 63 letters, digits and hyphens,
// none starting or ending with a hyphen.
func ValidHostname(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// CheckOwnerHostname checks a name the owner wants the local CA to vouch for.
// It needs at least two labels: a constraint of "app" or "com" would let the CA
// vouch for every site under that public top-level domain.
func CheckOwnerHostname(name string) error {
	if !ValidHostname(name) || net.ParseIP(name) != nil {
		return fmt.Errorf("it is not a host name such as library.example.com")
	}
	if !strings.Contains(name, ".") {
		return fmt.Errorf("a name needs at least two parts, like library.example.com or nas.local: a single word such as %q would make the CA vouch for every site ending in it. Use name.local, or the server's IP address", name)
	}
	return nil
}

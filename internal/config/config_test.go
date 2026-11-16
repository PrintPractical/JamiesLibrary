package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := Parse(nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "./data" || c.HTTPPort != 8080 || c.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestEnvironment(t *testing.T) {
	c, err := Parse(nil, env(map[string]string{"JL_DATA_DIR": "/srv/jl", "JL_HTTP_PORT": "9000", "JL_LOG_LEVEL": "DEBUG"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/srv/jl" || c.HTTPPort != 9000 || c.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestFlagsWinOverEnvironment(t *testing.T) {
	c, err := Parse([]string{"--data", "/flag", "--port", "9100", "--log-level", "warn"},
		env(map[string]string{"JL_DATA_DIR": "/env", "JL_HTTP_PORT": "9000", "JL_LOG_LEVEL": "debug"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/flag" || c.HTTPPort != 9100 || c.LogLevel != slog.LevelWarn {
		t.Fatalf("flags did not win: %+v", c)
	}
}

func TestBadValuesNameTheSetting(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want []string
	}{
		{"env port text", nil, map[string]string{"JL_HTTP_PORT": "abc"}, []string{"JL_HTTP_PORT", `"abc"`}},
		{"env port zero", nil, map[string]string{"JL_HTTP_PORT": "0"}, []string{"JL_HTTP_PORT"}},
		{"env port high", nil, map[string]string{"JL_HTTP_PORT": "70000"}, []string{"JL_HTTP_PORT", `"70000"`}},
		{"flag port", []string{"--port", "-3"}, nil, []string{"--port", `"-3"`}},
		{"flag beats good env but is bad", []string{"--port", "x"}, map[string]string{"JL_HTTP_PORT": "80"}, []string{"--port"}},
		{"env level", nil, map[string]string{"JL_LOG_LEVEL": "loud"}, []string{"JL_LOG_LEVEL", `"loud"`}},
		{"flag level", []string{"--log-level", "info+2"}, nil, []string{"--log-level"}},
		{"blank flag data dir", []string{"--data", "  "}, nil, []string{"--data"}},
		{"unknown flag", []string{"--nope"}, nil, []string{"nope"}},
		{"stray argument", []string{"extra"}, nil, []string{`"extra"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.args, env(tc.env))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %s", err, w)
				}
			}
		})
	}
}

func TestTLSSettings(t *testing.T) {
	c, err := Parse(nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPSPort != 8443 || c.HTTPOnly || c.TLSCert != "" || len(c.Addresses) != 0 || len(c.Hostnames) != 0 {
		t.Fatalf("defaults: %+v", c)
	}
	c, err = Parse([]string{"--https-port", "9443"}, env(map[string]string{
		"JL_ADDRESSES": "192.168.1.5, fe80::1", "JL_HOSTNAMES": "Library.Example.org nas.home.arpa.",
		"JL_TLS_CERT": "/c.pem", "JL_TLS_KEY": "/k.pem", "JL_HTTPS_PORT": "1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPSPort != 9443 || len(c.Addresses) != 2 || c.TLSCert != "/c.pem" ||
		strings.Join(c.Hostnames, ",") != "library.example.org,nas.home.arpa" {
		t.Fatalf("%+v", c)
	}
	if c, err = Parse(nil, env(map[string]string{"JL_HTTP_ONLY": "1"})); err != nil || !c.HTTPOnly {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestTLSBadValuesNameTheSetting(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"https port":      {map[string]string{"JL_HTTPS_PORT": "x"}, "JL_HTTPS_PORT"},
		"address":         {map[string]string{"JL_ADDRESSES": "192.168.1.5,nope"}, `JL_ADDRESSES`},
		"hostname":        {map[string]string{"JL_HOSTNAMES": "a b/c"}, "JL_HOSTNAMES"},
		"wildcard":        {map[string]string{"JL_HOSTNAMES": "*.example.org"}, "JL_HOSTNAMES"},
		"address as name": {map[string]string{"JL_HOSTNAMES": "10.0.0.1"}, "JL_HOSTNAMES"},
		"single label":    {map[string]string{"JL_HOSTNAMES": "app"}, "JL_HOSTNAMES"},
		"public tld":      {map[string]string{"JL_HOSTNAMES": "library.example.org com"}, `"com"`},
		"cert alone":      {map[string]string{"JL_TLS_CERT": "/c.pem"}, "JL_TLS_KEY"},
		"key alone":       {map[string]string{"JL_TLS_KEY": "/k.pem"}, "JL_TLS_CERT"},
		"http only":       {map[string]string{"JL_HTTP_ONLY": "maybe"}, "JL_HTTP_ONLY"},
		"proxy and cert":  {map[string]string{"JL_HTTP_ONLY": "1", "JL_TLS_CERT": "/c", "JL_TLS_KEY": "/k"}, "JL_HTTP_ONLY"},
		"same ports":      {map[string]string{"JL_HTTP_PORT": "9000", "JL_HTTPS_PORT": "9000"}, "must differ"},
	} {
		if _, err := Parse(nil, env(tc.env)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestBindAddress(t *testing.T) {
	if c, err := Parse(nil, env(nil)); err != nil || c.BindAddress != "" {
		t.Fatalf("default: %+v %v", c, err)
	}
	if c, err := Parse(nil, env(map[string]string{"JL_BIND_ADDRESS": " ::1 "})); err != nil || c.BindAddress != "::1" {
		t.Fatalf("env: %+v %v", c, err)
	}
	if c, err := Parse([]string{"--bind", "127.0.0.1"}, env(map[string]string{"JL_BIND_ADDRESS": "10.0.0.1"})); err != nil || c.BindAddress != "127.0.0.1" {
		t.Fatalf("flag beats env: %+v %v", c, err)
	}
	if _, err := Parse(nil, env(map[string]string{"JL_BIND_ADDRESS": "localhost"})); err == nil || !strings.Contains(err.Error(), "JL_BIND_ADDRESS") {
		t.Errorf("env: %v", err)
	}
	if _, err := Parse([]string{"--bind", "1.2.3"}, env(nil)); err == nil || !strings.Contains(err.Error(), "--bind") {
		t.Errorf("flag: %v", err)
	}
}

func TestTrustedProxies(t *testing.T) {
	if c, err := Parse(nil, env(nil)); err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("default: %v %v", c.TrustedProxies, err)
	}
	c, err := Parse(nil, env(map[string]string{"JL_TRUSTED_PROXIES": "10.0.0.0/8, fd00::/8"}))
	if err != nil || len(c.TrustedProxies) != 2 {
		t.Fatalf("%v %v", c.TrustedProxies, err)
	}
	for _, all := range []string{"0.0.0.0/0", "::/0"} {
		_, err = Parse(nil, env(map[string]string{"JL_TRUSTED_PROXIES": all}))
		if err == nil || !strings.Contains(err.Error(), "JL_TRUSTED_PROXIES") || !strings.Contains(err.Error(), all) {
			t.Fatalf("%s accepted: %v", all, err)
		}
	}
	_, err = Parse(nil, env(map[string]string{"JL_TRUSTED_PROXIES": "10.0.0.0/8, my-proxy"}))
	if err == nil || !strings.Contains(err.Error(), "JL_TRUSTED_PROXIES") || !strings.Contains(err.Error(), "my-proxy") {
		t.Fatalf("a bad entry must be named: %v", err)
	}
}

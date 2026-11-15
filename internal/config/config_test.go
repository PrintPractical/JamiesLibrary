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

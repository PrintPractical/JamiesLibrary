//go:build testhooks

package testhooks

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/clock"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestStartWithNothingSetDoesNothing(t *testing.T) {
	c, err := Start(env(nil))
	if c != nil || err != nil || MetadataURL() != "" || FailMigration(1) != nil {
		t.Fatalf("%v %v", c, err)
	}
}

func TestControlMovesTheClockPackageAndCloseRestoresIt(t *testing.T) {
	c, err := Start(env(map[string]string{EnvControlAddr: "127.0.0.1:0", EnvNow: "2040-01-02T03:04:05Z"}))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("clock.Now() = %v, want %v", got, want)
	}
	resp, err := http.Post("http://"+c.Addr()+"/clock", "application/json", strings.NewReader(`{"advance":"24h"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := clock.Now(); !got.Equal(want.Add(24 * time.Hour)) {
		t.Fatalf("after advance clock.Now() = %v", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(clock.Now()) > time.Minute {
		t.Error("the system clock was not put back")
	}
}

func TestEveryNonLoopbackAddressIsRefusedAndNothingStays(t *testing.T) {
	for _, a := range []string{"0.0.0.0:0", ":0", "10.0.0.1:0", "[::]:0", "example.com:80", "127.0.0.1", "localhost.evil.example:1"} {
		c, err := Start(env(map[string]string{EnvControlAddr: a, EnvMetadataURL: "http://x", EnvFailMigration: "1"}))
		if err == nil || c != nil || !strings.Contains(err.Error(), EnvControlAddr) {
			t.Errorf("%s: %v, %v", a, c, err)
		}
		if MetadataURL() != "" || FailMigration(1) != nil {
			t.Errorf("%s: a setting stayed after the refusal", a)
		}
	}
}

func TestLoopbackAddressesAreAccepted(t *testing.T) {
	for _, a := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		c, err := Start(env(map[string]string{EnvControlAddr: a}))
		if err != nil {
			t.Logf("%s: %v (skipped if the host has no such loopback)", a, err)
			continue
		}
		c.Close()
	}
}

func TestFailMigrationNamesOneVersion(t *testing.T) {
	c, err := Start(env(map[string]string{EnvFailMigration: "3", EnvMetadataURL: " http://127.0.0.1:9 "}))
	if err != nil || c != nil {
		t.Fatal(c, err)
	}
	if FailMigration(3) == nil || FailMigration(2) != nil || FailMigration(4) != nil {
		t.Error("FailMigration should fail version 3 only")
	}
	if MetadataURL() != "http://127.0.0.1:9" {
		t.Errorf("MetadataURL = %q", MetadataURL())
	}
}

func TestBadValuesAreRefused(t *testing.T) {
	for _, m := range []map[string]string{{EnvFailMigration: "0"}, {EnvFailMigration: "x"}, {EnvControlAddr: "127.0.0.1:0", EnvNow: "noon"}} {
		if c, err := Start(env(m)); err == nil {
			c.Close()
			t.Errorf("%v accepted", m)
		}
	}
}

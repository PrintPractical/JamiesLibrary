//go:build !testhooks

package testhooks

import "testing"

func TestReleaseBuildIgnoresEveryTestVariable(t *testing.T) {
	env := map[string]string{
		EnvControlAddr:   "0.0.0.0:0",
		EnvMetadataURL:   "http://127.0.0.1:1",
		EnvFailMigration: "1",
		EnvNow:           "2001-01-01T00:00:00Z",
	}
	c, err := Start(func(k string) string { return env[k] })
	if err != nil || c != nil {
		t.Fatalf("Start = %v, %v; want nothing at all", c, err)
	}
	if Enabled || MetadataURL() != "" || FailMigration(1) != nil || c.Addr() != "" || c.Close() != nil {
		t.Error("a hook is live in a build without the testhooks tag")
	}
}

// Package docscheck holds tests of the written documentation.
package docscheck

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// settings are every JL_* variable an owner can set; each deployment guide lists all of them.
var settings = []string{
	"JL_DATA_DIR", "JL_HTTP_PORT", "JL_HTTPS_PORT", "JL_BIND_ADDRESS", "JL_LOG_LEVEL",
	"JL_ADDRESSES", "JL_HOSTNAMES", "JL_TLS_CERT", "JL_TLS_KEY", "JL_HTTP_ONLY", "JL_TRUSTED_PROXIES",
}

func TestDEPLOY2(t *testing.T) {
	t.Run("Docs", func(t *testing.T) {
		for _, file := range []string{"install-lan.md", "install-public.md"} {
			b, err := os.ReadFile("../../docs/" + file)
			if err != nil {
				t.Fatal(err)
			}
			text := string(b)
			for _, topic := range []string{"Installation", "Configuration", "Network access", "Security", "Maintenance"} {
				if !regexp.MustCompile(`(?m)^## ` + topic + `$`).MatchString(text) {
					t.Errorf("%s has no section %q", file, topic)
				}
			}
			maint := text[strings.Index(text, "\n## Maintenance"):]
			for _, sub := range []string{"Backup", "Upgrade"} {
				if !regexp.MustCompile(`(?m)^### ` + sub + `$`).MatchString(maint) {
					t.Errorf("%s: Maintenance has no %q part", file, sub)
				}
			}
			for _, v := range settings {
				if !strings.Contains(text, "`"+v+"`") {
					t.Errorf("%s does not describe %s", file, v)
				}
			}
		}
	})
}

package docscheck

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// apacheSHA256 is the SHA-256 of the canonical, unmodified Apache License 2.0 text
// (https://www.apache.org/licenses/LICENSE-2.0.txt).
const apacheSHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

func TestDEPLOYT2Licence(t *testing.T) {
	b, err := os.ReadFile("../../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != apacheSHA256 {
		t.Errorf("LICENSE is not the canonical Apache-2.0 text: sha256 %s", got)
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "Apache License 2.0") {
		t.Error("README does not name the licence")
	}
}

// DEPLOY-4: the owner finds the recovery steps in the README and in each guide, and they
// name the command the program really has.
func TestDEPLOY4RecoveryDocumented(t *testing.T) {
	for _, file := range []string{"README.md", "docs/install-lan.md", "docs/install-public.md"} {
		b, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if !strings.Contains(text, "admin reset-password") {
			t.Errorf("%s does not document admin reset-password", file)
		}
		if !strings.Contains(text, "temporary password") && file != "README.md" {
			t.Errorf("%s does not say what the command prints", file)
		}
	}
}

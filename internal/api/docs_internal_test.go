package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every route the deadline exempts is in RUNNING.md's proxy timeouts, so a proxy set up as
// documented never cuts off a request the server deliberately keeps longer.
func TestProxyGuideNamesEveryLongRoute(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "RUNNING.md"))
	if err != nil {
		t.Fatal(err)
	}
	for pattern := range longRoutes {
		if !strings.Contains(string(raw), "`"+pattern+"`") {
			t.Errorf("docs/RUNNING.md's proxy timeouts do not name %s", pattern)
		}
	}
}

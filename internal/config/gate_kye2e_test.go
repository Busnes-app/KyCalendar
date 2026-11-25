//go:build kye2e

package config_test

import (
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
)

// The end-to-end test build starts a complete multi-tenant configuration, and only under a
// .test issuer. Run alone: go test -tags kye2e -run TestE2EBuild ./internal/config/
func TestE2EBuildOpensMultiUnderATestIssuerOnly(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	env := multiEnv(t)
	env["KY_KYSIGNON_ISSUER"] = "https://id.e2e.test"
	for k, v := range env {
		t.Setenv(k, v)
	}
	if cfg, err := config.LoadFromEnv(); err != nil || !cfg.Tenancy.Multi {
		t.Fatalf("test build refused a complete multi-tenant configuration: %v", err)
	}
	t.Setenv("KY_KYSIGNON_ISSUER", "https://id.example")
	if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_KYSIGNON_ISSUER") {
		t.Fatalf("test build took a real issuer: %v", err)
	}
	t.Setenv("KYCALENDAR_TENANCY", "single")
	for _, k := range []string{"KYCALENDAR_STAFF_ORG_ID", "KY_CELLS"} {
		t.Setenv(k, "")
	}
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("test build took a real issuer in single-tenant mode")
	}
}

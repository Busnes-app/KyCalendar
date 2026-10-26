package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
)

func TestSigningSecretFloorsPreserveEncryptionKey(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.Database.DataDir, "encryption.key")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"KY_SESSION_SECRET", "KY_KYSIGNON_HMAC_SECRET"} {
		for _, size := range []int{15, 16, 32} {
			t.Setenv(name, strings.Repeat("x", size))
			_, err := config.LoadFromEnv()
			if (err != nil) != (size < 16) {
				t.Errorf("%s length %d: %v", name, size, err)
			}
			if err != nil && !strings.Contains(err.Error(), name) {
				t.Errorf("error does not identify its configuration field: %v", err)
			}
		}
		t.Setenv(name, "")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("signing-key validation changed the retained encryption key")
	}
}

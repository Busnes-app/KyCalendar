package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// The CLI drill on an installer-managed install seals the installer credentials, as the server
// does, and its exit status follows the result.
func TestBackupDrillCLIOnManagedInstall(t *testing.T) {
	if os.Getenv("KY_TEST_BACKUP_DRILL_CHILD") == "1" {
		os.Args = []string{"kycalendar", "backup-drill"}
		main()
		os.Exit(0)
	}
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	t.Setenv("KY_DB_DRIVER", "sqlite")
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_APP_URL", "https://calendar.example")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	client, directory := filepath.Join(dir, "client.secret"), filepath.Join(dir, "directory.secret")
	for _, p := range []string{client, directory} {
		if err := os.WriteFile(p, []byte("disposable-private-secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kycalendar", SetupID: "271333e2-c221-4601-b249-563b3d828530", SSO: &applysetup.SSO{IssuerURL: "https://identity.example", ClientID: "kc", ClientSecretFile: client, DirectorySecretFile: directory, RedirectURI: cfg.Server.AppURL + "/api/sso/kysignon/callback"}, Admins: []applysetup.Identity{{Issuer: "https://identity.example", Subject: "owner", Username: "owner-admin"}}}
	if _, err := st.ApplyInstaller(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	drill := func(env []string) (string, int) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBackupDrillCLIOnManagedInstall$")
		cmd.Env = append(env, "KY_TEST_BACKUP_DRILL_CHILD=1")
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), exit.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}

	out, code := drill(os.Environ())
	if code != 0 || !strings.Contains(out, "PASSED") || !strings.Contains(out, "Managed credential members required") {
		t.Fatalf("managed drill: exit %d\n%s", code, out)
	}

	// KY_PORT is in the recipe's expected environment, so dropping it fails the drill.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "KY_PORT=") {
			env = append(env, kv)
		}
	}
	out, code = drill(env)
	if code == 0 || !strings.Contains(out, "FAILED") {
		t.Fatalf("failed drill: exit %d\n%s", code, out)
	}
}

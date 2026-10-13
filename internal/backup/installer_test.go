package backup_test

import (
	"context"
	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallerCredentialsSealAndDrillRequiresThem(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	ctx := context.Background()
	cfg, st := sqliteInstance(t)
	cfg.Server.AppURL = "https://calendar.example"
	dir := t.TempDir()
	client, directory := filepath.Join(dir, "client"), filepath.Join(dir, "directory")
	for _, p := range []string{client, directory} {
		if err := os.WriteFile(p, []byte("disposable-private-secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kycalendar", SetupID: "271333e2-c221-4601-b249-563b3d828530", SSO: &applysetup.SSO{IssuerURL: "https://identity.example", ClientID: "kc", ClientSecretFile: client, DirectorySecretFile: directory, RedirectURI: cfg.Server.AppURL + "/api/sso/kysignon/callback"}, Admins: []applysetup.Identity{{Issuer: "https://identity.example", Subject: "owner", Username: "owner-admin"}}}
	if _, err := st.ApplyInstaller(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	cfg.InstallerSSO = b.SSO
	payload, err := backup.Collect(ctx, cfg, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"installer/client.secret", "installer/directory.secret", "installer/credential-paths.json"} {
		if findFile(payload.Files, member) == nil {
			t.Fatal("missing", member)
		}
	}
	if err := os.MkdirAll(backup.DrillRoot(cfg), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := recoveryclient.Drill(ctx, backup.DrillRoot(cfg), payload, func(dir string, opened capsule.Manifest) []recoveryclient.Check { return backup.Checks(dir, opened) })
	if err != nil || !result.Passed {
		t.Fatal(result, err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Collect(ctx, cfg, "fixture"); err == nil {
		t.Fatal("backup succeeded without recovery credential")
	}
}

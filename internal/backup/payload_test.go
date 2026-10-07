package backup_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// payloadConfig is a real SQLite store in a temp data dir: the collectors snapshot the live
// database, so there has to be one.
func payloadConfig(t *testing.T) (*config.Config, []byte) {
	t.Helper()
	cfg, _ := sqliteInstance(t)
	return cfg, cfg.Security.EncryptionKey
}

// The encryption key must ride in the capsule: users.totp_secret_enc is AES-GCM under it, so
// a restore without it hands the operator a database whose MFA secrets are gone for good.
func TestCollectCarriesTheEncryptionKey(t *testing.T) {
	cfg, key := payloadConfig(t)
	payload, err := backup.Collect(context.Background(), cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range payload.Files {
		if f.Path != "data/encryption.key" {
			continue
		}
		found = true
		if want := hex.EncodeToString(key) + "\n"; string(f.Data) != want {
			t.Errorf("content: got %q, want the lowercase hex keyfile reads", f.Data)
		}
		if f.Mode != 0600 {
			t.Errorf("mode: got %o, want 600", f.Mode)
		}
	}
	if !found {
		t.Fatal("payload has no data/encryption.key")
	}
	req, _ := payload.VerificationRecipe["required_files"].([]string)
	if !slices.Contains(req, "data/encryption.key") {
		t.Errorf("required_files: got %v, want data/encryption.key among them", req)
	}
}

// A restore must come back paired, not half-paired: recovery.pub is public and the capsule is
// sealed to that very key, so it rides along whenever the instance has one.
func TestCollectCarriesTheRecoveryPublicKeyWhenPaired(t *testing.T) {
	cfg, _ := payloadConfig(t)
	ctx := context.Background()

	unpaired, err := backup.Collect(ctx, cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if f := findFile(unpaired.Files, "data/recovery.pub"); f != nil {
		t.Error("unpaired instance shipped a recovery.pub")
	}
	if req, _ := unpaired.VerificationRecipe["required_files"].([]string); slices.Contains(req, "data/recovery.pub") {
		t.Errorf("unpaired required_files: got %v", req)
	}

	pubPath := recoveryclient.RecoveryKeyPath(cfg.Database.DataDir)
	if err := os.MkdirAll(filepath.Dir(pubPath), 0700); err != nil {
		t.Fatal(err)
	}
	pub := []byte("a-recovery-public-key")
	if err := os.WriteFile(pubPath, pub, 0600); err != nil {
		t.Fatal(err)
	}
	paired, err := backup.Collect(ctx, cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	f := findFile(paired.Files, "data/recovery.pub")
	if f == nil {
		t.Fatal("paired instance has no data/recovery.pub in the payload")
	}
	if string(f.Data) != string(pub) {
		t.Error("data/recovery.pub is not the pinned public key byte for byte")
	}
	if f.Mode != 0600 {
		t.Errorf("mode: got %o, want 600", f.Mode)
	}
	if req, _ := paired.VerificationRecipe["required_files"].([]string); !slices.Contains(req, "data/recovery.pub") {
		t.Errorf("required_files: got %v, want data/recovery.pub among them", req)
	}
}

func findFile(files []recoveryclient.File, path string) *recoveryclient.File {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}

func TestCollectRefusesAShortKey(t *testing.T) {
	cfg, _ := payloadConfig(t)
	cfg.Security.EncryptionKey = cfg.Security.EncryptionKey[:16]
	if _, err := backup.Collect(context.Background(), cfg, "1.0.0"); err == nil {
		t.Fatal("a 16-byte encryption key was accepted")
	}
}

// The store runs in WAL mode, so a plain read of the main file misses every commit still in
// the -wal. The snapshot must carry a row committed moments ago and never checkpointed.
func TestSnapshotSeesUncheckpointedCommit(t *testing.T) {
	cfg, st := sqliteInstance(t)
	ctx := context.Background()
	if err := st.Settings().SetSetting(ctx, "canary", "still-in-the-wal"); err != nil {
		t.Fatal(err)
	}
	payload, err := backup.Collect(ctx, cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	f := findFile(payload.Files, backup.DatabaseMember)
	if f == nil {
		t.Fatal("no " + backup.DatabaseMember + " in the payload")
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restored, f.Data, 0600); err != nil {
		t.Fatal(err)
	}
	copyStore, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: restored})
	if err != nil {
		t.Fatalf("snapshot does not open: %v", err)
	}
	defer copyStore.Close()
	if got, err := copyStore.Settings().GetSetting(ctx, "canary"); err != nil || got != "still-in-the-wal" {
		t.Fatalf("snapshot lacks the uncheckpointed row: %q, %v", got, err)
	}
	if check, _ := payload.VerificationRecipe["check_sqlite_integrity"].(bool); !check {
		t.Error("recipe does not ask the drill to check the database")
	}
}

// A driver the collectors cannot snapshot must refuse, not seal a keys-and-config capsule
// that a receipt would then call a backup.
func TestCollectRefusesADriverItCannotSnapshot(t *testing.T) {
	cfg, _ := payloadConfig(t)
	cfg.Database.Driver = "postgres"
	if _, err := backup.Collect(context.Background(), cfg, "1.0.0"); !errors.Is(err, backup.ErrNoDatabaseSnapshot) {
		t.Fatalf("got %v, want ErrNoDatabaseSnapshot", err)
	}
}

// A KY_DB_DSN elsewhere would seal a file the restored server never opens: it would start empty.
func TestCollectRefusesADatabaseOutsideTheDefaultPath(t *testing.T) {
	cfg, _ := payloadConfig(t)
	cfg.Database.DSN = config.SQLiteDSN(filepath.Join(t.TempDir(), "kycalendar.db"))
	_, err := backup.Collect(context.Background(), cfg, "1.0.0")
	if !errors.Is(err, backup.ErrNoDatabaseSnapshot) || !strings.Contains(err.Error(), "KY_DB_DSN") {
		t.Fatalf("got %v, want ErrNoDatabaseSnapshot naming KY_DB_DSN", err)
	}
}

func TestCapsuleCarriesTheDatabaseTheServerOpens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	t.Setenv("KY_DB_DRIVER", "sqlite")
	t.Setenv("KY_DB_DSN", "")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := "data/" + filepath.Base(strings.SplitN(cfg.Database.DSN, "?", 2)[0])
	if want != backup.DatabaseMember {
		t.Fatalf("DSN file %q != member %q", want, backup.DatabaseMember)
	}
	st, err := store.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := backup.Collect(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.ServiceName != "kycalendar" {
		t.Fatalf("service = %q, want kycalendar", p.ServiceName)
	}
	if findFile(p.Files, backup.DatabaseMember) == nil {
		t.Fatalf("payload lacks %s", backup.DatabaseMember)
	}
}

func TestCollectRecordsCalendarCounts(t *testing.T) {
	cfg, _ := payloadConfig(t)
	seedCalendars(t, cfg.Database.DSN, map[string]map[string]string{
		"cal-a": {"a1.ics": fmt.Sprintf(validEvent, "a1"), "a2.ics": fmt.Sprintf(validEvent, "a2")},
	})
	p, err := backup.Collect(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.VerificationRecipe["calendar_count"] != int64(1) || p.VerificationRecipe["object_count"] != int64(2) {
		t.Fatalf("recipe %v", p.VerificationRecipe)
	}
}

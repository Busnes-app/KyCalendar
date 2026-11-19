package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func requirePostgres(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("KY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	return dsn
}

// serverConfig is a complete configuration on db with its own data directory.
func serverConfig(t *testing.T, db config.DatabaseConfig) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.Server = config.ServerConfig{Host: "127.0.0.1", Port: 8080, AppURL: "http://localhost:8080", AppName: "KyCalendar",
		Environment: "development", ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	cfg.Database = db
	cfg.Database.DataDir = t.TempDir()
	if db.Driver == "sqlite" {
		cfg.Database.DSN = config.SQLiteDSN(filepath.Join(cfg.Database.DataDir, "kycalendar.db"))
	}
	cfg.Security = config.SecurityConfig{SessionSecret: strings.Repeat("s", 32), EncryptionKey: bytes.Repeat([]byte{1}, 32), SessionTTL: time.Hour}
	cfg.Captcha.Provider = "none"
	cfg.Calendar = config.CalendarConfig{MaxObjectsPerUser: 1000, MaxCalendarsPerUser: 10, MaxBytesPerUser: 4 << 20, MaxBytesTotal: config.DefaultMaxBytesTotal, Scheduling: config.SchedulingOff}
	cfg.Backup.Keep = 3
	return cfg
}

func openStore(t *testing.T, cfg *config.Config) store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// The first start records the shared settings; a replica differing in one is refused naming
// the setting, never its value; per-host settings may differ; a different key is said alone.
func TestReplicaConfigRecordsAndRefuses(t *testing.T) {
	requirePostgres(t)
	db := testdb.Config(t)
	a := serverConfig(t, db)
	st := openStore(t, a)
	ctx := context.Background()
	if err := checkReplicaConfig(ctx, a, st.Settings()); err != nil {
		t.Fatal(err)
	}
	b := serverConfig(t, db)
	b.Server.Port, b.Server.Host, b.Database.MaxOpenConns = 9090, "0.0.0.0", 7
	if err := checkReplicaConfig(ctx, b, st.Settings()); err != nil {
		t.Fatalf("per-host settings refused: %v", err)
	}
	b.Server.AppURL = "https://cal.secret-host.example"
	err := checkReplicaConfig(ctx, b, st.Settings())
	if !errors.Is(err, errReplicaConfig) || !strings.Contains(err.Error(), "KY_APP_URL") || strings.Contains(err.Error(), "secret-host") {
		t.Fatalf("a differing KY_APP_URL: %v", err)
	}
	c := serverConfig(t, db)
	c.Security.EncryptionKey = bytes.Repeat([]byte{2}, 32)
	c.Server.AppURL = "https://elsewhere.example"
	if err := checkReplicaConfig(ctx, c, st.Settings()); err == nil || !strings.Contains(err.Error(), keyDiffers) || strings.Contains(err.Error(), "KY_APP_URL") {
		t.Fatalf("a different key must be reported alone: %v", err)
	}
	raw, _ := st.Settings().GetSetting(ctx, config.ReplicaConfigSetting)
	if strings.Contains(raw, "localhost") || strings.Contains(raw, "ssss") {
		t.Fatalf("the record holds a value: %s", raw)
	}
}

// Two first starts with different settings race: exactly one records, the other loses the
// insert, reads the winner back and refuses.
func TestFirstStartsRaceThroughTheRecord(t *testing.T) {
	requirePostgres(t)
	db := testdb.Config(t)
	cfgs := []*config.Config{serverConfig(t, db), serverConfig(t, db)}
	cfgs[1].Server.AppURL = "https://other.example"
	sts := []store.Store{openStore(t, cfgs[0]), openStore(t, cfgs[1])}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = checkReplicaConfig(context.Background(), cfgs[i], sts[i].Settings()) }()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("want one record and one refusal: %v", errs)
	}
	winner := 0
	if errs[1] == nil {
		winner = 1
	}
	if !strings.Contains(errs[1-winner].Error(), "KY_APP_URL") {
		t.Fatalf("the loser: %v", errs[1-winner])
	}
	raw, _ := sts[0].Settings().GetSetting(context.Background(), config.ReplicaConfigSetting)
	var rec map[string]string
	_ = json.Unmarshal([]byte(raw), &rec)
	if rec["KY_APP_URL"] != config.ReplicaFingerprint(cfgs[winner])["KY_APP_URL"] {
		t.Fatal("the record is not the winner's")
	}
}

// SQLite records no replica configuration, and start sweeps leftover snapshot scratch.
func TestSQLiteStartRecordsNothingAndSweepsScratch(t *testing.T) {
	t.Setenv("KY_ADMIN_PASSWORD", "AdminPassword-123!")
	cfg := serverConfig(t, config.DatabaseConfig{Driver: "sqlite"})
	st := openStore(t, cfg)
	leftover := filepath.Join(cfg.Database.DataDir, "snapshot-killed")
	if err := os.Mkdir(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := prepareStart(context.Background(), cfg, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Settings().GetSetting(context.Background(), config.ReplicaConfigSetting); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SQLite recorded a replica configuration: %v", err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatal("start left a killed process's plaintext snapshot")
	}
}

// accept-replica-config rewrites the record after a deliberate change, with the audit row in
// the same write, names only, never a fingerprint; it refuses SQLite and a different key; -h
// changes nothing.
func TestAcceptReplicaConfig(t *testing.T) {
	var help bytes.Buffer
	if err := runAcceptReplicaConfig([]string{"-h"}, &help); !errors.Is(err, flag.ErrHelp) || !strings.Contains(help.String(), "Usage: kycalendar accept-replica-config") {
		t.Fatalf("-h: %v %q", err, help.String())
	}
	sqliteCfg := serverConfig(t, config.DatabaseConfig{Driver: "sqlite"})
	if err := acceptReplicaConfig(context.Background(), sqliteCfg, openStore(t, sqliteCfg), &help); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("SQLite: %v", err)
	}

	requirePostgres(t)
	db := testdb.Config(t)
	a := serverConfig(t, db)
	st := openStore(t, a)
	ctx := context.Background()
	if err := checkReplicaConfig(ctx, a, st.Settings()); err != nil {
		t.Fatal(err)
	}
	b := serverConfig(t, db)
	b.Server.AppURL = "https://moved.example"
	var out bytes.Buffer
	if err := acceptReplicaConfig(ctx, b, st, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "KY_APP_URL") || regexp.MustCompile(`[0-9a-f]{32}`).MatchString(out.String()) || strings.Contains(out.String(), "moved.example") {
		t.Fatalf("output: %q", out.String())
	}
	if err := checkReplicaConfig(ctx, b, st.Settings()); err != nil {
		t.Fatalf("the accepted settings are refused: %v", err)
	}
	recs, _, _ := st.Audit().ListAuditRecords(ctx, 0, 10)
	var audited bool
	for _, r := range recs {
		if r.Action == "admin.replica_config_accepted" && r.UserID == "system" && r.Details == "fields=KY_APP_URL" {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("no admin.replica_config_accepted row naming the field: %+v", recs)
	}
	c := serverConfig(t, db)
	c.Server.AppURL = b.Server.AppURL
	c.Security.EncryptionKey = bytes.Repeat([]byte{3}, 32)
	if err := acceptReplicaConfig(ctx, c, st, &out); err == nil || !strings.Contains(err.Error(), keyDiffers) {
		t.Fatalf("a different key was accepted: %v", err)
	}
}

// apply-setup refuses on PostgreSQL before contacting the setup socket or writing anything.
func TestApplySetupRefusesPostgres(t *testing.T) {
	t.Setenv("KY_DB_DRIVER", "postgres")
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	bundle := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(bundle, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runInstaller([]string{"--file", bundle}); err == nil || !strings.Contains(err.Error(), "single-server SQLite") {
		t.Fatalf("apply-setup on PostgreSQL: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a refused apply-setup wrote to the data directory: %v", entries)
	}
}

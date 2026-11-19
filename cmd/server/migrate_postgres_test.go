package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// sqliteInstall is a stopped SQLite install: <dir>/data/kycalendar.db with a user and a
// calendar, its -wal checkpointed away.
func sqliteInstall(t *testing.T) (dataDir, db string) {
	t.Helper()
	dataDir = filepath.Join(t.TempDir(), "data")
	db = filepath.Join(dataDir, "kycalendar.db")
	st, err := store.Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", DSN: config.SQLiteDSN(db)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_a", Username: "alice", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_1", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Cal"}, 0); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if _, err := os.Stat(db + "-wal"); err == nil {
		t.Fatal("closing the store left a -wal")
	}
	return dataDir, db
}

func digestOf(t *testing.T, path string) [32]byte {
	t.Helper()
	d, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func targetUsers(t *testing.T, dsn string) int {
	t.Helper()
	db := pgDB(t, dsn)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return -1 // no schema: nothing was written
	}
	return n
}

func migrate(t *testing.T, args ...string) error {
	t.Helper()
	var out, errOut bytes.Buffer
	return migratePostgres(context.Background(), args, &out, &errOut)
}

// Relative, dot-dot, absolute and default sources all migrate; the source bytes stay
// identical and no -wal or -shm appears beside it; --check leaves the target empty; the
// private copy is removed (spec AC26).
func TestMigratePostgresPaths(t *testing.T) {
	requirePostgres(t)
	dataDir, db := sqliteInstall(t)
	t.Setenv("KY_DATA_DIR", dataDir)
	before := digestOf(t, db)
	t.Chdir(filepath.Dir(dataDir))

	check := testdb.Config(t).DSN
	t.Setenv("KY_MIGRATE_POSTGRES_DSN", check)
	if err := migrate(t, "--check", "--source", "data/kycalendar.db"); err != nil {
		t.Fatal(err)
	}
	if n := targetUsers(t, check); n != -1 {
		t.Fatalf("--check left %d users and a schema", n)
	}
	for name, source := range map[string][]string{
		"relative": {"--source", "data/kycalendar.db"},
		"dot-dot":  {"--source", "../" + filepath.Base(filepath.Dir(dataDir)) + "/x/../data/kycalendar.db"},
		"absolute": {"--source", db},
		"default":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			target := testdb.Config(t).DSN
			t.Setenv("KY_MIGRATE_POSTGRES_DSN", target)
			if err := migrate(t, source...); err != nil {
				t.Fatal(err)
			}
			if n := targetUsers(t, target); n != 1 {
				t.Fatalf("target holds %d users", n)
			}
		})
	}
	if digestOf(t, db) != before {
		t.Fatal("the source changed")
	}
	for _, side := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(db + side); err == nil {
			t.Fatalf("%s appeared beside the source", side)
		}
	}
	entries, _ := os.ReadDir(dataDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "snapshot-") {
			t.Fatalf("private copy %s left behind", e.Name())
		}
	}
}

// Every refusal leaves the target as it was and the source byte for byte (spec AC26).
func TestMigratePostgresRefusals(t *testing.T) {
	requirePostgres(t)
	ctx := context.Background()
	refused := func(t *testing.T, db, target, want string, args ...string) {
		t.Helper()
		before, users := digestOf(t, db), targetUsers(t, target)
		err := migrate(t, args...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want a refusal naming %q, got %v", want, err)
		}
		if digestOf(t, db) != before {
			t.Fatal("a refused migration changed the source")
		}
		if n := targetUsers(t, target); n != users {
			t.Fatalf("a refused migration changed the target: %d users, was %d", n, users)
		}
	}
	setup := func(t *testing.T) (string, string, string) {
		dataDir, db := sqliteInstall(t)
		t.Setenv("KY_DATA_DIR", dataDir)
		target := testdb.Config(t).DSN
		t.Setenv("KY_MIGRATE_POSTGRES_DSN", target)
		return dataDir, db, target
	}

	t.Run("DSN in argv", func(t *testing.T) {
		_, db, target := setup(t)
		refused(t, db, target, "command line", "--source", db, "postgres://u:p@db/x")
	})
	t.Run("populated target", func(t *testing.T) {
		_, db, target := setup(t)
		if err := migrate(t, "--source", db); err != nil {
			t.Fatal(err)
		}
		refused(t, db, target, "already holds data", "--source", db)
	})
	t.Run("newer schema", func(t *testing.T) {
		_, db, target := setup(t)
		raw, _ := sql.Open("sqlite", db)
		_, err := raw.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future', ?)`, time.Now())
		raw.Close()
		if err != nil {
			t.Fatal(err)
		}
		refused(t, db, target, "newer", "--source", db)
	})
	t.Run("non-empty wal", func(t *testing.T) {
		_, db, target := setup(t)
		raw, err := sql.Open("sqlite", config.SQLiteDSN(db))
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		if _, err := raw.Exec(`INSERT INTO server_settings (key, value, updated_at) VALUES ('x', 'y', ?)`, time.Now()); err != nil {
			t.Fatal(err)
		}
		refused(t, db, target, "-wal", "--source", db)
	})
	t.Run("running server", func(t *testing.T) {
		_, db, target := setup(t)
		held, release := make(chan struct{}), make(chan struct{})
		go func() {
			_, _ = store.WithSQLiteInstance(ctx, db, func(context.Context) error { close(held); <-release; return nil })
		}()
		<-held
		defer close(release)
		refused(t, db, target, "running", "--source", db)
	})
	t.Run("broken foreign key", func(t *testing.T) {
		_, db, target := setup(t)
		raw, _ := sql.Open("sqlite", db+"?_pragma=foreign_keys(OFF)")
		_, err := raw.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('s', 'usr_gone', ?, ?)`, time.Now(), time.Now())
		raw.Close()
		if err != nil {
			t.Fatal(err)
		}
		refused(t, db, target, "foreign_key_check", "--source", db)
	})
	t.Run("a SQLite URI", func(t *testing.T) {
		_, db, target := setup(t)
		refused(t, db, target, "file path", "--source", "file:"+db+"?mode=ro")
	})
	t.Run("no target", func(t *testing.T) {
		_, db, target := setup(t)
		t.Setenv("KY_MIGRATE_POSTGRES_DSN", "")
		refused(t, db, target, "KY_MIGRATE_POSTGRES_DSN", "--source", db)
	})
}

// The instance lock is what a second server and migrate-postgres meet; the CLI backup,
// export and restore-reset share the file beside a live server.
func TestSQLiteInstanceLockSparesTheCLI(t *testing.T) {
	t.Setenv("KY_ADMIN_PASSWORD", "AdminPassword-123!")
	cfg := serverConfig(t, config.DatabaseConfig{Driver: "sqlite"})
	cfg.Backup.Dir = t.TempDir()
	st := openStore(t, cfg)
	path, _, _ := strings.Cut(cfg.Database.DSN, "?")
	ctx := context.Background()
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = store.WithSQLiteInstance(ctx, path, func(context.Context) error { close(held); <-release; return nil })
	}()
	<-held
	if won, err := store.WithSQLiteInstance(ctx, path, func(context.Context) error { return nil }); won || err != nil {
		t.Fatalf("a second server took the instance lock: %v %v", won, err)
	}
	priv, _ := recoverykey.Generate()
	if err := backup.PinRecoveryKey(ctx, cfg, st, recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3}); err != nil {
		t.Fatal(err)
	}
	if res, err := runBackup(ctx, cfg, st); err != nil || res.LocalPath == "" {
		t.Fatalf("CLI deposit beside a live server: %+v %v", res, err)
	}
	if err := resetRestored(ctx, cfg.Database.DataDir); err != nil {
		t.Fatalf("restore-reset beside a live server: %v", err)
	}
	close(release)
	<-done
}

// A PostgreSQL capsule restores through the SQLite path and migrates into an empty database
// that starts: credentials and the queue are gone, the epoch is new, scheduled backups are off,
// the replica record and rate windows are absent, live refusals stay and the audit sequence
// continues past the restored rows (spec AC28, AC25).
func TestPostgresCapsuleRestoresAndStarts(t *testing.T) {
	requirePostgres(t)
	t.Setenv("KY_ADMIN_PASSWORD", "AdminPassword-123!")
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "postgres")
	ctx := context.Background()
	cfg := serverConfig(t, testdb.Config(t))
	st := openStore(t, cfg)
	if err := prepareStart(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	u, _ := st.Users().GetUserByUsername(ctx, "admin")
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "sess", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, store.Credentials{PasswordHash: u.PasswordHash}); err != nil {
		t.Fatal(err)
	}
	c := st.Coordination()
	_, _, _, _ = c.TakeAttempt(ctx, "acct:x", 5, time.Hour)
	_, _ = c.RecordReplay(ctx, "live-evt", time.Hour)
	_, _ = c.SpendChallenge(ctx, "live-salt", time.Now().Add(time.Hour).UnixMilli())
	_, _ = c.SpendChallenge(ctx, "gone-salt", time.Now().Add(-time.Hour).UnixMilli())
	epoch, _ := st.Calendars().SyncEpoch(ctx)

	priv, _ := recoverykey.Generate()
	shares, _ := recoverykey.Split(priv, 2, 3)
	key := recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3}
	payload, err := backup.Collect(ctx, cfg, st, "test")
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := recoveryclient.Seal(payload, key)
	if err != nil {
		t.Fatal(err)
	}
	capsulePath := filepath.Join(t.TempDir(), "pg.kycap")
	if err := os.WriteFile(capsulePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restored := t.TempDir()
	if err := restore(capsulePath, restored, backup.ServiceName, []string{shares[0].String(), shares[2].String()}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	target := testdb.Config(t)
	t.Setenv("KY_DATA_DIR", filepath.Join(restored, "data"))
	t.Setenv("KY_MIGRATE_POSTGRES_DSN", target.DSN)
	if err := migrate(t, "--source", filepath.Join(restored, backup.DatabaseMember)); err != nil {
		t.Fatal(err)
	}
	cfg2 := serverConfig(t, target)
	st2 := openStore(t, cfg2)
	if _, err := st2.Settings().GetSetting(ctx, "replica_config_fingerprint"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the replica record travelled in the capsule: %v", err)
	}
	if err := prepareStart(ctx, cfg2, st2); err != nil {
		t.Fatalf("the restored database does not start: %v", err)
	}
	if _, err := st2.Sessions().GetSession(ctx, "sess"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a session survived: %v", err)
	}
	if got, _ := st2.Calendars().SyncEpoch(ctx); got == epoch {
		t.Fatal("the sync epoch did not change")
	}
	if v, _ := st2.Settings().GetSetting(ctx, "backup_interval_sec"); v != "0" {
		t.Fatalf("backup_interval_sec %q, want 0", v)
	}
	db := pgDB(t, target.DSN)
	count := func(q string) (n int) {
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM app_passwords`:                                      0,
		`SELECT COUNT(*) FROM scheduling_outbox`:                                  0,
		`SELECT COUNT(*) FROM security_windows`:                                   0,
		`SELECT COUNT(*) FROM spent_challenges WHERE salt = 'live-salt'`:           1,
		`SELECT COUNT(*) FROM spent_challenges WHERE salt = 'gone-salt'`:           0,
		`SELECT COUNT(*) FROM kypost_replay WHERE event_id = 'live-evt'`:           1,
		`SELECT COUNT(*) FROM audit_records WHERE action = 'system.restore_reset'`: 1,
	} {
		if n := count(q); n != want {
			t.Errorf("%s = %d, want %d", q, n, want)
		}
	}
	// The restored instance's first start records its own.
	if n := count(`SELECT COUNT(*) FROM server_settings WHERE key = 'replica_config_fingerprint'`); n != 1 {
		t.Errorf("replica record rows after the first start: %d", n)
	}
	maxID := count(`SELECT MAX(id) FROM audit_records`)
	var id int
	if err := db.QueryRow(`INSERT INTO audit_records (action, created_at) VALUES ('after', now()) RETURNING id`).Scan(&id); err != nil || id <= maxID {
		t.Fatalf("audit id %d after restore, not above %d (%v)", id, maxID, err)
	}
}

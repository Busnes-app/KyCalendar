// Package testdb hands tests an isolated database configuration.
//
// Default backend is a throwaway SQLite file. Set KY_TEST_POSTGRES_DSN to run
// the same tests against a real Postgres instance; each caller gets its own
// schema, dropped on cleanup.
package testdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

// Config returns a database config pointing at an isolated, empty database.
func Config(t *testing.T) config.DatabaseConfig {
	t.Helper()

	dsn := os.Getenv("KY_TEST_POSTGRES_DSN")
	if dsn == "" {
		return config.DatabaseConfig{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "test.db"),
		}
	}
	return postgresConfig(t, dsn)
}

func postgresConfig(t *testing.T, dsn string) config.DatabaseConfig {
	t.Helper()

	// The "pgx" driver is registered by the store package, which every caller imports.
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("testdb: open postgres: %v", err)
	}

	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("testdb: create schema %s: %v", schema, err)
	}

	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}

	return config.DatabaseConfig{
		Driver: "postgres",
		DSN:    dsn + sep + "search_path=" + schema,
	}
}

// migratedSQLite is the bytes of a SQLite database every migration ran on, made once per test
// process. Under -race the pure-Go SQLite takes about half a second for the migrations, most of
// a short test's time; copying the result takes none.
var migratedSQLite = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "kycal-testdb-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	db, err := sql.Open("sqlite", config.SQLiteDSN(path))
	if err != nil {
		return nil, err
	}
	err = migrations.Run(context.Background(), db, "sqlite")
	if err == nil {
		// One self-contained file: no WAL left beside it.
		_, err = db.Exec(`PRAGMA journal_mode=DELETE`)
	}
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// SeedSQLite writes, at the database path of the SQLite dsn, a database every migration already
// ran on, with a sync epoch of its own, so a store opened on it skips them. A dsn that is not a
// file path (PostgreSQL), or a file that already exists, is left alone. It is for tests that
// open the store at once; Config still hands out an empty database for migration tests.
func SeedSQLite(t testing.TB, dsn string) {
	t.Helper()
	path, _, _ := strings.Cut(dsn, "?")
	if strings.HasPrefix(path, "postgres") || path == "" {
		return
	}
	tmpl, err := migratedSQLite()
	if err != nil {
		t.Fatalf("testdb: migrated SQLite template: %v", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return
	}
	if err != nil {
		t.Fatalf("testdb: seed %s: %v", path, err)
	}
	_, err = f.Write(tmpl)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("testdb: seed %s: %v", path, err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE calendar_meta SET value = lower(hex(randomblob(8))) WHERE key = 'sync_epoch'`); err != nil {
		t.Fatalf("testdb: seed %s: %v", path, err)
	}
}

package testdb_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Busnes-app/kycalendar/internal/store/migrations"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testdb.DropTemplate()
	os.Exit(code)
}

// Each Migrated database is fully migrated, its own and with its own sync epoch.
func TestMigratedPostgresClonesAreMigratedAndSeparate(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	ctx := context.Background()
	open := func() *sql.DB {
		db, err := sql.Open("pgx", testdb.Migrated(t).DSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	a, b := open(), open()
	epoch := func(db *sql.DB) string {
		var version int
		if err := db.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != migrations.LatestVersion() {
			t.Fatalf("schema version %d, want %d", version, migrations.LatestVersion())
		}
		var v string
		if err := db.QueryRowContext(ctx, `SELECT value FROM calendar_meta WHERE key = 'sync_epoch'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if ea, eb := epoch(a), epoch(b); ea == eb {
		t.Fatalf("both clones share sync epoch %s", ea)
	}
	if _, err := a.ExecContext(ctx, `INSERT INTO calendar_meta (key, value) VALUES ('only_in_a', 'x')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRowContext(ctx, `SELECT count(*) FROM calendar_meta WHERE key = 'only_in_a'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a write to one clone shows in the other: %d %v", n, err)
	}
}

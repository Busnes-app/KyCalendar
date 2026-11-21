package migrations_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/store/migrations"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// rawOpen opens cfg's database without the store, so no migration or org setup runs.
func rawOpen(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	name, src := "sqlite", dsn+"?_pragma=foreign_keys(ON)"
	if driver == "postgres" {
		name, src = "pgx", dsn
	}
	db, err := sql.Open(name, src)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func bind(driver, q string) string {
	if driver != "postgres" {
		return q
	}
	for i := 1; strings.Contains(q, "?"); i++ {
		q = strings.Replace(q, "?", "$"+strconv.Itoa(i), 1)
	}
	return q
}

// An install at version 36 upgrades with every row in the default org, its object bytes in
// org_usage, and the mode recorded as single: single-tenant behaviour is unchanged.
func TestMigration37MovesAnExistingInstallIntoTheDefaultOrg(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	db := rawOpen(t, cfg.Driver, cfg.DSN)
	if err := migrations.RunUntil(ctx, db, cfg.Driver, 36); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, created_at, updated_at) VALUES ('u1', 'alice', ?, ?)`, []any{now, now}},
		{`INSERT INTO groups (id, display_name, created_at, updated_at) VALUES ('g1', 'Team', ?, ?)`, []any{now, now}},
		{`INSERT INTO group_members (group_id, user_id) VALUES ('g1', 'u1')`, nil},
		{`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at) VALUES ('c1', 'user', 'u1', 'default', 'Mine', ?)`, []any{now}},
		{`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at) VALUES ('c2', 'group', 'c2', 'team', 'Team', ?)`, []any{now}},
		{`INSERT INTO calendar_grants (calendar_id, group_id, role) VALUES ('c2', 'g1', 'editor')`, nil},
		{`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at, component) VALUES ('c1', 'a.ics', 'a', 'e', ?, 0, ?, 'VEVENT')`, []any{data, now}},
		{`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at, component) VALUES ('c2', 'b.ics', 'b', 'e', ?, 0, ?, 'VEVENT')`, []any{data, now}},
		{`INSERT INTO audit_records (user_id, action, created_at) VALUES ('u1', 'x', ?)`, []any{now}},
		{`INSERT INTO scheduling_outbox (id, dedupe_key, sender_id, method, recipient, body, next_attempt_at, created_at) VALUES ('o1', 'd', 'u1', 'REQUEST', 'x@example.test', ?, ?, ?)`, []any{data, now, now}},
	} {
		if _, err := db.ExecContext(ctx, bind(cfg.Driver, q.sql), q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	_ = db.Close()

	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db = rawOpen(t, cfg.Driver, cfg.DSN)
	defer db.Close()
	for _, table := range migrations.OrgOwnedTables {
		var other int
		if err := db.QueryRowContext(ctx, bind(cfg.Driver, "SELECT COUNT(*) FROM "+table+" WHERE org_id IS NULL OR org_id <> ?"), migrations.DefaultOrgID).Scan(&other); err != nil || other != 0 {
			t.Errorf("%s: %d rows outside the default org (%v)", table, other, err)
		}
	}
	var status, mode string
	var bytes int64
	if err := db.QueryRowContext(ctx, bind(cfg.Driver, "SELECT status FROM orgs WHERE id = ?"), migrations.DefaultOrgID).Scan(&status); err != nil || status != "active" {
		t.Errorf("default org: %q %v", status, err)
	}
	if err := db.QueryRowContext(ctx, bind(cfg.Driver, "SELECT bytes FROM org_usage WHERE org_id = ?"), migrations.DefaultOrgID).Scan(&bytes); err != nil || bytes != int64(2*len(data)) {
		t.Errorf("org_usage: %d %v, want %d", bytes, err, 2*len(data))
	}
	if mode, err = st.Settings().GetSetting(ctx, store.TenancySetting); err != nil || mode != "single" {
		t.Errorf("tenancy_mode: %q %v", mode, err)
	}

	// The store writes as before, without naming an org, and usage follows a cascaded delete.
	if err := st.Users().CreateUser(ctx, &store.User{ID: "u2", Username: "bob", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM calendars WHERE id = 'c1'"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, bind(cfg.Driver, "SELECT bytes FROM org_usage WHERE org_id = ?"), migrations.DefaultOrgID).Scan(&bytes); err != nil || bytes != int64(len(data)) {
		t.Errorf("org_usage after deleting a calendar: %d %v, want %d", bytes, err, len(data))
	}
}

// An install with org-owned rows but no users (a start that died before its first admin) still
// upgrades: the default org exists for PostgreSQL's foreign key to find.
func TestMigration37WithRowsButNoUsers(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	db := rawOpen(t, cfg.Driver, cfg.DSN)
	if err := migrations.RunUntil(ctx, db, cfg.Driver, 36); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, bind(cfg.Driver, `INSERT INTO groups (id, display_name, created_at, updated_at) VALUES ('g1', 'Team', ?, ?)`), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, bind(cfg.Driver, `INSERT INTO audit_records (user_id, action, created_at) VALUES ('', 'x', ?)`), now); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	_ = st.Close()
}

// A fresh database gets the default org, its usage row and the mode from the store, not the
// migration.
func TestFreshDatabaseGetsTheDefaultOrg(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	db := rawOpen(t, cfg.Driver, cfg.DSN)
	if err := migrations.RunUntil(ctx, db, cfg.Driver, migrations.LatestVersion()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orgs").Scan(&n); err != nil || n != 0 {
		t.Fatalf("the migration seeded %d orgs on a fresh database (%v)", n, err)
	}
	_ = db.Close()
	for range 2 { // idempotent
		st, err := store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
	}
	db = rawOpen(t, cfg.Driver, cfg.DSN)
	defer db.Close()
	var orgs, usage int
	if err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM orgs), (SELECT COUNT(*) FROM org_usage)").Scan(&orgs, &usage); err != nil || orgs != 1 || usage != 1 {
		t.Fatalf("orgs %d, org_usage %d (%v)", orgs, usage, err)
	}
}

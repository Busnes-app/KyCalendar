package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 10 gives groups an owner. Before it SCIM was the only group writer, so every
// existing row becomes SCIM's; rows written afterwards default to local.
func TestMigration10BackfillsGroupSource(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_old", DisplayName: "Old"}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Pretend the database predates migration 10, then reopen to run it.
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE groups DROP COLUMN source", "DELETE FROM schema_migrations WHERE version = 10"} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = raw.Close()
	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	old, err := st.Groups().GetGroupByID(ctx, "grp_old")
	if err != nil || old.Source != store.GroupSourceSCIM {
		t.Fatalf("existing group after migration: %+v %v, want source scim", old, err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_new", DisplayName: "New"}); err != nil {
		t.Fatal(err)
	}
	if g, err := st.Groups().GetGroupByID(ctx, "grp_new"); err != nil || g.Source != store.GroupSourceLocal {
		t.Fatalf("new group: %+v %v, want source local", g, err)
	}
}

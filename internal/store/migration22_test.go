package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 22 cannot tell whether an SSO session from before it had an auth_time, so every SSO
// session becomes unproven (no step-up); local password sessions stay as they were.
func TestMigration22MarksSSOSessionsUnproven(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, u := range []*store.User{
		{ID: "usr_s", Username: "s", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s"},
		{ID: "usr_l", Username: "l", PasswordHash: "h", Role: "user", Status: "active", SSOProvider: "local"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_" + u.ID, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour), SSOSID: map[bool]string{true: "sid"}[u.SSOProvider != "local"]}, u.Credentials()); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE sessions DROP COLUMN unproven", "DELETE FROM schema_migrations WHERE version = 22"} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for id, want := range map[string]bool{"usr_s": true, "usr_l": false} {
		if s, err := st.Sessions().GetSession(ctx, "tok_"+id); err != nil || s.Unproven != want {
			t.Errorf("%s's session after migration 22: %+v (%v), want unproven %v", id, s, err, want)
		}
	}
}

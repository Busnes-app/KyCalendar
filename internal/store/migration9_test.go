package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 9 ends the sessions of SSO admins made by the retired global-role webhook; they
// sign in again and the roles claim decides. Local admins and everyday users keep theirs.
func TestMigration9RevokesSSOAdminSessions(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	users := []*store.User{
		{ID: "usr_ky", Username: "ky", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"},
		{ID: "usr_sc", Username: "sc", Role: "admin", Status: "active", SSOProvider: "scim", SSOSubject: "s2"},
		{ID: "usr_local", Username: "local", Role: "admin", Status: "active", SSOProvider: "local"},
		{ID: "usr_user", Username: "user", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s3"},
	}
	for _, u := range users {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_" + u.ID, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	// Pretend the database predates migration 9, then reopen to run it.
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = 9"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	for _, u := range users {
		_, err := st.Sessions().GetSession(ctx, "tok_"+u.ID)
		revoked := errors.Is(err, store.ErrNotFound)
		if want := u.ID == "usr_ky" || u.ID == "usr_sc"; revoked != want {
			t.Errorf("%s: session revoked %v, want %v (%v)", u.ID, revoked, want, err)
		}
	}
}

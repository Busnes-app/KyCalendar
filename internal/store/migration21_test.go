package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 21 clears generic OIDC emails, stored before email_verified was checked; local and
// KyIdentity emails stay.
func TestMigration21ClearsOIDCEmails(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_o", Username: "o", Email: "boss@example.com", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "o"},
		{ID: "usr_k", Username: "k", Email: "k@example.com", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "k"},
		{ID: "usr_l", Username: "l", Email: "l@example.com", Role: "user", Status: "active", SSOProvider: "local"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = 21"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for id, want := range map[string]string{"usr_o": "", "usr_k": "k@example.com", "usr_l": "l@example.com"} {
		if u, err := st.Users().GetUserByID(ctx, id); err != nil || u.Email != want {
			t.Errorf("%s after migration 21: %q (%v), want %q", id, u.Email, err, want)
		}
	}
}

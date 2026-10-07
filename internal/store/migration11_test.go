package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 11 stamps every existing SSO row of the stored binding's kind with that binding, so
// rows keep the issuer they were provisioned under; other kinds, local rows and an instance with
// no binding stay unstamped (the first binding stamps those).
func TestMigration11StampsTheBoundKind(t *testing.T) {
	for bound, want := range map[string]map[string]string{
		"kyidentity https://a.example": {"usr_k": "kyidentity https://a.example", "usr_s": "kyidentity https://a.example", "usr_o": "", "usr_l": ""},
		"oidc https://a.example":       {"usr_k": "", "usr_s": "", "usr_o": "oidc https://a.example", "usr_l": ""},
		"":                             {"usr_k": "", "usr_s": "", "usr_o": "", "usr_l": ""},
	} {
		ctx := context.Background()
		cfg := testdb.Config(t)
		st, err := store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range []*store.User{
			{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"},
			{ID: "usr_s", Username: "s", Role: "user", Status: "inactive", SSOProvider: "scim", SSOSubject: "s2"},
			{ID: "usr_o", Username: "o", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "s3"},
			{ID: "usr_l", Username: "l", Role: "admin", Status: "active", SSOProvider: "local"},
		} {
			if err := st.Users().CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
		if bound != "" {
			if err := st.Settings().SetSetting(ctx, "signin_bound", bound); err != nil {
				t.Fatal(err)
			}
		}
		_ = st.Close()

		// Pretend the database predates migration 11, then reopen to run it.
		driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
		raw, err := sql.Open(driver, cfg.DSN)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"ALTER TABLE users DROP COLUMN sso_issuer", "DELETE FROM schema_migrations WHERE version = 11"} {
			if _, err := raw.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		_ = raw.Close()
		st, err = store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for id, issuer := range want {
			u, err := st.Users().GetUserByID(ctx, id)
			if err != nil || u.SSOIssuer != issuer {
				t.Errorf("bound %q: %s stamped %+v %v, want %q", bound, id, u, err, issuer)
			}
		}
		_ = st.Close()
	}
}

// UpdateUser (SCIM replace and patch) never writes sso_issuer.
func TestUpdateUserKeepsTheIssuer(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u := &store.User{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: "kyidentity https://a.example"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	u.SSOIssuer = "kyidentity https://b.example"
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Users().GetUserByID(ctx, u.ID); got.SSOIssuer != "kyidentity https://a.example" {
		t.Fatalf("issuer after UpdateUser: %q", got.SSOIssuer)
	}
}

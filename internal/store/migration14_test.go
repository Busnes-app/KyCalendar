package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 14 gives app passwords a scope. Every existing one is a CalDAV device's.
func TestMigration14ExistingAppPasswordsAreDAV(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_a", Username: "a", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "ap_old", UserID: "usr_a", Label: "phone", Hash: "h", Scope: store.ScopeDAV}, 0); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Pretend the database predates migration 14, then reopen to run it.
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DROP TABLE mcp_events", "ALTER TABLE app_passwords DROP COLUMN scope", "DELETE FROM schema_migrations WHERE version = 14"} {
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
	if p, err := st.AppPasswords().Get(ctx, "ap_old"); err != nil || p.Scope != store.ScopeDAV {
		t.Fatalf("existing app password after migration: %+v %v, want scope dav", p, err)
	}
}

func TestAppPasswordScopesAndMCPEvents(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_a", Username: "a", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	ap := st.AppPasswords()
	for _, scope := range []string{"", "admin", "mcp:write"} {
		if err := ap.Create(ctx, store.Seed, &store.AppPassword{ID: "bad", UserID: "usr_a", Label: "x", Hash: "h", Scope: scope}, 0); !errors.Is(err, store.ErrInvalidScope) {
			t.Errorf("create with scope %q: %v, want ErrInvalidScope", scope, err)
		}
	}
	for id, scope := range map[string]string{"ap_dav": store.ScopeDAV, "ap_agent": store.ScopeMCPWriteOwn} {
		if err := ap.Create(ctx, store.Seed, &store.AppPassword{ID: id, UserID: "usr_a", Label: "x", Hash: "h", Scope: scope}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := ap.SetScope(ctx, "usr_a", "ap_dav", store.ScopeMCPWriteAny); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetScope on a dav token: %v, want ErrNotFound", err)
	}
	if err := ap.SetScope(ctx, "usr_a", "ap_agent", store.ScopeDAV); !errors.Is(err, store.ErrInvalidScope) {
		t.Errorf("SetScope to dav: %v, want ErrInvalidScope", err)
	}
	if err := ap.SetScope(ctx, "usr_other", "ap_agent", store.ScopeMCPRead); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetScope by another user: %v, want ErrNotFound", err)
	}

	// Events a token created go with the token and with their calendar.
	for _, id := range []string{"cal_keep", "cal_doomed"} {
		if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: id, OwnerKind: "user", OwnerID: "usr_a", Slug: id, Name: id}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][2]string{{"cal_keep", "u1"}, {"cal_doomed", "u2"}} {
		if err := ap.RecordMCPEvent(ctx, "ap_agent", e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Calendars().DeleteCalendar(ctx, "cal_doomed"); err != nil {
		t.Fatal(err)
	}
	if got, err := ap.MCPEvents(ctx, "ap_agent"); err != nil || len(got) != 1 || !got["cal_keep/u1"] {
		t.Fatalf("after deleting a calendar: %v %v, want only cal_keep/u1", got, err)
	}
	if err := ap.Delete(ctx, "usr_a", "ap_agent"); err != nil {
		t.Fatal(err)
	}
	if got, err := ap.MCPEvents(ctx, "ap_agent"); err != nil || len(got) != 0 {
		t.Fatalf("after revoking the token: %v %v, want none", got, err)
	}
}

// Revoking a user's grants (role, status, password and sign-in changes all share it) takes
// their agent tokens with them.
func TestRevocationTakesAgentTokens(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_a", Username: "a", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "sub-a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "ap_agent", UserID: "usr_a", Label: "x", Hash: "h", Scope: store.ScopeMCPWriteAny}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().RevokeSSOUser(ctx, "usr_a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppPasswords().Get(ctx, "ap_agent"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("agent token after revocation: %v, want revoked", err)
	}
}

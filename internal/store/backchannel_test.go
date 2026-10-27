package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// A fenced sid's session is refused and leaves no row; EndSSOSession ends only its own sid, and
// EndSSOSubject only its subject's sessions verified before its cutoff, which it then fences.
func TestSSOSessionFence(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := &store.User{ID: "usr_f", Username: "f", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "f", SSOIssuer: "idp"}
	other := &store.User{ID: "usr_g", Username: "g", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "f", SSOIssuer: "other-idp"}
	seedUsers(t, st, u, other)
	now := time.Now().UTC()
	create := func(token, sid string, at time.Time) error {
		return st.Sessions().CreateSession(ctx, &store.Session{TokenHash: token, UserID: u.ID, CreatedAt: at, ExpiresAt: now.Add(time.Hour), SSOSID: sid}, u.Credentials())
	}
	live := func(token string) bool { _, err := st.Sessions().GetSession(ctx, token); return err == nil }

	for _, tok := range []string{"a", "b"} {
		if err := create(tok, "sid-"+tok, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Sessions().EndSSOSession(ctx, "sid-a", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if live("a") || !live("b") {
		t.Fatal("EndSSOSession ended the wrong sessions")
	}
	if err := create("a2", "sid-a", now); !errors.Is(err, store.ErrSSOSessionEnded) || live("a2") {
		t.Fatalf("fenced create: %v, row left %v", err, live("a2"))
	}
	// An expired fence no longer refuses.
	if err := st.Sessions().EndSSOSession(ctx, "sid-old", now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := create("old", "sid-old", now); err != nil {
		t.Fatalf("expired fence refused a login: %v", err)
	}

	if err := create("late", "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "g", UserID: other.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, other.Credentials()); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().EndSSOSubject(ctx, "idp", "f", now.Add(time.Second), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if live("b") || live("old") || !live("late") || !live("g") {
		t.Fatal("EndSSOSubject ended the wrong sessions")
	}
	// The cutoff fences a login issued before it, not one issued after, and never moves back.
	if err := create("early", "sid-e", now); !errors.Is(err, store.ErrSSOSessionEnded) || live("early") {
		t.Fatalf("login before the cutoff: %v, row left %v", err, live("early"))
	}
	if err := st.Sessions().EndSSOSubject(ctx, "idp", "f", now.Add(-time.Hour), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := create("early2", "sid-e2", now); !errors.Is(err, store.ErrSSOSessionEnded) {
		t.Fatalf("an older logout moved the cutoff back: %v", err)
	}
	if err := create("after", "sid-f", now.Add(2*time.Second)); err != nil {
		t.Fatalf("login after the cutoff: %v", err)
	}
}

// Migration 19 adds the subject fence to a database already at 18.
func TestMigration19AddsTheSubjectFence(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DROP TABLE sso_subject_logouts", "DELETE FROM schema_migrations WHERE version = 19"} {
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
	now := time.Now().UTC()
	if err := st.Sessions().EndSSOSubject(ctx, "idp", "s", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("subject fence after migration 19: %v", err)
	}
}

// Sessions from before migration 18 carry no sid, so no back-channel logout could end them:
// migration 20 drops those SSO sessions and SSO accounts' MFA challenges, keeps local ones and
// SSO sessions issued with a sid, and runs once only.
func TestMigration20DropsUnreachableSSOSessions(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	local := &store.User{ID: "usr_local", Username: "local", Role: "user", Status: "active", SSOProvider: "local"}
	sso := &store.User{ID: "usr_sso", Username: "sso", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s", SSOIssuer: "idp"}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	seedUsers(t, st, local, sso)
	_ = st.Close()

	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if cfg.Driver == "postgres" {
			for i := 1; strings.Contains(q, "?"); i++ {
				q = strings.Replace(q, "?", fmt.Sprintf("$%d", i), 1)
			}
		}
		if _, err := raw.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	reopen := func() store.Store {
		t.Helper()
		st, err := store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	count := func(table, key, value string) int {
		t.Helper()
		var n int
		q := "SELECT COUNT(1) FROM " + table + " WHERE " + key + " = ?"
		if cfg.Driver == "postgres" {
			q = strings.Replace(q, "?", "$1", 1)
		}
		if err := raw.QueryRowContext(ctx, q, value).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	now := time.Now().UTC()

	// The schema before migration 18, with a session and an MFA challenge of each account.
	for _, q := range []string{"DROP INDEX idx_sessions_sso_sid", "ALTER TABLE sessions DROP COLUMN sso_sid", "DROP TABLE sso_logouts", "DROP TABLE sso_subject_logouts", "DELETE FROM schema_migrations WHERE version >= 18"} {
		exec(q)
	}
	for _, u := range []string{local.ID, sso.ID} {
		exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`, "legacy_"+u, u, now, now.Add(time.Hour))
		exec(`INSERT INTO mfa_challenges (token_hash, user_id, expires_at) VALUES (?, ?, ?)`, "mfa_"+u, u, now.Add(5*time.Minute))
	}
	_ = reopen()
	for _, c := range []struct {
		table, key string
		want       int
	}{{"sessions", "legacy_usr_local", 1}, {"sessions", "legacy_usr_sso", 0}, {"mfa_challenges", "mfa_usr_local", 1}, {"mfa_challenges", "mfa_usr_sso", 0}} {
		if got := count(c.table, "token_hash", c.key); got != c.want {
			t.Errorf("after the upgrade, %s %s: %d rows, want %d", c.table, c.key, got, c.want)
		}
	}

	// At 19, a session issued with a sid survives migration 20; one without does not.
	st = reopen()
	for token, sid := range map[string]string{"with_sid": "idp sid-1", "no_sid": ""} {
		if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: token, UserID: sso.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour), SSOSID: sid}, sso.Credentials()); err != nil {
			t.Fatal(err)
		}
	}
	exec("DELETE FROM schema_migrations WHERE version = 20")
	_ = reopen()
	if count("sessions", "token_hash", "with_sid") != 1 || count("sessions", "token_hash", "no_sid") != 0 {
		t.Error("migration 20 at 19: want the sid session kept and the sid-less one dropped")
	}

	// A later start does not run it again.
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "later", UserID: sso.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, sso.Credentials()); err != nil {
		t.Fatal(err)
	}
	_ = reopen()
	if count("sessions", "token_hash", "later") != 1 {
		t.Error("a second start signed out a session issued after migration 20")
	}
}

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

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

func TestAppPasswords(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, u := range []string{"usr_a", "usr_b"} {
		if err := st.Users().CreateUser(ctx, &store.User{ID: u, Username: u, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	ap := st.AppPasswords()
	for _, p := range []*store.AppPassword{
		{ID: "p1", UserID: "usr_a", Label: "phone", Hash: "h1"},
		{ID: "p2", UserID: "usr_a", Label: "laptop", Hash: "h2"},
		{ID: "p3", UserID: "usr_b", Label: "phone", Hash: "h3"},
	} {
		if err := ap.Create(ctx, store.Seed, p, 0); err != nil {
			t.Fatal(err)
		}
	}

	if err := ap.Delete(ctx, "usr_b", "p1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting another user's password: %v", err)
	}
	if err := ap.TouchLastUsed(ctx, "p1", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := ap.Get(ctx, "p1")
	if err != nil || got.LastUsedAt == nil || got.UserID != "usr_a" {
		t.Fatalf("get %+v %v", got, err)
	}
	if err := ap.DeleteByUser(ctx, "usr_a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := ap.ListByUser(ctx, "usr_a"); len(list) != 0 {
		t.Fatalf("left %d", len(list))
	}
	if list, _ := ap.ListByUser(ctx, "usr_b"); len(list) != 1 {
		t.Fatalf("usr_b lost passwords: %d", len(list))
	}
}

// A session grantor issues only while its session is live and the user's, the account active and
// its password unchanged, all checked under the user-row lock; the cap is counted in the same
// transaction. Every refusal stores nothing.
func TestAppPasswordIssuanceRechecksTheSession(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ann := &store.User{ID: "usr_ann", Username: "ann", PasswordHash: "h_ann", Role: "user", Status: "active", SSOProvider: "local"}
	bob := &store.User{ID: "usr_bob", Username: "bob", PasswordHash: "h_bob", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, ann, bob)
	seedSession(t, st, ann) // tok_usr_ann plus ap_usr_ann
	seedSession(t, st, bob)
	ap := st.AppPasswords()
	try := func(name string, by store.Grantor, max int, want error) {
		t.Helper()
		before, _ := ap.ListByUser(ctx, ann.ID)
		err := ap.Create(ctx, by, &store.AppPassword{ID: "new_" + name, UserID: ann.ID, Label: "phone", Hash: "h"}, max)
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
		after, _ := ap.ListByUser(ctx, ann.ID)
		if grew := len(after) - len(before); (want == nil) != (grew == 1) {
			t.Errorf("%s: %d stored", name, grew)
		}
	}
	try("zero grantor", store.Grantor{}, 0, store.ErrSessionExpired)
	try("unknown session", store.SessionGrantor("tok_nobody", "h_ann"), 0, store.ErrSessionExpired)
	try("another user's session", store.SessionGrantor("tok_usr_bob", "h_ann"), 0, store.ErrSessionExpired)
	try("stale password", store.SessionGrantor("tok_usr_ann", "h_old"), 0, store.ErrSessionExpired)
	try("live session", store.SessionGrantor("tok_usr_ann", "h_ann"), 0, nil)
	try("at the cap", store.SessionGrantor("tok_usr_ann", "h_ann"), 2, store.ErrQuotaExceeded)

	// Disabled: the row lock refuses even a session the purge missed.
	off := *ann
	off.Status = "inactive"
	if err := st.Users().UpdateUser(ctx, &off); err != nil {
		t.Fatal(err)
	}
	try("disabled user", store.SessionGrantor("tok_usr_ann", "h_ann"), 0, store.ErrSessionExpired)
	off.Status = "active"
	if err := st.Users().UpdateUser(ctx, &off); err != nil {
		t.Fatal(err)
	}
	// A purge (here a disable and re-enable through SetStatus) ends the session it was issued under.
	for _, status := range []string{"inactive", "active"} {
		if err := st.Users().SetStatus(ctx, store.System, ann.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	try("session purged", store.SessionGrantor("tok_usr_ann", "h_ann"), 0, store.ErrSessionExpired)
}

// On Postgres, issuance waits for a purge holding the user row (row first, as ReattachSSOUser
// does), then sees the session gone and stores nothing.
func TestAppPasswordIssuanceWaitsForThePurge(t *testing.T) {
	cfg := testdb.Config(t)
	if cfg.Driver != "postgres" {
		t.Skip("postgres only: SQLite's single connection serialises the two")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	carol := &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: "kyidentity https://a.example"}
	seedUsers(t, st, carol)
	seedSession(t, st, carol)

	raw, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	purge, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer purge.Rollback()
	for _, q := range []string{
		`UPDATE users SET sso_issuer = 'kyidentity https://b.example', status = 'active' WHERE id = 'usr_carol'`,
		`DELETE FROM sessions WHERE user_id = 'usr_carol'`,
		`DELETE FROM app_passwords WHERE user_id = 'usr_carol'`,
	} {
		if _, err := purge.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- st.AppPasswords().Create(ctx, store.SessionGrantor("tok_usr_carol", ""), &store.AppPassword{ID: "late", UserID: carol.ID, Label: "phone", Hash: "h"}, 20)
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		var waiting int
		if err := raw.QueryRowContext(ctx, `SELECT COUNT(1) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND wait_event IN ('transactionid', 'tuple')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("issuance never waited on the user row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := purge.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, store.ErrSessionExpired) {
		t.Fatalf("issuance after the purge committed: %v, want ErrSessionExpired", err)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, carol.ID); len(list) != 0 {
		t.Fatalf("a credential outlived the purge: %+v", list[0])
	}
}

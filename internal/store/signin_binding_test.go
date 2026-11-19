package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// Every sign-in write rechecks the binding inside its own transaction, after taking the
// local-admins lock BindSignIn holds: a save that commits while the write waits for the lock
// always wins. A recheck read before the lock would pass here and write under the old binding
// (spec AC8; code review round 2, F2 and F3).
func TestSignInWritesRecheckTheBindingUnderTheLock(t *testing.T) {
	onPostgres(t)
	_, st, other := openTwice(t)
	ctx := context.Background()
	const old, next = "kyidentity https://old.example", "kyidentity https://next.example"
	if err := st.Settings().SetSetting(ctx, "signin_bound", old); err != nil {
		t.Fatal(err)
	}
	carol := &store.User{ID: "usr_carol", Username: "carol", Email: "carol@example.com", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "sub-carol", SSOIssuer: old}
	if err := st.Users().CreateUser(ctx, carol); err != nil {
		t.Fatal(err)
	}
	commit, err := store.HoldLockForTest(other, "local-admins")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	writes := map[string]func() error{
		"CreateSSOUser": func() error {
			return st.Users().CreateSSOUser(ctx, &store.User{ID: "usr_new", Username: "new", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-new", SSOIssuer: old})
		},
		"CreateSession": func() error {
			return st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_carol", UserID: carol.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, store.Credentials{Binding: old})
		},
		"SetSSORole":   func() error { return st.Users().SetSSORole(ctx, carol.ID, "admin", old) },
		"SetOIDCEmail": func() error { return st.Users().SetOIDCEmail(ctx, carol.ID, "eve@example.com", old) },
	}
	results := make(map[string]chan error, len(writes))
	for name, write := range writes {
		results[name] = make(chan error, 1)
		go func() { results[name] <- write() }()
	}
	// Every write waits for the lock before the save commits.
	for deadline := time.Now().Add(10 * time.Second); ; {
		n, err := store.RowCountForTest(st, "pg_locks", "locktype = 'advisory' AND NOT granted AND database = (SELECT oid FROM pg_database WHERE datname = current_database())")
		if err != nil {
			t.Fatal(err)
		}
		if n >= len(writes) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d writes wait for the local-admins lock", n, len(writes))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := commit(`UPDATE server_settings SET value = ? WHERE key = 'signin_bound'`, next); err != nil {
		t.Fatal(err)
	}
	for name, res := range results {
		if err := <-res; !errors.Is(err, store.ErrBindingChanged) {
			t.Errorf("%s after a save that committed while it waited: %v, want ErrBindingChanged", name, err)
		}
	}
	got, err := st.Users().GetUserByID(ctx, carol.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "user" || got.Email != "carol@example.com" {
		t.Fatalf("carol is %s %s after the save", got.Role, got.Email)
	}
}

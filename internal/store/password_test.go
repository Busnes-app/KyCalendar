package store_test

import (
	"context"
	"errors"
	"github.com/Busnes-app/kycalendar/internal/store"
	"sync"
	"testing"
	"time"
)

func TestPasswordReplacementIsAtomicAndSingleUse(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := &store.User{ID: "reset", Username: "reset", PasswordHash: "old", Role: "admin", Status: "active", SSOProvider: "local", MustChangePassword: true}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{TokenHash: "session", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateSession(ctx, sess, "old"); err != nil {
		t.Fatal(err)
	}
	challenge := &store.MFAChallenge{TokenHash: "challenge", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateMFAChallenge(ctx, challenge, "old"); err != nil {
		t.Fatal(err)
	}
	addAppPassword(t, st, u.ID)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, hash := range []string{"new-one", "new-two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- st.Users().CompletePasswordChange(ctx, u.ID, "old", hash, "127.0.0.1")
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("%d successful changes", success)
	}
	if _, err := st.Sessions().GetSession(ctx, sess.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session survived", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, challenge.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("challenge survived", err)
	}
	checkAppPasswordsRevoked(t, st, u.ID)
	if err := st.Sessions().CreateSession(ctx, sess, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale session creation", err)
	}
	if err := st.Sessions().CreateMFAChallenge(ctx, challenge, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale MFA creation", err)
	}
	audits, n, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || n != 1 || audits[0].Action != "auth.password_changed" {
		t.Fatal("audit", n, err)
	}
}

func TestAdminPasswordResetRevokesGrants(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := &store.User{ID: "reset", Username: "reset", PasswordHash: "old", Role: "admin", Status: "active", SSOProvider: "local", MustChangePassword: false}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{TokenHash: "session", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateSession(ctx, sess, "old"); err != nil {
		t.Fatal(err)
	}
	challenge := &store.MFAChallenge{TokenHash: "challenge", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateMFAChallenge(ctx, challenge, "old"); err != nil {
		t.Fatal(err)
	}
	if err := st.Devices().CreatePairing(ctx, &store.DevicePairing{Secret: "pair", UserID: u.ID, Status: "pending", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	addAppPassword(t, st, u.ID)
	u.Status = "disabled"
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().ResetAdminPassword(ctx, u.ID, "reset-hash"); err != nil {
		t.Fatal(err)
	}
	updated, err := st.Users().GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.MustChangePassword || updated.PasswordHash != "reset-hash" || updated.Status != "active" || updated.Role != "admin" {
		t.Fatalf("unexpected reset state: %+v", updated)
	}
	if _, err := st.Devices().GetPairingBySecret(ctx, "pair"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("pairing survived", err)
	}

	if _, err := st.Sessions().GetSession(ctx, sess.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session survived", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, challenge.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("challenge survived", err)
	}
	checkAppPasswordsRevoked(t, st, u.ID)
	if err := st.Sessions().CreateSession(ctx, sess, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale session creation", err)
	}
	if err := st.Sessions().CreateMFAChallenge(ctx, challenge, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale MFA creation", err)
	}
	audits, n, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || n != 1 || audits[0].Action != "auth.password_changed" {
		t.Fatal("audit", n, err)
	}
}

// addAppPassword gives the user a CalDAV app password; checkAppPasswordsRevoked asserts it is gone.
func addAppPassword(t *testing.T, st store.Store, userID string) {
	t.Helper()
	if err := st.AppPasswords().Create(context.Background(), &store.AppPassword{ID: "ap_" + userID, UserID: userID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
}

func checkAppPasswordsRevoked(t *testing.T, st store.Store, userID string) {
	t.Helper()
	if list, err := st.AppPasswords().ListByUser(context.Background(), userID); err != nil || len(list) != 0 {
		t.Fatalf("app passwords survived: %d %v", len(list), err)
	}
}

func TestResetPasswordKeepsRoleAndStatus(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := &store.User{ID: "everyday", Username: "everyday", PasswordHash: "old", Role: "user", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{TokenHash: "session", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateSession(ctx, sess, "old"); err != nil {
		t.Fatal(err)
	}
	challenge := &store.MFAChallenge{TokenHash: "challenge", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateMFAChallenge(ctx, challenge, "old"); err != nil {
		t.Fatal(err)
	}
	addAppPassword(t, st, u.ID)
	u.Status = "disabled" // ResetAdminPassword would reactivate it
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	if err := st.Users().ResetPassword(ctx, store.System, u.ID, "new"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Users().GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "user" || got.Status != "disabled" || !got.MustChangePassword || got.PasswordHash != "new" {
		t.Fatalf("after reset: %+v", got)
	}
	if _, err := st.Sessions().GetSession(ctx, sess.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session survived", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, challenge.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("challenge survived", err)
	}
	checkAppPasswordsRevoked(t, st, u.ID)
	audits, n, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || n != 1 || audits[0].Action != "auth.password_changed" || audits[0].UserID != u.ID {
		t.Fatal("audit", n, err)
	}

	sso := &store.User{ID: "sso", Username: "sso", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub"}
	if err := st.Users().CreateUser(ctx, sso); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().ResetPassword(ctx, store.System, sso.ID, "new"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SSO account reset: %v", err)
	}
	if err := st.Users().ResetPassword(ctx, store.System, "nobody", "new"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user reset: %v", err)
	}
}

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func newTOTPUser(t *testing.T, st store.Store, id string) *store.User {
	t.Helper()
	u := &store.User{ID: id, Username: id, PasswordHash: "pw", Role: "user", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func newSession(token, userID string) *store.Session {
	return &store.Session{TokenHash: token, UserID: userID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
}

// A sign-in checked against one TOTP state cannot add its session once that state changed:
// the password-only path after TOTP went on, the code path after it went off or was re-enrolled.
func TestSessionCreationHoldsTheCheckedTOTPState(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := newTOTPUser(t, st, "tia")
	off := u.Credentials()
	if err := st.Users().SetPendingTOTP(ctx, u.ID, "", "sealed-a"); err != nil {
		t.Fatal(err)
	}
	// A pending secret is not a TOTP change: the password-only sign-in still lands.
	if err := st.Sessions().CreateSession(ctx, newSession("keep", u.ID), off); err != nil {
		t.Fatal("pending enrolment refused a password sign-in:", err)
	}
	if err := st.Users().EnableTOTP(ctx, u.ID, "pw", "sealed-a", "[]", "keep", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, newSession("late", u.ID), off); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("password-only session created after TOTP went on:", err)
	}
	on := store.Credentials{PasswordHash: "pw", TOTPEnabled: true, TOTPSecretEnc: "sealed-a"}
	if err := st.Sessions().CreateSession(ctx, newSession("mfa", u.ID), on); err != nil {
		t.Fatal("MFA session refused:", err)
	}
	if err := st.Users().DisableTOTP(ctx, u.ID, "pw", "keep", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, newSession("late-off", u.ID), on); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("MFA session created after TOTP went off:", err)
	}
	if err := st.Users().SetPendingTOTP(ctx, u.ID, "", "sealed-b"); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().EnableTOTP(ctx, u.ID, "pw", "sealed-b", "[]", "keep", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, newSession("old-secret", u.ID), on); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("MFA session checked against the replaced secret was created:", err)
	}
}

// Turning TOTP on or off revokes every grant a password change would, but the caller's session.
func TestTOTPTransitionsRevokeEveryOtherGrant(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := newTOTPUser(t, st, "ted")
	grant := func() {
		t.Helper()
		if err := st.Sessions().CreateSession(ctx, newSession("other", u.ID), reload(t, st, u.ID).Credentials()); err != nil {
			t.Fatal(err)
		}
		addAppPassword(t, st, u.ID)
		if err := st.Devices().CreatePairing(ctx, &store.DevicePairing{Secret: "pair", UserID: u.ID, Status: "pending", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(step string) {
		t.Helper()
		if _, err := st.Sessions().GetSession(ctx, "keep"); err != nil {
			t.Fatalf("%s ended the caller's session: %v", step, err)
		}
		if _, err := st.Sessions().GetSession(ctx, "other"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s kept another session: %v", step, err)
		}
		checkAppPasswordsRevoked(t, st, u.ID)
		if _, err := st.Devices().GetPairingBySecret(ctx, "pair"); err == nil {
			t.Fatalf("%s kept a device pairing", step)
		}
	}
	if err := st.Sessions().CreateSession(ctx, newSession("keep", u.ID), u.Credentials()); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().SetPendingTOTP(ctx, u.ID, "", "sealed"); err != nil {
		t.Fatal(err)
	}
	grant()
	if err := st.Users().EnableTOTP(ctx, u.ID, "pw", "sealed", "[]", "keep", ""); err != nil {
		t.Fatal(err)
	}
	check("enabling")
	grant()
	if err := st.Users().DisableTOTP(ctx, u.ID, "pw", "keep", ""); err != nil {
		t.Fatal(err)
	}
	check("disabling")
}

// Both operator resets are the way back from a lost authenticator, so both turn TOTP off.
func TestOperatorResetsTurnTOTPOff(t *testing.T) {
	ctx := context.Background()
	for name, reset := range map[string]func(store.Store, string) error{
		"ResetPassword":      func(st store.Store, id string) error { return st.Users().ResetPassword(ctx, store.System, id, "new") },
		"ResetAdminPassword": func(st store.Store, id string) error { return st.Users().ResetAdminPassword(ctx, id, "new") },
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			u := newTOTPUser(t, st, "olga")
			if err := st.Users().SetPendingTOTP(ctx, u.ID, "", "sealed"); err != nil {
				t.Fatal(err)
			}
			if err := st.Users().EnableTOTP(ctx, u.ID, "pw", "sealed", `["h"]`, "", ""); err != nil {
				t.Fatal(err)
			}
			if err := reset(st, u.ID); err != nil {
				t.Fatal(err)
			}
			got := reload(t, st, u.ID)
			if got.TOTPEnabled || got.TOTPSecretEnc != "" || got.RecoveryCodesHash != "[]" {
				t.Fatalf("reset left TOTP: enabled=%v secret=%q codes=%s", got.TOTPEnabled, got.TOTPSecretEnc, got.RecoveryCodesHash)
			}
		})
	}
}

func reload(t *testing.T, st store.Store, id string) *store.User {
	t.Helper()
	u, err := st.Users().GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

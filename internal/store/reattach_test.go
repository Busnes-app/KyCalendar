package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// Reattach stamps an SSO account with the current binding and activates it, revoking every grant
// so the person signs in fresh; role is untouched. Local accounts and revoked actors are refused.
func TestReattachSSOUser(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	carol := &store.User{ID: "usr_carol", Username: "carol", PasswordHash: "h", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: "kyidentity https://a.example"}
	ann := &store.User{ID: "usr_ann", Username: "ann", Role: "user", Status: "inactive", SSOProvider: "local"}
	seedUsers(t, st, root, carol, ann)
	rootActs := store.AdminActor(root.ID, seedRootSession(t, st, root))

	// Grants in every table, then a whole-row deactivation that leaves them in place.
	seedSession(t, st, carol)
	now := time.Now()
	if err := st.Sessions().CreateMFAChallenge(ctx, &store.MFAChallenge{TokenHash: "mfa_carol", UserID: carol.ID, ExpiresAt: now.Add(time.Hour)}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := st.Devices().CreatePairing(ctx, &store.DevicePairing{Secret: "pair_carol", UserID: carol.ID, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	carol.Status = "inactive"
	if err := st.Users().UpdateUser(ctx, carol); err != nil {
		t.Fatal(err)
	}

	const b = "kyidentity https://b.example"
	for name, a := range map[string]store.Actor{"zero actor": {}, "unknown session": store.AdminActor(root.ID, "tok_nobody")} {
		if _, err := st.Users().ReattachSSOUser(ctx, a, carol.ID, b); !errors.Is(err, store.ErrActorRevoked) {
			t.Errorf("%s: %v, want ErrActorRevoked", name, err)
		}
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.SSOIssuer != "kyidentity https://a.example" || u.Status != "inactive" {
		t.Fatalf("a refused reattach wrote: %+v", u)
	}
	if _, err := st.Sessions().GetSession(ctx, "tok_"+carol.ID); err != nil {
		t.Fatalf("a refused reattach revoked: %v", err)
	}
	for _, id := range []string{ann.ID, "usr_nobody"} {
		if _, err := st.Users().ReattachSSOUser(ctx, rootActs, id, b); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", id, err)
		}
	}
	if u, _ := st.Users().GetUserByID(ctx, ann.ID); u.Status != "inactive" || u.SSOIssuer != "" {
		t.Fatalf("a local account changed: %+v", u)
	}

	from, err := st.Users().ReattachSSOUser(ctx, rootActs, carol.ID, b)
	if err != nil || from != "kyidentity https://a.example" {
		t.Fatalf("reattach: from %q, %v", from, err)
	}
	u, _ := st.Users().GetUserByID(ctx, carol.ID)
	if u.SSOIssuer != b || u.Status != "active" || u.Role != "admin" || u.SSOProvider != "kysignon" || u.SSOSubject != "s1" {
		t.Fatalf("after reattach: %+v", u)
	}
	if _, err := st.Sessions().GetSession(ctx, "tok_"+carol.ID); !errors.Is(err, store.ErrNotFound) {
		t.Error("session survived", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, "mfa_carol"); !errors.Is(err, store.ErrNotFound) {
		t.Error("MFA challenge survived", err)
	}
	if _, err := st.Devices().GetPairingBySecret(ctx, "pair_carol"); !errors.Is(err, store.ErrNotFound) {
		t.Error("device pairing survived", err)
	}
	checkAppPasswordsRevoked(t, st, carol.ID)
	if _, err := st.Sessions().GetSession(ctx, "tok_"+root.ID); err != nil {
		t.Errorf("the actor's own session was revoked: %v", err)
	}
}

// A row already bound to the target, as after a concurrent reattach the IdP then disabled, is
// not this write's: nothing is activated, revoked or returned as changed.
func TestReattachSkipsARowAlreadyBound(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	const b = "kyidentity https://b.example"
	dan := &store.User{ID: "usr_dan", Username: "dan", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "d1", SSOIssuer: b}
	seedUsers(t, st, root, dan)
	rootActs := store.AdminActor(root.ID, seedRootSession(t, st, root))
	seedSession(t, st, dan)
	dan.Status = "inactive" // the IdP's deactivation, grants left in place to prove nothing is revoked
	if err := st.Users().UpdateUser(ctx, dan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Users().ReattachSSOUser(ctx, rootActs, dan.ID, b); !errors.Is(err, store.ErrAlreadyBound) {
		t.Fatalf("reattach onto its own binding: %v, want ErrAlreadyBound", err)
	}
	if u, _ := st.Users().GetUserByID(ctx, dan.ID); u.Status != "inactive" || u.SSOIssuer != b {
		t.Fatalf("the IdP's deactivation was undone: %+v", u)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, dan.ID); len(list) != 1 {
		t.Error("a skipped reattach revoked grants")
	}
}

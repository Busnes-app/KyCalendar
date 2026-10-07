package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func kyidentityAt(issuer string) sso.Settings {
	return sso.Settings{
		Provider: sso.Field{Value: sso.KindKyIdentity}, DisplayName: sso.Field{Value: "KyIdentity"},
		Issuer: sso.Field{Value: issuer}, ClientID: sso.Field{Value: "kc"},
		Secret: sso.SecretField{Value: "sec", Source: sso.SourceSaved},
	}
}

func oidcAt(issuer string) sso.Settings {
	st := kyidentityAt(issuer)
	st.Provider.Value, st.DisplayName.Value = sso.KindOIDC, "Acme"
	return st
}

func createUsers(t *testing.T, s *Server, users ...*store.User) {
	t.Helper()
	for _, u := range users {
		if err := s.store.Users().CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
}

// After an issuer change, the new provider's colliding sub reaches the disabled account of the
// old one and is refused: never a takeover of its calendars.
func TestProviderChangeRefusesTheOldSubject(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	createUsers(t, s,
		&store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-1"},
		&store.User{ID: "usr_dave", Username: "dave", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-2"},
		&store.User{ID: "usr_lo", Username: "lo", Role: "admin", Status: "active", SSOProvider: "local"},
	)
	if prev, n, err := s.bindAccounts(ctx, kyidentityAt("https://old.example")); err != nil || prev != "" || n != 0 {
		t.Fatalf("first binding: %q %d %v, want nothing disabled", prev, n, err)
	}
	if n, err := s.pendingDisable(ctx, kyidentityAt("https://new.example")); err != nil || n != 2 {
		t.Fatalf("pending disable: %d %v, want 2", n, err)
	}
	prev, n, err := s.bindAccounts(ctx, kyidentityAt("https://new.example"))
	if err != nil || prev != "kyidentity https://old.example" || n != 2 {
		t.Fatalf("issuer change: %q %d %v", prev, n, err)
	}
	for _, sub := range []string{"sub-1", "sub-2"} {
		_, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: sub, PreferredUsername: "intruder-" + sub}, "kyidentity https://new.example")
		if !errors.Is(err, errAccountInactive) {
			t.Errorf("%s through the new provider: %v, want errAccountInactive", sub, err)
		}
	}
	if u, _ := s.store.Users().GetUserByID(ctx, "usr_lo"); u.Status != "active" {
		t.Fatalf("local account after the change: %s", u.Status)
	}
	if prev, n, err := s.bindAccounts(ctx, kyidentityAt("https://new.example")); err != nil || prev != "" || n != 0 {
		t.Fatalf("rebinding the same provider: %q %d %v, want a no-op", prev, n, err)
	}
}

// The same guard for oidc: a new issuer of the same kind disables the old issuer's accounts.
func TestOIDCIssuerChangeRefusesTheOldSubject(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if _, _, err := s.bindAccounts(ctx, oidcAt("https://a.example")); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s, &store.User{ID: "usr_o", Username: "o", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "s1"})
	if prev, n, err := s.bindAccounts(ctx, oidcAt("https://b.example")); err != nil || prev != "oidc https://a.example" || n != 1 {
		t.Fatalf("oidc issuer change: %q %d %v", prev, n, err)
	}
	u, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "oidc", Subject: "s1", PreferredUsername: "mallory"}, "oidc https://b.example")
	if !errors.Is(err, errAccountInactive) {
		t.Fatalf("s1 through the new issuer: %+v %v, want errAccountInactive", u, err)
	}
}

// Switching sign-in off disables nothing and keeps the binding, so turning the same provider
// back on is not a change, and turning on another one still is.
func TestSignInOffKeepsTheBinding(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if _, _, err := s.bindAccounts(ctx, kyidentityAt("https://a.example")); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s, &store.User{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"})
	off := sso.Settings{Provider: sso.Field{Value: sso.KindNone}}
	if prev, n, err := s.bindAccounts(ctx, off); err != nil || prev != "" || n != 0 {
		t.Fatalf("switching off: %q %d %v, want nothing", prev, n, err)
	}
	if prev, n, err := s.bindAccounts(ctx, kyidentityAt("https://a.example")); err != nil || prev != "" || n != 0 {
		t.Fatalf("back on the same provider: %q %d %v, want a no-op", prev, n, err)
	}
	if prev, n, err := s.bindAccounts(ctx, oidcAt("https://a.example")); err != nil || prev != "kyidentity https://a.example" || n != 1 {
		t.Fatalf("kind change at the same issuer: %q %d %v", prev, n, err)
	}
}

// A binding whose kind is unknown (a damaged setting) disables every SSO account, never none.
func TestUnknownBoundKindDisablesEverySSOAccount(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if err := s.store.Settings().SetSetting(ctx, sso.KeyBound, "mystery https://a.example"); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s,
		&store.User{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"},
		&store.User{ID: "usr_s", Username: "s", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s2"},
		&store.User{ID: "usr_o", Username: "o", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "s3"},
		&store.User{ID: "usr_lo", Username: "lo", Role: "admin", Status: "active", SSOProvider: "local"},
	)
	if _, n, err := s.bindAccounts(ctx, oidcAt("https://a.example")); err != nil || n != 3 {
		t.Fatalf("unknown previous kind: %d %v, want 3", n, err)
	}
}

func TestLoadSignInAppliesSavedSettingsUnderTheEnvironment(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	sealed, err := sso.SealSecret(s.config.Security.EncryptionKey, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		sso.KeyProvider: sso.KindOIDC, sso.KeyDisplayName: "Acme", sso.KeyIssuer: "https://acme.example",
		sso.KeyClientID: "kc", sso.KeySecretSealed: sealed,
		sso.KeySecretRegistration: sso.SecretRegistration(sso.KindOIDC, "https://acme.example", "kc"),
	} {
		if err := s.store.Settings().SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if p := s.signin.Load(); p == nil || p.Kind != sso.KindOIDC || p.DisplayName != "Acme" {
		t.Fatalf("live provider %+v", p)
	}
	createUsers(t, s, &store.User{ID: "usr_o", Username: "o", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "o-1"})

	// An operator sets KyIdentity in the environment and restarts: it wins, and the oidc
	// accounts are disabled at startup. Without its own secret it would stay closed: the saved
	// one is the oidc registration's.
	s.config.SSO.KySignOnIssuer, s.config.SSO.KySignOnClientID, s.config.SSO.KySignOnSecret = "https://id.example", "env-client", "env-secret"
	before := auditCount(t, s)
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if p := s.signin.Load(); p == nil || p.Kind != sso.KindKyIdentity {
		t.Fatalf("live provider after the environment change %+v", p)
	}
	if u, _ := s.store.Users().GetUserByID(ctx, "usr_o"); u.Status != "inactive" {
		t.Fatalf("oidc account after the change: %s", u.Status)
	}
	if auditCount(t, s) != before+1 {
		t.Fatal("the provider change was not audited")
	}

	// The environment moves KyIdentity to another issuer between restarts: its accounts go too.
	createUsers(t, s, &store.User{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"})
	s.config.SSO.KySignOnIssuer = "https://id2.example"
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1", PreferredUsername: "mallory"}, "kyidentity https://id2.example"); !errors.Is(err, errAccountInactive) {
		t.Fatalf("s1 after an environment issuer change: %v, want errAccountInactive", err)
	}

	// A secret sealed under another key cannot be opened: refuse, and leave sign-in closed
	// rather than running the previous provider or one without its secret.
	other, _ := sso.SealSecret(make([]byte, 32), "s3cret")
	if err := s.store.Settings().SetSetting(ctx, sso.KeySecretSealed, other); err != nil {
		t.Fatal(err)
	}
	s.config.SSO.KySignOnIssuer, s.config.SSO.KySignOnClientID = "", ""
	if err := s.LoadSignIn(ctx); err == nil {
		t.Fatal("a secret sealed under another key was accepted")
	}
	if p := s.signin.Load(); p != nil {
		t.Fatalf("provider live after a failed load: %+v", p)
	}
}

// KyIdentity logins adopt scim rows by sub, so a scim row provisioned under an oidc binding is
// disabled when the binding moves to KyIdentity, and counted beforehand.
func TestMoveToKyIdentityDisablesSCIMRows(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if _, _, err := s.bindAccounts(ctx, oidcAt("https://a.example")); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s,
		&store.User{ID: "usr_o", Username: "o", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "o-1"},
		&store.User{ID: "usr_s", Username: "s", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s-1"},
	)
	if n, err := s.pendingDisable(ctx, kyidentityAt("https://id.example")); err != nil || n != 2 {
		t.Fatalf("pending disable: %d %v, want 2", n, err)
	}
	if _, n, err := s.bindAccounts(ctx, kyidentityAt("https://id.example")); err != nil || n != 2 {
		t.Fatalf("oidc to kyidentity: %d %v, want 2", n, err)
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s-1", PreferredUsername: "mallory"}, "kyidentity https://id.example"); !errors.Is(err, errAccountInactive) {
		t.Fatalf("scim sub through kyidentity: %v, want errAccountInactive", err)
	}
}

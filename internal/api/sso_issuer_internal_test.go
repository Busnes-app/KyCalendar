package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// scimCall sends one SCIM request through the full server, as the IdP would.
func scimCall(t *testing.T, s *Server, method, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+s.config.SCIM.BearerToken)
	req.Header.Set("Content-Type", "application/scim+json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func scimUser(name, sub string) map[string]any {
	return map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, "userName": name, "externalId": sub, "active": true}
}

func issuerOf(t *testing.T, s *Server, id string) *store.User {
	t.Helper()
	u, err := s.store.Users().GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// An issuer change disables the old issuer's accounts; SCIM, still the old directory, then
// re-activates them by replace (create never adopts across issuers: 409). The new issuer's colliding subs stay
// refused, on the kysignon row and through the kysignon-to-scim adoption, and no row is restamped.
func TestSCIMReactivationAfterIssuerChangeStaysRefused(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	a, b := kyidentityAt("https://a.example"), kyidentityAt("https://b.example")
	if _, _, err := s.bindAccounts(ctx, a); err != nil {
		t.Fatal(err)
	}
	carol, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1", PreferredUsername: "carol"}, a.Identity())
	if err != nil {
		t.Fatal(err)
	}
	w := scimCall(t, s, "POST", "/scim/v2/Users", scimUser("dave", "s2"))
	if w.Code != http.StatusCreated {
		t.Fatalf("scim create: %d %s", w.Code, w.Body.String())
	}
	var dave struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &dave)
	for _, id := range []string{carol.ID, dave.ID} {
		if u := issuerOf(t, s, id); u.SSOIssuer != a.Identity() {
			t.Fatalf("%s stamped %q, want %q", u.Username, u.SSOIssuer, a.Identity())
		}
	}
	if _, n, err := s.bindAccounts(ctx, b); err != nil || n != 2 {
		t.Fatalf("issuer change: %d %v", n, err)
	}

	if w := scimCall(t, s, "PUT", "/scim/v2/Users/"+dave.ID, scimUser("dave", "s2")); w.Code != http.StatusOK {
		t.Fatalf("scim replace: %d %s", w.Code, w.Body.String())
	}
	// SCIM never adopts the old issuer's kysignon row by sub; a replace by ID re-activates it.
	if w := scimCall(t, s, "POST", "/scim/v2/Users", scimUser("carol", "s1")); w.Code != http.StatusConflict {
		t.Fatalf("scim adopt across issuers: %d %s, want 409", w.Code, w.Body.String())
	}
	if w := scimCall(t, s, "PUT", "/scim/v2/Users/"+carol.ID, scimUser("carol", "s1")); w.Code != http.StatusOK {
		t.Fatalf("scim replace carol: %d %s", w.Code, w.Body.String())
	}
	for _, id := range []string{carol.ID, dave.ID} {
		if u := issuerOf(t, s, id); u.Status != "active" {
			t.Fatalf("%s not re-activated by SCIM: %s", u.Username, u.Status)
		}
	}

	for _, sub := range []string{"s1", "s2"} {
		_, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: sub, PreferredUsername: "mallory-" + sub}, b.Identity())
		if !errors.Is(err, errOtherIssuer) {
			t.Errorf("%s through the new issuer: %v, want errOtherIssuer", sub, err)
		}
	}
	for _, id := range []string{carol.ID, dave.ID} {
		if u := issuerOf(t, s, id); u.SSOIssuer != a.Identity() || u.Role != "user" {
			t.Fatalf("%s after the refused logins: issuer %q role %s", u.Username, u.SSOIssuer, u.Role)
		}
	}
	if _, err := s.store.Users().GetUserByUsername(ctx, "mallory-s1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused login created a second account: %v", err)
	}
}

// The first binding stamps the bound kind's unstamped rows (provisioned before any binding, or
// existing before migration 11 on an instance that had none) and they keep signing in; another
// kind's rows stay unstamped. Rows that existed under a stored binding are migration 11's.
func TestFirstBindingStampsUnstampedRowsOfItsKind(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	s.config.SSO.KySignOnIssuer, s.config.SSO.KySignOnClientID, s.config.SSO.KySignOnSecret = "https://id.example", "kc", "sec"
	identity := "kyidentity https://id.example"
	createUsers(t, s,
		&store.User{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"},
		&store.User{ID: "usr_s", Username: "s", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s2"},
		&store.User{ID: "usr_o", Username: "o", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "s3"},
	)
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"usr_k": identity, "usr_s": identity, "usr_o": ""} {
		if u := issuerOf(t, s, id); u.SSOIssuer != want {
			t.Fatalf("%s stamped %q, want %q", id, u.SSOIssuer, want)
		}
	}
	for _, sub := range []string{"s1", "s2"} {
		if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: sub}, identity); err != nil {
			t.Fatalf("%s after the stamp: %v", sub, err)
		}
	}
	// A restart under the same binding is a no-op and they still sign in.
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1"}, identity); err != nil {
		t.Fatalf("s1 after a restart: %v", err)
	}
}

// A row with no issuer is not the live provider's once anything is bound: one left unstamped by
// a provider change is refused even when re-activated.
func TestUnstampedRowIsRefusedAfterAChange(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if err := s.store.Settings().SetSetting(ctx, sso.KeyBound, "kyidentity https://a.example"); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s, &store.User{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"})
	b := kyidentityAt("https://b.example")
	if _, n, err := s.bindAccounts(ctx, b); err != nil || n != 1 {
		t.Fatalf("change: %d %v", n, err)
	}
	if u := issuerOf(t, s, "usr_k"); u.SSOIssuer != "" {
		t.Fatalf("a change stamped the old row with %q", u.SSOIssuer)
	}
	if w := scimCall(t, s, "PUT", "/scim/v2/Users/usr_k", scimUser("k", "s1")); w.Code != http.StatusOK {
		t.Fatalf("scim replace: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1"}, b.Identity()); !errors.Is(err, errOtherIssuer) {
		t.Fatalf("unstamped row through b: %v, want errOtherIssuer", err)
	}
	// Only the first binding stamps: rebinding b, as every later startup does, never stamps a row
	// a change passed by.
	if _, _, err := s.bindAccounts(ctx, b); err != nil {
		t.Fatal(err)
	}
	if u := issuerOf(t, s, "usr_k"); u.SSOIssuer != "" {
		t.Fatalf("restamped to %q", u.SSOIssuer)
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1"}, b.Identity()); !errors.Is(err, errOtherIssuer) {
		t.Fatalf("unstamped row after a restart: %v, want errOtherIssuer", err)
	}
}

// Issuers compare exactly: a trailing slash is another issuer and so a provider change.
func TestTrailingSlashIsAProviderChange(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	a := kyidentityAt("https://a.example")
	if _, _, err := s.bindAccounts(ctx, a); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s, &store.User{ID: "usr_k", Username: "k", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: a.Identity()})
	slash := kyidentityAt("https://a.example/")
	if sso.SavedSecretFits(map[string]string{sso.KeySecretSealed: "x", sso.KeySecretRegistration: sso.SecretRegistration(a.Provider.Value, a.Issuer.Value, a.ClientID.Value)}, slash) {
		t.Fatal("a trailing slash kept the registration, and so its secret")
	}
	if n, err := s.pendingDisable(ctx, slash); err != nil || n != 1 {
		t.Fatalf("pending: %d %v, want 1", n, err)
	}
	if prev, n, err := s.bindAccounts(ctx, slash); err != nil || prev != "kyidentity https://a.example" || n != 1 {
		t.Fatalf("rebinding with a slash: %q %d %v", prev, n, err)
	}
	// Even re-activated, the row is the unslashed issuer's.
	u := issuerOf(t, s, "usr_k")
	u.Status = "active"
	if err := s.store.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1"}, slash.Identity()); !errors.Is(err, errOtherIssuer) {
		t.Fatalf("s1 through the slashed issuer: %v, want errOtherIssuer", err)
	}
}

// On an install bound before migration 11, an inactive row stays unstamped: SCIM re-activating
// it does not let the bound provider sign in as it. The active row keeps signing in.
func TestMigration11InactiveRowStaysRefused(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	identity := "kyidentity https://a.example"
	if err := s.store.Settings().SetSetting(ctx, sso.KeyBound, identity); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s,
		&store.User{ID: "usr_on", Username: "on", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"},
		&store.User{ID: "usr_off", Username: "off", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "s2"},
	)
	// Pretend the database predates migration 11, then run it from a second handle.
	cfg := s.config.Database
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
	migrated, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = migrated.Close()

	if w := scimCall(t, s, "PUT", "/scim/v2/Users/usr_off", scimUser("off", "s2")); w.Code != http.StatusOK {
		t.Fatalf("scim replace: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s2"}, identity); !errors.Is(err, errOtherIssuer) {
		t.Fatalf("re-activated unstamped row: %v, want errOtherIssuer", err)
	}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1"}, identity); err != nil {
		t.Fatalf("active row after the migration: %v", err)
	}
}

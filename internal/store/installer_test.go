package store_test

import (
	"context"
	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kycalendar/internal/store"
	"testing"
)

func calendarBundle() applysetup.Common {
	return applysetup.Common{Schema: applysetup.Schema, Service: "kycalendar", SetupID: "12345678-1234-1234-1234-123456789abc", SSO: &applysetup.SSO{IssuerURL: "https://identity.example", ClientID: "message", ClientSecretFile: "/data/installer/client.secret", DirectorySecretFile: "/data/installer/directory.secret", RedirectURI: "https://message.example/api/sso/kysignon/callback"}, Admins: []applysetup.Identity{{Issuer: "https://identity.example", Subject: "owner", Username: "owner-admin"}}, Everyday: []applysetup.Identity{{Issuer: "https://identity.example", Subject: "daily", Username: "everyday"}}}
}
func TestInstallerAtomicOwnershipAndDirectoryFence(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	b := calendarBundle()
	if _, err := st.ApplyInstaller(ctx, b, true); err == nil {
		t.Fatal("missing check passed")
	}
	if n, _ := st.Users().CountUsers(ctx); n != 0 {
		t.Fatal("check mutated accounts")
	}
	r, err := st.ApplyInstaller(ctx, b, false)
	if err != nil || len(r.Created) != 3 {
		t.Fatalf("apply %+v %v", r, err)
	}
	for _, check := range []bool{true, false} {
		r, err = st.ApplyInstaller(ctx, b, check)
		if err != nil || len(r.Created) != 0 || len(r.Present) != 3 {
			t.Fatalf("resume %+v %v", r, err)
		}
	}
	if _, n, err := st.Audit().ListAuditRecords(ctx, 0, 100); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}
	u, err := st.Users().GetUserBySSO(ctx, "kysignon", "owner")
	if err != nil || u.Role != "admin" || u.PasswordHash != "" {
		t.Fatalf("owner %+v %v", u, err)
	}
	if _, allowed, err := st.InstallerIdentity(ctx, "https://foreign.example", "owner"); err != nil || allowed {
		t.Fatalf("foreign issuer %v %v", allowed, err)
	}
	if err = st.ApplyInstallerDirectory(ctx, b.SSO.IssuerURL, "owner", "user.deactivated", "2", "digest-off", false, false); err != nil {
		t.Fatal(err)
	}
	if err = st.ApplyInstallerDirectory(ctx, b.SSO.IssuerURL, "owner", "user.deactivated", "2", "digest-off", false, false); err != nil {
		t.Fatal(err)
	}
	if err = st.ApplyInstallerDirectory(ctx, b.SSO.IssuerURL, "owner", "user.updated", "1", "digest-on", true, true); err == nil {
		t.Fatal("stale activation accepted")
	}
	if err = st.ApplyInstallerDirectory(ctx, b.SSO.IssuerURL, "owner", "user.updated", "2", "digest-other", true, true); err == nil {
		t.Fatal("conflicting revision accepted")
	}
	if err = st.ApplyInstallerDirectory(ctx, "https://foreign.example", "owner", "user.updated", "3", "digest-on", true, true); err == nil {
		t.Fatal("foreign issuer accepted")
	}
	if _, err = st.ApplyInstaller(ctx, b, false); err == nil {
		t.Fatal("setup reactivated revoked account")
	}
	if _, allowed, _ := st.InstallerIdentity(ctx, b.SSO.IssuerURL, "owner"); allowed {
		t.Fatal("offboarded owner allowed")
	}
	if _, n, err := st.Audit().ListAuditRecords(ctx, 0, 100); err != nil || n != 2 {
		t.Fatalf("duplicate audit %d %v", n, err)
	}
}
func TestInstallerForeignCollisionRollsBack(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	b := calendarBundle()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "foreign", Username: "EVERYDAY", Role: "admin", Status: "active", SSOProvider: "local", PasswordHash: "retained-hash"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyInstaller(ctx, b, false); err == nil {
		t.Fatal("adopted foreign username")
	}
	if n, _ := st.Users().CountUsers(ctx); n != 1 {
		t.Fatal("partial creation survived")
	}
	if _, err := st.Settings().GetSetting(ctx, store.InstallerConfigKey); err != store.ErrNotFound {
		t.Fatalf("partial configuration %v", err)
	}
	u, _ := st.Users().GetUserByID(ctx, "foreign")
	if u.PasswordHash != "retained-hash" {
		t.Fatal("reset credential")
	}
}

func TestInstallerNativeSettingsAndBindingStayOwned(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	b := calendarBundle()
	if _, err := st.ApplyInstaller(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{store.InstallerConfigKey, "signin_bound", "signin_issuer"} {
		if err := st.Settings().SetSetting(ctx, key, "foreign"); err == nil {
			t.Fatal("changed owned setting", key)
		}
		if err := st.Settings().DeleteSetting(ctx, key); err == nil {
			t.Fatal("deleted owned setting", key)
		}
	}
	u, err := st.Users().GetUserBySSO(ctx, "kysignon", "owner")
	if err != nil {
		t.Fatal(err)
	}
	u.Role = "user"
	if err := st.Users().UpdateUser(ctx, u); err == nil {
		t.Fatal("overwrote owned role")
	}
	if err := st.Users().SetSSORole(ctx, u.ID, "user"); err == nil {
		t.Fatal("overwrote owned SSO role")
	}
	if err := st.Users().DeleteUser(ctx, u.ID); err == nil {
		t.Fatal("deleted owned user")
	}
	if _, err := st.Users().ReattachSSOUser(ctx, store.System, u.ID, "kyidentity https://foreign.example"); err == nil {
		t.Fatal("reattached owned user")
	}
	if _, err := st.ApplyInstaller(ctx, b, true); err != nil {
		t.Fatal("owned data drifted", err)
	}
}

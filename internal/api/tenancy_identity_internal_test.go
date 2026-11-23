package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/password"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// multiTenantSignIn is a server signing in through idp, switched to multi-tenant mode with staff
// as its staff org and the given orgs mirrored (id -> status).
func multiTenantSignIn(t *testing.T, staff string, orgs map[string]string) (*Server, *signingIdP) {
	t.Helper()
	s, _ := davInternalServer(t)
	ctx := context.Background()
	idp := newSigningIdP(t)
	if _, _, err := s.bindAccounts(ctx, kyidentityAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", idp.Client()))
	driver, q := "sqlite", `INSERT INTO orgs (id, status) VALUES (?, ?)`
	if s.config.Database.Driver == "postgres" {
		driver, q = "pgx", `INSERT INTO orgs (id, status) VALUES ($1, $2)`
	}
	db, err := sql.Open(driver, s.config.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, status := range orgs {
		if _, err := db.Exec(q, id, status); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{store.TenancySetting: "multi", store.StaffOrgSetting: staff} {
		if err := s.store.Settings().SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return s, idp
}

// loginWithOrg signs sub in with org as the token's org_id ("" sends none) and roles.
func loginWithOrg(t *testing.T, s *Server, idp *signingIdP, sub, org string, roles ...string) int {
	t.Helper()
	idp.extra = map[string]any{}
	if org != "" {
		idp.extra["org_id"] = org
	}
	idp.roles = roles
	return ssoLogin(t, s, idp, sub, "sid-"+sub).Code
}

func auditDetails(t *testing.T, s *Server, action string) []string {
	t.Helper()
	recs, _, err := s.store.Audit().ListAuditRecords(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range recs {
		if r.Action == action {
			out = append(out, r.UserID+" "+r.Details)
		}
	}
	return out
}

// In multi-tenant mode the org comes only from the verified ID token: none is refused, an org
// that does not serve is refused, a new account joins the token's org, and a known account's
// token naming another org changes nothing (tenancy spec, Identity binding).
func TestMultiTenantSignInBindsTheTokensOrg(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-b": "active", "org-gone": "suspended", "org-staff": "active"})
	ctx := context.Background()

	if code := loginWithOrg(t, s, idp, "alice", ""); code != http.StatusForbidden {
		t.Fatalf("no org_id: %d", code)
	}
	if got := auditDetails(t, s, "auth.sso_refused"); len(got) != 1 || !strings.Contains(got[0], "reason=no_org subject=alice") {
		t.Fatalf("no_org audit: %q", got)
	}
	for _, org := range []string{"org-unknown", "org-gone"} {
		if code := loginWithOrg(t, s, idp, "alice", org); code != http.StatusForbidden {
			t.Fatalf("%s: %d", org, code)
		}
	}
	if _, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "alice"); err == nil {
		t.Fatal("a refused sign-in created the account")
	}

	if code := loginWithOrg(t, s, idp, "alice", "org-a"); code != http.StatusFound && code != http.StatusSeeOther {
		t.Fatalf("org-a sign-in: %d", code)
	}
	alice, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "alice")
	if err != nil || store.OrgOf(alice) != "org-a" {
		t.Fatalf("new account: %+v %v", alice, err)
	}
	if code := loginWithOrg(t, s, idp, "alice", "org-b"); code != http.StatusForbidden {
		t.Fatalf("org_mismatch: %d", code)
	}
	if got := auditDetails(t, s, "auth.sso_refused"); len(got) != 2 || !strings.Contains(got[0]+got[1], alice.ID+" reason=org_mismatch org=org-a token_org=org-b") {
		t.Fatalf("org_mismatch audit: %q", got)
	}
	if after, _ := s.store.Users().GetUserByID(ctx, alice.ID); store.OrgOf(after) != "org-a" || after.Status != "active" {
		t.Fatalf("a mismatched token moved or changed the account: %+v", after)
	}
}

// D7: in multi-tenant mode kycalendar.admin makes an administrator only in the staff org.
func TestMultiTenantAdminOnlyInTheStaffOrg(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-staff": "active"})
	ctx := context.Background()
	for sub, org := range map[string]string{"customer": "org-a", "staffer": "org-staff"} {
		if code := loginWithOrg(t, s, idp, sub, org, access.AdminAppRole); code >= 400 {
			t.Fatalf("%s: %d", sub, code)
		}
	}
	for sub, want := range map[string]string{"customer": "user", "staffer": "admin"} {
		if u, err := s.store.Users().GetUserBySSO(ctx, "kysignon", sub); err != nil || u.Role != want {
			t.Errorf("%s: %+v %v, want role %s", sub, u, err, want)
		}
	}
}

// With no staff org recorded, nobody is an administrator.
func TestMultiTenantWithoutAStaffOrgHasNoAdmins(t *testing.T) {
	s, idp := multiTenantSignIn(t, "", map[string]string{"org-a": "active"})
	if code := loginWithOrg(t, s, idp, "x", "org-a", access.AdminAppRole); code >= 400 {
		t.Fatalf("sign-in: %d", code)
	}
	if u, err := s.store.Users().GetUserBySSO(context.Background(), "kysignon", "x"); err != nil || u.Role != "user" {
		t.Fatalf("%+v %v", u, err)
	}
}

// In multi-tenant mode a logout token must name the org of the account it ends, by sub or by a
// live session's sid; a missing or other org_id is refused and audited before anything changes.
func TestMultiTenantLogoutChecksTheOrgFirst(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active"})
	idp.extra = map[string]any{"org_id": "org-a"}
	idp.roles = nil
	first := ssoSession(ssoLogin(t, s, idp, "alice", "sid-1"))
	if first == nil || !signedIn(s, first) {
		t.Fatal("sign-in did not issue a working session")
	}
	token := func(jti string, claims map[string]any) string {
		c := map[string]any{"iss": idp.URL, "aud": "kc", "jti": jti, "iat": time.Now().Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
			"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}}
		for k, v := range claims {
			c[k] = v
		}
		return idp.sign(t, "logout+jwt", c)
	}
	for name, claims := range map[string]map[string]any{
		"no org_id":          {"sub": "alice", "sid": "sid-1"},
		"another org by sub": {"sub": "alice", "org_id": "org-b"},
		"another org by sid": {"sid": "sid-1", "org_id": "org-b"},
		"another org, both":  {"sub": "alice", "sid": "sid-1", "org_id": "org-b"},
	} {
		if w := postLogout(s, token(name, claims)); w.Code != http.StatusBadRequest || !signedIn(s, first) {
			t.Fatalf("%s: %d, signed in %v", name, w.Code, signedIn(s, first))
		}
	}
	if got := auditDetails(t, s, "auth.sso_org_mismatch"); len(got) != 4 {
		t.Fatalf("audit: %q", got)
	}
	if w := postLogout(s, token("ok", map[string]any{"sub": "alice", "sid": "sid-1", "org_id": "org-a"})); w.Code != http.StatusOK || signedIn(s, first) {
		t.Fatalf("logout of the account's org: %d, still signed in %v", w.Code, signedIn(s, first))
	}
}

// A logout token is applied at most once: a replay is refused and audited, and changes nothing.
func TestLogoutTokenIsAppliedOnce(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active"})
	idp.extra = map[string]any{"org_id": "org-a"}
	idp.roles = nil
	raw := idp.sign(t, "logout+jwt", map[string]any{"iss": idp.URL, "aud": "kc", "sub": "alice", "jti": "once", "org_id": "org-a",
		"iat": time.Now().Add(-2 * time.Second).Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}})
	if w := postLogout(s, raw); w.Code != http.StatusOK {
		t.Fatalf("first delivery: %d", w.Code)
	}
	if w := postLogout(s, raw); w.Code != http.StatusBadRequest {
		t.Fatalf("replay: %d", w.Code)
	}
	if got := auditDetails(t, s, "auth.sso_logout_replayed"); len(got) != 1 {
		t.Fatalf("replay audit: %q", got)
	}
}

// D8: a password admits only a staff administrator in multi-tenant mode.
func TestMultiTenantPasswordOnlyForStaffAdmins(t *testing.T) {
	s, _ := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-staff": "active"})
	s.config.Captcha.Provider = "" // the proof-of-work puzzle is not what this test is about
	ctx := context.Background()
	hash, err := password.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_breakglass", Username: "breakglass", Role: "admin", OrgID: "org-staff"},
		{ID: "usr_customer_admin", Username: "custadmin", Role: "admin", OrgID: "org-a"},
		{ID: "usr_member", Username: "member", Role: "user", OrgID: "org-staff"},
	} {
		u.Status, u.SSOProvider, u.PasswordHash = "active", "local", hash
		if err := s.store.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]int{"breakglass": http.StatusOK, "custadmin": http.StatusUnauthorized, "member": http.StatusUnauthorized} {
		if got := passwordLogin(t, s, name, "correct horse battery staple"); got != want {
			t.Errorf("%s: %d, want %d", name, got, want)
		}
	}
}

func passwordLogin(t *testing.T, s *Server, username, pw string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": pw})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w.Code
}

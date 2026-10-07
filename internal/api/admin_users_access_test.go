package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// sessionFor signs u in without a password, as an SSO login would, and returns its cookie.
func sessionFor(t *testing.T, st store.Store, u *store.User) *http.Cookie {
	t.Helper()
	raw := "tok-" + u.ID
	now := time.Now().UTC()
	if err := st.Sessions().CreateSession(context.Background(), &store.Session{TokenHash: crypto.SHA256Hex([]byte(raw)), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: raw}
}

func TestAdminUserWritesAreLocalOnly(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_synced", Username: "synced", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s1"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "/api/admin/users/usr_synced", `{"display_name":"Mine"}`},
		{"POST", "/api/admin/users/usr_synced/reset-password", ""},
		{"POST", "/api/admin/users/usr_synced/role", `{"role":"admin"}`},
		{"POST", "/api/admin/users/usr_synced/disable", ""},
		{"POST", "/api/admin/users/usr_synced/enable", ""},
	} {
		if w := call(t, srv, tc.method, tc.path, tc.body, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
			t.Errorf("%s %s: %d %s, want 409 managed_externally", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if u, _ := st.Users().GetUserByID(context.Background(), "usr_synced"); u.DisplayName != "" || u.Role != "user" || u.Status != "active" {
		t.Fatalf("a synced account changed: %+v", u)
	}
	if w := call(t, srv, "POST", "/api/admin/users/usr_nobody/disable", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("unknown person: %d, want 404", w.Code)
	}
}

func TestAdminRoleAndStatusRules(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	bob := loginAs(t, srv, st, "bob", "user")
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: "ap_bob", UserID: "usr_bob", Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/admin/users/usr_root/disable"} {
		if w := call(t, srv, "POST", path, "", admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "self" {
			t.Errorf("%s: %d %s, want 409 self", path, w.Code, w.Body.String())
		}
	}
	if w := call(t, srv, "POST", "/api/admin/users/usr_root/role", `{"role":"user"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "self" {
		t.Errorf("self demotion: %d %s, want 409 self", w.Code, w.Body.String())
	}

	if w := call(t, srv, "POST", "/api/admin/users/usr_bob/role", `{"role":"admin"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("promote bob: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/auth/me", "", bob); bytes.Contains(w.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Error("bob's session survived the role change")
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, "usr_bob"); len(list) != 0 {
		t.Error("bob's app passwords survived the role change")
	}
	if d := auditDetails(t, st, "admin.user_role"); len(d) != 1 || d[0] != "from=user to=admin" {
		t.Errorf("role audit: %v", d)
	}

	restore := api.SetStepUpWindowForTest(0)
	for _, tc := range []struct{ path, body string }{
		{"/api/admin/users/usr_bob/role", `{"role":"user"}`},
		{"/api/admin/users/usr_bob/disable", ""},
		{"/api/admin/users/usr_bob/reset-password", ""},
	} {
		if w := call(t, srv, "POST", tc.path, tc.body, admin); w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
			t.Errorf("%s on a stale session: %d %s, want 403 reauth_required", tc.path, w.Code, w.Body.String())
		}
	}
	restore()

	if w := call(t, srv, "POST", "/api/admin/users/usr_bob/disable", "", admin); w.Code != http.StatusOK {
		t.Fatalf("disable bob: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "POST", "/api/admin/users/usr_bob/enable", "", admin); w.Code != http.StatusOK {
		t.Fatalf("enable bob: %d %s", w.Code, w.Body.String())
	}
	// No-ops answer 200 and write nothing.
	for _, tc := range []struct{ path, body string }{
		{"/api/admin/users/usr_bob/enable", ""},
		{"/api/admin/users/usr_bob/role", `{"role":"admin"}`},
	} {
		if w := call(t, srv, "POST", tc.path, tc.body, admin); w.Code != http.StatusOK {
			t.Errorf("no-op %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if len(auditDetails(t, st, "admin.user_disable")) != 1 || len(auditDetails(t, st, "admin.user_enable")) != 1 || len(auditDetails(t, st, "admin.user_role")) != 1 {
		t.Error("want one admin.user_disable, one admin.user_enable and one admin.user_role row")
	}
	for _, body := range []string{`{"role":"owner"}`, `{}`, `nope`} {
		if w := call(t, srv, "POST", "/api/admin/users/usr_bob/role", body, admin); w.Code != http.StatusBadRequest {
			t.Errorf("role %s: %d, want 400", body, w.Code)
		}
	}

	// An SSO admin cannot take away the last active local admin: that account is the way back in.
	if err := st.Users().SetRole(ctx, "usr_bob", "user"); err != nil {
		t.Fatal(err)
	}
	ky := &store.User{ID: "usr_ky", Username: "ky", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "k1"}
	if err := st.Users().CreateUser(ctx, ky); err != nil {
		t.Fatal(err)
	}
	kyAdmin := sessionFor(t, st, ky)
	for _, tc := range []struct{ path, body string }{
		{"/api/admin/users/usr_root/disable", ""},
		{"/api/admin/users/usr_root/role", `{"role":"user"}`},
	} {
		if w := call(t, srv, "POST", tc.path, tc.body, kyAdmin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "last_admin" {
			t.Errorf("%s: %d %s, want 409 last_admin", tc.path, w.Code, w.Body.String())
		}
	}
}

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/api"
)

func passwordLogin(t *testing.T, srv *api.Server, username, pw string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": pw})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestAdminCreateUserReturnsThePasswordOnce(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")

	w := call(t, srv, "POST", "/api/admin/users", `{"username":"ann","display_name":"Ann Lee","email":"ann@example.com","role":"user"}`, admin)
	if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	var created struct {
		User struct {
			ID     string `json:"id"`
			Source string `json:"source"`
		} `json:"user"`
		TemporaryPassword string `json:"temporary_password"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	temp := created.TemporaryPassword
	if len(temp) < 12 || created.User.Source != "local" {
		t.Fatalf("created %+v", created)
	}
	u, err := st.Users().GetUserByID(ctx, created.User.ID)
	if err != nil || !u.MustChangePassword || u.Role != "user" || strings.Contains(u.PasswordHash, temp) {
		t.Fatalf("stored user %+v %v", u, err)
	}
	if ok, err := password.Verify(temp, u.PasswordHash); !ok || err != nil {
		t.Fatalf("the stored hash does not verify the returned password: %v", err)
	}
	if bytes.Contains(call(t, srv, "GET", "/api/admin/users", "", admin).Body.Bytes(), []byte(temp)) {
		t.Fatal("the temporary password is listed again")
	}
	for _, d := range auditDetails(t, st, "admin.user_create") {
		if strings.Contains(d, temp) {
			t.Fatal("the temporary password reached the audit log")
		}
	}
	if w := passwordLogin(t, srv, "ann", temp); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"must_change_password":true`)) {
		t.Fatalf("first sign-in: %d %s", w.Code, w.Body.String())
	}

	if w := call(t, srv, "POST", "/api/admin/users", `{"username":"ANN"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Errorf("case twin: %d %s, want 409 name_taken", w.Code, w.Body.String())
	}
	for _, bad := range []string{`{"username":"a b"}`, `{"username":"zed","email":"nope"}`, `{"username":"zed","role":"owner"}`, `{"username":"zed","display_name":"x\u0000y"}`} {
		if w := call(t, srv, "POST", "/api/admin/users", bad, admin); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}

	restore := api.SetStepUpWindowForTest(0)
	defer restore()
	if w := call(t, srv, "POST", "/api/admin/users", `{"username":"boss","role":"admin"}`, admin); w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
		t.Errorf("admin creation on a stale session: %d %s, want 403 reauth_required", w.Code, w.Body.String())
	}
	if w := call(t, srv, "POST", "/api/admin/users", `{"username":"cal"}`, admin); w.Code != http.StatusCreated {
		t.Errorf("everyday creation needs no step-up: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminResetPassword(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	bob := loginAs(t, srv, st, "bob", "user")
	w := call(t, srv, "POST", "/api/admin/users/usr_bob/reset-password", "", admin)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w := call(t, srv, "GET", "/api/auth/me", "", bob); bytes.Contains(w.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Error("bob's session survived the reset")
	}
	if w := passwordLogin(t, srv, "bob", body.TemporaryPassword); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"must_change_password":true`)) {
		t.Fatalf("sign-in with the temporary password: %d %s", w.Code, w.Body.String())
	}
	if d := auditDetails(t, st, "admin.user_reset_password"); len(d) != 1 || strings.Contains(d[0], body.TemporaryPassword) {
		t.Errorf("reset audit: %v", d)
	}
}

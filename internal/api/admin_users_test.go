package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdminListUsersSearchesAndHidesSecrets(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	_ = loginAs(t, srv, st, "alice", "user")
	_ = loginAs(t, srv, st, "bob", "user")

	w := call(t, srv, "GET", "/api/admin/users?q=LIC", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Users []struct {
			Username string `json:"username"`
			Source   string `json:"source"`
			Role     string `json:"role"`
		} `json:"users"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Users) != 1 || body.Users[0].Username != "alice" || body.Users[0].Source != "local" {
		t.Fatalf("q=LIC: %+v", body)
	}

	all := call(t, srv, "GET", "/api/admin/users", "", admin).Body.Bytes()
	for _, secret := range []string{"argon2", "password_hash", "totp_secret", "recovery_codes"} {
		if bytes.Contains(all, []byte(secret)) {
			t.Errorf("people list leaked %q: %s", secret, all)
		}
	}
	if w := call(t, srv, "GET", "/api/admin/users?q="+strings.Repeat("x", 256), "", admin); w.Code != http.StatusBadRequest {
		t.Errorf("overlong search: %d, want 400", w.Code)
	}
}

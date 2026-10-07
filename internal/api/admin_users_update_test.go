package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestAdminUpdateUser(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	_ = loginAs(t, srv, st, "alice", "user")
	_ = loginAs(t, srv, st, "bob", "user")

	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"username":"alice2","display_name":"Alice L","email":"a@example.com"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	u, _ := st.Users().GetUserByID(ctx, "usr_alice")
	if u.Username != "alice2" || u.DisplayName != "Alice L" || u.Email != "a@example.com" {
		t.Fatalf("after update: %+v", u)
	}
	recs, _, _ := st.Audit().ListAuditRecords(ctx, 0, 50)
	renamedBy := ""
	for _, r := range recs {
		if r.Action == "user.renamed" {
			renamedBy = r.UserID
		}
	}
	if renamedBy != "usr_root" {
		t.Errorf("user.renamed actor = %q, want the acting admin", renamedBy)
	}
	if d := auditDetails(t, st, "admin.user_update"); len(d) != 1 || d[0] != "fields=username,profile" {
		t.Errorf("update audit: %v", d)
	}

	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"username":"BOB"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Errorf("rename onto a case twin: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"username":"Alice2"}`, admin); w.Code != http.StatusOK {
		t.Errorf("a case-only rename of the same account: %d %s", w.Code, w.Body.String())
	}
	// Nothing is written when any field is invalid.
	long := `"` + strings.Repeat("a", 244) + `@example.com"` // 256 bytes
	for _, body := range []string{`{"username":"alice3","email":"nope"}`, `{"username":"alice3","email":` + long + `}`, `{"username":"alice3","display_name":" "}`, `{"username":"a"}`} {
		if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", body, admin); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_alice"); u.Username != "Alice2" || u.Email != "a@example.com" {
		t.Errorf("a refused update wrote %+v", u)
	}
	if w := call(t, srv, "POST", "/api/admin/users", `{"username":"carol","email":`+long+`}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("create with a 256-byte email: %d", w.Code)
	}

	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_synced", Username: "synced", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "x"}); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "PATCH", "/api/admin/users/usr_synced", `{"display_name":"S"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
		t.Errorf("edit a synced person: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/users/usr_nobody", `{"display_name":"N"}`, admin); w.Code != http.StatusNotFound {
		t.Errorf("edit a missing person: %d", w.Code)
	}
	if w := call(t, srv, "PATCH", "/api/admin/users/usr_bob", `{}`, admin); w.Code != http.StatusOK {
		t.Errorf("an empty edit: %d", w.Code)
	}
	if d := auditDetails(t, st, "admin.user_update"); len(d) != 2 {
		t.Errorf("an empty edit was audited: %v", d)
	}
}

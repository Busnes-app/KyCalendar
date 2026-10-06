package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// davUser creates an everyday user with one app password and returns the token.
func davUser(t *testing.T, st store.Store, username, role string) string {
	t.Helper()
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_" + username, Username: username, Role: role, Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: id, UserID: "usr_" + username, Label: "t", Hash: hash}); err != nil {
		t.Fatal(err)
	}
	return token
}

func davDo(srv *api.Server, method, path, user, pass string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestDAVAuthChallenge(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	w := davDo(srv, "PROPFIND", "/dav/", "", "")
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("want 401 challenge, got %d", w.Code)
	}
}

func TestDAVAuthWrongPassword(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	if w := davDo(srv, "PROPFIND", "/dav/", "alice", token+"x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", w.Code)
	}
	other := davUser(t, st, "bob", "user")
	if w := davDo(srv, "PROPFIND", "/dav/", "alice", other); w.Code != http.StatusUnauthorized {
		t.Fatalf("bob's token as alice: %d", w.Code)
	}
}

func TestDAVAuthInactiveUser(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	u, _ := st.Users().GetUserByUsername(context.Background(), "alice")
	u.Status = "inactive"
	if err := st.Users().UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if w := davDo(srv, "PROPFIND", "/dav/", "alice", token); w.Code != http.StatusUnauthorized {
		t.Fatalf("inactive user: %d", w.Code)
	}
}

func TestDAVAuthAdminRefused(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "root", "admin")
	if w := davDo(srv, "PROPFIND", "/dav/", "root", token); w.Code != http.StatusForbidden {
		t.Fatalf("admin: %d", w.Code)
	}
}

func TestDAVAuthLockout(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	for i := 0; i < 10; i++ {
		davDo(srv, "PROPFIND", "/dav/", "alice", "kc_bad_bad")
	}
	w := davDo(srv, "PROPFIND", "/dav/", "alice", token)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("want 429 after 10 failures, got %d", w.Code)
	}
}

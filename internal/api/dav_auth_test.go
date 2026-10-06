package api_test

import (
	"context"
	"fmt"
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

func davFrom(srv *api.Server, ip, user, pass string) int {
	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	req.RemoteAddr = ip + ":1234"
	req.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Code
}

// A stranger without any of alice's token IDs cannot lock her out (R28).
func TestDAVAuthStrangerCannotLockOutUser(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	for i := 0; i < 100; i++ {
		_, garbage, _, _ := apppass.Generate()
		davFrom(srv, fmt.Sprintf("198.51.100.%d", i), "alice", garbage)
		davFrom(srv, fmt.Sprintf("198.51.101.%d", i), "alice", "not-a-token")
	}
	if code := davFrom(srv, "203.0.113.9", "alice", token); code == http.StatusTooManyRequests || code == http.StatusUnauthorized {
		t.Fatalf("alice locked out by garbage tokens: %d", code)
	}
}

// Wrong secrets for alice's real token ID count against alice from any IP.
func TestDAVAuthWrongSecretCountsPerUser(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	for i := 0; i < 50; i++ {
		davFrom(srv, fmt.Sprintf("198.51.100.%d", i), "alice", token+"x")
	}
	if code := davFrom(srv, "203.0.113.9", "alice", token); code != http.StatusTooManyRequests {
		t.Fatalf("want 429 after 50 wrong secrets, got %d", code)
	}
}

// Usernames differing only in case resolve through the token's owner, not a LOWER() lookup.
func TestDAVAuthCaseVariantUsernames(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	lower := davUser(t, st, "alice", "user")
	upper := davUser(t, st, "Alice", "user")
	for _, tc := range []struct{ name, token, home string }{
		{"alice", lower, "/dav/usr_alice/"},
		{"Alice", upper, "/dav/usr_Alice/"},
		{"ALICE", upper, "/dav/usr_Alice/"},
	} {
		if w := davDo(srv, "OPTIONS", tc.home, tc.name, tc.token); w.Code >= 300 {
			t.Fatalf("%s: %d", tc.name, w.Code)
		}
	}
	// The token decides the user: alice's token never reaches Alice's home.
	if w := davDo(srv, "OPTIONS", "/dav/usr_Alice/", "Alice", lower); w.Code != http.StatusForbidden {
		t.Fatalf("alice's token on Alice's home: %d", w.Code)
	}
	if w := davDo(srv, "PROPFIND", "/dav/", "Alice", lower+"x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", w.Code)
	}
}

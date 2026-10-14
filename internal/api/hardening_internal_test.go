package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// A session revoked between requireEveryday and the handler must not panic list or delete.
func TestAppPasswordHandlersUseTheAuthenticatedUser(t *testing.T) {
	s, _ := davInternalServer(t)
	u := &store.User{ID: "usr_alice", Username: "alice", Role: "user", Status: "active", SSOProvider: "local"}
	if err := s.store.Users().CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method string
		h      http.HandlerFunc
		want   int
	}{
		{"GET", s.handleListAppPasswords, http.StatusOK},
		{"DELETE", s.handleDeleteAppPassword, http.StatusNotFound},
	} {
		// No cookie: the session is gone by the time the handler runs.
		r := withSessionUser(httptest.NewRequest(c.method, "/api/app-passwords/x", nil), u, &store.Session{})
		r.SetPathValue("id", "x")
		w := httptest.NewRecorder()
		c.h(w, r)
		if w.Code != c.want {
			t.Fatalf("%s: %d, want %d", c.method, w.Code, c.want)
		}
	}
}

// One host holds a whole IPv6 /64; the DAV limiter must not hand each address its own window.
func TestDAVLimiterKeysIPv6ByPrefix(t *testing.T) {
	_, h := davInternalServer(t)
	attempt := func(addr string) int {
		req := httptest.NewRequest("PROPFIND", "/dav/", nil)
		req.SetBasicAuth("alice", "kc_nope_nope")
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for i := 1; i <= davFailsPerIP; i++ {
		attempt(fmt.Sprintf("[2001:db8:1:2::%x]:40000", i))
	}
	if code := attempt("[2001:db8:1:2::ff]:40000"); code != http.StatusTooManyRequests {
		t.Fatalf("same /64 after %d failures: %d, want 429", davFailsPerIP, code)
	}
	if code := attempt("[2001:db8:1:3::1]:40000"); code == http.StatusTooManyRequests {
		t.Fatal("a neighbouring /64 shared the window")
	}
}

// Credentials that are not even shaped like an app password (an account password, a probe)
// count against the address but write no audit row.
func TestDAVAuthDoesNotAuditUnparseableCredentials(t *testing.T) {
	s, h := davInternalServer(t)
	for _, pass := range []string{"my-account-password", "kc_", "kc_id_"} {
		if w := davAttempt(h, "alice", pass); w.Code != http.StatusUnauthorized {
			t.Fatalf("%q: %d", pass, w.Code)
		}
	}
	if n := auditCount(t, s); n != 0 {
		t.Fatalf("audit rows: %d", n)
	}
	if n := s.davFails.seen["ip:"+"192.0.2.1"].count; n != 3 {
		t.Fatalf("limiter count: %d", n)
	}
}

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// An issuer change disables carol and refuses her sub through the new issuer; an admin's reattach
// binds her row to the new issuer, and her next login there signs in as the same account.
func TestLoginSucceedsAfterReattach(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	a, b := kyidentityAt("https://a.example"), kyidentityAt("https://b.example")
	if _, _, err := s.bindAccounts(ctx, a); err != nil {
		t.Fatal(err)
	}
	// An administrator under a: the role is re-proven at the first login through b.
	carol, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: "s1", PreferredUsername: "carol", Roles: []string{"kycalendar.admin"}}, a.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if _, n, err := s.bindAccounts(ctx, b); err != nil || n != 1 {
		t.Fatalf("issuer change: %d %v", n, err)
	}
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", "https://b.example", "kc", "sec", nil))
	claims := &sso.IdentityClaims{Provider: "kysignon", Subject: "s1", PreferredUsername: "carol"}
	if _, err := s.upsertSSOUser(ctx, claims, b.Identity()); !errors.Is(err, errAccountInactive) {
		t.Fatalf("before reattach: %v, want errAccountInactive", err)
	}

	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	createUsers(t, s, root)
	now := time.Now().UTC()
	if err := s.store.Sessions().CreateSession(ctx, &store.Session{TokenHash: crypto.SHA256Hex([]byte("tok-root")), UserID: root.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/users/"+carol.ID+"/reattach", strings.NewReader(`{"binding":"`+b.Identity()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "tok-root"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf"})
	req.Header.Set(auth.HeaderCSRF, "csrf")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reattach: %d %s", w.Code, w.Body.String())
	}

	got, err := s.upsertSSOUser(ctx, claims, b.Identity())
	if err != nil || got.ID != carol.ID {
		t.Fatalf("login after reattach: %+v %v, want carol's own row", got, err)
	}
	if carol.Role != "admin" || got.Role != "user" || issuerOf(t, s, carol.ID).Role != "user" {
		t.Fatalf("roles: under a %q, after login through b %q, stored %q; want admin, then user", carol.Role, got.Role, issuerOf(t, s, carol.ID).Role)
	}
}

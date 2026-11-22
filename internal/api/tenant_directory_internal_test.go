package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const directorySecret = "directory-test-secret-0123456789abcdef"

// directoryWorld is a multi-tenant server with alice signed in through KyIdentity in org-a.
func directoryWorld(t *testing.T) (*Server, *store.User, *http.Cookie) {
	t.Helper()
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-staff": "active"})
	s.config.SSO.Enabled = true
	s.config.SSO.KySignOnHMACSecret = directorySecret
	idp.extra = map[string]any{"org_id": "org-a"}
	cookie := ssoSession(ssoLogin(t, s, idp, "alice", "sid-alice"))
	alice, err := s.store.Users().GetUserBySSO(context.Background(), "kysignon", "alice")
	if err != nil || cookie == nil || !signedIn(s, cookie) {
		t.Fatalf("alice: %+v %v", alice, err)
	}
	return s, alice, cookie
}

// directoryEvent posts one signed SCIM User body as KyIdentity's connector does.
func directoryEvent(t *testing.T, s *Server, event, id string, body map[string]any) int {
	t.Helper()
	raw, _ := json.Marshal(body)
	h, err := syncauth.Sign([]byte(directorySecret), time.Now(), event, id, raw)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/sso/kysignon/sync", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/scim+json")
	h.Apply(r)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code
}

func scimBody(sub, org string, active bool, name string) map[string]any {
	b := map[string]any{"id": sub, "active": active, "displayName": name, "roles": []any{},
		"emails": []any{map[string]any{"value": name + "@example.com", "primary": true}}, "meta": map[string]any{"version": `W/"1"`}}
	if org != "" {
		b["org_id"] = org
	}
	return b
}

// Profile changes apply only with the account's own org; another org or none changes nothing
// and is audited. A replayed event is acknowledged and not applied again.
func TestTenantDirectoryProfileNeedsTheAccountsOrg(t *testing.T) {
	s, alice, cookie := directoryWorld(t)
	ctx := context.Background()
	name := func() string {
		u, err := s.store.Users().GetUserByID(ctx, alice.ID)
		if err != nil {
			t.Fatal(err)
		}
		return u.DisplayName + " " + u.Email
	}
	if code := directoryEvent(t, s, "user.updated", "e1", scimBody("alice", "org-a", true, "ally")); code != http.StatusNoContent || name() != "ally ally@example.com" {
		t.Fatalf("own org: %d, profile %q", code, name())
	}
	for i, org := range []string{"org-b", ""} {
		if code := directoryEvent(t, s, "user.updated", "e-other-"+string(rune('a'+i)), scimBody("alice", org, true, "mallory")); code != http.StatusNoContent || name() != "ally ally@example.com" {
			t.Errorf("org %q: %d, profile %q", org, code, name())
		}
	}
	got := strings.Join(auditDetails(t, s, "sso.webhook_org_mismatch"), "|")
	if !strings.Contains(got, "org=org-a token_org=org-b") || !strings.Contains(got, "reason=no_org") {
		t.Errorf("audit: %s", got)
	}
	if code := directoryEvent(t, s, "user.updated", "e2", scimBody("alice", "org-a", true, "alice2")); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code := directoryEvent(t, s, "user.updated", "e1", scimBody("alice", "org-a", true, "ally")); code != http.StatusNoContent || name() != "alice2 alice2@example.com" {
		t.Errorf("replay: %d, profile %q", code, name())
	}
	if !signedIn(s, cookie) {
		t.Error("a profile change ended alice's session")
	}
}

// Removals apply whatever the body's org: a KyIdentity bug that drops or garbles org_id never
// leaves a removed person signed in. Only an erasure naming the account's own org deletes data.
func TestTenantDirectoryRemovalsAlwaysApply(t *testing.T) {
	for _, c := range []struct {
		event, org string
		active     bool
		deleted    bool
	}{
		{"user.updated", "org-b", false, false},
		{"user.deactivated", "", false, false},
		{"user.deleted", "org-a", false, false}, // removal from the directory keeps the data
		{"user.erased", "org-b", false, false},
		{"user.erased", "org-a", false, true}, // Delete everything
	} {
		t.Run(c.event+" "+c.org, func(t *testing.T) {
			s, alice, cookie := directoryWorld(t)
			if code := directoryEvent(t, s, c.event, "e1", scimBody("alice", c.org, c.active, "alice")); code != http.StatusNoContent {
				t.Fatalf("%d", code)
			}
			if signedIn(s, cookie) {
				t.Error("still signed in")
			}
			u, err := s.store.Users().GetUserByID(context.Background(), alice.ID)
			switch {
			case c.deleted && err == nil:
				t.Errorf("not deleted: %+v", u)
			case !c.deleted && (err != nil || u.Status != "inactive"):
				t.Errorf("want kept and inactive: %+v %v", u, err)
			}
		})
	}
}

// An event refused before it was applied is forgotten by the replay guard, so KyIdentity's retry
// under the same event ID is applied rather than acknowledged as a duplicate.
func TestTenantDirectoryRetryAfterARefusalIsApplied(t *testing.T) {
	s, alice, cookie := directoryWorld(t)
	bad := scimBody("alice", "org-a", false, "alice")
	delete(bad, "active") // refused: an update must say whether the account is active
	if code := directoryEvent(t, s, "user.updated", "e1", bad); code != http.StatusBadRequest {
		t.Fatalf("refusal: %d", code)
	}
	if code := directoryEvent(t, s, "user.updated", "e1", scimBody("alice", "org-a", false, "alice")); code != http.StatusNoContent || signedIn(s, cookie) {
		t.Fatalf("retry: %d, signed in %v", code, signedIn(s, cookie))
	}
	if u, _ := s.store.Users().GetUserByID(context.Background(), alice.ID); u.Status != "inactive" {
		t.Errorf("the retry was not applied: %+v", u)
	}
}

// A replayed removal is applied again: a retry that raced a first delivery which then failed must
// not be acknowledged as done.
func TestTenantDirectoryReplayedRemovalApplies(t *testing.T) {
	s, alice, _ := directoryWorld(t)
	ctx := context.Background()
	if code := directoryEvent(t, s, "user.deactivated", "e1", scimBody("alice", "org-a", false, "alice")); code != http.StatusNoContent {
		t.Fatal(code)
	}
	alice.Status = "active" // as if the first delivery had not landed
	if err := s.store.Users().UpdateUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if code := directoryEvent(t, s, "user.deactivated", "e1", scimBody("alice", "org-a", false, "alice")); code != http.StatusNoContent {
		t.Fatalf("replay: %d", code)
	}
	if u, _ := s.store.Users().GetUserByID(ctx, alice.ID); u.Status != "inactive" {
		t.Errorf("a replayed removal was not applied: %+v", u)
	}
}

// A replayed active update still ends a stored administrator's sessions (the body cannot carry
// app roles), and never rewrites the profile.
func TestTenantDirectoryReplayedUpdateStillRevokesAnAdmin(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-staff": "active"})
	s.config.SSO.Enabled, s.config.SSO.KySignOnHMACSecret = true, directorySecret
	idp.extra, idp.roles = map[string]any{"org_id": "org-staff"}, []string{access.AdminAppRole}
	ctx := context.Background()
	first := ssoSession(ssoLogin(t, s, idp, "staffer", "sid-1"))
	staffer, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "staffer")
	if err != nil || staffer.Role != "admin" || first == nil {
		t.Fatalf("staffer: %+v %v", staffer, err)
	}
	if code := directoryEvent(t, s, "user.updated", "e1", scimBody("staffer", "org-staff", true, "first")); code != http.StatusNoContent || signedIn(s, first) {
		t.Fatalf("update: %d, signed in %v", code, signedIn(s, first))
	}
	second := ssoSession(ssoLogin(t, s, idp, "staffer", "sid-2")) // as if the first delivery had not landed
	if second == nil || !signedIn(s, second) {
		t.Fatal("second sign-in")
	}
	if err := s.store.Users().UpdateKySignOnProfile(ctx, staffer.ID, "newer", "newer@example.com"); err != nil {
		t.Fatal(err)
	}
	if code := directoryEvent(t, s, "user.updated", "e1", scimBody("staffer", "org-staff", true, "first")); code != http.StatusNoContent || signedIn(s, second) {
		t.Errorf("replay: %d, administrator still signed in %v", code, signedIn(s, second))
	}
	if u, _ := s.store.Users().GetUserByID(ctx, staffer.ID); u.DisplayName != "newer" {
		t.Errorf("a replay rewrote the profile: %+v", u)
	}
}

// An MFA reset ends sessions and keeps the account active; the directory never creates or
// activates an account; a bad signature is refused.
func TestTenantDirectoryNeverGrants(t *testing.T) {
	s, alice, cookie := directoryWorld(t)
	ctx := context.Background()
	if code := directoryEvent(t, s, "user.mfa_reset", "e1", scimBody("alice", "", true, "alice")); code != http.StatusNoContent || signedIn(s, cookie) {
		t.Fatalf("mfa reset: %d, signed in %v", code, signedIn(s, cookie))
	}
	if u, _ := s.store.Users().GetUserByID(ctx, alice.ID); u.Status != "active" {
		t.Errorf("mfa reset deactivated: %+v", u)
	}
	if code := directoryEvent(t, s, "user.created", "e2", scimBody("bob", "org-a", true, "bob")); code != http.StatusNoContent {
		t.Fatalf("unknown account: %d", code)
	}
	if _, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "bob"); err == nil {
		t.Error("the directory created an account")
	}
	directoryEvent(t, s, "user.deactivated", "e3", scimBody("alice", "org-a", false, "alice"))
	if code := directoryEvent(t, s, "user.updated", "e4", scimBody("alice", "org-a", true, "alice")); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if u, _ := s.store.Users().GetUserByID(ctx, alice.ID); u.Status != "inactive" {
		t.Errorf("the directory activated an account: %+v", u)
	}

	raw, _ := json.Marshal(scimBody("alice", "org-a", false, "alice"))
	h, _ := syncauth.Sign([]byte("another-secret-0123456789abcdefghij"), time.Now(), "user.deactivated", "e5", raw)
	r := httptest.NewRequest(http.MethodPost, "/api/sso/kysignon/sync", bytes.NewReader(raw))
	h.Apply(r)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bad signature: %d", w.Code)
	}
}

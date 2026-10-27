package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func (idp *signingIdP) logoutToken(t *testing.T, sub, sid string) string {
	claims := map[string]any{
		"iss": idp.URL, "aud": "kc", "sub": sub, "jti": sub + sid,
		"iat": time.Now().Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
	}
	if sid != "" {
		claims["sid"] = sid
	}
	return idp.sign(t, "logout+jwt", claims)
}

// ssoLogin runs the login and callback for sub/sid and returns the callback response.
func ssoLogin(t *testing.T, s *Server, idp *signingIdP, sub, sid string) *httptest.ResponseRecorder {
	t.Helper()
	req := idp.login(t, s, sub)
	idp.sid = sid
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func ssoSession(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

func signedIn(s *Server, c *http.Cookie) bool {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var me struct{ Authenticated bool }
	return json.Unmarshal(w.Body.Bytes(), &me) == nil && me.Authenticated
}

func postLogout(s *Server, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/sso/kysignon/backchannel-logout", strings.NewReader(url.Values{"logout_token": {token}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func TestBackchannelLogoutEndsTheSSOLogin(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	idp := newSigningIdP(t)
	idp.roles = nil
	if _, _, err := s.bindAccounts(ctx, kyidentityAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", idp.Client()))

	ended := ssoSession(ssoLogin(t, s, idp, "alice", "sid-a1"))
	kept := ssoSession(ssoLogin(t, s, idp, "alice", "sid-a2"))
	if ended == nil || kept == nil || !signedIn(s, ended) || !signedIn(s, kept) {
		t.Fatal("SSO login did not issue working sessions")
	}

	// An ID token is not a logout token, even signed by the provider.
	idToken := idp.sign(t, "JWT", map[string]any{"iss": idp.URL, "aud": "kc", "sub": "alice", "sid": "sid-a2", "nonce": "n", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
	if w := postLogout(s, idToken); w.Code != http.StatusBadRequest || !signedIn(s, kept) {
		t.Fatalf("ID token as logout token: %d", w.Code)
	}
	w := postLogout(s, idp.logoutToken(t, "alice", "sid-a1"))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("logout: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	if signedIn(s, ended) {
		t.Fatal("session survived its back-channel logout")
	}
	if !signedIn(s, kept) {
		t.Fatal("logout of one login ended another login of the same user")
	}
	if w := postLogout(s, idp.logoutToken(t, "alice", "sid-a1")); w.Code != http.StatusOK {
		t.Fatalf("replayed logout: %d", w.Code)
	}

	// The body is capped at 16 KiB: a token past the cap is never read.
	big := httptest.NewRequest(http.MethodPost, "/api/sso/kysignon/backchannel-logout", strings.NewReader("pad="+strings.Repeat("x", 16<<10)+"&logout_token="+idp.logoutToken(t, "alice", "sid-a2")))
	big.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, big)
	if w.Code != http.StatusBadRequest || !signedIn(s, kept) {
		t.Fatalf("oversized body: %d", w.Code)
	}

	// A logout that lands before the callback finishes fences that login.
	if w := postLogout(s, idp.logoutToken(t, "alice", "sid-late")); w.Code != http.StatusOK {
		t.Fatalf("early logout: %d", w.Code)
	}
	if w := ssoLogin(t, s, idp, "alice", "sid-late"); w.Code != http.StatusUnauthorized || ssoSession(w) != nil {
		t.Fatalf("callback revived an ended login: %d", w.Code)
	}

	// A subject-wide token ends that user's logins issued by then, and nobody else's. A KyIdentity
	// sub reaches the SCIM row the login adopted.
	bob := ssoSession(ssoLogin(t, s, idp, "bob", "sid-b1"))
	createUsers(t, s, &store.User{ID: "usr_dave", Username: "dave", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "dave", SSOIssuer: "kyidentity " + idp.URL})
	dave := ssoSession(ssoLogin(t, s, idp, "dave", "sid-d1"))
	if bob == nil || dave == nil {
		t.Fatal("SSO login did not issue sessions")
	}
	for _, sub := range []string{"alice", "dave"} {
		if w := postLogout(s, idp.logoutToken(t, sub, "")); w.Code != http.StatusOK {
			t.Fatalf("subject-wide logout of %s: %d", sub, w.Code)
		}
	}
	if signedIn(s, kept) || signedIn(s, dave) || !signedIn(s, bob) {
		t.Fatal("subject-wide logout ended the wrong sessions")
	}
	if w := postLogout(s, idp.logoutToken(t, "nobody", "")); w.Code != http.StatusOK {
		t.Fatalf("logout for an unknown subject: %d", w.Code)
	}

	// A login of an earlier provider, same sid and sub, is not this provider's to end.
	old := sso.NewProvider(sso.KindKyIdentity, "old", "https://old.example", "kc", "", nil)
	erin := &store.User{ID: "usr_erin", Username: "erin", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "erin", SSOIssuer: old.Binding}
	createUsers(t, s, erin)
	erinHash := crypto.SHA256Hex([]byte("erin-session"))
	if err := s.store.Sessions().CreateSession(ctx, &store.Session{TokenHash: erinHash, UserID: erin.ID, CreatedAt: time.Now().Add(-time.Minute).UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(), SSOSID: ssoSID(old, "sid-e1")}, erin.Credentials()); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{idp.logoutToken(t, "erin", "sid-e1"), idp.logoutToken(t, "erin", "")} {
		if w := postLogout(s, tok); w.Code != http.StatusOK {
			t.Fatalf("logout naming another provider's login: %d", w.Code)
		}
	}
	if _, err := s.store.Sessions().GetSession(ctx, erinHash); err != nil {
		t.Fatalf("another provider's session ended: %v", err)
	}

	recs, _, err := s.store.Audit().ListAuditRecords(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	audits := 0
	for _, r := range recs {
		if r.Action == "auth.sso_backchannel_logout" {
			audits++
		}
	}
	if audits != 6 {
		t.Fatalf("back-channel logout audits: %d, want 6", audits)
	}

	s.signin.Store(nil)
	if w := postLogout(s, idp.logoutToken(t, "bob", "sid-b1")); w.Code != http.StatusNotFound || !signedIn(s, bob) {
		t.Fatalf("logout with no provider: %d", w.Code)
	}
}

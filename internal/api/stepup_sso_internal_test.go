package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// An SSO session is dated from the token's auth_time, so a silent sign-in on a live provider
// session is no step-up. The step-up sign-in asks for fresh credentials and needs an auth_time
// from after it started (Q6).
func TestSSOStepUpNeedsAFreshAuthTime(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := newSigningIdP(t)
	idp.roles = nil
	if _, _, err := s.bindAccounts(context.Background(), kyidentityAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", idp.Client()))
	callback := func(stepUp bool, authTime time.Time) *httptest.ResponseRecorder {
		t.Helper()
		req := idp.login(t, s, "alice")
		if stepUp {
			req = startLoginAt(t, s, context.Background(), "/api/sso/kysignon/login?step_up=1")
			for _, c := range req.Cookies() {
				if strings.HasPrefix(c.Name, "ky_nonce_") {
					_, rest, _ := strings.Cut(c.Value, ".")
					idp.nonce, _, _ = strings.Cut(rest, ".")
				}
			}
		}
		idp.authTime = authTime
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		return w
	}
	steppedUp := func(w *httptest.ResponseRecorder) bool {
		t.Helper()
		c := ssoSession(w)
		if c == nil {
			t.Fatalf("no session: %d %s", w.Code, w.Body.String())
		}
		req := httptest.NewRequest(http.MethodPost, "/api/app-passwords", nil)
		req.AddCookie(c)
		return s.requireStepUp(httptest.NewRecorder(), req, "test")
	}
	hourAgo := time.Now().Add(-time.Hour)
	if steppedUp(callback(false, hourAgo)) {
		t.Fatal("a silent sign-in on an hour-old provider session passed step-up")
	}
	for name, at := range map[string]time.Time{"old auth_time": hourAgo, "no auth_time": {}} {
		if w := callback(true, at); w.Code != http.StatusUnauthorized || ssoSession(w) != nil {
			t.Fatalf("step-up with %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if !steppedUp(callback(true, time.Now())) {
		t.Fatal("a step-up with fresh credentials did not pass")
	}

	// The start time rides in the browser's cookie: back-dating it there, to let an hour-old
	// auth_time through, is refused.
	req := startLoginAt(t, s, context.Background(), "/api/sso/kysignon/login?step_up=1")
	forged := httptest.NewRequest(http.MethodGet, req.URL.String(), nil)
	for _, c := range req.Cookies() {
		if strings.HasPrefix(c.Name, "ky_nonce_") {
			parts := strings.Split(c.Value, ".") // id, nonce, start, mac
			idp.nonce = parts[1]
			parts[2] = strconv.FormatInt(hourAgo.Add(-time.Minute).Unix(), 10)
			c.Value = strings.Join(parts, ".")
		}
		forged.AddCookie(c)
	}
	idp.authTime = hourAgo
	w := httptest.NewRecorder()
	s.ServeHTTP(w, forged)
	if w.Code != http.StatusBadRequest || ssoSession(w) != nil {
		t.Fatalf("step-up with a back-dated start: %d %s", w.Code, w.Body.String())
	}
}

// A generic OIDC account's email follows the token at each sign-in (Q3): an account made with an
// unverified email loses it, and a verified one is kept.
func TestOIDCSignInKeepsOnlyAVerifiedEmail(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	idp := newSigningIdP(t)
	idp.roles = nil
	binding := sso.KindOIDC + " " + idp.URL
	if _, _, err := s.bindAccounts(ctx, oidcAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	s.signin.Store(sso.NewProvider(sso.KindOIDC, "Acme", idp.URL, "kc", "", idp.Client()))
	if err := s.store.Users().CreateUser(ctx, &store.User{ID: "usr_m", Username: "mallory", Email: "boss@example.com", Role: "user", Status: "active",
		SSOProvider: "oidc", SSOSubject: "mallory", SSOIssuer: binding}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		claims map[string]any
		want   string
	}{
		{map[string]any{"email": "boss@example.com"}, ""},
		{map[string]any{"email": "mallory@example.com", "email_verified": true}, "mallory@example.com"},
	} {
		idp.extra = tc.claims
		req := idp.login(t, s, "mallory")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if ssoSession(w) == nil {
			t.Fatalf("sign-in: %d %s", w.Code, w.Body.String())
		}
		if u, err := s.store.Users().GetUserByID(ctx, "usr_m"); err != nil || u.Email != tc.want {
			t.Fatalf("email after sign-in with %v: %q (%v), want %q", tc.claims, u.Email, err, tc.want)
		}
	}
}

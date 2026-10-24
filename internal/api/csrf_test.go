package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
)

// preSession is what a signed-out SPA does first: GET /api/auth/me, which hands out the
// pre-session cookie and the CSRF token bound to it.
func preSession(t *testing.T, srv *api.Server) ([]*http.Cookie, string) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	var cookies []*http.Cookie
	var token string
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case auth.PreSessionCookieName:
			if !c.HttpOnly {
				t.Error("pre-session cookie is readable by scripts")
			}
			cookies = append(cookies, c)
		case auth.CSRFCookieName:
			token = c.Value
			cookies = append(cookies, c)
		}
	}
	if len(cookies) != 2 || token == "" {
		t.Fatalf("anonymous /api/auth/me set %v, want a pre-session cookie and a CSRF token", cookies)
	}
	return cookies, token
}

// sessionToken is the ky_csrf value the server hands the session's own page on /api/auth/me.
func sessionToken(t *testing.T, srv *api.Server, session *http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(session)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CSRFCookieName && c.Value != "" {
			return c.Value
		}
	}
	t.Fatal("/api/auth/me did not hand the session its CSRF token")
	return ""
}

// createAppPassword is a state-changing session route; csrfCookie and header are what the
// browser sends alongside the session cookie.
func createAppPassword(srv *api.Server, session *http.Cookie, csrfCookie, header string) int {
	req := httptest.NewRequest(http.MethodPost, "/api/app-passwords", strings.NewReader(`{"label":"phone","scope":"dav"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(session)
	if csrfCookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrfCookie})
	}
	if header != "" {
		req.Header.Set(auth.HeaderCSRF, header)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Code
}

// The token is an HMAC of the session under the server secret: one session's token never
// authorizes another session, and a cookie/header pair an attacker made up or planted fails.
func TestCSRFTokenIsBoundToTheSession(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	bob := loginAs(t, srv, st, "bob", "user")
	aliceToken, bobToken := sessionToken(t, srv, alice), sessionToken(t, srv, bob)
	if aliceToken == bobToken {
		t.Fatal("two sessions got the same CSRF token")
	}

	for name, c := range map[string]struct{ cookie, header string }{
		"session A's token on session B":           {aliceToken, aliceToken},
		"planted double-submit pair":               {"attacker-chosen", "attacker-chosen"},
		"B's token in the cookie, forged header":   {bobToken, "attacker-chosen"},
		"missing header":                           {bobToken, ""},
		"missing cookie and header":                {"", ""},
		"token with a flipped character":           {bobToken, flip(bobToken)},
		"pre-session token on a session route":     {"", preSessionToken(t, srv)},
		"session token computed under another key": {"", auth.SessionCSRFToken("some-other-secret-of-32-characters", bob.Value)},
	} {
		if got := createAppPassword(srv, bob, c.cookie, c.header); got != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", name, got)
		}
	}
	// Only the header is checked: the session's own token works whatever ky_csrf holds.
	if got := createAppPassword(srv, bob, "stale", bobToken); got != http.StatusCreated && got != http.StatusOK {
		t.Fatalf("session B's own token: got %d", got)
	}
	if got := createAppPassword(srv, alice, "", aliceToken); got != http.StatusCreated && got != http.StatusOK {
		t.Fatalf("session A's own token: got %d", got)
	}
}

func preSessionToken(t *testing.T, srv *api.Server) string {
	_, token := preSession(t, srv)
	return token
}

func flip(s string) string {
	b := []byte(s)
	if b[0] == 'a' {
		b[0] = 'b'
	} else {
		b[0] = 'a'
	}
	return string(b)
}

// A browser's sign-in needs the token of its own pre-session cookie; a native client or script
// (no Origin, no Sec-Fetch-Site) cannot be made to forge one and signs in without.
func TestPreSessionCSRFProtectsSignIn(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	loginAs(t, srv, st, "carol", "user") // creates carol; this login is a script's, with no token
	body := `{"username":"carol","password":"SuperSecretPass123!"}`
	login := func(cookies []*http.Cookie, header string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost:8080")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		if header != "" {
			req.Header.Set(auth.HeaderCSRF, header)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}

	mine, myToken := preSession(t, srv)
	_, otherToken := preSession(t, srv)
	for name, c := range map[string]struct {
		cookies []*http.Cookie
		header  string
	}{
		"no token":                              {mine, ""},
		"another browser's pre-session token":   {mine, otherToken},
		"forged token":                          {mine, "attacker-chosen"},
		"token without its pre-session cookie":  {nil, myToken},
		"planted pre-session cookie, own token": {[]*http.Cookie{{Name: auth.PreSessionCookieName, Value: "planted"}}, "planted"},
	} {
		w := login(c.cookies, c.header)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", name, w.Code)
		}
		for _, ck := range w.Result().Cookies() {
			if ck.Name == auth.SessionCookieName {
				t.Errorf("%s: a session was issued", name)
			}
		}
	}

	w := login(mine, myToken)
	if w.Code != http.StatusOK {
		t.Fatalf("browser login with its pre-session token: %d %s", w.Code, w.Body.String())
	}
	// The session replaces the pre-session: its token is now bound to the new session.
	var session *http.Cookie
	var csrf string
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookieName:
			session = c
		case auth.CSRFCookieName:
			csrf = c.Value
		case auth.PreSessionCookieName:
			if c.Value != "" {
				t.Error("sign-in did not clear the pre-session cookie")
			}
		}
	}
	if session == nil || csrf != auth.SessionCSRFToken(testSessionSecret, session.Value) {
		t.Fatal("sign-in did not hand out the session-bound CSRF token")
	}
	if got := createAppPassword(srv, session, csrf, myToken); got != http.StatusForbidden {
		t.Errorf("pre-session token after sign-in: got %d, want 403", got)
	}

	// MFA steps are pre-session too.
	for _, path := range []string{"/api/auth/mfa/totp", "/api/auth/mfa/recovery-code"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"mfa_token":"x","code":"123456"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost:8080")
		for _, c := range mine {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s without a token: got %d, want 403", path, w.Code)
		}
	}
}

// Signing out needs the session's token, and hands the browser a fresh pre-session so the
// sign-in form after it works.
func TestLogoutHandsOutAPreSession(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "dave", "user")
	logout := func(csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
		req.AddCookie(session)
		if csrf {
			addCSRF(req, session)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	if w := logout(false); w.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF: got %d, want 403", w.Code)
	}
	sessionToken(t, srv, session) // still signed in
	var pre, csrf string
	for _, c := range logout(true).Result().Cookies() {
		switch c.Name {
		case auth.PreSessionCookieName:
			pre = c.Value
		case auth.CSRFCookieName:
			csrf = c.Value
		}
	}
	if pre == "" || csrf != auth.PreSessionCSRFToken(testSessionSecret, pre) {
		t.Fatalf("logout set pre-session %q and token %q", pre, csrf)
	}
}

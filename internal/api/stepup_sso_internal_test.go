package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/sso"
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
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestRequireStepUp(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	u := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	if err := s.store.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.sessions.IssueSession(ctx, httptest.NewRecorder(), httptest.NewRequest("POST", "/api/auth/login", nil), u)
	if err != nil {
		t.Fatal(err)
	}
	attempt := func(withSession bool) (bool, *httptest.ResponseRecorder) {
		r := httptest.NewRequest("POST", "/", nil)
		if withSession {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		return s.requireStepUp(w, r, "do this"), w
	}

	if ok, w := attempt(true); !ok || w.Body.Len() != 0 {
		t.Fatalf("fresh session: ok=%v body=%q, want true and nothing written", ok, w.Body.String())
	}

	old := stepUpWindow
	stepUpWindow = -time.Second // every session is older than this
	t.Cleanup(func() { stepUpWindow = old })
	ok, w := attempt(true)
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if ok || w.Code != http.StatusForbidden || body["code"] != "reauth_required" || body["error"] != "Sign in again to do this" {
		t.Fatalf("stale session: ok=%v %d %v", ok, w.Code, body)
	}

	if ok, w := attempt(false); ok || w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: ok=%v %d", ok, w.Code)
	}
}

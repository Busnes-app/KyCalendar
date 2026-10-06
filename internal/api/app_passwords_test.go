package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
)

func doJSON(t *testing.T, srv *api.Server, method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
	req.Header.Set(auth.HeaderCSRF, "test-csrf")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestAppPasswordLifecycle(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	w := doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "iPhone"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID, Label, Password string }
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Password == "" || created.Label != "iPhone" {
		t.Fatalf("created %+v", created)
	}

	w = do(t, srv, "GET", "/api/app-passwords", alice)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(created.Password)) || !bytes.Contains(w.Body.Bytes(), []byte("iPhone")) {
		t.Fatalf("list must not reveal the password: %s", w.Body.String())
	}

	if w = do(t, srv, "DELETE", "/api/app-passwords/"+created.ID, alice); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
}

func TestAppPasswordRules(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	if w := doJSON(t, srv, "POST", "/api/app-passwords", admin, map[string]string{"label": "x"}); w.Code != http.StatusForbidden {
		t.Fatalf("admin must be refused: %d", w.Code)
	}
	alice := loginAs(t, srv, st, "alice", "user")
	if w := doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "  "}); w.Code != http.StatusBadRequest {
		t.Fatalf("blank label: %d", w.Code)
	}
	for i := 0; i < 20; i++ {
		doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "d"})
	}
	if w := doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "21st"}); w.Code != http.StatusConflict {
		t.Fatalf("21st password: %d", w.Code)
	}
	bob := loginAs(t, srv, st, "bob", "user")
	list, _ := st.AppPasswords().ListByUser(t.Context(), "usr_alice")
	if w := do(t, srv, "DELETE", "/api/app-passwords/"+list[0].ID, bob); w.Code != http.StatusNotFound {
		t.Fatalf("bob deleting alice's password: %d", w.Code)
	}
}

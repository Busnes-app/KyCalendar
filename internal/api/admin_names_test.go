package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Admin-entered display names refuse invisible characters: format characters (zero-width
// space, bidi overrides) and line or paragraph separators render as nothing or reorder text,
// so two names that look identical could differ. The limit is 255 bytes and the message says so.
func TestAdminNamesRefuseInvisibleCharacters(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	_ = loginAs(t, srv, st, "alice", "user")
	w := call(t, srv, "POST", "/api/admin/groups", `{"display_name":"Crew"}`, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var crew struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &crew)

	for _, name := range []string{"Cr\u200bew", "Crew\u202eevil", "a\u2028b", "a\u2029b", "a\u00adb", "a\ufeffb"} {
		body, _ := json.Marshal(map[string]string{"display_name": name})
		for _, req := range []struct{ method, path string }{
			{"POST", "/api/admin/groups"},
			{"PATCH", "/api/admin/groups/" + crew.ID},
			{"PATCH", "/api/admin/users/usr_alice"},
		} {
			w := call(t, srv, req.method, req.path, string(body), admin)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "bytes") {
				t.Errorf("%s %s %q: %d %s, want 400 naming bytes", req.method, req.path, name, w.Code, w.Body.String())
			}
		}
		create, _ := json.Marshal(map[string]string{"username": "dora", "display_name": name})
		if w := call(t, srv, "POST", "/api/admin/users", string(create), admin); w.Code != http.StatusBadRequest {
			t.Errorf("create person %q: %d %s, want 400", name, w.Code, w.Body.String())
		}
	}
	// Ordinary non-ASCII stays allowed, up to 255 bytes.
	for _, name := range []string{"Équipe café", "チーム", strings.Repeat("é", 127)} {
		body, _ := json.Marshal(map[string]string{"display_name": name})
		if w := call(t, srv, "PATCH", "/api/admin/groups/"+crew.ID, string(body), admin); w.Code != http.StatusOK {
			t.Errorf("rename to %q: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/"+crew.ID, `{"display_name":"`+strings.Repeat("é", 128)+`"}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("256-byte name: %d, want 400", w.Code)
	}
}

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
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

// A name stored before the invisible-character rule never blocks an unrelated edit: an unchanged
// name is not revalidated, while a new one still is.
func TestStoredInvisibleNamesDoNotBlockOtherEdits(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	_ = loginAs(t, srv, st, "alice", "user")
	const legacy = "Ali\u200dce"
	u, _ := st.Users().GetUserByID(ctx, "usr_alice")
	u.DisplayName = legacy
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"email":"a@example.com"}`, `{"display_name":" ` + legacy + ` ","email":"b@example.com"}`} {
		if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", body, admin); w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_alice"); u.DisplayName != legacy || u.Email != "b@example.com" {
		t.Errorf("after the edits: %+v", u)
	}
	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"display_name":"Al\u200dice"}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("a changed invisible name: %d, want 400", w.Code)
	}

	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_old", DisplayName: "Cr\u200dew"}); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/grp_old", `{"display_name":"Cr\u200dew"}`, admin); w.Code != http.StatusOK {
		t.Errorf("unchanged group name: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/grp_old", `{"display_name":"C\u200drew"}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("changed invisible group name: %d, want 400", w.Code)
	}

	// The sign-in label: unchanged passes validation (discovery then refuses the loopback-free
	// .invalid issuer with 422), changed is 400.
	if err := st.Settings().SetSetting(ctx, sso.KeyDisplayName, "Ky\u200dId"); err != nil {
		t.Fatal(err)
	}
	label := func(l string) string {
		return `{"provider":"oidc","display_name":"` + l + `","issuer":"https://idp.invalid","client_id":"kc"}`
	}
	if w := call(t, srv, "POST", "/api/admin/signin/test", label("Ky\u200dId"), admin); w.Code == http.StatusBadRequest {
		t.Errorf("unchanged label: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "POST", "/api/admin/signin/test", label("K\u200dyId"), admin); w.Code != http.StatusBadRequest {
		t.Errorf("changed invisible label: %d, want 400", w.Code)
	}
}

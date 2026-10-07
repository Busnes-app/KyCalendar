package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// localRow is the local account SCIM must never see or change, with grants to prove it.
func localRow(t *testing.T, st store.Store) *store.User {
	t.Helper()
	ctx := context.Background()
	u := &store.User{ID: "usr_local", Username: "Root", DisplayName: "Root", Email: "root@example.com", PasswordHash: "h_root", Role: "admin", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_local", UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	return u
}

func assertLocalUntouched(t *testing.T, st store.Store, what string) {
	t.Helper()
	u, err := st.Users().GetUserByID(context.Background(), "usr_local")
	if err != nil {
		t.Fatalf("%s: local account gone: %v", what, err)
	}
	if u.Username != "Root" || u.DisplayName != "Root" || u.Email != "root@example.com" || u.PasswordHash != "h_root" || u.Role != "admin" || u.Status != "active" || u.SSOProvider != "local" {
		t.Fatalf("%s: local account changed: %+v", what, u)
	}
	if _, err := st.Sessions().GetSession(context.Background(), "tok_local"); err != nil {
		t.Fatalf("%s: local session revoked: %v", what, err)
	}
}

// SCIM provisions the IdP's people; a reconciling IdP that could see local accounts would
// demote, disable or delete the way back in when the IdP is gone.
func TestSCIMNeverSeesLocalUsers(t *testing.T) {
	h, st, token := scimWithStore(t)
	localRow(t, st)
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_synced", Username: "synced", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/scim/v2/Users", "/scim/v2/Users?filter=" + url.QueryEscape(`userName eq "root"`)} {
		w := scimDo(t, h, token, "GET", path, nil)
		var page struct {
			TotalResults int `json:"totalResults"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "usr_local") {
			t.Errorf("%s: %d %s, want the local account omitted", path, w.Code, w.Body.String())
		}
		want := 1
		if strings.Contains(path, "filter") {
			want = 0
		}
		if page.TotalResults != want {
			t.Errorf("%s: total %d, want %d (local accounts not counted)", path, page.TotalResults, want)
		}
	}
	for _, tc := range []struct {
		method string
		body   any
	}{
		{"GET", nil},
		{"PUT", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "Root", "active": false}},
		{"PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}}}},
		{"PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "remove", "path": "roles"}}}},
		{"DELETE", nil},
	} {
		if w := scimDo(t, h, token, tc.method, "/scim/v2/Users/usr_local", tc.body); w.Code != http.StatusNotFound {
			t.Errorf("%s on a local account: %d %s, want 404", tc.method, w.Code, w.Body.String())
		}
		assertLocalUntouched(t, st, tc.method)
	}
}

// A SCIM create never takes a local account's name, in any case.
func TestSCIMCreateCollidingWithALocalNameIsAConflict(t *testing.T) {
	h, st, token := scimWithStore(t)
	localRow(t, st)
	w := scimDo(t, h, token, "POST", "/scim/v2/Users", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "rOOT", "active": true})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "uniqueness") {
		t.Fatalf("create over a local name: %d %s, want 409 uniqueness", w.Code, w.Body.String())
	}
	assertLocalUntouched(t, st, "create")
}

// A SCIM group can never gain a local member, and SCIM neither reports nor removes a local
// membership a SCIM group already holds.
func TestSCIMGroupMembersNeverIncludeLocalUsers(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	localRow(t, st)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_synced", Username: "synced", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	members := func(ids ...string) []map[string]any {
		out := []map[string]any{}
		for _, id := range ids {
			out = append(out, map[string]any{"value": id})
		}
		return out
	}
	if w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Sneaky", "members": members("usr_synced", "usr_local")}); w.Code != http.StatusBadRequest {
		t.Errorf("create with a local member: %d %s, want 400", w.Code, w.Body.String())
	}
	if _, err := st.Groups().GetGroupByName(ctx, "Sneaky"); err == nil {
		t.Error("a refused create left its group behind")
	}

	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "members": members("usr_synced")})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var crew struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &crew)
	// A membership from before this rule: SCIM must not report it, nor remove it.
	if err := st.Groups().AddGroupMember(ctx, crew.ID, "usr_local"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/scim/v2/Groups/" + crew.ID, "/scim/v2/Groups"} {
		if w := scimDo(t, h, token, "GET", path, nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "usr_local") || !strings.Contains(w.Body.String(), "usr_synced") {
			t.Errorf("GET %s: %d %s, want usr_synced only", path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		name, method string
		body         any
	}{
		{"put adding local", "PUT", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "members": members("usr_synced", "usr_local")}},
		{"patch adding local", "PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "add", "path": "members", "value": members("usr_local")}}}},
		{"put unknown", "PUT", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "members": members("usr_nobody")}},
	} {
		if w := scimDo(t, h, token, tc.method, "/scim/v2/Groups/"+crew.ID, tc.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", tc.name, w.Code, w.Body.String())
		}
	}
	// Replacing the members with none removes the SCIM member and leaves the local one.
	if w := scimDo(t, h, token, "PUT", "/scim/v2/Groups/"+crew.ID, map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "members": members()}); w.Code != http.StatusOK {
		t.Fatalf("empty replace: %d %s", w.Code, w.Body.String())
	}
	g, err := st.Groups().GetGroupByID(ctx, crew.ID)
	if err != nil || len(g.Members) != 1 || g.Members[0] != "usr_local" {
		t.Fatalf("members after the empty replace: %+v %v, want the local one kept", g, err)
	}
	assertLocalUntouched(t, st, "groups")
}

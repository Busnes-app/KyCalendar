package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// unbound is SCIM's scope on an instance with no sign-in binding.
var unbound = store.SCIMScope{Providers: sso.AccountProviders(sso.KindKyIdentity)}

// SCIM reaches only the accounts it could create: KyIdentity's providers under the current
// binding. An OIDC account and one of an earlier binding are another directory's: invisible and
// untouchable by every verb, and never a SCIM group member.
func TestSCIMNeverReachesAnotherBinding(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	bound := "kyidentity https://a.example"
	if err := st.Settings().SetSetting(ctx, sso.KeyBound, bound); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_in", Username: "in", Role: "user", Status: "active", SSOProvider: "scim", SSOIssuer: bound},
		{ID: "usr_oidc", Username: "oidc", Role: "admin", Status: "active", SSOProvider: "oidc", SSOSubject: "o1", SSOIssuer: "oidc https://o.example"},
		{ID: "usr_old", Username: "old", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "k1", SSOIssuer: "kyidentity https://old.example"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	untouched := func(what string) {
		t.Helper()
		for _, id := range []string{"usr_oidc", "usr_old"} {
			u, err := st.Users().GetUserByID(ctx, id)
			if err != nil || u.Role != "admin" || u.Status != "active" || u.Username != strings.TrimPrefix(id, "usr_") {
				t.Fatalf("%s changed %s: %+v %v", what, id, u, err)
			}
		}
	}

	w := scimDo(t, h, token, "GET", "/scim/v2/Users", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "usr_oidc") || strings.Contains(w.Body.String(), "usr_old") || !strings.Contains(w.Body.String(), "usr_in") {
		t.Fatalf("list: %d %s, want usr_in only", w.Code, w.Body.String())
	}
	for _, id := range []string{"usr_oidc", "usr_old"} {
		for _, tc := range []struct {
			method string
			body   any
		}{
			{"GET", nil},
			{"PUT", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "taken", "active": false}},
			{"PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}}}},
			{"DELETE", nil},
		} {
			if w := scimDo(t, h, token, tc.method, "/scim/v2/Users/"+id, tc.body); w.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d %s, want 404", tc.method, id, w.Code, w.Body.String())
			}
			untouched(tc.method + " " + id)
		}
	}

	for _, id := range []string{"usr_oidc", "usr_old"} {
		w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Grab " + id, "members": memberList("usr_in", id)})
		if w.Code != http.StatusBadRequest {
			t.Errorf("create with %s: %d %s, want 400", id, w.Code, w.Body.String())
		}
	}
	w = scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "members": memberList("usr_in")})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var g struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	for _, body := range []map[string]any{
		{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "members": memberList("usr_in", "usr_oidc")},
		patchOps(map[string]any{"op": "add", "path": "members", "value": memberList("usr_old")}),
	} {
		method := "PUT"
		if _, ok := body["Operations"]; ok {
			method = "PATCH"
		}
		if w := scimDo(t, h, token, method, "/scim/v2/Groups/"+g.ID, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s adding another binding's account: %d %s, want 400", method, w.Code, w.Body.String())
		}
	}
	if got := storedMembers(t, st, g.ID); !slices.Equal(got, []string{"usr_in"}) {
		t.Fatalf("members %v, want usr_in only", got)
	}
	untouched("groups")
}

// A non-boolean active (Entra ID's legacy "False") never reaches the handler: the library refuses
// it as invalidValue on every write and the account keeps its status, so a failed deprovisioning
// is an error the IdP sees, never a silent no-op.
func TestSCIMActiveMustBeABoolean(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_e", Username: "e", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/scim/v2/Users/usr_e", patchOps(map[string]any{"op": "replace", "path": "active", "value": "False"})},
		{"PATCH", "/scim/v2/Users/usr_e", patchOps(map[string]any{"op": "replace", "value": map[string]any{"active": "False"}})},
		{"PUT", "/scim/v2/Users/usr_e", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "e", "active": "False"}},
		{"POST", "/scim/v2/Users", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "f", "active": "False"}},
	} {
		w := scimDo(t, h, token, tc.method, tc.path, tc.body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalidValue") {
			t.Errorf("%s %v: %d %s, want 400 invalidValue", tc.method, tc.body, w.Code, w.Body.String())
		}
	}
	if u, err := st.Users().GetUserByID(ctx, "usr_e"); err != nil || u.Status != "active" {
		t.Fatalf("after refused writes: %+v %v", u, err)
	}
	if _, err := st.Users().GetUserByUsername(ctx, "f"); err == nil {
		t.Fatal("a refused create stored the account")
	}
}

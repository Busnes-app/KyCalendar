package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func scimWithStore(t *testing.T) (http.Handler, store.Store, string) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-groups-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	return srv.AuthMiddleware(mux), st, token
}

// A reconciling IdP lists every group it can see and deletes what it does not know. Local
// groups must be invisible to it, or one sync would delete them and their calendar access.
func TestSCIMGroupsNeverTouchLocalGroups(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_local", DisplayName: "Local crew"}); err != nil {
		t.Fatal(err)
	}
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Synced crew"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if g, err := st.Groups().GetGroupByID(ctx, created.ID); err != nil || g.Source != store.GroupSourceSCIM {
		t.Fatalf("SCIM-created group: %+v %v, want source scim", g, err)
	}

	list := scimDo(t, h, token, "GET", "/scim/v2/Groups", nil).Body.String()
	if strings.Contains(list, "grp_local") || !strings.Contains(list, created.ID) {
		t.Fatalf("SCIM list must hold only SCIM groups: %s", list)
	}
	for _, tc := range []struct {
		method string
		body   any
	}{
		{"GET", nil},
		{"PUT", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Hijacked"}},
		{"PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "displayName", "value": "Hijacked"}}}},
		{"DELETE", nil},
	} {
		if w := scimDo(t, h, token, tc.method, "/scim/v2/Groups/grp_local", tc.body); w.Code != http.StatusNotFound {
			t.Errorf("%s on a local group: %d %s, want 404", tc.method, w.Code, w.Body.String())
		}
	}
	if g, err := st.Groups().GetGroupByID(ctx, "grp_local"); err != nil || g.DisplayName != "Local crew" {
		t.Fatalf("local group changed through SCIM: %+v %v", g, err)
	}
}

// A SCIM create whose name a local group holds (in any case) fails with a conflict and leaves a
// flag the Groups screen shows; the local group is untouched.
func TestSCIMGroupNameClashFlagsTheLocalGroup(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_local", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "crew"})
	if w.Code != http.StatusConflict {
		t.Fatalf("clashing create: %d %s, want 409", w.Code, w.Body.String())
	}
	if v, err := st.Settings().GetSetting(ctx, scim.ConflictKey("Crew")); err != nil || v == "" {
		t.Fatalf("clash not flagged: %q %v", v, err)
	}
	if g, _ := st.Groups().GetGroupByID(ctx, "grp_local"); g.Source != store.GroupSourceLocal || g.DisplayName != "Crew" {
		t.Fatalf("local group changed: %+v", g)
	}
}

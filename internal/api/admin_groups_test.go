package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type adminGroup struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Source        string `json:"source"`
	MemberCount   int    `json:"member_count"`
	CalendarCount int    `json:"calendar_count"`
	SCIMConflict  bool   `json:"scim_conflict"`
}

func listAdminGroups(t *testing.T, srv *api.Server, admin *http.Cookie) map[string]adminGroup {
	t.Helper()
	w := call(t, srv, "GET", "/api/admin/groups", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("list groups: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Groups []adminGroup `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]adminGroup{}
	for _, g := range body.Groups {
		out[g.DisplayName] = g
	}
	return out
}

// auditDetails returns the details of every audit row with action.
func auditDetails(t *testing.T, st store.Store, action string) []string {
	t.Helper()
	var out []string
	for _, r := range auditRows(t, st, action) {
		out = append(out, r.Details)
	}
	return out
}

func codeOf(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Code
}

func TestAdminGroupsCreateListRename(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")

	w := call(t, srv, "POST", "/api/admin/groups", `{"display_name":"  Crew "}`, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var crew adminGroup
	_ = json.Unmarshal(w.Body.Bytes(), &crew)
	if crew.DisplayName != "Crew" || crew.Source != "local" {
		t.Fatalf("created %+v, want trimmed local group", crew)
	}
	if w := call(t, srv, "POST", "/api/admin/groups", `{"display_name":"crew"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Fatalf("case twin: %d %s, want 409 name_taken", w.Code, w.Body.String())
	}
	for _, bad := range []string{`{"display_name":"   "}`, `{"display_name":"a\u0007b"}`, `{"display_name":"` + strings.Repeat("x", 256) + `"}`} {
		if w := call(t, srv, "POST", "/api/admin/groups", bad, admin); w.Code != http.StatusBadRequest {
			t.Errorf("bad name %s: %d, want 400", bad, w.Code)
		}
	}
	if len(auditDetails(t, st, "admin.group_create")) != 1 {
		t.Error("want one admin.group_create audit row")
	}

	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_bob", Username: "bob", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s-bob"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_scim", DisplayName: "Synced", Source: store.GroupSourceSCIM}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, "grp_scim", "usr_bob"); err != nil {
		t.Fatal(err)
	}
	cal := groupCalendar(t, st, "Rota")
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: crew.ID, Role: "reader"}); err != nil {
		t.Fatal(err)
	}
	groups := listAdminGroups(t, srv, admin)
	if g := groups["Crew"]; g.Source != "local" || g.MemberCount != 0 || g.CalendarCount != 1 {
		t.Errorf("Crew in list: %+v", g)
	}
	if g := groups["Synced"]; g.Source != "scim" || g.MemberCount != 1 || g.CalendarCount != 0 {
		t.Errorf("Synced in list: %+v", g)
	}

	w = call(t, srv, "GET", "/api/admin/groups/grp_scim", "", admin)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"username":"bob"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"source":"scim"`)) {
		t.Fatalf("SCIM group detail: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/admin/groups/grp_missing", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("missing group: %d, want 404", w.Code)
	}

	if w := call(t, srv, "PATCH", "/api/admin/groups/"+crew.ID, `{"display_name":"Crew 2"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/"+crew.ID, `{"display_name":"SYNCED"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Errorf("rename onto a case twin: %d %s, want 409 name_taken", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/grp_scim", `{"display_name":"Mine now"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
		t.Errorf("rename a SCIM group: %d %s, want 409 managed_externally", w.Code, w.Body.String())
	}
	if d := auditDetails(t, st, "admin.group_rename"); len(d) != 1 || !strings.Contains(d[0], `to="Crew 2"`) {
		t.Errorf("rename audit: %v", d)
	}
}

// A SCIM clash flag clears when the local group takes a different name, not on a case-only
// rename, which still clashes.
func TestAdminGroupsRenameClearsSCIMConflict(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_crew", DisplayName: "Crew", Source: store.GroupSourceLocal}); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().SetSetting(ctx, scim.ConflictKey("Crew"), "2026-10-07T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if g := listAdminGroups(t, srv, admin)["Crew"]; !g.SCIMConflict {
		t.Fatalf("Crew should be flagged: %+v", g)
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/grp_crew", `{"display_name":"CREW"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("case-only rename: %d %s", w.Code, w.Body.String())
	}
	if g := listAdminGroups(t, srv, admin)["CREW"]; !g.SCIMConflict {
		t.Errorf("case-only rename cleared the flag: %+v", g)
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/grp_crew", `{"display_name":"Crew local"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if v, _ := st.Settings().GetSetting(ctx, scim.ConflictKey("Crew")); v != "" {
		t.Errorf("flag survived a rename to a different name: %q", v)
	}
	if g := listAdminGroups(t, srv, admin)["Crew local"]; g.SCIMConflict {
		t.Errorf("renamed group still flagged: %+v", g)
	}
}

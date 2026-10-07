package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestAdminDeleteGroup(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_crew", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_scim", DisplayName: "Synced", Source: store.GroupSourceSCIM}); err != nil {
		t.Fatal(err)
	}
	cal := groupCalendar(t, st, "Rota")
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: "grp_crew", Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().SetSetting(ctx, scim.ConflictKey("Crew"), "2026-10-07T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if !listAdminGroups(t, srv, admin)["Crew"].SCIMConflict {
		t.Fatal("a SCIM clash on Crew is not flagged in the list")
	}

	restore := api.SetStepUpWindowForTest(0)
	w := call(t, srv, "DELETE", "/api/admin/groups/grp_crew", "", admin)
	restore()
	if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
		t.Fatalf("stale session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Groups().GetGroupByID(ctx, "grp_crew"); err != nil {
		t.Fatalf("a refused delete removed the group: %v", err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/groups/grp_scim", "", admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
		t.Errorf("delete a SCIM group: %d %s, want 409 managed_externally", w.Code, w.Body.String())
	}

	if w := call(t, srv, "DELETE", "/api/admin/groups/grp_crew", "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Calendars().GetCalendarByID(ctx, cal.ID); err != nil {
		t.Fatalf("the group calendar must survive its group: %v", err)
	}
	if gs, _ := st.Calendars().ListGrants(ctx, cal.ID); len(gs) != 0 {
		t.Fatalf("grants survived the group: %+v", gs)
	}
	if _, err := st.Settings().GetSetting(ctx, scim.ConflictKey("Crew")); err == nil {
		t.Error("the SCIM clash flag outlived the local group")
	}
	if d := auditDetails(t, st, "admin.group_delete"); len(d) != 1 || !strings.Contains(d[0], "calendars=1") {
		t.Errorf("delete audit: %v", d)
	}
}

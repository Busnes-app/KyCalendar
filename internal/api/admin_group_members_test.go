package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestAdminGroupMembers(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	bob := loginAs(t, srv, st, "bob", "user")
	_ = loginAs(t, srv, st, "other", "admin")
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_gone", Username: "gone", Role: "user", Status: "inactive", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_crew", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_scim", DisplayName: "Synced", Source: store.GroupSourceSCIM}); err != nil {
		t.Fatal(err)
	}
	cal := groupCalendar(t, st, "Rota")
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: "grp_crew", Role: "reader"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // idempotent
		if w := call(t, srv, "PUT", "/api/admin/groups/grp_crew/members/usr_bob", "", admin); w.Code != http.StatusNoContent {
			t.Fatalf("add bob (%d): %d %s", i, w.Code, w.Body.String())
		}
	}
	if n := len(auditDetails(t, st, "admin.group_member_add")); n != 1 {
		t.Errorf("repeated add audited %d times, want once", n)
	}
	if w := call(t, srv, "GET", "/api/calendars", "", bob); !bytes.Contains(w.Body.Bytes(), []byte(cal.ID)) {
		t.Fatalf("member does not see the group calendar: %s", w.Body.String())
	}

	for _, tc := range []struct{ path, code string }{
		{"/api/admin/groups/grp_crew/members/usr_other", "admin_member"},
		{"/api/admin/groups/grp_crew/members/usr_gone", "inactive_member"},
		{"/api/admin/groups/grp_scim/members/usr_bob", "managed_externally"},
	} {
		if w := call(t, srv, "PUT", tc.path, "", admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != tc.code {
			t.Errorf("PUT %s: %d %s, want 409 %s", tc.path, w.Code, w.Body.String(), tc.code)
		}
	}
	if w := call(t, srv, "PUT", "/api/admin/groups/grp_crew/members/usr_nobody", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("unknown person: %d, want 404", w.Code)
	}

	for i := 0; i < 2; i++ {
		if w := call(t, srv, "DELETE", "/api/admin/groups/grp_crew/members/usr_bob", "", admin); w.Code != http.StatusNoContent {
			t.Fatalf("remove bob (%d): %d %s", i, w.Code, w.Body.String())
		}
	}
	// Access is resolved live: the calendar is gone, the session is not.
	w := call(t, srv, "GET", "/api/calendars", "", bob)
	if w.Code != http.StatusOK || bytes.Contains(w.Body.Bytes(), []byte(cal.ID)) {
		t.Fatalf("after removal: %d %s", w.Code, w.Body.String())
	}
	if n := len(auditDetails(t, st, "admin.group_member_remove")); n != 1 {
		t.Errorf("repeated remove audited %d times, want once", n)
	}
}

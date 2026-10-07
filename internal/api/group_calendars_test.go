package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func call(t *testing.T, srv *api.Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
		req.Header.Set(auth.HeaderCSRF, "test-csrf")
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func audited(t *testing.T, st store.Store, action string) bool {
	t.Helper()
	recs, _, err := st.Audit().ListAuditRecords(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Action == action {
			return true
		}
	}
	return false
}

func TestAdminCreatesGroupCalendarAndGrants(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	bobTok := davUser(t, st, "bob", "user")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_team", DisplayName: "Team"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, "grp_team", "usr_bob"); err != nil {
		t.Fatal(err)
	}

	w := call(t, srv, "POST", "/api/admin/calendars", `{"name":"Rota","color":"#00aa11"}`, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = call(t, srv, "PUT", "/api/calendars/"+created.ID+"/grants/grp_team", `{"role":"reader"}`, admin)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"group_name":"Team"`)) {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/admin/calendars", "", admin); !bytes.Contains(w.Body.Bytes(), []byte(`"role":"reader"`)) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	if r := rawDAV(t, ts, "PROPFIND", "/dav/usr_bob/calendars/_"+created.ID+"/", "bob", bobTok, privBody, map[string]string{"Depth": "0", "Content-Type": "application/xml"}); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("the granted member cannot reach the calendar: %d", r.StatusCode)
	}
	for _, action := range []string{"admin.calendar_create", "calendar.grant_set"} {
		if !audited(t, st, action) {
			t.Errorf("no %s audit row", action)
		}
	}
}

func TestCreateGroupCalendarValidates(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	for _, body := range []string{`{"name":"  "}`, `{"name":"x","color":"red"}`, `not json`} {
		if w := call(t, srv, "POST", "/api/admin/calendars", body, admin); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
}

func TestManagerChangesGrantsOthersCannot(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	cal := groupCalendar(t, st, "Team")
	carol := loginAs(t, srv, st, "carol", "user")
	dave := loginAs(t, srv, st, "dave", "user")
	erin := loginAs(t, srv, st, "erin", "user")
	admin := loginAs(t, srv, st, "root", "admin")
	grantRole(t, st, cal, "manager", "usr_carol")
	grantRole(t, st, cal, "editor", "usr_dave")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_new", DisplayName: "New"}); err != nil {
		t.Fatal(err)
	}
	path := "/api/calendars/" + cal.ID + "/grants/grp_new"
	for who, want := range map[string]struct {
		c    *http.Cookie
		code int
	}{"manager": {carol, 200}, "editor": {dave, 403}, "nonmember": {erin, 404}, "admin": {admin, 200}} {
		if w := call(t, srv, "PUT", path, `{"role":"reader"}`, want.c); w.Code != want.code {
			t.Errorf("%s: %d, want %d: %s", who, w.Code, want.code, w.Body.String())
		}
	}
	if w := call(t, srv, "PUT", path, `{"role":"owner"}`, carol); w.Code != http.StatusBadRequest {
		t.Errorf("owner role: %d, want 400", w.Code)
	}
	if w := call(t, srv, "PUT", "/api/calendars/"+cal.ID+"/grants/grp_missing", `{"role":"reader"}`, carol); w.Code != http.StatusNotFound {
		t.Errorf("missing group: %d, want 404", w.Code)
	}
}

func TestDeleteGroupCalendarNeedsRecentSignIn(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cal := groupCalendar(t, st, "Doomed")
	admin := loginAs(t, srv, st, "root", "admin")
	restore := api.SetStepUpWindowForTest(0)
	w := call(t, srv, "DELETE", "/api/admin/calendars/"+cal.ID, "", admin)
	restore()
	if w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte(`"reauth_required"`)) {
		t.Fatalf("stale session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Calendars().GetCalendarByID(context.Background(), cal.ID); err != nil {
		t.Fatalf("a refused delete removed the calendar: %v", err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/calendars/"+cal.ID, "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("fresh session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Calendars().GetCalendarByID(context.Background(), cal.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("calendar survived: %v", err)
	}
	if !audited(t, st, "admin.calendar_delete") {
		t.Error("no admin.calendar_delete audit row")
	}
}

func TestGroupDeletionLeavesCalendarUnassigned(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	cal := groupCalendar(t, st, "Team")
	tok := davUser(t, st, "bob", "user")
	grantRole(t, st, cal, "reader", "usr_bob")
	admin := loginAs(t, srv, st, "root", "admin")
	if err := st.Groups().DeleteGroup(ctx, "grp_reader_"+cal.ID); err != nil {
		t.Fatal(err)
	}
	w := call(t, srv, "GET", "/api/admin/calendars", "", admin)
	if !bytes.Contains(w.Body.Bytes(), []byte(cal.ID)) || !bytes.Contains(w.Body.Bytes(), []byte(`"grants":[]`)) {
		t.Fatalf("calendar should be listed with no grants: %s", w.Body.String())
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	if r := rawDAV(t, ts, "PROPFIND", "/dav/usr_bob/calendars/_"+cal.ID+"/", "bob", tok, privBody, map[string]string{"Depth": "0", "Content-Type": "application/xml"}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("former member: %d, want 404", r.StatusCode)
	}
}

func personalCalendar(t *testing.T, st store.Store, userID string) *store.Calendar {
	t.Helper()
	c := &store.Calendar{ID: "cal_" + userID + "_p", OwnerKind: "user", OwnerID: userID, Slug: "mine", Name: "Mine"}
	if err := st.Calendars().CreateCalendar(context.Background(), c, 0); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPersonalCalendarsAreNotGroupRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	davUser(t, st, "bob", "user")
	p := personalCalendar(t, st, "usr_bob")
	if err := st.Groups().CreateGroup(context.Background(), &store.Group{ID: "grp_x", DisplayName: "X"}); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/calendars/"+p.ID, "", admin); w.Code != http.StatusNotFound {
		t.Errorf("delete personal: %d, want 404", w.Code)
	}
	if _, err := st.Calendars().GetCalendarByID(context.Background(), p.ID); err != nil {
		t.Errorf("personal calendar removed: %v", err)
	}
	if w := call(t, srv, "PUT", "/api/calendars/"+p.ID+"/grants/grp_x", `{"role":"reader"}`, admin); w.Code != http.StatusNotFound {
		t.Errorf("grant on personal: %d, want 404", w.Code)
	}
}

func TestReaderCannotGrantManagerCanListAndRemove(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cal := groupCalendar(t, st, "Team")
	rita := loginAs(t, srv, st, "rita", "user")
	carol := loginAs(t, srv, st, "carol", "user")
	grantRole(t, st, cal, "reader", "usr_rita")
	grantRole(t, st, cal, "manager", "usr_carol")
	base := "/api/calendars/" + cal.ID + "/grants"
	if w := call(t, srv, "PUT", base+"/grp_manager_"+cal.ID, `{"role":"reader"}`, rita); w.Code != http.StatusForbidden {
		t.Errorf("reader PUT: %d, want 403", w.Code)
	}
	w := call(t, srv, "GET", base, "", carol)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"role":"reader"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"role":"manager"`)) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		if w := call(t, srv, "DELETE", base+"/grp_reader_"+cal.ID, "", carol); w.Code != http.StatusOK {
			t.Fatalf("delete %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if w := call(t, srv, "GET", base, "", carol); bytes.Contains(w.Body.Bytes(), []byte(`"role":"reader"`)) {
		t.Errorf("grant survived: %s", w.Body.String())
	}
}

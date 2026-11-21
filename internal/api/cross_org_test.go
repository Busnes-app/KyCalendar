package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// addOrg inserts org id into cfg's database directly: orgs come from KyIdentity in
// multi-tenant mode (tenancy PR 4), so the store has no writer for them yet.
func addOrg(t *testing.T, cfg *config.Config, id string) {
	t.Helper()
	driver, q := "sqlite", `INSERT INTO orgs (id, status) VALUES (?, 'active')`
	if cfg.Database.Driver == "postgres" {
		driver, q = "pgx", `INSERT INTO orgs (id, status) VALUES ($1, 'active')`
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, id); err != nil {
		t.Fatal(err)
	}
}

// TestCrossOrgMatrix is the tenancy spec's cross-org test, area by area (tenancy PR 2): every
// route given another org's ID answers byte for byte as for an ID that exists nowhere, and
// writes nothing. This part covers users, groups, memberships and grants.
func TestCrossOrgMatrix(t *testing.T) {
	ctx := context.Background()
	srv, st, cfg := setupTestServer(t)
	const b = "org-b"
	addOrg(t, cfg, b)

	manager := loginAs(t, srv, st, "manager", "user")
	admin := loginAs(t, srv, st, "admin", "admin")
	for _, g := range []*store.Group{{ID: "grp_a", DisplayName: "A"}, {ID: "grp_b", DisplayName: "B", OrgID: b}} {
		if err := st.Groups().CreateGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_b", Username: "b", Role: "user", Status: "active", SSOProvider: "local", OrgID: b}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g", Slug: "g", Name: "G"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_b", OwnerKind: "user", OwnerID: "usr_b", Slug: "default", Name: "B"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.Bookings().CreateType(ctx, &store.BookingType{ID: "bt_b", UserID: "usr_b", Slug: strings.Repeat("b", 32), Title: "B", Zone: "UTC", CalendarID: "cal_b", Windows: "[]", DurationMin: 30}, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Tasks().CreateView(ctx, &store.TaskView{ID: "view_b", UserID: "usr_b", Name: "B", Query: "b"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TaskGoals().CreateGoal(ctx, &store.TaskGoal{ID: "goal_b", UserID: "usr_b", Title: "B", Period: "week", Starts: "2026-11-16", Ends: "2026-11-22"}, 1); err != nil {
		t.Fatal(err)
	}
	a := st.Org(store.DefaultOrg)
	if err := a.AddGroupMember(ctx, "grp_a", "usr_manager"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_a", Role: "manager"}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, method, other, missing, body string
		as                                 *http.Cookie
	}{
		{"grant to another org's group", "PUT", "/api/calendars/cal_g/grants/grp_b", "/api/calendars/cal_g/grants/grp_none", `{"role":"reader"}`, manager},
		{"read another org's group", "GET", "/api/admin/groups/grp_b", "/api/admin/groups/grp_none", "", admin},
		{"add another org's person", "PUT", "/api/admin/groups/grp_a/members/usr_b", "/api/admin/groups/grp_a/members/usr_none", "", admin},
		{"another org's calendar's grants", "GET", "/api/calendars/cal_b/grants", "/api/calendars/cal_none/grants", "", manager},
		{"rename another org's calendar", "PATCH", "/api/calendars/cal_b", "/api/calendars/cal_none", `{"name":"X"}`, manager},
		{"delete another org's calendar", "DELETE", "/api/calendars/cal_b", "/api/calendars/cal_none", "", manager},
		{"edit another org's booking type", "PUT", "/api/booking-types/bt_b", "/api/booking-types/bt_none", `{"title":"X","zone":"UTC","duration_min":30,"windows":[],"calendar_id":"cal_b"}`, manager},
		{"rotate another org's booking link", "POST", "/api/booking-types/bt_b/rotate", "/api/booking-types/bt_none/rotate", "", manager},
		{"delete another org's booking type", "DELETE", "/api/booking-types/bt_b", "/api/booking-types/bt_none", "", manager},
		{"edit another org's task view", "PUT", "/api/tasks/views/view_b", "/api/tasks/views/view_none", `{"name":"X","query":{"q":"x"}}`, manager},
		{"delete another org's task view", "DELETE", "/api/tasks/views/view_b", "/api/tasks/views/view_none", "", manager},
		{"edit another org's goal", "PUT", "/api/tasks/goals/goal_b", "/api/tasks/goals/goal_none", `{"title":"X","period":"quarter","starts":"2026-10-01","ends":"2026-12-31"}`, manager},
		{"another org's goal progress", "GET", "/api/tasks/goals/goal_b/progress", "/api/tasks/goals/goal_none/progress", "", manager},
		{"delete another org's goal", "DELETE", "/api/tasks/goals/goal_b", "/api/tasks/goals/goal_none", "", manager},
	} {
		other := call(t, srv, c.method, c.other, c.body, c.as)
		missing := call(t, srv, c.method, c.missing, c.body, c.as)
		if other.Code != missing.Code || other.Body.String() != missing.Body.String() || other.Code != http.StatusNotFound {
			t.Errorf("%s: %d %q, missing: %d %q", c.name, other.Code, other.Body.String(), missing.Code, missing.Body.String())
		}
	}
	if grants, err := st.Calendars().ListGrants(ctx, "cal_g"); err != nil || len(grants) != 1 {
		t.Errorf("grants after cross-org calls: %+v %v", grants, err)
	}
	if g, err := st.Org(b).GetGroup(ctx, "grp_b"); err != nil || len(g.Members) != 0 {
		t.Errorf("org b's group after cross-org calls: %+v %v", g, err)
	}
	if c, err := st.Calendars().GetCalendarByID(ctx, "cal_b"); err != nil || c.Name != "B" {
		t.Errorf("org b's calendar after cross-org calls: %+v %v", c, err)
	}
	if bt, err := st.Bookings().GetType(ctx, "usr_b", "bt_b"); err != nil || bt.Title != "B" || bt.Slug != strings.Repeat("b", 32) {
		t.Errorf("org b's booking type after cross-org calls: %+v %v", bt, err)
	}
	if vs, err := st.Tasks().Views(ctx, "usr_b"); err != nil || len(vs) != 1 || vs[0].Name != "B" {
		t.Errorf("org b's views after cross-org calls: %+v %v", vs, err)
	}
	if gs, err := st.TaskGoals().Goals(ctx, "usr_b"); err != nil || len(gs) != 1 || gs[0].Title != "B" {
		t.Errorf("org b's goals after cross-org calls: %+v %v", gs, err)
	}
}

// multiTenant switches st's install to multi-tenant mode, which no build accepts at startup yet
// (tenancy PR 6).
func multiTenant(t *testing.T, st store.Store) {
	t.Helper()
	if err := st.Settings().SetSetting(context.Background(), store.TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
}

// In multi-tenant mode every address is external, inside one org too (tenancy spec, D5): an
// invitation goes to KyPost and writes nothing here, and free/busy answers as for a stranger.
func TestMultiTenantSchedulesByEmail(t *testing.T) {
	srv, ts, st, kp := kypostCardWorld(t)
	alice, _ := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	busy := `{"title":"Busy","start":"2026-11-10T13:00:00Z","end":"2026-11-10T14:00:00Z","zone":"UTC"}`
	if r := call(t, srv, "POST", "/api/calendars/"+bobCal+"/events", busy, bob); r.Code != http.StatusCreated {
		t.Fatalf("bob's event %d", r.Code)
	}
	freeBusy := func(who string) string {
		r := call(t, srv, "GET", "/api/freebusy?users="+who+"&start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z", "", alice)
		return fmt.Sprint(r.Code, " ", strings.ReplaceAll(r.Body.String(), who, "X"))
	}
	if freeBusy("bob@example.com") == freeBusy("nobody@example.com") {
		t.Fatal("control: single-tenant free/busy should show bob's busy time")
	}
	multiTenant(t, st)
	if got, stranger := freeBusy("bob@example.com"), freeBusy("nobody@example.com"); got != stranger {
		t.Errorf("free/busy for bob %s, for a stranger %s", got, stranger)
	}

	carol := ssoUser(t, st, "carol") // KyPost mails only for a KyIdentity account
	if r := rawDAV(t, ts, "PUT", "/dav/usr_carol/calendars/default/plan.ics", "carol", carol, meeting("plan", "carol", "ATTENDEE;RSVP=TRUE:mailto:bob@example.com"),
		map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("carol's PUT %d", r.StatusCode)
	}
	srv.DeliverScheduled(context.Background())
	if bobCopy(t, st, "plan") != nil {
		t.Error("the invitation was written into bob's calendar")
	}
	if sent := kp.take(); len(sent) != 1 || fmt.Sprint(sent[0].Recipients) != "[bob@example.com]" || sent[0].Method != "REQUEST" {
		t.Errorf("KyPost sends %+v", sent)
	}
}

// The CalDAV outbox answers a member's free/busy, in multi-tenant mode, as for a stranger.
func TestMultiTenantOutboxFreeBusy(t *testing.T) {
	w := newSchedWorld(t, true)
	busy := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:bob-busy\r\nDTSTAMP:20261001T000000Z\r\n" +
		"DTSTART:20261110T130000Z\r\nDTEND:20261110T140000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if code, _ := w.put("bob", "busy.ics", busy, ""); code != 201 {
		t.Fatal("bob's event")
	}
	multiTenant(t, w.st)
	ask := func(who string) string {
		code, body := w.outbox("alice", fbRequest("alice@example.com", who))
		return fmt.Sprint(code, " ", strings.ReplaceAll(body, who, "X"))
	}
	if got, stranger := ask("bob@example.com"), ask("nobody@example.com"); got != stranger || !strings.Contains(got, "3.7;") {
		t.Errorf("outbox for bob:\n%s\nfor a stranger:\n%s", got, stranger)
	}
}

// In multi-tenant mode a colleague's invitation gets the remote out-of-office text.
func TestMultiTenantOutOfOfficeIsRemote(t *testing.T) {
	w := newOOOWorld(t)
	multiTenant(t, w.st)
	now := time.Now().UTC()
	on := kypost.OutOfOfficeRequest{Revision: 1, Away: true, Start: now.Add(-time.Hour).Format(time.RFC3339), End: now.Add(48 * time.Hour).Format(time.RFC3339),
		Tentative: true, CommentLocal: "Away; ask Sam.", CommentRemote: "I am away."}
	if r := w.set(t, on); r.Code != 200 {
		t.Fatalf("set: %d %s", r.Code, r.Body.String())
	}
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:alice-1\r\nDTSTAMP:20261101T000000Z\r\n" +
		"DTSTART:20261212T090000Z\r\nDTEND:20261212T100000Z\r\nSUMMARY:Plan\r\nORGANIZER:mailto:alice@example.com\r\n" +
		"ATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
	if r := signedPost(t, w.srv, "/api/itip/inbound", kypost.EventInbound, "in-1", kypost.InboundRequest{User: bob, From: "alice@example.com", DKIMAligned: true, ICS: invite}); !strings.Contains(r.Body.String(), "applied") {
		t.Fatalf("inbound: %d %s", r.Code, r.Body.String())
	}
	w.srv.DeliverScheduled(context.Background())
	if sent := w.fake.take(); len(sent) != 1 || !strings.Contains(sent[0].ICS, "COMMENT:I am away.") {
		t.Fatalf("reply %+v", sent)
	}
}

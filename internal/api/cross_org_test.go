package api_test

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
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

// crossOrgExempt is every route without a cross-org row, and why no other org's ID can reach
// it. TestEveryRouteIsInTheCrossOrgMatrix fails on a new route that is in neither list.
var crossOrgExempt = map[string]string{}

func init() {
	for reason, routes := range map[string][]string{
		// No object ID from the request: the route reads or writes only the caller's own rows.
		"own": {"GET /api/app-passwords", "POST /api/app-passwords", "GET /api/calendars", "POST /api/calendars",
			"GET /api/tasks", "POST /api/tasks/inbox", "GET /api/tasks/areas", "PUT /api/tasks/areas", "GET /api/tasks/stats",
			"GET /api/tasks/insights", "GET /api/tasks/goals", "POST /api/tasks/goals", "GET /api/tasks/prefs", "PUT /api/tasks/prefs",
			"GET /api/tasks/views", "POST /api/tasks/views", "GET /api/booking-types", "POST /api/booking-types",
			"POST /api/subscriptions", "GET /api/presence/me", "PUT /api/presence/me", "GET /api/suite/apps",
			"/api/auth/change-password", "/api/auth/logout", "/api/auth/me", "/api/settings", "/api/settings/theme", "/mcp"},
		// Addresses, not IDs: D5 counts an address as internal only inside the caller's org (TestMultiTenant*).
		"address": {"GET /api/freebusy"},
		// Resolved by KyIdentity as the caller, which answers only from the caller's org (T D5).
		"kyidentity": {"POST /api/presence", "GET /api/suite/people", "POST /api/suite/chat"},
		// Signed by a suite peer; the body's org_id is checked in tenancy PR 3 (D10).
		"peer": {"POST /api/itip/inbound", "POST /api/itip/respond", "POST /api/kypost/out-of-office", "POST /api/tasks/flag",
			"POST /api/sso/kysignon/backchannel-logout", "/api/sso/kysignon/sync", "POST /api/installer/directory"},
		// Public: a 128-bit slug or a confirm token decides the host, never a session (pre-org lookups).
		"public": {"GET /api/book/{slug}", "POST /api/book/{slug}/hold", "POST /api/book/confirm/view", "POST /api/book/confirm",
			"POST /api/book/manage", "POST /api/book/manage/cancel", "POST /api/book/manage/reschedule"},
		// Before a principal exists, or carries no org data.
		"pre-auth": {"/", "GET /healthz", "GET /livez", "GET /readyz", "/api/auth/login", "/api/auth/pow-challenge",
			"/api/sso/kysignon/callback", "/api/sso/kysignon/login", "/saml/metadata", "/.well-known/caldav",
			"GET /agent/skills/kycalendar/SKILL.md", "GET /api/installer/network"},
		// A CalDAV home other than the caller's is 403 before any lookup (dav.go).
		"dav": {"/dav/"},
		// Install-wide settings, staff only in multi-tenant mode (Admin surface, tenancy PR 5).
		"platform": {"POST /api/backup/drill", "POST /api/backup/export-capsule", "POST /api/backup/pair-remote", "POST /api/backup/deposit",
			"DELETE /api/backup/pairing", "POST /api/backup/pin-key", "PUT /api/backup/schedule", "GET /api/backup/status",
			"GET /api/settings/rate-limit-networks", "PUT /api/settings/rate-limit-networks", "GET /api/admin/signin",
			"POST /api/admin/signin/test", "PUT /api/admin/signin", "GET /api/admin/kypost", "GET /api/admin/kymessages",
			"GET /api/admin/booking", "PUT /api/admin/booking"},
		// Admin people and lists: staff only, bound to a selected org, in tenancy PR 5 (L12).
		"admin": {"GET /api/admin/users", "POST /api/admin/users", "POST /api/admin/users/{id}/reset-password",
			"PATCH /api/admin/users/{id}", "POST /api/admin/users/{id}/role", "POST /api/admin/users/{id}/disable",
			"POST /api/admin/users/{id}/enable", "GET /api/admin/audit", "GET /api/admin/groups", "GET /api/admin/calendars"},
		// Business features, off (404) in multi-tenant mode (D9).
		"off": {"POST /api/admin/users/{id}/reattach", "POST /api/admin/groups", "PATCH /api/admin/groups/{id}", "DELETE /api/admin/groups/{id}",
			"DELETE /api/admin/groups/{id}/members/{userId}", "POST /api/admin/calendars", "DELETE /api/admin/calendars/{id}",
			"GET /api/admin/scim", "/scim/v2", "/scim/v2/", "GET /api/admin/resources", "POST /api/admin/resources",
			"PUT /api/admin/resources/{id}", "GET /api/resources", "GET /api/resources/{id}/busy", "GET /api/resources/requests",
			"POST /api/resources/requests/{id}"},
	} {
		for _, r := range routes {
			crossOrgExempt[r] = reason
		}
	}
}

// crossOrgCase calls route with another org's ID (other) and with one that exists nowhere
// (missing); the answers must match byte for byte, with status want (404 when 0).
type crossOrgCase struct {
	route, other, missing, body string
	as                          *http.Cookie
	want                        int
}

// crossOrgWorld is org A's personas and org B's objects. b is org B's ID.
func crossOrgWorld(t *testing.T) (*api.Server, store.Store, []crossOrgCase) {
	t.Helper()
	ctx := context.Background()
	srv, st, cfg := setupTestServer(t)
	const b = "org-b"
	addOrg(t, cfg, b)
	manager := loginAs(t, srv, st, "manager", "user")
	admin := loginAs(t, srv, st, "admin", "admin")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []*store.Group{{ID: "grp_a", DisplayName: "A"}, {ID: "grp_b", DisplayName: "B", OrgID: b}} {
		must(st.Groups().CreateGroup(ctx, g))
	}
	must(st.Users().CreateUser(ctx, &store.User{ID: "usr_b", Username: "b", Role: "user", Status: "active", SSOProvider: "local", OrgID: b}))
	cs := st.Calendars()
	must(cs.CreateCalendar(ctx, &store.Calendar{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g", Slug: "g", Name: "G"}, 0))
	must(cs.CreateCalendar(ctx, &store.Calendar{ID: "cal_b", OwnerKind: "user", OwnerID: "usr_b", Slug: "default", Name: "B"}, 0))
	must(cs.CreateCalendar(ctx, &store.Calendar{ID: "cal_bt", OwnerKind: "user", OwnerID: "usr_b", Slug: "tasks", Name: "B tasks", Components: []string{"VTODO"}}, 0))
	must(cs.CreateSubscribedCalendar(ctx, &store.Calendar{ID: "sub_b", OwnerKind: "user", OwnerID: "usr_b", Slug: "feed", Name: "B feed"},
		&store.Subscription{URLSealed: "unopenable", URLHost: "b.invalid", NextCheckAt: time.Now().Add(time.Hour)}, 0, 0))
	for _, o := range []struct{ cal, uid, comp, body string }{
		{"cal_b", "ev_b", "VEVENT", "BEGIN:VEVENT\r\nUID:ev_b\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nDTEND:20261007T100000Z\r\nSUMMARY:B\r\nEND:VEVENT"},
		{"cal_bt", "task_b", "VTODO", "BEGIN:VTODO\r\nUID:task_b\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:B\r\nEND:VTODO"},
	} {
		data := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:t\r\n" + o.body + "\r\nEND:VCALENDAR\r\n")
		_, err := cs.PutObject(ctx, &store.CalendarObject{CalendarID: o.cal, Name: o.uid + ".ics", Component: o.comp, UID: o.uid, ETag: "1", Data: data, WriterID: "usr_b"}, "", true, store.OwnerLimits{})
		must(err)
	}
	must(st.Bookings().CreateType(ctx, &store.BookingType{ID: "bt_b", UserID: "usr_b", Slug: strings.Repeat("b", 32), Title: "B", Zone: "UTC", CalendarID: "cal_b", Windows: "[]", DurationMin: 30}, 1))
	must(st.Tasks().CreateView(ctx, &store.TaskView{ID: "view_b", UserID: "usr_b", Name: "B", Query: "b"}, 1))
	must(st.TaskGoals().CreateGoal(ctx, &store.TaskGoal{ID: "goal_b", UserID: "usr_b", Title: "B", Period: "week", Starts: "2026-11-16", Ends: "2026-11-22"}, 1))
	passB, _ := seedToken(t, st, "usr_b", store.ScopeMCPRead)
	a := st.Org(store.DefaultOrg)
	must(a.AddGroupMember(ctx, "grp_a", "usr_manager"))
	must(a.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_a", Role: "manager"}))

	const ev = `{"title":"X","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`
	return srv, st, []crossOrgCase{
		{route: "PUT /api/calendars/{id}/grants/{group}", other: "/api/calendars/cal_g/grants/grp_b", missing: "/api/calendars/cal_g/grants/grp_none", body: `{"role":"reader"}`, as: manager},
		{route: "DELETE /api/calendars/{id}/grants/{group}", other: "/api/calendars/cal_b/grants/grp_b", missing: "/api/calendars/cal_none/grants/grp_none", as: manager},
		{route: "GET /api/calendars/{id}/grants", other: "/api/calendars/cal_b/grants", missing: "/api/calendars/cal_none/grants", as: manager},
		{route: "PATCH /api/calendars/{id}", other: "/api/calendars/cal_b", missing: "/api/calendars/cal_none", body: `{"name":"X"}`, as: manager},
		{route: "DELETE /api/calendars/{id}", other: "/api/calendars/cal_b", missing: "/api/calendars/cal_none", as: manager},
		{route: "GET /api/events", other: "/api/events?calendar=cal_b&start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z&tz=UTC", missing: "/api/events?calendar=cal_none&start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z&tz=UTC", as: manager},
		{route: "POST /api/calendars/{id}/events", other: "/api/calendars/cal_b/events", missing: "/api/calendars/cal_none/events", body: ev, as: manager},
		{route: "PUT /api/events/{cal}/{uid}", other: "/api/events/cal_b/ev_b", missing: "/api/events/cal_none/ev_none", body: ev, as: manager},
		{route: "DELETE /api/events/{cal}/{uid}", other: "/api/events/cal_b/ev_b?scope=all", missing: "/api/events/cal_none/ev_none?scope=all", as: manager},
		{route: "POST /api/events/{cal}/{uid}/rsvp", other: "/api/events/cal_b/ev_b/rsvp", missing: "/api/events/cal_none/ev_none/rsvp", body: `{"partstat":"ACCEPTED"}`, as: manager},
		{route: "POST /api/calendars/{id}/tasks", other: "/api/calendars/cal_bt/tasks", missing: "/api/calendars/cal_none/tasks", body: `{"title":"X"}`, as: manager},
		{route: "PATCH /api/tasks/{cal}/{uid}", other: "/api/tasks/cal_bt/task_b", missing: "/api/tasks/cal_none/task_none", body: `{"status":"COMPLETED"}`, as: manager},
		{route: "DELETE /api/tasks/{cal}/{uid}", other: "/api/tasks/cal_bt/task_b", missing: "/api/tasks/cal_none/task_none", as: manager},
		{route: "GET /api/tasks/{cal}/{uid}/images/{n}", other: "/api/tasks/cal_bt/task_b/images/0", missing: "/api/tasks/cal_none/task_none/images/0", as: manager},
		{route: "GET /api/tasks/{cal}/{uid}/habit", other: "/api/tasks/cal_bt/task_b/habit", missing: "/api/tasks/cal_none/task_none/habit", as: manager},
		{route: "POST /api/calendars/{id}/import", other: "/api/calendars/cal_b/import?tz=UTC", missing: "/api/calendars/cal_none/import?tz=UTC", body: "Subject,Start Date\nX,10/07/2026\n", as: manager},
		{route: "PATCH /api/subscriptions/{id}", other: "/api/subscriptions/sub_b", missing: "/api/subscriptions/sub_none", body: `{"url":"http://b.invalid/x.ics"}`, as: manager},
		{route: "POST /api/calendars/{id}/refresh", other: "/api/calendars/sub_b/refresh", missing: "/api/calendars/sub_none/refresh", as: manager},
		{route: "PATCH /api/app-passwords/{id}", other: "/api/app-passwords/" + passB, missing: "/api/app-passwords/none", body: `{"scope":"mcp:write-own"}`, as: manager},
		{route: "DELETE /api/app-passwords/{id}", other: "/api/app-passwords/" + passB, missing: "/api/app-passwords/none", as: manager},
		{route: "GET /api/admin/groups/{id}", other: "/api/admin/groups/grp_b", missing: "/api/admin/groups/grp_none", as: admin},
		{route: "PUT /api/admin/groups/{id}/members/{userId}", other: "/api/admin/groups/grp_a/members/usr_b", missing: "/api/admin/groups/grp_a/members/usr_none", as: admin},
		{route: "PUT /api/booking-types/{id}", other: "/api/booking-types/bt_b", missing: "/api/booking-types/bt_none", body: `{"title":"X","zone":"UTC","duration_min":30,"windows":[],"calendar_id":"cal_b"}`, as: manager},
		{route: "POST /api/booking-types/{id}/rotate", other: "/api/booking-types/bt_b/rotate", missing: "/api/booking-types/bt_none/rotate", as: manager},
		{route: "DELETE /api/booking-types/{id}", other: "/api/booking-types/bt_b", missing: "/api/booking-types/bt_none", as: manager},
		{route: "PUT /api/tasks/views/{id}", other: "/api/tasks/views/view_b", missing: "/api/tasks/views/view_none", body: `{"name":"X","query":{"q":"x"}}`, as: manager},
		{route: "DELETE /api/tasks/views/{id}", other: "/api/tasks/views/view_b", missing: "/api/tasks/views/view_none", as: manager},
		{route: "PUT /api/tasks/goals/{id}", other: "/api/tasks/goals/goal_b", missing: "/api/tasks/goals/goal_none", body: `{"title":"X","period":"quarter","starts":"2026-10-01","ends":"2026-12-31"}`, as: manager},
		{route: "GET /api/tasks/goals/{id}/progress", other: "/api/tasks/goals/goal_b/progress", missing: "/api/tasks/goals/goal_none/progress", as: manager},
		{route: "DELETE /api/tasks/goals/{id}", other: "/api/tasks/goals/goal_b", missing: "/api/tasks/goals/goal_none", as: manager},
	}
}

// TestCrossOrgMatrix is the tenancy spec's cross-org test: every route given another org's ID
// answers byte for byte as for an ID that exists nowhere, and org B's rows are unchanged.
func TestCrossOrgMatrix(t *testing.T) {
	ctx := context.Background()
	srv, st, cases := crossOrgWorld(t)
	before := orgBSnapshot(t, st)
	for _, c := range cases {
		method := strings.Fields(c.route)[0]
		other := call(t, srv, method, c.other, c.body, c.as)
		missing := call(t, srv, method, c.missing, c.body, c.as)
		want := cmp.Or(c.want, http.StatusNotFound)
		if other.Code != missing.Code || other.Body.String() != missing.Body.String() || other.Code != want {
			t.Errorf("%s: %d %q, missing: %d %q", c.route, other.Code, other.Body.String(), missing.Code, missing.Body.String())
		}
	}
	if after := orgBSnapshot(t, st); after != before {
		t.Errorf("org b's rows changed:\nbefore %s\nafter  %s", before, after)
	}
	if grants, err := st.Calendars().ListGrants(ctx, "cal_g"); err != nil || len(grants) != 1 {
		t.Errorf("grants after cross-org calls: %+v %v", grants, err)
	}
}

// orgBSnapshot is org B's rows the matrix could reach, as one comparable string. A failed
// lookup is part of the snapshot, so a row deleted by a cross-org call reads as a change.
func orgBSnapshot(t *testing.T, st store.Store) string {
	t.Helper()
	ctx := context.Background()
	var out strings.Builder
	add := func(v any, err error) {
		if err != nil {
			v = "error: " + err.Error()
		}
		j, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(append(j, ';'))
	}
	add(st.Org("org-b").GetGroup(ctx, "grp_b"))
	for _, id := range []string{"cal_b", "cal_bt", "sub_b"} {
		add(st.Calendars().GetCalendarByID(ctx, id))
		add(st.Calendars().ListObjects(ctx, id))
	}
	add(st.Calendars().GetSubscription(ctx, "sub_b"))
	add(st.Calendars().ListGrants(ctx, "cal_b"))
	add(st.Bookings().GetType(ctx, "usr_b", "bt_b"))
	add(st.Tasks().Views(ctx, "usr_b"))
	add(st.TaskGoals().Goals(ctx, "usr_b"))
	add(st.AppPasswords().ListByUser(ctx, "usr_b"))
	return out.String()
}

// TestEveryRouteIsInTheCrossOrgMatrix: each registered route has a cross-org row or a stated
// reason no other org's ID reaches it, so a new route cannot skip the question.
func TestEveryRouteIsInTheCrossOrgMatrix(t *testing.T) {
	srv, _, cases := crossOrgWorld(t)
	rows := map[string]bool{}
	for _, c := range cases {
		rows[c.route] = true
	}
	routes := api.RoutesForTest(srv)
	for _, r := range routes {
		if _, exempt := crossOrgExempt[r]; rows[r] == exempt {
			t.Errorf("route %q needs exactly one of a cross-org row or an exemption (row %v, exempt %v)", r, rows[r], exempt)
		}
	}
	for r := range rows {
		if !slices.Contains(routes, r) {
			t.Errorf("cross-org row %q matches no registered route", r)
		}
	}
	for r := range crossOrgExempt {
		if !slices.Contains(routes, r) {
			t.Errorf("exemption %q matches no registered route", r)
		}
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

// otherOrgUser is an active account called name in a second org, org-other.
func otherOrgUser(t *testing.T, st store.Store, name string) {
	t.Helper()
	ctx := context.Background()
	if err := st.ApplyOrgs(ctx, "", []store.MirroredOrg{{ID: "org-other", Status: "active", Revision: 1, Enabled: true, Quotas: "{}"}}, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_" + name, Username: name, Email: name + "@example.com", Role: "user", Status: "active",
		SSOProvider: "local", OrgID: "org-other"}); err != nil {
		t.Fatal(err)
	}
}

// In multi-tenant mode an address is internal only inside the caller's org (tenancy spec, D5): a
// colleague's free/busy shows and an invitation is written into their calendar, while an account
// in another org answers as a stranger and its invitation goes to KyPost.
func TestMultiTenantSchedulesWithinTheOrg(t *testing.T) {
	srv, ts, st, kp := kypostCardWorld(t)
	alice, _ := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	otherOrgUser(t, st, "dave")
	busy := `{"title":"Busy","start":"2026-11-10T13:00:00Z","end":"2026-11-10T14:00:00Z","zone":"UTC"}`
	if r := call(t, srv, "POST", "/api/calendars/"+bobCal+"/events", busy, bob); r.Code != http.StatusCreated {
		t.Fatalf("bob's event %d", r.Code)
	}
	freeBusy := func(who string) string {
		r := call(t, srv, "GET", "/api/freebusy?users="+who+"&start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z", "", alice)
		return fmt.Sprint(r.Code, " ", strings.ReplaceAll(r.Body.String(), who, "X"))
	}
	multiTenant(t, st)
	stranger := freeBusy("nobody@example.com")
	if got := freeBusy("bob@example.com"); got == stranger || !strings.Contains(got, "13:00") {
		t.Errorf("free/busy for colleague bob %s", got)
	}
	if got := freeBusy("dave@example.com"); got != stranger {
		t.Errorf("free/busy for dave in another org %s, for a stranger %s", got, stranger)
	}

	carol := ssoUser(t, st, "carol") // KyPost mails only for a KyIdentity account
	if r := rawDAV(t, ts, "PUT", "/dav/usr_carol/calendars/default/plan.ics", "carol", carol,
		meeting("plan", "carol", "ATTENDEE;RSVP=TRUE:mailto:bob@example.com", "ATTENDEE;RSVP=TRUE:mailto:dave@example.com"),
		map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("carol's PUT %d", r.StatusCode)
	}
	srv.DeliverScheduled(context.Background())
	if bobCopy(t, st, "plan") == nil {
		t.Error("the invitation was not written into colleague bob's calendar")
	}
	if sent := kp.take(); len(sent) != 1 || fmt.Sprint(sent[0].Recipients) != "[dave@example.com]" || sent[0].Method != "REQUEST" ||
		sent[0].OrgID != string(store.DefaultOrg) { // D10: the sender's org
		t.Errorf("KyPost sends %+v", sent)
	}
	if cals, err := st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_dave"); err != nil || len(cals) != 0 {
		t.Errorf("dave's calendars %v %v: nothing may be written for another org", cals, err)
	}
}

// The CalDAV outbox answers a colleague's free/busy, in multi-tenant mode, and another org's
// member's as for a stranger.
func TestMultiTenantOutboxFreeBusy(t *testing.T) {
	w := newSchedWorld(t, true)
	busy := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:bob-busy\r\nDTSTAMP:20261001T000000Z\r\n" +
		"DTSTART:20261110T130000Z\r\nDTEND:20261110T140000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if code, _ := w.put("bob", "busy.ics", busy, ""); code != 201 {
		t.Fatal("bob's event")
	}
	otherOrgUser(t, w.st, "dave")
	multiTenant(t, w.st)
	ask := func(who string) string {
		code, body := w.outbox("alice", fbRequest("alice@example.com", who))
		return fmt.Sprint(code, " ", strings.ReplaceAll(body, who, "X"))
	}
	stranger := ask("nobody@example.com")
	if got := ask("bob@example.com"); got == stranger || !strings.Contains(got, "2.0;") {
		t.Errorf("outbox for colleague bob:\n%s", got)
	}
	if got := ask("dave@example.com"); got != stranger || !strings.Contains(got, "3.7;") {
		t.Errorf("outbox for dave in another org:\n%s\nfor a stranger:\n%s", got, stranger)
	}
}

// In multi-tenant mode out-of-office answers a colleague's invitation internally, as on a
// single-tenant install, and one from another org's member by mail with the remote text.
func TestMultiTenantOutOfOfficeByOrg(t *testing.T) {
	w := newOOOWorld(t)
	otherOrgUser(t, w.st, "dave")
	multiTenant(t, w.st)
	now := time.Now().UTC()
	on := kypost.OutOfOfficeRequest{Revision: 1, Away: true, Start: now.Add(-time.Hour).Format(time.RFC3339), End: now.Add(48 * time.Hour).Format(time.RFC3339),
		Tentative: true, CommentLocal: "Away; ask Sam.", CommentRemote: "I am away."}
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example", OrgID: string(store.DefaultOrg)}
	on.User = bob
	if r := w.set(t, on); r.Code != 200 {
		t.Fatalf("set: %d %s", r.Code, r.Body.String())
	}
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:dave-1\r\nDTSTAMP:20261101T000000Z\r\n" +
		"DTSTART:20261212T090000Z\r\nDTEND:20261212T100000Z\r\nSUMMARY:Plan\r\nORGANIZER:mailto:dave@example.com\r\n" +
		"ATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	in := kypost.InboundRequest{User: bob, From: "dave@example.com", DKIMAligned: true, ICS: invite}
	if r := signedPost(t, w.srv, "/api/itip/inbound", kypost.EventInbound, "in-1", in); !strings.Contains(r.Body.String(), "applied") {
		t.Fatalf("inbound: %d %s", r.Code, r.Body.String())
	}
	w.srv.DeliverScheduled(context.Background())
	if sent := w.fake.take(); len(sent) != 1 || !strings.Contains(sent[0].ICS, "COMMENT:I am away.") {
		t.Fatalf("reply to dave in another org %+v", sent)
	}

	// alice, a colleague, invites bob: his answer reaches her event internally, never by mail.
	ts := httptest.NewServer(w.srv)
	defer ts.Close()
	if r := rawDAV(t, ts, "PUT", "/dav/usr_alice/calendars/default/plan.ics", "alice", w.tokens["alice"], meeting("plan", "alice", "ATTENDEE;RSVP=TRUE:mailto:bob@example.com"),
		map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("alice's PUT %d", r.StatusCode)
	}
	w.srv.DeliverScheduled(context.Background())
	w.srv.DeliverScheduled(context.Background())
	got := readAll(rawDAV(t, ts, "GET", "/dav/usr_alice/calendars/default/plan.ics", "alice", w.tokens["alice"], "", nil))
	if !strings.Contains(got, "ATTENDEE;PARTSTAT=TENTATIVE;RSVP=TRUE:mailto:bob@example.com") {
		t.Fatalf("alice's event after bob's automatic answer:\n%s", got)
	}
	if sent := w.fake.take(); len(sent) != 0 {
		t.Errorf("a colleague's answer went by mail: %+v", sent)
	}
	if o := bobCopy(t, w.st, "plan"); o == nil || !strings.Contains(string(o.Data), `COMMENT:Away\; ask Sam.`) {
		t.Errorf("bob's copy of a colleague's invitation should carry the local text: %v", o)
	}
}

// D10: in multi-tenant mode every KyPost message must name the account's own org. Another org's
// ID, or none, answers exactly as an unknown subject does; single-tenant mode does not check.
func TestMultiTenantKyPostMessagesMustNameTheAccountsOrg(t *testing.T) {
	srv, _, st, _ := kypostWorld(t)
	routes := []struct{ path, event string }{
		{"/api/itip/inbound", kypost.EventInbound},
		{"/api/itip/respond", kypost.EventRespond},
		{"/api/kypost/out-of-office", kypost.EventOOO},
		{"/api/tasks/flag", kypost.EventTaskFlag},
	}
	n := 0
	send := func(rt struct{ path, event string }, sub, org string) *httptest.ResponseRecorder {
		n++
		return signedPost(t, srv, rt.path, rt.event, fmt.Sprintf("org-%d", n), map[string]any{"sub": sub, "issuer": "https://id.example", "org_id": org})
	}
	answer := func(w *httptest.ResponseRecorder) string { return fmt.Sprint(w.Code, " ", w.Body.String()) }
	for _, rt := range routes {
		if got := answer(send(rt, "sub-bob", "")); strings.Contains(got, "no_user") {
			t.Errorf("single-tenant %s refused bob: %s", rt.path, got)
		}
	}
	multiTenant(t, st)
	for _, rt := range routes {
		unknown := answer(send(rt, "sub-nobody", string(store.DefaultOrg)))
		if !strings.HasPrefix(unknown, "404 ") || !strings.Contains(unknown, "no_user") {
			t.Fatalf("%s for an unknown subject: %s", rt.path, unknown)
		}
		for _, org := range []string{"", "org-other"} {
			if got := answer(send(rt, "sub-bob", org)); got != unknown {
				t.Errorf("%s for bob with org_id %q: %s, want %s", rt.path, org, got, unknown)
			}
		}
		if got := answer(send(rt, "sub-bob", string(store.DefaultOrg))); strings.Contains(got, "no_user") {
			t.Errorf("%s for bob with his own org: %s", rt.path, got)
		}
	}
}

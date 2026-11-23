package api_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// customerMembers gives each member a session in a separate, serving customer org.
func customerMembers(t *testing.T) (*api.Server, store.Store, *http.Cookie, *http.Cookie) {
	t.Helper()
	srv, st, _ := setupTestServer(t)
	alice, bob := seedCustomerMembers(t, st, customerIssuer)
	return srv, st, alice, bob
}

const customerIssuer = "kyidentity https://id.example"

func seedCustomerMembers(t *testing.T, st store.Store, issuer string) (*http.Cookie, *http.Cookie) {
	t.Helper()
	ctx := t.Context()
	multiTenant(t, st)
	orgs := []store.MirroredOrg{
		{ID: "org-a", Status: "active", Enabled: true, Revision: 1, Quotas: "{}"},
		{ID: "org-b", Status: "active", Enabled: true, Revision: 1, Quotas: "{}"},
	}
	if err := st.ApplyOrgs(ctx, "", orgs, 1); err != nil {
		t.Fatal(err)
	}
	member := func(name string, org store.OrgID) *http.Cookie {
		t.Helper()
		u := &store.User{ID: "usr_" + name, OrgID: org, Username: name, Role: "user", Status: "active",
			Email: name + "@example.com", SSOProvider: "kysignon", SSOSubject: name, SSOIssuer: issuer}
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := davbackend.EnsureDefault(ctx, st, u.ID, 0); err != nil {
			t.Fatal(err)
		}
		return sessionFor(t, st, u)
	}
	return member("alice", "org-a"), member("bob", "org-b")
}

func TestCustomerTaskSearchAcrossOrgs(t *testing.T) {
	srv, _, alice, bob := customerMembers(t)
	aliceList, bobList := taskList(t, srv, alice), taskList(t, srv, bob)
	a := createTask(t, srv, alice, aliceList, map[string]any{"title": "quarterly inventory"})
	b := createTask(t, srv, bob, bobList, map[string]any{"title": "quarterly proposal"})
	for _, c := range []struct {
		cookie *http.Cookie
		want   string
	}{{bob, b.UID}, {alice, a.UID}} {
		if got := listTasks(t, srv, c.cookie, "q=quarterly"); len(got) != 1 || got[0].UID != c.want {
			t.Fatalf("search for common term should return only the caller's task: %+v", got)
		}
	}
	query := "/api/tasks?q=quarterly&lists="
	foreign := call(t, srv, "GET", query+bobList, "", alice)
	missing := call(t, srv, "GET", query+"cal_absent", "", alice)
	if foreign.Code != http.StatusNotFound || missing.Code != foreign.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("task list search: foreign %d %q, missing %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
}

func TestCustomerDAVAcrossOrgs(t *testing.T) {
	srv, st, _, _ := customerMembers(t)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	_, alice := seedToken(t, st, "usr_alice", store.ScopeDAV)
	_, bob := seedToken(t, st, "usr_bob", store.ScopeDAV)
	const object = "calendars/default/owner.ics"
	at := func(id, suffix string) string { return "/dav/" + id + "/" + suffix }
	ownerPath := at("usr_bob", object)
	r := rawDAV(t, ts, "PUT", ownerPath, "bob", bob, eventICS("owner-only"), map[string]string{"Content-Type": "text/calendar"})
	readAll(r)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("owner PUT: %d", r.StatusCode)
	}
	get := func() string {
		t.Helper()
		r := rawDAV(t, ts, "GET", ownerPath, "bob", bob, "", nil)
		body := readAll(r)
		if r.StatusCode != http.StatusOK || !strings.Contains(body, "UID:owner-only") {
			t.Fatalf("owner GET: %d", r.StatusCode)
		}
		return body
	}
	before := get()
	for _, c := range []struct{ method, suffix, body string }{
		{"PROPFIND", "", schedPrincipalProps},
		{"PROPFIND", "calendar-proxy-read/", privBody},
		{"PROPFIND", "calendar-proxy-write/", privBody},
		{"PROPFIND", "calendars/", privBody},
		{"PROPFIND", "calendars/default/", privBody},
		{"PROPFIND", "calendars/_inbox/", privBody},
		{"PROPFIND", "calendars/_outbox/", privBody},
		{"REPORT", "calendars/default/", `<c:calendar-query xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:d="DAV:"><d:prop><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"/></c:filter></c:calendar-query>`},
		{"GET", object, ""},
		{"DELETE", object, ""},
	} {
		t.Run(c.method+"/"+c.suffix, func(t *testing.T) {
			request := func(owner string) (int, string) {
				r := rawDAV(t, ts, c.method, at(owner, c.suffix), "alice", alice, c.body, map[string]string{"Content-Type": "application/xml", "Depth": "1"})
				return r.StatusCode, readAll(r)
			}
			code, body := request("usr_bob")
			missingCode, missingBody := request("usr_missing")
			if code != http.StatusForbidden || missingCode != code || missingBody != body {
				t.Fatalf("foreign %d %q, missing %d %q", code, body, missingCode, missingBody)
			}
		})
	}
	if get() != before {
		t.Error("owner's calendar object changed")
	}
}

func TestCustomerSuiteGroupCalendarAcrossOrgs(t *testing.T) {
	srv, st, alice, bob := customerMembers(t)
	scope := store.SCIMScope{Issuer: customerIssuer, Providers: []string{"kysignon"}}
	ch, err := st.Org("org-b").ReconcileSuiteGroup(t.Context(), scope, store.SuiteGroup{
		ID: "sg-b", Name: "Review team", Active: true, Subjects: []string{"alice", "bob"},
	})
	if err != nil || !ch.Created || ch.Added != 1 {
		t.Fatalf("reconcile org B: %+v, %v", ch, err)
	}
	cals, err := st.Calendars().ListCalendarsByKind(t.Context(), "group")
	if err != nil || len(cals) != 1 {
		t.Fatalf("group calendars: %v, %v", cals, err)
	}
	cal := cals[0]
	uid := createEvent(t, srv, bob, cal.ID, `{"title":"Review minutes","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`)
	const events = "/api/events?start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z&tz=UTC"
	for _, tc := range []struct{ path, marker string }{{"/api/calendars", cal.ID}, {events, uid}} {
		owner := call(t, srv, "GET", tc.path, "", bob)
		other := call(t, srv, "GET", tc.path, "", alice)
		if owner.Code != 200 || !strings.Contains(owner.Body.String(), tc.marker) || other.Code != 200 || strings.Contains(other.Body.String(), tc.marker) {
			t.Fatalf("%s: owner %d %s, other org %d %s", tc.path, owner.Code, owner.Body, other.Code, other.Body)
		}
	}
	for _, id := range []string{cal.ID, "cal_missing"} {
		w := call(t, srv, "DELETE", "/api/events/"+id+"/"+uid+"?scope=all", "", alice)
		if w.Code != http.StatusNotFound {
			t.Fatalf("deleting group event: %d %s", w.Code, w.Body)
		}
	}
	if w := call(t, srv, "GET", events, "", bob); w.Code != 200 || !strings.Contains(w.Body.String(), uid) {
		t.Fatal("member lost the original group event")
	}
}

func TestCustomerMemberCannotAdministerAnotherOrg(t *testing.T) {
	srv, st, alice, _ := customerMembers(t)
	bob := func() *store.User {
		t.Helper()
		u, err := st.Users().GetUserByID(t.Context(), "usr_bob")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	before := bob()
	for _, pattern := range api.RoutesForTest(srv) {
		method, path, _ := strings.Cut(pattern, " ")
		if !strings.HasPrefix(path, "/api/admin/") || strings.HasPrefix(path, "/api/admin/resources") {
			continue
		}
		t.Run(pattern, func(t *testing.T) {
			// People operations should stop at authorization even for an existing customer.
			target := func(id string) string {
				return strings.NewReplacer("{id}", id, "{userId}", id).Replace(path)
			}
			real := call(t, srv, method, target("usr_bob"), "{}", alice)
			missing := call(t, srv, method, target("usr_missing"), "{}", alice)
			if (real.Code != http.StatusForbidden && real.Code != http.StatusNotFound) || real.Code != missing.Code || real.Body.String() != missing.Body.String() {
				t.Fatalf("member %d %s, missing %d %s", real.Code, real.Body, missing.Code, missing.Body)
			}
		})
	}
	if after := bob(); !reflect.DeepEqual(before, after) {
		t.Fatal("admin requests changed the other org's account")
	}
}

// Sharing is by group grant. Start with a real grant so both changing its role
// and removing it would be visible to the calendar's owner.
func TestCustomerSharingAcrossOrgs(t *testing.T) {
	srv, st, alice, _ := customerMembers(t)
	ctx := t.Context()
	scope := store.SCIMScope{Issuer: customerIssuer, Providers: []string{"kysignon"}}
	for _, m := range []struct {
		org  store.OrgID
		name string
	}{{"org-a", "alice"}, {"org-b", "bob"}} {
		_, err := st.Org(m.org).ReconcileSuiteGroup(ctx, scope, store.SuiteGroup{
			ID: "sg-" + m.name, Name: m.name + " team", Active: true, Subjects: []string{m.name},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	grant := func(name string) store.CalendarGrant {
		t.Helper()
		gs, err := st.Calendars().UserGrants(ctx, "usr_"+name)
		if err != nil || len(gs) != 1 {
			t.Fatalf("%s grants: %v, %v", name, gs, err)
		}
		return gs[0]
	}
	a, b := grant("alice"), grant("bob")
	a.Role = "manager"
	if err := st.Org("org-a").SetGrant(ctx, a); err != nil {
		t.Fatal(err)
	}
	path := func(cal string) string { return "/api/calendars/" + cal + "/grants" }
	if w := call(t, srv, "GET", path(a.CalendarID), "", alice); w.Code != 200 || !strings.Contains(w.Body.String(), "manager") {
		t.Fatalf("manager reading own grants: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct{ method, foreign, missing string }{
		{"GET", path(b.CalendarID), path("cal_missing")},
		{"PUT", path(b.CalendarID) + "/" + b.GroupID, path("cal_missing") + "/" + b.GroupID},
		{"DELETE", path(b.CalendarID) + "/" + b.GroupID, path("cal_missing") + "/" + b.GroupID},
		{"PUT", path(a.CalendarID) + "/" + b.GroupID, path(a.CalendarID) + "/grp_missing"},
	} {
		foreign := call(t, srv, tc.method, tc.foreign, `{"role":"reader"}`, alice)
		missing := call(t, srv, tc.method, tc.missing, `{"role":"reader"}`, alice)
		if foreign.Code != 404 || missing.Code != foreign.Code || foreign.Body.String() != missing.Body.String() {
			t.Errorf("%s %s: foreign %d %s, missing %d %s", tc.method, tc.foreign, foreign.Code, foreign.Body, missing.Code, missing.Body)
		}
	}
	if grant("alice") != a || grant("bob") != b {
		t.Error("customer grants changed")
	}
}

func TestCustomerSchedulingAcrossOrgs(t *testing.T) {
	srv, ts, st, mail := kypostCardWorld(t)
	alice, bob := seedCustomerMembers(t, st, customerIssuer)
	ctx := t.Context()
	cal, err := st.Calendars().GetCalendarBySlug(ctx, "user", "usr_bob", "default")
	if err != nil {
		t.Fatal(err)
	}
	createEvent(t, srv, bob, cal.ID, `{"title":"Review","start":"2026-11-10T13:00:00Z","end":"2026-11-10T14:00:00Z","zone":"UTC"}`)
	before, err := st.Calendars().ListObjects(ctx, cal.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("busy event: %v, %v", before, err)
	}
	busy := func(cookie *http.Cookie, email string) string {
		t.Helper()
		w := call(t, srv, "GET", "/api/freebusy?start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z&users="+email, "", cookie)
		if w.Code != 200 {
			t.Fatalf("free/busy: %d %s", w.Code, w.Body)
		}
		return strings.ReplaceAll(w.Body.String(), email, "recipient")
	}
	if own := busy(bob, "bob@example.com"); !strings.Contains(own, "13:00:00Z") {
		t.Fatalf("owner's busy period missing: %s", own)
	}
	if b, missing := busy(alice, "bob@example.com"), busy(alice, "missing@example.com"); b != missing || !strings.Contains(b, `"unknown":true`) {
		t.Fatalf("foreign busy %s, missing %s", b, missing)
	}

	_, token := seedToken(t, st, "usr_alice", store.ScopeDAV)
	for _, recipient := range []string{"bob", "missing"} {
		uid := "invite-" + recipient
		addr := recipient + "@example.com"
		r := rawDAV(t, ts, "PUT", "/dav/usr_alice/calendars/default/"+uid+".ics", "alice", token,
			meeting(uid, "alice", "ATTENDEE;RSVP=TRUE:mailto:"+addr), map[string]string{"Content-Type": "text/calendar"})
		body := readAll(r)
		if r.StatusCode != http.StatusCreated || body != "" {
			t.Fatalf("invite %s: %d %s", recipient, r.StatusCode, body)
		}
		srv.DeliverScheduled(ctx)
		sent := mail.take()
		if len(sent) != 1 || sent[0].Method != "REQUEST" || sent[0].OrgID != "org-a" || !reflect.DeepEqual(sent[0].Recipients, []string{addr}) {
			t.Fatalf("mail for %s: %+v", recipient, sent)
		}
	}
	if after, err := st.Calendars().ListObjects(ctx, cal.ID); err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("attendee calendar changed: %v, %v", after, err)
	}
}

func TestCustomerKyPostWrongOrg(t *testing.T) {
	srv, _, st, mail := kypostCardWorld(t)
	_, bob := seedCustomerMembers(t, st, customerIssuer)
	ctx := t.Context()
	ics := strings.Replace(meeting("invitation", "sender", "ATTENDEE;RSVP=TRUE:mailto:bob@example.com"), "BEGIN:VEVENT", "METHOD:REQUEST\r\nBEGIN:VEVENT", 1)
	// Give Bob an actual invitation. A cancellation or an RSVP can now change it.
	owner := kypost.User{Sub: "bob", Issuer: "https://id.example", OrgID: "org-b"}
	resp := kypost.RespondRequest{User: owner, UID: "invitation", Partstat: "ACCEPTED", ICS: ics}
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "owner-accept", resp); w.Code != 200 {
		t.Fatalf("owner RSVP: %d %s", w.Code, w.Body)
	}
	srv.DeliverScheduled(ctx)
	if sent := mail.take(); len(sent) != 1 || sent[0].Method != "REPLY" {
		t.Fatalf("owner reply: %+v", sent)
	}
	before := bobCopy(t, st, "invitation")
	if before == nil {
		t.Fatal("owner invitation missing")
	}
	cancel := strings.Replace(ics, "METHOD:REQUEST", "METHOD:CANCEL", 1)
	for _, tc := range []struct {
		path, event string
		body        func(kypost.User) any
	}{
		{"/api/itip/inbound", kypost.EventInbound, func(u kypost.User) any {
			return kypost.InboundRequest{User: u, From: "sender@example.com", DKIMAligned: true, ICS: cancel}
		}},
		{"/api/itip/respond", kypost.EventRespond, func(u kypost.User) any {
			return kypost.RespondRequest{User: u, UID: "invitation", Partstat: "DECLINED", ICS: ics}
		}},
		{"/api/kypost/out-of-office", kypost.EventOOO, func(u kypost.User) any {
			return kypost.OutOfOfficeRequest{User: u, Revision: 1, Away: true, Start: "2026-11-10T09:00:00Z", End: "2026-11-11T09:00:00Z"}
		}},
		{"/api/tasks/flag", kypost.EventTaskFlag, func(u kypost.User) any {
			return kypost.TaskFlagRequest{User: u, Ref: "mail", Subject: "Review", Flagged: true, URL: "https://kypost.example/#/m/mail"}
		}},
	} {
		t.Run(tc.event, func(t *testing.T) {
			request := func(sub string) *httptest.ResponseRecorder {
				u := kypost.User{Sub: sub, Issuer: owner.Issuer, OrgID: "org-a"}
				return signedPost(t, srv, tc.path, tc.event, tc.event+"-"+sub, tc.body(u))
			}
			b, missing := request(owner.Sub), request("missing")
			if b.Code != 404 || missing.Code != b.Code || b.Body.String() != missing.Body.String() || codeOf(t, b.Body.Bytes()) != "no_user" {
				t.Fatalf("foreign %d %s, missing %d %s", b.Code, b.Body, missing.Code, missing.Body)
			}
		})
	}
	srv.DeliverScheduled(ctx)
	if !reflect.DeepEqual(before, bobCopy(t, st, "invitation")) {
		t.Error("invitation changed")
	}
	if sent := mail.take(); len(sent) != 0 {
		t.Errorf("unexpected mail: %+v", sent)
	}
	if _, err := st.Users().OutOfOffice(ctx, "usr_bob"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("out-of-office state: %v", err)
	}
	if tasks := listTasks(t, srv, bob, "source=kypost"); len(tasks) != 0 {
		t.Errorf("unexpected tasks: %+v", tasks)
	}
}

// KyIdentity filters the directory as the signed-in subject. Prime the search
// cache as Bob before asking for precisely the same name as Alice.
func TestCustomerAttendeeSearchAcrossOrgs(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	directory := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/suite/people" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("sub") == "bob" && q.Get("q") == "bob":
			_, _ = w.Write([]byte(`{"people":[{"sub":"bob","name":"Bob","email":"bob@example.com"}]}`))
		case q.Get("sub") == "alice" && q.Get("q") == "alice":
			_, _ = w.Write([]byte(`{"people":[{"sub":"alice","name":"Alice","email":"alice@example.com"}]}`))
		default:
			_, _ = w.Write([]byte(`{"people":[]}`))
		}
	}))
	t.Cleanup(directory.Close)
	p := sso.NewProvider(sso.KindKyIdentity, "SSO", directory.URL, "kycalendar", "suite-secret", directory.Client())
	api.SetSignInForTest(srv, p)
	alice, bob := seedCustomerMembers(t, st, p.Binding)
	search := func(cookie *http.Cookie, q string) string {
		t.Helper()
		w := call(t, srv, "GET", "/api/suite/people?q="+q, "", cookie)
		if w.Code != 200 {
			t.Fatalf("search: %d %s", w.Code, w.Body)
		}
		return w.Body.String()
	}
	if got := search(bob, "bob"); !strings.Contains(got, "bob@example.com") {
		t.Fatalf("owner's search: %s", got)
	}
	if got := search(alice, "alice"); !strings.Contains(got, "alice@example.com") {
		t.Fatalf("caller's search: %s", got)
	}
	if foreign, missing := search(alice, "bob"), search(alice, "missing"); foreign != missing || !strings.Contains(foreign, `"people":[]`) {
		t.Fatalf("foreign %s, missing %s", foreign, missing)
	}
}

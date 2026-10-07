package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func eventICS(uid string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nDTEND:20261007T100000Z\r\nSUMMARY:m\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

// groupCalendar stores a group calendar the way the admin route creates one.
func groupCalendar(t *testing.T, st store.Store, name string) *store.Calendar {
	t.Helper()
	id := "cal_" + uuid.NewString()
	c := &store.Calendar{ID: id, OwnerKind: "group", OwnerID: id, Slug: "group", Name: name}
	if err := st.Calendars().CreateCalendar(context.Background(), c, 0); err != nil {
		t.Fatal(err)
	}
	return c
}

// grantRole gives userID the role on cal through a group of its own.
func grantRole(t *testing.T, st store.Store, cal *store.Calendar, role, userID string) {
	t.Helper()
	ctx := context.Background()
	g := &store.Group{ID: "grp_" + role + "_" + cal.ID, DisplayName: role + " " + cal.ID}
	if err := st.Groups().CreateGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, g.ID, userID); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: g.ID, Role: role}); err != nil {
		t.Fatal(err)
	}
}

var writePriv = regexp.MustCompile(`<(\w+:)?write[\s/>]`)

const privBody = `<d:propfind xmlns:d="DAV:"><d:prop><d:current-user-privilege-set/></d:prop></d:propfind>`

func TestGroupCalendarInEveryMembersHome(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := groupCalendar(t, st, "Team")
	tokens := map[string]string{}
	for _, name := range []string{"reader", "editor", "nonmember"} {
		tokens[name] = davUser(t, st, name, "user")
	}
	grantRole(t, st, cal, "reader", "usr_reader")
	grantRole(t, st, cal, "editor", "usr_editor")

	for name, wantListed := range map[string]bool{"reader": true, "editor": true, "nonmember": false} {
		c := davClient(t, ts, name, tokens[name])
		home := "/dav/usr_" + name + "/calendars/"
		cals, err := c.FindCalendars(context.Background(), home)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		listed := false
		for _, dc := range cals {
			listed = listed || dc.Path == home+"_"+cal.ID+"/"
		}
		if listed != wantListed {
			t.Errorf("%s: group calendar listed %v, want %v", name, listed, wantListed)
		}
	}
	for name, wantWrite := range map[string]bool{"reader": false, "editor": true} {
		r := rawDAV(t, ts, "PROPFIND", "/dav/usr_"+name+"/calendars/_"+cal.ID+"/", name, tokens[name], privBody, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
		body := readAll(r)
		if r.StatusCode != http.StatusMultiStatus || writePriv.MatchString(body) != wantWrite {
			t.Errorf("%s: %d write privilege %v, want %v: %s", name, r.StatusCode, writePriv.MatchString(body), wantWrite, body)
		}
	}
}

func TestGroupCalendarRoles(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := groupCalendar(t, st, "Team")
	tok := map[string]string{}
	for _, name := range []string{"reader", "editor", "manager", "nonmember"} {
		tok[name] = davUser(t, st, name, "user")
	}
	for _, role := range []string{"reader", "editor", "manager"} {
		grantRole(t, st, cal, role, "usr_"+role)
	}
	at := func(user, name string) string { return "/dav/usr_" + user + "/calendars/_" + cal.ID + "/" + name }
	ics := map[string]string{"Content-Type": "text/calendar"}
	xmlCT := map[string]string{"Content-Type": "application/xml"}
	patch := `<d:propertyupdate xmlns:d="DAV:"><d:set><d:prop><d:displayname>Renamed</d:displayname></d:prop></d:set></d:propertyupdate>`
	mk := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:displayname>x</d:displayname></d:prop></d:set></c:mkcalendar>`

	steps := []struct {
		who, method, path, body string
		hdr                     map[string]string
		want                    int
	}{
		{"editor", "PUT", at("editor", "e.ics"), eventICS("e"), ics, http.StatusCreated},
		{"reader", "GET", at("reader", "e.ics"), "", nil, http.StatusOK},
		{"reader", "PUT", at("reader", "r.ics"), eventICS("r"), ics, http.StatusForbidden},
		{"reader", "DELETE", at("reader", "e.ics"), "", nil, http.StatusForbidden},
		{"reader", "PROPPATCH", at("reader", ""), patch, xmlCT, http.StatusForbidden},
		{"editor", "PROPPATCH", at("editor", ""), patch, xmlCT, http.StatusForbidden},
		{"manager", "PROPPATCH", at("manager", ""), patch, xmlCT, http.StatusMultiStatus},
		{"manager", "MKCALENDAR", "/dav/usr_manager/calendars/_cal_" + uuid.NewString() + "/", mk, xmlCT, http.StatusForbidden},
		{"nonmember", "GET", at("nonmember", "e.ics"), "", nil, http.StatusNotFound},
		{"nonmember", "PUT", at("nonmember", "n.ics"), eventICS("n"), ics, http.StatusNotFound},
		{"editor", "DELETE", at("editor", "e.ics"), "", nil, http.StatusNoContent},
	}
	for i, s := range steps {
		if r := rawDAV(t, ts, s.method, s.path, s.who, tok[s.who], s.body, s.hdr); r.StatusCode != s.want {
			t.Fatalf("step %d %s %s %s: %d, want %d: %s", i, s.who, s.method, s.path, r.StatusCode, s.want, readAll(r))
		}
	}
	if got, _ := st.Calendars().GetCalendarByID(context.Background(), cal.ID); got.Name != "Renamed" {
		t.Fatalf("manager rename not stored: %q", got.Name)
	}
}

// Grants are loaded per request: leaving the group cuts a syncing phone off at once.
func TestMembershipRemovalTakesEffectNextRequest(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := groupCalendar(t, st, "Team")
	token := davUser(t, st, "reader", "user")
	grantRole(t, st, cal, "reader", "usr_reader")
	path := "/dav/usr_reader/calendars/_" + cal.ID + "/"
	if r := rawDAV(t, ts, "PROPFIND", path, "reader", token, privBody, map[string]string{"Depth": "0", "Content-Type": "application/xml"}); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("member: %d", r.StatusCode)
	}
	if err := st.Groups().RemoveGroupMember(context.Background(), "grp_reader_"+cal.ID, "usr_reader"); err != nil {
		t.Fatal(err)
	}
	if r := rawDAV(t, ts, "PROPFIND", path, "reader", token, privBody, map[string]string{"Depth": "0", "Content-Type": "application/xml"}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("after leaving the group: %d, want 404", r.StatusCode)
	}
}

// A group segment never reaches a personal calendar, even with the right ID.
func TestGroupSegmentNeverReachesPersonalCalendar(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	aliceTok := davUser(t, st, "alice", "user")
	bobTok := davUser(t, st, "bob", "user")
	if r := rawDAV(t, ts, "PROPFIND", "/dav/usr_alice/calendars/", "alice", aliceTok, privBody, map[string]string{"Depth": "1", "Content-Type": "application/xml"}); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("alice home: %d", r.StatusCode)
	}
	cals, err := st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_alice")
	if err != nil || len(cals) == 0 {
		t.Fatalf("alice's personal calendars: %v %v", cals, err)
	}
	for _, who := range []struct{ user, tok string }{{"bob", bobTok}, {"alice", aliceTok}} {
		p := "/dav/usr_" + who.user + "/calendars/_" + cals[0].ID + "/"
		if r := rawDAV(t, ts, "PROPFIND", p, who.user, who.tok, privBody, map[string]string{"Depth": "0", "Content-Type": "application/xml"}); r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s via group segment: %d, want 404", who.user, r.StatusCode)
		}
	}
}

// A multiget on one's own calendar must not fetch an href inside a group calendar one cannot read.
func TestGroupCalendarMultigetFromPersonalCalendar(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := groupCalendar(t, st, "Team")
	editorTok := davUser(t, st, "editor", "user")
	outsiderTok := davUser(t, st, "outsider", "user")
	grantRole(t, st, cal, "editor", "usr_editor")
	secret := strings.Replace(eventICS("secret-uid-7"), "SUMMARY:m", "SUMMARY:secret-summary", 1)
	if r := rawDAV(t, ts, "PUT", "/dav/usr_editor/calendars/_"+cal.ID+"/seed.ics", "editor", editorTok, secret, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("seed: %d %s", r.StatusCode, readAll(r))
	}

	multiget := func(user, tok string) (*http.Response, string) {
		href := "/dav/usr_" + user + "/calendars/_" + cal.ID + "/seed.ics"
		body := `<c:calendar-multiget xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/><c:calendar-data/></d:prop><d:href>` + href + `</d:href></c:calendar-multiget>`
		r := rawDAV(t, ts, "REPORT", "/dav/usr_"+user+"/calendars/default/", user, tok, body, map[string]string{"Depth": "1", "Content-Type": "application/xml"})
		return r, readAll(r)
	}
	// Control: the same request from a member does return the object, so the check below bites.
	if r, got := multiget("editor", editorTok); !strings.Contains(got, "secret-summary") {
		t.Fatalf("member multiget did not return the object: %d %s", r.StatusCode, got)
	}
	r, got := multiget("outsider", outsiderTok)
	t.Logf("outsider multiget: %d %s", r.StatusCode, got)
	if strings.Contains(got, "secret-summary") || strings.Contains(got, "secret-uid-7") {
		t.Fatalf("multiget leaked the group object: %d %s", r.StatusCode, got)
	}
	if r.StatusCode == http.StatusMultiStatus && !regexp.MustCompile(`HTTP/1\.1 40[34]`).MatchString(got) {
		t.Fatalf("multiget answered the href without 403/404: %s", got)
	}
}

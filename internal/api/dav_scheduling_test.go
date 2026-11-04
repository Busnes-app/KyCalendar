package api_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// schedUser is an everyday user with an email (their calendar user address) and a dav token.
func schedUser(t *testing.T, st store.Store, name string) string {
	t.Helper()
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_" + name, Username: name, Email: name + "@example.com",
		Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{Scope: store.ScopeDAV, ID: id, UserID: "usr_" + name, Label: "t", Hash: hash}, 0); err != nil {
		t.Fatal(err)
	}
	return token
}

func meeting(uid, organizer string, extra ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261001T000000Z\r\n" +
		"DTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\nSUMMARY:Planning\r\nORGANIZER:mailto:" + organizer + "@example.com\r\n" +
		strings.Join(extra, "\r\n") + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

type schedWorld struct {
	t      *testing.T
	srv    *api.Server
	st     store.Store
	ts     *httptest.Server
	tokens map[string]string
}

func newSchedWorld(t *testing.T, on bool) *schedWorld {
	if on {
		t.Setenv("KYCALENDAR_SCHEDULING", "true")
	}
	srv, st, _ := setupTestServer(t)
	w := &schedWorld{t: t, srv: srv, st: st, ts: httptest.NewServer(srv), tokens: map[string]string{}}
	t.Cleanup(w.ts.Close)
	for _, n := range []string{"alice", "bob", "mallory"} {
		w.tokens[n] = schedUser(t, st, n)
	}
	return w
}

func (w *schedWorld) put(user, path, body, ifMatch string) (int, string) {
	w.t.Helper()
	h := map[string]string{"Content-Type": "text/calendar"}
	if ifMatch != "" {
		h["If-Match"] = ifMatch
	}
	r := rawDAV(w.t, w.ts, "PUT", "/dav/usr_"+user+"/calendars/default/"+path, user, w.tokens[user], body, h)
	return r.StatusCode, r.Header.Get("ETag")
}

func (w *schedWorld) get(user, path string) (int, string, string) {
	w.t.Helper()
	r := rawDAV(w.t, w.ts, "GET", "/dav/usr_"+user+"/calendars/default/"+path, user, w.tokens[user], "", nil)
	return r.StatusCode, readAll(r), r.Header.Get("ETag")
}

// copyOf is user's only object with uid in their default calendar.
func (w *schedWorld) copyOf(user, uid string) (string, *store.CalendarObject) {
	w.t.Helper()
	c, err := w.st.Calendars().GetCalendarBySlug(context.Background(), "user", "usr_"+user, "default")
	if err != nil {
		return "", nil
	}
	o, err := w.st.Calendars().GetObjectByUID(context.Background(), c.ID, uid)
	if err != nil {
		return "", nil
	}
	return string(o.Data), o
}

func (w *schedWorld) deliver() {
	w.t.Helper()
	w.srv.DeliverScheduled(context.Background())
	if due, err := w.st.Calendars().DueOutbox(context.Background(), time.Now().Add(48*time.Hour), 100); err != nil || len(due) != 0 {
		w.t.Fatalf("messages still pending after delivery: %d %v", len(due), err)
	}
}

func TestSchedulingOffChangesNothing(t *testing.T) {
	w := newSchedWorld(t, false)
	body := meeting("m-off", "alice", "ATTENDEE:mailto:bob@example.com")
	if code, etag := w.put("alice", "m-off.ics", body, ""); code != 201 || etag == "" {
		t.Fatalf("PUT %d %q", code, etag)
	}
	if _, got, _ := w.get("alice", "m-off.ics"); got != body {
		t.Fatal("stored object is not byte-identical")
	}
	w.srv.DeliverScheduled(context.Background())
	if due, _ := w.st.Calendars().DueOutbox(context.Background(), time.Now().Add(48*time.Hour), 10); len(due) != 0 {
		t.Fatalf("%d messages queued with scheduling off", len(due))
	}
	if got, _ := w.copyOf("bob", "m-off"); got != "" {
		t.Fatal("bob got a copy with scheduling off")
	}
}

func TestSchedulingInternalRoundTrip(t *testing.T) {
	w := newSchedWorld(t, true)

	// Invite: alice's object is stored as sent; bob gets a copy awaiting his answer.
	invite := meeting("m1", "alice", "SEQUENCE:0", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com", "ATTENDEE;RSVP=TRUE:mailto:bob@example.com")
	if code, etag := w.put("alice", "m1.ics", invite, ""); code != 201 || etag == "" {
		t.Fatalf("invite PUT %d %q", code, etag)
	}
	if _, got, _ := w.get("alice", "m1.ics"); got != invite {
		t.Fatal("an unmodified organizer object must be stored byte for byte")
	}
	w.deliver()
	bobCopy, bobObj := w.copyOf("bob", "m1")
	if bobObj == nil || !strings.Contains(bobCopy, "PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:bob@example.com") && !strings.Contains(bobCopy, "RSVP=TRUE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com") {
		t.Fatalf("bob's copy:\n%s", bobCopy)
	}
	if strings.Contains(bobCopy, "METHOD") {
		t.Fatal("a delivered copy carries METHOD")
	}

	// Bob accepts from his client; alice's object records it.
	accepted := strings.Replace(bobCopy, "PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED", 1)
	r := rawDAV(t, w.ts, "PUT", "/dav/usr_bob/calendars/default/"+bobObj.Name, "bob", w.tokens["bob"], accepted,
		map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + bobObj.ETag + `"`})
	if r.StatusCode != 201 {
		t.Fatalf("bob's accept: %d %s", r.StatusCode, readAll(r))
	}
	w.deliver()
	_, alices, aliceETag := w.get("alice", "m1.ics")
	if !strings.Contains(alices, "ACCEPTED;RSVP=TRUE:mailto:bob@example.com") && !strings.Contains(alices, "RSVP=TRUE;PARTSTAT=ACCEPTED:mailto:bob@example.com") && !strings.Contains(alices, "PARTSTAT=ACCEPTED:mailto:bob@example.com") {
		t.Fatalf("alice's object after bob accepted:\n%s", alices)
	}

	// Alice moves it without raising SEQUENCE: the server raises it, returns no ETag so the client
	// refetches, and bob is asked again.
	moved := strings.Replace(alices, "DTSTART:20261110T090000Z", "DTSTART:20261110T080000Z", 1)
	code, etag := w.put("alice", "m1.ics", moved, aliceETag)
	if code != 201 || etag != "" {
		t.Fatalf("move PUT %d, ETag %q (want none: the server changed the object)", code, etag)
	}
	if c, got, _ := w.get("alice", "m1.ics"); !strings.Contains(got, "SEQUENCE:1") {
		t.Fatalf("SEQUENCE not raised (GET %d):\n%s\nsent:\n%s", c, got, moved)
	}
	w.deliver()
	if got, _ := w.copyOf("bob", "m1"); !strings.Contains(got, "DTSTART:20261110T080000Z") || !strings.Contains(got, "NEEDS-ACTION") {
		t.Fatalf("bob's copy after the move:\n%s", got)
	}

	// Alice deletes it. Bob has not answered the moved meeting, so his copy goes (Q7).
	if r := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/m1.ics", "alice", w.tokens["alice"], "", nil); r.StatusCode != 204 {
		t.Fatalf("delete %d", r.StatusCode)
	}
	w.deliver()
	if got, obj := w.copyOf("bob", "m1"); obj != nil {
		t.Fatalf("bob's unanswered copy after the cancel:\n%s", got)
	}
}

func TestSchedulingExternalWaitsForTransport(t *testing.T) {
	w := newSchedWorld(t, true)
	if code, _ := w.put("alice", "m2.ics", meeting("m2", "alice", "ATTENDEE:mailto:carol@example.net"), ""); code != 201 {
		t.Fatalf("PUT %d", code)
	}
	w.deliver() // fails at once (no transport), so nothing stays pending
}

func TestSchedulingRefusesImpostors(t *testing.T) {
	w := newSchedWorld(t, true)
	// Mallory writes a meeting naming alice as organizer: mallory is neither, nothing is sent.
	if code, _ := w.put("mallory", "fake.ics", meeting("fake", "alice", "ATTENDEE:mailto:bob@example.com"), ""); code != 201 {
		t.Fatalf("PUT %d", code)
	}
	w.deliver()
	if got, _ := w.copyOf("bob", "fake"); got != "" {
		t.Fatalf("an impostor's meeting reached bob:\n%s", got)
	}

	// Alice invites bob; mallory then organizes a meeting with the same UID to overwrite bob's copy.
	if code, _ := w.put("alice", "real.ics", meeting("real", "alice", "ATTENDEE:mailto:bob@example.com"), ""); code != 201 {
		t.Fatal("alice's invite")
	}
	w.deliver()
	before, _ := w.copyOf("bob", "real")
	hijack := strings.Replace(meeting("real", "mallory", "ATTENDEE:mailto:bob@example.com"), "SUMMARY:Planning", "SUMMARY:Hijacked", 1)
	if code, _ := w.put("mallory", "real.ics", hijack, ""); code != 201 {
		t.Fatal("mallory's PUT")
	}
	w.deliver()
	if after, _ := w.copyOf("bob", "real"); after != before || strings.Contains(after, "Hijacked") {
		t.Fatalf("another organizer changed bob's copy:\n%s", after)
	}
}

// With scheduling on, an agent's edit of a meeting would mail every attendee: refused until
// the mcp:invite scope exists. Events without attendees are unaffected.
func TestSchedulingAgentCannotChangeMeetings(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)
	c, err := w.st.Calendars().GetCalendarByID(context.Background(), w.personal)
	if err != nil {
		t.Fatal(err)
	}
	for uid, body := range map[string]string{
		"meet":  meeting("meet", "alice", "ATTENDEE:mailto:bob@example.com"),
		"alone": strings.Replace(meeting("alone", "alice"), "ORGANIZER:mailto:alice@example.com\r\n", "", 1),
	} {
		if _, err := davbackend.Write(context.Background(), w.st, c, uid+".ics", decode(t, body), []byte(body), "", true, store.OwnerLimits{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	etag := func(uid string) string {
		o, err := w.st.Calendars().GetObjectByUID(context.Background(), w.personal, uid)
		if err != nil {
			t.Fatal(err)
		}
		return o.ETag
	}
	retitle := func(uid string) map[string]any {
		return map[string]any{"calendar_id": w.personal, "uid": uid, "etag": etag(uid), "title": "Renamed"}
	}
	if msg := callTool(t, cs, "update_event", retitle("meet"), nil); msg != api.MCPMeetingForTest {
		t.Errorf("meeting: got %q, want %q", msg, api.MCPMeetingForTest)
	}
	if msg := callTool(t, cs, "update_event", retitle("alone"), nil); msg != "" {
		t.Errorf("an event without attendees: %q", msg)
	}
}

// An object go-ical decodes but will not re-encode (here a VEVENT without DTSTAMP) is stored as
// sent and schedules nothing; it never takes the server down.
func TestSchedulingUnencodableObject(t *testing.T) {
	w := newSchedWorld(t, true)
	body := strings.Replace(meeting("odd", "alice", "ATTENDEE:mailto:bob@example.com"), "DTSTAMP:20261001T000000Z\r\n", "", 1)
	code, _ := w.put("alice", "odd.ics", body, "")
	if code == 201 {
		if _, got, _ := w.get("alice", "odd.ics"); got != body {
			t.Fatal("stored object differs from what was sent")
		}
	} else if code != 409 {
		t.Fatalf("PUT %d", code)
	}
	w.deliver()
	if got, _ := w.copyOf("bob", "odd"); got != "" {
		t.Fatalf("an object the server cannot re-encode was delivered:\n%s", got)
	}
	t.Logf("PUT answered %d", code)
}

// The guard reads the stored object, not the edit's result: moving a series drops its
// overrides, so an override carrying the only attendee would otherwise be cancelled unasked.
func TestSchedulingAgentCannotDropAMeetingOverride(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	w := newMCPWorld(t)
	ctx := context.Background()
	u, err := w.st.Users().GetUserByID(ctx, "usr_alice")
	if err != nil {
		t.Fatal(err)
	}
	u.Email = "alice@example.com"
	if err := w.st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)
	c, err := w.st.Calendars().GetCalendarByID(ctx, w.personal)
	if err != nil {
		t.Fatal(err)
	}
	series := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:series\r\nDTSTAMP:20261001T000000Z\r\n" +
		"DTSTART:20261102T090000Z\r\nDTEND:20261102T100000Z\r\nRRULE:FREQ=DAILY;COUNT=5\r\nSUMMARY:Standup\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:series\r\nDTSTAMP:20261001T000000Z\r\nRECURRENCE-ID:20261103T090000Z\r\nDTSTART:20261103T090000Z\r\n" +
		"DTEND:20261103T100000Z\r\nSUMMARY:Standup with Bob\r\nORGANIZER:mailto:alice@example.com\r\nATTENDEE:mailto:bob@example.com\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := davbackend.Write(ctx, w.st, c, "series.ics", decode(t, series), []byte(series), "", true, store.OwnerLimits{}, nil); err != nil {
		t.Fatal(err)
	}
	before, err := w.st.Calendars().GetObjectByUID(ctx, w.personal, "series")
	if err != nil {
		t.Fatal(err)
	}
	move := map[string]any{"calendar_id": w.personal, "uid": "series", "etag": before.ETag, "scope": "all",
		"start": "2026-11-02T11:00:00Z", "end": "2026-11-02T12:00:00Z", "zone": "UTC"}
	if msg := callTool(t, cs, "update_event", move, nil); msg != api.MCPMeetingForTest {
		t.Fatalf("moving a series with a meeting override: %q", msg)
	}
	after, _ := w.st.Calendars().GetObjectByUID(ctx, w.personal, "series")
	if after.ETag != before.ETag {
		t.Fatal("the refused edit changed the stored object")
	}
	if due, _ := w.st.Calendars().DueOutbox(ctx, time.Now().Add(48*time.Hour), 10); len(due) != 0 {
		t.Fatalf("the refused edit queued %d messages", len(due))
	}
}

// Each recipient's copy counts against the writer's storage, inside the write's transaction: a
// meeting whose invitations do not fit is refused whole (507), with nothing stored or queued.
func TestSchedulingOutboxCountsAgainstQuota(t *testing.T) {
	t.Setenv("KY_CALENDAR_MAX_BYTES_PER_USER", "20000")
	w := newSchedWorld(t, true)
	var atts []string
	for i := range 40 {
		atts = append(atts, "ATTENDEE:mailto:guest"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.net")
	}
	big := meeting("big", "alice", atts...)
	if code, _ := w.put("alice", "big.ics", big, ""); code != 507 {
		t.Fatalf("a meeting whose %d invitations exceed the quota: %d", len(atts), code)
	}
	if code, _, _ := w.get("alice", "big.ics"); code != 404 {
		t.Fatalf("the refused meeting was stored: GET %d", code)
	}
	if due, _ := w.st.Calendars().DueOutbox(context.Background(), time.Now().Add(48*time.Hour), 100); len(due) != 0 {
		t.Fatalf("the refused meeting queued %d messages", len(due))
	}
	if code, _ := w.put("alice", "small.ics", meeting("small", "alice", "ATTENDEE:mailto:bob@example.com"), ""); code != 201 {
		t.Fatalf("a small meeting: %d", code)
	}
}

// An answered copy is kept, cancelled, with the attendee's own reminders once each.
func TestSchedulingCancelKeepsAlarmsOnce(t *testing.T) {
	w := newSchedWorld(t, true)
	if code, _ := w.put("alice", "m-alarm.ics", meeting("m-alarm", "alice", "ATTENDEE:mailto:bob@example.com"), ""); code != 201 {
		t.Fatalf("invite PUT %d", code)
	}
	w.deliver()
	bobCopy, bobObj := w.copyOf("bob", "m-alarm")
	if bobObj == nil {
		t.Fatal("bob got no copy")
	}
	alarm := "BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:Planning\r\nTRIGGER:-PT10M\r\nEND:VALARM\r\nEND:VEVENT"
	r := rawDAV(t, w.ts, "PUT", "/dav/usr_bob/calendars/default/"+bobObj.Name, "bob", w.tokens["bob"], strings.Replace(strings.Replace(bobCopy, "END:VEVENT", alarm, 1), "PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED", 1),
		map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + bobObj.ETag + `"`})
	if r.StatusCode != 201 && r.StatusCode != 204 {
		t.Fatalf("bob's alarm: %d %s", r.StatusCode, readAll(r))
	}
	if r := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/m-alarm.ics", "alice", w.tokens["alice"], "", nil); r.StatusCode != 204 {
		t.Fatalf("delete %d", r.StatusCode)
	}
	w.deliver()
	if got, _ := w.copyOf("bob", "m-alarm"); !strings.Contains(got, "STATUS:CANCELLED") || strings.Count(got, "BEGIN:VALARM") != 1 {
		t.Fatalf("bob's copy after the cancel:\n%s", got)
	}
}

// Bob deleted his "default" calendar and kept another: invitations land in that one.
func TestSchedulingInviteWithoutADefaultCalendar(t *testing.T) {
	w := newSchedWorld(t, true)
	ctx := context.Background()
	def, err := w.st.Calendars().GetCalendarBySlug(ctx, "user", "usr_bob", "default")
	if err != nil {
		if code, _ := w.put("bob", "seed.ics", meeting("seed", "bob"), ""); code != 201 {
			t.Fatalf("seed PUT %d", code)
		}
		if def, err = w.st.Calendars().GetCalendarBySlug(ctx, "user", "usr_bob", "default"); err != nil {
			t.Fatal(err)
		}
	}
	work := &store.Calendar{ID: "cal_bob_work", OwnerKind: "user", OwnerID: "usr_bob", Slug: "work", Name: "Work"}
	if err := w.st.Calendars().CreateCalendar(ctx, work, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.st.Calendars().DeleteCalendar(ctx, def.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := w.put("alice", "m-nodef.ics", meeting("m-nodef", "alice", "ATTENDEE:mailto:bob@example.com"), ""); code != 201 {
		t.Fatalf("invite PUT %d", code)
	}
	w.deliver()
	if _, err := w.st.Calendars().GetObjectByUID(ctx, work.ID, "m-nodef"); err != nil {
		t.Fatalf("bob's invitation did not land in his remaining calendar: %v", err)
	}
}

// Bob deleted his default calendar and kept only a task list: the invitation still reaches him,
// in a default calendar made again, instead of failing for good.
func TestSchedulingInviteWithOnlyATaskList(t *testing.T) {
	w := newSchedWorld(t, true)
	ctx := context.Background()
	if code, _ := w.put("bob", "seed.ics", meeting("seed", "bob"), ""); code != 201 {
		t.Fatalf("seed PUT %d", code)
	}
	def, err := w.st.Calendars().GetCalendarBySlug(ctx, "user", "usr_bob", "default")
	if err != nil {
		t.Fatal(err)
	}
	tasks := &store.Calendar{ID: "cal_bob_tasks", OwnerKind: "user", OwnerID: "usr_bob", Slug: "tasks", Name: "Tasks", Components: []string{"VTODO"}}
	if err := w.st.Calendars().CreateCalendar(ctx, tasks, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.st.Calendars().DeleteCalendar(ctx, def.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := w.put("alice", "m-tasks.ics", meeting("m-tasks", "alice", "ATTENDEE:mailto:bob@example.com"), ""); code != 201 {
		t.Fatalf("invite PUT %d", code)
	}
	w.deliver()
	if got, obj := w.copyOf("bob", "m-tasks"); obj == nil {
		t.Fatalf("bob's invitation did not land in a new default calendar: %q", got)
	}
}

// Bob's "default" is a task list and he has no event calendar: the invitation lands in a new one.
func TestSchedulingInviteWhenDefaultHoldsTasks(t *testing.T) {
	w := newSchedWorld(t, true)
	ctx := context.Background()
	if code, _ := w.put("bob", "seed.ics", meeting("seed", "bob"), ""); code != 201 {
		t.Fatalf("seed PUT %d", code)
	}
	def, err := w.st.Calendars().GetCalendarBySlug(ctx, "user", "usr_bob", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.st.Calendars().DeleteCalendar(ctx, def.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_bob_td", OwnerKind: "user", OwnerID: "usr_bob", Slug: "default", Name: "Tasks", Components: []string{"VTODO"}}, 0); err != nil {
		t.Fatal(err)
	}
	if code, _ := w.put("alice", "m-td.ics", meeting("m-td", "alice", "ATTENDEE:mailto:bob@example.com"), ""); code != 201 {
		t.Fatalf("invite PUT %d", code)
	}
	w.deliver()
	cals, err := w.st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if _, err := w.st.Calendars().GetObjectByUID(ctx, c.ID, "m-td"); err == nil && c.Holds("VEVENT") {
			return
		}
	}
	t.Fatal("bob's invitation landed nowhere")
}

// Alice's meeting lives in a group calendar bob can also edit. Bob's copy is his own: delivering to
// him never rewrites or cancels alice's object in the shared calendar.
func TestSchedulingLeavesAGroupCalendarMeetingAlone(t *testing.T) {
	w := newSchedWorld(t, true)
	team := groupCalendar(t, w.st, "Team")
	grantRole(t, w.st, team, "manager", "usr_alice")
	grantRole(t, w.st, team, "editor", "usr_bob")
	path := "/dav/usr_alice/calendars/_" + team.ID + "/m-team.ics"
	body := meeting("m-team", "alice", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com", "ATTENDEE:mailto:bob@example.com", "ATTENDEE:mailto:mallory@example.com")
	if r := rawDAV(t, w.ts, "PUT", path, "alice", w.tokens["alice"], body, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("invite PUT %d", r.StatusCode)
	}
	w.deliver()
	stored, err := w.st.Calendars().GetObjectByUID(context.Background(), team.ID, "m-team")
	if err != nil || string(stored.Data) != body {
		t.Fatalf("delivery to bob rewrote alice's object in the group calendar (%v):\n%s", err, stored.Data)
	}
	if got, _ := w.copyOf("bob", "m-team"); got == "" {
		t.Fatal("bob got no copy of his own")
	}
	dropped := strings.Replace(body, "ATTENDEE:mailto:bob@example.com\r\n", "", 1)
	if r := rawDAV(t, w.ts, "PUT", path, "alice", w.tokens["alice"], dropped, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + stored.ETag + `"`}); r.StatusCode != 201 && r.StatusCode != 204 {
		t.Fatalf("removing bob: %d %s", r.StatusCode, readAll(r))
	}
	w.deliver()
	if stored, _ := w.st.Calendars().GetObjectByUID(context.Background(), team.ID, "m-team"); strings.Contains(string(stored.Data), "CANCELLED") {
		t.Fatalf("removing bob cancelled the meeting for the whole team:\n%s", stored.Data)
	}
	if got, obj := w.copyOf("bob", "m-team"); obj != nil {
		t.Fatalf("bob's own unanswered copy after he was removed:\n%s", got)
	}
	if _, err := w.st.Calendars().GetObjectByUID(context.Background(), team.ID, "m-team"); err != nil {
		t.Fatalf("removing bob deleted alice's meeting from the group calendar: %v", err)
	}
}

// Bob answers one occurrence of a weekly meeting alice stores as a master alone: alice's copy
// gains that occurrence's override with his answer, and the series keeps its own.
func TestSchedulingReplyToOneOccurrenceOfAPlainSeries(t *testing.T) {
	w := newSchedWorld(t, true)
	invite := meeting("m-occ", "alice", "SEQUENCE:0", "RRULE:FREQ=WEEKLY;COUNT=4",
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com", "ATTENDEE;RSVP=TRUE:mailto:bob@example.com")
	if code, _ := w.put("alice", "m-occ.ics", invite, ""); code != 201 {
		t.Fatalf("invite PUT %d", code)
	}
	w.deliver()
	bobCopy, bobObj := w.copyOf("bob", "m-occ")
	if bobObj == nil {
		t.Fatal("bob got no copy")
	}
	override := "BEGIN:VEVENT\r\nUID:m-occ\r\nDTSTAMP:20261001T000000Z\r\nRECURRENCE-ID:20261117T090000Z\r\n" +
		"DTSTART:20261117T090000Z\r\nDTEND:20261117T100000Z\r\nSUMMARY:Planning\r\nSEQUENCE:0\r\n" +
		"ORGANIZER:mailto:alice@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com\r\n" +
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com\r\nEND:VEVENT\r\n"
	answered := strings.Replace(bobCopy, "END:VCALENDAR", override+"END:VCALENDAR", 1)
	r := rawDAV(t, w.ts, "PUT", "/dav/usr_bob/calendars/default/"+bobObj.Name, "bob", w.tokens["bob"], answered,
		map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + bobObj.ETag + `"`})
	if r.StatusCode != 201 && r.StatusCode != 204 {
		t.Fatalf("bob's answer: %d %s", r.StatusCode, readAll(r))
	}
	w.deliver()
	_, alices, _ := w.get("alice", "m-occ.ics")
	parts := strings.Split(alices, "BEGIN:VEVENT")
	if len(parts) != 3 {
		t.Fatalf("alice's object should hold the series and one override:\n%s", alices)
	}
	series, occ := parts[1], parts[2]
	if strings.Contains(series, "RECURRENCE-ID") {
		series, occ = occ, series
	}
	if !strings.Contains(occ, "RECURRENCE-ID:20261117T090000Z") || !strings.Contains(occ, "DTSTART:20261117T090000Z") ||
		!strings.Contains(occ, "PARTSTAT=ACCEPTED;RSVP=TRUE:mailto:bob@example.com") {
		t.Fatalf("the override must sit on its own date with bob's answer:\n%s", alices)
	}
	if !strings.Contains(series, "RRULE:FREQ=WEEKLY;COUNT=4") || !strings.Contains(series, "ATTENDEE;RSVP=TRUE:mailto:bob@example.com") {
		t.Fatalf("the series must keep bob's own answer:\n%s", alices)
	}
}

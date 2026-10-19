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

	// Alice deletes it: bob's copy stays, cancelled.
	if r := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/m1.ics", "alice", w.tokens["alice"], "", nil); r.StatusCode != 204 {
		t.Fatalf("delete %d", r.StatusCode)
	}
	w.deliver()
	if got, _ := w.copyOf("bob", "m1"); !strings.Contains(got, "STATUS:CANCELLED") {
		t.Fatalf("bob's copy after the cancel:\n%s", got)
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

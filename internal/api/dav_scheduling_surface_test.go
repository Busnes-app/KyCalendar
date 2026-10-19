package api_test

import (
	"strings"
	"testing"
)

const schedPrincipalProps = `<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop>
<c:calendar-user-address-set/><c:schedule-inbox-URL/><c:schedule-outbox-URL/></d:prop></d:propfind>`

func fbRequest(organizer string, attendees ...string) string {
	var atts strings.Builder
	for _, a := range attendees {
		atts.WriteString("ATTENDEE:mailto:" + a + "\r\n")
	}
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nMETHOD:REQUEST\r\nBEGIN:VFREEBUSY\r\nUID:fbq\r\nDTSTAMP:20261101T000000Z\r\n" +
		"DTSTART:20261110T000000Z\r\nDTEND:20261111T000000Z\r\nORGANIZER:mailto:" + organizer + "\r\n" + atts.String() + "END:VFREEBUSY\r\nEND:VCALENDAR\r\n"
}

func (w *schedWorld) outbox(user, body string) (int, string) {
	w.t.Helper()
	r := rawDAV(w.t, w.ts, "POST", "/dav/usr_"+user+"/calendars/_outbox/", user, w.tokens[user], body, map[string]string{"Content-Type": "text/calendar"})
	return r.StatusCode, readAll(r)
}

func TestSchedulingSurfaceAdvertised(t *testing.T) {
	w := newSchedWorld(t, true)
	if dav := rawDAV(t, w.ts, "OPTIONS", "/dav/usr_alice/calendars/default/", "alice", w.tokens["alice"], "", nil).Header.Get("DAV"); !strings.Contains(dav, "calendar-auto-schedule") {
		t.Errorf("DAV %q", dav)
	}
	r := rawDAV(t, w.ts, "PROPFIND", "/dav/usr_alice/", "alice", w.tokens["alice"], schedPrincipalProps, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	body := readAll(r)
	for _, want := range []string{"mailto:alice@example.com", "/dav/usr_alice/calendars/_inbox/", "/dav/usr_alice/calendars/_outbox/"} {
		if !strings.Contains(body, want) {
			t.Errorf("principal lacks %q:\n%s", want, body)
		}
	}
	inbox := readAll(rawDAV(t, w.ts, "PROPFIND", "/dav/usr_alice/calendars/_inbox/", "alice", w.tokens["alice"], `<d:propfind xmlns:d="DAV:"><d:allprop/></d:propfind>`, map[string]string{"Depth": "1", "Content-Type": "application/xml"}))
	if !strings.Contains(inbox, "schedule-inbox") || !strings.Contains(inbox, "/dav/usr_alice/calendars/default/") {
		t.Errorf("inbox:\n%s", inbox)
	}
	sync := `<d:sync-collection xmlns:d="DAV:"><d:sync-token></d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	if r := rawDAV(t, w.ts, "REPORT", "/dav/usr_alice/calendars/_inbox/", "alice", w.tokens["alice"], sync, map[string]string{"Content-Type": "application/xml"}); r.StatusCode != 207 {
		t.Errorf("inbox sync %d %s", r.StatusCode, readAll(r))
	}
	// Bob's inbox is not alice's.
	if r := rawDAV(t, w.ts, "PROPFIND", "/dav/usr_bob/calendars/_inbox/", "alice", w.tokens["alice"], "", map[string]string{"Depth": "0"}); r.StatusCode != 403 {
		t.Errorf("another user's inbox: %d", r.StatusCode)
	}
}

func TestSchedulingSurfaceNeedsAnAddress(t *testing.T) {
	w := newSchedWorld(t, true)
	tok := davUser(t, w.st, "noemail", "user") // no email: no calendar user address
	if dav := rawDAV(t, w.ts, "OPTIONS", "/dav/usr_noemail/calendars/default/", "noemail", tok, "", nil).Header.Get("DAV"); strings.Contains(dav, "auto-schedule") {
		t.Errorf("a user without an address is told the server schedules: %q", dav)
	}
}

func TestSchedulingOutboxFreeBusy(t *testing.T) {
	w := newSchedWorld(t, true)
	busy := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:bob-secret\r\nDTSTAMP:20261001T000000Z\r\n" +
		"DTSTART:20261110T130000Z\r\nDTEND:20261110T140000Z\r\nSUMMARY:Secret interview\r\nLOCATION:Room 9\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if code, _ := w.put("bob", "secret.ics", busy, ""); code != 201 {
		t.Fatal("bob's event")
	}
	code, body := w.outbox("alice", fbRequest("alice@example.com", "bob@example.com", "carol@example.net"))
	if code != 200 {
		t.Fatalf("outbox %d %s", code, body)
	}
	for _, want := range []string{"mailto:bob@example.com", "2.0;Success", "FREEBUSY:20261110T130000Z/20261110T140000Z", "mailto:carol@example.net", "3.7;Invalid calendar user"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
	for _, leak := range []string{"Secret", "Room 9", "bob-secret"} {
		if strings.Contains(body, leak) {
			t.Errorf("free/busy reveals %q:\n%s", leak, body)
		}
	}
	if code, body := w.outbox("alice", fbRequest("bob@example.com", "bob@example.com")); code != 403 {
		t.Errorf("asking as someone else: %d %s", code, body)
	}
	many := make([]string, 51)
	for i := range many {
		many[i] = "bob@example.com"
	}
	if code, _ := w.outbox("alice", fbRequest("alice@example.com", many...)); code != 403 {
		t.Errorf("51 recipients: %d", code)
	}
}

func TestSchedulingScheduleTag(t *testing.T) {
	w := newSchedWorld(t, true)
	invite := meeting("st1", "alice", "ATTENDEE:mailto:bob@example.com")
	if code, _ := w.put("alice", "st1.ics", invite, ""); code != 201 {
		t.Fatal("invite")
	}
	r := rawDAV(t, w.ts, "GET", "/dav/usr_alice/calendars/default/st1.ics", "alice", w.tokens["alice"], "", nil)
	tag, etag := r.Header.Get("Schedule-Tag"), r.Header.Get("ETag")
	if tag == "" {
		t.Fatal("no Schedule-Tag on a scheduling object")
	}
	w.deliver()
	_, bobObj := w.copyOf("bob", "st1")
	accepted := strings.Replace(string(bobObj.Data), "ATTENDEE:mailto:bob@example.com", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com", 1)
	accepted = strings.Replace(accepted, "PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED", 1)
	rawDAV(t, w.ts, "PUT", "/dav/usr_bob/calendars/default/"+bobObj.Name, "bob", w.tokens["bob"], accepted, map[string]string{"Content-Type": "text/calendar"})
	w.deliver()
	r = rawDAV(t, w.ts, "GET", "/dav/usr_alice/calendars/default/st1.ics", "alice", w.tokens["alice"], "", nil)
	if got := r.Header.Get("Schedule-Tag"); got != tag || r.Header.Get("ETag") == etag {
		t.Errorf("after bob's answer: Schedule-Tag %q (want unchanged %q), ETag %q (want changed)", got, tag, r.Header.Get("ETag"))
	}
	ct := map[string]string{"Content-Type": "text/calendar", "If-Schedule-Tag-Match": `"stale"`}
	if r := rawDAV(t, w.ts, "PUT", "/dav/usr_alice/calendars/default/st1.ics", "alice", w.tokens["alice"], invite, ct); r.StatusCode != 412 {
		t.Errorf("stale If-Schedule-Tag-Match: %d", r.StatusCode)
	}
	ct["If-Schedule-Tag-Match"] = tag
	if r := rawDAV(t, w.ts, "PUT", "/dav/usr_alice/calendars/default/st1.ics", "alice", w.tokens["alice"], strings.Replace(invite, "SUMMARY:Planning", "SUMMARY:Planning v2", 1), ct); r.StatusCode != 201 {
		t.Errorf("current If-Schedule-Tag-Match: %d %s", r.StatusCode, readAll(r))
	}
	plain := strings.Replace(meeting("plain", "alice"), "ORGANIZER:mailto:alice@example.com\r\n", "", 1)
	w.put("alice", "plain.ics", plain, "")
	if tag := rawDAV(t, w.ts, "GET", "/dav/usr_alice/calendars/default/plain.ics", "alice", w.tokens["alice"], "", nil).Header.Get("Schedule-Tag"); tag != "" {
		t.Errorf("a plain event has a Schedule-Tag %q", tag)
	}
}

func TestSchedulingOffKeepsSurface(t *testing.T) {
	w := newSchedWorld(t, false)
	if dav := rawDAV(t, w.ts, "OPTIONS", "/dav/usr_alice/calendars/default/", "alice", w.tokens["alice"], "", nil).Header.Get("DAV"); strings.Contains(dav, "auto-schedule") {
		t.Errorf("DAV %q with scheduling off", dav)
	}
	if code, _ := w.outbox("alice", fbRequest("alice@example.com", "bob@example.com")); code != 405 {
		t.Errorf("outbox POST with scheduling off: %d", code)
	}
	if tag := rawDAV(t, w.ts, "PUT", "/dav/usr_alice/calendars/default/x.ics", "alice", w.tokens["alice"], meeting("x", "alice", "ATTENDEE:mailto:bob@example.com"), map[string]string{"Content-Type": "text/calendar"}).Header.Get("Schedule-Tag"); tag != "" {
		t.Errorf("Schedule-Tag %q with scheduling off", tag)
	}
}

package api_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

func TestDeliveryBindingReorderAndOrdinarySequence(t *testing.T) {
	w, _ := cancellationWorld(t, 12000, 50000)
	master := meeting("binding-series", "alice", "SEQUENCE:0", "RRULE:FREQ=DAILY;COUNT=3", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	var overrides strings.Builder
	for i, day := range []string{"11", "12"} {
		ov := meeting("binding-series", "alice", "SEQUENCE:"+string(rune('1'+i)), "RECURRENCE-ID:202611"+day+"T090000Z", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
		ov = strings.ReplaceAll(ov, "20261110T", "202611"+day+"T")
		first, last := strings.Index(ov, "BEGIN:VEVENT"), strings.Index(ov, "END:VEVENT")+len("END:VEVENT\r\n")
		overrides.WriteString(ov[first:last])
	}
	body := strings.Replace(master, "END:VCALENDAR", overrides.String()+"END:VCALENDAR", 1)
	if code, _ := w.put("alice", "binding.ics", body, ""); code != 201 {
		t.Fatalf("series admission: %d", code)
	}
	w.deliver()
	original, copy := w.copyOf("bob", "binding-series")
	if copy == nil {
		t.Fatal("no delivered series")
	}
	base := meeting("full-binding", "bob")
	filler := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", 12000-len(base)-len("DESCRIPTION:\r\n"))+"\r\nEND:VEVENT", 1)
	if code, _ := w.put("bob", "full.ics", filler, ""); code != 201 {
		t.Fatalf("fill Bob: %d", code)
	}
	path := "/dav/usr_bob/calendars/default/" + copy.Name
	poisoned := strings.Replace(original, "X-KYCALENDAR-ORGANIZER-SEQUENCE:0", "X-KYCALENDAR-ORGANIZER-SEQUENCE:9", 1)
	if poisoned == original || len(poisoned) != len(original) {
		t.Fatal("not an equal-size binding change")
	}
	res := rawDAV(t, w.ts, "PUT", path, "bob", w.tokens["bob"], poisoned, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
	if res.StatusCode != 507 {
		t.Fatalf("full recipient replaced cancellation binding: %d %s", res.StatusCode, readAll(res))
	}
	// A component permutation with a same-size ordinary SEQUENCE edit preserves every delivery binding.
	cal := decode(t, original)
	if len(cal.Children) != 3 {
		t.Fatalf("expected master and two overrides, got %d", len(cal.Children))
	}
	for _, ev := range cal.Children {
		if ev.Props.Get(ical.PropRecurrenceID) == nil {
			ev.Props.Set(&ical.Prop{Name: ical.PropSequence, Value: "9"})
		}
	}
	cal.Children[0], cal.Children[2] = cal.Children[2], cal.Children[0]
	var raw bytes.Buffer
	if err := ical.NewEncoder(&raw).Encode(cal); err != nil {
		t.Fatal(err)
	}
	if raw.Len() != len(original) {
		t.Fatalf("reorder/ordinary sequence unexpectedly grew payload: %d -> %d", len(original), raw.Len())
	}
	res = rawDAV(t, w.ts, "PUT", path, "bob", w.tokens["bob"], raw.String(), map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
	if res.StatusCode != 201 && res.StatusCode != 204 {
		t.Fatalf("harmless component reordering/ordinary sequence caused reassignment: %d %s", res.StatusCode, readAll(res))
	}
	_, stored := w.copyOf("bob", "binding-series")
	if stored == nil || stored.InvitationSender != "usr_alice" {
		t.Fatal("unchanged binding reassigned charge")
	}
	_, _, etag := w.get("alice", "binding.ics")
	res = rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/binding.ics", "alice", w.tokens["alice"], "", map[string]string{"If-Match": etag})
	if res.StatusCode != 204 {
		t.Fatalf("cancel after refused binding rewrite: %d %s", res.StatusCode, readAll(res))
	}
	w.deliver()
	if _, remaining := w.copyOf("bob", "binding-series"); remaining != nil {
		t.Fatal("valid cancellation did not reclaim original sender-paid copy")
	}
}

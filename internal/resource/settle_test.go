package resource

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
)

var roomID = strings.Repeat("ab", 16)

func parse(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(s, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func event(start, end, extra string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:u1\nDTSTAMP:20261101T000000Z\nDTSTART:" + start +
		"\nDTEND:" + end + "\nSUMMARY:Secret\nORGANIZER:mailto:ann@example.com\nATTENDEE;PARTSTAT=ACCEPTED;RSVP=TRUE:mailto:resource-" + roomID +
		"@kycalendar.invalid\n" + extra + "END:VEVENT\nEND:VCALENDAR\n"
}

func lastEnd(t *testing.T, cal *ical.Calendar) *int64 {
	t.Helper()
	o, err := calendar.Inspect(cal)
	if err != nil {
		t.Fatal(err)
	}
	return o.LastEnd
}

var room = &Resource{ID: roomID, Kind: Room, Name: "Fjord", Enabled: true, Audience: Everyone, Approval: Auto, MaxMinutes: 120, HorizonDays: 30}

func TestPlan(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, ics, status, reason string
		r                         *Resource
	}{
		{"free", event("20261120T090000Z", "20261120T100000Z", ""), Accepted, "", room},
		{"unknown", event("20261120T090000Z", "20261120T100000Z", ""), Declined, ReasonUnknown, nil},
		{"past", event("20261101T090000Z", "20261101T100000Z", ""), Declined, ReasonPast, room},
		{"beyond", event("20270120T090000Z", "20270120T100000Z", ""), Declined, ReasonBeyond, room},
		{"long", event("20261120T090000Z", "20261120T120000Z", ""), Declined, ReasonTooLong, room},
		{"open series", event("20261120T090000Z", "20261120T100000Z", "RRULE:FREQ=WEEKLY\n"), Accepted, "", room},
		{"far series", event("20270120T090000Z", "20270120T100000Z", "RRULE:FREQ=WEEKLY\n"), Declined, ReasonBeyond, room},
		{"hourly", event("20261120T090000Z", "20261120T093000Z", "RRULE:FREQ=MINUTELY;INTERVAL=1\n"), Declined, ReasonOccurrences, room},
	}
	for _, c := range cases {
		cal := parse(t, c.ics)
		d := Plan(cal, c.r, lastEnd(t, cal), now)
		if d.Status != c.status || d.Reason != c.reason {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
	disabled := *room
	disabled.Enabled = false
	cal := parse(t, event("20261120T090000Z", "20261120T100000Z", ""))
	if d := Plan(cal, &disabled, lastEnd(t, cal), now); d.Reason != ReasonDisabled {
		t.Errorf("disabled: %+v", d)
	}
	approved := *room
	approved.Approval = Approvers
	if d := Plan(cal, &approved, lastEnd(t, cal), now); d.Status != Pending || len(d.Spans) != 1 {
		t.Errorf("approval: %+v", d)
	}
	series := parse(t, event("20261120T090000Z", "20261120T100000Z", "RRULE:FREQ=WEEKLY\n"))
	d := Plan(series, room, lastEnd(t, series), now)
	if d.OpenAfter != now.Add(30*24*time.Hour).Unix() || len(d.Spans) != 4 {
		t.Fatalf("open series: %d spans, open after %d", len(d.Spans), d.OpenAfter)
	}
	if got := OpenSpans(series, d.OpenAfter, d.OpenAfter, d.OpenAfter+14*24*3600); len(got) != 2 {
		t.Fatalf("open spans %v", got)
	}
}

func TestConflicts(t *testing.T) {
	want := []Span{{100, 200}, {300, 400}, {500, 600}}
	busy := []Span{{150, 160}, {400, 500}, {590, 700}}
	got := Conflicts(want, busy)
	if len(got) != 2 || got[0] != 100 || got[1] != 500 {
		t.Fatalf("conflicts %v", got)
	}
	day := int64(1795000000)
	if d := ConflictDetail([]int64{day, day + 60, day + 86400}); strings.Count(d, ",") != 1 {
		t.Fatalf("detail %q", d)
	}
}

func TestFingerprintIgnoresText(t *testing.T) {
	a := parse(t, event("20261120T090000Z", "20261120T100000Z", ""))
	b := parse(t, strings.Replace(event("20261120T090000Z", "20261120T100000Z", "DESCRIPTION:x\n"), "SUMMARY:Secret", "SUMMARY:Other", 1))
	c := parse(t, event("20261120T100000Z", "20261120T110000Z", ""))
	if Fingerprint(a) != Fingerprint(b) || Fingerprint(a) == Fingerprint(c) {
		t.Fatal("fingerprint must follow times only")
	}
}

func TestSetAnswersOverridesTheClient(t *testing.T) {
	cal := parse(t, event("20261120T090000Z", "20261120T100000Z", ""))
	if !SetAnswers(cal, map[string]string{roomID: Declined}, map[string]*Resource{roomID: room}) {
		t.Fatal("no change")
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	out := strings.ReplaceAll(buf.String(), "\r\n ", "")
	for _, want := range []string{"PARTSTAT=DECLINED", "CUTYPE=ROOM", "CN=Fjord", "ROLE=NON-PARTICIPANT"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "RSVP") || strings.Contains(out, "ACCEPTED") {
		t.Errorf("client answer kept:\n%s", out)
	}
	if SetAnswers(cal, map[string]string{roomID: Declined}, map[string]*Resource{roomID: room}) {
		t.Error("a second pass changed it again")
	}
}

func TestSetList(t *testing.T) {
	ann := itip.Address("mailto:ann@example.com")
	cal := parse(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:u1\nDTSTAMP:20261101T000000Z\nDTSTART:20261120T090000Z\nDTEND:20261120T100000Z\nSUMMARY:x\nEND:VEVENT\nEND:VCALENDAR\n")
	room2 := *room
	room2.Location = "Floor 2"
	if err := SetList(cal, ann, []string{roomID}, map[string]*Resource{roomID: &room2}); err != nil {
		t.Fatal(err)
	}
	if org, _ := itip.Organizer(cal); org != ann || IDs(cal)[0] != roomID {
		t.Fatalf("organizer %s ids %v", org, IDs(cal))
	}
	if loc, _ := cal.Children[0].Props.Text(ical.PropLocation); loc != "Fjord, Floor 2" {
		t.Fatalf("location %q", loc)
	}
	if err := SetList(cal, "mailto:bob@example.com", nil, nil); err != ErrNotOrganizer {
		t.Fatalf("another organizer: %v", err)
	}
	if err := SetList(cal, ann, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := itip.Organizer(cal); ok || len(IDs(cal)) != 0 || cal.Children[0].Props.Get(ical.PropAttendee) != nil {
		t.Fatal("removing the last room leaves a meeting")
	}
}

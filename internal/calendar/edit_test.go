package calendar

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

var now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// roundTrip encodes and decodes, as the store and a client would.
func roundTrip(t *testing.T, cal *ical.Calendar) (*ical.Calendar, string) {
	t.Helper()
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	out, err := ical.NewDecoder(bytes.NewReader(buf.Bytes())).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return out, buf.String()
}

func TestNewEventRoundTripsAcrossZones(t *testing.T) {
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "Planning", Start: time.Date(2026, 10, 7, 9, 0, 0, 0, berlin), End: time.Date(2026, 10, 7, 10, 0, 0, 0, berlin), Zone: berlin}
	cal, raw := roundTrip(t, NewEvent("uid-1", in, now))
	if !strings.Contains(raw, "BEGIN:VTIMEZONE") || !strings.Contains(raw, "DTSTART;TZID=Europe/Berlin:20261007T090000") {
		t.Fatalf("missing zone data:\n%s", raw)
	}
	if _, err := Inspect(cal); err != nil {
		t.Fatalf("a new event must pass Inspect: %v", err)
	}
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 10, 31), zone(t, "America/New_York"), 10)
	if err != nil || len(got) != 1 || !got[0].Start.Equal(in.Start) {
		t.Fatalf("expanded %v %v; want %v", got, err, in.Start)
	}
}

func TestNewEventUTCAndAllDay(t *testing.T) {
	_, raw := roundTrip(t, NewEvent("u", EventInput{Start: day(2026, 10, 7).Add(9 * time.Hour), End: day(2026, 10, 7).Add(10 * time.Hour), Zone: time.UTC}, now))
	if strings.Contains(raw, "VTIMEZONE") || !strings.Contains(raw, "DTSTART:20261007T090000Z") {
		t.Fatalf("UTC event:\n%s", raw)
	}
	_, raw = roundTrip(t, NewEvent("a", EventInput{Start: day(2026, 10, 7), End: day(2026, 10, 8), AllDay: true, Repeat: Repeat{Freq: "yearly"}}, now))
	for _, want := range []string{"DTSTART;VALUE=DATE:20261007", "DTEND;VALUE=DATE:20261008", "RRULE:FREQ=YEARLY", "PRODID:" + ProductID} {
		if !strings.Contains(raw, want) {
			t.Fatalf("all-day event lacks %q:\n%s", want, raw)
		}
	}
}

func TestRepeatOfAndPresets(t *testing.T) {
	cases := map[string]Repeat{
		"":                                   {},
		"FREQ=DAILY":                         {Freq: "daily"},
		"FREQ=WEEKLY":                        {Freq: "weekly"},
		"FREQ=WEEKLY;BYDAY=MO,WE":            {Freq: "weekly", Weekdays: []time.Weekday{time.Monday, time.Wednesday}},
		"FREQ=MONTHLY":                       {Freq: "monthly"},
		"FREQ=YEARLY;INTERVAL=1":             {Freq: "yearly"},
		"FREQ=WEEKLY;COUNT=3":                {Freq: "custom"},
		"FREQ=MONTHLY;BYDAY=-1FR":            {Freq: "custom"},
		"FREQ=DAILY;INTERVAL=2":              {Freq: "custom"},
		"FREQ=WEEKLY;UNTIL=20261231T000000Z": {Freq: "custom"},
	}
	for rule, want := range cases {
		comp := ical.NewEvent().Component
		if rule != "" {
			comp.Props.Set(&ical.Prop{Name: ical.PropRecurrenceRule, Params: ical.Params{}, Value: rule})
		}
		got := RepeatOf(comp)
		if got.Freq != want.Freq || len(got.Weekdays) != len(want.Weekdays) {
			t.Errorf("RepeatOf(%q) = %+v, want %+v", rule, got, want)
		}
	}
}

func TestEditOnePreservesUnknownProperties(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	in := EventInput{Title: "Standup (this week)", Start: time.Date(2026, 10, 26, 8, 30, 0, 0, time.UTC), End: time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC), Zone: zone(t, "Europe/Berlin"), Repeat: Repeat{Freq: "custom"}}
	if err := EditOne(cal, "20261026T080000Z", in, now); err != nil {
		t.Fatal(err)
	}
	out, raw := roundTrip(t, cal)
	for _, keep := range []string{"X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC", "ATTENDEE;CN=Bob", "BEGIN:VALARM", "RRULE:FREQ=WEEKLY;BYDAY=MO", "Standup (moved)"} {
		if !strings.Contains(raw, keep) {
			t.Fatalf("edit lost %q:\n%s", keep, raw)
		}
	}
	if !strings.Contains(raw, "RECURRENCE-ID;TZID=Europe/Berlin:20261026T090000") {
		t.Fatalf("override must name the occurrence in the master's zone:\n%s", raw)
	}
	got, _ := Expand(out, day(2026, 10, 25), day(2026, 10, 28), time.UTC, 10)
	if len(got) != 1 || !got[0].Override || got[0].Start.UTC().Format(time.RFC3339) != "2026-10-26T08:30:00Z" {
		t.Fatalf("edited occurrence: %+v", got)
	}
	if s, _ := got[0].Event.Props.Text(ical.PropSummary); s != "Standup (this week)" {
		t.Fatalf("summary %q", s)
	}
	if got[0].Event.Props.Get(ical.PropRecurrenceRule) != nil {
		t.Fatal("an override must not carry the master's RRULE")
	}
}

func TestEditAllTextKeepsOverrides(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "Daily standup", Start: time.Date(2026, 10, 5, 9, 0, 0, 0, berlin), End: time.Date(2026, 10, 5, 10, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "custom"}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	_, raw := roundTrip(t, cal)
	if strings.Contains(raw, "VALUE=TEXT") {
		t.Fatalf("an edit wrote VALUE=TEXT:\n%s", raw)
	}
	for _, keep := range []string{"SUMMARY:Daily standup", "Standup (moved)", "EXDATE", "BEGIN:VALARM", "SEQUENCE:1"} {
		if !strings.Contains(raw, keep) {
			t.Fatalf("text-only edit of all lost %q:\n%s", keep, raw)
		}
	}
}

func TestEditAllTimeChangeDropsExceptions(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "Standup", Start: time.Date(2026, 10, 5, 10, 0, 0, 0, berlin), End: time.Date(2026, 10, 5, 11, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "weekly", Weekdays: []time.Weekday{time.Monday}}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	_, raw := roundTrip(t, cal)
	if strings.Contains(raw, "Standup (moved)") || strings.Contains(raw, "EXDATE") || strings.Contains(raw, "RECURRENCE-ID") {
		t.Fatalf("exceptions referring to old instants survived:\n%s", raw)
	}
	if !strings.Contains(raw, "BEGIN:VALARM") || !strings.Contains(raw, "X-APPLE-TRAVEL-ADVISORY-BEHAVIOR") {
		t.Fatalf("unknown properties lost:\n%s", raw)
	}
}

func TestDeleteOneAllDayInAuckland(t *testing.T) {
	cal := fixture(t, "thunderbird_allday.ics")
	if err := DeleteOne(cal, "20271007", now); err != nil {
		t.Fatal(err)
	}
	out, raw := roundTrip(t, cal)
	if !strings.Contains(raw, "EXDATE;VALUE=DATE:20271007") {
		t.Fatalf("all-day EXDATE must be a DATE:\n%s", raw)
	}
	got, _ := Expand(out, day(2026, 1, 1), day(2028, 12, 31), zone(t, "Pacific/Auckland"), 10)
	var dates []string
	for _, in := range got {
		dates = append(dates, in.Start.Format("2006-01-02"))
	}
	if strings.Join(dates, ",") != "2026-10-07,2028-10-07" {
		t.Fatalf("dates after deleting 2027: %v", dates)
	}
}

func TestDeleteOneRemovesOverride(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	if err := DeleteOne(cal, "20261012T070000Z", now); err != nil {
		t.Fatal(err)
	}
	out, raw := roundTrip(t, cal)
	if strings.Contains(raw, "Standup (moved)") {
		t.Fatalf("deleting a moved occurrence must remove its override:\n%s", raw)
	}
	got, _ := Expand(out, day(2026, 10, 10), day(2026, 10, 15), time.UTC, 10)
	if len(got) != 0 {
		t.Fatalf("occurrence still shows: %+v", got)
	}
}

func TestEditErrors(t *testing.T) {
	single := fixture(t, "floating.ics")
	if err := EditOne(single, "20261007T090000", EventInput{}, now); !errors.Is(err, ErrNotRecurring) {
		t.Fatalf("EditOne on a single event: %v", err)
	}
	if err := DeleteOne(fixture(t, "lists.ics"), "garbage", now); !errors.Is(err, ErrNoSuchOccurrence) {
		t.Fatalf("bad recurrence id: %v", err)
	}
}

func decodeString(t *testing.T, raw string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(raw, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func encode(t *testing.T, cal *ical.Calendar) string {
	t.Helper()
	_, raw := roundTrip(t, cal)
	return raw
}

func recurring(t *testing.T, name, rule string) *ical.Calendar {
	t.Helper()
	cal := fixture(t, name)
	m, err := Master(cal)
	if err != nil {
		t.Fatal(err)
	}
	setRaw(m, ical.PropRecurrenceRule, rule)
	return cal
}

func starts(t *testing.T, cal *ical.Calendar, from, to time.Time) string {
	t.Helper()
	got, err := Expand(cal, from, to, time.UTC, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, in := range got {
		out = append(out, in.Start.Format("2006-01-02T15:04"))
	}
	return strings.Join(out, ",")
}

func TestDeleteOneWritesTZIDAsWritten(t *testing.T) {
	cal := recurring(t, "unknown_tz.ics", "FREQ=DAILY;COUNT=5")
	if err := DeleteOne(cal, "20261008T090000Z", now); err != nil {
		t.Fatal(err)
	}
	out, raw := roundTrip(t, cal)
	if !strings.Contains(raw, "EXDATE;TZID=Custom/Nowhere:20261008T090000") {
		t.Fatalf("EXDATE must carry the master's TZID:\n%s", raw)
	}
	if got := starts(t, out, day(2026, 10, 7), day(2026, 10, 12)); got != "2026-10-07T09:00,2026-10-09T09:00,2026-10-10T09:00,2026-10-11T09:00" {
		t.Fatalf("after delete: %s", got)
	}

	cal = fixture(t, "outlook_windows_tz.ics")
	if err := DeleteOne(cal, "20261102T140000Z", now); err != nil {
		t.Fatal(err)
	}
	out, raw = roundTrip(t, cal)
	if !strings.Contains(raw, "EXDATE;TZID=Eastern Standard Time:20261102T090000") && !strings.Contains(raw, `EXDATE;TZID="Eastern Standard Time":20261102T090000`) {
		t.Fatalf("EXDATE must carry the Windows TZID:\n%s", raw)
	}
	if got := starts(t, out, day(2026, 10, 25), day(2026, 11, 30)); got != "2026-10-26T09:00,2026-11-09T09:00" {
		t.Fatalf("after delete: %s", got)
	}
}

func TestEditOneKeepsMozillaTZID(t *testing.T) {
	cal := recurring(t, "thunderbird_mozilla_tz.ics", "FREQ=DAILY;COUNT=5")
	in := EventInput{Title: "x", Start: time.Date(2026, 10, 8, 9, 0, 0, 0, zone(t, "America/New_York")), End: time.Date(2026, 10, 8, 10, 0, 0, 0, zone(t, "America/New_York")), Zone: zone(t, "America/New_York"), Repeat: Repeat{Freq: "custom"}}
	if err := EditOne(cal, "20261008T130000Z", in, now); err != nil {
		t.Fatal(err)
	}
	if raw := encode(t, cal); !strings.Contains(raw, "RECURRENCE-ID;TZID=/mozilla.org/20050126_1/America/New_York:20261008T090000") {
		t.Fatalf("RECURRENCE-ID lost the Mozilla TZID:\n%s", raw)
	}
}

const floatingWeekly = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:fl
DTSTAMP:20261001T000000Z
DTSTART:20261007T090000
DTEND:20261007T100000
RRULE:FREQ=WEEKLY
EXDATE:20261014T090000
SUMMARY:Floating
END:VEVENT
BEGIN:VEVENT
UID:fl
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261021T090000
DTSTART:20261021T110000
DTEND:20261021T120000
SUMMARY:Moved
END:VEVENT
END:VCALENDAR
`

func TestEditAllTextOnFloatingKeepsExceptions(t *testing.T) {
	cal := decodeString(t, floatingWeekly)
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "New", Start: time.Date(2026, 10, 7, 9, 0, 0, 0, berlin), End: time.Date(2026, 10, 7, 10, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "weekly"}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	raw := encode(t, cal)
	for _, keep := range []string{"EXDATE:20261014T090000", "RECURRENCE-ID:20261021T090000", "DTSTART:20261007T090000", "DTEND:20261007T100000", "SUMMARY:New"} {
		if !strings.Contains(raw, keep) {
			t.Fatalf("lost %q:\n%s", keep, raw)
		}
	}
	if strings.Contains(raw, "TZID") || strings.Contains(raw, "VTIMEZONE") {
		t.Fatalf("floating series became zoned:\n%s", raw)
	}
	in.End = time.Date(2026, 10, 7, 11, 0, 0, 0, berlin)
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	if raw = encode(t, cal); !strings.Contains(raw, "DTEND:20261007T110000") || !strings.Contains(raw, "EXDATE:20261014T090000") || strings.Contains(raw, "TZID") {
		t.Fatalf("end change in floating form:\n%s", raw)
	}
}

func TestEditRefusesWrongOrNonexistentKeys(t *testing.T) {
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "x", Start: time.Date(2026, 10, 27, 9, 0, 0, 0, berlin), End: time.Date(2026, 10, 27, 10, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "custom"}}
	cal := fixture(t, "apple_weekly_override.ics")
	before := encode(t, cal)
	for _, key := range []string{"20261027T080000Z", "20261012T090000", "20261027", "20261102T083000Z"} {
		if err := EditOne(cal, key, in, now); !errors.Is(err, ErrNoSuchOccurrence) {
			t.Errorf("EditOne(%q) = %v", key, err)
		}
		if err := DeleteOne(cal, key, now); !errors.Is(err, ErrNoSuchOccurrence) {
			t.Errorf("DeleteOne(%q) = %v", key, err)
		}
	}
	if encode(t, cal) != before {
		t.Fatal("a refused edit changed the object")
	}
	if err := EditOne(recurring(t, "floating.ics", "FREQ=DAILY"), "20261008T090000Z", in, now); !errors.Is(err, ErrNoSuchOccurrence) {
		t.Fatalf("Z key on floating master: %v", err)
	}
	if err := EditOne(recurring(t, "floating.ics", "FREQ=DAILY"), "20261008T090000", in, now); err != nil {
		t.Fatalf("wall key on floating master: %v", err)
	}
}

func countOverrides(cal *ical.Calendar) int {
	n := 0
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) != nil {
			n++
		}
	}
	return n
}

func TestEditOneTwiceEditsOneOverrideWithoutAliasing(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "A", Start: time.Date(2026, 11, 2, 9, 0, 0, 0, berlin), End: time.Date(2026, 11, 2, 10, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "custom"}}
	if err := EditOne(cal, "20261102T080000Z", in, now); err != nil {
		t.Fatal(err)
	}
	in.Title = "B"
	if err := EditOne(cal, "20261102T080000Z", in, now); err != nil {
		t.Fatal(err)
	}
	if n := countOverrides(cal); n != 2 {
		t.Fatalf("want the fixture's override plus one, got %d", n)
	}
	m, _ := Master(cal)
	m.Props.Get(ical.PropAttendee).Params.Set(ical.ParamCommonName, "Eve")
	for _, c := range cal.Children {
		if rid := c.Props.Get(ical.PropRecurrenceID); rid != nil && strings.HasPrefix(rid.Value, "20261102") {
			if cn := c.Props.Get(ical.PropAttendee).Params.Get(ical.ParamCommonName); cn != "Bob" {
				t.Fatalf("override aliases the master's ATTENDEE: %q", cn)
			}
			if s, _ := c.Props.Text(ical.PropSummary); s != "B" {
				t.Fatalf("summary %q", s)
			}
		}
	}
}

func TestDeleteOneTwiceWritesOneExdate(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	for range 2 {
		if err := DeleteOne(cal, "20261102T080000Z", now); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(encode(t, cal), "20261102T090000"); n != 1 {
		t.Fatalf("want one EXDATE value, found %d", n)
	}
	m, _ := Master(cal)
	seq := m.Props.Get(ical.PropSequence).Value
	if err := DeleteOne(cal, "20261102T080000Z", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := m.Props.Get(ical.PropSequence).Value; got != seq {
		t.Fatalf("a no-op repeat bumped SEQUENCE %s -> %s", seq, got)
	}
}

func TestRepeatEqualIgnoresWeekdayOrder(t *testing.T) {
	a := Repeat{Freq: "weekly", Weekdays: []time.Weekday{time.Wednesday, time.Monday}}
	if !a.equal(Repeat{Freq: "weekly", Weekdays: []time.Weekday{time.Monday, time.Wednesday}}) {
		t.Fatal("order must not matter")
	}
}

func TestSequenceIncrements(t *testing.T) {
	cal := fixture(t, "floating.ics")
	m, _ := Master(cal)
	setRaw(m, ical.PropSequence, "4")
	in := EventInput{Start: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC), Zone: time.UTC}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	if raw := encode(t, cal); !strings.Contains(raw, "SEQUENCE:5") {
		t.Fatalf("SEQUENCE:\n%s", raw)
	}
	setRaw(m, ical.PropSequence, "2147483647")
	touch(m, now)
	if p := m.Props.Get(ical.PropSequence); p.Value != "2147483647" {
		t.Fatalf("SEQUENCE overflowed: %s", p.Value)
	}
}

func TestDeleteOneRemovesOverrideBehindExistingExdate(t *testing.T) {
	cal := decodeString(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:d
DTSTAMP:20261001T000000Z
DTSTART:20261005T090000Z
DTEND:20261005T100000Z
RRULE:FREQ=DAILY;COUNT=5
EXDATE:20261007T090000Z
END:VEVENT
BEGIN:VEVENT
UID:d
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261007T090000Z
DTSTART:20261007T110000Z
DTEND:20261007T120000Z
SUMMARY:Stale
END:VEVENT
END:VCALENDAR
`)
	if got := starts(t, cal, day(2026, 10, 7), day(2026, 10, 8)); got != "2026-10-07T11:00" {
		t.Fatalf("before: %s", got)
	}
	if err := DeleteOne(cal, "20261007T090000Z", now); err != nil {
		t.Fatal(err)
	}
	if got := starts(t, cal, day(2026, 10, 7), day(2026, 10, 8)); got != "" {
		t.Fatalf("override still shows: %s", got)
	}
	if countOverrides(cal) != 0 || strings.Count(encode(t, cal), "EXDATE") != 1 {
		t.Fatalf("override or EXDATE count wrong:\n%s", encode(t, cal))
	}
}

// An EXDATE written in UTC on a TZID master is the same occurrence; deleting it again is a no-op.
func TestDeleteOneRecognisesUTCExdateOnTZIDMaster(t *testing.T) {
	cal := decodeString(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:x
DTSTAMP:20261001T000000Z
DTSTART;TZID=Europe/Berlin:20261005T090000
DTEND;TZID=Europe/Berlin:20261005T100000
RRULE:FREQ=DAILY;COUNT=5
EXDATE:20261007T070000Z
END:VEVENT
END:VCALENDAR
`)
	before := encode(t, cal)
	if err := DeleteOne(cal, "20261007T070000Z", now); err != nil {
		t.Fatal(err)
	}
	if after := encode(t, cal); after != before {
		t.Fatalf("a repeated delete changed the object:\n%s", after)
	}
}

// Answering or retitling one occurrence makes an override with no time change: it must stay at
// that occurrence's own start and end, not jump to the series' first one.
func TestNewOverrideKeepsItsOccurrenceTimes(t *testing.T) {
	for _, tc := range []struct{ name, start, end, rid, want string }{
		{"utc", "DTSTART:20261110T090000Z", "DTEND:20261110T100000Z", "20261117T090000Z", "2026-11-10T09:00,2026-11-17T09:00,2026-11-24T09:00"},
		{"tzid", "DTSTART;TZID=Europe/Berlin:20261110T090000", "DTEND;TZID=Europe/Berlin:20261110T100000", "20261117T080000Z", "2026-11-10T09:00,2026-11-17T09:00,2026-11-24T09:00"},
		{"floating", "DTSTART:20261110T090000", "DTEND:20261110T100000", "20261117T090000", "2026-11-10T09:00,2026-11-17T09:00,2026-11-24T09:00"},
		{"all-day", "DTSTART;VALUE=DATE:20261110", "DTEND;VALUE=DATE:20261111", "20261117", "2026-11-10T00:00,2026-11-17T00:00,2026-11-24T00:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cal := decodeString(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:w\nDTSTAMP:20261001T000000Z\n"+
				tc.start+"\n"+tc.end+"\nRRULE:FREQ=WEEKLY;COUNT=3\nSUMMARY:Weekly\nEND:VEVENT\nEND:VCALENDAR\n")
			title := "Moved title"
			if err := EditText(cal, tc.rid, TextEdit{Title: &title}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := AddOverride(cal, "20261124"+tc.rid[8:]); err != nil {
				t.Fatal(err)
			}
			back, _ := roundTrip(t, cal)
			got, err := Expand(back, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), time.UTC, 50)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, in := range got {
				out = append(out, in.Start.Format("2006-01-02T15:04"))
				if !in.End.After(in.Start) {
					t.Errorf("%s ends at %s", in.RecurrenceID, in.End)
				}
			}
			if s := strings.Join(out, ","); s != tc.want {
				t.Fatalf("starts %s, want %s\n%s", s, tc.want, encode(t, cal))
			}
		})
	}
}

// A kept rule's UTC UNTIL moves with the series: moving four weekly meetings an hour later keeps
// all four.
func TestEditAllMoveKeepsTheLastOccurrenceBeforeUntil(t *testing.T) {
	berlin := zone(t, "Europe/Berlin")
	cal := decodeString(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:w\nDTSTAMP:20261001T000000Z\n"+
		"DTSTART;TZID=Europe/Berlin:20261005T090000\nDTEND;TZID=Europe/Berlin:20261005T100000\nRRULE:FREQ=WEEKLY;UNTIL=20261026T080000Z\nSUMMARY:Weekly\nEND:VEVENT\nEND:VCALENDAR\n")
	from, to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	if got := starts(t, cal, from, to); strings.Count(got, ",") != 3 {
		t.Fatalf("fixture: %s", got)
	}
	in := EventInput{Title: "Weekly", Start: time.Date(2026, 10, 5, 10, 0, 0, 0, berlin), End: time.Date(2026, 10, 5, 11, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "custom"}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	back, _ := roundTrip(t, cal)
	if got := starts(t, back, from, to); strings.Count(got, ",") != 3 {
		t.Fatalf("after moving an hour later: %s\n%s", got, encode(t, cal))
	}
}

// Moving a series to an earlier date across a DST change keeps the same end date and every
// occurrence up to it.
func TestEditAllMoveAcrossDSTKeepsTheEndDate(t *testing.T) {
	berlin := zone(t, "Europe/Berlin")
	cal := decodeString(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:w\nDTSTAMP:20261001T000000Z\n"+
		"DTSTART;TZID=Europe/Berlin:20261026T090000\nDTEND;TZID=Europe/Berlin:20261026T100000\nRRULE:FREQ=WEEKLY;UNTIL=20261116T080000Z\nSUMMARY:Weekly\nEND:VEVENT\nEND:VCALENDAR\n")
	in := EventInput{Title: "Weekly", Start: time.Date(2026, 10, 19, 10, 0, 0, 0, berlin), End: time.Date(2026, 10, 19, 11, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "custom"}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	back, _ := roundTrip(t, cal)
	got := starts(t, back, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if want := "2026-10-19T10:00,2026-10-26T10:00,2026-11-02T10:00,2026-11-09T10:00,2026-11-16T10:00"; got != want {
		t.Fatalf("got %s, want %s\n%s", got, want, encode(t, cal))
	}
}

// Moving a UTC series to the same wall time in another zone recomputes UNTIL there: every
// occurrence is kept.
func TestEditAllZoneChangeKeepsTheLastOccurrence(t *testing.T) {
	ny := zone(t, "America/New_York")
	cal := decodeString(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:w\nDTSTAMP:20261001T000000Z\n"+
		"DTSTART:20261102T140000Z\nDTEND:20261102T150000Z\nRRULE:FREQ=WEEKLY;UNTIL=20261123T140000Z\nSUMMARY:Weekly\nEND:VEVENT\nEND:VCALENDAR\n")
	in := EventInput{Title: "Weekly", Start: time.Date(2026, 11, 2, 14, 0, 0, 0, ny), End: time.Date(2026, 11, 2, 15, 0, 0, 0, ny), Zone: ny, Repeat: Repeat{Freq: "custom"}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	back, _ := roundTrip(t, cal)
	if got := starts(t, back, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)); strings.Count(got, ",") != 3 {
		t.Fatalf("after moving to New York: %s\n%s", got, encode(t, cal))
	}
}

// A kept rule that sets its own clock (BYHOUR) does not follow DTSTART's time of day: its UNTIL
// stays, and so does its last occurrence.
func TestEditAllKeepsUntilOfAClockRule(t *testing.T) {
	berlin := zone(t, "Europe/Berlin")
	cal := decodeString(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:w\nDTSTAMP:20261001T000000Z\n"+
		"DTSTART;TZID=Europe/Berlin:20261110T100000\nDTEND;TZID=Europe/Berlin:20261110T103000\nRRULE:FREQ=DAILY;BYHOUR=9,10;UNTIL=20261112T090000Z\nSUMMARY:Twice\nEND:VEVENT\nEND:VCALENDAR\n")
	in := EventInput{Title: "Twice", Start: time.Date(2026, 11, 10, 9, 0, 0, 0, berlin), End: time.Date(2026, 11, 10, 9, 30, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "custom"}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	back, raw := roundTrip(t, cal)
	if !strings.Contains(raw, "UNTIL=20261112T090000Z") || !strings.Contains(starts(t, back, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)), "2026-11-12T10:00") {
		t.Fatalf("the rule's own last occurrence was lost:\n%s", raw)
	}
}

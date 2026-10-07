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

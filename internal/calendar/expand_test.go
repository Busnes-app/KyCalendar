package calendar

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func fixture(t *testing.T, name string) *ical.Calendar {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "recurrence", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cal, err := ical.NewDecoder(f).Decode()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return cal
}

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// describe renders an instance as "<start> [flags]": a viewer-local date for all-day instances,
// otherwise the UTC instant.
func describe(in Instance) string {
	s := in.Start.UTC().Format(time.RFC3339)
	if in.AllDay {
		s = in.Start.Format("2006-01-02")
	}
	for flag, on := range map[string]bool{"override": in.Override, "floating": in.Floating, "unknown": in.UnknownZone, "partial": in.Partial} {
		if on {
			s += " " + flag
		}
	}
	return s
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestExpandCorpus(t *testing.T) {
	cases := []struct {
		file     string
		from, to time.Time
		viewer   string
		want     []string
	}{
		{"apple_weekly_override.ics", day(2026, 10, 1), day(2026, 11, 3), "UTC",
			[]string{"2026-10-05T07:00:00Z", "2026-10-13T08:00:00Z override", "2026-10-26T08:00:00Z", "2026-11-02T08:00:00Z"}},
		{"google_until_exdate.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-06T00:00:00Z", "2026-10-09T00:00:00Z"}},
		{"outlook_windows_tz.ics", day(2026, 10, 1), day(2026, 12, 1), "UTC",
			[]string{"2026-10-26T13:00:00Z", "2026-11-02T14:00:00Z", "2026-11-09T14:00:00Z"}},
		{"thunderbird_allday.ics", day(2026, 1, 1), day(2028, 12, 31), "Pacific/Auckland",
			[]string{"2026-10-07", "2027-10-07", "2028-10-07"}},
		{"thunderbird_mozilla_tz.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-07T13:00:00Z"}},
		{"lists.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-05T09:00:00Z", "2026-10-08T09:00:00Z", "2026-10-20T09:00:00Z", "2026-10-21T09:00:00Z"}},
		{"floating.ics", day(2026, 10, 1), day(2026, 10, 31), "Asia/Tokyo",
			[]string{"2026-10-07T00:00:00Z floating"}},
		{"floating.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-07T09:00:00Z floating"}},
		{"unknown_tz.ics", day(2026, 10, 1), day(2026, 10, 31), "Europe/Berlin",
			[]string{"2026-10-07T09:00:00Z unknown"}},
		{"override_moved.ics", day(2026, 10, 1), day(2026, 10, 15), "UTC",
			[]string{"2026-10-02T09:00:00Z override", "2026-10-05T09:00:00Z"}},
		{"minutely.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-07T09:00:00Z partial"}},
		{"unmatched_dtstart.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-06T13:00:00Z", "2026-10-12T13:00:00Z", "2026-10-19T13:00:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.file+"/"+tc.viewer, func(t *testing.T) {
			got, err := Expand(fixture(t, tc.file), tc.from, tc.to, zone(t, tc.viewer), 5000)
			if err != nil {
				t.Fatal(err)
			}
			var desc []string
			for _, in := range got {
				desc = append(desc, describe(in))
			}
			if !reflect.DeepEqual(desc, tc.want) {
				t.Fatalf("got  %v\nwant %v", desc, tc.want)
			}
		})
	}
}

func TestExpandRecurrenceIDsAndEvents(t *testing.T) {
	got, err := Expand(fixture(t, "apple_weekly_override.ics"), day(2026, 10, 1), day(2026, 10, 20), time.UTC, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RecurrenceID != "20261005T070000Z" || got[1].RecurrenceID != "20261012T070000Z" {
		t.Fatalf("recurrence ids: %+v", got)
	}
	if s, _ := got[1].Event.Props.Text(ical.PropSummary); s != "Standup (moved)" {
		t.Fatalf("override instance must carry the override's properties, got %q", s)
	}
	allDay, _ := Expand(fixture(t, "thunderbird_allday.ics"), day(2026, 1, 1), day(2026, 12, 31), time.UTC, 5000)
	if allDay[0].RecurrenceID != "20261007" || !allDay[0].End.Equal(allDay[0].Start.AddDate(0, 0, 1)) {
		t.Fatalf("all-day instance: %+v", allDay[0])
	}
	floating, _ := Expand(fixture(t, "floating.ics"), day(2026, 1, 1), day(2026, 12, 31), time.UTC, 5000)
	if floating[0].RecurrenceID != "" || floating[0].Recurring {
		t.Fatalf("non-recurring instance: %+v", floating[0])
	}
}

func TestExpandLimit(t *testing.T) {
	cal := fixture(t, "override_moved.ics") // weekly, no end
	if _, err := Expand(cal, day(2026, 1, 1), day(2027, 1, 1), time.UTC, 5); !errors.Is(err, ErrTooManyInstances) {
		t.Fatalf("want ErrTooManyInstances, got %v", err)
	}
}

// An all-day event spans local midnights even across a DST change.
func TestExpandAllDayAcrossDST(t *testing.T) {
	src := strings.ReplaceAll(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:dst-allday
DTSTAMP:20261001T000000Z
DTSTART;VALUE=DATE:20261031
DTEND;VALUE=DATE:20261102
RRULE:FREQ=WEEKLY;COUNT=2
SUMMARY:Weekend
END:VEVENT
END:VCALENDAR
`, "\n", "\r\n")
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	ny := zone(t, "America/New_York")
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 12, 1), ny, 5000)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, in := range got {
		if in.End.In(ny).Hour() != 0 || in.End.In(ny).Sub(in.Start.In(ny)) < 47*time.Hour {
			t.Fatalf("all-day instance does not end at local midnight two days later: %v -> %v", in.Start, in.End)
		}
	}
}

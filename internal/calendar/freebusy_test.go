package calendar

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func decodeICS(t *testing.T, body string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(body, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func busyString(list []Busy) string {
	var out []string
	for _, b := range list {
		s := b.Start.UTC().Format("0102T1504") + "-" + b.End.UTC().Format("0102T1504")
		if b.Tentative {
			s += " tentative"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

func TestBusyTimes(t *testing.T) {
	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	for name, c := range map[string]struct{ event, want string }{
		"opaque by default":  {"DTSTART:20261110T090000Z\nDTEND:20261110T100000Z\n", "1110T0900-1110T1000"},
		"explicit opaque":    {"DTSTART:20261110T090000Z\nDTEND:20261110T100000Z\nTRANSP:OPAQUE\n", "1110T0900-1110T1000"},
		"transparent":        {"DTSTART:20261110T090000Z\nDTEND:20261110T100000Z\nTRANSP:TRANSPARENT\n", ""},
		"cancelled":          {"DTSTART:20261110T090000Z\nDTEND:20261110T100000Z\nSTATUS:CANCELLED\n", ""},
		"tentative":          {"DTSTART:20261110T090000Z\nDTEND:20261110T100000Z\nSTATUS:TENTATIVE\n", "1110T0900-1110T1000 tentative"},
		"lower-case values":  {"DTSTART:20261110T090000Z\nDTEND:20261110T100000Z\nTRANSP:transparent\n", ""},
		"all-day in UTC":     {"DTSTART;VALUE=DATE:20261111\nDTEND;VALUE=DATE:20261112\n", "1111T0000-1112T0000"},
		"zoned":              {"DTSTART;TZID=Europe/Berlin:20261110T090000\nDTEND;TZID=Europe/Berlin:20261110T100000\n", "1110T0800-1110T0900"},
		"daily with EXDATE":  {"DTSTART:20261109T120000Z\nDTEND:20261109T123000Z\nRRULE:FREQ=DAILY;COUNT=3\nEXDATE:20261110T120000Z\n", "1109T1200-1109T1230, 1111T1200-1111T1230"},
		"outside the range":  {"DTSTART:20261201T090000Z\nDTEND:20261201T100000Z\n", ""},
		"zero-length marker": {"DTSTART:20261110T090000Z\n", "1110T0900-1110T0900"},
	} {
		cal := decodeICS(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:u\nDTSTAMP:20261001T000000Z\n"+c.event+"SUMMARY:Secret\nEND:VEVENT\nEND:VCALENDAR\n")
		got, err := BusyTimes(cal, from, to, time.UTC, 100)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if s := busyString(got); s != c.want {
			t.Errorf("%s: %q, want %q", name, s, c.want)
		}
	}
}

// An override decides its own occurrence: cancelling one instance frees it, a tentative one
// stays busy but tentative, and a transparent master frees every instance it generates.
func TestBusyTimesOverrides(t *testing.T) {
	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cal := decodeICS(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:s
DTSTAMP:20261001T000000Z
DTSTART:20261109T090000Z
DTEND:20261109T100000Z
RRULE:FREQ=DAILY;COUNT=3
END:VEVENT
BEGIN:VEVENT
UID:s
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261110T090000Z
DTSTART:20261110T090000Z
DTEND:20261110T100000Z
STATUS:CANCELLED
END:VEVENT
BEGIN:VEVENT
UID:s
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261111T090000Z
DTSTART:20261111T140000Z
DTEND:20261111T150000Z
STATUS:TENTATIVE
END:VEVENT
END:VCALENDAR
`)
	got, err := BusyTimes(cal, from, from.AddDate(0, 0, 7), time.UTC, 100)
	if err != nil {
		t.Fatal(err)
	}
	if s, want := busyString(got), "1109T0900-1109T1000, 1111T1400-1111T1500 tentative"; s != want {
		t.Fatalf("%q, want %q", s, want)
	}
}

func TestBusyTimesLimit(t *testing.T) {
	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cal := decodeICS(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:u\nDTSTAMP:20261001T000000Z\nDTSTART:20261109T090000Z\nDTEND:20261109T091500Z\nRRULE:FREQ=HOURLY\nEND:VEVENT\nEND:VCALENDAR\n")
	if _, err := BusyTimes(cal, from, from.AddDate(0, 0, 7), time.UTC, 10); !errors.Is(err, ErrTooManyInstances) {
		t.Fatalf("err %v, want ErrTooManyInstances", err)
	}
}

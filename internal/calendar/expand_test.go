package calendar

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
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
	flags := []struct {
		name string
		on   bool
	}{{"override", in.Override}, {"floating", in.Floating}, {"unknown", in.UnknownZone}, {"partial", in.Partial}}
	for _, f := range flags {
		if f.on {
			s += " " + f.name
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
	allDay, err := Expand(fixture(t, "thunderbird_allday.ics"), day(2026, 1, 1), day(2026, 12, 31), time.UTC, 5000)
	if err != nil || len(allDay) == 0 {
		t.Fatalf("all-day: %v %v", allDay, err)
	}
	if allDay[0].RecurrenceID != "20261007" || !allDay[0].End.Equal(allDay[0].Start.AddDate(0, 0, 1)) {
		t.Fatalf("all-day instance: %+v", allDay[0])
	}
	floating, err := Expand(fixture(t, "floating.ics"), day(2026, 1, 1), day(2026, 12, 31), time.UTC, 5000)
	if err != nil || len(floating) == 0 {
		t.Fatalf("floating: %v %v", floating, err)
	}
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

// parse builds a calendar from LF test source, converting to CRLF.
func parse(t *testing.T, src string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(src, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

// event wraps VEVENT body lines in a calendar.
func event(body string) string {
	return "BEGIN:VEVENT\nDTSTAMP:20261001T000000Z\n" + body + "\nEND:VEVENT\n"
}

func calendarOf(vevents ...string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\n" + strings.Join(vevents, "") + "END:VCALENDAR\n"
}

func TestExpandNeverMatchingRuleIsBounded(t *testing.T) {
	for _, rule := range []string{"FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30", "FREQ=HOURLY;BYMINUTE=0;BYSETPOS=2", "FREQ=MONTHLY;BYDAY=1MO;BYMONTHDAY=15"} {
		// Structural: the rule is recognised as unsatisfiable, so expansion never iterates it.
		opt, err := rrule.StrToROption(rule)
		if err != nil {
			t.Fatal(err)
		}
		start := day(2010, 1, 1)
		if satisfiable(opt) && yields(opt, start) {
			t.Errorf("%s: neither satisfiable nor yields reports false", rule)
		}
		var evs []string
		for i := range 10 {
			dt := "201" + strconv.Itoa(i) + "0101T000000Z"
			evs = append(evs, event("UID:never\nDTSTART:"+dt+"\nDTEND:"+dt+"\nRRULE:"+rule))
		}
		began := time.Now()
		got, err := Expand(parse(t, calendarOf(evs...)), day(2026, 10, 1), day(2026, 10, 8), time.UTC, 5000)
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range got {
			if !in.Partial {
				t.Fatalf("%s: instance of a never-matching rule: %+v", rule, in)
			}
		}
		if d := time.Since(began); d > 10*time.Second {
			t.Fatalf("%s: took %v", rule, d)
		}
	}
}

func TestSatisfiable(t *testing.T) {
	for rule, want := range map[string]bool{
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30":          false,
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29":          true,
		"FREQ=YEARLY;BYMONTH=4;BYMONTHDAY=31":          false,
		"FREQ=YEARLY;BYMONTHDAY=-31;BYMONTH=6":         false,
		"FREQ=YEARLY;BYYEARDAY=366":                    true,
		"FREQ=YEARLY;BYYEARDAY=1;BYMONTH=6":            false,
		"FREQ=MONTHLY;BYMONTHDAY=13;BYDAY=FR":          true,
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO": true,
	} {
		opt, err := rrule.StrToROption(rule)
		if err != nil {
			t.Fatal(err)
		}
		if got := satisfiable(opt); got != want {
			t.Errorf("%s: got %v want %v", rule, got, want)
		}
	}
}

func TestExpandOutOfRangeOrdinalIsPartial(t *testing.T) {
	cal := parse(t, calendarOf(event("UID:o\nDTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=MONTHLY;BYDAY=6MO")))
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 12, 1), time.UTC, 5000)
	if err != nil || len(got) != 1 || !got[0].Partial {
		t.Fatalf("%v %v", got, err)
	}
}

func TestExpandOldHourlySeriesShowsInRange(t *testing.T) {
	cal := parse(t, calendarOf(event("UID:old\nDTSTART:20140101T000000Z\nDTEND:20140101T003000Z\nRRULE:FREQ=HOURLY")))
	got, err := Expand(cal, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.UTC, 5000)
	if err != nil || len(got) != 24 {
		t.Fatalf("%d instances, %v", len(got), err)
	}
}

func TestExpandDateUntilSameForEveryViewer(t *testing.T) {
	cal := parse(t, calendarOf(event("UID:until\nDTSTART;VALUE=DATE:20261005\nDTEND;VALUE=DATE:20261006\nRRULE:FREQ=DAILY;UNTIL=20261007")))
	for _, v := range []string{"UTC", "America/Los_Angeles", "Pacific/Auckland"} {
		got, err := Expand(cal, day(2026, 9, 1), day(2026, 12, 1), zone(t, v), 5000)
		if err != nil || len(got) != 3 {
			t.Fatalf("%s: %d instances, %v", v, len(got), err)
		}
	}
}

func TestExpandManyMastersCapped(t *testing.T) {
	var evs []string
	for i := 1; i <= 12; i++ {
		evs = append(evs, event("UID:many\nDTSTART:202610"+strconv.Itoa(10+i)+"T090000Z\nDTEND:202610"+strconv.Itoa(10+i)+"T100000Z\nRRULE:FREQ=DAILY"))
	}
	got, err := Expand(parse(t, calendarOf(evs...)), day(2026, 10, 1), day(2026, 10, 30), time.UTC, 5000)
	if err != nil {
		t.Fatal(err)
	}
	partial := 0
	for _, in := range got {
		if in.Partial {
			partial++
		}
	}
	if partial != 2 {
		t.Fatalf("want 2 partial masters, got %d of %d instances", partial, len(got))
	}
}

func TestExpandHugeCountIsPartial(t *testing.T) {
	cal := parse(t, calendarOf(event("UID:huge\nDTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;COUNT=200000")))
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 12, 1), time.UTC, 5000)
	if err != nil || len(got) != 1 || !got[0].Partial {
		t.Fatalf("%v %v", got, err)
	}
}

func TestExpandSharedBudgetExhaustedIsAnError(t *testing.T) {
	cal := parse(t, calendarOf(event("UID:b\nDTSTART:20140101T000000Z\nDTEND:20140101T000100Z\nRRULE:FREQ=YEARLY;BYHOUR=1,2,3,4,5,6,7,8,9,10,11,12;BYMINUTE=0,1,2,3,4,5,6,7;BYMONTH=1,2,3,4,5,6,7,8,9,10,11,12;BYMONTHDAY=1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28")))
	_, err := Expand(cal, day(2014, 1, 1), day(9000, 1, 1), time.UTC, 10000000)
	if !errors.Is(err, ErrTooManyInstances) {
		t.Fatalf("want ErrTooManyInstances, got %v", err)
	}
}

func TestExpandOverrideReplacesPartialMaster(t *testing.T) {
	cal := parse(t, calendarOf(
		event("UID:p\nDTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=MINUTELY"),
		event("UID:p\nRECURRENCE-ID:20261007T090000Z\nDTSTART:20261007T110000Z\nDTEND:20261007T120000Z")))
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 10, 31), time.UTC, 5000)
	if err != nil || len(got) != 1 || !got[0].Override {
		t.Fatalf("%v %v", got, err)
	}
}

func TestExpandRecurrenceIDKeyedInMasterForm(t *testing.T) {
	cal := parse(t, calendarOf(
		event("UID:d\nDTSTART;VALUE=DATE:20261005\nDTEND;VALUE=DATE:20261006\nRRULE:FREQ=DAILY;COUNT=3"),
		event("UID:d\nRECURRENCE-ID:20261006\nDTSTART;VALUE=DATE:20261010\nDTEND;VALUE=DATE:20261011")))
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 10, 31), time.UTC, 5000)
	var desc []string
	for _, in := range got {
		desc = append(desc, describe(in))
	}
	if err != nil || !reflect.DeepEqual(desc, []string{"2026-10-05", "2026-10-07", "2026-10-10 override"}) {
		t.Fatalf("%v %v", desc, err)
	}
}

func TestExpandHugeIntervalDoesNotPanic(t *testing.T) {
	for _, freq := range []string{"HOURLY", "DAILY", "WEEKLY"} {
		for _, extra := range []string{"", ";COUNT=3"} {
			for _, interval := range []string{"9223372036854775807", "2251799813685248"} { // 2^51 wraps unit x interval to 0
				rule := "FREQ=" + freq + ";INTERVAL=" + interval + extra
				cal := parse(t, calendarOf(event("UID:i\nDTSTART:20140101T000000Z\nDTEND:20140101T010000Z\nRRULE:"+rule)))
				got, err := Expand(cal, day(2026, 10, 1), day(2026, 10, 8), time.UTC, 5000)
				if err != nil {
					t.Fatalf("%s: %v", rule, err)
				}
				for _, in := range got {
					if !in.Partial {
						t.Fatalf("%s: unexpected instance %+v", rule, in)
					}
				}
			}
		}
	}
}

func TestExpandHourlySkipAheadKeepsWallPhase(t *testing.T) {
	cal := parse(t, calendarOf(event("UID:h\nDTSTART;TZID=America/New_York:20140101T000000\nDTEND;TZID=America/New_York:20140101T003000\nRRULE:FREQ=HOURLY;INTERVAL=2")))
	got, err := Expand(cal, day(2026, 10, 7), day(2026, 10, 8), time.UTC, 5000)
	if err != nil || len(got) != 12 {
		t.Fatalf("%d instances, %v", len(got), err)
	}
	for _, in := range got {
		if in.Start.UTC().Hour()%2 != 0 {
			t.Fatalf("phase flipped: %v", in.Start.UTC())
		}
	}
}

func TestExpandSetposAndWeekno(t *testing.T) {
	for _, tc := range []struct {
		rule    string
		partial bool
	}{
		{"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=6", true},
		{"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", false},
		{"FREQ=YEARLY;BYWEEKNO=53;BYDAY=MO", false},
		{"FREQ=YEARLY;BYWEEKNO=1;BYMONTH=7", true},
	} {
		cal := parse(t, calendarOf(event("UID:s\nDTSTART:20261001T090000Z\nDTEND:20261001T100000Z\nRRULE:"+tc.rule)))
		got, err := Expand(cal, day(2026, 10, 1), day(2030, 1, 1), time.UTC, 5000)
		if err != nil || len(got) == 0 {
			t.Fatalf("%s: %v %v", tc.rule, got, err)
		}
		if tc.partial != got[0].Partial || tc.partial && len(got) != 1 || !tc.partial && len(got) < 2 {
			t.Errorf("%s: partial=%v, %d instances", tc.rule, got[0].Partial, len(got))
		}
	}
}

// Skip-ahead landing on a spring-forward gap must not shift the wall-clock phase: the result
// equals the same rule expanded from the original DTSTART under a COUNT that reaches the range.
func TestExpandSkipAheadAvoidsDSTGap(t *testing.T) {
	ny := zone(t, "America/New_York")
	for _, tc := range []struct {
		start, rule, count string
		from               time.Time
		days               int
	}{
		{"20260101T020000", "FREQ=HOURLY;INTERVAL=12", ";COUNT=2000", time.Date(2027, 3, 15, 0, 0, 0, 0, ny), 1},
		{"20260101T023000", "FREQ=DAILY", ";COUNT=1000", time.Date(2027, 3, 16, 0, 0, 0, 0, ny), 2},
	} {
		run := func(rule string) []string {
			cal := parse(t, calendarOf(event("UID:g\nDTSTART;TZID=America/New_York:"+tc.start+"\nDTEND;TZID=America/New_York:"+tc.start+"\nRRULE:"+rule)))
			got, err := Expand(cal, tc.from, tc.from.AddDate(0, 0, tc.days), time.UTC, 5000)
			if err != nil || len(got) == 0 {
				t.Fatalf("%s: %v %v", rule, got, err)
			}
			var desc []string
			for _, in := range got {
				desc = append(desc, in.Start.In(ny).Format(time.RFC3339)+" "+in.RecurrenceID)
			}
			return desc
		}
		if skip, full := run(tc.rule), run(tc.rule+tc.count); !reflect.DeepEqual(skip, full) {
			t.Errorf("%s:\nskip %v\nfull %v", tc.rule, skip, full)
		}
	}
}

func TestExpandOrdinalBydayWithDayFilter(t *testing.T) {
	for _, tc := range []struct {
		rule    string
		partial bool
	}{
		{"FREQ=MONTHLY;BYDAY=1MO;BYMONTHDAY=15", true},
		{"FREQ=MONTHLY;BYDAY=2TU;BYMONTHDAY=8,9,10,11,12,13,14", false},
	} {
		cal := parse(t, calendarOf(event("UID:b\nDTSTART:20261001T090000Z\nDTEND:20261001T100000Z\nRRULE:"+tc.rule)))
		got, err := Expand(cal, day(2026, 10, 1), day(2027, 1, 1), time.UTC, 5000)
		if err != nil || len(got) == 0 {
			t.Fatalf("%s: %v %v", tc.rule, got, err)
		}
		if tc.partial != got[0].Partial || tc.partial && len(got) != 1 || !tc.partial && len(got) < 3 {
			t.Errorf("%s: partial=%v, %d instances", tc.rule, got[0].Partial, len(got))
		}
	}
}

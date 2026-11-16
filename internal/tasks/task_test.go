package tasks

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func decode(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(s, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func encode(t *testing.T, cal *ical.Calendar) string {
	t.Helper()
	var b bytes.Buffer
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func ptr[T any](v T) *T { return &v }

const berlinDaily = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VTODO
UID:t1
SUMMARY:Standup notes
DTSTART;TZID=Europe/Berlin:20261024T083000
DUE;TZID=Europe/Berlin:20261024T090000
RRULE:FREQ=DAILY
X-UNKNOWN;FOO=bar:keep me
END:VTODO
END:VCALENDAR
`

func TestCompleteRepeatingKeepsWallClockAcrossDST(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	cal := decode(t, berlinDaily)
	// 2026-10-25 is the end of CEST in Berlin.
	res, err := Apply(cal, Patch{Status: ptr(Completed)}, time.Date(2026, 10, 24, 10, 0, 0, 0, berlin), berlin)
	if err != nil || !res.Completed || !res.Advanced {
		t.Fatalf("res %+v err %v", res, err)
	}
	task, _ := Read(cal, berlin)
	if got := task.Due.T.In(berlin).Format("2006-01-02 15:04 MST"); got != "2026-10-25 09:00 CET" {
		t.Fatalf("due %s", got)
	}
	if got := task.Start.T.In(berlin).Format("2006-01-02 15:04 MST"); got != "2026-10-25 08:30 CET" {
		t.Fatalf("start %s", got)
	}
	if task.Status != NeedsAction || !task.CompletedAt.IsZero() {
		t.Fatalf("status %s completed %v", task.Status, task.CompletedAt)
	}
	out := encode(t, cal)
	if !strings.Contains(out, "X-UNKNOWN;FOO=bar:keep me") || !strings.Contains(out, "DUE;TZID=Europe/Berlin:20261025T090000") {
		t.Fatal(out)
	}
}

func TestCompleteRepeatingFromCompletion(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	cal := decode(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VTODO
UID:t2
SUMMARY:Water the plants
DUE;VALUE=DATE:20261101
RRULE:FREQ=DAILY;INTERVAL=3
END:VTODO
END:VCALENDAR
`)
	if _, err := Apply(cal, Patch{FromCompletion: ptr(true)}, time.Now(), ny); err != nil {
		t.Fatal(err)
	}
	// Completed late on 6 Nov in New York (already 7 Nov in UTC): next is 9 Nov.
	if _, err := Apply(cal, Patch{Status: ptr(Completed)}, time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC), ny); err != nil {
		t.Fatal(err)
	}
	task, _ := Read(cal, ny)
	if got := task.Due.T.Format(time.DateOnly); got != "2026-11-09" || !task.Due.AllDay {
		t.Fatalf("due %s", got)
	}
}

func TestCompleteMonthlyLastWeekdayAndCount(t *testing.T) {
	cal := decode(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VTODO
UID:t3
SUMMARY:Invoice
DUE:20261030T160000Z
RRULE:FREQ=MONTHLY;BYDAY=-1FR;COUNT=2
END:VTODO
END:VCALENDAR
`)
	now := time.Date(2026, 10, 30, 17, 0, 0, 0, time.UTC)
	if res, _ := Apply(cal, Patch{Status: ptr(Completed)}, now, time.UTC); !res.Advanced {
		t.Fatal("first completion should advance")
	}
	task, _ := Read(cal, time.UTC)
	if task.Due.T.Format(time.RFC3339) != "2026-11-27T16:00:00Z" || !strings.Contains(task.RRule, "COUNT=1") {
		t.Fatalf("due %v rule %s", task.Due.T, task.RRule)
	}
	if res, _ := Apply(cal, Patch{Status: ptr(Completed)}, now, time.UTC); res.Advanced || !res.Completed {
		t.Fatalf("last occurrence should complete: %+v", res)
	}
	task, _ = Read(cal, time.UTC)
	if task.Status != Completed || task.Percent != 100 || task.CompletedAt.IsZero() {
		t.Fatalf("%+v", task)
	}
	// Completing again changes nothing; reopening clears COMPLETED.
	if res, _ := Apply(cal, Patch{Status: ptr(Completed)}, now, time.UTC); res.Completed {
		t.Fatal("second completion counted")
	}
	if res, _ := Apply(cal, Patch{Status: ptr(NeedsAction)}, now, time.UTC); !res.Reopened {
		t.Fatal("not reopened")
	}
	task, _ = Read(cal, time.UTC)
	if task.Status != NeedsAction || !task.CompletedAt.IsZero() || task.Percent != 0 {
		t.Fatalf("%+v", task)
	}
}

func TestCompleteLastDayOfMonthUntil(t *testing.T) {
	cal := decode(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VTODO
UID:t4
SUMMARY:Rent
DUE;VALUE=DATE:20261130
RRULE:FREQ=MONTHLY;BYMONTHDAY=-1;UNTIL=20261231
END:VTODO
END:VCALENDAR
`)
	if res, _ := Apply(cal, Patch{Status: ptr(Completed)}, time.Now(), time.UTC); !res.Advanced {
		t.Fatal("should advance to 31 Dec")
	}
	task, _ := Read(cal, time.UTC)
	if task.Due.T.Format(time.DateOnly) != "2026-12-31" {
		t.Fatal(task.Due.T)
	}
	if res, _ := Apply(cal, Patch{Status: ptr(Completed)}, time.Now(), time.UTC); res.Advanced {
		t.Fatal("past UNTIL must complete")
	}
}

func TestNewAndEditFields(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	due := Moment{T: time.Date(2026, 11, 16, 15, 0, 0, 0, ny)}
	cal, err := New("u1", Patch{Title: ptr("Write the launch announcement"), Priority: ptr(1), Due: &due, Zone: ny,
		Tags: &[]string{"web", "copy"}, Estimate: ptr(time.Hour), Reminders: &[]Reminder{{Before: 15 * time.Minute}}}, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	out := encode(t, cal)
	for _, want := range []string{"DUE;TZID=America/New_York:20261116T150000", "PRIORITY:1", "CATEGORIES:web,copy",
		"X-KY-ESTIMATE:PT1H", "STATUS:NEEDS-ACTION", "TRIGGER;RELATED=END:-PT15M", "BEGIN:VTIMEZONE"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	// Placing a task with a DUE keeps the length in the estimate, never DURATION.
	start := Moment{T: time.Date(2026, 11, 16, 13, 0, 0, 0, ny)}
	if _, err := Apply(cal, Patch{Start: &start, Zone: ny, Duration: ptr(30 * time.Minute)}, now, ny); err != nil {
		t.Fatal(err)
	}
	task, _ := Read(cal, ny)
	if task.Duration != 0 || task.Estimate != 30*time.Minute {
		t.Fatalf("duration %v estimate %v", task.Duration, task.Estimate)
	}
	// Without DUE, placement writes DURATION.
	if _, err := Apply(cal, Patch{Due: &Moment{}, Duration: ptr(45 * time.Minute)}, now, ny); err != nil {
		t.Fatal(err)
	}
	task, _ = Read(cal, ny)
	if task.Duration != 45*time.Minute || task.Due != nil {
		t.Fatalf("%+v", task)
	}
	// A rule needs a date.
	if _, err := Apply(cal, Patch{Start: &Moment{}, RRule: ptr("FREQ=DAILY")}, now, ny); err == nil {
		t.Fatal("rule without a date accepted")
	}
}

func TestParentKeepsOtherRelations(t *testing.T) {
	cal := decode(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VTODO
UID:c
SUMMARY:Child
RELATED-TO;RELTYPE=SIBLING:s
RELATED-TO:p
END:VTODO
END:VCALENDAR
`)
	task, _ := Read(cal, time.UTC)
	if task.Parent != "p" {
		t.Fatal(task.Parent)
	}
	if _, err := Apply(cal, Patch{Parent: ptr("")}, time.Now(), time.UTC); err != nil {
		t.Fatal(err)
	}
	out := encode(t, cal)
	if strings.Contains(out, "RELATED-TO:p") || !strings.Contains(out, "RELATED-TO;RELTYPE=SIBLING:s") {
		t.Fatal(out)
	}
}

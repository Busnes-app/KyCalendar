package calendar

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func decode(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(s, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return cal
}

func ev(body string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:u1\nDTSTAMP:20261001T000000Z\n" + body + "END:VEVENT\nEND:VCALENDAR\n"
}

func unix(s string) int64 {
	t, _ := time.Parse("20060102T150405Z", s)
	return t.Unix()
}

func TestInspectSingle(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.UID != "u1" || o.FirstStart != unix("20261007T090000Z") || o.LastEnd == nil || *o.LastEnd != unix("20261007T100000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectAllDay(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART;VALUE=DATE:20261007\n")))
	if err != nil {
		t.Fatal(err)
	}
	// All-day without DTEND lasts one day; floating dates widen by 14h each side.
	if o.FirstStart > unix("20261007T000000Z") || *o.LastEnd < unix("20261008T000000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectUnboundedRecurrence(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=WEEKLY\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd != nil {
		t.Fatalf("unbounded rule must have nil LastEnd, got %d", *o.LastEnd)
	}
}

func TestInspectHugeCountIsBoundedWork(t *testing.T) {
	done := make(chan Object, 1)
	cal := decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=SECONDLY;COUNT=1000000000\n"))
	go func() {
		o, _ := Inspect(cal)
		done <- o
	}()
	select {
	case o := <-done:
		if o.LastEnd != nil {
			t.Fatalf("capped rule must index as unbounded, got %d", *o.LastEnd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Inspect did not return within 2s")
	}
}

func TestInspectBoundedRecurrence(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;COUNT=3\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd == nil || *o.LastEnd != unix("20261009T100000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectUnknownTZIDWidens(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART;TZID=Eastern Standard Time:20261007T090000\nDTEND;TZID=Eastern Standard Time:20261007T100000\n")))
	if err != nil {
		t.Fatalf("unknown TZID must be accepted: %v", err)
	}
	want := unix("20261007T130000Z") // 09:00 EDT
	if o.FirstStart > want || o.LastEnd == nil || *o.LastEnd < want+3600 {
		t.Fatalf("index must cover the real instant: %+v", o)
	}
	rec, err := Inspect(decode(t, ev("DTSTART;TZID=Eastern Standard Time:20261007T090000\nDTEND;TZID=Eastern Standard Time:20261007T100000\nRRULE:FREQ=WEEKLY;COUNT=4\n")))
	if err != nil {
		t.Fatalf("recurring event with unknown TZID must be accepted: %v", err)
	}
	if rec.FirstStart > want {
		t.Fatalf("recurring index starts too late: %+v", rec)
	}
}

func TestInspectRejects(t *testing.T) {
	todo := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VTODO\nUID:t1\nDTSTAMP:20261001T000000Z\nEND:VTODO\nEND:VCALENDAR\n"
	if _, err := Inspect(decode(t, todo)); !errors.Is(err, ErrUnsupportedComponent) {
		t.Fatalf("VTODO: %v", err)
	}
	noUID := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nDTSTAMP:20261001T000000Z\nDTSTART:20261007T090000Z\nEND:VEVENT\nEND:VCALENDAR\n"
	if _, err := Inspect(decode(t, noUID)); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("no UID: %v", err)
	}
	twoUIDs := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:a\nDTSTAMP:20261001T000000Z\nDTSTART:20261007T090000Z\nEND:VEVENT\nBEGIN:VEVENT\nUID:b\nDTSTAMP:20261001T000000Z\nDTSTART:20261008T090000Z\nEND:VEVENT\nEND:VCALENDAR\n"
	if _, err := Inspect(decode(t, twoUIDs)); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("two UIDs: %v", err)
	}
}

func TestInspectOverrideExtendsBounds(t *testing.T) {
	s := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\n" +
		"BEGIN:VEVENT\nUID:r\nDTSTAMP:20261001T000000Z\nDTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;COUNT=2\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:r\nDTSTAMP:20261001T000000Z\nRECURRENCE-ID:20261008T090000Z\nDTSTART:20261020T090000Z\nDTEND:20261020T100000Z\nEND:VEVENT\n" +
		"END:VCALENDAR\n"
	o, err := Inspect(decode(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd == nil || *o.LastEnd != unix("20261020T100000Z") {
		t.Fatalf("moved occurrence must extend LastEnd: %+v", o)
	}
}

func TestInspectRDate(t *testing.T) {
	base := "DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\n"
	cases := []struct {
		name      string
		body      string
		wantFirst int64
		wantLast  int64 // 0: nil
	}{
		{"late with rrule", base + "RRULE:FREQ=DAILY;COUNT=2\nRDATE:20300101T090000Z\n", unix("20261007T090000Z"), unix("20300101T100000Z")},
		{"rdate only", base + "RDATE:20261015T090000Z,20261020T090000Z\n", unix("20261007T090000Z"), unix("20261020T100000Z")},
		{"before dtstart", base + "RDATE:20260101T090000Z\n", unix("20260101T090000Z"), unix("20261007T100000Z")},
		{"period end", base + "RDATE;VALUE=PERIOD:20261101T090000Z/20261101T120000Z\n", unix("20261007T090000Z"), unix("20261101T120000Z")},
		{"period duration", base + "RDATE;VALUE=PERIOD:20261101T090000Z/PT3H\n", unix("20261007T090000Z"), unix("20261101T120000Z")},
		{"unparseable", base + "RDATE:garbage\n", unix("20261007T090000Z"), 0},
	}
	for _, c := range cases {
		o, err := Inspect(decode(t, ev(c.body)))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if o.FirstStart > c.wantFirst {
			t.Errorf("%s: FirstStart %d > %d", c.name, o.FirstStart, c.wantFirst)
		}
		switch {
		case c.wantLast == 0 && o.LastEnd != nil:
			t.Errorf("%s: want nil LastEnd, got %d", c.name, *o.LastEnd)
		case c.wantLast != 0 && (o.LastEnd == nil || *o.LastEnd < c.wantLast):
			t.Errorf("%s: LastEnd %v < %d", c.name, o.LastEnd, c.wantLast)
		}
	}
	o, _ := Inspect(decode(t, ev(base+"RDATE:20261015T090000Z\n")))
	if o.LastEnd == nil || *o.LastEnd != unix("20261015T100000Z") {
		t.Errorf("exact utc rdate should not widen: %+v", o)
	}
}

func TestInspectDurationWithUnknownTZID(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART;TZID=Eastern Standard Time:20261007T090000\nDURATION:PT10H\n")))
	if err != nil {
		t.Fatal(err)
	}
	// Wall-clock start read as UTC, plus DURATION, plus 14h slack.
	if want := unix("20261008T090000Z"); o.LastEnd == nil || *o.LastEnd < want {
		t.Fatalf("DURATION ignored: %+v want >= %d", o, want)
	}
}

func TestInspectOccurrenceBudgetIsPerObject(t *testing.T) {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\n")
	for i := 0; i < 200; i++ {
		b.WriteString("BEGIN:VEVENT\nUID:r\nDTSTAMP:20261001T000000Z\n")
		if i > 0 {
			b.WriteString("RECURRENCE-ID:" + time.Unix(unix("20261007T090000Z")+int64(i)*3600, 0).UTC().Format("20060102T150405Z") + "\n")
		}
		b.WriteString("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=SECONDLY;COUNT=1000000\nEND:VEVENT\n")
	}
	b.WriteString("END:VCALENDAR\n")
	cal := decode(t, b.String())
	done := make(chan Object, 1)
	go func() {
		o, _ := Inspect(cal)
		done <- o
	}()
	select {
	case o := <-done:
		if o.LastEnd != nil {
			t.Fatalf("budget exhaustion must index as unbounded, got %d", *o.LastEnd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Inspect did not return within 2s")
	}
}

func TestInspectExdateWithUnknownTZIDIsAccepted(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;COUNT=3\nEXDATE;TZID=Eastern Standard Time:20261008T090000\n")))
	if err != nil {
		t.Fatalf("must accept: %v", err)
	}
	// EXDATE only removes instances, so ignoring it keeps the index conservative.
	if o.LastEnd == nil || *o.LastEnd < unix("20261009T100000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectManyEmptyRulesAreBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\n")
	for i := 0; i < 2000; i++ {
		b.WriteString("BEGIN:VEVENT\nUID:r\nDTSTAMP:20261001T000000Z\n")
		if i > 0 {
			b.WriteString("RECURRENCE-ID:" + time.Unix(unix("20261007T090000Z")+int64(i)*3600, 0).UTC().Format("20060102T150405Z") + "\n")
		}
		b.WriteString("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;BYMONTH=2;BYMONTHDAY=30\nEND:VEVENT\n")
	}
	b.WriteString("END:VCALENDAR\n")
	cal := decode(t, b.String())
	done := make(chan Object, 1)
	go func() {
		o, _ := Inspect(cal)
		done <- o
	}()
	select {
	case o := <-done:
		if o.LastEnd != nil {
			t.Fatalf("want unbounded, got %d", *o.LastEnd)
		}
	case <-time.After(time.Second):
		t.Fatal("Inspect did not return within 1s")
	}
}

func TestInspectSubHourlyRuleIsUnbounded(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=MINUTELY;COUNT=5\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd != nil {
		t.Fatalf("want nil LastEnd, got %d", *o.LastEnd)
	}
}

func TestInspectRDateBudgetExhaustion(t *testing.T) {
	vals := strings.Repeat("20261015T090000Z,", maxIndexOccurrences+1) + "20261016T090000Z"
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRDATE:"+vals+"\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd != nil || o.FirstStart > unix("20261007T090000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectUnknownZoneRuleIsUnbounded(t *testing.T) {
	for name, body := range map[string]string{
		"unknown tzid": "DTSTART;TZID=Tokyo Standard Time:20261007T090000\nDTEND;TZID=Tokyo Standard Time:20261007T100000\nRRULE:FREQ=WEEKLY;UNTIL=20261014T000000Z\n",
		"floating":     "DTSTART:20261007T090000\nDTEND:20261007T100000\nRRULE:FREQ=WEEKLY;COUNT=3\n",
		"date":         "DTSTART;VALUE=DATE:20261007\nRRULE:FREQ=WEEKLY;COUNT=3\n",
	} {
		o, err := Inspect(decode(t, ev(body)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if o.LastEnd != nil {
			t.Errorf("%s: want nil LastEnd, got %d", name, *o.LastEnd)
		}
	}
}

func TestInspectTwoRRulesAreUnbounded(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;COUNT=2\nRRULE:FREQ=WEEKLY\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd != nil {
		t.Fatalf("want nil LastEnd, got %d", *o.LastEnd)
	}
}

// rrule-go builds a whole period's batch before the first occurrence is returned, so the
// occurrence budget alone cannot bound a rule whose BYHOUR x BYMINUTE x BYSECOND is wide.
func TestInspectWideTimeSetIsUnboundedWithoutExpansion(t *testing.T) {
	list := func(n int) string {
		s := make([]string, n)
		for i := range s {
			s[i] = strconv.Itoa(i)
		}
		return strings.Join(s, ",")
	}
	rule := "RRULE:FREQ=YEARLY;COUNT=1000000000;BYMONTH=1,2,3,4,5,6,7,8,9,10,11,12;BYMONTHDAY=" +
		strings.Join(strings.Split(list(32), ",")[1:], ",") +
		";BYHOUR=" + list(24) + ";BYMINUTE=" + list(60) + ";BYSECOND=" + list(60) + "\n"
	cal := decode(t, ev("DTSTART:20260101T000000Z\nDTEND:20260101T010000Z\n"+rule))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	o, err := Inspect(cal)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd != nil {
		t.Fatalf("wide time-set must index as unbounded, got %d", *o.LastEnd)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 16<<20 {
		t.Fatalf("Inspect allocated %d bytes; the rule was expanded", alloc)
	}
}

func TestInspectHourlyMinutesStillBounded(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T091500Z\nRRULE:FREQ=DAILY;COUNT=4;BYHOUR=9,17;BYMINUTE=0,30\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd == nil || *o.LastEnd != unix("20261007T174500Z") {
		t.Fatalf("%+v", o)
	}
}

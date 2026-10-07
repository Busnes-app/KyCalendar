package calendar

import (
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func TestVTimezoneTransitions(t *testing.T) {
	ny := zone(t, "America/New_York")
	tz := VTimezone(ny, time.Date(2026, 1, 1, 0, 0, 0, 0, ny), time.Date(2027, 1, 1, 0, 0, 0, 0, ny))
	if id, _ := tz.Props.Text(ical.PropTimezoneID); id != "America/New_York" {
		t.Fatalf("TZID %q", id)
	}
	if len(tz.Children) != 3 {
		t.Fatalf("want the opening offset plus 2 changes in 2026, got %d", len(tz.Children))
	}
	o := tz.Children[0]
	if of, ot := o.Props.Get(ical.PropTimezoneOffsetFrom).Value, o.Props.Get(ical.PropTimezoneOffsetTo).Value; o.Name != ical.CompTimezoneStandard || o.Props.Get(ical.PropDateTimeStart).Value != "20260101T000000" || of != "-0500" || ot != "-0500" {
		t.Fatalf("opening observance: %s %s %s", o.Name, of, ot)
	}
	d := tz.Children[1]
	start := d.Props.Get(ical.PropDateTimeStart).Value
	from := d.Props.Get(ical.PropTimezoneOffsetFrom).Value
	to := d.Props.Get(ical.PropTimezoneOffsetTo).Value
	if v := d.Props.Get(ical.PropTimezoneOffsetFrom).Params.Get(ical.ParamValue); v != "" {
		t.Fatalf("TZOFFSETFROM must not carry VALUE=%s", v)
	}
	if d.Name != ical.CompTimezoneDaylight || start != "20260308T020000" || from != "-0500" || to != "-0400" {
		t.Fatalf("daylight onset: %s %s %s %s", d.Name, start, from, to)
	}
	if s := tz.Children[2].Props.Get(ical.PropDateTimeStart).Value; tz.Children[2].Name != ical.CompTimezoneStandard || s != "20261101T020000" {
		t.Fatalf("standard onset: %s %s", tz.Children[2].Name, s)
	}
	tokyo := VTimezone(zone(t, "Asia/Tokyo"), day(2026, 1, 1), day(2037, 1, 1))
	if len(tokyo.Children) != 1 || tokyo.Children[0].Name != ical.CompTimezoneStandard {
		t.Fatalf("a zone without changes needs one STANDARD observance: %+v", tokyo.Children)
	}
	if off := tokyo.Children[0].Props.Get(ical.PropTimezoneOffsetTo).Value; off != "+0900" {
		t.Fatalf("Tokyo offset %q", off)
	}
	_ = time.UTC
}

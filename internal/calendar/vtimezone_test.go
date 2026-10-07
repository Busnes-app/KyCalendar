package calendar

import (
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func TestVTimezoneTransitions(t *testing.T) {
	ny := zone(t, "America/New_York")
	tz := VTimezone(ny, day(2026, 1, 1), day(2027, 1, 1))
	if id, _ := tz.Props.Text(ical.PropTimezoneID); id != "America/New_York" {
		t.Fatalf("TZID %q", id)
	}
	if len(tz.Children) != 2 {
		t.Fatalf("want 2 observances in 2026, got %d", len(tz.Children))
	}
	d := tz.Children[0]
	start := d.Props.Get(ical.PropDateTimeStart).Value
	from := d.Props.Get(ical.PropTimezoneOffsetFrom).Value
	to := d.Props.Get(ical.PropTimezoneOffsetTo).Value
	if v := d.Props.Get(ical.PropTimezoneOffsetFrom).Params.Get(ical.ParamValue); v != "" {
		t.Fatalf("TZOFFSETFROM must not carry VALUE=%s", v)
	}
	if d.Name != ical.CompTimezoneDaylight || start != "20260308T020000" || from != "-0500" || to != "-0400" {
		t.Fatalf("daylight onset: %s %s %s %s", d.Name, start, from, to)
	}
	if s := tz.Children[1].Props.Get(ical.PropDateTimeStart).Value; tz.Children[1].Name != ical.CompTimezoneStandard || s != "20261101T020000" {
		t.Fatalf("standard onset: %s %s", tz.Children[1].Name, s)
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

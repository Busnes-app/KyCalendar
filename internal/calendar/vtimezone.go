package calendar

import (
	"fmt"
	"time"

	"github.com/emersion/go-ical"
)

// VTimezone describes loc for clients without its IANA data: one observance per offset change
// in [from, to), or a single STANDARD observance for a zone that does not change there.
func VTimezone(loc *time.Location, from, to time.Time) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	tz.Props.SetText(ical.PropTimezoneID, loc.String())
	for t := from.In(loc); t.Before(to); {
		_, end := t.ZoneBounds()
		if end.IsZero() || !end.Before(to) {
			break
		}
		tz.Children = append(tz.Children, observance(end, end.Add(-time.Second)))
		t = end
	}
	if len(tz.Children) == 0 {
		at := from.In(loc)
		tz.Children = append(tz.Children, observance(at, at))
	}
	return tz
}

// observance is the onset at 'at', from the offset in force at 'before'. Its DTSTART is local
// time in the previous offset, as RFC 5545 requires.
func observance(at, before time.Time) *ical.Component {
	name := ical.CompTimezoneStandard
	if at.IsDST() {
		name = ical.CompTimezoneDaylight
	}
	c := ical.NewComponent(name)
	_, fromOff := before.Zone()
	abbr, toOff := at.Zone()
	start := ical.NewProp(ical.PropDateTimeStart)
	start.Value = at.In(time.FixedZone("", fromOff)).Format("20060102T150405")
	c.Props.Set(start)
	setRaw(c, ical.PropTimezoneOffsetFrom, utcOffset(fromOff))
	setRaw(c, ical.PropTimezoneOffsetTo, utcOffset(toOff))
	c.Props.SetText(ical.PropTimezoneName, abbr)
	return c
}

// setRaw sets a property's value verbatim. Props.SetText marks non-TEXT properties such as
// SEQUENCE or TZOFFSETFROM with VALUE=TEXT, which clients reject.
func setRaw(comp *ical.Component, name, value string) {
	comp.Props.Set(&ical.Prop{Name: name, Params: ical.Params{}, Value: value})
}

func utcOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return fmt.Sprintf("%s%02d%02d", sign, sec/3600, sec%3600/60)
}

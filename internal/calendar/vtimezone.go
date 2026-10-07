package calendar

import (
	"fmt"
	"time"

	"github.com/emersion/go-ical"
)

// maxObservances caps a generated VTIMEZONE: an offset change or two per year over
// vtimezoneYears, plus the opening observance, with room to spare.
const maxObservances = 4*vtimezoneYears + 1

// VTimezone describes loc for clients without its IANA data: the observance in force at from,
// then one per offset change in [from, to).
func VTimezone(loc *time.Location, from, to time.Time) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	tz.Props.SetText(ical.PropTimezoneID, loc.String())
	t := from.In(loc)
	tz.Children = append(tz.Children, observance(t, t))
	for t.Before(to) && len(tz.Children) < maxObservances {
		_, end := t.ZoneBounds()
		if end.IsZero() || !end.Before(to) {
			break
		}
		if !end.After(t) {
			// Past tzdata's explicit transitions Go extrapolates the zone's rule and ZoneBounds
			// can return t itself. t is a change already recorded and no zone changes twice in a
			// day, so stepping a day ahead skips nothing.
			t = t.Add(24 * time.Hour)
			continue
		}
		before := end.Add(-time.Second)
		_, offBefore := before.Zone()
		_, offAfter := end.Zone()
		// Go also reports boundaries that change nothing (e.g. at year ends past the explicit
		// data); only real offset or DST changes become observances.
		if offBefore != offAfter || before.IsDST() != end.IsDST() {
			tz.Children = append(tz.Children, observance(end, before))
		}
		t = end
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

package calendar

import (
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Busy is one busy instance: times only, never the event's text.
type Busy struct {
	Start, End time.Time
	Tentative  bool
}

// BusyTimes lists the busy instances of cal's VEVENTs overlapping [from, to), by RFC 4791
// §7.10: TRANSP:TRANSPARENT and STATUS:CANCELLED instances are free, STATUS:TENTATIVE is
// tentative. Floating times and dates are read in loc. More than limit instances is
// ErrTooManyInstances.
func BusyTimes(cal *ical.Calendar, from, to time.Time, loc *time.Location, limit int) ([]Busy, error) {
	insts, err := Expand(cal, from, to, loc, limit)
	if err != nil {
		return nil, err
	}
	var out []Busy
	for _, in := range insts {
		status := propValue(in.Event, ical.PropStatus)
		if strings.EqualFold(propValue(in.Event, ical.PropTransparency), "TRANSPARENT") || strings.EqualFold(status, "CANCELLED") {
			continue
		}
		out = append(out, Busy{Start: in.Start, End: in.End, Tentative: strings.EqualFold(status, "TENTATIVE")})
	}
	return out, nil
}

func propValue(c *ical.Component, name string) string {
	if p := c.Props.Get(name); p != nil {
		return strings.TrimSpace(p.Value)
	}
	return ""
}

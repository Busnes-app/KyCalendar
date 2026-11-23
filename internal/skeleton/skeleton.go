// Package skeleton splits a calendar object into the readable skeleton the server keeps in
// calendar_objects.data and the content that is sealed, and merges them back for a reader
// (content encryption spec E1, E2, E4, E5; docs/CONTENT-ENCRYPTION-FORMAT.md, "Skeleton and
// content"). It does no
// cryptography: callers seal and open the plaintexts. Nothing calls it until build step CE2.
package skeleton

import (
	"bytes"
	"fmt"

	"github.com/emersion/go-ical"
)

// readable is E1's allowlist, per component: everything else, every unknown X- property
// included, is content. VTIMEZONE is readable whole.
var readable = map[string]map[string]bool{
	ical.CompCalendar: set("VERSION", "PRODID", "CALSCALE", "METHOD"),
	ical.CompEvent:    eventProps,
	ical.CompToDo:     eventProps,
	ical.CompAlarm:    set("ACTION", "TRIGGER", "REPEAT", "DURATION", "UID", "ACKNOWLEDGED"),
}

var eventProps = set("UID", "DTSTART", "DTEND", "DUE", "DURATION", "RRULE", "RDATE", "EXDATE", "RECURRENCE-ID",
	"SEQUENCE", "DTSTAMP", "CREATED", "LAST-MODIFIED", "STATUS", "TRANSP", "CLASS", "PRIORITY", "COMPLETED",
	"PERCENT-COMPLETE", "ORGANIZER", "ATTENDEE",
	// KyCalendar's own markers, which workers read (internal/tasks, internal/davbackend, internal/booking).
	"X-KY-REPEAT-FROM", "X-KY-HABIT", "X-KY-ESTIMATE", "X-KY-SOURCE", "X-KY-REF",
	"X-KYCALENDAR-ORGANIZER-SEQUENCE", "X-KYCALENDAR-OUT-OF-OFFICE", "X-KYCALENDAR-BOOKING-TYPE")

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// isReadable reports whether property name of a component named comp stays in the skeleton.
// A component KyCalendar does not store (VJOURNAL and so on) is treated as an event.
func isReadable(comp, name string) bool {
	if comp == ical.CompTimezone || comp == ical.CompTimezoneStandard || comp == ical.CompTimezoneDaylight {
		return true
	}
	allow, ok := readable[comp]
	if !ok {
		allow = eventProps
	}
	return allow[name]
}

// Split is raw's skeleton: the same components with only their readable properties.
func Split(raw []byte) ([]byte, error) {
	cal, err := decode(raw)
	if err != nil {
		return nil, err
	}
	return encode(strip(cal.Component))
}

func strip(c *ical.Component) *ical.Component {
	out := &ical.Component{Name: c.Name, Props: ical.Props{}}
	for name, ps := range c.Props {
		if isReadable(c.Name, name) {
			out.Props[name] = ps
		}
	}
	for _, child := range c.Children {
		out.Children = append(out.Children, strip(child))
	}
	return out
}

func decode(raw []byte) (*ical.Calendar, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("skeleton: %w", err)
	}
	return cal, nil
}

func encode(c *ical.Component) ([]byte, error) {
	var b bytes.Buffer
	if err := ical.NewEncoder(&b).Encode(&ical.Calendar{Component: c}); err != nil {
		return nil, fmt.Errorf("skeleton: %w", err)
	}
	return b.Bytes(), nil
}

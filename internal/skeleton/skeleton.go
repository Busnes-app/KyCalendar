// Package skeleton splits a calendar object into the readable skeleton the server keeps in
// calendar_objects.data and the content that is sealed, and merges them back for a reader
// (content encryption spec E1, E2, E4, E5; docs/CONTENT-ENCRYPTION-FORMAT.md, "Skeleton and
// content"). It does no
// cryptography: callers seal and open the plaintexts. Nothing calls it until build step CE2.
package skeleton

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

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
	write(&b, c)
	// Whatever is written must read back, as every skeleton reader uses go-ical's decoder.
	if _, err := decode(b.Bytes()); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// write is c as iCalendar text. go-ical's encoder allows only VALARM inside an event, but
// KyCalendar stores any nesting a client sends (RFC 9073 VLOCATION, PARTICIPANT), so the skeleton
// and the merged object are written here: properties in name order, parameters in name order and
// quoted when they hold ':', ';' or ',', lines folded at 75 octets. Values are written as go-ical
// read them, which is still escaped.
func write(b *bytes.Buffer, c *ical.Component) {
	line(b, "BEGIN:"+c.Name)
	names := make([]string, 0, len(c.Props))
	for n := range c.Props {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		for _, p := range c.Props[n] {
			var l strings.Builder
			l.WriteString(n)
			keys := make([]string, 0, len(p.Params))
			for k := range p.Params {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				l.WriteString(";" + k + "=")
				for i, v := range p.Params[k] {
					if i > 0 {
						l.WriteByte(',')
					}
					if strings.ContainsAny(v, ":;,") {
						v = `"` + v + `"`
					}
					l.WriteString(v)
				}
			}
			l.WriteString(":" + p.Value)
			line(b, l.String())
		}
	}
	for _, child := range c.Children {
		write(b, child)
	}
	line(b, "END:"+c.Name)
}

// line writes s folded at 75 octets (RFC 5545 3.1), never inside a UTF-8 character.
func line(b *bytes.Buffer, s string) {
	limit := 75
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		b.WriteString(s[:cut] + "\r\n ")
		s = s[cut:]
		limit = 74 // the leading space counts
	}
	b.WriteString(s + "\r\n")
}

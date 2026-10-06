// Package calendar holds pure iCalendar rules: what KyCalendar accepts and how it is indexed.
package calendar

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// MaxObjectSize bounds one stored calendar object in bytes.
const MaxObjectSize = 1 << 20

var (
	ErrInvalidData          = errors.New("calendar: invalid calendar data")
	ErrUnsupportedComponent = errors.New("calendar: only VEVENT is supported")
)

// Object is what the store indexes for one calendar resource.
type Object struct {
	UID        string
	FirstStart int64  // unix seconds
	LastEnd    *int64 // nil: recurs without end
}

// zoneSlack widens bounds when a time's zone is unknown or floating: UTC-12 to UTC+14.
const zoneSlack = 14 * time.Hour

// horizon caps recurrence expansion while indexing; rules running past it index as unbounded.
const horizon = 100 * 365 * 24 * time.Hour

// maxIndexOccurrences bounds work per rule; a rule needing more indexes as unbounded.
const maxIndexOccurrences = 100000

// Inspect validates a decoded calendar object and computes its index bounds.
func Inspect(cal *ical.Calendar) (Object, error) {
	var o Object
	first, last := int64(0), int64(0)
	unbounded, seen := false, false

	for _, comp := range cal.Children {
		switch comp.Name {
		case ical.CompTimezone:
			continue
		case ical.CompEvent:
		default:
			return Object{}, fmt.Errorf("%w: %s", ErrUnsupportedComponent, comp.Name)
		}
		uid, err := comp.Props.Text(ical.PropUID)
		if err != nil || uid == "" {
			return Object{}, fmt.Errorf("%w: missing UID", ErrInvalidData)
		}
		if o.UID != "" && uid != o.UID {
			return Object{}, fmt.Errorf("%w: conflicting UIDs", ErrInvalidData)
		}
		o.UID = uid

		start, end, slack, err := eventSpan(comp)
		if err != nil {
			return Object{}, err
		}
		compFirst, compLast := start.Add(-slack), end.Add(slack)

		if comp.Props.Get(ical.PropRecurrenceRule) != nil {
			rset, err := comp.RecurrenceSet(time.UTC)
			if err != nil && slack > 0 {
				// Zone unknown, so the rule cannot be expanded here; index as unbounded.
				unbounded = true
			} else if err != nil || rset == nil {
				return Object{}, fmt.Errorf("%w: bad recurrence", ErrInvalidData)
			} else if lastOcc, ok := lastOccurrence(rset, start.Add(horizon)); !ok {
				unbounded = true
			} else if !lastOcc.IsZero() {
				compLast = lastOcc.Add(end.Sub(start)).Add(slack)
			}
		}

		if !seen || compFirst.Unix() < first {
			first = compFirst.Unix()
		}
		if !seen || compLast.Unix() > last {
			last = compLast.Unix()
		}
		seen = true
	}
	if !seen {
		return Object{}, fmt.Errorf("%w: no VEVENT", ErrInvalidData)
	}
	o.FirstStart = first
	if !unbounded {
		o.LastEnd = &last
	}
	return o, nil
}

// lastOccurrence walks the set until it ends. ok is false when an occurrence passes limit
// or maxIndexOccurrences is reached, i.e. the rule is treated as unbounded.
func lastOccurrence(rset interface {
	Iterator() func() (time.Time, bool)
}, limit time.Time) (last time.Time, ok bool) {
	next := rset.Iterator()
	for n := 0; n < maxIndexOccurrences; n++ {
		occ, more := next()
		if !more {
			return last, true
		}
		if occ.After(limit) {
			return time.Time{}, false
		}
		last = occ
	}
	return time.Time{}, false
}

// eventSpan returns start and end in UTC, plus the slack to apply when the zone is not exact.
func eventSpan(comp *ical.Component) (time.Time, time.Time, time.Duration, error) {
	event := ical.Event{Component: comp}
	var slack time.Duration
	start, err := event.DateTimeStart(time.UTC)
	if err != nil {
		start, err = parseFloating(comp.Props.Get(ical.PropDateTimeStart))
		if err != nil {
			return time.Time{}, time.Time{}, 0, fmt.Errorf("%w: DTSTART", ErrInvalidData)
		}
		slack = zoneSlack
	}
	if isFloating(comp.Props.Get(ical.PropDateTimeStart)) {
		slack = zoneSlack
	}
	end, err := event.DateTimeEnd(time.UTC)
	if err != nil || end.Before(start) {
		if parsed, perr := parseFloating(comp.Props.Get(ical.PropDateTimeEnd)); perr == nil && !parsed.Before(start) {
			end = parsed
		} else {
			end = start
		}
	}
	if end.Equal(start) && isDate(comp.Props.Get(ical.PropDateTimeStart)) {
		end = start.Add(24 * time.Hour)
	}
	return start, end, slack, nil
}

func isDate(p *ical.Prop) bool {
	return p != nil && p.ValueType() == ical.ValueDate
}

// isFloating reports a DATE or a DATE-TIME with neither TZID nor a Z suffix.
func isFloating(p *ical.Prop) bool {
	if p == nil {
		return false
	}
	return isDate(p) || (p.Params.Get(ical.ParamTimezoneID) == "" && !strings.HasSuffix(p.Value, "Z"))
}

// parseFloating reads the wall-clock value as UTC, ignoring an unresolvable TZID.
func parseFloating(p *ical.Prop) (time.Time, error) {
	if p == nil {
		return time.Time{}, errors.New("missing")
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		if t, err := time.ParseInLocation(layout, p.Value, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unparseable")
}

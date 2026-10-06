// Package calendar holds pure iCalendar rules: what KyCalendar accepts and how it is indexed.
package calendar

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
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

// maxIndexOccurrences is one budget per object, shared by every VEVENT and RDATE instance;
// exhausting it indexes the object as unbounded.
const maxIndexOccurrences = 100000

// Inspect validates a decoded calendar object and computes its index bounds.
func Inspect(cal *ical.Calendar) (Object, error) {
	var o Object
	first, last := int64(0), int64(0)
	unbounded, seen := false, false
	budget := maxIndexOccurrences

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

		if comp.Props.Get(ical.PropRecurrenceRule) != nil && !unbounded {
			rset, err := comp.RecurrenceSet(time.UTC)
			if err != nil || rset == nil {
				// Not expandable here (e.g. unknown TZID); index conservatively.
				unbounded = true
			} else if lastOcc, ok := lastOccurrence(rset, start.Add(horizon), &budget); !ok {
				unbounded = true
			} else if !lastOcc.IsZero() {
				compLast = lastOcc.Add(end.Sub(start)).Add(slack)
			}
		}

		rFirst, rLast, ok := rdateBounds(comp, end.Sub(start), &budget)
		if !ok {
			unbounded = true
		}
		compFirst, compLast = minTime(compFirst, rFirst), maxTime(compLast, rLast)

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

// lastOccurrence walks the set until it ends, spending from budget. ok is false when an
// occurrence passes limit or the budget runs out, i.e. the rule is treated as unbounded.
func lastOccurrence(rset *rrule.Set, limit time.Time, budget *int) (last time.Time, ok bool) {
	next := rset.Iterator()
	for *budget > 0 {
		*budget--
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

// rdateBounds returns the widest span of all RDATE instances (zero times when there are none).
// go-ical's RecurrenceSet does not read RDATE, so it is scanned here. ok is false when a value
// is unparseable or the budget runs out. Parsing is bounded by object size, so it always finishes.
func rdateBounds(comp *ical.Component, dur time.Duration, budget *int) (first, last time.Time, ok bool) {
	ok = true
	for _, p := range comp.Props[ical.PropRecurrenceDates] {
		for _, v := range strings.Split(p.Value, ",") {
			*budget--
			if *budget < 0 {
				ok = false
			}
			start, end, slack, err := parseRDate(&p, strings.TrimSpace(v), dur)
			if err != nil {
				ok = false
				continue
			}
			first, last = minTime(first, start.Add(-slack)), maxTime(last, end.Add(slack))
		}
	}
	return first, last, ok
}

// parseRDate reads one RDATE value: DATE, DATE-TIME, or PERIOD (start/end or start/duration).
func parseRDate(p *ical.Prop, v string, dur time.Duration) (start, end time.Time, slack time.Duration, err error) {
	startText, rest, isPeriod := strings.Cut(v, "/")
	loc := time.UTC
	if tzid := p.Params.Get(ical.ParamTimezoneID); tzid != "" {
		if l, lerr := time.LoadLocation(tzid); lerr == nil {
			loc = l
		} else {
			slack = zoneSlack
		}
	} else if !strings.HasSuffix(startText, "Z") {
		slack = zoneSlack
	}
	start, err = parseWall(startText, loc)
	if err != nil {
		return
	}
	end = start.Add(dur)
	if !isPeriod {
		return
	}
	if strings.HasPrefix(rest, "P") || strings.HasPrefix(rest, "+P") || strings.HasPrefix(rest, "-P") {
		d, derr := (&ical.Prop{Value: rest}).Duration()
		if derr != nil {
			return start, end, slack, derr
		}
		end = start.Add(d)
	} else if end, err = parseWall(rest, loc); err != nil {
		return
	}
	return
}

func parseWall(s string, loc *time.Location) (time.Time, error) {
	if strings.HasSuffix(s, "Z") {
		loc = time.UTC
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unparseable time")
}

// minTime and maxTime treat the zero time as "unset".
func minTime(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if a.IsZero() || b.After(a) {
		return b
	}
	return a
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
		} else if d, derr := durationOf(comp); derr == nil && d > 0 {
			end = start.Add(d)
		} else {
			end = start
		}
	}
	if end.Equal(start) && isDate(comp.Props.Get(ical.PropDateTimeStart)) {
		end = start.Add(24 * time.Hour)
	}
	return start, end, slack, nil
}

func durationOf(comp *ical.Component) (time.Duration, error) {
	p := comp.Props.Get(ical.PropDuration)
	if p == nil {
		return 0, errors.New("missing")
	}
	return p.Duration()
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

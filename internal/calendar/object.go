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

// maxRecurringComponents bounds RRULE-bearing VEVENTs evaluated per object: a rule can burn CPU
// without yielding occurrences, so the occurrence budget alone does not cap the work.
const maxRecurringComponents = 10

// fastFreq reports an RRULE with FREQ=SECONDLY or MINUTELY, which is never expanded.
func fastFreq(comp *ical.Component) bool {
	for _, p := range comp.Props[ical.PropRecurrenceRule] {
		for _, part := range strings.Split(strings.ToUpper(p.Value), ";") {
			if part == "FREQ=SECONDLY" || part == "FREQ=MINUTELY" {
				return true
			}
		}
	}
	return false
}

// Inspect validates a decoded calendar object and computes its index bounds.
func Inspect(cal *ical.Calendar) (Object, error) {
	var o Object
	first, last := int64(0), int64(0)
	unbounded, seen := false, false
	budget, recurring := maxIndexOccurrences, 0

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
			recurring++
			if recurring > maxRecurringComponents || slack > 0 || len(comp.Props[ical.PropRecurrenceRule]) > 1 || fastFreq(comp) {
				// Too many rules, inexact zone (UNTIL is UTC, expansion is wall-clock), several
				// rules, or too fine-grained: index unbounded without expanding.
				unbounded = true
			} else if lastOcc, ok := ruleLast(comp, start, &budget); !ok {
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

// ruleLast returns the last occurrence of the component's RRULE, spending from budget.
// ok is false (index as unbounded) when the rule has neither COUNT nor UNTIL, runs past the
// horizon, is unparseable, or exhausts the budget. EXDATE is ignored: it can only narrow.
// Iteration is cut at the horizon so rules that yield nothing stay cheap.
func ruleLast(comp *ical.Component, start time.Time, budget *int) (last time.Time, ok bool) {
	opt, err := comp.Props.RecurrenceRule()
	if err != nil || opt == nil {
		return time.Time{}, false
	}
	limit := start.Add(horizon)
	cut := false
	switch {
	case opt.Until.IsZero() && opt.Count == 0:
		return time.Time{}, false
	case opt.Until.After(limit):
		return time.Time{}, false
	case opt.Until.IsZero():
		opt.Until, cut = limit, true
	}
	opt.Dtstart = start
	rule, err := rrule.NewRRule(*opt)
	if err != nil {
		return time.Time{}, false
	}
	next := rule.Iterator()
	yielded := 0
	for *budget > 0 {
		*budget--
		occ, more := next()
		if !more {
			// Ended at the horizon before COUNT was reached: the rule continues beyond it.
			return last, !(cut && yielded < opt.Count)
		}
		yielded++
		last = occ
	}
	return time.Time{}, false
}

// rdateBounds returns the widest span of all RDATE instances (zero times when there are none).
// go-ical's RecurrenceSet does not read RDATE, so it is scanned here. ok is false when a value
// is unparseable or the budget runs out, at which point parsing stops.
func rdateBounds(comp *ical.Component, dur time.Duration, budget *int) (first, last time.Time, ok bool) {
	ok = true
	for _, p := range comp.Props[ical.PropRecurrenceDates] {
		loc, zslack := rdateZone(&p)
		for _, v := range strings.Split(p.Value, ",") {
			if *budget <= 0 {
				// Out of budget: stop parsing; FirstStart falls to the epoch to stay conservative.
				return time.Unix(0, 0), last, false
			}
			*budget--
			start, end, slack, err := parseRDate(strings.TrimSpace(v), loc, zslack, dur)
			if err != nil {
				ok = false
				continue
			}
			first, last = minTime(first, start.Add(-slack)), maxTime(last, end.Add(slack))
		}
	}
	return first, last, ok
}

// rdateZone resolves an RDATE property's zone once; slack is set when it is unknown.
func rdateZone(p *ical.Prop) (*time.Location, time.Duration) {
	tzid := p.Params.Get(ical.ParamTimezoneID)
	if tzid == "" {
		return time.UTC, 0
	}
	if l, err := time.LoadLocation(tzid); err == nil {
		return l, 0
	}
	return time.UTC, zoneSlack
}

// parseRDate reads one RDATE value: DATE, DATE-TIME, or PERIOD (start/end or start/duration).
func parseRDate(v string, loc *time.Location, slack, dur time.Duration) (start, end time.Time, _ time.Duration, err error) {
	startText, rest, isPeriod := strings.Cut(v, "/")
	if !strings.HasSuffix(startText, "Z") && loc == time.UTC {
		slack = zoneSlack // floating, or TZID unresolved
	}
	start, err = parseWall(startText, loc)
	if err != nil {
		return
	}
	end = start.Add(dur)
	if !isPeriod {
		return start, end, slack, nil
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
	return start, end, slack, nil
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

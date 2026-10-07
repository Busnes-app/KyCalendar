package calendar

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// Instance is one occurrence of an event as a viewer sees it.
type Instance struct {
	UID          string
	RecurrenceID string    // the occurrence's original start as a key; "" when not recurring
	Start, End   time.Time // absolute; an all-day instance spans viewer-local midnights
	AllDay       bool
	Floating     bool // wall-clock time with no zone, shown in the viewer's zone
	UnknownZone  bool // TZID unresolved: parsed as UTC
	Recurring    bool
	Override     bool // a RECURRENCE-ID component replaced the generated occurrence
	Partial      bool // the rule is not expanded safely; only the first occurrence is shown
	Event        *ical.Component
}

var ErrTooManyInstances = errors.New("calendar: too many instances in range")

// span is a VEVENT's start and length, resolved for one viewer.
type span struct {
	start                     time.Time
	dur                       time.Duration
	days                      int // all-day length in calendar days
	allDay, floating, unknown bool
}

// zoneOf resolves a date-time property: UTC for a Z value, the viewer's zone for dates and
// floating times, the TZID's zone, or UTC with unknown=true for an unresolvable TZID.
func zoneOf(p *ical.Prop, viewer *time.Location) (loc *time.Location, floating, unknown bool) {
	switch {
	case strings.HasSuffix(p.Value, "Z"):
		return time.UTC, false, false
	case isDate(p):
		return viewer, false, false
	}
	tzid := p.Params.Get(ical.ParamTimezoneID)
	if tzid == "" {
		return viewer, true, false
	}
	if loc, ok := ResolveZone(tzid); ok {
		return loc, false, false
	}
	return time.UTC, false, true
}

func spanOf(comp *ical.Component, viewer *time.Location) (span, error) {
	sp := comp.Props.Get(ical.PropDateTimeStart)
	if sp == nil {
		return span{}, fmt.Errorf("%w: DTSTART", ErrInvalidData)
	}
	loc, floating, unknown := zoneOf(sp, viewer)
	start, err := parseWall(sp.Value, loc)
	if err != nil {
		return span{}, fmt.Errorf("%w: DTSTART", ErrInvalidData)
	}
	s := span{start: start, allDay: isDate(sp), floating: floating, unknown: unknown}
	if ep := comp.Props.Get(ical.PropDateTimeEnd); ep != nil {
		eloc, _, _ := zoneOf(ep, viewer)
		if end, err := parseWall(ep.Value, eloc); err == nil && !end.Before(start) {
			s.dur = end.Sub(start)
		}
	} else if d, err := durationOf(comp); err == nil && d > 0 {
		s.dur = d
	}
	if s.allDay {
		s.days = max(1, int(math.Round(s.dur.Hours()/24)))
	}
	return s, nil
}

func (s span) endAt(occ time.Time) time.Time {
	if s.allDay {
		return occ.AddDate(0, 0, s.days)
	}
	return occ.Add(s.dur)
}

// occurrenceKey names an occurrence by its original start: a date for all-day events, the
// wall-clock time for floating events, else the UTC instant.
func occurrenceKey(t time.Time, allDay, floating bool) string {
	switch {
	case allDay:
		return t.Format("20060102")
	case floating:
		return t.Format("20060102T150405")
	}
	return t.UTC().Format("20060102T150405Z")
}

// dateList reads an EXDATE or RDATE value list; a PERIOD value contributes its start.
func dateList(p *ical.Prop, viewer *time.Location) ([]time.Time, error) {
	loc, _, _ := zoneOf(p, viewer)
	var out []time.Time
	for _, v := range strings.Split(p.Value, ",") {
		v, _, _ = strings.Cut(strings.TrimSpace(v), "/")
		t, err := parseWall(v, loc)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// ruleSet builds a master's recurrence set: nil for a non-recurring event, partial=true for a
// rule Inspect also refuses to expand (several RRULEs, SECONDLY/MINUTELY, an oversized time set,
// an unparseable rule or RDATE). DTSTART is always an instance (RFC 5545), so it is added as an
// RDATE; the set de-duplicates it when the rule yields it too.
func ruleSet(comp *ical.Component, s span, viewer *time.Location) (set *rrule.Set, partial bool) {
	rules, rdates := comp.Props[ical.PropRecurrenceRule], comp.Props[ical.PropRecurrenceDates]
	if len(rules) == 0 && len(rdates) == 0 {
		return nil, false
	}
	if len(rules) > 1 || fastFreq(comp) {
		return nil, true
	}
	set = &rrule.Set{}
	set.DTStart(s.start)
	set.RDate(s.start)
	if len(rules) == 1 {
		opt, err := comp.Props.RecurrenceRule()
		if err != nil || opt == nil || wideTimeSet(opt) {
			return nil, true
		}
		opt.Dtstart = s.start
		r, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, true
		}
		set.RRule(r)
	}
	for _, p := range rdates {
		ts, err := dateList(&p, viewer)
		if err != nil {
			return nil, true
		}
		for _, t := range ts {
			set.RDate(t)
		}
	}
	for _, p := range comp.Props[ical.PropExceptionDates] {
		ts, err := dateList(&p, viewer)
		if err != nil {
			continue // an unreadable EXDATE only fails to hide an occurrence
		}
		for _, t := range ts {
			set.ExDate(t)
		}
	}
	return set, false
}

func overlaps(start, end, from, to time.Time) bool {
	if end.Equal(start) {
		return !start.Before(from) && start.Before(to)
	}
	return start.Before(to) && end.After(from)
}

// Expand returns cal's instances overlapping [from, to), sorted by start. More than limit
// instances is ErrTooManyInstances. Each recurring master spends at most maxIndexOccurrences
// rule iterations, so a rule that yields nothing in range still stops.
func Expand(cal *ical.Calendar, from, to time.Time, viewer *time.Location, limit int) ([]Instance, error) {
	var masters []*ical.Component
	overrides := map[string]*ical.Component{}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		rid := c.Props.Get(ical.PropRecurrenceID)
		if rid == nil {
			masters = append(masters, c)
			continue
		}
		loc, floating, _ := zoneOf(rid, viewer)
		t, err := parseWall(rid.Value, loc)
		if err != nil {
			continue
		}
		overrides[occurrenceKey(t, isDate(rid), floating)] = c
	}

	var out []Instance
	add := func(in Instance) error {
		if len(out) >= limit {
			return ErrTooManyInstances
		}
		out = append(out, in)
		return nil
	}
	for _, m := range masters {
		s, err := spanOf(m, viewer)
		if err != nil {
			return nil, err
		}
		uid, _ := m.Props.Text(ical.PropUID)
		base := Instance{UID: uid, AllDay: s.allDay, Floating: s.floating, UnknownZone: s.unknown, Event: m}
		set, partial := ruleSet(m, s, viewer)
		if set == nil {
			in := base
			in.Start, in.End = s.start, s.endAt(s.start)
			if partial {
				in.Recurring, in.Partial = true, true
				in.RecurrenceID = occurrenceKey(s.start, s.allDay, s.floating)
			}
			if overlaps(in.Start, in.End, from, to) {
				if err := add(in); err != nil {
					return nil, err
				}
			}
			continue
		}
		base.Recurring = true
		next := set.Iterator()
		for budget := maxIndexOccurrences; budget > 0; budget-- {
			occ, ok := next()
			if !ok || !occ.Before(to) {
				break
			}
			end := s.endAt(occ)
			if !overlaps(occ, end, from, to) {
				continue
			}
			key := occurrenceKey(occ, s.allDay, s.floating)
			if _, moved := overrides[key]; moved {
				continue
			}
			in := base
			in.RecurrenceID, in.Start, in.End = key, occ, end
			if err := add(in); err != nil {
				return nil, err
			}
		}
	}
	for key, o := range overrides {
		s, err := spanOf(o, viewer)
		if err != nil {
			return nil, err
		}
		uid, _ := o.Props.Text(ical.PropUID)
		in := Instance{UID: uid, RecurrenceID: key, Start: s.start, End: s.endAt(s.start), AllDay: s.allDay,
			Floating: s.floating, UnknownZone: s.unknown, Recurring: true, Override: true, Event: o}
		if overlaps(in.Start, in.End, from, to) {
			if err := add(in); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].RecurrenceID < out[j].RecurrenceID
	})
	return out, nil
}

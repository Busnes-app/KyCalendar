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

// freqUnit is the nominal length of one period, short enough that estimates err on the high side.
func freqUnit(f rrule.Frequency) time.Duration {
	switch f {
	case rrule.YEARLY:
		return 365 * 24 * time.Hour
	case rrule.MONTHLY:
		return 28 * 24 * time.Hour
	case rrule.WEEKLY:
		return 7 * 24 * time.Hour
	case rrule.DAILY:
		return 24 * time.Hour
	}
	return time.Hour
}

// periods counts whole intervals of the rule in d without overflowing: a client INTERVAL can be
// any int, so unit x interval is never formed before it is known to fit in d.
func periods(opt *rrule.ROption, d time.Duration) int {
	unit, n := freqUnit(opt.Freq), max(opt.Interval, 1)
	if d <= 0 || n > int(d/unit) {
		return 0
	}
	return int(d / (unit * time.Duration(n)))
}

// zonedUntil re-reads a date or floating UNTIL as wall time in loc; go-ical parses it as UTC.
func zonedUntil(rule ical.Prop, loc *time.Location) (time.Time, bool) {
	for _, part := range strings.Split(rule.Value, ";") {
		k, v, _ := strings.Cut(part, "=")
		if strings.EqualFold(k, "UNTIL") && !strings.HasSuffix(v, "Z") {
			t, err := parseWall(v, loc)
			return t, err == nil
		}
	}
	return time.Time{}, false
}

// tooLong reports a COUNT rule that cannot be enumerated within the iteration budget.
func tooLong(opt *rrule.ROption, to time.Time) bool {
	return opt.Count > maxIndexOccurrences || periods(opt, to.Sub(opt.Dtstart)) > maxIndexOccurrences
}

// skipAhead moves DTSTART of a COUNT-less DAILY, WEEKLY or HOURLY rule by whole intervals to just
// before from, so an old series does not spend its budget on the past. MONTHLY and YEARLY stay put.
func skipAhead(opt *rrule.ROption, from time.Time) {
	k := periods(opt, from.Sub(opt.Dtstart)) - 1
	if k <= 0 {
		return
	}
	n := max(opt.Interval, 1)
	switch opt.Freq {
	case rrule.DAILY:
		opt.Dtstart = opt.Dtstart.AddDate(0, 0, k*n)
	case rrule.WEEKLY:
		opt.Dtstart = opt.Dtstart.AddDate(0, 0, 7*k*n)
	case rrule.HOURLY:
		opt.Dtstart = opt.Dtstart.Add(time.Duration(k*n) * time.Hour)
	}
}

// boundedRule parses comp's single RRULE for expansion over [from, to): nil when it is not safe.
func boundedRule(rule ical.Prop, comp *ical.Component, s span, from, to time.Time) *rrule.ROption {
	opt, err := comp.Props.RecurrenceRule()
	if err != nil || opt == nil || wideTimeSet(opt) {
		return nil
	}
	if !ordinalsInRange(opt) || !satisfiable(opt) {
		return nil
	}
	opt.Dtstart = s.start
	if u, ok := zonedUntil(rule, s.start.Location()); ok {
		opt.Until = u
	}
	if opt.Until.IsZero() || opt.Until.After(to) {
		opt.Until = to
	}
	if opt.Count > 0 {
		if tooLong(opt, to) {
			return nil
		}
	} else if opt.Freq == rrule.DAILY || opt.Freq == rrule.WEEKLY || opt.Freq == rrule.HOURLY {
		skipAhead(opt, from)
	}
	return opt
}

// ruleSet builds a master's recurrence set: nil for a non-recurring event, partial=true for a
// rule that is not expanded safely (several RRULEs, SECONDLY/MINUTELY, an oversized time set, an
// unparseable rule or RDATE, a COUNT too long for the budget). DTSTART is always an instance
// (RFC 5545), so the original start is added as an RDATE; the set de-duplicates it.
func ruleSet(comp *ical.Component, s span, viewer *time.Location, from, to time.Time) (set *rrule.Set, partial bool) {
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
		opt := boundedRule(rules[0], comp, s, from, to)
		if opt == nil {
			return nil, true
		}
		set.DTStart(opt.Dtstart)
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

// overrideKey keys a RECURRENCE-ID in the master's form (date, floating wall time or instant),
// whatever form the RECURRENCE-ID itself is written in.
func overrideKey(rid *ical.Prop, viewer *time.Location, form *span) (string, bool) {
	loc, floating, _ := zoneOf(rid, viewer)
	allDay := isDate(rid)
	if form != nil && (form.allDay || form.floating) {
		loc, allDay, floating = viewer, form.allDay, form.floating
	}
	t, err := parseWall(rid.Value, loc)
	if err != nil {
		return "", false
	}
	return occurrenceKey(t, allDay, floating), true
}

// Expand returns cal's instances overlapping [from, to), sorted by start. More than limit
// instances, or an object that spends the shared maxIndexOccurrences iteration budget before
// reaching to, is ErrTooManyInstances. At most maxRecurringComponents masters are expanded; the
// rest show only DTSTART, flagged Partial.
func Expand(cal *ical.Calendar, from, to time.Time, viewer *time.Location, limit int) ([]Instance, error) {
	var masters, ridComps []*ical.Component
	for _, c := range cal.Children {
		switch {
		case c.Name != ical.CompEvent:
		case c.Props.Get(ical.PropRecurrenceID) == nil:
			masters = append(masters, c)
		default:
			ridComps = append(ridComps, c)
		}
	}
	var form *span
	if len(masters) > 0 {
		if s, err := spanOf(masters[0], viewer); err == nil {
			form = &s
		}
	}
	overrides := map[string]*ical.Component{}
	for _, c := range ridComps {
		if key, ok := overrideKey(c.Props.Get(ical.PropRecurrenceID), viewer, form); ok {
			overrides[key] = c
		}
	}

	var out []Instance
	add := func(in Instance) error {
		if len(out) >= limit {
			return ErrTooManyInstances
		}
		out = append(out, in)
		return nil
	}
	budget, recurring := maxIndexOccurrences, 0
	for _, m := range masters {
		s, err := spanOf(m, viewer)
		if err != nil {
			return nil, err
		}
		uid, _ := m.Props.Text(ical.PropUID)
		base := Instance{UID: uid, AllDay: s.allDay, Floating: s.floating, UnknownZone: s.unknown, Event: m}
		var set *rrule.Set
		partial := false
		if len(m.Props[ical.PropRecurrenceRule])+len(m.Props[ical.PropRecurrenceDates]) > 0 {
			if recurring++; recurring > maxRecurringComponents {
				partial = true
			} else {
				set, partial = ruleSet(m, s, viewer, from, to)
			}
		}
		if set == nil {
			in := base
			in.Start, in.End = s.start, s.endAt(s.start)
			if partial {
				in.Recurring, in.Partial = true, true
				in.RecurrenceID = occurrenceKey(s.start, s.allDay, s.floating)
				if _, moved := overrides[in.RecurrenceID]; moved {
					continue
				}
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
		done := false
		for budget > 0 {
			budget--
			occ, ok := next()
			if !ok || !occ.Before(to) {
				done = true
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
		if !done {
			return nil, fmt.Errorf("%w: recurrence expansion budget spent", ErrTooManyInstances)
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

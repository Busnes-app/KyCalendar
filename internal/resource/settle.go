package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
)

// Booking answers.
const (
	Accepted = "accepted"
	Pending  = "pending"
	Declined = "declined"
)

// Reasons a booking is declined.
const (
	ReasonUnknown       = "unknown"
	ReasonDisabled      = "disabled"
	ReasonNotAllowed    = "not_allowed"
	ReasonPast          = "past"
	ReasonBeyond        = "beyond_horizon"
	ReasonTooLong       = "too_long"
	ReasonOccurrences   = "too_many_occurrences"
	ReasonTooMany       = "too_many_resources"
	ReasonBusy          = "busy"
	ReasonRejected      = "rejected"
	ReasonAmbiguous     = "ambiguous"
	MaxPerEvent         = 10
	MaxOccurrences      = 1000
	maxConflictsNamed   = 5
	conflictDateLayout  = time.DateOnly
	resourceParamCUType = "CUTYPE"
)

// Span is one busy interval, unix seconds, end exclusive.
type Span struct{ Start, End int64 }

// IDs lists the resources cal's VEVENTs name as attendees, in first-seen order.
func IDs(cal *ical.Calendar) []string {
	var out []string
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		for _, p := range c.Props[ical.PropAttendee] {
			if a, ok := itip.ParseAddress(p.Value); ok {
				if id, ok := IDOf(a); ok && !slices.Contains(out, id) {
					out = append(out, id)
				}
			}
		}
	}
	return out
}

// Fingerprint hashes what decides a booking: every VEVENT's times, recurrence and status. A
// write that leaves it alone (a new title, other attendees) keeps the resource's answer.
func Fingerprint(cal *ical.Calendar) string {
	var parts []string
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		var b strings.Builder
		for _, name := range []string{ical.PropRecurrenceID, ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDuration,
			ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates, ical.PropStatus} {
			for _, p := range c.Props[name] {
				b.WriteString(name + ";" + p.Params.Get(ical.ParamTimezoneID) + ";" + p.Params.Get(ical.ParamValue) + ":" + p.Value + "\n")
			}
		}
		parts = append(parts, b.String())
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Decision is the answer for one resource on one event, before any busy check.
type Decision struct {
	Status, Reason, Detail string
	Spans                  []Span // the occurrences within the horizon that need the resource, start order
	// OpenAfter, when not 0, is where Spans stop although the series goes on (unix seconds): its
	// later occurrences are reserved too, and later bookings are checked against them.
	OpenAfter int64
	// Moved are the spans of Spans that a RECURRENCE-ID component gives, not the rule.
	Moved map[Span]bool
}

// Plan decides what r's booking of cal needs at now: the spans to reserve, or a declined
// reason. lastEnd is the object's indexed end (nil: recurs without end). Every occurrence
// from now to the horizon is checked; a series may run past it. It checks the definition
// only; who may book and what is busy are the caller's.
func Plan(cal *ical.Calendar, r *Resource, lastEnd *int64, now time.Time) Decision {
	if r == nil {
		return Decision{Status: Declined, Reason: ReasonUnknown}
	}
	if !r.Enabled {
		return Decision{Status: Declined, Reason: ReasonDisabled}
	}
	horizon := now.Add(time.Duration(r.HorizonDays) * 24 * time.Hour)
	beyond := Decision{Status: Declined, Reason: ReasonBeyond, Detail: horizon.UTC().Format(conflictDateLayout)}
	if lastEnd != nil && *lastEnd <= now.Unix() {
		return Decision{Status: Declined, Reason: ReasonPast}
	}
	open := lastEnd == nil || *lastEnd > horizon.Unix()
	if open && !calendar.Recurring(cal) {
		return beyond
	}
	to := horizon
	if !open {
		to = time.Unix(*lastEnd, 0).Add(time.Second)
	}
	insts, err := calendar.Expand(cal, now, to, time.UTC, MaxOccurrences)
	if err != nil {
		return Decision{Status: Declined, Reason: ReasonOccurrences}
	}
	if len(insts) == 0 {
		if open {
			return beyond
		}
		return Decision{Status: Declined, Reason: ReasonPast}
	}
	if ambiguous(cal) {
		return Decision{Status: Declined, Reason: ReasonAmbiguous}
	}
	if tooLong(cal, time.Duration(r.MaxMinutes)*time.Minute) {
		return Decision{Status: Declined, Reason: ReasonTooLong, Detail: strconv.Itoa(r.MaxMinutes)}
	}
	d := Decision{Status: Accepted, Moved: map[Span]bool{}}
	if r.Approval == Approvers {
		d.Status = Pending
	}
	if open {
		d.OpenAfter = horizon.Unix()
	}
	for _, in := range insts {
		if in.Partial {
			return Decision{Status: Declined, Reason: ReasonOccurrences}
		}
		if Cancelled(in.Event) {
			continue
		}
		if in.End.Sub(in.Start) > time.Duration(r.MaxMinutes)*time.Minute {
			return Decision{Status: Declined, Reason: ReasonTooLong, Detail: strconv.Itoa(r.MaxMinutes)}
		}
		sp := Span{in.Start.Unix(), in.End.Unix()}
		d.Spans = append(d.Spans, sp)
		if in.Override {
			d.Moved[sp] = true
		}
	}
	return d
}

// ambiguous reports an object whose occurrences could be read two ways: a VEVENT with both DTEND
// and DURATION, or two components for one occurrence. No resource is booked on a guess.
func ambiguous(cal *ical.Calendar) bool {
	seen := map[string]bool{}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if c.Props.Get(ical.PropDateTimeEnd) != nil && c.Props.Get(ical.PropDuration) != nil {
			return true
		}
		key := ""
		if rid := c.Props.Get(ical.PropRecurrenceID); rid != nil {
			k, ok := calendar.OccurrenceKey(cal, rid)
			if !ok {
				return true
			}
			key = "rid:" + k
		}
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

// tooLong reports a VEVENT (master or override) or an RDATE period longer than max anywhere in
// the series: an open series reserves them past the horizon, where Plan does not expand.
func tooLong(cal *ical.Calendar, max time.Duration) bool {
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if l, ok := componentLength(c); !ok || l > max {
			return true // a length expansion cannot tell could be any
		}
		for _, p := range c.Props[ical.PropRecurrenceDates] {
			// A period is known by its value: expansion reads one whatever VALUE says.
			for _, v := range strings.Split(p.Value, ",") {
				from, to, ok := strings.Cut(v, "/")
				if !ok {
					continue
				}
				start := ical.Prop{Name: ical.PropDateTimeStart, Value: from, Params: ical.Params{}}
				if tz := p.Params.Get(ical.ParamTimezoneID); tz != "" {
					start.Params.Set(ical.ParamTimezoneID, tz)
				}
				s, err := start.DateTime(time.UTC)
				if err != nil {
					return true // a period we cannot read could be any length
				}
				var l time.Duration
				if strings.ContainsAny(to, "Pp") {
					d := ical.Prop{Name: ical.PropDuration, Value: to, Params: ical.Params{}}
					l, err = d.Duration()
				} else {
					end := start
					end.Value = to
					var e time.Time
					e, err = end.DateTime(time.UTC)
					l = e.Sub(s)
				}
				if err != nil || l > max {
					return true
				}
			}
		}
	}
	return false
}

// componentLength is a VEVENT's own length as expansion reads it (implicit all-day day
// included), by expanding a copy without its recurrence properties.
func componentLength(c *ical.Component) (time.Duration, bool) {
	one := ical.NewComponent(ical.CompEvent)
	for name, props := range c.Props {
		switch name {
		case ical.PropRecurrenceID, ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates:
			continue
		}
		one.Props[name] = props
	}
	tmp := &ical.Calendar{Component: &ical.Component{Name: ical.CompCalendar, Props: ical.Props{}, Children: []*ical.Component{one}}}
	insts, err := calendar.Expand(tmp, time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC, 2)
	if err != nil || len(insts) != 1 {
		return 0, false
	}
	return insts[0].End.Sub(insts[0].Start), true
}

// SeriesFingerprint hashes the timing of the components without RECURRENCE-ID: the rule itself.
// An attendee's answer to one occurrence adds an override but leaves it alone.
func SeriesFingerprint(cal *ical.Calendar) string {
	masters := &ical.Calendar{Component: &ical.Component{Name: ical.CompCalendar, Props: cal.Props}}
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) == nil {
			masters.Children = append(masters.Children, c)
		}
	}
	return Fingerprint(masters)
}

// LaterOverrides hashes the timing of the RECURRENCE-ID components that start, or replace an
// occurrence, at or after after (unix seconds): the overrides a series reserves past its horizon.
func LaterOverrides(cal *ical.Calendar, after int64) string {
	later := &ical.Calendar{Component: &ical.Component{Name: ical.CompCalendar, Props: cal.Props}}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent || c.Props.Get(ical.PropRecurrenceID) == nil {
			continue
		}
		rid, err1 := c.Props.DateTime(ical.PropRecurrenceID, time.UTC)
		start, err2 := c.Props.DateTime(ical.PropDateTimeStart, time.UTC)
		if err1 != nil || err2 != nil || rid.Unix() >= after || start.Unix() >= after {
			later.Children = append(later.Children, c)
		}
	}
	return Fingerprint(later)
}

// Covered reports whether d asks for nothing a booking does not already hold: every span is one
// of held, or a rule occurrence at or after the booking's openAfter (which its series reserves).
func (d Decision) Covered(held map[Span]bool, openAfter int64) bool {
	for _, sp := range d.Spans {
		if !held[sp] && (d.Moved[sp] || openAfter == 0 || sp.Start < openAfter) {
			return false
		}
	}
	return true
}

// Cancelled reports STATUS:CANCELLED: such an occurrence needs no resource.
func Cancelled(c *ical.Component) bool {
	st, _ := c.Props.Text(ical.PropStatus)
	return strings.EqualFold(st, "CANCELLED")
}

// Conflicts lists the starts of want's spans that overlap any of busy.
func Conflicts(want, busy []Span) []int64 {
	var out []int64
	for _, w := range want {
		for _, b := range busy {
			if b.Start < w.End && b.End > w.Start {
				out = append(out, w.Start)
				break
			}
		}
	}
	return out
}

// OpenSpans are cal's occurrences in [from, to) at or after openAfter: what an accepted series
// reserves past its checked horizon.
func OpenSpans(cal *ical.Calendar, openAfter, from, to int64) []Span {
	from = max(from, openAfter)
	if from >= to {
		return nil
	}
	insts, err := calendar.Expand(cal, time.Unix(from, 0), time.Unix(to, 0), time.UTC, MaxOccurrences)
	if err != nil {
		return []Span{{from, to}} // too many to tell apart: the whole range is taken
	}
	var out []Span
	for _, in := range insts {
		if !Cancelled(in.Event) && in.Start.Unix() >= openAfter {
			out = append(out, Span{in.Start.Unix(), in.End.Unix()})
		}
	}
	return out
}

// ConflictDetail names up to five conflicting dates (UTC) for a declined reason.
func ConflictDetail(starts []int64) string {
	var dates []string
	for _, s := range starts {
		d := time.Unix(s, 0).UTC().Format(conflictDateLayout)
		if !slices.Contains(dates, d) {
			dates = append(dates, d)
		}
		if len(dates) == maxConflictsNamed {
			break
		}
	}
	return strings.Join(dates, ", ")
}

// Partstat is the iCalendar answer for a booking status.
func Partstat(status string) string {
	switch status {
	case Accepted:
		return "ACCEPTED"
	case Declined:
		return "DECLINED"
	}
	return "NEEDS-ACTION"
}

// SetAnswers writes each resource attendee's answer (by resource ID; missing: declined) and
// identity (CUTYPE, CN, ROLE) into cal, and reports whether anything changed.
func SetAnswers(cal *ical.Calendar, answers map[string]string, defs map[string]*Resource) bool {
	changed := false
	set := func(p *ical.Prop, name, value string) {
		if value == "" {
			if p.Params.Get(name) != "" {
				p.Params.Del(name)
				changed = true
			}
			return
		}
		if p.Params.Get(name) != value || len(p.Params[name]) != 1 {
			p.Params.Set(name, value)
			changed = true
		}
	}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		atts := c.Props[ical.PropAttendee]
		for i := range atts {
			a, ok := itip.ParseAddress(atts[i].Value)
			if !ok {
				continue
			}
			id, ok := IDOf(a)
			if !ok {
				continue
			}
			status, ok := answers[id]
			if !ok {
				status = Declined
			}
			set(&atts[i], ical.ParamParticipationStatus, Partstat(status))
			set(&atts[i], ical.ParamRSVP, "")
			set(&atts[i], ical.ParamRole, "NON-PARTICIPANT")
			if r := defs[id]; r != nil {
				cutype := "ROOM"
				if r.Kind == Equipment {
					cutype = "RESOURCE"
				}
				set(&atts[i], resourceParamCUType, cutype)
				set(&atts[i], ical.ParamCommonName, r.Name)
			}
		}
	}
	return changed
}

// ErrNotOrganizer is an event whose organizer another person is: only the organizer changes
// its resources.
var ErrNotOrganizer = errors.New("resource: only the organizer changes the rooms")

// SetList makes ids the resources of every VEVENT of cal, keeping the answers of those already
// there, and fills an empty LOCATION with the rooms' names and locations. self, when known,
// becomes the organizer of an event that has none, and its first attendee.
func SetList(cal *ical.Calendar, self itip.Address, ids []string, defs map[string]*Resource) error {
	if org, ok := itip.Organizer(cal); ok && org != self {
		return ErrNotOrganizer
	}
	var place []string
	for _, id := range ids {
		if r := defs[id]; r != nil && r.Kind == Room {
			place = append(place, strings.Join(slices.DeleteFunc([]string{r.Name, r.Location}, func(s string) bool { return s == "" }), ", "))
		}
	}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		old := map[string]ical.Prop{}
		var people []ical.Prop
		for _, p := range c.Props[ical.PropAttendee] {
			if a, ok := itip.ParseAddress(p.Value); ok {
				if id, ok := IDOf(a); ok {
					old[id] = p
					continue
				}
			}
			people = append(people, p)
		}
		if len(ids) > 0 && c.Props.Get(ical.PropOrganizer) == nil && self != "" {
			org := ical.NewProp(ical.PropOrganizer)
			org.Value = string(self)
			c.Props.Set(org)
			if len(people) == 0 {
				me := ical.NewProp(ical.PropAttendee)
				me.Value = string(self)
				me.Params.Set(ical.ParamParticipationStatus, "ACCEPTED")
				people = append(people, *me)
			}
		}
		c.Props.Del(ical.PropAttendee)
		for i := range people {
			c.Props.Add(&people[i])
		}
		for _, id := range ids {
			p, ok := old[id]
			if !ok {
				np := ical.NewProp(ical.PropAttendee)
				np.Value = string(Address(id))
				np.Params.Set(ical.ParamParticipationStatus, "NEEDS-ACTION")
				p = *np
			}
			c.Props.Add(&p)
		}
		if loc, _ := c.Props.Text(ical.PropLocation); strings.TrimSpace(loc) == "" && len(place) > 0 && len(old) == 0 {
			c.Props.SetText(ical.PropLocation, strings.Join(place, "; "))
		}
		// An event left with only its organizer as attendee is no longer a meeting.
		if len(ids) == 0 && len(c.Props[ical.PropAttendee]) == 1 && self != "" {
			if a, ok := itip.ParseAddress(c.Props[ical.PropAttendee][0].Value); ok && a == self && len(old) > 0 {
				c.Props.Del(ical.PropAttendee)
				c.Props.Del(ical.PropOrganizer)
			}
		}
	}
	return nil
}

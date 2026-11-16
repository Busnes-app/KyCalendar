package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

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
	// Length prefixes preserve boundaries even when a quoted parameter contains delimiters.
	encode := func(fields []string) string {
		var b strings.Builder
		for _, field := range fields {
			b.WriteString(strconv.Itoa(len(field)))
			b.WriteByte(':')
			b.WriteString(field)
		}
		return b.String()
	}
	var parts []string
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		var fields []string
		for _, name := range []string{ical.PropRecurrenceID, ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDuration,
			ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates, ical.PropStatus} {
			for _, p := range c.Props[name] {
				fields = append(fields, name, p.Params.Get(ical.ParamTimezoneID), p.Params.Get(ical.ParamValue), p.Value)
			}
		}
		parts = append(parts, encode(fields))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(encode(parts)))
	return "v2:" + hex.EncodeToString(sum[:])
}

// Decision is the answer for one resource on one event, before any busy check.
type Decision struct {
	Status, Reason, Detail string
	Spans                  []Span // the occurrences within the horizon that need the resource, start order
	// OpenAfter, when not 0, is where Spans stop although the series goes on (unix seconds): its
	// later occurrences are reserved too, and later bookings are checked against them.
	OpenAfter int64
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
	insts, err := calendar.ExpandComplete(cal, now, to, time.UTC, MaxOccurrences)
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
	d := Decision{Status: Accepted}
	if r.Approval == Approvers {
		d.Status = Pending
	}
	if open {
		d.OpenAfter = horizon.Unix()
	}
	for _, in := range insts {
		if Cancelled(in.Event) {
			continue
		}
		if in.End.Sub(in.Start) > time.Duration(r.MaxMinutes)*time.Minute {
			return Decision{Status: Declined, Reason: ReasonTooLong, Detail: strconv.Itoa(r.MaxMinutes)}
		}
		d.Spans = append(d.Spans, Span{in.Start.Unix(), in.End.Unix()})
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

// Within reports whether every occurrence of next is an occurrence of prev, from now on: an
// attendee's answer to one occurrence, or a cancelled occurrence, asks for no new time. Both must
// share the series rule (SeriesFingerprint); past the last override of either, the rule alone
// makes both alike. An object too large to compare is not within.
func Within(prev, next *ical.Calendar, now time.Time) bool {
	if ambiguous(next) {
		return false
	}
	end := now.Add(24 * time.Hour)
	for _, cal := range []*ical.Calendar{prev, next} {
		for _, c := range cal.Children {
			if c.Name != ical.CompEvent || c.Props.Get(ical.PropRecurrenceID) == nil {
				continue
			}
			for _, name := range []string{ical.PropRecurrenceID, ical.PropDateTimeStart, ical.PropDateTimeEnd} {
				p := c.Props.Get(name)
				if p == nil {
					continue
				}
				// Use expansion's timezone resolution for each comparison bound too.
				one := ical.NewComponent(ical.CompEvent)
				one.Props[ical.PropDateTimeStart] = []ical.Prop{*p}
				t, _, _, err := calendar.SeriesTimes(one, time.UTC)
				if err != nil {
					return false
				}
				if t.After(end) {
					end = t
				}
			}
		}
	}
	end = end.Add(15 * 24 * time.Hour) // past the longest occurrence and any zone
	spans := func(cal *ical.Calendar) (map[Span]bool, bool) {
		insts, err := calendar.ExpandComplete(cal, now, end, time.UTC, MaxOccurrences)
		if err != nil {
			return nil, false
		}
		out := map[Span]bool{}
		for _, in := range insts {
			if !Cancelled(in.Event) {
				out[Span{in.Start.Unix(), in.End.Unix()}] = true
			}
		}
		return out, true
	}
	before, ok1 := spans(prev)
	after, ok2 := spans(next)
	if !ok1 || !ok2 {
		return false
	}
	for sp := range after {
		if !before[sp] {
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

// ExplicitFutureSpans expands the finite future dates and overrides of a series without its
// RRULE. Include spans crossing after: their remaining portion is still reserved.
// Each must be checked even though the ordinary rule is reserved past the horizon.
// False means expansion cannot account for every explicit reservation within the bound.
func ExplicitFutureSpans(cal *ical.Calendar, after int64) ([]Span, bool) {
	finite := *cal.Component
	finite.Children = nil
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			finite.Children = append(finite.Children, c)
			continue
		}
		copy := *c
		copy.Props = maps.Clone(c.Props)
		if len(c.Props[ical.PropRecurrenceRule]) > 0 && len(c.Props[ical.PropRecurrenceDates]) == 0 && c.Props.Get(ical.PropRecurrenceID) == nil {
			// Keep recurrence-set processing of EXDATE when the infinite rule is removed.
			start := *c.Props.Get(ical.PropDateTimeStart)
			start.Name = ical.PropRecurrenceDates
			copy.Props[ical.PropRecurrenceDates] = []ical.Prop{start}
		}
		delete(copy.Props, ical.PropRecurrenceRule)
		finite.Children = append(finite.Children, &copy)
	}
	insts, err := calendar.ExpandComplete(&ical.Calendar{Component: &finite}, time.Unix(after, 0), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC, MaxOccurrences)
	if err != nil {
		return nil, false
	}
	var spans []Span
	for _, in := range insts {
		if !Cancelled(in.Event) && in.End.Unix() > after {
			spans = append(spans, Span{in.Start.Unix(), in.End.Unix()})
		}
	}
	return spans, true
}

// FutureRuleConflicts proves rule separation or fully expands both calendars within the
// occurrence bound. A finite sample of an unbounded rule is never evidence of non-overlap.
// Explicit dates/overrides still require their separate admission windows.
func FutureRuleConflicts(a, b *ical.Calendar, after int64) ([]Span, bool) {
	hasRule := func(cal *ical.Calendar) bool {
		for _, c := range cal.Children {
			if c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) == nil &&
				!Cancelled(c) && len(c.Props[ical.PropRecurrenceRule]) > 0 {
				return true
			}
		}
		return false
	}
	if !hasRule(a) || !hasRule(b) {
		return nil, true
	}
	clockSpan := func(cal *ical.Calendar) (Span, bool) {
		var master *ical.Component
		for _, c := range cal.Children {
			if c.Name != ical.CompEvent {
				continue
			}
			if p := c.Props.Get(ical.PropRecurrenceID); p != nil {
				if p.Params.Get("RANGE") != "" {
					return Span{}, false
				}
				continue
			}
			if master != nil {
				return Span{}, false
			}
			master = c
		}
		if master == nil || len(master.Props[ical.PropRecurrenceRule]) != 1 {
			return Span{}, false
		}
		start := master.Props.Get(ical.PropDateTimeStart)
		if start == nil || start.Params.Get(ical.ParamTimezoneID) != "" {
			return Span{}, false
		}
		dt, err := time.Parse("20060102T150405Z", start.Value)
		if err != nil {
			return Span{}, false
		}
		if end := master.Props.Get(ical.PropDateTimeEnd); end != nil {
			if _, err := time.Parse("20060102T150405Z", end.Value); err != nil || end.Params.Get(ical.ParamTimezoneID) != "" {
				return Span{}, false
			}
		}
		rule, err := master.Props.RecurrenceRule()
		if err != nil || rule == nil || rule.Freq > rrule.DAILY || len(rule.Byhour)+len(rule.Byminute)+len(rule.Bysecond) > 0 {
			return Span{}, false
		}
		length, ok := componentLength(master)
		if !ok {
			return Span{}, false
		}
		startClock := int64(dt.Hour()*3600 + dt.Minute()*60 + dt.Second())
		endClock := startClock + int64(length/time.Second)
		if endClock > 24*3600 {
			return Span{}, false
		}
		return Span{startClock, endClock}, true
	}
	// Fixed, same-day UTC clock ranges cannot meet, regardless of the rule's dates.
	x, okA := clockSpan(a)
	y, okB := clockSpan(b)
	if okA && okB && (x.End <= y.Start || y.End <= x.Start) {
		return nil, true
	}
	expand := func(cal *ical.Calendar) ([]Span, bool) {
		info, err := calendar.Inspect(cal)
		if err != nil || info.LastEnd == nil {
			return nil, false
		}
		end := time.Unix(*info.LastEnd, 0).Add(time.Second)
		insts, err := calendar.ExpandComplete(cal, time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), end, time.UTC, MaxOccurrences)
		if err != nil {
			return nil, false
		}
		var out []Span
		for _, in := range insts {
			if !Cancelled(in.Event) && in.End.Unix() > after {
				out = append(out, Span{in.Start.Unix(), in.End.Unix()})
			}
		}
		return out, true
	}
	want, okA := expand(a)
	held, okB := expand(b)
	if !okA || !okB {
		return nil, false
	}
	var hits []Span
	for _, sp := range want {
		if len(Conflicts([]Span{sp}, held)) > 0 {
			hits = append(hits, sp)
		}
	}
	return hits, true
}

// OpenSpans are cal's occurrences in [from, to) at or after openAfter: what an accepted series
// reserves past its checked horizon.
func OpenSpans(cal *ical.Calendar, openAfter, from, to int64) []Span {
	from = max(from, openAfter)
	if from >= to {
		return nil
	}
	insts, err := calendar.ExpandComplete(cal, time.Unix(from, 0), time.Unix(to, 0), time.UTC, MaxOccurrences)
	if err != nil {
		return []Span{{from, to}} // cannot enumerate safely: the whole range is taken
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

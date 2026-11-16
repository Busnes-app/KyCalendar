// Package tasks reads and edits VTODO objects for the web Tasks screens. Edits change only the
// properties they name and keep every other property and parameter as stored, so a task
// written by Tasks.org, Apple Reminders or Thunderbird keeps what those clients put there.
package tasks

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

// PropEstimate is a task's estimated length (an RFC 5545 DURATION value) when nothing else
// carries it; see docs/superpowers/specs/2026-11-15-tasks-interop-research.md.
const PropEstimate = "X-KY-ESTIMATE"

// PropSource marks where a task came from ("kypost" for a flagged message).
const PropSource = "X-KY-SOURCE"

// Status values (RFC 5545 §3.8.1.11, VTODO).
const (
	NeedsAction = "NEEDS-ACTION"
	InProcess   = "IN-PROCESS"
	Completed   = "COMPLETED"
	Cancelled   = "CANCELLED"
)

var ErrInvalid = errors.New("tasks: invalid task")

// Moment is a task time: a day (AllDay) or an instant.
type Moment struct {
	T      time.Time
	AllDay bool
}

// Reminder is one VALARM: Before is the offset before DUE (or DTSTART without DUE) for a
// relative trigger; At is an absolute trigger.
type Reminder struct {
	Before time.Duration
	At     time.Time
}

// Task is the web's view of a VTODO's master component.
type Task struct {
	UID, Title, Notes, Status string
	CompletedAt               time.Time
	Percent, Priority         int
	Due, Start                *Moment
	// Duration is DTSTART+DURATION's length, Estimate the estimate property.
	Duration, Estimate time.Duration
	Tags               []string
	Parent             string
	RRule              string
	FromCompletion     bool
	Habit              bool
	Reminders          []Reminder
	URL, Source        string
	Created, Modified  time.Time
}

// Length is how long the task takes: its placed DURATION, else its estimate.
func (t *Task) Length() time.Duration {
	if t.Duration > 0 {
		return t.Duration
	}
	return t.Estimate
}

// Master is the object's VTODO without RECURRENCE-ID.
func Master(cal *ical.Calendar) (*ical.Component, error) {
	for _, c := range cal.Children {
		if c.Name == ical.CompToDo && c.Props.Get(ical.PropRecurrenceID) == nil {
			return c, nil
		}
	}
	return nil, ErrInvalid
}

// Read returns the task of cal; floating times read in viewer.
func Read(cal *ical.Calendar, viewer *time.Location) (*Task, error) {
	m, err := Master(cal)
	if err != nil {
		return nil, err
	}
	text := func(name string) string { v, _ := m.Props.Text(name); return v }
	t := &Task{UID: text(ical.PropUID), Title: text(ical.PropSummary), Notes: text(ical.PropDescription),
		Status: strings.ToUpper(text(ical.PropStatus)), URL: text(ical.PropURL), Source: text(PropSource)}
	if t.Status == "" {
		t.Status = NeedsAction
	}
	if p := m.Props.Get(ical.PropCompleted); p != nil {
		t.CompletedAt, _ = p.DateTime(time.UTC)
	}
	if t.Status == NeedsAction && !t.CompletedAt.IsZero() {
		t.Status = Completed // a client that writes COMPLETED without STATUS
	}
	t.Percent = intProp(m, ical.PropPercentComplete)
	t.Priority = intProp(m, ical.PropPriority)
	if p := m.Props.Get(ical.PropDue); p != nil {
		if mo, err := moment(p, viewer); err == nil {
			t.Due = &mo
		}
	}
	if p := m.Props.Get(ical.PropDateTimeStart); p != nil {
		if mo, err := moment(p, viewer); err == nil {
			t.Start = &mo
		}
	}
	if p := m.Props.Get(ical.PropDuration); p != nil {
		t.Duration, _ = p.Duration()
	}
	if p := m.Props.Get(PropEstimate); p != nil {
		t.Estimate, _ = p.Duration()
	}
	for _, p := range m.Props[ical.PropCategories] {
		l, _ := p.TextList()
		for _, v := range l {
			if v = strings.TrimSpace(v); v != "" {
				t.Tags = append(t.Tags, v)
			}
		}
	}
	for _, p := range m.Props[ical.PropRelatedTo] {
		if rt := p.Params.Get(ical.ParamRelationshipType); rt == "" || strings.EqualFold(rt, "PARENT") {
			t.Parent = p.Value
			break
		}
	}
	if p := m.Props.Get(ical.PropRecurrenceRule); p != nil {
		t.RRule = p.Value
	}
	t.FromCompletion = fromCompletion(m)
	t.Habit = isHabit(m)
	for _, a := range m.Children {
		if a.Name != ical.CompAlarm {
			continue
		}
		tr := a.Props.Get(ical.PropTrigger)
		if tr == nil {
			continue
		}
		if strings.EqualFold(tr.Params.Get(ical.ParamValue), "DATE-TIME") {
			if at, err := tr.DateTime(time.UTC); err == nil {
				t.Reminders = append(t.Reminders, Reminder{At: at})
			}
		} else if d, err := tr.Duration(); err == nil {
			t.Reminders = append(t.Reminders, Reminder{Before: -d})
		}
	}
	if p := m.Props.Get(ical.PropCreated); p != nil {
		t.Created, _ = p.DateTime(time.UTC)
	}
	if p := m.Props.Get(ical.PropLastModified); p != nil {
		t.Modified, _ = p.DateTime(time.UTC)
	}
	return t, nil
}

func intProp(m *ical.Component, name string) int {
	if p := m.Props.Get(name); p != nil {
		n, _ := strconv.Atoi(strings.TrimSpace(p.Value))
		return n
	}
	return 0
}

// moment reads a DATE or DATE-TIME property: TZID through calendar.ResolveZone (UTC when it
// does not resolve), floating in viewer.
func moment(p *ical.Prop, viewer *time.Location) (Moment, error) {
	if isDate(p) {
		t, err := time.ParseInLocation("20060102", p.Value, time.UTC)
		return Moment{T: t, AllDay: true}, err
	}
	t, err := p.DateTime(zoneOf(p, viewer))
	return Moment{T: t}, err
}

func isDate(p *ical.Prop) bool {
	return strings.EqualFold(p.Params.Get(ical.ParamValue), "DATE") || len(p.Value) == 8
}

func zoneOf(p *ical.Prop, viewer *time.Location) *time.Location {
	if strings.HasSuffix(p.Value, "Z") {
		return time.UTC
	}
	if id := p.Params.Get(ical.ParamTimezoneID); id != "" {
		if loc, ok := calendar.ResolveZone(id); ok {
			return loc
		}
		return time.UTC
	}
	return viewer
}

// Patch is one web edit, validated at the API boundary. A nil field keeps the stored value.
// A Moment with a zero T, an empty string, a zero number or an empty list removes it.
type Patch struct {
	Title, Notes, Status, Parent, RRule *string
	Priority                            *int
	Due, Start                          *Moment
	// Zone writes date-times with a TZID; nil or UTC writes UTC.
	Zone                  *time.Location
	Duration, Estimate    *time.Duration
	Tags                  *[]string
	FromCompletion, Habit *bool
	Reminders             *[]Reminder
}

// New builds a one-VTODO object.
func New(uid string, p Patch, url, source string, now time.Time) (*ical.Calendar, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, calendar.ProductID)
	td := ical.NewComponent(ical.CompToDo)
	td.Props.SetText(ical.PropUID, uid)
	created := ical.NewProp(ical.PropCreated)
	created.SetDateTime(now.UTC())
	td.Props.Set(created)
	if url != "" {
		setRaw(td, ical.PropURL, url)
	}
	if source != "" {
		td.Props.SetText(PropSource, source)
	}
	cal.Children = append(cal.Children, td)
	if p.Status == nil {
		s := NeedsAction
		p.Status = &s
	}
	if _, err := Apply(cal, p, now, time.UTC); err != nil {
		return nil, err
	}
	setRaw(td, ical.PropSequence, "0")
	return cal, nil
}

// Result says what a status change did.
type Result struct {
	// Completed: the task was completed now (a repeating one moved to its next occurrence).
	Completed bool
	// Reopened: a completed task became open again.
	Reopened bool
	// Advanced: a repeating task moved to its next occurrence instead of completing.
	Advanced bool
}

// Apply edits cal's task in place. viewer is the completing user's zone, used for floating
// and all-day times when a repeating task moves on from its completion day.
func Apply(cal *ical.Calendar, p Patch, now time.Time, viewer *time.Location) (Result, error) {
	var res Result
	m, err := Master(cal)
	if err != nil {
		return res, err
	}
	zone := p.Zone
	if zone == nil {
		zone = time.UTC
	}
	if p.Title != nil {
		setText(m, ical.PropSummary, *p.Title)
	}
	if p.Notes != nil {
		setText(m, ical.PropDescription, *p.Notes)
	}
	if p.Priority != nil {
		if *p.Priority == 0 {
			m.Props.Del(ical.PropPriority)
		} else {
			setRaw(m, ical.PropPriority, strconv.Itoa(*p.Priority))
		}
	}
	if p.Due != nil {
		setMoment(cal, m, ical.PropDue, *p.Due, zone)
	}
	if p.Start != nil {
		setMoment(cal, m, ical.PropDateTimeStart, *p.Start, zone)
	}
	if p.Duration != nil {
		m.Props.Del(ical.PropDuration)
		if *p.Duration > 0 {
			setRaw(m, ical.PropDuration, isoDuration(*p.Duration))
		}
	}
	if p.Estimate != nil {
		m.Props.Del(PropEstimate)
		if *p.Estimate > 0 {
			setRaw(m, PropEstimate, isoDuration(*p.Estimate))
		}
	}
	// RFC 5545 §3.6.2: DUE and DURATION never both; DURATION needs DTSTART. The length moves
	// to the estimate property.
	if d := m.Props.Get(ical.PropDuration); d != nil && (m.Props.Get(ical.PropDue) != nil || m.Props.Get(ical.PropDateTimeStart) == nil) {
		if m.Props.Get(PropEstimate) == nil || p.Duration != nil {
			e := *d
			e.Name = PropEstimate
			m.Props.Set(&e)
		}
		m.Props.Del(ical.PropDuration)
	}
	if p.Tags != nil {
		m.Props.Del(ical.PropCategories)
		if len(*p.Tags) > 0 {
			c := ical.NewProp(ical.PropCategories)
			c.SetTextList(*p.Tags)
			m.Props.Set(c)
		}
	}
	if p.Parent != nil {
		var keep []ical.Prop
		for _, r := range m.Props[ical.PropRelatedTo] {
			if rt := r.Params.Get(ical.ParamRelationshipType); rt != "" && !strings.EqualFold(rt, "PARENT") {
				keep = append(keep, r)
			}
		}
		if *p.Parent != "" {
			r := ical.NewProp(ical.PropRelatedTo)
			r.Params.Set(ical.ParamRelationshipType, "PARENT")
			r.Value = *p.Parent
			keep = append([]ical.Prop{*r}, keep...)
		}
		if len(keep) == 0 {
			m.Props.Del(ical.PropRelatedTo)
		} else {
			m.Props[ical.PropRelatedTo] = keep
		}
	}
	if p.RRule != nil {
		m.Props.Del(ical.PropRecurrenceRule)
		if *p.RRule != "" {
			setRaw(m, ical.PropRecurrenceRule, *p.RRule)
		}
	}
	if p.FromCompletion != nil {
		setFromCompletion(m, *p.FromCompletion)
	}
	if p.Habit != nil {
		setHabit(m, *p.Habit)
	}
	if m.Props.Get(ical.PropRecurrenceRule) == nil && (fromCompletion(m) || isHabit(m)) {
		m.Props.Del(PropRepeatFrom) // the markers mean nothing without a rule
		m.Props.Del(PropHabit)
	}
	if m.Props.Get(ical.PropRecurrenceRule) != nil && m.Props.Get(ical.PropDue) == nil && m.Props.Get(ical.PropDateTimeStart) == nil {
		return res, fmt.Errorf("%w: a repeating task needs a due or start date", ErrInvalid)
	}
	if p.Reminders != nil {
		setReminders(m, *p.Reminders)
	}
	if p.Status != nil {
		res, err = setStatus(cal, m, *p.Status, now, viewer)
		if err != nil {
			return res, err
		}
	}
	touch(m, now)
	return res, nil
}

func setStatus(cal *ical.Calendar, m *ical.Component, status string, now time.Time, viewer *time.Location) (Result, error) {
	var res Result
	before, _ := Read(cal, viewer)
	wasDone := before.Status == Completed
	switch status {
	case Completed:
		if wasDone {
			return res, nil
		}
		res.Completed = true
		if m.Props.Get(ical.PropRecurrenceRule) != nil {
			moved, err := advance(m, now, viewer)
			if err != nil {
				return res, err
			}
			if moved {
				res.Advanced = true
				setRaw(m, ical.PropStatus, NeedsAction)
				m.Props.Del(ical.PropCompleted)
				m.Props.Del(ical.PropPercentComplete)
				return res, nil
			}
		}
		setRaw(m, ical.PropStatus, Completed)
		c := ical.NewProp(ical.PropCompleted)
		c.SetDateTime(now.UTC())
		m.Props.Set(c)
		setRaw(m, ical.PropPercentComplete, "100")
	case NeedsAction, InProcess, Cancelled:
		res.Reopened = wasDone && status != Cancelled
		setRaw(m, ical.PropStatus, status)
		m.Props.Del(ical.PropCompleted)
		if wasDone || status == NeedsAction {
			if status == NeedsAction {
				m.Props.Del(ical.PropPercentComplete)
			} else if intProp(m, ical.PropPercentComplete) == 100 {
				m.Props.Del(ical.PropPercentComplete)
			}
		}
	default:
		return res, fmt.Errorf("%w: unknown status", ErrInvalid)
	}
	return res, nil
}

// advance moves a repeating task to its next occurrence: DUE (or DTSTART without DUE) to the
// rule's next date after the current one, or after the completion day with repeat from
// completion; the other time keeps its distance. A COUNT drops by one. false: the rule has no
// next occurrence.
func advance(m *ical.Component, now time.Time, viewer *time.Location) (bool, error) {
	rp := m.Props.Get(ical.PropRecurrenceRule)
	anchorName := ical.PropDue
	if m.Props.Get(ical.PropDue) == nil {
		anchorName = ical.PropDateTimeStart
	}
	ap := m.Props.Get(anchorName)
	anchor, err := moment(ap, viewer)
	if err != nil {
		return false, fmt.Errorf("%w: %s", ErrInvalid, anchorName)
	}
	loc := zoneOf(ap, viewer)
	if anchor.AllDay {
		loc = time.UTC
	}
	opt, err := rrule.StrToROption(rp.Value)
	if err != nil {
		return false, fmt.Errorf("%w: RRULE", ErrInvalid)
	}
	count := opt.Count
	if count == 1 {
		return false, nil
	}
	opt.Count = 0
	at := anchor.T.In(loc)
	from := at
	if fromCompletion(m) {
		day := now.In(viewer)
		if !anchor.AllDay {
			day = now.In(loc)
		}
		from = time.Date(day.Year(), day.Month(), day.Day(), at.Hour(), at.Minute(), at.Second(), 0, loc)
	}
	if !opt.Until.IsZero() && anchor.AllDay {
		opt.Until = time.Date(opt.Until.Year(), opt.Until.Month(), opt.Until.Day(), 23, 59, 59, 0, time.UTC)
	}
	opt.Dtstart = from
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return false, fmt.Errorf("%w: RRULE", ErrInvalid)
	}
	next := r.After(from, false)
	if next.IsZero() {
		return false, nil
	}
	for _, name := range []string{ical.PropDue, ical.PropDateTimeStart} {
		p := m.Props.Get(name)
		if p == nil {
			continue
		}
		cur, err := moment(p, viewer)
		if err != nil {
			continue
		}
		plocal := zoneOf(p, viewer)
		if cur.AllDay {
			plocal = time.UTC
		}
		moved := shift(cur.T.In(plocal), at, next)
		rewrite(p, moved, cur.AllDay)
	}
	if count > 1 {
		setRuleCount(rp, count-1)
	}
	return true, nil
}

// shift moves t as far as from moved to to: by whole days on the wall clock when from and to
// share a time of day (so 09:00 stays 09:00 across DST), else by the elapsed time.
func shift(t, from, to time.Time) time.Time {
	if from.Hour() == to.Hour() && from.Minute() == to.Minute() && from.Second() == to.Second() {
		fd := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
		td := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
		days := int(math.Round(td.Sub(fd).Hours() / 24))
		return t.AddDate(0, 0, days)
	}
	return t.Add(to.Sub(from))
}

// rewrite sets p's value to t in p's own form: a DATE, UTC, its TZID's wall time or floating.
func rewrite(p *ical.Prop, t time.Time, allDay bool) {
	switch {
	case allDay:
		p.Value = t.Format("20060102")
	case strings.HasSuffix(p.Value, "Z"):
		p.Value = t.UTC().Format("20060102T150405Z")
	default:
		p.Value = t.Format("20060102T150405")
	}
}

func setRuleCount(p *ical.Prop, n int) {
	parts := strings.Split(p.Value, ";")
	for i, part := range parts {
		if k, _, ok := strings.Cut(part, "="); ok && strings.EqualFold(k, "COUNT") {
			parts[i] = "COUNT=" + strconv.Itoa(n)
		}
	}
	p.Value = strings.Join(parts, ";")
}

func setMoment(cal *ical.Calendar, m *ical.Component, name string, mo Moment, zone *time.Location) {
	m.Props.Del(name)
	if mo.T.IsZero() {
		return
	}
	p := ical.NewProp(name)
	switch {
	case mo.AllDay:
		p.SetDate(mo.T)
	case zone == time.UTC:
		p.SetDateTime(mo.T.UTC())
	default:
		p.SetDateTime(mo.T.In(zone))
		ensureVTimezone(cal, zone, mo.T)
	}
	m.Props.Set(p)
}

func ensureVTimezone(cal *ical.Calendar, loc *time.Location, at time.Time) {
	for _, c := range cal.Children {
		if id, _ := c.Props.Text(ical.PropTimezoneID); c.Name == ical.CompTimezone && id == loc.String() {
			return
		}
	}
	from := time.Date(at.In(loc).Year(), 1, 1, 0, 0, 0, 0, loc)
	cal.Children = append([]*ical.Component{calendar.VTimezone(loc, from, from.AddDate(11, 0, 0))}, cal.Children...)
}

// setReminders replaces the task's alarms with display alarms; an unchanged list keeps the
// stored alarms as they are.
func setReminders(m *ical.Component, rs []Reminder) {
	var keep []*ical.Component
	var old []Reminder
	for _, c := range m.Children {
		if c.Name != ical.CompAlarm {
			keep = append(keep, c)
		}
	}
	for _, c := range m.Children {
		if c.Name == ical.CompAlarm {
			if tr := c.Props.Get(ical.PropTrigger); tr != nil {
				if strings.EqualFold(tr.Params.Get(ical.ParamValue), "DATE-TIME") {
					at, _ := tr.DateTime(time.UTC)
					old = append(old, Reminder{At: at})
				} else if d, err := tr.Duration(); err == nil {
					old = append(old, Reminder{Before: -d})
				}
			}
		}
	}
	if sameReminders(old, rs) {
		return
	}
	related := "END"
	if m.Props.Get(ical.PropDue) == nil {
		related = "START"
	}
	title, _ := m.Props.Text(ical.PropSummary)
	for _, r := range rs {
		a := ical.NewComponent(ical.CompAlarm)
		setRaw(a, ical.PropAction, "DISPLAY")
		a.Props.SetText(ical.PropDescription, cmpOr(title, "Reminder"))
		tr := ical.NewProp(ical.PropTrigger)
		if !r.At.IsZero() {
			tr.SetDateTime(r.At.UTC())
			tr.Params.Set(ical.ParamValue, "DATE-TIME")
		} else {
			tr.Value = isoDuration(-r.Before)
			if related == "END" {
				tr.Params.Set(ical.ParamRelated, related)
			}
		}
		a.Props.Set(tr)
		keep = append(keep, a)
	}
	m.Children = keep
}

func sameReminders(a, b []Reminder) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Before != b[i].Before || !a[i].At.Equal(b[i].At) {
			return false
		}
	}
	return true
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func setText(m *ical.Component, name, value string) {
	if value == "" {
		m.Props.Del(name)
		return
	}
	m.Props.SetText(name, value)
}

func setRaw(m *ical.Component, name, value string) {
	p := ical.NewProp(name)
	p.Value = value
	m.Props.Set(p)
}

// touch records a change for clients: DTSTAMP and LAST-MODIFIED now, SEQUENCE + 1.
func touch(m *ical.Component, now time.Time) {
	for _, name := range []string{ical.PropDateTimeStamp, ical.PropLastModified} {
		p := ical.NewProp(name)
		p.SetDateTime(now.UTC())
		m.Props.Set(p)
	}
	setRaw(m, ical.PropSequence, strconv.Itoa(min(intProp(m, ical.PropSequence)+1, math.MaxInt32)))
}

// isoDuration writes d as an RFC 5545 duration in whole seconds: PT1H30M, -PT15M, P1D.
func isoDuration(d time.Duration) string {
	sign := ""
	if d < 0 {
		sign, d = "-", -d
	}
	s := int64(d / time.Second)
	if s == 0 {
		return "PT0S"
	}
	var b strings.Builder
	b.WriteString(sign + "P")
	if days := s / 86400; days > 0 && s%86400 == 0 {
		fmt.Fprintf(&b, "%dD", days)
		return b.String()
	}
	b.WriteString("T")
	for _, u := range []struct {
		n int64
		c string
	}{{3600, "H"}, {60, "M"}, {1, "S"}} {
		if v := s / u.n; v > 0 {
			fmt.Fprintf(&b, "%d%s", v, u.c)
			s %= u.n
		}
	}
	return b.String()
}

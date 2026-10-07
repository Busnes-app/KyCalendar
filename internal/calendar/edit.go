package calendar

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// ProductID names KyCalendar in objects it creates.
const ProductID = "-//Busnes.app//KyCalendar//EN"

var (
	ErrNotRecurring     = errors.New("calendar: event does not repeat")
	ErrNoSuchOccurrence = errors.New("calendar: no such occurrence")
)

// Repeat is a web repeat preset. Freq "custom" is any rule outside the presets; edits with it
// keep the stored rule.
type Repeat struct {
	Freq     string
	Weekdays []time.Weekday
}

// EventInput is one web edit, validated at the API boundary.
type EventInput struct {
	Title, Location, Description string
	Start, End                   time.Time
	AllDay                       bool
	Zone                         *time.Location
	Repeat                       Repeat
}

// vtimezoneYears is how far ahead a generated VTIMEZONE lists offset changes.
const vtimezoneYears = 11

var weekdayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// Master is the object's VEVENT without RECURRENCE-ID.
func Master(cal *ical.Calendar) (*ical.Component, error) {
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) == nil {
			return c, nil
		}
	}
	return nil, ErrInvalidData
}

// RepeatOf classifies a component's recurrence as a preset or "custom".
func RepeatOf(comp *ical.Component) Repeat {
	rules := comp.Props[ical.PropRecurrenceRule]
	switch {
	case len(rules) == 0 && len(comp.Props[ical.PropRecurrenceDates]) == 0:
		return Repeat{}
	case len(rules) != 1 || len(comp.Props[ical.PropRecurrenceDates]) > 0:
		return Repeat{Freq: "custom"}
	}
	var r Repeat
	for _, part := range strings.Split(strings.ToUpper(rules[0].Value), ";") {
		key, val, _ := strings.Cut(part, "=")
		switch key {
		case "FREQ":
			r.Freq = strings.ToLower(val)
		case "INTERVAL":
			if val != "1" {
				return Repeat{Freq: "custom"}
			}
		case "BYDAY":
			for _, code := range strings.Split(val, ",") {
				i := slices.Index(weekdayCodes, code)
				if i < 0 {
					return Repeat{Freq: "custom"} // an ordinal such as -1FR
				}
				r.Weekdays = append(r.Weekdays, time.Weekday(i))
			}
		case "WKST":
		default:
			return Repeat{Freq: "custom"}
		}
	}
	switch {
	case r.Freq != "daily" && r.Freq != "weekly" && r.Freq != "monthly" && r.Freq != "yearly":
		return Repeat{Freq: "custom"}
	case len(r.Weekdays) > 0 && r.Freq != "weekly":
		return Repeat{Freq: "custom"}
	}
	return r
}

func (r Repeat) equal(o Repeat) bool {
	a, b := slices.Clone(r.Weekdays), slices.Clone(o.Weekdays)
	slices.Sort(a)
	slices.Sort(b)
	return r.Freq == o.Freq && slices.Equal(a, b)
}

func setRepeat(comp *ical.Component, r Repeat) {
	if r.Freq == "custom" {
		return
	}
	comp.Props.Del(ical.PropRecurrenceRule)
	if r.Freq == "" {
		return
	}
	rule := "FREQ=" + strings.ToUpper(r.Freq)
	if r.Freq == "weekly" && len(r.Weekdays) > 0 {
		codes := make([]string, len(r.Weekdays))
		for i, d := range r.Weekdays {
			codes[i] = weekdayCodes[d]
		}
		rule += ";BYDAY=" + strings.Join(codes, ",")
	}
	setRaw(comp, ical.PropRecurrenceRule, rule)
}

func setText(comp *ical.Component, name, value string) {
	if value == "" {
		comp.Props.Del(name)
		return
	}
	comp.Props.SetText(name, value)
}

func setTimes(cal *ical.Calendar, comp *ical.Component, in EventInput) {
	comp.Props.Del(ical.PropDuration)
	for name, t := range map[string]time.Time{ical.PropDateTimeStart: in.Start, ical.PropDateTimeEnd: in.End} {
		p := ical.NewProp(name)
		switch {
		case in.AllDay:
			p.SetDate(t)
		case in.Zone == time.UTC:
			p.SetDateTime(t.UTC())
		default:
			p.SetDateTime(t.In(in.Zone))
		}
		comp.Props.Set(p)
	}
	if !in.AllDay && in.Zone != time.UTC {
		ensureVTimezone(cal, in.Zone, in.Start)
	}
}

// ensureVTimezone adds a VTIMEZONE for loc unless one with that TZID exists.
func ensureVTimezone(cal *ical.Calendar, loc *time.Location, start time.Time) {
	for _, c := range cal.Children {
		if id, _ := c.Props.Text(ical.PropTimezoneID); c.Name == ical.CompTimezone && id == loc.String() {
			return
		}
	}
	from := time.Date(start.In(loc).Year(), 1, 1, 0, 0, 0, 0, loc)
	cal.Children = append([]*ical.Component{VTimezone(loc, from, from.AddDate(vtimezoneYears, 0, 0))}, cal.Children...)
}

// touch records a change for clients: DTSTAMP and LAST-MODIFIED now, SEQUENCE + 1.
func touch(comp *ical.Component, now time.Time) {
	for _, name := range []string{ical.PropDateTimeStamp, ical.PropLastModified} {
		p := ical.NewProp(name)
		p.SetDateTime(now.UTC())
		comp.Props.Set(p)
	}
	seq := 0
	if p := comp.Props.Get(ical.PropSequence); p != nil {
		seq, _ = p.Int()
	}
	setRaw(comp, ical.PropSequence, strconv.Itoa(min(seq+1, math.MaxInt32)))
}

func applyText(comp *ical.Component, in EventInput) {
	setText(comp, ical.PropSummary, in.Title)
	setText(comp, ical.PropLocation, in.Location)
	setText(comp, ical.PropDescription, in.Description)
}

// NewEvent builds a one-VEVENT object for a web create.
func NewEvent(uid string, in EventInput, now time.Time) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, ProductID)
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	created := ical.NewProp(ical.PropCreated)
	created.SetDateTime(now.UTC())
	ev.Props.Set(created)
	applyText(ev.Component, in)
	cal.Children = append(cal.Children, ev.Component)
	setTimes(cal, ev.Component, in)
	setRepeat(ev.Component, in.Repeat)
	touch(ev.Component, now)
	setRaw(ev.Component, ical.PropSequence, "0")
	return cal
}

// formProp writes instant t as a time property in the form of DTSTART ds: a DATE, UTC, the TZID
// as written (wall time in that zone; UTC wall digits for an unresolvable one, as Expand reads
// them), or floating wall time in floating.
func formProp(ds *ical.Prop, name string, t time.Time, floating *time.Location) *ical.Prop {
	p := ical.NewProp(name)
	tzid := ds.Params.Get(ical.ParamTimezoneID)
	switch {
	case isDate(ds):
		p.SetDate(t)
	case tzid != "":
		loc, ok := ResolveZone(tzid)
		if !ok {
			loc = time.UTC
		}
		p.Params.Set(ical.ParamTimezoneID, tzid)
		p.Value = t.In(loc).Format("20060102T150405")
	case strings.HasSuffix(ds.Value, "Z"):
		p.SetDateTime(t.UTC())
	default:
		p.Value = t.In(floating).Format("20060102T150405")
	}
	return p
}

// sameStart reports whether in.Start is the master's start, compared in the master's own form.
func sameStart(ds *ical.Prop, old span, in EventInput) bool {
	switch {
	case old.allDay || in.AllDay:
		return old.allDay && in.AllDay && in.Start.Format("20060102") == ds.Value
	case old.floating:
		return in.Start.In(in.Zone).Format("20060102T150405") == ds.Value
	}
	return old.start.Equal(in.Start)
}

// EditAll edits the master. A change of start or repeat preset removes overrides, EXDATE and
// RDATE: they name instants of the old series. Text-only edits keep them, and DTSTART as stored;
// an end change is written in DTSTART's form. Overrides keep their own text.
func EditAll(cal *ical.Calendar, in EventInput, now time.Time) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	old, err := spanOf(m, time.UTC)
	if err != nil {
		return err
	}
	ds := m.Props.Get(ical.PropDateTimeStart)
	moved := !sameStart(ds, old, in) || (in.Repeat.Freq != "custom" && !RepeatOf(m).equal(in.Repeat))
	applyText(m, in)
	setRepeat(m, in.Repeat)
	if moved {
		setTimes(cal, m, in)
		m.Props.Del(ical.PropExceptionDates)
		m.Props.Del(ical.PropRecurrenceDates)
		cal.Children = slices.DeleteFunc(cal.Children, func(c *ical.Component) bool {
			return c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) != nil
		})
	} else {
		end := in.End
		if old.floating {
			e := end.In(in.Zone)
			end = time.Date(e.Year(), e.Month(), e.Day(), e.Hour(), e.Minute(), e.Second(), 0, time.UTC)
		}
		if !end.Equal(old.endAt(old.start)) {
			m.Props.Del(ical.PropDuration)
			m.Props.Set(formProp(ds, ical.PropDateTimeEnd, in.End, in.Zone))
		}
	}
	touch(m, now)
	return nil
}

// occurrenceProp names an occurrence (RECURRENCE-ID or EXDATE) in the form of the master's
// DTSTART, so every client matches it: a DATE, wall time in the TZID as written, or UTC. A key
// in another form than the master's is ErrNoSuchOccurrence. It returns the occurrence's time
// (wall digits read as UTC unless the key is an instant).
func occurrenceProp(master *ical.Component, name, key string) (*ical.Prop, time.Time, error) {
	ds := master.Props.Get(ical.PropDateTimeStart)
	tzid := ds.Params.Get(ical.ParamTimezoneID)
	utc := strings.HasSuffix(ds.Value, "Z")
	var t time.Time
	var err error
	switch {
	case isDate(ds):
		if t, err = time.Parse("20060102", key); err == nil {
			return formProp(ds, name, t, time.UTC), t, nil
		}
	case strings.HasSuffix(key, "Z") && (utc || tzid != ""):
		if t, err = time.Parse("20060102T150405Z", key); err == nil {
			return formProp(ds, name, t, time.UTC), t, nil
		}
	case !strings.HasSuffix(key, "Z") && !utc && tzid == "" && !isDate(ds):
		if t, err = time.Parse("20060102T150405", key); err == nil {
			p := ical.NewProp(name)
			p.Value = key
			return p, t, nil
		}
	}
	return nil, time.Time{}, ErrNoSuchOccurrence
}

// overrideFor returns the override for key, or nil. Keys are in the master's form, as Expand
// keys them.
func overrideFor(cal *ical.Calendar, master *ical.Component, key string) (*ical.Component, error) {
	form, err := spanOf(master, time.UTC)
	if err != nil {
		return nil, err
	}
	for _, c := range cal.Children {
		rid := c.Props.Get(ical.PropRecurrenceID)
		if c.Name != ical.CompEvent || rid == nil {
			continue
		}
		if k, ok := overrideKey(rid, time.UTC, &form); ok && k == key {
			return c, nil
		}
	}
	return nil, nil
}

// checkOccurrence returns the existing override for key, or nil, and ErrNoSuchOccurrence unless
// the key names an override or an occurrence Expand yields near its time.
func checkOccurrence(cal *ical.Calendar, m *ical.Component, key string, at time.Time) (*ical.Component, error) {
	o, err := overrideFor(cal, m, key)
	if err != nil || o != nil {
		return o, err
	}
	form, err := spanOf(m, time.UTC)
	if err != nil {
		return nil, err
	}
	reach := form.endAt(at).Sub(at)
	got, err := Expand(cal, at.Add(-24*time.Hour-reach), at.Add(24*time.Hour), time.UTC, 1000)
	if err != nil {
		return nil, ErrNoSuchOccurrence
	}
	for _, in := range got {
		if in.RecurrenceID == key {
			return nil, nil
		}
	}
	return nil, ErrNoSuchOccurrence
}

func cloneProps(p ical.Props) ical.Props {
	out := ical.Props{}
	for name, props := range p {
		for _, x := range props {
			c := x
			c.Params = ical.Params{}
			for k, v := range x.Params {
				c.Params[k] = slices.Clone(v)
			}
			out[name] = append(out[name], c)
		}
	}
	return out
}

func cloneComp(c *ical.Component) *ical.Component {
	out := &ical.Component{Name: c.Name, Props: cloneProps(c.Props)}
	for _, ch := range c.Children {
		out.Children = append(out.Children, cloneComp(ch))
	}
	return out
}

// seriesOf returns the master and the occurrence's property, refusing non-recurring objects and
// keys that are not real occurrences; o is the existing override, if any.
func seriesOf(cal *ical.Calendar, name, key string) (m *ical.Component, p *ical.Prop, o *ical.Component, err error) {
	if m, err = Master(cal); err != nil {
		return
	}
	if m.Props.Get(ical.PropRecurrenceRule) == nil && m.Props.Get(ical.PropRecurrenceDates) == nil {
		return nil, nil, nil, ErrNotRecurring
	}
	var at time.Time
	if p, at, err = occurrenceProp(m, name, key); err != nil {
		return
	}
	if name == ical.PropExceptionDates && hasExdate(m, p) {
		return // already deleted: repeating it is a no-op
	}
	o, err = checkOccurrence(cal, m, key, at)
	return
}

// EditOne writes an override for one occurrence: a copy of the master without its recurrence,
// with the edit applied. An existing override for that occurrence is edited in place.
func EditOne(cal *ical.Calendar, recurrenceID string, in EventInput, now time.Time) error {
	m, rid, o, err := seriesOf(cal, ical.PropRecurrenceID, recurrenceID)
	if err != nil {
		return err
	}
	if o == nil {
		o = cloneComp(m)
		for _, name := range []string{ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates} {
			o.Props.Del(name)
		}
		o.Props.Set(rid)
		cal.Children = append(cal.Children, o)
	}
	applyText(o, in)
	setTimes(cal, o, in)
	touch(o, now)
	return nil
}

// DeleteOne hides one occurrence: an EXDATE on the master, and its override removed.
func DeleteOne(cal *ical.Calendar, recurrenceID string, now time.Time) error {
	m, ex, o, err := seriesOf(cal, ical.PropExceptionDates, recurrenceID)
	if err != nil {
		return err
	}
	if !hasExdate(m, ex) {
		m.Props.Add(ex)
	}
	if o != nil {
		cal.Children = slices.DeleteFunc(cal.Children, func(c *ical.Component) bool { return c == o })
	}
	touch(m, now)
	return nil
}

func hasExdate(m *ical.Component, ex *ical.Prop) bool {
	for _, p := range m.Props[ical.PropExceptionDates] {
		if p.Params.Get(ical.ParamTimezoneID) == ex.Params.Get(ical.ParamTimezoneID) && slices.Contains(strings.Split(p.Value, ","), ex.Value) {
			return true
		}
	}
	return false
}

// Package itip holds the pure iTIP (RFC 5546) rules KyCalendar's server-side scheduling (RFC
// 6638) runs on every write: which messages a change produces, how an inbound message is checked,
// and how a reply or an organizer's update lands in a stored object. No I/O.
package itip

import (
	"bytes"
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

// Address is a calendar user address: "mailto:" and a lower-cased email address.
type Address string

const (
	MethodRequest = "REQUEST"
	MethodReply   = "REPLY"
	MethodCancel  = "CANCEL"
)

// Bounds on one object, checked on every inbound message.
const (
	maxAttendeesPerComponent = 100
	maxAttendeesPerObject    = 1000
	maxAddressLen            = 254
)

var (
	ErrInvalid     = errors.New("itip: invalid scheduling message")
	ErrNotAttendee = errors.New("itip: sender is not an attendee")
	ErrUIDMismatch = errors.New("itip: message is for another object")
)

var partstats = map[string]bool{"NEEDS-ACTION": true, "ACCEPTED": true, "DECLINED": true, "TENTATIVE": true, "DELEGATED": true}

// ParseAddress accepts "mailto:<addr-spec>" (scheme in any case) and returns it normalised.
func ParseAddress(v string) (Address, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 7 || !strings.EqualFold(v[:7], "mailto:") {
		return "", false
	}
	email := v[7:]
	if email == "" || len(email) > maxAddressLen || strings.ContainsAny(email, "\r\n<>,; \t\"") {
		return "", false
	}
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || a.Name != "" {
		return "", false
	}
	return Address("mailto:" + strings.ToLower(email)), true
}

// Email is the address without its scheme.
func (a Address) Email() string { return strings.TrimPrefix(string(a), "mailto:") }

// Message is one iTIP message and who receives it.
type Message struct {
	Method     string
	Recipients []Address
	Calendar   *ical.Calendar
}

func events(cal *ical.Calendar) []*ical.Component {
	if cal == nil {
		return nil
	}
	var out []*ical.Component
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			out = append(out, c)
		}
	}
	return out
}

func propAddress(p *ical.Prop) (Address, bool) {
	if p == nil {
		return "", false
	}
	return ParseAddress(p.Value)
}

// Organizer is the object's ORGANIZER, read from its first VEVENT.
func Organizer(cal *ical.Calendar) (Address, bool) {
	for _, c := range events(cal) {
		return propAddress(c.Props.Get(ical.PropOrganizer))
	}
	return "", false
}

// scheduled reports whether the server handles this attendee: SCHEDULE-AGENT absent or SERVER.
func scheduled(p ical.Prop) bool {
	agent := p.Params.Get("SCHEDULE-AGENT")
	return agent == "" || strings.EqualFold(agent, "SERVER")
}

// attendees is every server-scheduled attendee across the object's VEVENTs.
func attendees(cal *ical.Calendar) map[Address]bool {
	out := map[Address]bool{}
	for _, c := range events(cal) {
		for _, p := range c.Props[ical.PropAttendee] {
			if a, ok := ParseAddress(p.Value); ok && scheduled(p) {
				out[a] = true
			}
		}
	}
	return out
}

// clone is a deep copy by encoding and decoding. go-ical's encoder is stricter than its decoder,
// so a client's object can decode and still fail here: that is ErrInvalid, never a panic.
func clone(cal *ical.Calendar) (out *ical.Calendar, err error) {
	defer func() {
		if recover() != nil {
			out, err = nil, ErrInvalid
		}
	}()
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, ErrInvalid
	}
	if out, err = ical.NewDecoder(&buf).Decode(); err != nil {
		return nil, ErrInvalid
	}
	return out, nil
}

// message is cal as sent: METHOD set, VALARMs and the server-only parameters removed.
func message(cal *ical.Calendar, method string) (*ical.Calendar, error) {
	out, err := clone(cal)
	if err != nil {
		return nil, err
	}
	out.Props.SetText(ical.PropMethod, method)
	for _, c := range events(out) {
		kept := c.Children[:0]
		for _, child := range c.Children {
			if child.Name != ical.CompAlarm {
				kept = append(kept, child)
			}
		}
		c.Children = kept
		for _, name := range []string{ical.PropOrganizer, ical.PropAttendee} {
			for i := range c.Props[name] {
				for _, param := range []string{"SCHEDULE-AGENT", "SCHEDULE-STATUS", "SCHEDULE-FORCE-SEND"} {
					c.Props[name][i].Params.Del(param)
				}
			}
		}
	}
	return out, nil
}

func sorted(set map[Address]bool, except Address) []Address {
	var out []Address
	for a := range set {
		if a != except {
			out = append(out, a)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Outgoing is what the server sends after self writes newCal over oldCal. oldCal is nil for a
// create and newCal nil for a delete. self is the writer's own address. ErrInvalid is an object
// that cannot be sent as it stands (go-ical will not encode it).
func Outgoing(self Address, oldCal, newCal *ical.Calendar, now time.Time) ([]Message, error) {
	cur := newCal
	if cur == nil {
		cur = oldCal
	}
	org, ok := Organizer(cur)
	if !ok {
		return nil, nil
	}
	if org == self {
		return organizerMessages(self, oldCal, newCal)
	}
	if reply := attendeeReply(self, org, oldCal, newCal, now); reply != nil {
		return []Message{*reply}, nil
	}
	return nil, nil
}

func organizerMessages(self Address, oldCal, newCal *ical.Calendar) ([]Message, error) {
	if newCal == nil {
		if to := sorted(attendees(oldCal), self); len(to) > 0 {
			c, err := cancelled(oldCal)
			if err != nil {
				return nil, err
			}
			return []Message{{Method: MethodCancel, Recipients: to, Calendar: c}}, nil
		}
		return nil, nil
	}
	now, before := attendees(newCal), attendees(oldCal)
	var out []Message
	removed := map[Address]bool{}
	for a := range before {
		if !now[a] {
			removed[a] = true
		}
	}
	if to := sorted(removed, self); len(to) > 0 {
		c, err := cancelled(newCal)
		if err != nil {
			return nil, err
		}
		out = append(out, Message{Method: MethodCancel, Recipients: to, Calendar: c})
	}
	if oldCal != nil && !Changed(oldCal, newCal) {
		return out, nil
	}
	if to := sorted(now, self); len(to) > 0 {
		req, err := message(newCal, MethodRequest)
		if err != nil {
			return nil, err
		}
		out = append(out, Message{Method: MethodRequest, Recipients: to, Calendar: req})
	}
	return out, nil
}

func cancelled(cal *ical.Calendar) (*ical.Calendar, error) {
	out, err := message(cal, MethodCancel)
	if err != nil {
		return nil, err
	}
	for _, c := range events(out) {
		c.Props.SetText(ical.PropStatus, "CANCELLED")
	}
	return out, nil
}

// partstatOf is self's PARTSTAT on each VEVENT, keyed by RECURRENCE-ID ("" for the master).
func partstatOf(cal *ical.Calendar, self Address) map[string]string {
	out := map[string]string{}
	for _, c := range events(cal) {
		for _, p := range c.Props[ical.PropAttendee] {
			if a, ok := ParseAddress(p.Value); ok && a == self {
				out[recurrenceKey(c)] = strings.ToUpper(cmpOr(p.Params.Get(ical.ParamParticipationStatus), "NEEDS-ACTION"))
			}
		}
	}
	return out
}

func cmpOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func recurrenceKey(c *ical.Component) string {
	if p := c.Props.Get(ical.PropRecurrenceID); p != nil {
		return p.Value
	}
	return ""
}

// attendeeReply is the REPLY an attendee's write produces: only when their own PARTSTAT changed,
// or DECLINED when they delete an invitation.
func attendeeReply(self, org Address, oldCal, newCal *ical.Calendar, now time.Time) *Message {
	if oldCal == nil {
		return nil // an attendee's first copy arrives by delivery, not by their write
	}
	if before, ok := Organizer(oldCal); !ok || before != org {
		return nil // an attendee who rewrites ORGANIZER must not aim a reply at another address
	}
	before := partstatOf(oldCal, self)
	var src *ical.Calendar
	changed := map[string]string{}
	if newCal == nil {
		for k := range before {
			changed[k] = "DECLINED"
		}
		src = oldCal
	} else {
		for k, v := range partstatOf(newCal, self) {
			if before[k] != v && partstats[v] {
				changed[k] = v
			}
		}
		src = newCal
	}
	if len(changed) == 0 {
		return nil
	}
	reply := ical.NewCalendar()
	reply.Props.SetText(ical.PropVersion, "2.0")
	reply.Props.SetText(ical.PropProductID, "-//Busnes//KyCalendar//EN")
	reply.Props.SetText(ical.PropMethod, MethodReply)
	for _, c := range events(src) {
		ps, ok := changed[recurrenceKey(c)]
		if !ok {
			continue
		}
		ev := ical.NewComponent(ical.CompEvent)
		for _, name := range []string{ical.PropUID, ical.PropRecurrenceID, ical.PropDateTimeStart, ical.PropSequence, ical.PropOrganizer} {
			if p := c.Props.Get(name); p != nil {
				ev.Props.Set(clonedProp(*p))
			}
		}
		ev.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
		att := ical.NewProp(ical.PropAttendee)
		att.Value = string(self)
		att.Params.Set(ical.ParamParticipationStatus, ps)
		ev.Props.Set(att)
		reply.Children = append(reply.Children, ev)
	}
	for _, c := range src.Children {
		if c.Name == ical.CompTimezone {
			reply.Children = append(reply.Children, c)
		}
	}
	return &Message{Method: MethodReply, Recipients: []Address{org}, Calendar: reply}
}

func clonedProp(p ical.Prop) *ical.Prop {
	out := ical.NewProp(p.Name)
	out.Value = p.Value
	for k, v := range p.Params {
		out.Params[k] = append([]string(nil), v...)
	}
	return out
}

// significant are the properties whose change RFC 5546 calls significant, plus LOCATION.
var significant = []string{ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDuration, ical.PropDue,
	ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates, ical.PropStatus, ical.PropLocation}

func propsEqual(a, b *ical.Component, name string) bool {
	pa, pb := a.Props[name], b.Props[name]
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if pa[i].Value != pb[i].Value || pa[i].Params.Get("TZID") != pb[i].Params.Get("TZID") {
			return false
		}
	}
	return true
}

func byRecurrence(cal *ical.Calendar) map[string]*ical.Component {
	out := map[string]*ical.Component{}
	for _, c := range events(cal) {
		out[recurrenceKey(c)] = c
	}
	return out
}

// Significant reports a change attendees must re-confirm: times, recurrence, status or place,
// or an occurrence added or removed.
func Significant(oldCal, newCal *ical.Calendar) bool {
	before, after := byRecurrence(oldCal), byRecurrence(newCal)
	if len(before) != len(after) {
		return true
	}
	for k, n := range after {
		o, ok := before[k]
		if !ok {
			return true
		}
		for _, name := range significant {
			if !propsEqual(o, n, name) {
				return true
			}
		}
	}
	return false
}

// Changed reports anything attendees should hear about: a significant change, or a change to
// the summary, description, organizer or attendee list. Answers alone (PARTSTAT) are not.
func Changed(oldCal, newCal *ical.Calendar) bool {
	if Significant(oldCal, newCal) {
		return true
	}
	before, after := byRecurrence(oldCal), byRecurrence(newCal)
	for k, n := range after {
		o := before[k]
		for _, name := range []string{ical.PropSummary, ical.PropDescription, ical.PropOrganizer, "CONFERENCE"} {
			if !propsEqual(o, n, name) {
				return true
			}
		}
		if !equalSet(addressSet(o), addressSet(n)) {
			return true
		}
	}
	return false
}

func addressSet(c *ical.Component) map[Address]bool {
	out := map[Address]bool{}
	for _, p := range c.Props[ical.PropAttendee] {
		if a, ok := ParseAddress(p.Value); ok {
			out[a] = true
		}
	}
	return out
}

func equalSet(a, b map[Address]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sequence(c *ical.Component) int {
	if p := c.Props.Get(ical.PropSequence); p != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(p.Value)); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// BumpSequence raises SEQUENCE on every VEVENT of newCal past oldCal's highest when the change
// is significant and the client did not raise it. It reports whether it changed newCal.
func BumpSequence(oldCal, newCal *ical.Calendar) bool {
	if oldCal == nil || !Significant(oldCal, newCal) {
		return false
	}
	top := 0
	for _, c := range events(oldCal) {
		top = max(top, sequence(c))
	}
	bumped := false
	for _, c := range events(newCal) {
		if sequence(c) <= top {
			p := ical.NewProp(ical.PropSequence) // SetText would mark the integer VALUE=TEXT
			p.Value = strconv.Itoa(min(top+1, 1<<31-1))
			c.Props.Set(p)
			bumped = true
		}
	}
	return bumped
}

// Decode reads a scheduling message from outside. It refuses bodies over calendar.MaxObjectSize
// and parameter values go-ical decodes in quadratic time, and turns go-ical's panics on some
// malformed parameters into ErrInvalid: inbound messages are handled outside HTTP recovery.
func Decode(body []byte) (cal *ical.Calendar, err error) {
	if len(body) > calendar.MaxObjectSize || calendar.LongParams(body) {
		return nil, ErrInvalid
	}
	defer func() {
		if recover() != nil {
			cal, err = nil, ErrInvalid
		}
	}()
	cal, err = ical.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil || cal.Name != ical.CompCalendar {
		return nil, ErrInvalid
	}
	return cal, nil
}

// Validate checks an inbound scheduling message at the boundary and returns its METHOD and UID.
func Validate(cal *ical.Calendar) (method, uid string, err error) {
	method = strings.ToUpper(strings.TrimSpace(propText(cal.Props.Get(ical.PropMethod))))
	if method != MethodRequest && method != MethodReply && method != MethodCancel {
		return "", "", ErrInvalid
	}
	total := 0
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompTimezone:
			continue
		case ical.CompEvent:
		default:
			return "", "", ErrInvalid
		}
		u := propText(c.Props.Get(ical.PropUID))
		if u == "" || (uid != "" && u != uid) {
			return "", "", ErrInvalid
		}
		uid = u
		if _, ok := propAddress(c.Props.Get(ical.PropOrganizer)); !ok {
			return "", "", ErrInvalid
		}
		atts := c.Props[ical.PropAttendee]
		if len(atts) > maxAttendeesPerComponent {
			return "", "", ErrInvalid
		}
		total += len(atts)
		for _, p := range atts {
			if _, ok := ParseAddress(p.Value); !ok {
				return "", "", ErrInvalid
			}
		}
	}
	if uid == "" || total > maxAttendeesPerObject {
		return "", "", ErrInvalid
	}
	return method, uid, nil
}

func propText(p *ical.Prop) string {
	if p == nil {
		return ""
	}
	return p.Value
}

// ApplyReply records sender's answer from reply on stored, an object its user organizes. Only
// that attendee's PARTSTAT changes; components the stored object lacks and answers older than
// the stored SEQUENCE are ignored. It reports whether stored changed.
func ApplyReply(stored, reply *ical.Calendar, sender Address) (bool, error) {
	method, uid, err := Validate(reply)
	if err != nil || method != MethodReply {
		return false, ErrInvalid
	}
	if master, _ := firstUID(stored); master != uid {
		return false, ErrUIDMismatch
	}
	targets := byRecurrence(stored)
	changed, matched := false, false
	for _, c := range events(reply) {
		ps := ""
		for _, p := range c.Props[ical.PropAttendee] {
			if a, ok := ParseAddress(p.Value); ok && a == sender {
				ps = strings.ToUpper(p.Params.Get(ical.ParamParticipationStatus))
			}
		}
		if !partstats[ps] {
			continue
		}
		t, ok := targets[recurrenceKey(c)]
		if !ok || sequence(c) < sequence(t) {
			continue
		}
		atts := t.Props[ical.PropAttendee]
		for i := range atts {
			if a, ok := ParseAddress(atts[i].Value); ok && a == sender {
				matched = true
				if !strings.EqualFold(atts[i].Params.Get(ical.ParamParticipationStatus), ps) {
					atts[i].Params.Set(ical.ParamParticipationStatus, ps)
					changed = true
				}
			}
		}
	}
	if !matched {
		return false, ErrNotAttendee
	}
	return changed, nil
}

func firstUID(cal *ical.Calendar) (string, bool) {
	for _, c := range events(cal) {
		return propText(c.Props.Get(ical.PropUID)), true
	}
	return "", false
}

// AttendeeCopy is what lands in self's calendar from an organizer's REQUEST: the organizer's
// object, with self's own VALARMs kept from existing (nil for a first delivery) and self's
// PARTSTAT kept unless the change is significant, which asks them again.
func AttendeeCopy(request, existing *ical.Calendar, self Address) (*ical.Calendar, error) {
	out, err := clone(request)
	if err != nil {
		return nil, err
	}
	out.Props.Del(ical.PropMethod)
	keep := map[string]string{}
	alarms := map[string][]*ical.Component{}
	if existing != nil {
		if !Significant(existing, request) {
			keep = partstatOf(existing, self)
		}
		for _, c := range events(existing) {
			for _, child := range c.Children {
				if child.Name == ical.CompAlarm {
					alarms[recurrenceKey(c)] = append(alarms[recurrenceKey(c)], child)
				}
			}
		}
	}
	for _, c := range events(out) {
		key := recurrenceKey(c)
		c.Children = append(c.Children, alarms[key]...)
		atts := c.Props[ical.PropAttendee]
		for i := range atts {
			if a, ok := ParseAddress(atts[i].Value); ok && a == self {
				atts[i].Params.Set(ical.ParamParticipationStatus, cmpOr(keep[key], "NEEDS-ACTION"))
			}
		}
	}
	return out, nil
}

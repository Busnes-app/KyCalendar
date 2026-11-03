package davbackend

import (
	"context"
	"errors"
	"strings"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// kypostUser is who KyPost knows the sender as: their KyIdentity subject and issuer URL. Local
// accounts have none, so their external invitations cannot be mailed.
func kypostUser(u *store.User) (kypost.User, bool) {
	_, issuer, ok := strings.Cut(u.SSOIssuer, " ") // "<kind> <issuer>"
	if u.SSOSubject == "" || !ok || issuer == "" {
		return kypost.User{}, false
	}
	return kypost.User{Sub: u.SSOSubject, Issuer: issuer}, true
}

// sendExternal hands one message for a recipient outside the instance to KyPost, which mails it
// from the sender's mailbox.
func (s *Scheduler) sendExternal(ctx context.Context, m *store.OutboxMessage, sender *store.User, from, to itip.Address) error {
	if s.KyPost == nil {
		return errNoTransport
	}
	who, ok := kypostUser(sender)
	if !ok {
		return errPermanent{"no_kypost_account"}
	}
	err := s.KyPost.Send(ctx, m.DedupeKey, kypost.SendRequest{User: who, From: from.Email(), Recipients: []string{to.Email()},
		Method: m.Method, ICS: string(m.Body)})
	var refused kypost.Refused
	if errors.Is(err, kypost.ErrNotConnected) {
		return errNoTransport
	}
	if errors.As(err, &refused) {
		return errPermanent{"kypost_" + refused.Code}
	}
	return err // nil, or a retry
}

// Inbound outcomes.
const (
	InboundApplied = "applied" // a stored object changed
	InboundPending = "pending" // a new invitation: it waits in the mailbox for the user's answer
	InboundIgnored = "ignored" // nothing here to change
)

// Inbound applies an iTIP message that arrived in u's mailbox from from, whose DKIM signature
// KyPost found aligned. It is hostile input: a REPLY changes only that attendee's answer on an
// event u organizes; a REQUEST or CANCEL changes only a copy whose organizer is the sender.
// A new invitation is never written here; the user's own answer (Respond) writes it.
func (s *Scheduler) Inbound(ctx context.Context, u *store.User, from itip.Address, msg *ical.Calendar) (string, error) {
	self, ok := address(u)
	if !ok {
		return "", errPermanent{"no_address"}
	}
	method, uid, err := itip.Validate(msg)
	if err != nil {
		return "", itip.ErrInvalid
	}
	org, _ := itip.Organizer(msg)
	if method == itip.MethodReply {
		if org != self {
			return InboundIgnored, nil
		}
		c, o, stored, err := s.find(ctx, u, uid, self)
		if errors.Is(err, store.ErrNotFound) {
			return InboundIgnored, nil
		}
		if err != nil {
			return "", err
		}
		if addOverrides(stored, msg, from) != nil {
			return InboundIgnored, nil
		}
		changed, err := itip.ApplyReply(stored, msg, from)
		if errors.Is(err, itip.ErrNotAttendee) || errors.Is(err, itip.ErrUIDMismatch) {
			return InboundIgnored, nil
		}
		if err != nil || !changed {
			return InboundIgnored, err
		}
		return InboundApplied, s.put(ctx, c, o.Name, stored, o.ETag, false)
	}
	if org != from || org == self {
		return InboundIgnored, nil // only an organizer speaks for their meeting
	}
	c, o, existing, err := s.find(ctx, u, uid, org)
	switch {
	case errors.Is(err, store.ErrNotFound) && method == itip.MethodRequest:
		return InboundPending, nil
	case errors.Is(err, store.ErrNotFound):
		return InboundIgnored, nil
	case err != nil:
		var perm errPermanent
		if errors.As(err, &perm) { // the UID is another organizer's event here
			return InboundIgnored, nil
		}
		return "", err
	}
	if err := s.deliverInto(ctx, c, o, existing, self, method, msg); err != nil {
		var perm errPermanent
		if errors.As(err, &perm) { // nothing here it applies to, or an object that cannot be stored
			return InboundIgnored, nil
		}
		return "", err
	}
	return InboundApplied, nil
}

// deliverInto writes an organizer's REQUEST or CANCEL over an attendee's existing copy. A message
// about occurrences that names none of the copy's series is errPermanent{"no_occurrence"}.
func (s *Scheduler) deliverInto(ctx context.Context, c *store.Calendar, o *store.CalendarObject, existing *ical.Calendar, self itip.Address, method string, msg *ical.Calendar) error {
	if occurrencesOnly(msg) {
		if _, err := calendar.Master(existing); err == nil {
			next, err := deliverOccurrences(existing, self, method, msg)
			if err != nil {
				return err
			}
			return s.put(ctx, c, o.Name, next, o.ETag, false)
		}
	}
	if method == itip.MethodCancel {
		for _, ev := range existing.Children {
			if ev.Name == ical.CompEvent {
				ev.Props.SetText(ical.PropStatus, "CANCELLED")
			}
		}
		return s.put(ctx, c, o.Name, existing, o.ETag, false)
	}
	next, err := itip.AttendeeCopy(msg, existing, self)
	if err != nil {
		return errPermanent{"invalid_object"}
	}
	return s.put(ctx, c, o.Name, next, o.ETag, false)
}

// occurrencesOnly reports a message whose every VEVENT has a RECURRENCE-ID: it is about some
// occurrences of a series, not the series.
func occurrencesOnly(msg *ical.Calendar) bool {
	n := 0
	for _, c := range msg.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if c.Props.Get(ical.PropRecurrenceID) == nil {
			return false
		}
		n++
	}
	return n > 0
}

// overridesOf indexes cal's overrides by the occurrence each names (calendar.OccurrenceKey), so a
// RECURRENCE-ID written in another form (UTC against TZID) still finds its override.
func overridesOf(cal *ical.Calendar) map[string]*ical.Component {
	out := map[string]*ical.Component{}
	for _, c := range cal.Children {
		if rid := c.Props.Get(ical.PropRecurrenceID); c.Name == ical.CompEvent && rid != nil {
			if key, ok := calendar.OccurrenceKey(cal, rid); ok {
				out[key] = c
			}
		}
	}
	return out
}

// sameOccurrence writes c's RECURRENCE-ID as o's, so the two match by value.
func sameOccurrence(c, o *ical.Component) {
	rid := *o.Props.Get(ical.PropRecurrenceID)
	rid.Params = ical.Params{}
	for k, v := range o.Props.Get(ical.PropRecurrenceID).Params {
		rid.Params[k] = append([]string(nil), v...)
	}
	c.Props.Set(&rid)
}

// deliverOccurrences applies an organizer's message about single occurrences to the attendee's copy
// of the series: a REQUEST replaces or adds those overrides, a CANCEL marks those occurrences
// cancelled. The master and every other override stay as they are.
func deliverOccurrences(existing *ical.Calendar, self itip.Address, method string, msg *ical.Calendar) (*ical.Calendar, error) {
	have := overridesOf(existing)
	adding := 0
	for _, c := range msg.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if key, ok := calendar.OccurrenceKey(existing, c.Props.Get(ical.PropRecurrenceID)); ok && have[key] == nil {
			adding++
		}
	}
	if adding > maxNewOverrides {
		return nil, errTooManyOccurrences
	}
	// occurrence is the copy's override for the occurrence c names, added when the copy holds it only
	// through its master; nil when c names no occurrence of the series.
	occurrence := func(c *ical.Component) *ical.Component {
		key, ok := calendar.OccurrenceKey(existing, c.Props.Get(ical.PropRecurrenceID))
		if !ok {
			return nil
		}
		if o := have[key]; o != nil {
			return o
		}
		o, err := calendar.AddOverride(existing, key)
		if err != nil {
			return nil
		}
		have[key] = o
		return o
	}
	if method == itip.MethodCancel {
		cancelled := false
		for _, c := range msg.Children {
			if c.Name != ical.CompEvent {
				continue // a VTIMEZONE
			}
			if o := occurrence(c); o != nil {
				o.Props.SetText(ical.PropStatus, "CANCELLED")
				cancelled = true
			}
		}
		if !cancelled {
			return nil, errPermanent{"no_occurrence"}
		}
		return existing, nil
	}
	// Each replaced override, as the copy has it, is what answers and alarms carry over from.
	before := ical.NewCalendar()
	replaced := map[*ical.Component]bool{}
	for _, c := range msg.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if o := occurrence(c); o != nil {
			sameOccurrence(c, o)
			before.Children = append(before.Children, o)
			replaced[o] = true
		}
	}
	if len(before.Children) == 0 {
		return nil, errPermanent{"no_occurrence"}
	}
	next, err := itip.AttendeeCopy(msg, before, self)
	if err != nil {
		return nil, errPermanent{"invalid_object"}
	}
	kept := existing.Children[:0:0]
	zones := map[string]bool{}
	for _, c := range existing.Children {
		if replaced[c] {
			continue
		}
		if id, _ := c.Props.Text(ical.PropTimezoneID); c.Name == ical.CompTimezone {
			zones[id] = true
		}
		kept = append(kept, c)
	}
	for _, c := range next.Children {
		if c.Name == ical.CompTimezone {
			if id, _ := c.Props.Text(ical.PropTimezoneID); zones[id] {
				continue
			}
		} else if r := c.Props.Get(ical.PropRecurrenceID); c.Name == ical.CompEvent && (r == nil || !isOverride(before, r.Value)) {
			continue // an occurrence the series does not have
		}
		kept = append(kept, c)
	}
	existing.Children = kept
	return existing, nil
}

func isOverride(cal *ical.Calendar, rid string) bool {
	for _, c := range cal.Children {
		if p := c.Props.Get(ical.PropRecurrenceID); p != nil && p.Value == rid {
			return true
		}
	}
	return false
}

// Respond records u's answer from a KyPost RSVP card to invite, the invitation as mailed. The
// answer is bound to invite's organizer: it changes only u's copy of that organizer's event, so
// an invitation reusing another meeting's UID cannot answer for that meeting. When KyCalendar has
// not seen the invitation it is written into u's default calendar first: the user's own answer is
// what lets a sender put an event in their calendar. The answer then goes through the scheduled
// write path, so the organizer gets a REPLY.
func (s *Scheduler) Respond(ctx context.Context, u *store.User, uid, recurrenceID, partstat string, invite *ical.Calendar) error {
	self, ok := address(u)
	if !ok {
		return errPermanent{"no_address"}
	}
	if invite == nil {
		return itip.ErrInvalid
	}
	method, iuid, err := itip.Validate(invite)
	org, _ := itip.Organizer(invite)
	if err != nil || method != itip.MethodRequest || iuid != uid || org == self || !invited(invite, self) {
		return itip.ErrInvalid
	}
	c, o, cal, err := s.find(ctx, u, uid, org)
	if errors.Is(err, store.ErrNotFound) {
		if err := s.deliverToAttendee(ctx, u, self, org, uid, method, invite); err != nil {
			return err
		}
		c, o, cal, err = s.find(ctx, u, uid, org)
	}
	var perm errPermanent
	if errors.As(err, &perm) { // the UID belongs to another organizer's event here
		return itip.ErrNotInvited
	}
	if err != nil {
		return err
	}
	if recurrenceID != "" {
		o, err := calendar.AddOverride(cal, recurrenceID)
		if err != nil {
			return err
		}
		recurrenceID = o.Props.Get(ical.PropRecurrenceID).Value
	}
	if err := itip.SetPartstat(cal, recurrenceID, self, partstat); err != nil {
		return err
	}
	raw, err := encodeCal(cal)
	if err != nil {
		return errPermanent{"invalid_object"}
	}
	_, err = Write(ctx, s.Store, c, o.Name, cal, raw, o.ETag, false, s.Limits, s.Plan(u, cal))
	return err
}

func invited(cal *ical.Calendar, self itip.Address) bool {
	for _, ev := range cal.Children {
		if ev.Name != ical.CompEvent {
			continue
		}
		for _, a := range itip.AttendeesOf(ev) {
			if a.Address == self {
				return true
			}
		}
	}
	return false
}

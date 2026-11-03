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
		addOverrides(stored, msg)
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
		return "", err
	}
	return InboundApplied, nil
}

// deliverInto writes an organizer's REQUEST or CANCEL over an attendee's existing copy.
func (s *Scheduler) deliverInto(ctx context.Context, c *store.Calendar, o *store.CalendarObject, existing *ical.Calendar, self itip.Address, method string, msg *ical.Calendar) error {
	if rids := occurrencesOnly(msg); rids != nil {
		if _, err := calendar.Master(existing); err == nil {
			next, err := deliverOccurrences(existing, self, method, msg, rids)
			if err != nil {
				return errPermanent{"invalid_object"}
			}
			return s.put(ctx, c, o.Name, next, o.ETag, false)
		}
	}
	if m, err := calendar.Master(msg); err == nil {
		if e, err := calendar.Master(existing); err == nil && sequenceOf(m) < sequenceOf(e) {
			return nil // an older version of the meeting arriving after a newer one (RFC 5546 2.1.5)
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

func sequenceOf(c *ical.Component) int {
	n := 0
	if p := c.Props.Get(ical.PropSequence); p != nil {
		n, _ = p.Int()
	}
	return n
}

// occurrencesOnly is the RECURRENCE-ID of every VEVENT in msg when each has one: a message about
// some occurrences of a series, not the series. nil otherwise.
func occurrencesOnly(msg *ical.Calendar) map[string]bool {
	rids := map[string]bool{}
	for _, c := range msg.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		p := c.Props.Get(ical.PropRecurrenceID)
		if p == nil {
			return nil
		}
		rids[p.Value] = true
	}
	if len(rids) == 0 {
		return nil
	}
	return rids
}

// deliverOccurrences applies an organizer's message about single occurrences to the attendee's copy
// of the series: a REQUEST replaces or adds those overrides, a CANCEL marks those occurrences
// cancelled. The master and every other override stay as they are.
func deliverOccurrences(existing *ical.Calendar, self itip.Address, method string, msg *ical.Calendar, rids map[string]bool) (*ical.Calendar, error) {
	if method == itip.MethodCancel {
		for rid := range rids {
			if o, err := calendar.AddOverride(existing, rid); err == nil { // else not an occurrence here
				o.Props.SetText(ical.PropStatus, "CANCELLED")
			}
		}
		return existing, nil
	}
	// Answers and alarms carry over from the overrides being replaced, judged against them alone.
	before := ical.NewCalendar()
	kept := existing.Children[:0:0]
	for _, ev := range existing.Children {
		if p := ev.Props.Get(ical.PropRecurrenceID); ev.Name == ical.CompEvent && p != nil && rids[p.Value] {
			before.Children = append(before.Children, ev)
			continue
		}
		kept = append(kept, ev)
	}
	next, err := itip.AttendeeCopy(msg, before, self)
	if err != nil {
		return nil, err
	}
	zones := map[string]bool{}
	for _, c := range kept {
		if id, _ := c.Props.Text(ical.PropTimezoneID); c.Name == ical.CompTimezone {
			zones[id] = true
		}
	}
	for _, c := range next.Children {
		if id, _ := c.Props.Text(ical.PropTimezoneID); c.Name == ical.CompEvent || c.Name == ical.CompTimezone && !zones[id] {
			kept = append(kept, c)
		}
	}
	existing.Children = kept
	return existing, nil
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
		if err := calendar.EditText(cal, recurrenceID, calendar.TextEdit{}, s.now()); err != nil {
			return err
		}
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

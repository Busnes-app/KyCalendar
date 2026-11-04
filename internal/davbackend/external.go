package davbackend

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

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
	self, ok, err := Address(ctx, s.Store, u)
	if err != nil {
		return "", err
	}
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
		next, changed, err := applyReply(stored, msg, from)
		if errors.Is(err, itip.ErrNotAttendee) || errors.Is(err, itip.ErrUIDMismatch) || errors.Is(err, errTooManyOccurrences) {
			return InboundIgnored, nil
		}
		if err != nil || !changed {
			return InboundIgnored, err
		}
		return InboundApplied, s.put(ctx, c, o.Name, next, o.ETag, false)
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
	if err := s.deliverInto(ctx, c, o, existing, self, method, msg, "", ""); err != nil {
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
func (s *Scheduler) deliverInto(ctx context.Context, c *store.Calendar, o *store.CalendarObject, existing *ical.Calendar, self itip.Address, method string, msg *ical.Calendar, deliveryID, recipientID string) error {
	if stale(existing, msg) {
		return errPermanent{"stale"} // a retry or replay older than what the copy already took
	}
	if occurrencesOnly(msg) {
		if _, err := calendar.Master(existing); err == nil || occurrencesOnly(existing) {
			next, err := deliverOccurrences(existing, self, method, msg, c.OwnerKind == "group", o.InvitationRecipient != "" && o.InvitationSender != "")
			if errors.Is(err, errDeleteCopy) {
				return s.Store.Calendars().DeleteObject(ctx, c.ID, o.Name, o.ETag)
			}
			if err != nil {
				return err
			}
			return s.putDelivery(ctx, c, o.Name, next, o.ETag, false, deliveryID, recipientID)
		}
	}
	if method == itip.MethodCancel {
		keep := answered(existing, self)
		if o.InvitationRecipient != "" {
			// Tracked copies retain only an authenticated response or writer-paid content.
			// Client-controlled PARTSTAT cannot strand the sender's quota.
			keep = o.InvitationSender == ""
		}
		if !keep {
			return s.Store.Calendars().DeleteObject(ctx, c.ID, o.Name, o.ETag)
		}
		seq := 0
		if m, err := calendar.Master(msg); err == nil {
			seq = sequenceOf(m)
		}
		for _, ev := range existing.Children {
			if ev.Name == ical.CompEvent {
				ev.Props.SetText(ical.PropStatus, "CANCELLED")
				setOrganizerSequence(ev, seq)
			}
		}
		return s.putDelivery(ctx, c, o.Name, existing, o.ETag, false, deliveryID, recipientID)
	}
	copyRequest := itip.AttendeeCopy
	if c.OwnerKind == "group" {
		copyRequest = itip.SharedAttendeeCopy
	}
	next, err := copyRequest(msg, existing, self)
	if err != nil {
		return errPermanent{"invalid_object"}
	}
	stamp(next)
	return s.putDelivery(ctx, c, o.Name, next, o.ETag, false, deliveryID, recipientID)
}

// organizerSequence is kept on each VEVENT of a delivered copy: the organizer's SEQUENCE it last
// took. The copy's own SEQUENCE is no guide, since the attendee's edits raise it.
const organizerSequence = "X-KYCALENDAR-ORGANIZER-SEQUENCE"

func setOrganizerSequence(c *ical.Component, seq int) {
	c.Props.Set(&ical.Prop{Name: organizerSequence, Params: ical.Params{}, Value: strconv.Itoa(seq)})
}

// stamp records each VEVENT's SEQUENCE as the organizer's: cal's events are the organizer's own.
func stamp(cal *ical.Calendar) {
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			setOrganizerSequence(c, sequenceOf(c))
		}
	}
}

// stale reports a message with a VEVENT older (lower SEQUENCE) than the organizer's SEQUENCE the
// copy last took for that event: the series for one without RECURRENCE-ID, else that occurrence.
func stale(existing, msg *ical.Calendar) bool {
	key := func(c *ical.Component) (string, bool) {
		rid := c.Props.Get(ical.PropRecurrenceID)
		if rid == nil {
			return "", true
		}
		return calendar.OccurrenceKey(existing, rid)
	}
	took := map[string]int{}
	for _, c := range existing.Children {
		if p := c.Props.Get(organizerSequence); c.Name == ical.CompEvent && p != nil {
			if n, err := p.Int(); err == nil {
				if k, ok := key(c); ok {
					took[k] = n
				}
			}
		}
	}
	for _, c := range msg.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if k, ok := key(c); ok {
			n, had := took[k]
			if !had && k != "" {
				n, had = took[""] // an occurrence the copy holds only through its series
			}
			if had && sequenceOf(c) < n {
				return true
			}
		}
	}
	return false
}

// answered reports whether self gave any answer (a PARTSTAT other than NEEDS-ACTION) on any
// VEVENT of their copy.
func answered(cal *ical.Calendar, self itip.Address) bool {
	for _, ev := range cal.Children {
		if ev.Name != ical.CompEvent {
			continue
		}
		for _, a := range itip.AttendeesOf(ev) {
			if a.Address == self && a.Status != "NEEDS-ACTION" {
				return true
			}
		}
	}
	return false
}

// errDeleteCopy: the message removed the last occurrence of the copy, so the copy goes.
var errDeleteCopy = errors.New("delete the copy")

// answeredOccurrence reports whether self answered one occurrence of their copy: on its override
// o, else on the series. An occurrence a copy with no master does not hold counts as answered,
// so nothing is removed for it.
func answeredOccurrence(cal *ical.Calendar, o *ical.Component, self itip.Address) bool {
	if o == nil {
		m, err := calendar.Master(cal)
		if err != nil {
			return true
		}
		o = m
	}
	for _, a := range itip.AttendeesOf(o) {
		if a.Address == self && a.Status != "NEEDS-ACTION" {
			return true
		}
	}
	return false
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
// cancelled. The master and every other override stay as they are. A copy with no master (an
// invitation to some occurrences only) takes any occurrence the REQUEST names. Tracked sender-funded
// copies have no billable answer; paid and legacy copies keep their per-occurrence answer rules.
func deliverOccurrences(existing *ical.Calendar, self itip.Address, method string, msg *ical.Calendar, shared, senderFunded bool) (*ical.Calendar, error) {
	_, err := calendar.Master(existing)
	masterless := err != nil
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
		if o := have[key]; o != nil || masterless {
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
			// An occurrence the attendee never answered goes, as a whole unanswered copy does.
			if key, ok := calendar.OccurrenceKey(existing, c.Props.Get(ical.PropRecurrenceID)); ok && (senderFunded || !answeredOccurrence(existing, have[key], self)) {
				err := calendar.DeleteOne(existing, key, time.Now())
				if errors.Is(err, calendar.ErrLastOccurrence) {
					return nil, errDeleteCopy
				}
				if err == nil {
					delete(have, key)
					cancelled = true
				}
				continue
			}
			if o := occurrence(c); o != nil {
				o.Props.SetText(ical.PropStatus, "CANCELLED")
				setOrganizerSequence(o, sequenceOf(c))
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
	if len(before.Children) == 0 && !masterless {
		return nil, errPermanent{"no_occurrence"}
	}
	copyRequest := itip.AttendeeCopy
	if shared {
		copyRequest = itip.SharedAttendeeCopy
	}
	next, err := copyRequest(msg, before, self)
	if err != nil {
		return nil, errPermanent{"invalid_object"}
	}
	stamp(next)
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
		} else if r := c.Props.Get(ical.PropRecurrenceID); c.Name == ical.CompEvent && !masterless && (r == nil || !isOverride(before, r.Value)) {
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
	self, ok, err := Address(ctx, s.Store, u)
	if err != nil {
		return err
	}
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
		if err := s.deliverToAttendee(ctx, u, self, org, uid, method, invite, ""); err != nil {
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
		o, err := calendar.AddOverride(cal, occurrenceKeyOf(cal, invite, recurrenceID))
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
	_, err = Write(ctx, s.Store, c, o.Name, cal, raw, o.ETag, false, s.Limits, s.Plan(ctx, u, cal), u.ID)
	return err
}

// occurrenceKeyOf reads the occurrence KyPost names as it is written in the mail: the invitation's
// RECURRENCE-ID with that value, else a value in the series' own zone (DTSTART's TZID). A UTC
// value reads the same either way, so KyCalendar's own keys still work.
func occurrenceKeyOf(cal, invite *ical.Calendar, rid string) string {
	prop := &ical.Prop{Name: ical.PropRecurrenceID, Value: rid, Params: ical.Params{}}
	if m, err := calendar.Master(cal); err == nil && m.Props.Get(ical.PropDateTimeStart) != nil {
		prop.Params = m.Props.Get(ical.PropDateTimeStart).Params
	}
	for _, c := range invite.Children {
		if p := c.Props.Get(ical.PropRecurrenceID); c.Name == ical.CompEvent && p != nil && p.Value == rid {
			prop = p
			break
		}
	}
	if key, ok := calendar.OccurrenceKey(cal, prop); ok {
		return key
	}
	return rid
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

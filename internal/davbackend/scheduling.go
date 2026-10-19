package davbackend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Scheduler is server-side scheduling (RFC 6638): Plan queues the iTIP messages a write
// produces inside its transaction, DeliverDue delivers them. A nil *Scheduler schedules nothing.
type Scheduler struct {
	Store  store.Store
	Limits store.OwnerLimits // applied to deliveries into a recipient's calendar
	Now    func() time.Time
}

// External delivery has no transport yet: such messages fail at once instead of waiting.
var errNoTransport = errors.New("no transport for external attendees")

// errPermanent marks a delivery that retrying cannot fix.
type errPermanent struct{ reason string }

func (e errPermanent) Error() string { return e.reason }

const (
	outboxBatch   = 50
	outboxGiveUp  = 24 * time.Hour
	outboxBackoff = time.Minute
	outboxMaxWait = time.Hour
)

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// address is the user's calendar user address, or false when they have no email.
func address(u *store.User) (itip.Address, bool) {
	if u == nil || u.Email == "" || u.Role != "user" {
		return "", false
	}
	return itip.ParseAddress("mailto:" + u.Email)
}

func encodeCal(cal *ical.Calendar) ([]byte, error) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Plan is the store.Planner for writer writing newCal (nil for a delete). It is nil when
// scheduling is off or the writer has no address, so the write is exactly an unscheduled one.
func (s *Scheduler) Plan(writer *store.User, newCal *ical.Calendar) store.Planner {
	self, ok := address(writer)
	if s == nil || !ok {
		return nil
	}
	return func(previous []byte) ([]byte, []*store.OutboxMessage, error) {
		var oldCal *ical.Calendar
		if previous != nil {
			var err error
			if oldCal, err = ical.NewDecoder(bytes.NewReader(previous)).Decode(); err != nil {
				return nil, nil, fmt.Errorf("stored object does not parse: %w", err)
			}
		}
		var data []byte
		if newCal != nil {
			if org, ok := itip.Organizer(newCal); ok && org == self && itip.BumpSequence(oldCal, newCal) {
				data, _ = encodeCal(newCal) // nil when go-ical will not encode it: stored as sent
			}
		}
		msgs, err := itip.Outgoing(self, oldCal, newCal, s.now())
		if err != nil {
			// The object cannot be sent as it stands. It is still stored: refusing would break the
			// client's sync over something it cannot fix.
			return data, nil, nil
		}
		var rows []*store.OutboxMessage
		for _, m := range msgs {
			body, err := encodeCal(m.Calendar)
			if err != nil {
				return nil, nil, err
			}
			for _, to := range m.Recipients {
				sum := sha256.Sum256([]byte(writer.ID + "\x00" + m.Method + "\x00" + string(to) + "\x00" + string(body)))
				rows = append(rows, &store.OutboxMessage{ID: "obx_" + uuid.NewString(), DedupeKey: hex.EncodeToString(sum[:]),
					SenderID: writer.ID, Method: m.Method, Recipient: string(to), Body: body})
			}
		}
		return data, rows, nil
	}
}

// DeliverDue delivers up to one batch of due messages and reports how many it attempted.
func (s *Scheduler) DeliverDue(ctx context.Context) (int, error) {
	due, err := s.Store.Calendars().DueOutbox(ctx, s.now(), outboxBatch)
	if err != nil {
		return 0, err
	}
	for _, m := range due {
		err := s.deliverSafely(ctx, m)
		var perm errPermanent
		switch {
		case err == nil:
			err = s.Store.Calendars().FinishOutbox(ctx, m.ID, "delivered", s.now(), "")
		case errors.As(err, &perm) || errors.Is(err, errNoTransport) || s.now().Sub(m.CreatedAt) > outboxGiveUp:
			err = s.Store.Calendars().FinishOutbox(ctx, m.ID, "failed", s.now(), reason(err))
		default:
			wait := min(outboxBackoff<<min(m.Attempts, 10), outboxMaxWait)
			err = s.Store.Calendars().FinishOutbox(ctx, m.ID, "pending", s.now().Add(wait), reason(err))
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return 0, err
		}
	}
	return len(due), nil
}

// reason is a fixed code for the outbox row: never a message body or an address.
func reason(err error) string {
	var perm errPermanent
	switch {
	case errors.As(err, &perm):
		return perm.reason
	case errors.Is(err, errNoTransport):
		return "no_transport"
	case errors.Is(err, store.ErrPreconditionFailed):
		return "conflict"
	case errors.Is(err, store.ErrQuotaExceeded):
		return "quota"
	}
	return "error"
}

// deliverSafely is deliver with a panic turned into a permanent failure of that one message:
// the worker runs outside HTTP recovery, and go-ical panics on some input.
func (s *Scheduler) deliverSafely(ctx context.Context, m *store.OutboxMessage) (err error) {
	defer func() {
		if recover() != nil {
			err = errPermanent{"panic"}
		}
	}()
	return s.deliver(ctx, m)
}

func (s *Scheduler) deliver(ctx context.Context, m *store.OutboxMessage) error {
	sender, err := s.Store.Users().GetUserByID(ctx, m.SenderID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && sender.Status != "active") {
		return errPermanent{"sender_inactive"}
	}
	if err != nil {
		return err
	}
	from, ok := address(sender)
	if !ok {
		return errPermanent{"sender_no_address"}
	}
	to, ok := itip.ParseAddress(m.Recipient)
	if !ok {
		return errPermanent{"bad_recipient"}
	}
	recipient, err := s.Store.Users().CalendarUser(ctx, to.Email())
	if errors.Is(err, store.ErrNotFound) {
		return errNoTransport
	}
	if err != nil {
		return err
	}
	msg, err := itip.Decode(m.Body)
	if err != nil {
		return errPermanent{"bad_body"}
	}
	method, uid, err := itip.Validate(msg)
	if err != nil || method != m.Method {
		return errPermanent{"bad_body"}
	}
	org, _ := itip.Organizer(msg)
	switch method {
	case itip.MethodRequest, itip.MethodCancel:
		if org != from {
			return errPermanent{"not_organizer"}
		}
		return s.deliverToAttendee(ctx, recipient, to, org, uid, method, msg)
	default: // REPLY
		if org != to {
			return errPermanent{"not_organizer"}
		}
		return s.deliverReply(ctx, recipient, to, uid, from, msg)
	}
}

// writable is every calendar of u that can hold events and that u may edit: personal calendars
// that are not subscriptions, and group calendars an editor role reaches.
func (s *Scheduler) writable(ctx context.Context, u *store.User) ([]*store.Calendar, error) {
	if err := EnsureDefault(ctx, s.Store, u.ID, 0); err != nil {
		return nil, err
	}
	own, err := s.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, u.ID)
	if err != nil {
		return nil, err
	}
	grants, err := s.Store.Calendars().UserGrants(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	var out []*store.Calendar
	for _, c := range own {
		if !c.Subscribed && c.Holds(ical.CompEvent) {
			out = append(out, c)
		}
	}
	seen := map[string]bool{}
	for _, g := range grants {
		if seen[g.CalendarID] {
			continue
		}
		seen[g.CalendarID] = true
		c, err := s.Store.Calendars().GetCalendarByID(ctx, g.CalendarID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if c.Holds(ical.CompEvent) && access.CanEditEvents(c, access.Resolve(u, c, grants)) {
			out = append(out, c)
		}
	}
	return out, nil
}

// find is u's object with uid whose ORGANIZER is org, in a calendar u can edit.
func (s *Scheduler) find(ctx context.Context, u *store.User, uid string, org itip.Address) (*store.Calendar, *store.CalendarObject, *ical.Calendar, error) {
	cals, err := s.writable(ctx, u)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, c := range cals {
		o, err := s.Store.Calendars().GetObjectByUID(ctx, c.ID, uid)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			return nil, nil, nil, err
		}
		if got, ok := itip.Organizer(cal); !ok || got != org {
			return nil, nil, nil, errPermanent{"uid_taken"} // the UID belongs to another event
		}
		return c, o, cal, nil
	}
	return nil, nil, nil, store.ErrNotFound
}

func (s *Scheduler) put(ctx context.Context, c *store.Calendar, name string, cal *ical.Calendar, ifMatch string, create bool) error {
	raw, err := encodeCal(cal)
	if err != nil {
		return err
	}
	o, err := Prepare(c.ID, name, cal, raw)
	if err != nil {
		return errPermanent{"invalid_object"}
	}
	_, err = s.Store.Calendars().PutObject(ctx, o, ifMatch, create, s.Limits)
	return err
}

// deliverToAttendee writes the organizer's REQUEST or CANCEL into an internal attendee's
// calendar: their existing copy when they have one, else (REQUEST only) their default calendar.
func (s *Scheduler) deliverToAttendee(ctx context.Context, u *store.User, self, org itip.Address, uid, method string, msg *ical.Calendar) error {
	if org == self {
		return nil // the organizer's own address on the attendee list
	}
	c, o, existing, err := s.find(ctx, u, uid, org)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if method == itip.MethodCancel {
			return nil // nothing to cancel
		}
		def, err := s.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, u.ID, defaultSlug)
		if err != nil {
			return err
		}
		first, err := itip.AttendeeCopy(msg, nil, self)
		if err != nil {
			return errPermanent{"invalid_object"}
		}
		sum := sha256.Sum256([]byte(uid))
		return s.put(ctx, def, hex.EncodeToString(sum[:16])+".ics", first, "", true)
	case err != nil:
		return err
	}
	src := msg
	if method == itip.MethodCancel {
		src = existing
	}
	next, err := itip.AttendeeCopy(src, existing, self)
	if err != nil {
		return errPermanent{"invalid_object"}
	}
	if method == itip.MethodCancel {
		for _, ev := range next.Children {
			if ev.Name == ical.CompEvent {
				ev.Props.SetText(ical.PropStatus, "CANCELLED")
			}
		}
	}
	return s.put(ctx, c, o.Name, next, o.ETag, false)
}

// deliverReply records an internal attendee's answer on the organizer's object.
func (s *Scheduler) deliverReply(ctx context.Context, organizer *store.User, self itip.Address, uid string, from itip.Address, msg *ical.Calendar) error {
	c, o, stored, err := s.find(ctx, organizer, uid, self)
	if errors.Is(err, store.ErrNotFound) {
		return errPermanent{"no_event"}
	}
	if err != nil {
		return err
	}
	changed, err := itip.ApplyReply(stored, msg, from)
	if errors.Is(err, itip.ErrNotAttendee) || errors.Is(err, itip.ErrUIDMismatch) || errors.Is(err, itip.ErrInvalid) {
		return errPermanent{"not_attendee"}
	}
	if err != nil || !changed {
		return err
	}
	return s.put(ctx, c, o.Name, stored, o.ETag, false)
}

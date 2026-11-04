package davbackend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Scheduler is server-side scheduling (RFC 6638): Plan queues the iTIP messages a write
// produces inside its transaction, DeliverDue delivers them. A nil *Scheduler schedules nothing.
type Scheduler struct {
	Store  store.Store
	Limits store.OwnerLimits // applied to deliveries into a recipient's calendar
	Now    func() time.Time
	KyPost KyPostSender // nil: nothing reaches people outside the instance (no_transport)

	invitesMu sync.Mutex
	invites   map[string]*inviteWindow // sender ID -> this hour's new invitees
}

// InviteLimit is how many people one sender may newly invite per hour. Re-sending a meeting to
// people already on it does not count, so one meeting of the largest size (1000 attendees)
// always fits; the limit bounds how fast one account can fill other people's calendars.
const InviteLimit = 1000

type inviteWindow struct {
	start time.Time
	n     int
}

// InviteLimitError refuses a write that would invite more people than InviteLimit this hour.
type InviteLimitError struct{ Until time.Time }

func (e InviteLimitError) Error() string {
	return fmt.Sprintf("You have invited more than %d people in the last hour. You can send new invitations again at %s UTC.",
		InviteLimit, e.Until.UTC().Format("15:04"))
}

// admitInvites counts n new invitees for sender against InviteLimit.
func (s *Scheduler) admitInvites(sender string, n int) error {
	if n == 0 {
		return nil
	}
	s.invitesMu.Lock()
	defer s.invitesMu.Unlock()
	now := s.now()
	if s.invites == nil {
		s.invites = map[string]*inviteWindow{}
	}
	for id, w := range s.invites {
		if now.Sub(w.start) >= time.Hour {
			delete(s.invites, id)
		}
	}
	w := s.invites[sender]
	if w == nil {
		w = &inviteWindow{start: now}
		s.invites[sender] = w
	}
	if w.n+n > InviteLimit {
		return InviteLimitError{Until: w.start.Add(time.Hour)}
	}
	w.n += n
	return nil
}

// newInvitees counts the REQUEST recipients in msgs who were not attendees of oldCal.
func newInvitees(oldCal *ical.Calendar, msgs []itip.Message) int {
	had := map[itip.Address]bool{}
	if oldCal != nil {
		for _, ev := range oldCal.Children {
			if ev.Name == ical.CompEvent {
				for _, a := range itip.AttendeesOf(ev) {
					had[a.Address] = true
				}
			}
		}
	}
	seen := map[itip.Address]bool{}
	for _, m := range msgs {
		if m.Method != itip.MethodRequest {
			continue
		}
		for _, to := range m.Recipients {
			if !had[to] {
				seen[to] = true
			}
		}
	}
	return len(seen)
}

// KyPostSender mails a message to people outside the instance: a *kypost.Client, or the
// *kypost.Link KyIdentity can change at runtime (kypost.ErrNotConnected while unlinked).
type KyPostSender interface {
	Send(ctx context.Context, id string, req kypost.SendRequest) error
}

// errNoTransport is an external recipient with no KyPost configured: it fails at once.
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

// emailAddress is the address u's email spells, or false when u is not an everyday user with
// an email. It does not say u may use it: see Address.
func emailAddress(u *store.User) (itip.Address, bool) {
	if u == nil || u.Email == "" || u.Role != "user" {
		return "", false
	}
	return itip.ParseAddress("mailto:" + u.Email)
}

// Address is u's calendar user address: their email, when no other active everyday user holds
// it. A shared email is nobody's, so an account that copied a colleague's email can neither
// send nor answer as them, and the colleague does not fall back to "external" unnoticed.
func Address(ctx context.Context, st store.Store, u *store.User) (itip.Address, bool, error) {
	a, ok := emailAddress(u)
	if !ok {
		return "", false, nil
	}
	owner, err := st.Users().CalendarUser(ctx, a.Email())
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return a, owner.ID == u.ID, nil
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
func (s *Scheduler) Plan(ctx context.Context, writer *store.User, newCal *ical.Calendar) store.Planner {
	if s == nil {
		return nil
	}
	self, ok, err := Address(ctx, s.Store, writer)
	if err != nil {
		return func([]byte) ([]byte, []*store.OutboxMessage, error) { return nil, nil, err }
	}
	if !ok {
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
		if err := s.admitInvites(writer.ID, newInvitees(oldCal, msgs)); err != nil {
			return nil, nil, err
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
	from, ok, err := Address(ctx, s.Store, sender)
	if err != nil {
		return err
	}
	if !ok {
		return errPermanent{"sender_no_address"}
	}
	to, ok := itip.ParseAddress(m.Recipient)
	if !ok {
		return errPermanent{"bad_recipient"}
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
	if (method == itip.MethodReply && org != to) || (method != itip.MethodReply && org != from) {
		return errPermanent{"not_organizer"}
	}
	recipient, err := s.Store.Users().CalendarUser(ctx, to.Email())
	if errors.Is(err, store.ErrNotFound) {
		return s.sendExternal(ctx, m, sender, from, to)
	}
	if err != nil {
		return err
	}
	switch method {
	case itip.MethodRequest, itip.MethodCancel:
		return s.deliverToAttendee(ctx, recipient, to, org, uid, method, msg)
	default: // REPLY
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

// find is u's object with uid whose ORGANIZER is org, in a calendar u can edit. Looking for an
// attendee's copy (org is not u), a group calendar the organizer can also edit is skipped: an
// object there is the organizer's own, shared with u, not u's copy.
func (s *Scheduler) find(ctx context.Context, u *store.User, uid string, org itip.Address) (*store.Calendar, *store.CalendarObject, *ical.Calendar, error) {
	cals, err := s.writable(ctx, u)
	if err != nil {
		return nil, nil, nil, err
	}
	self, _ := emailAddress(u)
	for _, c := range cals {
		if org != self && c.OwnerKind != ownerUser {
			shared, err := s.organizerEdits(ctx, org, c)
			if err != nil {
				return nil, nil, nil, err
			}
			if shared {
				continue
			}
		}
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

// organizerEdits reports whether org is a user on this instance who can edit events in c.
func (s *Scheduler) organizerEdits(ctx context.Context, org itip.Address, c *store.Calendar) (bool, error) {
	ou, err := s.Store.Users().CalendarUser(ctx, org.Email())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	grants, err := s.Store.Calendars().UserGrants(ctx, ou.ID)
	if err != nil {
		return false, err
	}
	return access.CanEditEvents(c, access.Resolve(ou, c, grants)), nil
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
		def, err := s.inviteCalendar(ctx, u)
		if err != nil {
			return err
		}
		first, err := itip.AttendeeCopy(msg, nil, self)
		if err != nil {
			return errPermanent{"invalid_object"}
		}
		stamp(first)
		sum := sha256.Sum256([]byte(uid))
		return s.put(ctx, def, hex.EncodeToString(sum[:16])+".ics", first, "", true)
	case err != nil:
		return err
	}
	return s.deliverInto(ctx, c, o, existing, self, method, msg)
}

// inviteCalendar is where a new invitation lands: the user's default calendar, or their first
// personal event calendar when they deleted it or it holds no events. With neither (only
// subscriptions or task lists left) an event calendar is made: "default" again when it is free.
func (s *Scheduler) inviteCalendar(ctx context.Context, u *store.User) (*store.Calendar, error) {
	def, err := s.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, u.ID, defaultSlug)
	if err == nil && !def.Subscribed && def.Holds(ical.CompEvent) {
		return def, nil
	}
	missing := errors.Is(err, store.ErrNotFound)
	if err != nil && !missing {
		return nil, err
	}
	cals, err := s.writable(ctx, u)
	if err != nil {
		return nil, err
	}
	for _, c := range cals {
		if c.OwnerKind == ownerUser {
			return c, nil
		}
	}
	id := uuid.NewString()
	slug := defaultSlug
	if !missing {
		slug = "calendar-" + id[:8] // "default" is taken by a subscription or a task list
	}
	c := &store.Calendar{ID: "cal_" + id, OwnerKind: ownerUser, OwnerID: u.ID, Slug: slug, Name: "Calendar"}
	if err := s.Store.Calendars().CreateCalendar(ctx, c, 0); err != nil {
		return nil, err // retried: a slug taken meanwhile is found next time
	}
	return s.Store.Calendars().GetCalendarByID(ctx, c.ID)
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
	next, changed, err := applyReply(stored, msg, from)
	if errors.Is(err, itip.ErrNotAttendee) || errors.Is(err, itip.ErrUIDMismatch) || errors.Is(err, itip.ErrInvalid) {
		return errPermanent{"not_attendee"}
	}
	if err != nil || !changed {
		return err
	}
	return s.put(ctx, c, o.Name, next, o.ETag, false)
}

// maxNewOverrides caps the overrides one message may add to a stored series: each is a copy of the
// master. A message that would add more is refused whole, so it is never half applied.
const maxNewOverrides = 20

var errTooManyOccurrences = errPermanent{"too_many_occurrences"}

func sequenceOf(c *ical.Component) int {
	n := 0
	if p := c.Props.Get(ical.PropSequence); p != nil {
		n, _ = p.Int()
	}
	return n
}

// applyReply records from's answer (itip.ApplyReply) on a copy of stored, with the overrides it
// needs (addOverrides), and returns the copy. Only a copy the answer changed is worth storing, so
// a reply that records nothing leaves no override behind.
func applyReply(stored, msg *ical.Calendar, from itip.Address) (*ical.Calendar, bool, error) {
	raw, err := encodeCal(stored)
	if err != nil {
		return nil, false, err
	}
	work, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, false, err
	}
	if err := addOverrides(work, msg, from); err != nil {
		return nil, false, err
	}
	changed, err := itip.ApplyReply(work, msg, from)
	return work, changed, err
}

// addOverrides readies stored, an organizer's series, for sender's reply. A reply component naming
// an occurrence gets its RECURRENCE-ID written as the object's override for that occurrence has it.
// When the object holds that occurrence only through its master, and sender (an attendee of the
// series) answers it, as of the series' current SEQUENCE, differently from the series, the
// override is added first (RFC 5546 3.2.3). ApplyReply then records the answer there. More than
// maxNewOverrides to add is errTooManyOccurrences, and nothing is added.
func addOverrides(stored, reply *ical.Calendar, sender itip.Address) error {
	m, err := calendar.Master(stored)
	if err != nil {
		return nil
	}
	series := ""
	for _, a := range itip.AttendeesOf(m) {
		if a.Address == sender {
			series = a.Status
		}
	}
	have := overridesOf(stored)
	missing := map[string]*ical.Component{}
	for _, c := range reply.Children {
		rid := c.Props.Get(ical.PropRecurrenceID)
		if c.Name != ical.CompEvent || rid == nil {
			continue
		}
		key, ok := calendar.OccurrenceKey(stored, rid)
		if !ok {
			continue
		}
		if o := have[key]; o != nil {
			sameOccurrence(c, o)
		} else if ps := itip.ReplyAnswer(c, sender); series != "" && ps != "" && ps != series && sequenceOf(c) >= sequenceOf(m) {
			missing[key] = c
		}
	}
	if len(missing) > maxNewOverrides {
		return errTooManyOccurrences
	}
	for key, c := range missing {
		if o, err := calendar.AddOverride(stored, key); err == nil {
			sameOccurrence(c, o)
		}
	}
	return nil
}

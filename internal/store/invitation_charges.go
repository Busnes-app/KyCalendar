package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/emersion/go-ical"
)

// ownerUsage counts objects where they are charged, including pending sender reservations.
// Exclude one replaced object, or the whole subscription when excludeName is empty.
func (c *calendarStore) ownerUsage(ctx context.Context, tx *sql.Tx, kind, id, excludeCalendar, excludeName string) (int, int64, error) {
	var n int
	var used int64
	err := tx.QueryRowContext(ctx, c.q(`SELECT COUNT(*), COALESCE(SUM(LENGTH(o.data)), 0) FROM calendar_objects o JOIN calendars c ON c.id = o.calendar_id
 WHERE (CASE WHEN o.invitation_recipient <> '' THEN 'user' ELSE c.owner_kind END) = ?
 AND (CASE WHEN o.invitation_sender <> '' THEN o.invitation_sender WHEN o.invitation_recipient <> '' THEN o.invitation_recipient ELSE c.owner_id END) = ?
 AND NOT (o.calendar_id = ? AND (? = '' OR o.name = ?))`), kind, id, excludeCalendar, excludeName, excludeName).Scan(&n, &used)
	if err != nil {
		return 0, 0, err
	}
	if kind == "user" {
		var pending int64
		if err := tx.QueryRowContext(ctx, c.q(`SELECT COALESCE(SUM(LENGTH(body)), 0) FROM scheduling_outbox WHERE status = 'pending' AND sender_id = ?`), id).Scan(&pending); err != nil {
			return 0, 0, err
		}
		used += pending
	}
	return n, used, nil
}

// invitationCharge runs after planning, under calendar-total. Client edits inherit the stored
// sponsorship; only delivery of a pending REQUEST can establish it. Any answer transfers the
// whole copy once, including an answer to one occurrence. Erasing an answer cannot charge back.
func (c *calendarStore) invitationCharge(ctx context.Context, tx *sql.Tx, o *CalendarObject, exists bool) error {
	o.InvitationSender, o.InvitationAttendee, o.InvitationRecipient = "", "", ""
	var previous []byte
	var previousUID string
	if exists {
		if err := tx.QueryRowContext(ctx, c.q(`SELECT invitation_sender, invitation_attendee, invitation_recipient, data, uid FROM calendar_objects WHERE calendar_id = ? AND name = ?`), o.CalendarID, o.Name).Scan(&o.InvitationSender, &o.InvitationAttendee, &o.InvitationRecipient, &previous, &previousUID); err != nil {
			return err
		}
	}
	if o.DeliveryID != "" {
		var sender, attendee, method string
		err := tx.QueryRowContext(ctx, c.q(`SELECT sender_id, recipient, method FROM scheduling_outbox WHERE id = ? AND status = 'pending'`), o.DeliveryID).Scan(&sender, &attendee, &method)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPreconditionFailed
		}
		if err != nil {
			return err
		}
		if o.InvitationRecipient == "" && method == itip.MethodRequest {
			if o.DeliveryRecipient == "" {
				return errors.New("delivery missing recipient identity")
			}
			o.InvitationSender, o.InvitationAttendee, o.InvitationRecipient = sender, attendee, o.DeliveryRecipient
		}
		// Release the queued reservation in the same transaction as installing its copy. A failure
		// rolls both back; two workers cannot deliver and bill the same reservation twice.
		if _, err := tx.ExecContext(ctx, c.q(`UPDATE scheduling_outbox SET status = 'delivered', attempts = attempts + 1, body = ?, last_error = '' WHERE id = ?`), []byte{}, o.DeliveryID); err != nil {
			return err
		}
	}

	// Delivery can establish sponsorship but can never prove an attendee's response.
	if o.InvitationSender == "" || o.DeliveryID != "" || o.WriterID == "" {
		return nil
	}
	before, err := ical.NewDecoder(bytes.NewReader(previous)).Decode()
	if err != nil {
		return fmt.Errorf("stored invitation: %w", err)
	}
	next, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return fmt.Errorf("invitation charge: %w", err)
	}

	beforeOrganizer, beforeOK := itip.Organizer(before)
	nextOrganizer, nextOK := itip.Organizer(next)
	identityChanged := o.UID != previousUID || beforeOK != nextOK || beforeOrganizer != nextOrganizer
	for _, ev := range next.Children {
		if ev.Name != ical.CompEvent {
			continue
		}
		p := ev.Props.Get(ical.PropOrganizer)
		if p == nil {
			identityChanged = true
			continue
		}
		org, ok := itip.ParseAddress(p.Value)
		if !ok || org != beforeOrganizer {
			identityChanged = true
		}
	}
	// A client may keep the delivered copy at the sender's cost, not enlarge or replace it.
	// Such content belongs to its authenticated writer and must fit that writer's quota.
	if len(o.Data) > len(previous) || identityChanged {
		o.InvitationSender = ""
		o.InvitationRecipient = o.WriterID
		o.InvitationAttendee = ""
		return nil
	}
	var email string
	err = tx.QueryRowContext(ctx, c.q(`SELECT email FROM users u WHERE id = ? AND status = 'active' AND role = 'user'
 AND (SELECT COUNT(*) FROM users v WHERE LOWER(v.email) = LOWER(u.email) AND v.status = 'active' AND v.role = 'user') = 1`), o.WriterID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	self, ok := itip.ParseAddress("mailto:" + email)
	if !ok {
		return nil
	}
	// The existing pure scheduling rule proves whose own answer changed, including occurrences.
	replies, err := itip.Outgoing(self, before, next, time.Now())
	if err != nil {
		return err
	}
	for _, reply := range replies {
		if reply.Method != itip.MethodReply {
			continue
		}
		for _, ev := range reply.Calendar.Children {
			for _, a := range itip.AttendeesOf(ev) {
				if a.Address == self && a.Status != "NEEDS-ACTION" {
					o.InvitationSender = ""
					o.InvitationRecipient = o.WriterID
					o.InvitationAttendee = string(self)
					return nil
				}
			}
		}
	}
	return nil
}

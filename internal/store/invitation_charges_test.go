package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

const unansweredCopy = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:invite\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\nORGANIZER:mailto:alice@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestInvitationChargeTransferIsAtomic(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	source := outboxObject(t, st, "alice")
	recipient := outboxObject(t, st, "bob")
	source.Data = []byte("seed")
	cap := int64(len(unansweredCopy) + len(source.Data))
	lim := store.OwnerLimits{MaxBytes: cap, MaxTotalBytes: 10000}
	// The recipient's own budget is already full. An unsolicited copy must not spend it.
	recipient.Data = []byte(strings.Repeat("x", int(cap)))
	if _, err := st.Calendars().PutObject(ctx, recipient, "", true, lim); err != nil {
		t.Fatal(err)
	}
	pending := row("invite", "usr_alice", len(unansweredCopy))
	pending.Recipient = "mailto:bob@example.com"
	if _, err := st.Calendars().PutObjectPlanned(ctx, source, "", true, lim, plan(pending)); err != nil {
		t.Fatal(err)
	}
	copy := obj(recipient.CalendarID, "invite.ics", "invite", unansweredCopy, 0, nil)
	copy.DeliveryID, copy.DeliveryRecipient = pending.ID, "usr_bob"
	if _, err := st.Calendars().PutObject(ctx, copy, "", true, lim); err != nil {
		t.Fatalf("reservation must be exchanged, not counted twice: %v", err)
	}
	stored, _ := st.Calendars().GetObject(ctx, copy.CalendarID, copy.Name)
	if stored.InvitationSender != "usr_alice" {
		t.Fatalf("wrong sponsor: %+v", stored)
	}
	due, _ := st.Calendars().DueOutbox(ctx, time.Now().Add(time.Hour), 10)
	if len(due) != 0 {
		t.Fatal("delivered reservation still pending")
	}
	extra := obj(source.CalendarID, "extra.ics", "extra", "x", 0, nil)
	if _, err := st.Calendars().PutObject(ctx, extra, "", true, lim); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("sender escaped sponsored bytes: %v", err)
	}
	// A failed answer must leave the old bytes, ETag, sync sequence and charge untouched.
	before, _ := st.Calendars().GetCalendarByID(ctx, copy.CalendarID)
	answer := obj(copy.CalendarID, copy.Name, copy.UID, strings.Replace(unansweredCopy, "NEEDS-ACTION", "ACCEPTED", 1), 0, nil)
	answer.WriterID = "usr_bob"
	if _, err := st.Calendars().PutObject(ctx, answer, stored.ETag, false, lim); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("answer over recipient quota: %v", err)
	}
	after, _ := st.Calendars().GetCalendarByID(ctx, copy.CalendarID)
	unchanged, _ := st.Calendars().GetObject(ctx, copy.CalendarID, copy.Name)
	if before.Seq != after.Seq || unchanged.ETag != stored.ETag || unchanged.InvitationSender != "usr_alice" {
		t.Fatal("refused transfer changed data, seq or charge")
	}
	if err := st.Calendars().DeleteObject(ctx, recipient.CalendarID, recipient.Name, recipient.ETag); err != nil {
		t.Fatal(err)
	}
	answer.WriterID = "usr_bob"
	if _, err := st.Calendars().PutObject(ctx, answer, stored.ETag, false, lim); err != nil {
		t.Fatalf("answer after freeing recipient space: %v", err)
	}
	answered, _ := st.Calendars().GetObject(ctx, copy.CalendarID, copy.Name)
	if answered.InvitationSender != "" {
		t.Fatal("answer did not release sender")
	}
	if _, err := st.Calendars().PutObject(ctx, extra, "", true, lim); err != nil {
		t.Fatalf("sender charge survived answer: %v", err)
	}
	// Writing NEEDS-ACTION back is an attendee edit, not a new sender-sponsored delivery.
	reset := obj(copy.CalendarID, copy.Name, copy.UID, unansweredCopy, 0, nil)
	if _, err := st.Calendars().PutObject(ctx, reset, answered.ETag, false, lim); err != nil {
		t.Fatal(err)
	}
	reset, _ = st.Calendars().GetObject(ctx, copy.CalendarID, copy.Name)
	if reset.InvitationSender != "" {
		t.Fatal("attendee charged an answered copy back to sender")
	}
}

func TestConcurrentInvitationDeliveriesRespectSenderObjectQuota(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	source := outboxObject(t, st, "alice")
	bob := outboxObject(t, st, "bob")
	carol := outboxObject(t, st, "carol")
	a, b := row("bob", "usr_alice", 200), row("carol", "usr_alice", 200)
	a.Recipient = "mailto:bob@example.com"
	b.Recipient = "mailto:carol@example.com"
	lim := store.OwnerLimits{MaxObjects: 2, MaxBytes: 4096} // organizer plus one delivered copy
	if _, err := st.Calendars().PutObjectPlanned(ctx, source, "", true, lim, plan(a, b)); err != nil {
		t.Fatal(err)
	}
	targets := []*store.CalendarObject{bob, carol}
	errs := concurrently(2, func(i int) error {
		data := unansweredCopy
		id := a.ID
		if i == 1 {
			data = strings.ReplaceAll(data, "bob@", "carol@")
			id = b.ID
		}
		o := obj(targets[i].CalendarID, "invite.ics", "invite", data, 0, nil)
		o.DeliveryID, o.DeliveryRecipient = id, "usr_bob"
		if i == 1 {
			o.DeliveryRecipient = "usr_carol"
		}
		_, err := st.Calendars().PutObject(ctx, o, "", true, lim)
		return err
	})
	ok, refused := 0, 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if errors.Is(err, store.ErrQuotaExceeded) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("deliveries: %d accepted, %d refused", ok, refused)
	}
	due, _ := st.Calendars().DueOutbox(ctx, time.Now().Add(time.Hour), 10)
	if len(due) != 1 {
		t.Fatalf("refused delivery lost reservation: %d pending", len(due))
	}
}

func TestAnsweredGroupCalendarInvitationIsChargedToAttendee(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	source := outboxObject(t, st, "alice")
	bob := outboxObject(t, st, "bob")
	group := &store.Calendar{ID: "cal_group", OwnerKind: "group", OwnerID: "cal_group", Slug: "shared", Name: "Shared"}
	if err := st.Calendars().CreateCalendar(ctx, group, 0); err != nil {
		t.Fatal(err)
	}
	// Existing group copy: the next internal REQUEST establishes trusted delivery metadata.
	copy := obj(group.ID, "invite.ics", "invite", unansweredCopy, 0, nil)
	if _, err := st.Calendars().PutObject(ctx, copy, "", true, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	pending := row("group-invite", "usr_alice", len(unansweredCopy))
	pending.Recipient = "mailto:bob@example.com"
	if _, err := st.Calendars().PutObjectPlanned(ctx, source, "", true, store.OwnerLimits{}, plan(pending)); err != nil {
		t.Fatal(err)
	}
	delivered := *copy
	delivered.DeliveryID, delivered.DeliveryRecipient = pending.ID, "usr_bob"
	if _, err := st.Calendars().PutObject(ctx, &delivered, copy.ETag, false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	bob.Data = []byte(strings.Repeat("x", len(unansweredCopy)))
	lim := store.OwnerLimits{MaxBytes: int64(len(unansweredCopy))}
	if _, err := st.Calendars().PutObject(ctx, bob, "", true, lim); err != nil {
		t.Fatal(err)
	}
	answer := obj(group.ID, copy.Name, copy.UID, strings.Replace(unansweredCopy, "NEEDS-ACTION", "TENTATIVE", 1), 0, nil)
	answer.WriterID = "usr_bob"
	if _, err := st.Calendars().PutObject(ctx, answer, delivered.ETag, false, lim); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("answer charged the group instead of the full attendee: %v", err)
	}
	if err := st.Calendars().DeleteObject(ctx, bob.CalendarID, bob.Name, bob.ETag); err != nil {
		t.Fatal(err)
	}
	answer.WriterID = "usr_bob"
	if _, err := st.Calendars().PutObject(ctx, answer, delivered.ETag, false, lim); err != nil {
		t.Fatal(err)
	}
	answer, _ = st.Calendars().GetObject(ctx, group.ID, copy.Name)
	if answer.InvitationSender != "" || answer.InvitationRecipient != "usr_bob" {
		t.Fatalf("wrong charge after group answer: %+v", answer)
	}
}

package davbackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// OutOfOfficeUID is the UID of the one event KyPost's out-of-office period keeps in u's calendar.
func OutOfOfficeUID(userID string) string {
	sum := sha256.Sum256([]byte("kypost-ooo\x00" + userID))
	return "kypost-ooo-" + hex.EncodeToString(sum[:16])
}

// ApplyOutOfOffice makes u's calendar match o: one busy, out-of-office event over the period
// while o.Away, none otherwise. The event is found by its UID in u's own calendars only: a
// calendar shared to u is never searched, so another editor cannot plant the UID there and
// have the private period published to that calendar's readers.
func (s *Scheduler) ApplyOutOfOffice(ctx context.Context, u *store.User, o *store.OutOfOffice) error {
	uid := OutOfOfficeUID(u.ID)
	cals, err := s.writable(ctx, u)
	if err != nil {
		return err
	}
	var c *store.Calendar
	var obj *store.CalendarObject
	for _, cal := range cals {
		if cal.OwnerKind != ownerUser || cal.OwnerID != u.ID {
			continue
		}
		if found, err := s.Store.Calendars().GetObjectByUID(ctx, cal.ID, uid); err == nil {
			c, obj = cal, found
			break
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	if !o.Away {
		if obj == nil {
			return nil
		}
		err := Delete(ctx, s.Store, c, obj.Name, obj.ETag, s.Limits, nil)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	cal := outOfOfficeEvent(uid, o, s.now())
	raw, err := encodeCal(cal)
	if err != nil {
		return err
	}
	if obj != nil {
		_, err = Write(ctx, s.Store, c, obj.Name, cal, raw, obj.ETag, false, s.Limits, nil, u.ID)
		return err
	}
	if c, err = s.inviteCalendar(ctx, u); err != nil {
		return err
	}
	_, err = Write(ctx, s.Store, c, uid+".ics", cal, raw, "", true, s.Limits, nil, u.ID)
	return err
}

// outOfOfficeEvent is the integration's event: opaque, so free/busy shows busy, and marked
// out-of-office for clients that read Microsoft's busy status.
func outOfOfficeEvent(uid string, o *store.OutOfOffice, now time.Time) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Busnes//KyCalendar//EN")
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	ev.Props.SetText(ical.PropSummary, "Out of office")
	ev.Props.SetText(ical.PropTransparency, "OPAQUE")
	ev.Props.SetText("X-MICROSOFT-CDO-BUSYSTATUS", "OOF")
	ev.Props.SetText("X-KYCALENDAR-OUT-OF-OFFICE", "TRUE")
	if zone, err := time.LoadLocation(o.Zone); o.AllDay && err == nil {
		ev.Props.SetDate(ical.PropDateTimeStart, o.Start.In(zone))
		ev.Props.SetDate(ical.PropDateTimeEnd, o.End.In(zone))
	} else {
		ev.Props.SetDateTime(ical.PropDateTimeStart, o.Start.UTC())
		ev.Props.SetDateTime(ical.PropDateTimeEnd, o.End.UTC())
	}
	cal.Children = append(cal.Children, ev.Component)
	return cal
}

// autoTentative answers u's new invitation TENTATIVE, never anything else, while their
// out-of-office period with automatic answers runs. The comment is the local text for an
// organizer who is a KyCalendar user, else the remote text (empty unless KyPost sends one).
func (s *Scheduler) autoTentative(ctx context.Context, u *store.User, uid string, invite *ical.Calendar) bool {
	o, err := s.Store.Users().OutOfOffice(ctx, u.ID)
	if err != nil || !o.ActiveAt(s.now()) {
		return false
	}
	comment := o.CommentRemote
	if org, ok := itip.Organizer(invite); ok {
		if _, err := s.Store.Users().CalendarUser(ctx, u.OrgID, org.Email()); err == nil {
			comment = o.CommentLocal
		}
	}
	return s.respond(ctx, u, uid, "", "TENTATIVE", invite, comment) == nil
}

// autoTentativeDue reports whether u answers invitations automatically now: one read, so the
// caller only looks for an existing copy when it does.
func (s *Scheduler) autoTentativeDue(ctx context.Context, u *store.User) bool {
	o, err := s.Store.Users().OutOfOffice(ctx, u.ID)
	return err == nil && o.ActiveAt(s.now())
}

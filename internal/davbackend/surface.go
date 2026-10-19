package davbackend

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Inbox and outbox live in the calendar home under "_" names: no calendar slug starts with
// "_", and group segments are "_cal_<uuid>", so neither can collide with a calendar.
const (
	inboxSegment  = "_inbox"
	outboxSegment = "_outbox"
)

// Outbox free/busy bounds per request.
const (
	maxFreeBusyRecipients = 50
	maxOutboxRange        = 62 * 24 * time.Hour
)

var (
	errNotOrganizer = webdav.NewHTTPError(http.StatusForbidden, errors.New("the organizer must be you"))
	errOutboxTooBig = webdav.NewHTTPError(http.StatusForbidden, errors.New("free/busy request too large: ask for fewer people or a shorter range"))
)

func (b *Backend) inbox() string  { return b.home() + inboxSegment + "/" }
func (b *Backend) outbox() string { return b.home() + outboxSegment + "/" }

// Scheduling is nil, so nothing about scheduling is advertised, while scheduling is off or the
// user has no calendar user address.
func (b *Backend) Scheduling(ctx context.Context) (*caldav.Scheduling, error) {
	self, ok := address(b.User)
	if b.Sched == nil || !ok {
		return nil, nil
	}
	return &caldav.Scheduling{
		CalendarUserAddresses: []string{string(self)},
		InboxPath:             b.inbox(),
		OutboxPath:            b.outbox(),
		DefaultCalendarPath:   b.home() + defaultSlug + "/",
	}, nil
}

// ScheduleFreeBusy answers an outbox VFREEBUSY request for people on this instance: busy times
// of their personal event calendars, subscriptions excluded (they are transparent). Anyone
// else gets 3.7. The organizer must be the caller.
func (b *Backend) ScheduleFreeBusy(ctx context.Context, req *ical.Calendar) ([]caldav.ScheduleResponse, error) {
	self, ok := address(b.User)
	if b.Sched == nil || !ok {
		return nil, errNotOrganizer
	}
	var fb *ical.Component
	for _, c := range req.Children {
		if c.Name == ical.CompFreeBusy {
			fb = c
		}
	}
	if org, ok := itip.ParseAddress(fb.Props.Get(ical.PropOrganizer).Value); !ok || org != self {
		return nil, errNotOrganizer
	}
	start, err1 := fb.Props.DateTime(ical.PropDateTimeStart, time.UTC)
	end, err2 := fb.Props.DateTime(ical.PropDateTimeEnd, time.UTC)
	if err1 != nil || err2 != nil || !end.After(start) {
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidSchedulingMessage)
	}
	atts := fb.Props[ical.PropAttendee]
	if end.Sub(start) > maxOutboxRange || len(atts) > maxFreeBusyRecipients {
		return nil, errOutboxTooBig
	}
	begun := time.Now()
	var out []caldav.ScheduleResponse
	for _, p := range atts {
		to, ok := itip.ParseAddress(p.Value)
		resp := caldav.ScheduleResponse{Recipient: p.Value, Status: "3.7;Invalid calendar user"}
		if ok {
			resp.Recipient = string(to)
			u, err := b.Store.Users().CalendarUser(ctx, to.Email())
			switch {
			case errors.Is(err, store.ErrNotFound):
			case err != nil:
				return nil, err
			default:
				periods, err := b.personalBusy(ctx, u, start, end, begun)
				if errors.Is(err, errFreeBusyTooBig) {
					resp.Status = "5.1;Could not complete delivery"
					break
				}
				if err != nil {
					return nil, err
				}
				resp.Status = "2.0;Success"
				resp.Calendar = freeBusyReply(fb, to, start, end, periods)
			}
		}
		out = append(out, resp)
	}
	return out, nil
}

func (b *Backend) personalBusy(ctx context.Context, u *store.User, start, end, begun time.Time) ([]caldav.FreeBusyPeriod, error) {
	own, err := b.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, u.ID)
	if err != nil {
		return nil, err
	}
	var cals []*store.Calendar
	for _, c := range own {
		if !c.Subscribed && c.Holds(ical.CompEvent) {
			cals = append(cals, c)
		}
	}
	return busyIn(ctx, b.Store, cals, start, end, begun)
}

// freeBusyReply is the METHOD:REPLY VFREEBUSY for one recipient of request.
func freeBusyReply(request *ical.Component, to itip.Address, start, end time.Time, periods []caldav.FreeBusyPeriod) *ical.Calendar {
	cal := caldav.FreeBusyCalendar(start, end, time.Now(), periods)
	cal.Props.SetText(ical.PropMethod, "REPLY")
	fb := cal.Children[0]
	if uid := request.Props.Get(ical.PropUID); uid != nil {
		fb.Props.SetText(ical.PropUID, uid.Value)
	}
	org := ical.NewProp(ical.PropOrganizer)
	org.Value = request.Props.Get(ical.PropOrganizer).Value
	fb.Props.Set(org)
	att := ical.NewProp(ical.PropAttendee)
	att.Value = string(to)
	fb.Props.Set(att)
	return cal
}

// scheduleTag is the object's Schedule-Tag while scheduling is on; "" otherwise.
func (b *Backend) scheduleTag(cal *ical.Calendar) string {
	if b.Sched == nil || cal == nil {
		return ""
	}
	return itip.ScheduleTag(cal)
}

// isInbox reports p is the user's schedule inbox while scheduling is on. Deliveries go straight
// to the calendar (RFC 6638 §4.3 allows it), so the inbox is always empty.
func (b *Backend) isInbox(p string) bool { return b.Sched != nil && p == b.inbox() }

// syncInbox answers sync-collection on the empty inbox: one token per sync epoch.
func (b *Backend) syncInbox(ctx context.Context, token string) (*caldav.SyncResult, error) {
	epoch, err := b.Store.Calendars().SyncEpoch(ctx)
	if err != nil {
		return nil, err
	}
	current := calendar.FormatSyncToken(epoch, inboxSegment, 0)
	if token != "" && token != current {
		return nil, caldav.ErrInvalidSyncToken
	}
	return &caldav.SyncResult{SyncToken: current}, nil
}

// matchScheduleTag checks If-Schedule-Tag-Match against the stored object (412 when it moved or
// is missing) and returns the If-Match to write with: the caller's, else the stored ETag, so
// the check and the write are one atomic condition.
func (b *Backend) matchScheduleTag(ctx context.Context, c *store.Calendar, name, header, ifMatch string) (string, error) {
	want, ok := strings.CutPrefix(strings.TrimSpace(header), `"`)
	if want, ok = strings.CutSuffix(want, `"`); !ok || want == "" {
		return "", webdav.NewHTTPError(http.StatusBadRequest, errors.New("malformed If-Schedule-Tag-Match"))
	}
	o, err := b.Store.Calendars().GetObject(ctx, c.ID, name)
	if errors.Is(err, store.ErrNotFound) {
		return "", webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	}
	if err != nil {
		return "", err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil || b.scheduleTag(cal) != want {
		return "", webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("the meeting changed: Schedule-Tag does not match"))
	}
	if ifMatch == "" {
		ifMatch = o.ETag
	}
	return ifMatch, nil
}

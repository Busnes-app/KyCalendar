package davbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// MeetingBlocks is u's busy blocks in [start, end) for KyMessages' status
// (docs/SUITE-INTEGRATIONS.md section P): instances in their personal event calendars that are
// opaque, confirmed (not cancelled or tentative), and that u organised or accepted. Their
// out-of-office event is left out: out-of-office reaches KyMessages from KyPost. Times only.
func MeetingBlocks(ctx context.Context, st store.Store, u *store.User, start, end time.Time) ([]calendar.Busy, error) {
	own, err := st.Calendars().ListCalendarsByOwner(ctx, ownerUser, u.ID)
	if err != nil {
		return nil, err
	}
	self := itip.Address("mailto:" + strings.ToLower(u.Email))
	begun := time.Now()
	var out []calendar.Busy
	for _, c := range own {
		if c.Subscribed || !c.Holds(ical.CompEvent) {
			continue
		}
		objs, err := st.Calendars().ListObjectsInRange(ctx, c.ID, ical.CompEvent, start.Unix(), end.Unix())
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			if time.Since(begun) > maxFreeBusyTime {
				return nil, errFreeBusyTooBig
			}
			cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
			if err != nil {
				return nil, fmt.Errorf("stored object %s/%s does not parse: %w", c.ID, o.Name, err)
			}
			insts, err := calendar.Expand(cal, start, end, time.UTC, maxFreeBusyInstances-len(out))
			if errors.Is(err, calendar.ErrTooManyInstances) {
				return nil, errFreeBusyTooBig
			}
			if err != nil {
				return nil, err
			}
			for _, in := range insts {
				if meetingInstance(in.Event, self) {
					out = append(out, calendar.Busy{Start: in.Start.UTC(), End: in.End.UTC()})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b calendar.Busy) int { return a.Start.Compare(b.Start) })
	return out, nil
}

// meetingInstance reports whether ev keeps self busy as a meeting.
func meetingInstance(ev *ical.Component, self itip.Address) bool {
	text := func(name string) string {
		if p := ev.Props.Get(name); p != nil {
			return strings.ToUpper(strings.TrimSpace(p.Value))
		}
		return ""
	}
	if text(ical.PropTransparency) == "TRANSPARENT" || text(ical.PropStatus) == "CANCELLED" || text(ical.PropStatus) == "TENTATIVE" ||
		text("X-KYCALENDAR-OUT-OF-OFFICE") == "TRUE" || text("X-MICROSOFT-CDO-BUSYSTATUS") == "OOF" {
		return false
	}
	org := ev.Props.Get(ical.PropOrganizer)
	if org == nil {
		return true // the member's own appointment
	}
	if a, ok := itip.ParseAddress(org.Value); ok && a == self {
		return true
	}
	for _, a := range itip.AttendeesOf(ev) {
		if a.Address == self {
			return a.Status == "ACCEPTED"
		}
	}
	return false
}

package davbackend

import (
	"context"
	"errors"
	"fmt"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// ErrInvalidResource is an object CalDAV refuses outright, such as one carrying METHOD.
var ErrInvalidResource = errors.New("davbackend: not a valid calendar object resource")

// Write is the one write path for calendar objects, from CalDAV PUT and from web edits alike:
// CalDAV validation, Inspect, then a store write with ETag preconditions, quotas and a change
// row in one transaction.
func Write(ctx context.Context, st store.Store, calendarID, name string, cal *ical.Calendar, raw []byte, ifMatch string, ifNoneMatch bool, lim store.OwnerLimits) (*store.CalendarObject, error) {
	if _, _, err := caldav.ValidateCalendarObject(cal); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResource, err)
	}
	info, err := calendar.Inspect(cal)
	if err != nil {
		return nil, err
	}
	o := &store.CalendarObject{CalendarID: calendarID, Name: name, UID: info.UID, Data: raw, FirstStart: info.FirstStart, LastEnd: info.LastEnd}
	if _, err := st.Calendars().PutObject(ctx, o, ifMatch, ifNoneMatch, lim); err != nil {
		return nil, err
	}
	return o, nil
}

// EnsureDefault creates the user's default personal calendar when they own none.
func EnsureDefault(ctx context.Context, st store.Store, userID string, maxCalendars int) error {
	cals, err := st.Calendars().ListCalendarsByOwner(ctx, ownerUser, userID)
	if err != nil || len(cals) > 0 {
		return err
	}
	err = st.Calendars().CreateCalendar(ctx, &store.Calendar{
		ID: "cal_" + uuid.NewString(), OwnerKind: ownerUser, OwnerID: userID,
		Slug: defaultSlug, Name: "Calendar",
	}, maxCalendars)
	if errors.Is(err, store.ErrAlreadyExists) {
		return nil
	}
	return err
}

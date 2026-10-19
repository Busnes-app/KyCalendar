package davbackend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// ErrInvalidResource is an object CalDAV refuses outright, such as one carrying METHOD.
var ErrInvalidResource = errors.New("davbackend: not a valid calendar object resource")

// ErrTooLarge is an object over calendar.MaxObjectSize, whichever path wrote it.
var ErrTooLarge = errors.New("davbackend: calendar object too large")

// Write is the one write path for calendar objects, from CalDAV PUT and from web edits alike:
// CalDAV validation, Inspect, the calendar's component set, then a store write with ETag
// preconditions, quotas and a change row in one transaction. The component set is fixed at
// creation, so checking it outside the transaction is safe. A non-nil plan (Scheduler.Plan)
// runs inside the store's transaction and may rewrite the object; the returned object holds
// what was stored.
func Write(ctx context.Context, st store.Store, c *store.Calendar, name string, cal *ical.Calendar, raw []byte, ifMatch string, ifNoneMatch bool, lim store.OwnerLimits, plan store.Planner) (*store.CalendarObject, error) {
	o, err := Prepare(c.ID, name, cal, raw)
	if err != nil {
		return nil, err
	}
	if !c.Holds(o.Component) {
		return nil, fmt.Errorf("%w: this calendar holds %s", calendar.ErrUnsupportedComponent, strings.Join(c.Components, " and "))
	}
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, ifMatch, ifNoneMatch, lim, plan); err != nil {
		return nil, err
	}
	return o, nil
}

// Delete is the one delete path for calendar objects, with plan run inside its transaction.
func Delete(ctx context.Context, st store.Store, c *store.Calendar, name, ifMatch string, plan store.Planner) error {
	return st.Calendars().DeleteObjectPlanned(ctx, c.ID, name, ifMatch, plan)
}

// Prepare runs every check Write makes before the store: size, CalDAV validity and Inspect.
// Subscription refreshes use it so mirrored objects pass the same gate as client writes.
func Prepare(calendarID, name string, cal *ical.Calendar, raw []byte) (*store.CalendarObject, error) {
	if len(raw) > calendar.MaxObjectSize {
		return nil, ErrTooLarge
	}
	if _, _, err := caldav.ValidateCalendarObject(cal); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResource, err)
	}
	info, err := calendar.Inspect(cal)
	if err != nil {
		return nil, err
	}
	return &store.CalendarObject{CalendarID: calendarID, Name: name, Component: info.Component, UID: info.UID, Data: raw, FirstStart: info.FirstStart, LastEnd: info.LastEnd}, nil
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

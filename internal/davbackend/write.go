package davbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/tasks"
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
func Write(ctx context.Context, st store.Store, c *store.Calendar, name string, cal *ical.Calendar, raw []byte, ifMatch string, ifNoneMatch bool, lim store.OwnerLimits, plan store.Planner, writerID string) (*store.CalendarObject, error) {
	return WriteMeeting(ctx, st, c, name, cal, raw, ifMatch, ifNoneMatch, lim, plan, writerID, nil)
}

// WriteMeeting is Write that also applies a web edit's video meeting switch (nil keeps it) in
// the same transaction.
func WriteMeeting(ctx context.Context, st store.Store, c *store.Calendar, name string, cal *ical.Calendar, raw []byte, ifMatch string, ifNoneMatch bool, lim store.OwnerLimits, plan store.Planner, writerID string, sw *store.MeetingSwitch) (*store.CalendarObject, error) {
	o, err := Prepare(c.ID, name, cal, raw)
	if err != nil {
		return nil, err
	}
	if !c.Holds(o.Component) {
		return nil, fmt.Errorf("%w: this calendar holds %s", calendar.ErrUnsupportedComponent, strings.Join(c.Components, " and "))
	}
	o.WriterID, o.Meeting = writerID, sw
	var prev *store.CalendarObject
	if o.Component == ical.CompToDo && writerID != "" && !ifNoneMatch {
		prev, _ = st.Calendars().GetObject(ctx, c.ID, name)
	}
	var saved *store.CalendarObject
	err = store.RetryStorageRefresh(ctx, func() error {
		attempt := *o
		if _, err := st.Calendars().PutObjectPlanned(ctx, &attempt, ifMatch, ifNoneMatch, lim, plan); err != nil {
			return err
		}
		saved = &attempt
		return nil
	})
	if err != nil {
		return nil, err
	}
	o = saved
	if o.Component == ical.CompToDo && writerID != "" {
		logCompletion(ctx, st, writerID, c.ID, o.UID, prev, cal)
	}
	return o, nil
}

// logCompletion records a task finished or reopened by this write, whichever client wrote it,
// for the writer's streaks and insights. The log is a record, not the task: a failure never
// fails the write.
func logCompletion(ctx context.Context, st store.Store, writerID, calendarID, uid string, prev *store.CalendarObject, next *ical.Calendar) {
	var before *ical.Calendar
	if prev != nil {
		before, _ = ical.NewDecoder(bytes.NewReader(prev.Data)).Decode()
	}
	ch, ok := tasks.Change(before, next, time.Now())
	switch {
	case !ok:
	case ch.Done:
		_ = st.Tasks().LogCompletion(ctx, store.TaskCompletion{UserID: writerID, CalendarID: calendarID, UID: uid, Occurrence: ch.Occurrence, At: ch.At})
	default:
		_ = st.Tasks().UndoCompletion(ctx, writerID, calendarID, uid, ch.Occurrence)
	}
}

// Delete is the one delete path for calendar objects, with plan run inside its transaction and
// the messages it queues bounded by lim.
func Delete(ctx context.Context, st store.Store, c *store.Calendar, name, ifMatch string, lim store.OwnerLimits, plan store.Planner) error {
	return store.RetryStorageRefresh(ctx, func() error {
		return st.Calendars().DeleteObjectPlanned(ctx, c.ID, name, ifMatch, lim, plan)
	})
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

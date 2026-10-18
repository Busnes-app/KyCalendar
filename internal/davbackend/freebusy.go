package davbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

// One free-busy-query is bounded like GET /api/events.
const (
	maxFreeBusyRange     = 400 * 24 * time.Hour
	maxFreeBusyInstances = 5000
	maxFreeBusyTime      = 2 * time.Second
)

var errFreeBusyTooBig = webdav.NewHTTPError(http.StatusForbidden, errors.New("free-busy range too large: ask for a shorter one"))

// QueryFreeBusy answers CALDAV:free-busy-query on a calendar the user can read. Floating times
// and dates are read as UTC: a calendar has no time zone of its own.
func (b *Backend) QueryFreeBusy(ctx context.Context, p string, start, end time.Time) ([]caldav.FreeBusyPeriod, error) {
	_, c, _, name, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	if name != "" {
		return nil, webdav.NewHTTPError(http.StatusForbidden, errors.New("free-busy-query targets a calendar"))
	}
	if end.Sub(start) > maxFreeBusyRange {
		return nil, errFreeBusyTooBig
	}
	if !c.Holds(ical.CompEvent) {
		return nil, nil
	}
	objs, err := b.Store.Calendars().ListObjectsInRange(ctx, c.ID, ical.CompEvent, start.Unix(), end.Unix())
	if err != nil {
		return nil, err
	}
	begun := time.Now()
	var out []caldav.FreeBusyPeriod
	for _, o := range objs {
		if time.Since(begun) > maxFreeBusyTime {
			return nil, errFreeBusyTooBig
		}
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			return nil, fmt.Errorf("stored object %s/%s does not parse: %w", c.ID, o.Name, err)
		}
		busy, err := calendar.BusyTimes(cal, start, end, time.UTC, maxFreeBusyInstances-len(out))
		if errors.Is(err, calendar.ErrTooManyInstances) {
			return nil, errFreeBusyTooBig
		}
		if err != nil {
			return nil, err
		}
		for _, bt := range busy {
			typ := caldav.FreeBusy
			if bt.Tentative {
				typ = caldav.FreeBusyTentative
			}
			out = append(out, caldav.FreeBusyPeriod{Start: bt.Start, End: bt.End, Type: typ})
		}
	}
	return out, nil
}

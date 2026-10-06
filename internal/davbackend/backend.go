// Package davbackend maps CalDAV onto the calendar store for one authenticated user.
package davbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const Prefix = "/dav"

const (
	ownerUser   = "user"
	defaultSlug = "default"
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Backend is built per request; User is the authenticated, active, non-admin user.
type Backend struct {
	Store             store.Store
	User              *store.User
	MaxObjectsPerUser int
}

type ifMatchKey struct{}

// WithIfMatch carries a DELETE If-Match header, which go-webdav does not pass to backends.
func WithIfMatch(ctx context.Context, etag string) context.Context {
	etag = strings.Trim(etag, `"`)
	if etag == "*" {
		etag = "" // any current version; DeleteObject already 404s a missing one
	}
	return context.WithValue(ctx, ifMatchKey{}, etag)
}

func ifMatchFrom(ctx context.Context) string {
	v, _ := ctx.Value(ifMatchKey{}).(string)
	return v
}

func (b *Backend) principal() string { return Prefix + "/" + b.User.ID + "/" }
func (b *Backend) home() string      { return b.principal() + "calendars/" }

func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return b.principal(), nil
}
func (b *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) { return b.home(), nil }

// split parses "<home><slug>/<name>"; name is empty for the calendar itself.
func (b *Backend) split(p string) (slug, name string, err error) {
	rest, ok := strings.CutPrefix(p, b.home())
	if !ok {
		return "", "", webdav.NewHTTPError(http.StatusForbidden, errors.New("path outside the user's calendars"))
	}
	slug, name, _ = strings.Cut(rest, "/")
	if !slugPattern.MatchString(slug) || strings.Contains(name, "/") {
		return "", "", webdav.NewHTTPError(http.StatusNotFound, errors.New("no such calendar"))
	}
	return slug, name, nil
}

func (b *Backend) ensureDefault(ctx context.Context) error {
	cals, err := b.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, b.User.ID)
	if err != nil || len(cals) > 0 {
		return err
	}
	err = b.Store.Calendars().CreateCalendar(ctx, &store.Calendar{
		ID: "cal_" + uuid.NewString(), OwnerKind: ownerUser, OwnerID: b.User.ID,
		Slug: defaultSlug, Name: "Calendar",
	})
	if errors.Is(err, store.ErrAlreadyExists) {
		return nil
	}
	return err
}

func (b *Backend) toDAV(ctx context.Context, c *store.Calendar) (caldav.Calendar, error) {
	epoch, err := b.Store.Calendars().SyncEpoch(ctx)
	if err != nil {
		return caldav.Calendar{}, err
	}
	return caldav.Calendar{
		Path:                  b.home() + c.Slug + "/",
		Name:                  c.Name,
		Description:           c.Description,
		Color:                 c.Color,
		CTag:                  strconv.FormatInt(c.Seq, 10),
		SyncToken:             calendar.FormatSyncToken(epoch, c.Seq),
		MaxResourceSize:       calendar.MaxObjectSize,
		SupportedComponentSet: []string{ical.CompEvent},
	}, nil
}

func (b *Backend) calendar(ctx context.Context, slug string) (*store.Calendar, error) {
	c, err := b.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, b.User.ID, slug)
	if errors.Is(err, store.ErrNotFound) && slug == defaultSlug {
		// A client may write to the default calendar before it ever lists calendars.
		if err := b.ensureDefault(ctx); err != nil {
			return nil, err
		}
		c, err = b.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, b.User.ID, slug)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	return c, err
}

func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	if err := b.ensureDefault(ctx); err != nil {
		return nil, err
	}
	cals, err := b.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, b.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.Calendar, 0, len(cals))
	for _, c := range cals {
		dc, err := b.toDAV(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	return out, nil
}

func (b *Backend) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	slug, _, err := b.split(p)
	if err != nil {
		return nil, err
	}
	c, err := b.calendar(ctx, slug)
	if err != nil {
		return nil, err
	}
	dc, err := b.toDAV(ctx, c)
	return &dc, err
}

func (b *Backend) CreateCalendar(ctx context.Context, cal *caldav.Calendar) error {
	slug, name, err := b.split(cal.Path)
	if err != nil || name != "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("calendars live directly under the home set"))
	}
	display := cal.Name
	if display == "" {
		display = slug
	}
	err = b.Store.Calendars().CreateCalendar(ctx, &store.Calendar{
		ID: "cal_" + uuid.NewString(), OwnerKind: ownerUser, OwnerID: b.User.ID,
		Slug: slug, Name: display, Description: cal.Description, Color: cal.Color,
	})
	if errors.Is(err, store.ErrAlreadyExists) {
		return webdav.NewHTTPError(http.StatusMethodNotAllowed, err)
	}
	return err
}

func (b *Backend) UpdateCalendar(ctx context.Context, p string, u *caldav.CalendarUpdate) error {
	slug, _, err := b.split(p)
	if err != nil {
		return err
	}
	c, err := b.calendar(ctx, slug)
	if err != nil {
		return err
	}
	return b.Store.Calendars().UpdateCalendar(ctx, c.ID, u.Name, u.Description, u.Color)
}

func (b *Backend) toObject(slug string, o *store.CalendarObject) (*caldav.CalendarObject, error) {
	data, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return nil, fmt.Errorf("stored object %s/%s does not parse: %w", slug, o.Name, err)
	}
	return &caldav.CalendarObject{
		Path: b.home() + slug + "/" + o.Name, ModTime: o.ModifiedAt, ContentLength: int64(len(o.Data)),
		ETag: o.ETag, Data: data, Raw: o.Data,
	}, nil
}

func (b *Backend) objectAt(ctx context.Context, p string) (string, *store.Calendar, string, error) {
	slug, name, err := b.split(p)
	if err != nil {
		return "", nil, "", err
	}
	c, err := b.calendar(ctx, slug)
	return slug, c, name, err
}

func (b *Backend) GetCalendarObject(ctx context.Context, p string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	slug, c, name, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	o, err := b.Store.Calendars().GetObject(ctx, c.ID, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	if err != nil {
		return nil, err
	}
	return b.toObject(slug, o)
}

func (b *Backend) convert(slug string, list []*store.CalendarObject) ([]caldav.CalendarObject, error) {
	out := make([]caldav.CalendarObject, 0, len(list))
	for _, o := range list {
		co, err := b.toObject(slug, o)
		if err != nil {
			return nil, err
		}
		out = append(out, *co)
	}
	return out, nil
}

func (b *Backend) ListCalendarObjects(ctx context.Context, p string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	slug, c, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	list, err := b.Store.Calendars().ListObjects(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return b.convert(slug, list)
}

func (b *Backend) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	slug, c, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	var list []*store.CalendarObject
	if r, ok := eventRange(q); ok {
		list, err = b.Store.Calendars().ListObjectsInRange(ctx, c.ID, r[0], r[1])
	} else {
		list, err = b.Store.Calendars().ListObjects(ctx, c.ID)
	}
	if err != nil {
		return nil, err
	}
	out := make([]caldav.CalendarObject, 0, len(list))
	for _, o := range list {
		co, err := b.toObject(slug, o)
		if err != nil {
			return nil, err
		}
		// Unbounded objects are never expanded: the index already counts them as overlapping.
		// Keep an object when matching errors (e.g. an unknown TZID): an extra result is harmless, a missing one is not.
		if o.LastEnd == nil {
			out = append(out, *co)
		} else if ok, err := caldav.Match(q.CompFilter, co); ok || err != nil {
			out = append(out, *co)
		}
	}
	return out, nil
}

// eventRange returns the VEVENT time-range of a query as unix seconds, if it has one.
func eventRange(q *caldav.CalendarQuery) ([2]int64, bool) {
	for _, cf := range q.CompFilter.Comps {
		if cf.Name == ical.CompEvent && !cf.Start.IsZero() {
			end := int64(1 << 62)
			if !cf.End.IsZero() {
				end = cf.End.Unix()
			}
			return [2]int64{cf.Start.Unix(), end}, true
		}
	}
	return [2]int64{}, false
}

func (b *Backend) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	slug, c, name, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, webdav.NewHTTPError(http.StatusMethodNotAllowed, errors.New("PUT needs an object name"))
	}
	if _, _, err := caldav.ValidateCalendarObject(cal); err != nil {
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarObjectResource)
	}
	info, err := calendar.Inspect(cal)
	switch {
	case errors.Is(err, calendar.ErrUnsupportedComponent):
		return nil, caldav.NewPreconditionError(caldav.PreconditionSupportedCalendarComponent)
	case err != nil:
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarData)
	}
	if _, err := b.Store.Calendars().GetObject(ctx, c.ID, name); errors.Is(err, store.ErrNotFound) {
		n, err := b.Store.Calendars().CountObjectsByOwner(ctx, ownerUser, b.User.ID)
		if err != nil {
			return nil, err
		}
		if n >= b.MaxObjectsPerUser {
			return nil, webdav.NewHTTPError(http.StatusInsufficientStorage, errors.New("calendar quota reached"))
		}
	} else if err != nil {
		return nil, err
	}

	ifMatch := ""
	if opts.IfMatch.IsSet() && !opts.IfMatch.IsWildcard() {
		if ifMatch, err = opts.IfMatch.ETag(); err != nil {
			return nil, webdav.NewHTTPError(http.StatusBadRequest, err)
		}
	}
	ifNoneMatch := opts.IfNoneMatch.IsSet() && opts.IfNoneMatch.IsWildcard()
	o := &store.CalendarObject{CalendarID: c.ID, Name: name, UID: info.UID, Data: opts.Raw, FirstStart: info.FirstStart, LastEnd: info.LastEnd}
	_, err = b.Store.Calendars().PutObject(ctx, o, ifMatch, ifNoneMatch)
	switch {
	case errors.Is(err, store.ErrPreconditionFailed):
		return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	case errors.Is(err, store.ErrUIDConflict):
		return nil, caldav.NewPreconditionError(caldav.PreconditionNoUIDConflict)
	case err != nil:
		return nil, err
	}
	return &caldav.CalendarObject{Path: b.home() + slug + "/" + name, ETag: o.ETag, ModTime: o.ModifiedAt}, nil
}

func (b *Backend) DeleteCalendarObject(ctx context.Context, p string) error {
	_, c, name, err := b.objectAt(ctx, p)
	if err != nil {
		return err
	}
	if name == "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("calendars cannot be deleted over CalDAV"))
	}
	err = b.Store.Calendars().DeleteObject(ctx, c.ID, name, ifMatchFrom(ctx))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return webdav.NewHTTPError(http.StatusNotFound, err)
	case errors.Is(err, store.ErrPreconditionFailed):
		return webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	}
	return err
}

func (b *Backend) SyncCalendar(ctx context.Context, p, token string) (*caldav.SyncResult, error) {
	slug, c, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	epoch, err := b.Store.Calendars().SyncEpoch(ctx)
	if err != nil {
		return nil, err
	}
	since := int64(0)
	if token != "" {
		tokEpoch, seq, err := calendar.ParseSyncToken(token)
		if err != nil || tokEpoch != epoch || seq > c.Seq {
			return nil, caldav.ErrInvalidSyncToken
		}
		since = seq
	}

	res := &caldav.SyncResult{SyncToken: calendar.FormatSyncToken(epoch, since)}
	if token == "" {
		list, err := b.Store.Calendars().ListObjects(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		if res.Updated, err = b.convert(slug, list); err != nil {
			return nil, err
		}
		res.SyncToken = calendar.FormatSyncToken(epoch, c.Seq)
		return res, nil
	}

	changes, err := b.Store.Calendars().ChangesSince(ctx, c.ID, since)
	if errors.Is(err, store.ErrSyncTokenExpired) {
		return nil, caldav.ErrInvalidSyncToken
	}
	if err != nil {
		return nil, err
	}
	latest := map[string]bool{} // name -> deleted, last change wins
	maxSeq := since
	for _, ch := range changes {
		latest[ch.Name] = ch.Deleted
		maxSeq = ch.Seq
	}
	for name, deleted := range latest {
		path := b.home() + slug + "/" + name
		if deleted {
			res.Deleted = append(res.Deleted, path)
			continue
		}
		o, err := b.Store.Calendars().GetObject(ctx, c.ID, name)
		if errors.Is(err, store.ErrNotFound) {
			res.Deleted = append(res.Deleted, path)
			continue
		}
		if err != nil {
			return nil, err
		}
		co, err := b.toObject(slug, o)
		if err != nil {
			return nil, err
		}
		res.Updated = append(res.Updated, *co)
	}
	res.SyncToken = calendar.FormatSyncToken(epoch, maxSeq)
	return res, nil
}

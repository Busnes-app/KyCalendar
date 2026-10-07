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
	"unicode/utf8"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const Prefix = "/dav"

const (
	ownerUser   = "user"
	ownerGroup  = "group"
	defaultSlug = "default"
	// groupPrefix marks a group calendar in every member's home: "_" + calendar ID. Personal
	// slugs start with a letter or digit, so the two never collide.
	groupPrefix = "_"
)

var groupSegment = regexp.MustCompile(`^_cal_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var (
	errNoCalendar = webdav.NewHTTPError(http.StatusNotFound, errors.New("no such calendar"))
	errReadOnly   = webdav.NewHTTPError(http.StatusForbidden, errors.New("this calendar is read-only for you"))
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// validName accepts any opaque object key: 1-255 bytes of UTF-8, no slash, no control characters, not a dot segment.
func validName(name string) bool {
	if len(name) == 0 || len(name) > 255 || name == "." || name == ".." || !utf8.ValidString(name) {
		return false
	}
	for _, c := range name {
		if c == '/' || c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

var errQuota = webdav.NewHTTPError(http.StatusInsufficientStorage, errors.New("calendar quota reached"))

// Backend is built per request; User is the authenticated, active, non-admin user and Grants
// are every grant reaching User through group membership, loaded for this request.
type Backend struct {
	Store               store.Store
	User                *store.User
	Grants              []store.CalendarGrant
	MaxObjectsPerUser   int
	MaxCalendarsPerUser int
	MaxBytesPerUser     int64
	MaxBytesTotal       int64
}

type ifMatchKey struct{}

// WithIfMatch carries a DELETE If-Match header, which go-webdav does not pass to backends.
func WithIfMatch(ctx context.Context, header string) context.Context {
	return context.WithValue(ctx, ifMatchKey{}, webdav.ConditionalMatch(header))
}

// ifMatchETag returns the store's If-Match: "" for none, "*" for any current version, else the strong ETag.
func ifMatchETag(m webdav.ConditionalMatch) (string, error) {
	if !m.IsSet() || m.IsWildcard() {
		return string(m), nil
	}
	etag, err := m.ETag()
	if err != nil {
		return "", webdav.NewHTTPError(http.StatusBadRequest, err)
	}
	return etag, nil
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
	if !slugPattern.MatchString(slug) && !groupSegment.MatchString(slug) {
		return "", "", webdav.NewHTTPError(http.StatusNotFound, errors.New("no such calendar"))
	}
	if name != "" && !validName(name) {
		return "", "", webdav.NewHTTPError(http.StatusForbidden, errors.New("unsupported object name"))
	}
	return slug, name, nil
}

func (b *Backend) ensureDefault(ctx context.Context) error {
	return EnsureDefault(ctx, b.Store, b.User.ID, b.MaxCalendarsPerUser)
}

func (b *Backend) segment(c *store.Calendar) string {
	if c.OwnerKind == ownerGroup {
		return groupPrefix + c.ID
	}
	return c.Slug
}

func (b *Backend) toDAV(ctx context.Context, c *store.Calendar, role access.Role) (caldav.Calendar, error) {
	epoch, err := b.Store.Calendars().SyncEpoch(ctx)
	if err != nil {
		return caldav.Calendar{}, err
	}
	return caldav.Calendar{
		Path:                  b.home() + b.segment(c) + "/",
		Name:                  c.Name,
		Description:           c.Description,
		Color:                 c.Color,
		CTag:                  strconv.FormatInt(c.Seq, 10),
		SyncToken:             calendar.FormatSyncToken(epoch, c.Seq),
		MaxResourceSize:       calendar.MaxObjectSize,
		SupportedComponentSet: []string{ical.CompEvent},
		ReadOnly:              !role.CanWrite(),
	}, nil
}

// calendar resolves a home segment to a calendar and the user's role on it. A group calendar
// the user cannot read answers 404, exactly like one that does not exist.
func (b *Backend) calendar(ctx context.Context, seg string) (*store.Calendar, access.Role, error) {
	if id, ok := strings.CutPrefix(seg, groupPrefix); ok {
		c, err := b.Store.Calendars().GetCalendarByID(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, access.None, errNoCalendar
		}
		if err != nil {
			return nil, access.None, err
		}
		role := access.Resolve(b.User, c, b.Grants)
		if c.OwnerKind != ownerGroup || !role.CanRead() {
			return nil, access.None, errNoCalendar
		}
		return c, role, nil
	}
	c, err := b.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, b.User.ID, seg)
	if errors.Is(err, store.ErrNotFound) && seg == defaultSlug {
		// A client may write to the default calendar before it ever lists calendars.
		if err := b.ensureDefault(ctx); err != nil {
			return nil, access.None, err
		}
		c, err = b.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, b.User.ID, seg)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, access.None, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	if err != nil {
		return nil, access.None, err
	}
	return c, access.Owner, nil
}

func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	if err := b.ensureDefault(ctx); err != nil {
		return nil, err
	}
	cals, err := b.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, b.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.Calendar, 0, len(cals)+len(b.Grants))
	for _, c := range cals {
		dc, err := b.toDAV(ctx, c, access.Owner)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	seen := map[string]bool{}
	for _, g := range b.Grants {
		if seen[g.CalendarID] {
			continue
		}
		seen[g.CalendarID] = true
		c, role, err := b.calendar(ctx, groupPrefix+g.CalendarID)
		if errors.Is(err, errNoCalendar) {
			continue
		}
		if err != nil {
			return nil, err
		}
		dc, err := b.toDAV(ctx, c, role)
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
	c, role, err := b.calendar(ctx, slug)
	if err != nil {
		return nil, err
	}
	dc, err := b.toDAV(ctx, c, role)
	return &dc, err
}

func (b *Backend) CreateCalendar(ctx context.Context, cal *caldav.Calendar) error {
	slug, name, err := b.split(cal.Path)
	if err != nil || name != "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("calendars live directly under the home set"))
	}
	if strings.HasPrefix(slug, groupPrefix) {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("group calendars are created by an administrator"))
	}
	if err := calendar.CheckProps(&cal.Name, &cal.Description, &cal.Color); err != nil {
		return webdav.NewHTTPError(http.StatusBadRequest, err)
	}
	display := cal.Name
	if display == "" {
		display = slug
	}
	err = b.Store.Calendars().CreateCalendar(ctx, &store.Calendar{
		ID: "cal_" + uuid.NewString(), OwnerKind: ownerUser, OwnerID: b.User.ID,
		Slug: slug, Name: display, Description: cal.Description, Color: cal.Color,
	}, b.MaxCalendarsPerUser)
	switch {
	case errors.Is(err, store.ErrAlreadyExists):
		return webdav.NewHTTPError(http.StatusMethodNotAllowed, err)
	case errors.Is(err, store.ErrQuotaExceeded):
		return errQuota
	}
	return err
}

func (b *Backend) UpdateCalendar(ctx context.Context, p string, u *caldav.CalendarUpdate) error {
	slug, _, err := b.split(p)
	if err != nil {
		return err
	}
	c, role, err := b.calendar(ctx, slug)
	if err != nil {
		return err
	}
	if !role.CanManage() {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("only a manager can change this calendar"))
	}
	// PROPPATCH is atomic: one bad property refuses the whole request.
	if err := calendar.CheckProps(u.Name, u.Description, u.Color); err != nil {
		return webdav.NewHTTPError(http.StatusForbidden, err)
	}
	return b.Store.Calendars().UpdateCalendar(ctx, c.ID, u.Name, u.Description, u.Color)
}

// toObject serves the stored bytes; Data stays nil because the fork writes Raw everywhere.
func (b *Backend) toObject(slug string, o *store.CalendarObject) caldav.CalendarObject {
	return caldav.CalendarObject{
		Path: b.home() + slug + "/" + o.Name, ModTime: o.ModifiedAt, ContentLength: int64(len(o.Data)),
		ETag: o.ETag, Raw: o.Data,
	}
}

func (b *Backend) objectAt(ctx context.Context, p string) (string, *store.Calendar, access.Role, string, error) {
	seg, name, err := b.split(p)
	if err != nil {
		return "", nil, access.None, "", err
	}
	c, role, err := b.calendar(ctx, seg)
	return seg, c, role, name, err
}

func (b *Backend) GetCalendarObject(ctx context.Context, p string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	slug, c, _, name, err := b.objectAt(ctx, p)
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
	co := b.toObject(slug, o)
	return &co, nil
}

func (b *Backend) convert(slug string, list []*store.CalendarObject) []caldav.CalendarObject {
	out := make([]caldav.CalendarObject, 0, len(list))
	for _, o := range list {
		out = append(out, b.toObject(slug, o))
	}
	return out
}

func (b *Backend) ListCalendarObjects(ctx context.Context, p string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	slug, c, _, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	list, err := b.Store.Calendars().ListObjects(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return b.convert(slug, list), nil
}

func (b *Backend) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	slug, c, _, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	var list []*store.CalendarObject
	r, ranged := eventRange(q)
	if ranged {
		list, err = b.Store.Calendars().ListObjectsInRange(ctx, c.ID, r[0], r[1])
	} else {
		list, err = b.Store.Calendars().ListObjects(ctx, c.ID)
	}
	if err != nil {
		return nil, err
	}
	out := make([]caldav.CalendarObject, 0, len(list))
	for _, o := range list {
		co := b.toObject(slug, o)
		// A time-range query never expands a recurring or unbounded object: the index already counts it as overlapping.
		if ranged && (o.LastEnd == nil || bytes.Contains(o.Data, []byte("RRULE")) || bytes.Contains(o.Data, []byte("RDATE"))) {
			out = append(out, co)
			continue
		}
		if co.Data, err = ical.NewDecoder(bytes.NewReader(o.Data)).Decode(); err != nil {
			return nil, fmt.Errorf("stored object %s/%s does not parse: %w", slug, o.Name, err)
		}
		// Keep an object when matching errors (e.g. an unknown TZID): an extra result is harmless, a missing one is not.
		if ok, err := caldav.Match(q.CompFilter, &co); ok || err != nil {
			co.Data = nil
			out = append(out, co)
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
	slug, c, role, name, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, webdav.NewHTTPError(http.StatusMethodNotAllowed, errors.New("PUT needs an object name"))
	}
	if !role.CanWrite() {
		return nil, errReadOnly
	}
	ifMatch, err := ifMatchETag(opts.IfMatch)
	if err != nil {
		return nil, err
	}
	ifNoneMatch := opts.IfNoneMatch.IsSet() && opts.IfNoneMatch.IsWildcard()
	// A group calendar is its own owner, so the per-owner limits apply to each group calendar.
	o, err := Write(ctx, b.Store, c.ID, name, cal, opts.Raw, ifMatch, ifNoneMatch, b.limits())
	switch {
	case errors.Is(err, ErrInvalidResource):
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarObjectResource)
	case errors.Is(err, calendar.ErrUnsupportedComponent):
		return nil, caldav.NewPreconditionError(caldav.PreconditionSupportedCalendarComponent)
	case errors.Is(err, calendar.ErrInvalidData):
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarData)
	case errors.Is(err, store.ErrQuotaExceeded):
		return nil, errQuota
	case errors.Is(err, store.ErrPreconditionFailed):
		return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	case errors.Is(err, store.ErrUIDConflict):
		return nil, caldav.NewPreconditionError(caldav.PreconditionNoUIDConflict)
	case err != nil:
		return nil, err
	}
	return &caldav.CalendarObject{Path: b.home() + slug + "/" + name, ETag: o.ETag, ModTime: o.ModifiedAt}, nil
}

func (b *Backend) limits() store.OwnerLimits {
	return store.OwnerLimits{MaxObjects: b.MaxObjectsPerUser, MaxBytes: b.MaxBytesPerUser, MaxTotalBytes: b.MaxBytesTotal}
}

func (b *Backend) DeleteCalendarObject(ctx context.Context, p string) error {
	_, c, role, name, err := b.objectAt(ctx, p)
	if err != nil {
		return err
	}
	if name == "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("calendars cannot be deleted over CalDAV"))
	}
	if !role.CanWrite() {
		return errReadOnly
	}
	m, _ := ctx.Value(ifMatchKey{}).(webdav.ConditionalMatch)
	ifMatch, err := ifMatchETag(m)
	if err != nil {
		return err
	}
	err = b.Store.Calendars().DeleteObject(ctx, c.ID, name, ifMatch)
	switch {
	case errors.Is(err, store.ErrNotFound) && m.IsSet():
		return webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	case errors.Is(err, store.ErrNotFound):
		return webdav.NewHTTPError(http.StatusNotFound, err)
	case errors.Is(err, store.ErrPreconditionFailed):
		return webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	}
	return err
}

func (b *Backend) SyncCalendar(ctx context.Context, p, token string) (*caldav.SyncResult, error) {
	slug, c, _, _, err := b.objectAt(ctx, p)
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
		res.Updated = b.convert(slug, list)
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
		res.Updated = append(res.Updated, b.toObject(slug, o))
	}
	res.SyncToken = calendar.FormatSyncToken(epoch, maxSeq)
	return res, nil
}

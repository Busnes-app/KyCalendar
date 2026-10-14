package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	maxEventRange     = 400 * 24 * time.Hour
	maxEventInstances = 5000
)

// maxExpandTime bounds the expansion work of one events request; a var so tests can shrink it.
var maxExpandTime = 2 * time.Second

type repeatView struct {
	Freq     string   `json:"freq"`
	Weekdays []string `json:"weekdays,omitempty"`
}

type eventView struct {
	CalendarID   string `json:"calendar_id"`
	UID          string `json:"uid"`
	RecurrenceID string `json:"recurrence_id,omitempty"`
	ETag         string `json:"etag"`
	Title        string `json:"title"`
	Location     string `json:"location,omitempty"`
	Description  string `json:"description,omitempty"`
	// DescriptionTruncated: MCP results cap descriptions; the calendar keeps them whole.
	DescriptionTruncated bool       `json:"description_truncated,omitempty"`
	Start                string     `json:"start"`
	End                  string     `json:"end"`
	SeriesStart          string     `json:"series_start"`
	SeriesEnd            string     `json:"series_end"`
	AllDay               bool       `json:"all_day"`
	Recurring            bool       `json:"recurring"`
	Override             bool       `json:"override,omitempty"`
	Repeat               repeatView `json:"repeat"`
	Floating             bool       `json:"floating,omitempty"`
	UnknownZone          bool       `json:"unknown_zone,omitempty"`
	Partial              bool       `json:"partial,omitempty"`
	Zone                 string     `json:"zone,omitempty"`
	Editable             bool       `json:"editable"`
}

var weekdayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

func repeatViewOf(r calendar.Repeat) repeatView {
	v := repeatView{Freq: r.Freq}
	for _, d := range r.Weekdays {
		v.Weekdays = append(v.Weekdays, weekdayCodes[d])
	}
	return v
}

// viewerZone reads the tz query parameter: an IANA name, default UTC. "Local" is refused: it
// would be the server's zone, not the viewer's.
func viewerZone(r *http.Request) (*time.Location, bool) {
	return zoneNamed(r.URL.Query().Get("tz"))
}

// zoneNamed is the IANA zone name, default UTC; "Local" is refused.
func zoneNamed(name string) (*time.Location, bool) {
	if name == "" {
		return time.UTC, true
	}
	if name == "Local" {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	return loc, err == nil
}

// seriesTimes are the master's first start and end, formatted like an instance's.
type seriesTimes struct{ start, end string }

func instanceView(c *store.Calendar, o *store.CalendarObject, repeat repeatView, zone string, series seriesTimes, in calendar.Instance, viewer *time.Location, role access.Role) eventView {
	text := func(name string) string { v, _ := in.Event.Props.Text(name); return v }
	v := eventView{CalendarID: c.ID, UID: in.UID, RecurrenceID: in.RecurrenceID, ETag: o.ETag,
		Title: text(ical.PropSummary), Location: text(ical.PropLocation), Description: text(ical.PropDescription),
		AllDay: in.AllDay, Recurring: in.Recurring, Override: in.Override, Floating: in.Floating,
		UnknownZone: in.UnknownZone, Partial: in.Partial, Editable: role.CanWrite(), Repeat: repeat, Zone: zone,
		SeriesStart: series.start, SeriesEnd: series.end}
	if in.AllDay {
		v.Start, v.End = in.Start.In(viewer).Format(time.DateOnly), in.End.In(viewer).Format(time.DateOnly)
	} else {
		v.Start, v.End = in.Start.In(viewer).Format(time.RFC3339), in.End.In(viewer).Format(time.RFC3339)
	}
	return v
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	q := r.URL.Query()
	start, err1 := time.Parse(time.RFC3339, q.Get("start"))
	end, err2 := time.Parse(time.RFC3339, q.Get("end"))
	if err1 != nil || err2 != nil || !end.After(start) || end.Sub(start) > maxEventRange {
		s.writeError(w, http.StatusBadRequest, "start and end must be RFC 3339 times, end after start, at most 400 days apart")
		return
	}
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	cals, grants, err := s.visibleCalendars(r.Context(), user)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list calendars")
		return
	}
	if want := q.Get("calendar"); want != "" {
		byID := map[string]*store.Calendar{}
		for _, c := range cals {
			byID[c.ID] = c
		}
		cals = cals[:0]
		seen := map[string]bool{}
		for _, id := range strings.Split(want, ",") {
			if seen[id] {
				continue
			}
			seen[id] = true
			c, ok := byID[id]
			if !ok {
				s.writeError(w, http.StatusNotFound, "No such calendar")
				return
			}
			cals = append(cals, c)
		}
	}
	out, apiErr := s.expandEvents(r.Context(), user, cals, grants, start, end, viewer)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// apiError is a failure the REST handlers write as JSON and the MCP tools return as text.
type apiError struct {
	status    int
	msg, code string
}

func (e *apiError) Error() string { return e.msg }

func (s *Server) writeAPIError(w http.ResponseWriter, e *apiError) {
	if e.code == "" {
		s.writeError(w, e.status, e.msg)
		return
	}
	s.writeJSON(w, e.status, map[string]string{"error": e.msg, "code": e.code})
}

// expandEvents expands every instance of cals between start and end, as viewer sees them.
func (s *Server) expandEvents(ctx context.Context, user *store.User, cals []*store.Calendar, grants []store.CalendarGrant, start, end time.Time, viewer *time.Location) ([]eventView, *apiError) {
	begun := time.Now()
	out := []eventView{}
	tooSlow := func() bool {
		if time.Since(begun) <= maxExpandTime {
			return false
		}
		return true
	}
	for _, c := range cals {
		if tooSlow() {
			return nil, errTooSlow
		}
		role := access.Resolve(user, c, grants)
		objs, err := s.store.Calendars().ListObjectsInRange(ctx, c.ID, start.Unix(), end.Unix())
		if err != nil {
			return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load events"}
		}
		for _, o := range objs {
			cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
			if err != nil {
				return nil, &apiError{status: http.StatusInternalServerError, msg: "A stored event does not parse"}
			}
			insts, err := calendar.Expand(cal, start, end, viewer, maxEventInstances-len(out))
			if errors.Is(err, calendar.ErrTooManyInstances) {
				return nil, &apiError{status: http.StatusUnprocessableEntity, msg: "Too many events in this range; choose a shorter range", code: "too_many_instances"}
			}
			if err != nil {
				return nil, &apiError{status: http.StatusInternalServerError, msg: "A stored event does not expand"}
			}
			if tooSlow() {
				return nil, errTooSlow
			}
			var repeat repeatView
			var zone string
			var series seriesTimes
			if master, _ := calendar.Master(cal); master != nil {
				repeat = repeatViewOf(calendar.RepeatOf(master))
				if p := master.Props.Get(ical.PropDateTimeStart); p != nil {
					zone = p.Params.Get(ical.ParamTimezoneID)
				}
				if ss, se, sAllDay, err := calendar.SeriesTimes(master, viewer); err == nil {
					layout := time.RFC3339
					if sAllDay {
						layout = time.DateOnly
					}
					series = seriesTimes{ss.Format(layout), se.Format(layout)}
				}
			}
			for _, in := range insts {
				out = append(out, instanceView(c, o, repeat, zone, series, in, viewer, role))
			}
		}
	}
	return out, nil
}

var errTooSlow = &apiError{status: http.StatusUnprocessableEntity, msg: "This range takes too long to show; choose a shorter range", code: "too_many_instances"}

const (
	maxTitleBytes       = 1000
	maxLocationBytes    = 1000
	maxDescriptionBytes = 65536
)

type eventBody struct {
	Title        string     `json:"title"`
	Location     string     `json:"location"`
	Description  string     `json:"description"`
	Start        string     `json:"start"`
	End          string     `json:"end"`
	AllDay       bool       `json:"all_day"`
	Zone         string     `json:"zone"`
	Repeat       repeatView `json:"repeat"`
	Scope        string     `json:"scope"`
	RecurrenceID string     `json:"recurrence_id"`
}

var errBadEvent = errors.New("bad event")

// checkText bounds an event's text and refuses control characters other than newline and tab.
func checkText(title, location, description string) error {
	if len(title) > maxTitleBytes || len(location) > maxLocationBytes || len(description) > maxDescriptionBytes {
		return fmt.Errorf("%w: text too long", errBadEvent)
	}
	for _, text := range []string{title, location, description} {
		if strings.ContainsFunc(text, func(c rune) bool { return unicode.IsControl(c) && c != '\n' && c != '\t' }) {
			return fmt.Errorf("%w: text contains control characters", errBadEvent)
		}
	}
	return nil
}

// eventInput validates a body at the boundary. allowCustom is true for edits, where "custom"
// keeps the stored rule.
func eventInput(b eventBody, allowCustom bool) (calendar.EventInput, error) {
	in := calendar.EventInput{Title: b.Title, Location: b.Location, Description: b.Description, AllDay: b.AllDay}
	if err := checkText(b.Title, b.Location, b.Description); err != nil {
		return in, err
	}
	if b.AllDay {
		s, err1 := time.Parse(time.DateOnly, b.Start)
		e, err2 := time.Parse(time.DateOnly, b.End)
		if err1 != nil || err2 != nil || !e.After(s) {
			return in, fmt.Errorf("%w: all-day start and end must be dates, end after start", errBadEvent)
		}
		if !yearsInRange(s, e) {
			return in, fmt.Errorf("%w: years must be 1900 to 9000", errBadEvent)
		}
		in.Start, in.End = s, e
	} else {
		if b.Zone == "" || b.Zone == "Local" {
			return in, fmt.Errorf("%w: zone must be an IANA time zone", errBadEvent)
		}
		loc, err := time.LoadLocation(b.Zone)
		if err != nil {
			return in, fmt.Errorf("%w: zone must be an IANA time zone", errBadEvent)
		}
		s, err1 := time.Parse(time.RFC3339, b.Start)
		e, err2 := time.Parse(time.RFC3339, b.End)
		if err1 != nil || err2 != nil || e.Before(s) {
			return in, fmt.Errorf("%w: start and end must be RFC 3339, end not before start", errBadEvent)
		}
		if !yearsInRange(s, e) {
			return in, fmt.Errorf("%w: years must be 1900 to 9000", errBadEvent)
		}
		in.Start, in.End, in.Zone = s, e, loc
	}
	switch b.Repeat.Freq {
	case "", "daily", "monthly", "yearly":
		if len(b.Repeat.Weekdays) > 0 {
			return in, fmt.Errorf("%w: weekdays only apply to weekly", errBadEvent)
		}
	case "weekly":
		for _, code := range b.Repeat.Weekdays {
			i := slices.Index(weekdayCodes, code)
			if i < 0 || slices.Contains(in.Repeat.Weekdays, time.Weekday(i)) {
				return in, fmt.Errorf("%w: weekdays must be distinct MO..SU", errBadEvent)
			}
			in.Repeat.Weekdays = append(in.Repeat.Weekdays, time.Weekday(i))
		}
	case "custom":
		if !allowCustom {
			return in, fmt.Errorf("%w: custom repeat cannot be created here", errBadEvent)
		}
	default:
		return in, fmt.Errorf("%w: unknown repeat", errBadEvent)
	}
	in.Repeat.Freq = b.Repeat.Freq
	return in, nil
}

func yearsInRange(ts ...time.Time) bool {
	for _, t := range ts {
		if y := t.Year(); y < 1900 || y > 9000 {
			return false
		}
	}
	return true
}

// ifMatch reads exactly one strong entity tag, "<etag>": present reports whether the header was
// sent, ok whether it is well formed. A bare, weak, wildcard, empty or listed value is not ok,
// because the store reads "" and "*" as no precondition.
func ifMatch(r *http.Request) (etag string, present, ok bool) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" && len(r.Header.Values("If-Match")) == 0 {
		return "", false, false
	}
	if len(v) < 3 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", true, false
	}
	etag = v[1 : len(v)-1]
	if etag == "*" || strings.ContainsFunc(etag, func(c rune) bool { return c == '"' || c == ',' || unicode.IsSpace(c) }) {
		return "", true, false
	}
	return etag, true, true
}

// writeEvent stores cal through the one write path and writes any error as JSON.
func (s *Server) writeEvent(w http.ResponseWriter, r *http.Request, c *store.Calendar, name string, cal *ical.Calendar, etag string, create bool) (*store.CalendarObject, bool) {
	o, apiErr := s.saveEvent(r.Context(), c, name, cal, etag, create)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return nil, false
	}
	return o, true
}

// saveEvent stores cal through the one write path and maps its errors.
func (s *Server) saveEvent(ctx context.Context, c *store.Calendar, name string, cal *ical.Calendar, etag string, create bool) (*store.CalendarObject, *apiError) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "The event cannot be encoded"}
	}
	o, err := davbackend.Write(ctx, s.store, c.ID, name, cal, buf.Bytes(), etag, create, s.objectLimits())
	switch {
	case err == nil:
		return o, nil
	case errors.Is(err, store.ErrPreconditionFailed):
		return nil, errEventChanged
	case errors.Is(err, davbackend.ErrTooLarge):
		return nil, &apiError{status: http.StatusRequestEntityTooLarge, msg: "The event is too large", code: "too_large"}
	case errors.Is(err, store.ErrNotFound):
		return nil, &apiError{status: http.StatusNotFound, msg: "No such calendar"}
	case errors.Is(err, store.ErrUIDConflict):
		return nil, &apiError{status: http.StatusConflict, msg: "An event with this UID already exists"}
	case errors.Is(err, store.ErrQuotaExceeded):
		return nil, &apiError{status: http.StatusInsufficientStorage, msg: "Calendar storage is full", code: "quota"}
	case errors.Is(err, davbackend.ErrInvalidResource), errors.Is(err, calendar.ErrInvalidData), errors.Is(err, calendar.ErrUnsupportedComponent):
		return nil, &apiError{status: http.StatusUnprocessableEntity, msg: "The event is not valid"}
	default:
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to save the event"}
	}
}

var errEventChanged = &apiError{status: http.StatusPreconditionFailed, msg: "The event changed elsewhere; reload it", code: "conflict"}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	if !role.CanWrite() {
		s.writeError(w, http.StatusForbidden, "This calendar is read-only for you")
		return
	}
	var b eventBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	in, err := eventInput(b, false)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	uid := uuid.NewString()
	o, ok := s.writeEvent(w, r, c, uid+".ics", calendar.NewEvent(uid, in, time.Now()), "", true)
	if !ok {
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]string{"calendar_id": c.ID, "uid": uid, "etag": o.ETag})
}

// loadEvent authorizes a write and loads the event's object and decoded calendar.
func (s *Server) loadEvent(w http.ResponseWriter, r *http.Request) (*store.Calendar, *store.CalendarObject, *ical.Calendar, string, bool) {
	c, role := s.calendarFor(w, r, r.PathValue("cal"))
	if c == nil {
		return nil, nil, nil, "", false
	}
	if !role.CanWrite() {
		s.writeError(w, http.StatusForbidden, "This calendar is read-only for you")
		return nil, nil, nil, "", false
	}
	etag, present, ok := ifMatch(r)
	if !present {
		s.writeError(w, http.StatusPreconditionRequired, "If-Match with the event's ETag is required")
		return nil, nil, nil, "", false
	}
	if !ok {
		s.writeError(w, http.StatusBadRequest, "If-Match must be one strong ETag")
		return nil, nil, nil, "", false
	}
	o, cal, apiErr := s.loadObject(r.Context(), c, r.PathValue("uid"))
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return nil, nil, nil, "", false
	}
	return c, o, cal, etag, true
}

// loadObject loads event uid of c and decodes it.
func (s *Server) loadObject(ctx context.Context, c *store.Calendar, uid string) (*store.CalendarObject, *ical.Calendar, *apiError) {
	o, err := s.store.Calendars().GetObjectByUID(ctx, c.ID, uid)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, &apiError{status: http.StatusNotFound, msg: "No such event"}
	}
	if err != nil {
		return nil, nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load the event"}
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return nil, nil, &apiError{status: http.StatusInternalServerError, msg: "The stored event does not parse"}
	}
	return o, cal, nil
}

func (s *Server) editError(w http.ResponseWriter, err error) {
	s.writeAPIError(w, editFailure(err))
}

func editFailure(err error) *apiError {
	switch {
	case errors.Is(err, calendar.ErrNotRecurring):
		return &apiError{status: http.StatusBadRequest, msg: "This event does not repeat"}
	case errors.Is(err, calendar.ErrNoSuchOccurrence):
		return &apiError{status: http.StatusBadRequest, msg: "No such occurrence"}
	default:
		return &apiError{status: http.StatusUnprocessableEntity, msg: "The stored event cannot be edited"}
	}
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	c, o, cal, etag, ok := s.loadEvent(w, r)
	if !ok {
		return
	}
	var b eventBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if apiErr := applyEdit(cal, b); apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	if saved, ok := s.writeEvent(w, r, c, o.Name, cal, etag, false); ok {
		s.writeJSON(w, http.StatusOK, map[string]string{"etag": saved.ETag})
	}
}

// applyEdit validates b and applies it to cal, to the whole series or one occurrence.
func applyEdit(cal *ical.Calendar, b eventBody) *apiError {
	in, err := eventInput(b, true)
	if err != nil {
		return &apiError{status: http.StatusBadRequest, msg: err.Error()}
	}
	switch b.Scope {
	case "", "all":
		err = calendar.EditAll(cal, in, time.Now())
	case "this":
		err = calendar.EditOne(cal, b.RecurrenceID, in, time.Now())
	default:
		return &apiError{status: http.StatusBadRequest, msg: "scope must be this or all"}
	}
	if err != nil {
		return editFailure(err)
	}
	return nil
}

func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	c, o, cal, etag, ok := s.loadEvent(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	switch q.Get("scope") {
	case "", "all":
		err := s.store.Calendars().DeleteObject(r.Context(), c.ID, o.Name, etag)
		switch {
		case errors.Is(err, store.ErrPreconditionFailed):
			s.writeAPIError(w, errEventChanged)
		case errors.Is(err, store.ErrNotFound):
			s.writeError(w, http.StatusNotFound, "No such event")
		case err != nil:
			s.writeError(w, http.StatusInternalServerError, "Failed to delete the event")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	case "this":
		if err := calendar.DeleteOne(cal, q.Get("recurrence_id"), time.Now()); err != nil {
			s.editError(w, err)
			return
		}
		if saved, ok := s.writeEvent(w, r, c, o.Name, cal, etag, false); ok {
			s.writeJSON(w, http.StatusOK, map[string]string{"etag": saved.ETag})
		}
	default:
		s.writeError(w, http.StatusBadRequest, "scope must be this or all")
	}
}

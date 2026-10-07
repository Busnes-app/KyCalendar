package api

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
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
	CalendarID   string     `json:"calendar_id"`
	UID          string     `json:"uid"`
	RecurrenceID string     `json:"recurrence_id,omitempty"`
	ETag         string     `json:"etag"`
	Title        string     `json:"title"`
	Location     string     `json:"location,omitempty"`
	Description  string     `json:"description,omitempty"`
	Start        string     `json:"start"`
	End          string     `json:"end"`
	AllDay       bool       `json:"all_day"`
	Recurring    bool       `json:"recurring"`
	Override     bool       `json:"override,omitempty"`
	Repeat       repeatView `json:"repeat"`
	Floating     bool       `json:"floating,omitempty"`
	UnknownZone  bool       `json:"unknown_zone,omitempty"`
	Partial      bool       `json:"partial,omitempty"`
	Zone         string     `json:"zone,omitempty"`
	Editable     bool       `json:"editable"`
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
	name := r.URL.Query().Get("tz")
	if name == "" {
		return time.UTC, true
	}
	if name == "Local" {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	return loc, err == nil
}

func instanceView(c *store.Calendar, o *store.CalendarObject, master *ical.Component, in calendar.Instance, viewer *time.Location, role access.Role) eventView {
	text := func(name string) string { v, _ := in.Event.Props.Text(name); return v }
	v := eventView{CalendarID: c.ID, UID: in.UID, RecurrenceID: in.RecurrenceID, ETag: o.ETag,
		Title: text(ical.PropSummary), Location: text(ical.PropLocation), Description: text(ical.PropDescription),
		AllDay: in.AllDay, Recurring: in.Recurring, Override: in.Override, Floating: in.Floating,
		UnknownZone: in.UnknownZone, Partial: in.Partial, Editable: role.CanWrite()}
	if master != nil {
		v.Repeat = repeatViewOf(calendar.RepeatOf(master))
		if p := master.Props.Get(ical.PropDateTimeStart); p != nil {
			v.Zone = p.Params.Get(ical.ParamTimezoneID)
		}
	}
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
		for _, id := range strings.Split(want, ",") {
			c, ok := byID[id]
			if !ok {
				s.writeError(w, http.StatusNotFound, "No such calendar")
				return
			}
			cals = append(cals, c)
		}
	}
	begun := time.Now()
	out := []eventView{}
	for _, c := range cals {
		role := access.Resolve(user, c, grants)
		objs, err := s.store.Calendars().ListObjectsInRange(r.Context(), c.ID, start.Unix(), end.Unix())
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to load events")
			return
		}
		for _, o := range objs {
			cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "A stored event does not parse")
				return
			}
			insts, err := calendar.Expand(cal, start, end, viewer, maxEventInstances-len(out))
			if errors.Is(err, calendar.ErrTooManyInstances) {
				s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Too many events in this range; choose a shorter range", "code": "too_many_instances"})
				return
			}
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "A stored event does not expand")
				return
			}
			if time.Since(begun) > maxExpandTime {
				s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "This range takes too long to show; choose a shorter range", "code": "too_many_instances"})
				return
			}
			master, _ := calendar.Master(cal)
			for _, in := range insts {
				out = append(out, instanceView(c, o, master, in, viewer, role))
			}
		}
	}
	s.writeJSON(w, http.StatusOK, out)
}

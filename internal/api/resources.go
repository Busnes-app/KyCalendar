package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/resource"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// maxResourceRange bounds a busy or free query.
const maxResourceRange = 62 * 24 * time.Hour

// resourceRef is a resource on an event, with KyCalendar's answer for it.
type resourceRef struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Location string `json:"location,omitempty"`
	Status   string `json:"status"`           // accepted, pending or declined
	Reason   string `json:"reason,omitempty"` // why it was declined (resource.Reason*)
	Detail   string `json:"detail,omitempty"` // conflicting dates, last bookable date or longest booking
}

func answerOf(partstat string) string {
	switch partstat {
	case "ACCEPTED":
		return resource.Accepted
	case "DECLINED":
		return resource.Declined
	}
	return resource.Pending
}

// resourceDescriber fills in resource names and declined reasons, loading each once per request.
type resourceDescriber struct {
	s        *Server
	defs     map[string]*resource.Resource
	bookings map[string]map[string]map[string]*store.ResourceBooking // calendar -> object -> resource
}

func (s *Server) resourceDescriber() *resourceDescriber {
	return &resourceDescriber{s: s, bookings: map[string]map[string]map[string]*store.ResourceBooking{}}
}

func (d *resourceDescriber) describe(ctx context.Context, calendarID, name string, refs []resourceRef) error {
	if len(refs) == 0 {
		return nil
	}
	if d.defs == nil {
		rs, err := d.s.store.Resources().ListResources(ctx)
		if err != nil {
			return err
		}
		d.defs = map[string]*resource.Resource{}
		for _, r := range rs {
			d.defs[r.ID] = r
		}
	}
	if d.bookings[calendarID] == nil {
		b, err := d.s.store.Resources().BookingsIn(ctx, calendarID)
		if err != nil {
			return err
		}
		d.bookings[calendarID] = b
	}
	for i := range refs {
		if r := d.defs[refs[i].ID]; r != nil {
			refs[i].Name, refs[i].Kind, refs[i].Location = r.Name, r.Kind, r.Location
		}
		if b := d.bookings[calendarID][name][refs[i].ID]; b != nil && b.Status == resource.Declined {
			refs[i].Reason, refs[i].Detail = b.Reason, b.Detail
		}
	}
	return nil
}

var errNotResourceOrganizer = &apiError{status: http.StatusForbidden, msg: "Only the organizer can change the rooms", code: "not_organizer"}

// applyPeople applies a body's attendees and resources to cal. Changing attendees keeps the
// resources unless the body names them too.
func (s *Server) applyPeople(ctx context.Context, cal *ical.Calendar, b eventBody) *apiError {
	keep := resource.IDs(cal)
	if apiErr := s.applyAttendees(ctx, cal, b.Attendees); apiErr != nil {
		return apiErr
	}
	if b.Resources == nil && (b.Attendees == nil || len(keep) == 0) {
		return nil
	}
	ids := keep
	if b.Resources != nil {
		ids = nil
		for _, id := range *b.Resources {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) > resource.MaxPerEvent {
		return &apiError{status: http.StatusBadRequest, msg: "An event can book at most 10 rooms and equipment", code: "too_many_resources"}
	}
	user := sessionUser(ctx)
	bookable, err := s.store.Resources().Bookable(ctx, user.ID)
	if err != nil {
		return &apiError{status: http.StatusInternalServerError, msg: "Failed to load the rooms"}
	}
	defs := map[string]*resource.Resource{}
	for _, r := range bookable {
		defs[r.ID] = r
	}
	for _, id := range ids {
		if defs[id] == nil && !slices.Contains(keep, id) {
			return &apiError{status: http.StatusBadRequest, msg: "That room or equipment cannot be booked", code: "unknown_resource"}
		}
	}
	self, _, err := davbackend.Address(ctx, s.store, user)
	if err != nil {
		return &apiError{status: http.StatusInternalServerError, msg: "Failed to look up your address"}
	}
	if err := resource.SetList(cal, self, ids, defs); errors.Is(err, resource.ErrNotOrganizer) {
		return errNotResourceOrganizer
	} else if err != nil {
		return &apiError{status: http.StatusInternalServerError, msg: "The rooms cannot be changed"}
	}
	return nil
}

type bookableView struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Location string   `json:"location"`
	Capacity int      `json:"capacity"`
	Tags     []string `json:"tags"`
	Approval string   `json:"approval"`
	Address  string   `json:"address"`
	Free     *bool    `json:"free,omitempty"`
}

type spanView struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Pending bool   `json:"pending,omitempty"`
}

// resourceRange reads start and end (RFC 3339, at most 62 days); ok false has written 400.
func (s *Server) resourceRange(w http.ResponseWriter, r *http.Request, required bool) (from, to int64, set, ok bool) {
	q := r.URL.Query()
	if q.Get("start") == "" && q.Get("end") == "" && !required {
		return 0, 0, false, true
	}
	start, err1 := time.Parse(time.RFC3339, q.Get("start"))
	end, err2 := time.Parse(time.RFC3339, q.Get("end"))
	if err1 != nil || err2 != nil || !end.After(start) || end.Sub(start) > maxResourceRange {
		s.writeError(w, http.StatusBadRequest, "start and end must be RFC 3339 times, end after start, at most 62 days apart")
		return 0, 0, false, false
	}
	return start.Unix(), end.Unix(), true, true
}

func (s *Server) resourceReadAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.allowAccountAttempt("resources:"+sessionUser(r.Context()).ID, 120, time.Minute) {
		s.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many room requests; wait a minute", "code": "rate_limited"})
		return false
	}
	return true
}

// handleBookableResources is the room finder: GET /api/resources?start&end&calendar&uid lists the
// enabled resources the caller may book, each free or not over [start, end) leaving out the
// bookings of event uid in calendar.
func (s *Server) handleBookableResources(w http.ResponseWriter, r *http.Request) {
	if !s.resourceReadAllowed(w, r) {
		return
	}
	from, to, set, ok := s.resourceRange(w, r, false)
	if !ok {
		return
	}
	rs, err := s.store.Resources().Bookable(r.Context(), sessionUser(r.Context()).ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list rooms and equipment")
		return
	}
	out := make([]bookableView, 0, len(rs))
	for _, res := range rs {
		v := bookableView{ID: res.ID, Kind: res.Kind, Name: res.Name, Location: res.Location, Capacity: res.Capacity,
			Tags: res.Tags, Approval: res.Approval, Address: resource.Address(res.ID).Email()}
		if set {
			busy, err := s.store.Resources().Busy(r.Context(), res.ID, from, to, r.URL.Query().Get("calendar"), r.URL.Query().Get("uid"))
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "Failed to list rooms and equipment")
				return
			}
			free := len(busy) == 0
			v.Free = &free
		}
		out = append(out, v)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"resources": out})
}

// handleResourceBusy answers a bookable resource's busy times: GET /api/resources/{id}/busy.
// Times only; anyone who may not book it gets 404.
func (s *Server) handleResourceBusy(w http.ResponseWriter, r *http.Request) {
	if !s.resourceReadAllowed(w, r) {
		return
	}
	from, to, _, ok := s.resourceRange(w, r, true)
	if !ok {
		return
	}
	rs, err := s.store.Resources().Bookable(r.Context(), sessionUser(r.Context()).ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the room")
		return
	}
	id := r.PathValue("id")
	if !slices.ContainsFunc(rs, func(x *resource.Resource) bool { return x.ID == id }) {
		s.writeError(w, http.StatusNotFound, "No such room or equipment")
		return
	}
	busy, err := s.store.Resources().Busy(r.Context(), id, from, to, "", "")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the room")
		return
	}
	out := make([]spanView, 0, len(busy))
	for _, b := range busy {
		out = append(out, spanView{Start: time.Unix(b.Start, 0).UTC().Format(time.RFC3339), End: time.Unix(b.End, 0).UTC().Format(time.RFC3339), Pending: b.Pending})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"busy": out})
}

type requestView struct {
	ID           string   `json:"id"`
	ResourceID   string   `json:"resource_id"`
	ResourceName string   `json:"resource_name"`
	Organizer    string   `json:"organizer"`
	First        spanView `json:"first"`
	Last         spanView `json:"last"`
	Occurrences  int      `json:"occurrences"`
}

// handleResourceRequests lists the pending bookings of the resources the caller approves.
func (s *Server) handleResourceRequests(w http.ResponseWriter, r *http.Request) {
	qs, err := s.store.Resources().Requests(r.Context(), sessionUser(r.Context()).ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list room requests")
		return
	}
	span := func(b store.BusySpan) spanView {
		return spanView{Start: time.Unix(b.Start, 0).UTC().Format(time.RFC3339), End: time.Unix(b.End, 0).UTC().Format(time.RFC3339)}
	}
	out := make([]requestView, 0, len(qs))
	for _, q := range qs {
		out = append(out, requestView{ID: q.ID, ResourceID: q.ResourceID, ResourceName: q.ResourceName, Organizer: q.Organizer,
			First: span(q.First), Last: span(q.Last), Occurrences: q.Occurrences})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

// handleDecideResourceRequest accepts or declines a pending booking:
// POST /api/resources/requests/{id} {"decision": "accept"|"decline"}.
func (s *Server) handleDecideResourceRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || (body.Decision != "accept" && body.Decision != "decline") {
		s.writeError(w, http.StatusBadRequest, `decision must be "accept" or "decline"`)
		return
	}
	user := sessionUser(r.Context())
	switch err := s.store.Resources().Decide(r.Context(), r.PathValue("id"), user.ID, body.Decision == "accept"); {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "No such request")
		return
	case errors.Is(err, store.ErrBookingState):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Someone already answered this request, or the event changed", "code": "not_pending"})
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to answer the request")
		return
	}
	s.auditAction(r.Context(), r, "resource.request_"+body.Decision, r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

// isResourceApprover is GET /api/settings' resource_approver for an everyday session.
func (s *Server) isResourceApprover(ctx context.Context, u *store.User) bool {
	if u == nil || u.Role == "admin" {
		return false
	}
	ok, err := s.store.Resources().IsApprover(ctx, u.ID)
	return err == nil && ok
}


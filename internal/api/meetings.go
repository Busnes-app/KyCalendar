package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
)

var (
	errSchedulingOff = &apiError{status: http.StatusUnprocessableEntity, msg: "Invitations are not turned on for this calendar server", code: "scheduling_off"}
	errNoAddress     = &apiError{status: http.StatusUnprocessableEntity, msg: "Your account has no email address, so you cannot invite or answer", code: "no_address"}
	errNotOrganizer  = &apiError{status: http.StatusForbidden, msg: "Only the organizer can change who is invited", code: "not_organizer"}
	errNotInvited    = &apiError{status: http.StatusUnprocessableEntity, msg: "You are not invited to this event", code: "not_invited"}
)

// selfAddress is the signed-in user's calendar user address while scheduling is on.
func (s *Server) selfAddress(ctx context.Context) (itip.Address, *apiError) {
	if s.sched == nil {
		return "", errSchedulingOff
	}
	u := sessionUser(ctx)
	if u == nil || u.Email == "" {
		return "", errNoAddress
	}
	a, ok := itip.ParseAddress("mailto:" + u.Email)
	if !ok {
		return "", errNoAddress
	}
	return a, nil
}

// applyAttendees replaces cal's attendee list with emails, the caller as organizer. nil keeps it.
func (s *Server) applyAttendees(ctx context.Context, cal *ical.Calendar, emails *[]string) *apiError {
	if emails == nil {
		return nil
	}
	self, apiErr := s.selfAddress(ctx)
	if apiErr != nil {
		return apiErr
	}
	var list []itip.Address
	for _, e := range *emails {
		a, ok := itip.ParseAddress("mailto:" + e)
		if !ok {
			return &apiError{status: http.StatusBadRequest, msg: "Not an email address: use name@example.com", code: "bad_attendee"}
		}
		list = append(list, a)
	}
	switch err := itip.SetAttendees(cal, self, list); {
	case errors.Is(err, itip.ErrNotOrganizer):
		return errNotOrganizer
	case errors.Is(err, itip.ErrInvalid):
		return &apiError{status: http.StatusBadRequest, msg: "At most 100 attendees", code: "too_many_attendees"}
	case err != nil:
		return &apiError{status: http.StatusInternalServerError, msg: "The attendee list cannot be changed"}
	}
	return nil
}

// handleRSVP records the caller's answer to a meeting they are invited to; the scheduler then
// tells the organizer. Body {"partstat": "ACCEPTED"|"TENTATIVE"|"DECLINED", "recurrence_id"?}.
func (s *Server) handleRSVP(w http.ResponseWriter, r *http.Request) {
	c, o, cal, etag, ok := s.loadEvent(w, r)
	if !ok {
		return
	}
	self, apiErr := s.selfAddress(r.Context())
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	var b struct {
		Partstat     string `json:"partstat"`
		RecurrenceID string `json:"recurrence_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if org, ok := itip.Organizer(cal); !ok || org == self {
		s.writeAPIError(w, errNotInvited)
		return
	}
	if b.RecurrenceID != "" {
		// Answering one occurrence needs its own override; EditText with no change makes one.
		if err := calendar.EditText(cal, b.RecurrenceID, calendar.TextEdit{}, time.Now()); err != nil {
			s.writeAPIError(w, editFailure(err))
			return
		}
	}
	switch err := itip.SetPartstat(cal, b.RecurrenceID, self, b.Partstat); {
	case errors.Is(err, itip.ErrBadPartstat):
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Answer with ACCEPTED, TENTATIVE or DECLINED", "code": "bad_partstat"})
		return
	case errors.Is(err, itip.ErrNotInvited):
		s.writeAPIError(w, errNotInvited)
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "The answer cannot be recorded")
		return
	}
	if saved, ok := s.writeEvent(w, r, c, o.Name, cal, etag, false); ok {
		s.writeJSON(w, http.StatusOK, map[string]string{"etag": saved.ETag})
	}
}

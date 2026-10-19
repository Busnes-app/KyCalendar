package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type busyView struct {
	Start     string `json:"start"`
	End       string `json:"end"`
	Tentative bool   `json:"tentative,omitempty"`
}

type personBusy struct {
	Email   string     `json:"email"`
	Unknown bool       `json:"unknown,omitempty"` // not someone on this instance: no busy times shown
	Busy    []busyView `json:"busy"`
}

// handleFreeBusy is the attendee picker's free/busy: GET /api/freebusy?users=a@x,b@y&start&end.
// Busy times only, for active everyday users on this instance; anyone else is unknown.
func (s *Server) handleFreeBusy(w http.ResponseWriter, r *http.Request) {
	if _, apiErr := s.selfAddress(r.Context()); apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	if !s.allowAccountAttempt("freebusy:"+sessionUser(r.Context()).ID, 120, time.Minute) {
		s.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many free/busy requests; wait a minute", "code": "rate_limited"})
		return
	}
	q := r.URL.Query()
	start, err1 := time.Parse(time.RFC3339, q.Get("start"))
	end, err2 := time.Parse(time.RFC3339, q.Get("end"))
	emails := strings.Split(q.Get("users"), ",")
	if err1 != nil || err2 != nil || !end.After(start) || end.Sub(start) > davbackend.MaxFreeBusyRange || q.Get("users") == "" || len(emails) > davbackend.MaxFreeBusyPeople {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Give users (at most 50 emails) and a start before end, at most 62 days apart", "code": "bad_range"})
		return
	}
	begun := time.Now()
	out := []personBusy{}
	for _, e := range emails {
		a, ok := itip.ParseAddress("mailto:" + strings.TrimSpace(e))
		if !ok {
			s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Not an email address: use name@example.com", "code": "bad_attendee"})
			return
		}
		p := personBusy{Email: a.Email(), Busy: []busyView{}}
		u, err := s.store.Users().CalendarUser(r.Context(), a.Email())
		switch {
		case errors.Is(err, store.ErrNotFound):
			p.Unknown = true
		case err != nil:
			s.writeError(w, http.StatusInternalServerError, "Failed to read free/busy")
			return
		default:
			periods, err := davbackend.PersonalBusy(r.Context(), s.store, u, start, end, begun)
			if errors.Is(err, davbackend.ErrFreeBusyTooBig) {
				s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Too much to show; choose fewer people or a shorter range", "code": "too_many_instances"})
				return
			}
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "Failed to read free/busy")
				return
			}
			for _, b := range periods {
				p.Busy = append(p.Busy, busyView{Start: b.Start.UTC().Format(time.RFC3339), End: b.End.UTC().Format(time.RFC3339), Tentative: b.Type == caldav.FreeBusyTentative})
			}
		}
		out = append(out, p)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"people": out})
}

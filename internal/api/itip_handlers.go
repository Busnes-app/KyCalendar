package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Inbound iTIP from KyPost: 60 messages per user per hour.
const (
	itipPerUser = 60
	itipWindow  = time.Hour
)

// kypostRequest verifies a signed request from KyPost and resolves who it speaks for. It writes
// the refusal itself and returns ok false: 404 while KyPost or scheduling is off, 401 for a
// bad signature, 200 for a replay already acted on, 404 for an unknown user, 429 past the limit.
func (s *Server) kypostRequest(w http.ResponseWriter, r *http.Request, event string, into any, who func() kypost.User) (*store.User, bool) {
	if s.sched == nil || len(s.config.Calendar.KyPostSecret) == 0 {
		http.NotFound(w, r)
		return nil, false
	}
	body, replayed, err := kypost.Verify(r, s.config.Calendar.KyPostSecret, event, s.kypostReplay)
	if replayed {
		s.writeJSON(w, http.StatusOK, map[string]string{"outcome": "duplicate"})
		return nil, false
	}
	if err != nil {
		s.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Bad signature", "code": "bad_signature"})
		return nil, false
	}
	if err := json.Unmarshal(body, into); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body", "code": "bad_body"})
		return nil, false
	}
	u := s.kypostUser(r, who())
	if u == nil {
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": "No such user", "code": "no_user"})
		return nil, false
	}
	if !s.allowAccountAttempt("itip:"+u.ID, itipPerUser, itipWindow) {
		s.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many messages", "code": "rate_limited"})
		return nil, false
	}
	return u, true
}

// kypostUser is the one active everyday account bound to who's KyIdentity subject and issuer.
func (s *Server) kypostUser(r *http.Request, who kypost.User) *store.User {
	if who.Sub == "" || who.Issuer == "" {
		return nil
	}
	for _, provider := range []string{"kysignon", "scim", "oidc"} {
		u, err := s.store.Users().GetUserBySSO(r.Context(), provider, who.Sub)
		if err != nil {
			continue
		}
		if _, issuer, _ := strings.Cut(u.SSOIssuer, " "); issuer == who.Issuer && u.Role == "user" && u.Status == "active" {
			return u
		}
	}
	return nil
}

// handleITIPInbound applies an iTIP message KyPost found in a user's mailbox.
func (s *Server) handleITIPInbound(w http.ResponseWriter, r *http.Request) {
	var in kypost.InboundRequest
	u, ok := s.kypostRequest(w, r, kypost.EventInbound, &in, func() kypost.User { return in.User })
	if !ok {
		return
	}
	from, okFrom := itip.ParseAddress("mailto:" + in.From)
	if !in.DKIMAligned || !okFrom {
		s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Only mail with an aligned DKIM signature is applied", "code": "unverified_sender"})
		return
	}
	msg, err := itip.Decode([]byte(in.ICS))
	if err != nil {
		s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Not a valid scheduling message", "code": "bad_itip"})
		return
	}
	outcome, err := s.sched.Inbound(r.Context(), u, from, msg)
	if errors.Is(err, itip.ErrInvalid) {
		s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Not a valid scheduling message", "code": "bad_itip"})
		return
	}
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "The message could not be applied", "code": "error"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
}

// handleITIPRespond records a user's answer from a KyPost RSVP card.
func (s *Server) handleITIPRespond(w http.ResponseWriter, r *http.Request) {
	var in kypost.RespondRequest
	u, ok := s.kypostRequest(w, r, kypost.EventRespond, &in, func() kypost.User { return in.User })
	if !ok {
		return
	}
	var invite *ical.Calendar
	if in.ICS != "" {
		cal, err := itip.Decode([]byte(in.ICS))
		if err != nil {
			s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Not a valid invitation", "code": "bad_itip"})
			return
		}
		invite = cal
	}
	err := s.sched.Respond(r.Context(), u, in.UID, in.RecurrenceID, in.Partstat, invite)
	switch {
	case err == nil:
		s.writeJSON(w, http.StatusOK, map[string]string{"outcome": "answered"})
	case errors.Is(err, store.ErrNotFound):
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": "No such invitation", "code": "no_event"})
	case errors.Is(err, itip.ErrBadPartstat):
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Answer with ACCEPTED, TENTATIVE or DECLINED", "code": "bad_partstat"})
	case errors.Is(err, itip.ErrInvalid), errors.Is(err, itip.ErrNotInvited):
		s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Not an invitation to this user", "code": "not_invited"})
	case errors.Is(err, store.ErrPreconditionFailed):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "The event changed; try again", "code": "conflict"})
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "Calendar storage is full", "code": "quota"})
	default:
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "The answer could not be recorded", "code": "error"})
	}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Status from KyMessages (docs/SUITE-INTEGRATIONS.md section P).
const (
	presenceBatch    = 20
	presenceCacheTTL = 60 * time.Second
	presenceCacheMax = 5000
)

// SyncPresence sends due members' busy blocks to KyMessages. cmd/server calls it on a ticker.
func (s *Server) SyncPresence(ctx context.Context) {
	if s.kymessages.Peer() == nil {
		return
	}
	cals := s.store.Calendars()
	if err := cals.RefreshPresence(ctx, meetingNow()); err != nil {
		log.Printf("[KYMESSAGES] refreshing status pushes failed: %v", err)
		return
	}
	due, err := cals.DuePresence(ctx, meetingNow(), presenceBatch)
	if err != nil {
		log.Printf("[KYMESSAGES] listing status pushes failed: %v", err)
		return
	}
	for _, p := range due {
		if err := s.pushPresence(ctx, p); err != nil {
			log.Printf("[KYMESSAGES] status push for %s: recording the result failed: %v", p.UserID, err)
			return
		}
	}
}

// pushPresence makes one attempt for p's member and records it; only a store failure is returned.
func (s *Server) pushPresence(ctx context.Context, p store.PresencePush) error {
	cals, now := s.store.Calendars(), meetingNow()
	u, err := s.store.Users().GetUserByID(ctx, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // the row goes with the user
	}
	if err != nil {
		return err
	}
	if u.Status != "active" || !s.meetingsFor(u) {
		return cals.PushedPresence(ctx, p.UserID, p.Rev, now.Add(store.PresenceRefresh))
	}
	busy, err := davbackend.MeetingBlocks(ctx, s.store, u, now, now.Add(kymessages.BusyAhead))
	if err != nil {
		log.Printf("[KYMESSAGES] status push for %s: %v", u.ID, err)
		return cals.RetryPresence(ctx, p.UserID, now.Add(meetingWait(p.Attempts)))
	}
	push := kymessages.BusyPush{User: kymessages.User{Sub: u.SSOSubject, Issuer: issuerURL(u)}, Revision: now.UnixMilli(), Busy: []kymessages.Block{}}
	for _, b := range busy {
		push.Busy = append(push.Busy, kymessages.Block{Start: b.Start.Format(time.RFC3339), End: b.End.Format(time.RFC3339)})
	}
	_, err = s.kymessages.Busy(ctx, "evt_"+crypto.RandomHex(16), push)
	var refused kymessages.Refused
	switch {
	case err == nil, errors.As(err, &refused): // a refusal waits for the next change or refresh
		return cals.PushedPresence(ctx, p.UserID, p.Rev, now.Add(store.PresenceRefresh))
	default:
		return cals.RetryPresence(ctx, p.UserID, now.Add(meetingWait(p.Attempts)))
	}
}

type presenceEntry struct {
	status kymessages.Status
	at     time.Time
}

// presenceCache holds KyMessages' answers by subject for presenceCacheTTL.
type presenceCache struct {
	mu sync.Mutex
	m  map[string]presenceEntry
}

func (c *presenceCache) get(sub string, now time.Time) (kymessages.Status, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[sub]
	return e.status, ok && now.Sub(e.at) < presenceCacheTTL
}

func (c *presenceCache) put(m map[string]kymessages.Status, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) > presenceCacheMax {
		c.m = map[string]presenceEntry{}
	}
	for sub, st := range m {
		c.m[sub] = presenceEntry{st, now}
	}
}

func (s *Server) presenceRefuse(w http.ResponseWriter, status int, code, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// presenceFailed maps a KyMessages failure to an answer; it reports whether it wrote one.
func (s *Server) presenceFailed(w http.ResponseWriter, err error) bool {
	var refused kymessages.Refused
	switch {
	case err == nil:
		return false
	case errors.As(err, &refused) && refused.Code == "no_user":
		s.presenceRefuse(w, http.StatusNotFound, "no_user", "You have no KyMessages account yet.")
	case errors.As(err, &refused) && refused.Code == "sharing_off":
		s.presenceRefuse(w, http.StatusConflict, "sharing_off", "Turn on Show my status to colleagues first.")
	case errors.As(err, &refused):
		s.presenceRefuse(w, http.StatusBadGateway, "refused", "KyMessages refused the request")
	default:
		s.presenceRefuse(w, http.StatusServiceUnavailable, "unavailable", "Try again in a moment")
	}
	return true
}

// handlePresenceMe is GET and PUT /api/presence/me: the member's own status settings, kept by
// KyMessages. PUT takes {"share"?: bool, "manual"?: {"state","until"} | null}.
func (s *Server) handlePresenceMe(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r.Context())
	if !s.meetingsFor(u) {
		s.presenceRefuse(w, http.StatusConflict, "status_off", "Status needs KyMessages and a KyIdentity sign-in")
		return
	}
	if !s.allowAccountAttempt("presence-me:"+u.ID, 30, time.Minute) {
		s.presenceRefuse(w, http.StatusTooManyRequests, "rate_limited", "Too many changes; wait a minute")
		return
	}
	var share *bool
	var manual *kymessages.Manual
	setManual := false
	if r.Method == http.MethodPut {
		var req struct {
			Share  *bool           `json:"share"`
			Manual json.RawMessage `json:"manual"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil {
			s.presenceRefuse(w, http.StatusBadRequest, "bad_body", "Invalid request")
			return
		}
		share = req.Share
		if req.Manual != nil {
			setManual = true
			if string(req.Manual) != "null" {
				var m kymessages.Manual
				err := json.Unmarshal(req.Manual, &m)
				var until time.Time
				if err == nil {
					until, err = time.Parse(time.RFC3339, m.Until)
				}
				now := time.Now()
				if err != nil || (m.State != "available" && m.State != "busy" && m.State != "away") || !until.After(now) || until.After(now.Add(kymessages.ManualMaxAhead)) {
					s.presenceRefuse(w, http.StatusBadRequest, "bad_body", "Choose a status and when it ends, within 7 days")
					return
				}
				m.Until = until.UTC().Format(time.RFC3339)
				manual = &m
			}
		}
	}
	got, err := s.kymessages.Me(r.Context(), "evt_"+crypto.RandomHex(16), kymessages.User{Sub: u.SSOSubject, Issuer: issuerURL(u)}, share, manual, setManual)
	if s.presenceFailed(w, err) {
		return
	}
	if r.Method == http.MethodPut {
		_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: u.ID, Action: "presence.settings"})
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, got)
}

// handlePresenceLookup is POST /api/presence {"emails": [...]}: organisation people's status by
// address, at most kymessages.MaxLookup, cached for a minute. Anyone else is absent.
func (s *Server) handlePresenceLookup(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r.Context())
	var req struct {
		Emails []string `json:"emails"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req) != nil || len(req.Emails) == 0 {
		s.presenceRefuse(w, http.StatusBadRequest, "bad_body", "Name at least one address")
		return
	}
	if len(req.Emails) > kymessages.MaxLookup {
		s.presenceRefuse(w, http.StatusBadRequest, "too_many", "At most 50 people at a time")
		return
	}
	for _, e := range req.Emails {
		if validEmail(e) != nil {
			s.presenceRefuse(w, http.StatusBadRequest, "bad_body", "Invalid address")
			return
		}
	}
	if !s.meetingsFor(u) {
		s.presenceRefuse(w, http.StatusConflict, "status_off", "Status needs KyMessages and a KyIdentity sign-in")
		return
	}
	if !s.allowAccountAttempt("presence:"+u.ID, 60, time.Minute) {
		s.presenceRefuse(w, http.StatusTooManyRequests, "rate_limited", "Too many status requests; wait a minute")
		return
	}
	people, err := s.resolveAs(r.Context(), u, req.Emails)
	if err != nil {
		log.Printf("presence: people lookup for %s: %v", u.ID, err) // never an address
		s.presenceRefuse(w, http.StatusServiceUnavailable, "unavailable", "Try again in a moment")
		return
	}
	now := time.Now()
	found := map[string]kymessages.Status{}
	var ask []string
	for _, p := range people {
		if st, ok := s.presence.get(p.Sub, now); ok {
			found[p.Sub] = st
		} else {
			ask = append(ask, p.Sub)
		}
	}
	if len(ask) > 0 {
		got, err := s.kymessages.Lookup(r.Context(), "evt_"+crypto.RandomHex(16), kymessages.User{Sub: u.SSOSubject, Issuer: issuerURL(u)}, ask)
		if s.presenceFailed(w, err) {
			return
		}
		s.presence.put(got, now)
		for sub, st := range got {
			found[sub] = st
		}
	}
	type row struct {
		Email string `json:"email"`
		State string `json:"state"`
		Until string `json:"until,omitempty"`
	}
	out := []row{}
	for _, p := range people {
		if st, ok := found[p.Sub]; ok {
			out = append(out, row{strings.ToLower(p.Email), st.State, st.Until})
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	s.writeJSON(w, http.StatusOK, map[string]any{"people": out})
}

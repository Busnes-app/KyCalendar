package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/feed"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type subscriptionView struct {
	Host        string     `json:"host"`
	RefreshedAt *time.Time `json:"refreshed_at"`
	CheckedAt   *time.Time `json:"checked_at"`
	Error       string     `json:"error,omitempty"`
}

func subscriptionViewOf(sub *store.Subscription) *subscriptionView {
	return &subscriptionView{Host: sub.URLHost, RefreshedAt: sub.RefreshedAt, CheckedAt: sub.CheckedAt, Error: sub.Error}
}

// Each link is fetched by the server, so a user may try at most this many per window.
const (
	feedAttempts      = 10
	feedAttemptWindow = 15 * time.Minute
	refreshSpacing    = time.Minute
)

// loadLink checks and fetches a pasted link for calendarID and writes the error itself.
func (s *Server) loadLink(w http.ResponseWriter, r *http.Request, calendarID, raw string) (link, host string, f *feed.Fetched, objs []*store.CalendarObject, ok bool) {
	link, host, err := feed.NormalizeURL(raw)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, feed.Message(err))
		return "", "", nil, nil, false
	}
	if !s.allowAttempt("feed:"+sessionUser(r.Context()).ID, feedAttempts, feedAttemptWindow) {
		w.Header().Set("Retry-After", "900")
		s.writeError(w, http.StatusTooManyRequests, "Too many links tried; wait a few minutes")
		return "", "", nil, nil, false
	}
	f, objs, err = s.feed.Load(r.Context(), calendarID, host, link, "", "")
	if err != nil {
		s.writeError(w, http.StatusUnprocessableEntity, feed.Message(err))
		return "", "", nil, nil, false
	}
	return link, host, f, objs, true
}

// subscribedCalendar is the caller's subscribed calendar id; anything else answers 404.
func (s *Server) subscribedCalendar(w http.ResponseWriter, r *http.Request) (*store.Calendar, *store.Subscription) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return nil, nil
	}
	sub, err := s.store.Calendars().GetSubscription(r.Context(), c.ID)
	if role != access.Owner || errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such subscription")
		return nil, nil
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the subscription")
		return nil, nil
	}
	return c, sub
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	var b struct {
		URL string `json:"url"`
		calendarBody
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	err := b.check()
	if err == nil && b.Name == nil {
		err = errors.New("Name is required")
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := &store.Calendar{ID: "cal_" + uuid.NewString(), OwnerKind: "user", OwnerID: user.ID, Slug: "s-" + crypto.RandomHex(4), Name: *b.Name}
	if b.Color != nil {
		c.Color = *b.Color
	}
	if b.Description != nil {
		c.Description = *b.Description
	}
	link, host, f, objs, ok := s.loadLink(w, r, c.ID, b.URL)
	if !ok {
		return
	}
	sealed, err := feed.Seal(s.config.Security.EncryptionKey, link)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the subscription")
		return
	}
	now := time.Now().UTC()
	sub := &store.Subscription{URLSealed: sealed, URLHost: host, ETag: f.ETag, LastModified: f.LastModified,
		RefreshedAt: &now, CheckedAt: &now, NextCheckAt: now.Add(feed.Interval)}
	// Detached: a dropped connection must not leave a calendar without its events.
	ctx := context.WithoutCancel(r.Context())
	err = s.store.Calendars().CreateSubscribedCalendar(ctx, c, sub, s.config.Calendar.MaxCalendarsPerUser, feed.MaxPerUser)
	switch {
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "You have reached the calendar or subscription limit", "code": "quota"})
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to save the subscription")
		return
	}
	if err := s.store.Calendars().ReplaceSubscriptionObjects(ctx, c.ID, objs, s.objectLimits()); err != nil {
		_ = s.store.Calendars().DeleteCalendar(ctx, c.ID)
		if errors.Is(err, store.ErrQuotaExceeded) {
			s.writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "Calendar storage is full", "code": "quota"})
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to save the subscription")
		return
	}
	s.auditAction(ctx, r, "subscription.create", c.ID, "host="+host)
	v := s.viewOf(user, c, access.Owner)
	v.Subscription = subscriptionViewOf(sub)
	s.writeJSON(w, http.StatusCreated, v)
}

// handlePatchSubscription replaces the link, for example after the user reset it at the source.
// The new link must load before anything changes.
func (s *Server) handlePatchSubscription(w http.ResponseWriter, r *http.Request) {
	c, sub := s.subscribedCalendar(w, r)
	if c == nil {
		return
	}
	var b struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	link, host, f, objs, ok := s.loadLink(w, r, c.ID, b.URL)
	if !ok {
		return
	}
	sealed, err := feed.Seal(s.config.Security.EncryptionKey, link)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the subscription")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.store.Calendars().ReplaceSubscriptionObjects(ctx, c.ID, objs, s.objectLimits()); err != nil {
		if errors.Is(err, store.ErrQuotaExceeded) {
			s.writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "Calendar storage is full", "code": "quota"})
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to save the subscription")
		return
	}
	if err := s.store.Calendars().SetSubscriptionSource(ctx, c.ID, sealed, host); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the subscription")
		return
	}
	now := time.Now().UTC()
	sub.URLSealed, sub.URLHost, sub.ETag, sub.LastModified = sealed, host, f.ETag, f.LastModified
	sub.RefreshedAt, sub.CheckedAt, sub.NextCheckAt, sub.Failures, sub.Error = &now, &now, now.Add(feed.Interval), 0, ""
	if err := s.store.Calendars().RecordSubscriptionCheck(ctx, sub); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the subscription")
		return
	}
	s.auditAction(ctx, r, "subscription.url_change", c.ID, "host="+host)
	v := s.viewOf(sessionUser(r.Context()), c, access.Owner)
	v.Subscription = subscriptionViewOf(sub)
	s.writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleRefreshSubscription(w http.ResponseWriter, r *http.Request) {
	c, sub := s.subscribedCalendar(w, r)
	if c == nil {
		return
	}
	now := time.Now().UTC()
	// Claimed in the store, so parallel requests cannot all pass the spacing and each fetch.
	claimed, err := s.store.Calendars().ClaimSubscriptionCheck(r.Context(), c.ID, now, now.Add(-refreshSpacing))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to refresh the subscription")
		return
	}
	if !claimed {
		w.Header().Set("Retry-After", "60")
		s.writeError(w, http.StatusTooManyRequests, "Refreshed less than a minute ago")
		return
	}
	// Detached so a dropped connection still records the outcome, but bounded: it may wait for a
	// fetch slot. The outcome, failure included, is recorded on sub and shown in the view.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	_ = s.feed.Check(ctx, sub, now)
	v := s.viewOf(sessionUser(r.Context()), c, access.Owner)
	v.Subscription = subscriptionViewOf(sub)
	s.writeJSON(w, http.StatusOK, v)
}

// RefreshDueSubscriptions mirrors every subscription whose next check has come.
func (s *Server) RefreshDueSubscriptions(ctx context.Context) { s.feed.RunDue(ctx, time.Now().UTC()) }

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/tasks"
)

// Flagged mail (design: docs/superpowers/specs/2026-11-15-kypost-flag-to-task-design.md).
const (
	flagsPerHour   = 120
	maxSubject     = 255
	sourceKyPost   = "kypost"
	taskStateLimit = 5 * time.Second
)

var flagRef = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// flagInput validates KyPost's request at the boundary: the link must be KyPost's own https
// address, the subject one line of text.
func flagInput(in kypost.TaskFlagRequest, peer *kypost.Peer) (string, *tasks.Moment, bool) {
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		subject = "Flagged email"
	}
	if len(subject) > maxSubject {
		subject = strings.ToValidUTF8(subject[:maxSubject], "")
	}
	if !flagRef.MatchString(in.Ref) || !cleanText(subject, maxSubject, false) || !validTaskURL(in.URL) {
		return "", nil, false
	}
	u, err1 := url.Parse(in.URL)
	p, err2 := url.Parse(peer.URL)
	if err1 != nil || err2 != nil || !strings.EqualFold(u.Host, p.Host) {
		return "", nil, false
	}
	var due *tasks.Moment
	if in.Due != "" {
		d, err := time.Parse(time.DateOnly, in.Due)
		if err != nil || !yearsInRange(d) {
			return "", nil, false
		}
		due = &tasks.Moment{T: d, AllDay: true}
	}
	return subject, due, true
}

// flaggedTask finds the user's task made from message ref, in their own task lists.
func (s *Server) flaggedTask(ctx context.Context, u *store.User, ref string) (*loadedTask, error) {
	cals, err := s.store.Calendars().ListCalendarsByOwner(ctx, "user", u.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range cals {
		if c.Subscribed || !slices.Contains(c.Components, ical.CompToDo) {
			continue
		}
		ts, err := s.loadTasks(ctx, c, time.UTC)
		if err != nil {
			return nil, err
		}
		for i := range ts {
			if ts[i].task.Source == sourceKyPost && ts[i].task.Ref == ref {
				return &ts[i], nil
			}
		}
	}
	return nil, nil
}

// handleKyPostTaskFlag makes, updates or completes the task for a message the user flagged in
// KyPost. It never calls KyPost back: the change came from there.
func (s *Server) handleKyPostTaskFlag(w http.ResponseWriter, r *http.Request) {
	var in kypost.TaskFlagRequest
	u, ok := s.kypostPeerRequest(w, r, kypost.EventTaskFlag, "taskflag:", flagsPerHour, &in, func() kypost.User { return in.User })
	if !ok {
		return
	}
	ctx := r.Context()
	reply := func(status int, outcome, code string, t *loadedTask) {
		body := map[string]string{"outcome": outcome}
		if code != "" {
			body = map[string]string{"error": outcome, "code": code}
		}
		if t != nil {
			body["calendar_id"], body["uid"] = t.cal.ID, t.task.UID
		}
		_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: u.ID, Action: "kypost.task_flag", Details: "outcome=" + outcome + code})
		s.writeJSON(w, status, body)
	}
	on, err := s.store.Tasks().KyPostFlags(ctx, u.ID)
	if err != nil {
		s.retryKyPost(r)
		reply(http.StatusServiceUnavailable, "Settings unavailable", "unavailable", nil)
		return
	}
	if !on {
		reply(http.StatusConflict, "The user turned tasks from flagged mail off", "tasks_off", nil)
		return
	}
	subject, due, ok := flagInput(in, s.kypost.Peer())
	if !ok {
		reply(http.StatusBadRequest, "Invalid flag", "bad_body", nil)
		return
	}
	found, err := s.flaggedTask(ctx, u, in.Ref)
	if err != nil {
		s.retryKyPost(r)
		reply(http.StatusServiceUnavailable, "Tasks unavailable", "unavailable", nil)
		return
	}
	now := time.Now()
	write := func(t *loadedTask, p tasks.Patch) bool {
		if _, err := tasks.Apply(t.ical, p, now, time.UTC); err != nil {
			reply(http.StatusUnprocessableEntity, "The stored task cannot be changed", "bad_task", t)
			return false
		}
		if _, apiErr := s.saveEvent(ctx, u, t.cal, t.obj.Name, t.ical, t.obj.ETag, false); apiErr != nil {
			s.retryKyPost(r)
			reply(apiErr.status, apiErr.msg, cmpCode(apiErr.code), t)
			return false
		}
		return true
	}
	done := func(t *loadedTask) bool { return t.task.Status == tasks.Completed || t.task.Status == tasks.Cancelled }
	switch {
	case !in.Flagged && (found == nil || done(found)):
		reply(http.StatusOK, "unchanged", "", found)
	case !in.Flagged:
		st := tasks.Completed
		if write(found, tasks.Patch{Status: &st}) {
			reply(http.StatusOK, "completed", "", found)
		}
	case found != nil:
		p := tasks.Patch{}
		if done(found) {
			st := tasks.NeedsAction
			p.Status = &st
		}
		if (due == nil) != (found.task.Due == nil) || due != nil && found.task.Due != nil && !due.T.Equal(found.task.Due.T) {
			p.Due = &tasks.Moment{}
			if due != nil {
				p.Due = due
			}
		}
		if p.Status == nil && p.Due == nil {
			reply(http.StatusOK, "unchanged", "", found)
		} else if write(found, p) {
			reply(http.StatusOK, "updated", "", found)
		}
	default:
		box, apiErr := s.inbox(ctx, u)
		if apiErr != nil {
			s.retryKyPost(r)
			reply(apiErr.status, apiErr.msg, cmpCode(apiErr.code), nil)
			return
		}
		uid := uuid.NewString()
		cal, err := tasks.New(uid, tasks.Patch{Title: &subject, Due: due}, in.URL, sourceKyPost, now)
		if err == nil {
			err = tasks.SetRef(cal, in.Ref)
		}
		if err != nil {
			reply(http.StatusBadRequest, "Invalid flag", "bad_body", nil)
			return
		}
		if _, apiErr := s.saveEvent(ctx, u, box, uid+".ics", cal, "", true); apiErr != nil {
			s.retryKyPost(r)
			reply(apiErr.status, apiErr.msg, cmpCode(apiErr.code), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]string{"outcome": "created", "calendar_id": box.ID, "uid": uid})
		_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: u.ID, Action: "kypost.task_flag", Details: "outcome=created"})
	}
}

func cmpCode(c string) string {
	if c == "" {
		return "error"
	}
	return c
}

// tellKyPost reports a flagged-mail task's new state to KyPost, so the flag follows: done
// clears it, reopened sets it again. Best effort, after the user's write has landed; the user's
// own KyIdentity subject names them. Never for a change KyPost itself made.
func (s *Server) tellKyPost(ctx context.Context, u *store.User, t *tasks.Task, done bool) {
	if t.Source != sourceKyPost || !flagRef.MatchString(t.Ref) || u.SSOSubject == "" || s.kypost.Peer() == nil {
		return
	}
	_, issuer, _ := strings.Cut(u.SSOIssuer, " ")
	if issuer == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), taskStateLimit)
	defer cancel()
	err := s.kypost.SendTaskState(ctx, uuid.NewString(), kypost.TaskStateRequest{User: kypost.User{Sub: u.SSOSubject, Issuer: issuer}, Ref: t.Ref, Done: done})
	outcome := "sent"
	var refused kypost.Refused
	switch {
	case errors.As(err, &refused):
		outcome = "refused " + refused.Code
	case err != nil:
		outcome = "failed"
	}
	_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: u.ID, Action: "kypost.task_state", Details: "outcome=" + outcome})
}

type taskPrefs struct {
	KyPostFlags bool `json:"kypost_flags"`
	// KyPostLinked: KyPost is paired, so the switch does something.
	KyPostLinked bool `json:"kypost_linked"`
}

func (s *Server) handleGetTaskPrefs(w http.ResponseWriter, r *http.Request) {
	on, err := s.store.Tasks().KyPostFlags(r.Context(), sessionUser(r.Context()).ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load settings")
		return
	}
	s.writeJSON(w, http.StatusOK, taskPrefs{KyPostFlags: on, KyPostLinked: s.kypost.Peer() != nil})
}

func (s *Server) handlePutTaskPrefs(w http.ResponseWriter, r *http.Request) {
	var b struct {
		KyPostFlags *bool `json:"kypost_flags"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<10)).Decode(&b); err != nil || b.KyPostFlags == nil {
		s.writeError(w, http.StatusBadRequest, "kypost_flags is required")
		return
	}
	u := sessionUser(r.Context())
	if err := s.store.Tasks().SetKyPostFlags(r.Context(), u.ID, *b.KyPostFlags); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save settings")
		return
	}
	s.writeJSON(w, http.StatusOK, taskPrefs{KyPostFlags: *b.KyPostFlags, KyPostLinked: s.kypost.Peer() != nil})
}

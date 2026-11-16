package api

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Out-of-office bounds: a period of at most a year, comments as long as KyPost's reply texts.
const (
	oooMaxPeriod   = 366 * 24 * time.Hour
	oooMaxComment  = 4000
	oooApplyTrials = 3
)

var errOOOBody = errors.New("bad out-of-office body")

// oooFromRequest validates KyPost's out-of-office state at the boundary.
func oooFromRequest(userID string, in kypost.OutOfOfficeRequest) (*store.OutOfOffice, error) {
	o := &store.OutOfOffice{UserID: userID, Revision: in.Revision, Away: in.Away}
	if in.Revision <= 0 {
		return nil, errOOOBody
	}
	if !in.Away {
		return o, nil
	}
	var err1, err2 error
	o.Start, err1 = time.Parse(time.RFC3339, in.Start)
	o.End, err2 = time.Parse(time.RFC3339, in.End)
	if err1 != nil || err2 != nil || !o.End.After(o.Start) || o.End.Sub(o.Start) > oooMaxPeriod {
		return nil, errOOOBody
	}
	o.Start, o.End = o.Start.UTC(), o.End.UTC()
	if in.AllDay {
		zone, err := time.LoadLocation(in.TimeZone)
		if err != nil || in.TimeZone == "" || in.TimeZone == "Local" || !midnight(o.Start.In(zone)) || !midnight(o.End.In(zone)) {
			return nil, errOOOBody
		}
		o.AllDay, o.Zone = true, in.TimeZone
	}
	o.Tentative = in.Tentative
	for dst, src := range map[*string]string{&o.CommentLocal: in.CommentLocal, &o.CommentRemote: in.CommentRemote} {
		if !utf8.ValidString(src) || utf8.RuneCountInString(src) > oooMaxComment ||
			strings.ContainsFunc(src, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
			return nil, errOOOBody
		}
		*dst = src
	}
	return o, nil
}

func midnight(t time.Time) bool { return t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 }

// handleKyPostOutOfOffice applies the user's out-of-office state from KyPost: one busy event
// over the period, and optionally TENTATIVE answers to new invitations during it. KyPost is the
// only source; nothing here ever turns out-of-office on by itself.
func (s *Server) handleKyPostOutOfOffice(w http.ResponseWriter, r *http.Request) {
	var in kypost.OutOfOfficeRequest
	u, ok := s.kypostRequestLimited(w, r, kypost.EventOOO, "ooo:", &in, func() kypost.User { return in.User })
	if !ok {
		return
	}
	o, err := oooFromRequest(u.ID, in)
	if err != nil {
		s.auditOOO(r, u, "bad_body")
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid out-of-office state", "code": "bad_body"})
		return
	}
	stored, err := s.store.Users().SetOutOfOffice(r.Context(), o)
	if err != nil {
		s.retryKyPost(r)
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "The state could not be stored", "code": "error"})
		return
	}
	if !stored {
		s.writeJSON(w, http.StatusOK, map[string]string{"outcome": "stale"})
		return
	}
	// Apply what is stored now, not o: a newer revision may have landed meanwhile.
	for range oooApplyTrials {
		cur, rerr := s.store.Users().OutOfOffice(r.Context(), u.ID)
		if err = rerr; err == nil {
			err = s.sched.ApplyOutOfOffice(r.Context(), u, cur)
		}
		if !errors.Is(err, store.ErrPreconditionFailed) {
			break
		}
	}
	switch {
	case err == nil:
		s.auditOOO(r, u, "")
		s.writeJSON(w, http.StatusOK, map[string]string{"outcome": "applied"})
	case errors.Is(err, store.ErrQuotaExceeded):
		s.auditOOO(r, u, "quota")
		s.writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "Calendar storage is full", "code": "quota"})
	default:
		s.retryKyPost(r)
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "The event could not be written", "code": "error"})
	}
}

// auditOOO records an outcome with IDs only, never the period or the comments.
func (s *Server) auditOOO(r *http.Request, u *store.User, code string) {
	rec := &store.AuditRecord{UserID: u.ID, Action: "kypost.ooo.applied"}
	if code != "" {
		rec.Action, rec.Details = "kypost.ooo.refused", "code="+code
	}
	_ = s.store.Audit().LogAudit(r.Context(), rec)
}

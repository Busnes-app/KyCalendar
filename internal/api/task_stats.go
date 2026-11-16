package api

import (
	"net/http"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// handleTaskStats is Today's small insight: tasks completed this week (from Monday) and today,
// and the streak of consecutive days with a completion, in the viewer's zone, from the log of
// web completions.
func (s *Server) handleTaskStats(w http.ResponseWriter, r *http.Request) {
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	now := time.Now().In(viewer)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, viewer)
	comps, err := s.store.Tasks().Completions(r.Context(), sessionUser(r.Context()).ID, today.AddDate(0, 0, -400))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load completions")
		return
	}
	s.writeJSON(w, http.StatusOK, completionStats(comps, now))
}

type taskStats struct {
	DoneToday    int `json:"done_today"`
	DoneThisWeek int `json:"done_this_week"`
	Streak       int `json:"streak"`
}

// completionStats counts completions today and since Monday, and the run of consecutive days
// with one, ending today (or yesterday, so a streak survives until the day ends).
func completionStats(comps []store.TaskCompletion, now time.Time) taskStats {
	loc := now.Location()
	dayOf := func(t time.Time) string { return t.In(loc).Format(time.DateOnly) }
	days := map[string]bool{}
	today := dayOf(now)
	monday := now.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
	weekStart := time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, loc)
	var st taskStats
	for _, c := range comps {
		d := dayOf(c.At)
		days[d] = true
		if d == today {
			st.DoneToday++
		}
		if !c.At.Before(weekStart) {
			st.DoneThisWeek++
		}
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc)
	if !days[today] {
		day = day.AddDate(0, 0, -1)
	}
	for days[day.Format(time.DateOnly)] {
		st.Streak++
		day = day.AddDate(0, 0, -1)
	}
	return st
}

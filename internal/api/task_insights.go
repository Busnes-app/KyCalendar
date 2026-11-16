package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/tasks"
)

// Goals, habits and insights (PR 1d). They read the completion log; nothing here writes a task.
const (
	maxGoals      = 50
	maxGoalTitle  = 200
	maxGoalLinks  = 50
	maxGoalDays   = 366
	stalledAfter  = 14 * 24 * time.Hour
	historyWeeks  = 12
	workdayStartH = 8
	workdayEndH   = 18
)

var goalPeriods = []string{"season", "quarter", "year", "custom"}

type goalView struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Period string   `json:"period"`
	Starts string   `json:"starts"`
	Ends   string   `json:"ends"`
	Areas  []string `json:"areas"`
	Lists  []string `json:"lists"`
}

func viewOfGoal(g store.TaskGoal) goalView {
	v := goalView{ID: g.ID, Title: g.Title, Period: g.Period, Starts: g.Starts, Ends: g.Ends, Areas: g.Areas, Lists: g.Lists}
	if v.Areas == nil {
		v.Areas = []string{}
	}
	if v.Lists == nil {
		v.Lists = []string{}
	}
	return v
}

// decodeGoal validates a goal at the boundary: links must be the caller's areas and readable lists.
func (s *Server) decodeGoal(r *http.Request, user *store.User) (goalView, *apiError) {
	var b goalView
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<10)).Decode(&b); err != nil {
		return b, &apiError{status: http.StatusBadRequest, msg: "Invalid JSON body"}
	}
	b.Title = strings.TrimSpace(b.Title)
	starts, err1 := time.Parse(time.DateOnly, b.Starts)
	ends, err2 := time.Parse(time.DateOnly, b.Ends)
	switch {
	case b.Title == "" || !cleanText(b.Title, maxGoalTitle, false):
		return b, badTask(fmt.Sprintf("A goal's title is 1 to %d bytes", maxGoalTitle))
	case !slices.Contains(goalPeriods, b.Period):
		return b, badTask("period is season, quarter, year or custom")
	case err1 != nil || err2 != nil || ends.Before(starts) || ends.Sub(starts) > maxGoalDays*24*time.Hour || !yearsInRange(starts, ends):
		return b, badTask("starts and ends are dates, at most a year apart")
	case len(b.Areas) > maxGoalLinks || len(b.Lists) > maxGoalLinks:
		return b, badTask(fmt.Sprintf("A goal links at most %d areas and %d lists", maxGoalLinks, maxGoalLinks))
	}
	lists, apiErr := s.taskLists(r.Context(), user)
	if apiErr != nil {
		return b, apiErr
	}
	areas, apiErr := s.areasOf(r.Context(), user, lists)
	if apiErr != nil {
		return b, apiErr
	}
	for _, id := range b.Lists {
		if !slices.ContainsFunc(lists, func(c *store.Calendar) bool { return c.ID == id }) {
			return b, errNoCalendar
		}
	}
	for _, id := range b.Areas {
		if !slices.ContainsFunc(areas, func(a taskArea) bool { return a.ID == id }) {
			return b, &apiError{status: http.StatusNotFound, msg: "No such area"}
		}
	}
	if b.Areas == nil {
		b.Areas = []string{}
	}
	if b.Lists == nil {
		b.Lists = []string{}
	}
	return b, nil
}

func (s *Server) handleListGoals(w http.ResponseWriter, r *http.Request) {
	gs, err := s.store.TaskGoals().Goals(r.Context(), sessionUser(r.Context()).ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load goals")
		return
	}
	out := []goalView{}
	for _, g := range gs {
		out = append(out, viewOfGoal(g))
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateGoal(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	b, apiErr := s.decodeGoal(r, user)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	g := &store.TaskGoal{ID: "goal_" + crypto.RandomHex(8), UserID: user.ID, Title: b.Title, Period: b.Period, Starts: b.Starts, Ends: b.Ends, Areas: b.Areas, Lists: b.Lists, CreatedAt: time.Now()}
	err := s.store.TaskGoals().CreateGoal(r.Context(), g, maxGoals)
	if errors.Is(err, store.ErrQuotaExceeded) {
		s.writeAPIError(w, &apiError{status: http.StatusConflict, msg: fmt.Sprintf("At most %d goals", maxGoals), code: "quota"})
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the goal")
		return
	}
	s.writeJSON(w, http.StatusCreated, viewOfGoal(*g))
}

func (s *Server) handleUpdateGoal(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	b, apiErr := s.decodeGoal(r, user)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	g := &store.TaskGoal{ID: r.PathValue("id"), UserID: user.ID, Title: b.Title, Period: b.Period, Starts: b.Starts, Ends: b.Ends, Areas: b.Areas, Lists: b.Lists}
	err := s.store.TaskGoals().UpdateGoal(r.Context(), g)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such goal")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the goal")
		return
	}
	s.writeJSON(w, http.StatusOK, viewOfGoal(*g))
}

func (s *Server) handleDeleteGoal(w http.ResponseWriter, r *http.Request) {
	err := s.store.TaskGoals().DeleteGoal(r.Context(), sessionUser(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such goal")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the goal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type goalListProgress struct {
	ListID   string `json:"list_id"`
	Name     string `json:"name"`
	Done     int    `json:"done"`
	Open     int    `json:"open"`
	LastDone string `json:"last_done,omitempty"`
	Stalled  bool   `json:"stalled"`
}

// handleGoalProgress reports each list the goal links (areas expanded): completions in the
// period, open tasks, the last completion and whether it stalled (none in 14 days).
func (s *Server) handleGoalProgress(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	gs, err := s.store.TaskGoals().Goals(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load goals")
		return
	}
	i := slices.IndexFunc(gs, func(g store.TaskGoal) bool { return g.ID == r.PathValue("id") })
	if i < 0 {
		s.writeError(w, http.StatusNotFound, "No such goal")
		return
	}
	g := gs[i]
	lists, apiErr := s.taskLists(r.Context(), user)
	if apiErr == nil {
		var areas []taskArea
		if areas, apiErr = s.areasOf(r.Context(), user, lists); apiErr == nil {
			ids := slices.Clone(g.Lists)
			for _, a := range areas {
				if slices.Contains(g.Areas, a.ID) {
					ids = append(ids, a.Lists...)
				}
			}
			out, apiErr := s.goalProgress(r, user, lists, ids, g, viewer)
			if apiErr == nil {
				s.writeJSON(w, http.StatusOK, map[string]any{"goal": viewOfGoal(g), "lists": out})
				return
			}
		}
	}
	s.writeAPIError(w, apiErr)
}

func (s *Server) goalProgress(r *http.Request, user *store.User, lists []*store.Calendar, ids []string, g store.TaskGoal, viewer *time.Location) ([]goalListProgress, *apiError) {
	starts, _ := time.ParseInLocation(time.DateOnly, g.Starts, viewer)
	ends, _ := time.ParseInLocation(time.DateOnly, g.Ends, viewer)
	comps, err := s.store.Tasks().Completions(r.Context(), user.ID, starts.AddDate(0, 0, -15))
	if err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load completions"}
	}
	now := time.Now()
	out := []goalListProgress{}
	seen := map[string]bool{}
	for _, id := range ids {
		i := slices.IndexFunc(lists, func(c *store.Calendar) bool { return c.ID == id })
		if i < 0 || seen[id] {
			continue // no longer readable
		}
		seen[id] = true
		c := lists[i]
		p := goalListProgress{ListID: c.ID, Name: c.Name}
		var last time.Time
		for _, cm := range comps {
			if cm.CalendarID != c.ID {
				continue
			}
			if !cm.At.Before(starts) && cm.At.Before(ends.AddDate(0, 0, 1)) {
				p.Done++
			}
			if cm.At.After(last) {
				last = cm.At
			}
		}
		ts, err := s.loadTasks(r.Context(), c, viewer)
		if err != nil {
			return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load tasks"}
		}
		for _, t := range ts {
			if t.task.Status == tasks.NeedsAction || t.task.Status == tasks.InProcess {
				p.Open++
			}
		}
		if !last.IsZero() {
			p.LastDone = last.In(viewer).Format(time.RFC3339)
		}
		p.Stalled = last.IsZero() || now.Sub(last) > stalledAfter
		out = append(out, p)
	}
	return out, nil
}

type habitView struct {
	Freq    string   `json:"freq"` // daily or weekly
	Current int      `json:"current"`
	Longest int      `json:"longest"`
	Days    []string `json:"days"` // local dates with a completion, last 12 weeks
}

// handleHabit is a habit's streaks over its completions: consecutive days (daily rules) or
// weeks (weekly rules) with one, and the days of the last 12 weeks that had one.
func (s *Server) handleHabit(w http.ResponseWriter, r *http.Request) {
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	c, _ := s.calendarFor(w, r, r.PathValue("cal"))
	if c == nil {
		return
	}
	_, cal, apiErr := s.loadTaskObject(r.Context(), c, r.PathValue("uid"))
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	t, err := tasks.Read(cal, viewer)
	if err != nil || !t.Habit || t.RRule == "" {
		s.writeAPIError(w, &apiError{status: http.StatusUnprocessableEntity, msg: "This task is not a habit", code: "not_habit"})
		return
	}
	opt, err := rrule.StrToROption(t.RRule)
	if err != nil || (opt.Freq != rrule.DAILY && opt.Freq != rrule.WEEKLY) {
		s.writeAPIError(w, &apiError{status: http.StatusUnprocessableEntity, msg: "Habits repeat daily or weekly", code: "not_habit"})
		return
	}
	comps, err := s.store.Tasks().Completions(r.Context(), sessionUser(r.Context()).ID, time.Now().AddDate(-1, 0, -7))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load completions")
		return
	}
	var ats []time.Time
	for _, cm := range comps {
		if cm.CalendarID == c.ID && cm.UID == t.UID {
			ats = append(ats, cm.At)
		}
	}
	s.writeJSON(w, http.StatusOK, habitStats(ats, opt.Freq == rrule.WEEKLY, time.Now().In(viewer)))
}

// habitStats counts streaks in days, or ISO-like weeks from Monday, ending now (or the period
// before, so a streak lasts until its period ends).
func habitStats(ats []time.Time, weekly bool, now time.Time) habitView {
	loc := now.Location()
	key := func(t time.Time) string {
		t = t.In(loc)
		if weekly {
			t = t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
		}
		return t.Format(time.DateOnly)
	}
	step := 1
	if weekly {
		step = 7
	}
	have := map[string]bool{}
	days := map[string]bool{}
	from := now.AddDate(0, 0, -7*historyWeeks)
	for _, at := range ats {
		have[key(at)] = true
		if at.After(from) {
			days[at.In(loc).Format(time.DateOnly)] = true
		}
	}
	v := habitView{Freq: "daily", Days: []string{}}
	if weekly {
		v.Freq = "weekly"
	}
	cur := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc)
	if !have[key(cur)] {
		cur = cur.AddDate(0, 0, -step)
	}
	for have[key(cur)] {
		v.Current++
		cur = cur.AddDate(0, 0, -step)
	}
	var keys []string
	for k := range have {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	run := 0
	var prev time.Time
	for _, k := range keys {
		d, _ := time.ParseInLocation(time.DateOnly, k, loc)
		d = d.Add(12 * time.Hour)
		if run > 0 && d.Sub(prev).Hours() < float64(step*24)+2 {
			run++
		} else {
			run = 1
		}
		prev = d
		v.Longest = max(v.Longest, run)
	}
	for d := range days {
		v.Days = append(v.Days, d)
	}
	slices.Sort(v.Days)
	return v
}

type insightsView struct {
	Weeks []struct {
		Start string `json:"start"`
		Done  int    `json:"done"`
	} `json:"weeks"`
	Lists []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Open    int    `json:"open"`
		Overdue int    `json:"overdue"`
	} `json:"lists"`
	Days []struct {
		Date       string `json:"date"`
		PlannedMin int    `json:"planned_min"`
		FreeMin    int    `json:"free_min"`
	} `json:"days"`
}

// handleInsights: completions per week (12 weeks), open and overdue per list, and planned
// against free minutes for each day of this week (free: 08:00 to 18:00 less personal events).
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	ctx := r.Context()
	now := time.Now().In(viewer)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, viewer)
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	firstWeek := monday.AddDate(0, 0, -7*(historyWeeks-1))
	comps, err := s.store.Tasks().Completions(ctx, user.ID, firstWeek)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load completions")
		return
	}
	var out insightsView
	for i := range historyWeeks {
		start := firstWeek.AddDate(0, 0, 7*i)
		n := 0
		for _, c := range comps {
			if !c.At.Before(start) && c.At.Before(start.AddDate(0, 0, 7)) {
				n++
			}
		}
		out.Weeks = append(out.Weeks, struct {
			Start string `json:"start"`
			Done  int    `json:"done"`
		}{start.Format(time.DateOnly), n})
	}
	lists, apiErr := s.taskLists(ctx, user)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	planned := map[string]int{}
	for _, c := range lists {
		ts, err := s.loadTasks(ctx, c, viewer)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to load tasks")
			return
		}
		row := struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Open    int    `json:"open"`
			Overdue int    `json:"overdue"`
		}{ID: c.ID, Name: c.Name}
		for _, t := range ts {
			if t.task.Status != tasks.NeedsAction && t.task.Status != tasks.InProcess {
				continue
			}
			row.Open++
			if d := t.task.Due; d != nil && (d.AllDay && d.T.Format(time.DateOnly) < today.Format(time.DateOnly) || !d.AllDay && d.T.Before(now)) {
				row.Overdue++
			}
			if st := t.task.Start; st != nil && !st.AllDay && c.OwnerKind == "user" {
				planned[st.T.In(viewer).Format(time.DateOnly)] += int(t.task.Length() / time.Minute)
			}
		}
		out.Lists = append(out.Lists, row)
	}
	for i := range 7 {
		day := monday.AddDate(0, 0, i)
		from := time.Date(day.Year(), day.Month(), day.Day(), workdayStartH, 0, 0, 0, viewer)
		to := time.Date(day.Year(), day.Month(), day.Day(), workdayEndH, 0, 0, 0, viewer)
		busy, err := davbackend.PersonalBusy(ctx, s.store, user, from, to, time.Now())
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to read busy times")
			return
		}
		free := 0
		for _, g := range tasks.Gaps(from, to, busy).Free {
			free += int(g.End.Sub(g.Start) / time.Minute)
		}
		key := day.Format(time.DateOnly)
		out.Days = append(out.Days, struct {
			Date       string `json:"date"`
			PlannedMin int    `json:"planned_min"`
			FreeMin    int    `json:"free_min"`
		}{key, planned[key], free})
	}
	s.writeJSON(w, http.StatusOK, out)
}

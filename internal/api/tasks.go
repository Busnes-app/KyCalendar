package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"
	"github.com/teambition/rrule-go"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/tasks"
)

// Task bounds, checked at the API boundary (design: docs/superpowers/specs/2026-11-15-tasks-design.md).
const (
	maxTasks       = 5000
	maxTags        = 20
	maxTagBytes    = 64
	maxTaskDepth   = 3 // a task, its subtask and that subtask's subtask
	maxReminders   = 10
	maxRRuleBytes  = 200
	maxLengthMin   = 24 * 60
	maxAreas       = 20
	maxAreaLists   = 50
	maxViews       = 50
	maxViewBytes   = 2048
	maxNameBytes   = 100
	maxQueryBytes  = 200
	maxQueryWords  = 10
	searchesPerMin = 30
	inboxName      = "Tasks"
)

type taskView struct {
	CalendarID     string     `json:"calendar_id"`
	UID            string     `json:"uid"`
	ETag           string     `json:"etag"`
	Title          string     `json:"title"`
	Notes          string     `json:"notes,omitempty"`
	Status         string     `json:"status"`
	CompletedAt    string     `json:"completed_at,omitempty"`
	Percent        int        `json:"percent,omitempty"`
	Priority       int        `json:"priority,omitempty"`
	Due            string     `json:"due,omitempty"`
	Start          string     `json:"start,omitempty"`
	DurationMin    int        `json:"duration_min,omitempty"`
	EstimateMin    int        `json:"estimate_min,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
	Parent         string     `json:"parent,omitempty"`
	RRule          string     `json:"rrule,omitempty"`
	FromCompletion bool       `json:"repeat_from_completion,omitempty"`
	Habit          bool       `json:"habit,omitempty"`
	Reminders      []reminder `json:"reminders,omitempty"`
	URL            string     `json:"url,omitempty"`
	Source         string     `json:"source,omitempty"`
	Files          []fileView `json:"files,omitempty"`
	Created        string     `json:"created,omitempty"`
	Modified       string     `json:"modified,omitempty"`
	Editable       bool       `json:"editable"`
	Subtasks       int        `json:"subtasks,omitempty"`
	SubtasksDone   int        `json:"subtasks_done,omitempty"`
	// Images are the inline images, served by GET .../images/{n}; never their bytes here.
	Images []imageView `json:"images,omitempty"`
}

type imageView struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// reminder is a VALARM: before_min minutes before the due time (start without one), or at.
type reminder struct {
	BeforeMin *int   `json:"before_min,omitempty"`
	At        string `json:"at,omitempty"`
}

// taskBody is a create or patch; an absent field keeps the stored value, an empty one removes it.
type taskBody struct {
	Title          *string     `json:"title"`
	Notes          *string     `json:"notes"`
	Status         *string     `json:"status"`
	Priority       *int        `json:"priority"`
	Due            *string     `json:"due"`
	Start          *string     `json:"start"`
	Zone           *string     `json:"zone"`
	DurationMin    *int        `json:"duration_min"`
	EstimateMin    *int        `json:"estimate_min"`
	Tags           *[]string   `json:"tags"`
	Parent         *string     `json:"parent"`
	RRule          *string     `json:"rrule"`
	FromCompletion *bool       `json:"repeat_from_completion"`
	Habit          *bool       `json:"habit"`
	Reminders      *[]reminder `json:"reminders"`
	Files          *[]fileView `json:"files"`
	// Image is a pasted image (create only): base64 of a PNG, JPEG or WebP of at most 512 KiB.
	Image *imageBody `json:"image"`
}

type imageBody struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

func badTask(msg string) *apiError {
	return &apiError{status: http.StatusBadRequest, msg: msg, code: "bad_task"}
}

func cleanText(s string, max int, lines bool) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, func(c rune) bool {
		return unicode.IsControl(c) && !(lines && (c == '\n' || c == '\t'))
	})
}

// moment reads "YYYY-MM-DD" (a day) or RFC 3339; "" is the zero Moment, which removes.
func taskMoment(s string) (tasks.Moment, bool) {
	if s == "" {
		return tasks.Moment{}, true
	}
	if d, err := time.Parse(time.DateOnly, s); err == nil {
		return tasks.Moment{T: d, AllDay: true}, yearsInRange(d)
	}
	t, err := time.Parse(time.RFC3339, s)
	return tasks.Moment{T: t}, err == nil && yearsInRange(t)
}

// patch validates b at the boundary into a tasks.Patch; files are checked separately.
func (b taskBody) patch() (tasks.Patch, *apiError) {
	var p tasks.Patch
	if b.Title != nil {
		title := strings.TrimSpace(*b.Title)
		if title == "" || !cleanText(title, maxTitleBytes, false) {
			return p, badTask(fmt.Sprintf("A title is 1 to %d bytes without control characters", maxTitleBytes))
		}
		p.Title = &title
	}
	if b.Notes != nil {
		if !cleanText(*b.Notes, maxDescriptionBytes, true) {
			return p, badTask(fmt.Sprintf("Notes are at most %d bytes without control characters", maxDescriptionBytes))
		}
		p.Notes = b.Notes
	}
	if b.Status != nil {
		switch *b.Status {
		case tasks.NeedsAction, tasks.InProcess, tasks.Completed, tasks.Cancelled:
			p.Status = b.Status
		default:
			return p, badTask("status must be NEEDS-ACTION, IN-PROCESS, COMPLETED or CANCELLED")
		}
	}
	if b.Priority != nil {
		if *b.Priority < 0 || *b.Priority > 9 {
			return p, badTask("priority must be 0 to 9")
		}
		p.Priority = b.Priority
	}
	for _, m := range []struct {
		in  *string
		out **tasks.Moment
	}{{b.Due, &p.Due}, {b.Start, &p.Start}} {
		if m.in == nil {
			continue
		}
		mo, ok := taskMoment(*m.in)
		if !ok {
			return p, badTask("Dates are YYYY-MM-DD or RFC 3339, years 1900 to 9000")
		}
		*m.out = &mo
	}
	if b.Zone != nil && *b.Zone != "" {
		loc, ok := zoneNamed(*b.Zone)
		if !ok {
			return p, badTask("zone must be an IANA time zone")
		}
		p.Zone = loc
	}
	for _, d := range []struct {
		in  *int
		out **time.Duration
	}{{b.DurationMin, &p.Duration}, {b.EstimateMin, &p.Estimate}} {
		if d.in == nil {
			continue
		}
		if *d.in < 0 || *d.in > maxLengthMin {
			return p, badTask("Lengths are 0 to 1440 minutes")
		}
		v := time.Duration(*d.in) * time.Minute
		*d.out = &v
	}
	if b.Tags != nil {
		if len(*b.Tags) > maxTags {
			return p, badTask(fmt.Sprintf("A task has at most %d tags", maxTags))
		}
		var tags []string
		for _, tag := range *b.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" || strings.Contains(tag, ",") || !cleanText(tag, maxTagBytes, false) {
				return p, badTask(fmt.Sprintf("A tag is 1 to %d bytes without commas or control characters", maxTagBytes))
			}
			if !slices.Contains(tags, tag) {
				tags = append(tags, tag)
			}
		}
		p.Tags = &tags
	}
	if b.Parent != nil {
		if len(*b.Parent) > 255 || !cleanText(*b.Parent, 255, false) {
			return p, badTask("parent must be a task's UID")
		}
		p.Parent = b.Parent
	}
	if b.RRule != nil {
		if r := *b.RRule; r != "" {
			if len(r) > maxRRuleBytes || !cleanText(r, maxRRuleBytes, false) {
				return p, badTask("The repeat rule is too long")
			}
			opt, err := rrule.StrToROption(r)
			if err != nil || opt.Freq > rrule.DAILY {
				return p, badTask("The repeat rule must be an RFC 5545 RRULE, daily to yearly")
			}
		}
		p.RRule = b.RRule
	}
	p.FromCompletion, p.Habit = b.FromCompletion, b.Habit
	if b.Reminders != nil {
		if len(*b.Reminders) > maxReminders {
			return p, badTask(fmt.Sprintf("A task has at most %d reminders", maxReminders))
		}
		rs := []tasks.Reminder{}
		for _, r := range *b.Reminders {
			switch {
			case r.BeforeMin != nil && r.At == "" && *r.BeforeMin >= -maxLengthMin*31 && *r.BeforeMin <= maxLengthMin*31:
				rs = append(rs, tasks.Reminder{Before: time.Duration(*r.BeforeMin) * time.Minute})
			case r.BeforeMin == nil && r.At != "":
				at, err := time.Parse(time.RFC3339, r.At)
				if err != nil || !yearsInRange(at) {
					return p, badTask("A reminder's at is RFC 3339")
				}
				rs = append(rs, tasks.Reminder{At: at})
			default:
				return p, badTask("A reminder is before_min (within 31 days) or at")
			}
		}
		p.Reminders = &rs
	}
	return p, nil
}

func formatMoment(m *tasks.Moment, viewer *time.Location) string {
	if m == nil {
		return ""
	}
	if m.AllDay {
		return m.T.Format(time.DateOnly)
	}
	return m.T.In(viewer).Format(time.RFC3339)
}

func formatTime(t time.Time, viewer *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(viewer).Format(time.RFC3339)
}

func viewOfTask(c *store.Calendar, o *store.CalendarObject, cal *ical.Calendar, t *tasks.Task, viewer *time.Location, editable bool) taskView {
	v := taskView{CalendarID: c.ID, UID: t.UID, ETag: o.ETag, Title: t.Title, Notes: t.Notes, Status: t.Status,
		CompletedAt: formatTime(t.CompletedAt, viewer), Percent: t.Percent, Priority: t.Priority,
		Due: formatMoment(t.Due, viewer), Start: formatMoment(t.Start, viewer),
		DurationMin: int(t.Duration / time.Minute), EstimateMin: int(t.Estimate / time.Minute),
		Tags: t.Tags, Parent: t.Parent, RRule: t.RRule, FromCompletion: t.FromCompletion, Habit: t.Habit,
		URL: t.URL, Source: t.Source, Created: formatTime(t.Created, viewer), Modified: formatTime(t.Modified, viewer),
		Editable: editable}
	for _, r := range t.Reminders {
		if r.At.IsZero() {
			m := int(r.Before / time.Minute)
			v.Reminders = append(v.Reminders, reminder{BeforeMin: &m})
		} else {
			v.Reminders = append(v.Reminders, reminder{At: r.At.Format(time.RFC3339)})
		}
	}
	if m, err := tasks.Master(cal); err == nil {
		files := calendar.Files(m)
		v.Notes = calendar.StripFiles(v.Notes, files)
		for _, f := range files {
			if kymessages.ValidURL(f.URL) {
				v.Files = append(v.Files, fileView(f))
			}
		}
	}
	for _, img := range tasks.Images(cal) {
		v.Images = append(v.Images, imageView{Name: img.Name, Type: img.Type})
	}
	if !validTaskURL(v.URL) {
		v.URL = "" // only an https link is ever offered as the source
	}
	return v
}

func validTaskURL(u string) bool { return kymessages.ValidURL(u) }

// loadedTask is one stored task, decoded.
type loadedTask struct {
	cal  *store.Calendar
	obj  *store.CalendarObject
	ical *ical.Calendar
	task *tasks.Task
}

// loadTasks decodes every task of c. A stored object that does not decode is skipped: one bad
// client write never hides a list.
func (s *Server) loadTasks(ctx context.Context, c *store.Calendar, viewer *time.Location) ([]loadedTask, error) {
	if !slices.Contains(c.Components, ical.CompToDo) {
		return nil, nil
	}
	objs, err := s.store.Calendars().ListObjects(ctx, c.ID)
	if err == nil {
		err = store.RefuseLocked(objs...)
	}
	if err != nil {
		return nil, err
	}
	var out []loadedTask
	for _, o := range objs {
		if o.Component != ical.CompToDo {
			continue
		}
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			continue
		}
		t, err := tasks.Read(cal, viewer)
		if err != nil {
			continue
		}
		out = append(out, loadedTask{c, o, cal, t})
	}
	return out, nil
}

// taskFilter is GET /api/tasks's query, also the shape a saved view stores.
type taskFilter struct {
	Q        string   `json:"q,omitempty"`
	Lists    []string `json:"lists,omitempty"`
	Area     string   `json:"area,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Priority []string `json:"priority,omitempty"`
	DueFrom  string   `json:"due_from,omitempty"`
	DueTo    string   `json:"due_to,omitempty"`
	Status   []string `json:"status,omitempty"`
	Source   string   `json:"source,omitempty"`
	Sort     string   `json:"sort,omitempty"`
	Layout   string   `json:"layout,omitempty"`
}

var priorityNames = []string{"high", "medium", "low", "none"}

func priorityName(p int) string {
	switch {
	case p == 0:
		return "none"
	case p <= 4:
		return "high"
	case p == 5:
		return "medium"
	}
	return "low"
}

var statusNames = map[string]string{"needs-action": tasks.NeedsAction, "in-process": tasks.InProcess, "completed": tasks.Completed, "cancelled": tasks.Cancelled}

// check validates f at the boundary.
func (f *taskFilter) check() *apiError {
	f.Q = strings.TrimSpace(f.Q)
	if len(f.Q) > maxQueryBytes || !cleanText(f.Q, maxQueryBytes, false) || len(strings.Fields(f.Q)) > maxQueryWords {
		return badTask(fmt.Sprintf("A search is at most %d bytes and %d words", maxQueryBytes, maxQueryWords))
	}
	if len(f.Lists) > maxAreaLists || len(f.Tags) > maxTags || len(f.Area) > 64 {
		return badTask("Too many lists or tags in the filter")
	}
	for _, t := range f.Tags {
		if !cleanText(t, maxTagBytes, false) {
			return badTask("A tag is at most 64 bytes")
		}
	}
	for _, p := range f.Priority {
		if !slices.Contains(priorityNames, p) {
			return badTask("priority is high, medium, low or none")
		}
	}
	for _, st := range f.Status {
		if statusNames[st] == "" {
			return badTask("status is needs-action, in-process, completed or cancelled")
		}
	}
	for _, d := range []string{f.DueFrom, f.DueTo} {
		if _, err := time.Parse(time.DateOnly, d); d != "" && err != nil {
			return badTask("due_from and due_to are dates")
		}
	}
	if !slices.Contains([]string{"", "smart", "due", "priority", "created", "title"}, f.Sort) ||
		!slices.Contains([]string{"", "list", "kanban"}, f.Layout) || !slices.Contains([]string{"", "kypost"}, f.Source) {
		return badTask("Unknown sort, layout or source")
	}
	return nil
}

func splitList(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func filterFromQuery(r *http.Request) taskFilter {
	q := r.URL.Query()
	return taskFilter{Q: q.Get("q"), Lists: splitList(q.Get("lists")), Area: q.Get("area"), Tags: splitList(q.Get("tags")),
		Priority: splitList(q.Get("priority")), DueFrom: q.Get("due_from"), DueTo: q.Get("due_to"),
		Status: splitList(q.Get("status")), Source: q.Get("source"), Sort: q.Get("sort")}
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	f := filterFromQuery(r)
	out, apiErr := s.listTasks(r.Context(), sessionUser(r.Context()), f, viewer, false)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// listTasks answers f over every task list user can read (personalOnly: their own lists only,
// for MCP), filtered and sorted. Unreadable lists are 404 like missing ones.
func (s *Server) listTasks(ctx context.Context, user *store.User, f taskFilter, viewer *time.Location, personalOnly bool) ([]taskView, *apiError) {
	if apiErr := f.check(); apiErr != nil {
		return nil, apiErr
	}
	if f.Q != "" && !s.allowAccountAttempt(ctx, "tasks-search:"+user.ID, searchesPerMin, time.Minute) {
		return nil, &apiError{status: http.StatusTooManyRequests, msg: "Too many searches; wait a minute", code: "rate_limited"}
	}
	cals, grants, err := s.visibleCalendars(ctx, user)
	if err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to list calendars"}
	}
	var lists []*store.Calendar
	for _, c := range cals {
		if slices.Contains(c.Components, ical.CompToDo) && (!personalOnly || c.OwnerKind == "user") {
			lists = append(lists, c)
		}
	}
	want := f.Lists
	if f.Area != "" {
		areas, apiErr := s.areasOf(ctx, user, lists)
		if apiErr != nil {
			return nil, apiErr
		}
		i := slices.IndexFunc(areas, func(a taskArea) bool { return a.ID == f.Area })
		if i < 0 {
			return nil, &apiError{status: http.StatusNotFound, msg: "No such area"}
		}
		want = append(want, areas[i].Lists...)
	}
	if len(want) > 0 {
		var picked []*store.Calendar
		for _, id := range want {
			i := slices.IndexFunc(lists, func(c *store.Calendar) bool { return c.ID == id })
			if i < 0 {
				return nil, errNoCalendar
			}
			if !slices.Contains(picked, lists[i]) {
				picked = append(picked, lists[i])
			}
		}
		lists = picked
	}
	var all []loadedTask
	for _, c := range lists {
		ts, err := s.loadTasks(ctx, c, viewer)
		if errors.Is(err, store.ErrLocked) {
			return nil, errContentLocked
		} else if err != nil {
			return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load tasks"}
		}
		all = append(all, ts...)
		if len(all) > maxTasks {
			return nil, &apiError{status: http.StatusUnprocessableEntity, msg: "Too many tasks; choose fewer lists", code: "too_many_tasks"}
		}
	}
	var hits map[string]bool
	if f.Q != "" {
		if hits, err = s.searchTasks(ctx, lists, all, f.Q); err != nil {
			return nil, &apiError{status: http.StatusInternalServerError, msg: "Search failed"}
		}
	}
	subs := map[string][2]int{} // calendar/uid -> subtasks, done
	for _, t := range all {
		if t.task.Parent != "" {
			k := t.cal.ID + "/" + t.task.Parent
			n := subs[k]
			n[0]++
			if t.task.Status == tasks.Completed {
				n[1]++
			}
			subs[k] = n
		}
	}
	statuses := []string{tasks.NeedsAction, tasks.InProcess}
	if len(f.Status) > 0 {
		statuses = nil
		for _, st := range f.Status {
			statuses = append(statuses, statusNames[st])
		}
	}
	type sorted struct {
		v   taskView
		due time.Time
		t   *tasks.Task
	}
	var picked []sorted
	for _, t := range all {
		k := t.cal.ID + "/" + t.task.UID
		if hits != nil && !hits[k] || !slices.Contains(statuses, t.task.Status) || f.Source != "" && t.task.Source != f.Source {
			continue
		}
		if len(f.Priority) > 0 && !slices.Contains(f.Priority, priorityName(t.task.Priority)) {
			continue
		}
		if len(f.Tags) > 0 && !slices.ContainsFunc(f.Tags, func(tag string) bool {
			return slices.ContainsFunc(t.task.Tags, func(have string) bool { return strings.EqualFold(have, tag) })
		}) {
			continue
		}
		dueDay := formatMoment(t.task.Due, viewer)
		if len(dueDay) > 10 {
			dueDay = dueDay[:10]
		}
		if f.DueFrom != "" && (dueDay == "" || dueDay < f.DueFrom) || f.DueTo != "" && (dueDay == "" || dueDay > f.DueTo) {
			continue
		}
		v := viewOfTask(t.cal, t.obj, t.ical, t.task, viewer, access.CanEditEvents(t.cal, access.Resolve(user, t.cal, grants)))
		n := subs[k]
		v.Subtasks, v.SubtasksDone = n[0], n[1]
		var due time.Time
		if t.task.Due != nil {
			due = t.task.Due.T
		}
		picked = append(picked, sorted{v, due, t.task})
	}
	now := time.Now()
	overdue := func(x sorted) bool { return !x.due.IsZero() && x.due.Before(now) }
	byDue := func(a, b sorted) int {
		switch {
		case a.due.IsZero() && b.due.IsZero():
			return 0
		case a.due.IsZero():
			return 1
		case b.due.IsZero():
			return -1
		}
		return a.due.Compare(b.due)
	}
	prio := func(x sorted) int { // 1 first, none last
		if x.t.Priority == 0 {
			return 10
		}
		return x.t.Priority
	}
	slices.SortStableFunc(picked, func(a, b sorted) int {
		tie := cmp.Or(cmp.Compare(strings.ToLower(a.v.Title), strings.ToLower(b.v.Title)), cmp.Compare(a.v.CalendarID, b.v.CalendarID), cmp.Compare(a.v.UID, b.v.UID))
		switch f.Sort {
		case "due":
			return cmp.Or(byDue(a, b), tie)
		case "priority":
			return cmp.Or(cmp.Compare(prio(a), prio(b)), byDue(a, b), tie)
		case "created":
			return cmp.Or(b.t.Created.Compare(a.t.Created), tie)
		case "title":
			return tie
		}
		if slices.Contains(statuses, tasks.Completed) && len(statuses) == 1 {
			return cmp.Or(b.t.CompletedAt.Compare(a.t.CompletedAt), tie)
		}
		oa, ob := overdue(a), overdue(b)
		if oa != ob {
			if oa {
				return -1
			}
			return 1
		}
		return cmp.Or(byDue(a, b), cmp.Compare(prio(a), prio(b)), tie)
	})
	out := make([]taskView, len(picked))
	for i, p := range picked {
		out[i] = p.v
	}
	return out, nil
}

// searchTasks reconciles each list's search rows with its tasks, then asks the index. The
// query text is never logged.
func (s *Server) searchTasks(ctx context.Context, lists []*store.Calendar, all []loadedTask, q string) (map[string]bool, error) {
	docs := map[string][]store.TaskDoc{}
	for _, t := range all {
		body := strings.Join(append([]string{t.task.Title, t.task.Notes}, t.task.Tags...), "\n")
		docs[t.cal.ID] = append(docs[t.cal.ID], store.TaskDoc{UID: t.task.UID, ETag: t.obj.ETag, Body: body})
	}
	sealed := map[string]bool{} // an encrypted list's tasks are matched here, never indexed (CE2)
	words := strings.Fields(q)
	ids := make([]string, 0, len(lists))
	for _, c := range lists {
		keyed, err := s.store.Tasks().IndexTasks(ctx, c.ID, docs[c.ID])
		if err != nil {
			return nil, err
		}
		if sealed[c.ID] = keyed; !keyed {
			ids = append(ids, c.ID)
		}
	}
	out, err := s.store.Tasks().SearchTasks(ctx, ids, words)
	if err != nil {
		return nil, err
	}
	for _, t := range all {
		if sealed[t.cal.ID] && s.store.Tasks().MatchTask(strings.Join(append([]string{t.task.Title, t.task.Notes}, t.task.Tags...), "\n"), words) {
			out[t.cal.ID+"/"+t.task.UID] = true
		}
	}
	return out, nil
}

// taskList authorizes a write on the task list id for the session user.
func (s *Server) taskList(w http.ResponseWriter, r *http.Request, id string) *store.Calendar {
	c, role := s.calendarFor(w, r, id)
	if c == nil {
		return nil
	}
	if !access.CanEditEvents(c, role) {
		s.writeAPIError(w, errReadOnlyCalendar)
		return nil
	}
	if !slices.Contains(c.Components, ical.CompToDo) {
		s.writeAPIError(w, &apiError{status: http.StatusUnprocessableEntity, msg: "This calendar holds events, not tasks", code: "events_only"})
		return nil
	}
	return c
}

func decodeTaskBody(r *http.Request) (taskBody, *apiError) {
	var b taskBody
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(&b); err != nil {
		return b, &apiError{status: http.StatusBadRequest, msg: "Invalid JSON body"}
	}
	return b, nil
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	c := s.taskList(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	b, apiErr := decodeTaskBody(r)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	uid := uuid.NewString()
	etag, apiErr := s.createTask(r.Context(), sessionUser(r.Context()), c, uid, b, "", "")
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]string{"calendar_id": c.ID, "uid": uid, "etag": etag})
}

// createTask validates b and writes it to c, which the caller may edit, as task uid.
func (s *Server) createTask(ctx context.Context, user *store.User, c *store.Calendar, uid string, b taskBody, url, source string) (string, *apiError) {
	if b.Title == nil {
		return "", badTask("A task needs a title")
	}
	p, apiErr := b.patch()
	if apiErr != nil {
		return "", apiErr
	}
	files, apiErr := eventFiles(b.Files)
	if apiErr != nil {
		return "", apiErr
	}
	if p.Parent != nil && *p.Parent != "" {
		if apiErr := s.checkParent(ctx, c, uid, *p.Parent); apiErr != nil {
			return "", apiErr
		}
	}
	cal, err := tasks.New(uid, p, url, source, time.Now())
	if err != nil {
		return "", badTask(strings.TrimPrefix(err.Error(), "tasks: invalid task: "))
	}
	if files != nil {
		calendar.SetFiles(cal, files)
	}
	if b.Image != nil {
		data, err := base64.StdEncoding.DecodeString(b.Image.Data)
		name := strings.TrimSpace(b.Image.Name)
		if err != nil || !cleanText(name, 255, false) || strings.ContainsAny(name, `"`) || tasks.AddImage(cal, tasks.Image{Name: name, Data: data}) != nil {
			return "", &apiError{status: http.StatusBadRequest, msg: "The image must be a PNG, JPEG or WebP of at most 512 KiB", code: "bad_image"}
		}
	}
	o, apiErr := s.saveEvent(ctx, user, c, uid+".ics", cal, "", true)
	if apiErr != nil {
		return "", apiErr
	}
	return o.ETag, nil
}

// checkParent refuses a parent that is missing from c, is the task itself, would make a cycle
// or would put any task deeper than maxTaskDepth.
func (s *Server) checkParent(ctx context.Context, c *store.Calendar, uid, parent string) *apiError {
	bad := &apiError{status: http.StatusBadRequest, msg: fmt.Sprintf("The parent must be another task in this list, at most %d levels deep", maxTaskDepth), code: "bad_parent"}
	if parent == uid {
		return bad
	}
	all, err := s.loadTasks(ctx, c, time.UTC)
	if err != nil {
		return &apiError{status: http.StatusInternalServerError, msg: "Failed to load tasks"}
	}
	parentOf := map[string]string{}
	for _, t := range all {
		parentOf[t.task.UID] = t.task.Parent
	}
	if _, ok := parentOf[parent]; !ok {
		return bad
	}
	depth := 1 // levels from the new parent up to its top
	for p := parentOf[parent]; p != ""; p = parentOf[p] {
		if p == uid || depth > maxTaskDepth {
			return bad
		}
		depth++
	}
	// The task's own subtree hangs below the parent.
	var height func(id string, seen int) int
	height = func(id string, seen int) int {
		h := 1
		if seen > maxTaskDepth {
			return h
		}
		for child, p := range parentOf {
			if p == id {
				h = max(h, 1+height(child, seen+1))
			}
		}
		return h
	}
	if depth+height(uid, 1) > maxTaskDepth {
		return bad
	}
	return nil
}

// loadTask authorizes a write and loads task uid with the request's If-Match.
func (s *Server) loadTask(w http.ResponseWriter, r *http.Request) (*store.Calendar, *store.CalendarObject, *ical.Calendar, string, bool) {
	c := s.taskList(w, r, r.PathValue("cal"))
	if c == nil {
		return nil, nil, nil, "", false
	}
	etag, present, ok := ifMatch(r)
	if !present {
		s.writeError(w, http.StatusPreconditionRequired, "If-Match with the task's ETag is required")
		return nil, nil, nil, "", false
	}
	if !ok {
		s.writeError(w, http.StatusBadRequest, "If-Match must be one strong ETag")
		return nil, nil, nil, "", false
	}
	o, cal, apiErr := s.loadTaskObject(r.Context(), c, r.PathValue("uid"))
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return nil, nil, nil, "", false
	}
	return c, o, cal, etag, true
}

var errNoTask = &apiError{status: http.StatusNotFound, msg: "No such task"}

func (s *Server) loadTaskObject(ctx context.Context, c *store.Calendar, uid string) (*store.CalendarObject, *ical.Calendar, *apiError) {
	o, err := s.store.Calendars().GetObjectByUID(ctx, c.ID, uid)
	if errors.Is(err, store.ErrNotFound) || (err == nil && o.Component != ical.CompToDo) {
		return nil, nil, errNoTask
	}
	if err == nil && o.Locked {
		return nil, nil, errContentLocked
	}
	if err != nil {
		return nil, nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load the task"}
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return nil, nil, &apiError{status: http.StatusUnprocessableEntity, msg: "The stored task does not parse"}
	}
	return o, cal, nil
}

func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	c, o, cal, etag, ok := s.loadTask(w, r)
	if !ok {
		return
	}
	b, apiErr := decodeTaskBody(r)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	saved, apiErr := s.patchTask(r.Context(), sessionUser(r.Context()), c, o, cal, etag, b, viewer)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	s.writeJSON(w, http.StatusOK, saved)
}

// patchTask applies b to the stored task and writes it; a completion is logged for the user.
func (s *Server) patchTask(ctx context.Context, user *store.User, c *store.Calendar, o *store.CalendarObject, cal *ical.Calendar, etag string, b taskBody, viewer *time.Location) (taskView, *apiError) {
	p, apiErr := b.patch()
	if apiErr != nil {
		return taskView{}, apiErr
	}
	files, apiErr := eventFiles(b.Files)
	if apiErr != nil {
		return taskView{}, apiErr
	}
	uid := o.UID
	if p.Parent != nil && *p.Parent != "" {
		if apiErr := s.checkParent(ctx, c, uid, *p.Parent); apiErr != nil {
			return taskView{}, apiErr
		}
	}
	if p.Zone == nil {
		p.Zone = viewer
	}
	now := time.Now()
	res, err := tasks.Apply(cal, p, now, viewer)
	if err != nil {
		return taskView{}, badTask(strings.TrimPrefix(err.Error(), "tasks: invalid task: "))
	}
	if files != nil {
		calendar.SetFiles(cal, files)
	}
	saved, apiErr := s.saveEvent(ctx, user, c, o.Name, cal, etag, false)
	if apiErr != nil {
		return taskView{}, apiErr
	}
	t, err := tasks.Read(cal, viewer)
	if err != nil {
		return taskView{}, &apiError{status: http.StatusInternalServerError, msg: "The saved task does not read"}
	}
	// A repeating task that moved on is open again: its flag stays.
	if res.Completed && !res.Advanced || res.Reopened {
		s.tellKyPost(ctx, user, t, res.Completed)
	}
	v := viewOfTask(c, saved, cal, t, viewer, true)
	return v, nil
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	c, o, cal, etag, ok := s.loadTask(w, r)
	if !ok {
		return
	}
	err := davbackend.Delete(r.Context(), s.store, c, o.Name, etag, s.objectLimits(), s.sched.Plan(r.Context(), sessionUser(r.Context()), nil))
	switch {
	case errors.Is(err, store.ErrPreconditionFailed):
		s.writeAPIError(w, errEventChanged)
	case errors.Is(err, store.ErrNotFound):
		s.writeAPIError(w, errNoTask)
	case errors.Is(err, store.ErrReadOnly):
		s.writeAPIError(w, errReadOnlyCalendar)
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the task")
	default:
		w.WriteHeader(http.StatusNoContent)
		// A deleted flagged-mail task clears its flag.
		if t, err := tasks.Read(cal, time.UTC); err == nil && t.Status != tasks.Completed {
			s.tellKyPost(r.Context(), sessionUser(r.Context()), t, true)
		}
	}
}

// inbox is the user's default task list: their first personal VTODO list named "Tasks",
// created when they have none.
func (s *Server) inbox(ctx context.Context, user *store.User) (*store.Calendar, *apiError) {
	cals, _, err := s.visibleCalendars(ctx, user)
	if err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to list calendars"}
	}
	for _, c := range cals {
		if c.OwnerKind == "user" && c.Name == inboxName && slices.Contains(c.Components, ical.CompToDo) && !c.Subscribed {
			return c, nil
		}
	}
	c := &store.Calendar{ID: "cal_" + uuid.NewString(), OwnerKind: "user", OwnerID: user.ID, Slug: "c-" + crypto.RandomHex(4), Name: inboxName, Components: []string{ical.CompToDo}}
	err = s.store.Calendars().CreateCalendar(ctx, c, s.config.Calendar.MaxCalendarsPerUser)
	if errors.Is(err, store.ErrQuotaExceeded) {
		return nil, &apiError{status: http.StatusConflict, msg: "Calendar limit reached", code: "quota"}
	}
	if err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to create the task list"}
	}
	return c, nil
}

func (s *Server) handleTaskInbox(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.inbox(r.Context(), sessionUser(r.Context()))
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"calendar_id": c.ID})
}

// taskArea is a KyCalendar-only group of task lists.
type taskArea struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Lists []string `json:"lists"`
}

var viewID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// areasOf reads the user's areas, dropping lists they can no longer read.
func (s *Server) areasOf(ctx context.Context, user *store.User, lists []*store.Calendar) ([]taskArea, *apiError) {
	raw, err := s.store.Tasks().Areas(ctx, user.ID)
	if err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load areas"}
	}
	areas := []taskArea{}
	if raw != "" && json.Unmarshal([]byte(raw), &areas) != nil {
		areas = []taskArea{}
	}
	for i := range areas {
		areas[i].Lists = slices.DeleteFunc(areas[i].Lists, func(id string) bool {
			return !slices.ContainsFunc(lists, func(c *store.Calendar) bool { return c.ID == id })
		})
		if areas[i].Lists == nil {
			areas[i].Lists = []string{}
		}
	}
	return areas, nil
}

// taskLists are the calendars holding VTODO the user can read.
func (s *Server) taskLists(ctx context.Context, user *store.User) ([]*store.Calendar, *apiError) {
	cals, _, err := s.visibleCalendars(ctx, user)
	if err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to list calendars"}
	}
	return slices.DeleteFunc(cals, func(c *store.Calendar) bool { return !slices.Contains(c.Components, ical.CompToDo) }), nil
}

func (s *Server) handleGetAreas(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	lists, apiErr := s.taskLists(r.Context(), user)
	if apiErr == nil {
		var areas []taskArea
		if areas, apiErr = s.areasOf(r.Context(), user, lists); apiErr == nil {
			s.writeJSON(w, http.StatusOK, areas)
			return
		}
	}
	s.writeAPIError(w, apiErr)
}

func (s *Server) handlePutAreas(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	var areas []taskArea
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10)).Decode(&areas); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	lists, apiErr := s.taskLists(r.Context(), user)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	if len(areas) > maxAreas {
		s.writeAPIError(w, badTask(fmt.Sprintf("At most %d areas", maxAreas)))
		return
	}
	seen := map[string]bool{}
	for i, a := range areas {
		a.Name = strings.TrimSpace(a.Name)
		if !viewID.MatchString(a.ID) || seen[a.ID] || a.Name == "" || !cleanText(a.Name, maxNameBytes, false) || len(a.Lists) > maxAreaLists {
			s.writeAPIError(w, badTask("Each area needs a unique id and a name of 1 to 100 bytes"))
			return
		}
		seen[a.ID] = true
		for _, id := range a.Lists {
			if !slices.ContainsFunc(lists, func(c *store.Calendar) bool { return c.ID == id }) {
				s.writeAPIError(w, errNoCalendar)
				return
			}
		}
		if a.Lists == nil {
			a.Lists = []string{}
		}
		areas[i] = a
	}
	if areas == nil {
		areas = []taskArea{}
	}
	data, _ := json.Marshal(areas)
	if err := s.store.Tasks().SetAreas(r.Context(), user.ID, string(data)); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save areas")
		return
	}
	s.writeJSON(w, http.StatusOK, areas)
}

type savedView struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Query taskFilter `json:"query"`
}

func (s *Server) handleListViews(w http.ResponseWriter, r *http.Request) {
	vs, err := s.store.Tasks().Views(r.Context(), sessionUser(r.Context()).ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load views")
		return
	}
	out := []savedView{}
	for _, v := range vs {
		sv := savedView{ID: v.ID, Name: v.Name}
		_ = json.Unmarshal([]byte(v.Query), &sv.Query)
		out = append(out, sv)
	}
	s.writeJSON(w, http.StatusOK, out)
}

// decodeView validates a saved view's name and query at the boundary.
func decodeView(r *http.Request) (string, string, *apiError) {
	var b struct {
		Name  string          `json:"name"`
		Query json.RawMessage `json:"query"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<10)).Decode(&b); err != nil {
		return "", "", &apiError{status: http.StatusBadRequest, msg: "Invalid JSON body"}
	}
	name := strings.TrimSpace(b.Name)
	if name == "" || !cleanText(name, maxNameBytes, false) {
		return "", "", badTask("A view's name is 1 to 100 bytes")
	}
	var f taskFilter
	dec := json.NewDecoder(bytes.NewReader(b.Query))
	dec.DisallowUnknownFields()
	if len(b.Query) > maxViewBytes || dec.Decode(&f) != nil {
		return "", "", badTask("A view's query is a filter object of at most 2 KiB")
	}
	if apiErr := f.check(); apiErr != nil {
		return "", "", apiErr
	}
	q, _ := json.Marshal(f)
	return name, string(q), nil
}

func (s *Server) handleCreateView(w http.ResponseWriter, r *http.Request) {
	name, q, apiErr := decodeView(r)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	v := &store.TaskView{ID: "view_" + crypto.RandomHex(8), UserID: sessionUser(r.Context()).ID, Name: name, Query: q, CreatedAt: time.Now()}
	err := s.store.Tasks().CreateView(r.Context(), v, maxViews)
	if errors.Is(err, store.ErrQuotaExceeded) {
		s.writeAPIError(w, &apiError{status: http.StatusConflict, msg: fmt.Sprintf("At most %d saved views", maxViews), code: "quota"})
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the view")
		return
	}
	sv := savedView{ID: v.ID, Name: name}
	_ = json.Unmarshal([]byte(q), &sv.Query)
	s.writeJSON(w, http.StatusCreated, sv)
}

func (s *Server) handleUpdateView(w http.ResponseWriter, r *http.Request) {
	name, q, apiErr := decodeView(r)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	err := s.store.Tasks().UpdateView(r.Context(), sessionUser(r.Context()).ID, r.PathValue("id"), name, q)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such view")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the view")
		return
	}
	sv := savedView{ID: r.PathValue("id"), Name: name}
	_ = json.Unmarshal([]byte(q), &sv.Query)
	s.writeJSON(w, http.StatusOK, sv)
}

func (s *Server) handleDeleteView(w http.ResponseWriter, r *http.Request) {
	err := s.store.Tasks().DeleteView(r.Context(), sessionUser(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such view")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the view")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTaskImage serves inline image n of a task the caller can read, typed by its bytes, as a
// sandboxed download-safe response.
func (s *Server) handleTaskImage(w http.ResponseWriter, r *http.Request) {
	c, _ := s.calendarFor(w, r, r.PathValue("cal"))
	if c == nil {
		return
	}
	_, cal, apiErr := s.loadTaskObject(r.Context(), c, r.PathValue("uid"))
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	imgs := tasks.Images(cal)
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= len(imgs) {
		s.writeError(w, http.StatusNotFound, "No such image")
		return
	}
	h := w.Header()
	h.Set("Content-Type", imgs[n].Type)
	h.Set("Content-Disposition", "inline")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write(imgs[n].Data)
}

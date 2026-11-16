package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// fold lowercases s and drops its combining marks ("Café" is "cafe"), so both backends match
// the way SQLite's FTS5 tokenizer does.
func fold(s string) string {
	out, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err != nil {
		out = s
	}
	return strings.ToLower(out)
}

// TaskStore keeps what KyCalendar knows about tasks beyond CalDAV: each user's areas, the log
// of web completions, saved views and the search index. Task content lives in calendar objects.
type TaskStore interface {
	// Areas is the user's areas JSON, "" when none were saved.
	Areas(ctx context.Context, userID string) (string, error)
	SetAreas(ctx context.Context, userID, data string) error

	// LogCompletion records c once per user, task and occurrence; a repeat is ignored.
	LogCompletion(ctx context.Context, c TaskCompletion) error
	// UndoCompletion deletes the completion of that occurrence.
	UndoCompletion(ctx context.Context, userID, calendarID, uid, occurrence string) error
	// Completions are the user's completions at or after since, oldest first.
	Completions(ctx context.Context, userID string, since time.Time) ([]TaskCompletion, error)

	Views(ctx context.Context, userID string) ([]TaskView, error)
	// CreateView inserts v unless the user already has max views (ErrQuotaExceeded).
	CreateView(ctx context.Context, v *TaskView, max int) error
	// UpdateView and DeleteView reach only the user's own view (ErrNotFound otherwise).
	UpdateView(ctx context.Context, userID, id, name, query string) error
	DeleteView(ctx context.Context, userID, id string) error

	// IndexTasks makes the calendar's search rows equal docs, rewriting only changed ETags.
	IndexTasks(ctx context.Context, calendarID string, docs []TaskDoc) error
	// SearchTasks is the "calendarID/uid" of each indexed task in calendarIDs holding every
	// word, as a prefix.
	SearchTasks(ctx context.Context, calendarIDs, words []string) (map[string]bool, error)

	// KyPostFlags is whether flagged mail makes tasks for the user (default on).
	KyPostFlags(ctx context.Context, userID string) (bool, error)
	SetKyPostFlags(ctx context.Context, userID string, on bool) error

	// PruneTasks deletes completions before before and search rows of deleted calendars.
	PruneTasks(ctx context.Context, before time.Time) error
}

type TaskCompletion struct {
	UserID, CalendarID, UID, Occurrence string
	At                                  time.Time
}

type TaskView struct {
	ID, UserID, Name, Query string
	CreatedAt               time.Time
}

// TaskDoc is one task's searchable text (title, notes and tags).
type TaskDoc struct{ UID, ETag, Body string }

type taskStore struct{ store *SQLStore }

func (s *SQLStore) Tasks() TaskStore { return &taskStore{s} }

func (t *taskStore) q(query string) string { return t.store.rebind(query) }

func (t *taskStore) Areas(ctx context.Context, userID string) (string, error) {
	var data string
	err := t.store.db.QueryRowContext(ctx, t.q(`SELECT data FROM task_areas WHERE user_id = ?`), userID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return data, err
}

func (t *taskStore) SetAreas(ctx context.Context, userID, data string) error {
	_, err := t.store.db.ExecContext(ctx, t.q(`INSERT INTO task_areas (user_id, data) VALUES (?, ?)
ON CONFLICT (user_id) DO UPDATE SET data = excluded.data`), userID, data)
	return err
}

func (t *taskStore) LogCompletion(ctx context.Context, c TaskCompletion) error {
	_, err := t.store.db.ExecContext(ctx, t.q(`INSERT INTO task_completions (user_id, calendar_id, uid, occurrence, completed_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (user_id, calendar_id, uid, occurrence) DO NOTHING`),
		c.UserID, c.CalendarID, c.UID, c.Occurrence, c.At.Unix())
	return err
}

func (t *taskStore) UndoCompletion(ctx context.Context, userID, calendarID, uid, occurrence string) error {
	_, err := t.store.db.ExecContext(ctx, t.q(`DELETE FROM task_completions WHERE user_id = ? AND calendar_id = ? AND uid = ? AND occurrence = ?`),
		userID, calendarID, uid, occurrence)
	return err
}

func (t *taskStore) Completions(ctx context.Context, userID string, since time.Time) ([]TaskCompletion, error) {
	rows, err := t.store.db.QueryContext(ctx, t.q(`SELECT calendar_id, uid, occurrence, completed_at FROM task_completions WHERE user_id = ? AND completed_at >= ? ORDER BY completed_at`), userID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskCompletion
	for rows.Next() {
		c := TaskCompletion{UserID: userID}
		var at int64
		if err := rows.Scan(&c.CalendarID, &c.UID, &c.Occurrence, &at); err != nil {
			return nil, err
		}
		c.At = time.Unix(at, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func (t *taskStore) Views(ctx context.Context, userID string) ([]TaskView, error) {
	rows, err := t.store.db.QueryContext(ctx, t.q(`SELECT id, name, query, created_at FROM task_views WHERE user_id = ? ORDER BY created_at, id`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskView
	for rows.Next() {
		v := TaskView{UserID: userID}
		var at int64
		if err := rows.Scan(&v.ID, &v.Name, &v.Query, &at); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(at, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *taskStore) CreateView(ctx context.Context, v *TaskView, max int) error {
	tx, err := t.store.lockedTx(ctx, "task-views:"+v.UserID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, t.q(`SELECT COUNT(*) FROM task_views WHERE user_id = ?`), v.UserID).Scan(&n); err != nil {
		return err
	}
	if n >= max {
		return ErrQuotaExceeded
	}
	if _, err := tx.ExecContext(ctx, t.q(`INSERT INTO task_views (id, user_id, name, query, created_at) VALUES (?, ?, ?, ?, ?)`),
		v.ID, v.UserID, v.Name, v.Query, v.CreatedAt.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (t *taskStore) UpdateView(ctx context.Context, userID, id, name, query string) error {
	res, err := t.store.db.ExecContext(ctx, t.q(`UPDATE task_views SET name = ?, query = ? WHERE user_id = ? AND id = ?`), name, query, userID, id)
	return affected(res, err)
}

func (t *taskStore) DeleteView(ctx context.Context, userID, id string) error {
	res, err := t.store.db.ExecContext(ctx, t.q(`DELETE FROM task_views WHERE user_id = ? AND id = ?`), userID, id)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return cmpErr(err, ErrNotFound)
	}
	return nil
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func (t *taskStore) IndexTasks(ctx context.Context, calendarID string, docs []TaskDoc) error {
	rows, err := t.store.db.QueryContext(ctx, t.q(`SELECT uid, etag FROM task_search WHERE calendar_id = ?`), calendarID)
	if err != nil {
		return err
	}
	have := map[string]string{}
	for rows.Next() {
		var uid, etag string
		if err := rows.Scan(&uid, &etag); err != nil {
			rows.Close()
			return err
		}
		have[uid] = etag
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	want := map[string]TaskDoc{}
	for _, d := range docs {
		want[d.UID] = d
	}
	var stale []string
	var fresh []TaskDoc
	for uid, etag := range have {
		if d, ok := want[uid]; !ok || d.ETag != etag {
			stale = append(stale, uid)
		}
	}
	for uid, d := range want {
		if have[uid] != d.ETag {
			fresh = append(fresh, d)
		}
	}
	if len(stale) == 0 && len(fresh) == 0 {
		return nil
	}
	tx, err := t.store.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, uid := range stale {
		if _, err := tx.ExecContext(ctx, t.q(`DELETE FROM task_search WHERE calendar_id = ? AND uid = ?`), calendarID, uid); err != nil {
			return err
		}
	}
	for _, d := range fresh {
		if _, err := tx.ExecContext(ctx, t.q(`INSERT INTO task_search (calendar_id, uid, etag, body) VALUES (?, ?, ?, ?)`), calendarID, d.UID, d.ETag, fold(d.Body)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (t *taskStore) SearchTasks(ctx context.Context, calendarIDs, words []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(calendarIDs) == 0 || len(words) == 0 {
		return out, nil
	}
	args := []any{}
	var where string
	if t.store.driver == "postgres" {
		var conds []string
		for _, w := range words {
			conds = append(conds, `body ILIKE ? ESCAPE '\'`)
			args = append(args, "%"+likeEscape(fold(w))+"%")
		}
		where = strings.Join(conds, " AND ")
	} else {
		var terms []string
		for _, w := range words {
			terms = append(terms, `"`+strings.ReplaceAll(fold(w), `"`, `""`)+`"*`)
		}
		where = `task_search MATCH ?`
		args = append(args, strings.Join(terms, " AND "))
	}
	where += ` AND calendar_id IN (?` + strings.Repeat(", ?", len(calendarIDs)-1) + `)`
	for _, id := range calendarIDs {
		args = append(args, id)
	}
	rows, err := t.store.db.QueryContext(ctx, t.q(`SELECT calendar_id, uid FROM task_search WHERE `+where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cal, uid string
		if err := rows.Scan(&cal, &uid); err != nil {
			return nil, err
		}
		out[cal+"/"+uid] = true
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (t *taskStore) PruneTasks(ctx context.Context, before time.Time) error {
	if _, err := t.store.db.ExecContext(ctx, t.q(`DELETE FROM task_completions WHERE completed_at < ?`), before.Unix()); err != nil {
		return err
	}
	_, err := t.store.db.ExecContext(ctx, `DELETE FROM task_search WHERE calendar_id NOT IN (SELECT id FROM calendars)`)
	return err
}

// TaskGoal is a user's goal over a period, linked to areas and lists (KyCalendar-only).
type TaskGoal struct {
	ID, UserID, Title, Period, Starts, Ends string
	Areas, Lists                            []string
	CreatedAt                               time.Time
}

// TaskGoalStore is the goals part of TaskStore.
type TaskGoalStore interface {
	Goals(ctx context.Context, userID string) ([]TaskGoal, error)
	// CreateGoal inserts g unless the user already has max goals (ErrQuotaExceeded).
	CreateGoal(ctx context.Context, g *TaskGoal, max int) error
	// UpdateGoal and DeleteGoal reach only the user's own goal (ErrNotFound otherwise).
	UpdateGoal(ctx context.Context, g *TaskGoal) error
	DeleteGoal(ctx context.Context, userID, id string) error
}

func (s *SQLStore) TaskGoals() TaskGoalStore { return &taskStore{s} }

func (t *taskStore) Goals(ctx context.Context, userID string) ([]TaskGoal, error) {
	rows, err := t.store.db.QueryContext(ctx, t.q(`SELECT id, title, period, starts, ends, areas, lists, created_at FROM task_goals WHERE user_id = ? ORDER BY created_at, id`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskGoal
	for rows.Next() {
		g := TaskGoal{UserID: userID}
		var areas, lists string
		var at int64
		if err := rows.Scan(&g.ID, &g.Title, &g.Period, &g.Starts, &g.Ends, &areas, &lists, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(areas), &g.Areas)
		_ = json.Unmarshal([]byte(lists), &g.Lists)
		g.CreatedAt = time.Unix(at, 0).UTC()
		out = append(out, g)
	}
	return out, rows.Err()
}

func (t *taskStore) CreateGoal(ctx context.Context, g *TaskGoal, max int) error {
	tx, err := t.store.lockedTx(ctx, "task-goals:"+g.UserID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, t.q(`SELECT COUNT(*) FROM task_goals WHERE user_id = ?`), g.UserID).Scan(&n); err != nil {
		return err
	}
	if n >= max {
		return ErrQuotaExceeded
	}
	areas, _ := json.Marshal(g.Areas)
	lists, _ := json.Marshal(g.Lists)
	if _, err := tx.ExecContext(ctx, t.q(`INSERT INTO task_goals (id, user_id, title, period, starts, ends, areas, lists, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		g.ID, g.UserID, g.Title, g.Period, g.Starts, g.Ends, string(areas), string(lists), g.CreatedAt.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (t *taskStore) UpdateGoal(ctx context.Context, g *TaskGoal) error {
	areas, _ := json.Marshal(g.Areas)
	lists, _ := json.Marshal(g.Lists)
	res, err := t.store.db.ExecContext(ctx, t.q(`UPDATE task_goals SET title = ?, period = ?, starts = ?, ends = ?, areas = ?, lists = ? WHERE user_id = ? AND id = ?`),
		g.Title, g.Period, g.Starts, g.Ends, string(areas), string(lists), g.UserID, g.ID)
	return affected(res, err)
}

func (t *taskStore) DeleteGoal(ctx context.Context, userID, id string) error {
	res, err := t.store.db.ExecContext(ctx, t.q(`DELETE FROM task_goals WHERE user_id = ? AND id = ?`), userID, id)
	return affected(res, err)
}

func (t *taskStore) KyPostFlags(ctx context.Context, userID string) (bool, error) {
	var on bool
	err := t.store.db.QueryRowContext(ctx, t.q(`SELECT kypost_flags FROM task_prefs WHERE user_id = ?`), userID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return on, err
}

func (t *taskStore) SetKyPostFlags(ctx context.Context, userID string, on bool) error {
	_, err := t.store.db.ExecContext(ctx, t.q(`INSERT INTO task_prefs (user_id, kypost_flags) VALUES (?, ?)
ON CONFLICT (user_id) DO UPDATE SET kypost_flags = excluded.kypost_flags`), userID, on)
	return err
}

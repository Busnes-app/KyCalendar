package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// VideoMeeting is one KyMessages room for one event object, and its outbox: a row is due
// while Rev > DoneRev. State is open, closing (a DELETE is owed) or closed (kept, so the ID is
// never reused).
type VideoMeeting struct {
	ID, UserID, CalendarID, Name, State string
	MemberURL                           string // the room link, "" until a PUT answered
	Synced                              string // digest of the last PUT KyMessages accepted
	LastError                           string // a fixed code, never a message
	Rev, DoneRev                        int64
	Attempts                            int
	NextAttempt, CreatedAt              time.Time
}

// MeetingSwitch is a web edit's video meeting switch: Open with a fresh NewID starts a room
// unless one is open; !Open closes it.
type MeetingSwitch struct {
	Open  bool
	NewID string
}

const meetingCols = `id, user_id, calendar_id, name, state, member_url, synced, last_error, rev, done_rev, attempts, next_attempt_at, created_at`

func scanMeeting(row interface{ Scan(...any) error }) (*VideoMeeting, error) {
	var m VideoMeeting
	err := row.Scan(&m.ID, &m.UserID, &m.CalendarID, &m.Name, &m.State, &m.MemberURL, &m.Synced, &m.LastError,
		&m.Rev, &m.DoneRev, &m.Attempts, &m.NextAttempt, &m.CreatedAt)
	return &m, err
}

// touchMeeting runs in every object write's transaction: any change makes the object's open
// meeting due, a delete or a switch off owes a close, a switch on opens one.
func (c *calendarStore) touchMeeting(ctx context.Context, tx *sql.Tx, calendarID, name, writerID string, sw *MeetingSwitch, deleted bool) error {
	if deleted || (sw != nil && !sw.Open) {
		return c.closeMeetings(ctx, tx, `calendar_id = ? AND name = ?`, calendarID, name)
	}
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, c.q(`UPDATE video_meetings SET rev = rev + 1, attempts = 0, next_attempt_at = ?
WHERE calendar_id = ? AND name = ? AND state = 'open'`), now, calendarID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 || sw == nil || writerID == "" {
		return nil
	}
	_, err = tx.ExecContext(ctx, c.q(`INSERT INTO video_meetings (id, user_id, calendar_id, name, state, next_attempt_at, created_at)
VALUES (?, ?, ?, ?, 'open', ?, ?)`), sw.NewID, writerID, calendarID, name, now, now)
	return err
}

// closeMeetings owes a close for every open meeting matching where.
func (c *calendarStore) closeMeetings(ctx context.Context, tx *sql.Tx, where string, args ...any) error {
	_, err := tx.ExecContext(ctx, c.q(`UPDATE video_meetings SET state = 'closing', rev = rev + 1, attempts = 0, next_attempt_at = ?
WHERE state = 'open' AND `+where), append([]any{time.Now().UTC()}, args...)...)
	return err
}

func (c *calendarStore) OpenMeeting(ctx context.Context, calendarID, name string) (*VideoMeeting, error) {
	m, err := scanMeeting(c.store.db.QueryRowContext(ctx, c.q(`SELECT `+meetingCols+` FROM video_meetings
WHERE calendar_id = ? AND name = ? AND state = 'open'`), calendarID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (c *calendarStore) OpenMeetings(ctx context.Context, calendarID string) (map[string]*VideoMeeting, error) {
	list, err := c.meetings(ctx, `WHERE calendar_id = ? AND state = 'open'`, calendarID)
	byName := make(map[string]*VideoMeeting, len(list))
	for _, m := range list {
		byName[m.Name] = m
	}
	return byName, err
}

func (c *calendarStore) DueMeetings(ctx context.Context, now time.Time, limit int) ([]*VideoMeeting, error) {
	return c.meetings(ctx, `WHERE state <> 'closed' AND rev > done_rev AND next_attempt_at <= ? ORDER BY created_at, id LIMIT ?`, now.UTC(), limit)
}

func (c *calendarStore) meetings(ctx context.Context, rest string, args ...any) ([]*VideoMeeting, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT `+meetingCols+` FROM video_meetings `+rest), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VideoMeeting
	for rows.Next() {
		m, err := scanMeeting(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (c *calendarStore) SyncedMeeting(ctx context.Context, id string, rev int64, synced, memberURL string) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE video_meetings SET done_rev = CASE WHEN done_rev > ? THEN done_rev ELSE ? END,
synced = ?, member_url = ?, last_error = '', attempts = 0 WHERE id = ? AND state = 'open'`), rev, rev, synced, memberURL, id)
	return err
}

func (c *calendarStore) ClosedMeeting(ctx context.Context, id string) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE video_meetings SET state = 'closed', done_rev = rev, last_error = '' WHERE id = ?`), id)
	return err
}

func (c *calendarStore) RetryMeeting(ctx context.Context, id string, next time.Time, lastError string) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE video_meetings SET attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE id = ?`), next.UTC(), lastError, id)
	return err
}

// FailedMeeting records a refusal retrying cannot fix: nothing more is sent until the event changes.
func (c *calendarStore) FailedMeeting(ctx context.Context, id string, rev int64, lastError string) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE video_meetings SET done_rev = CASE WHEN done_rev > ? THEN done_rev ELSE ? END,
last_error = ?, attempts = 0 WHERE id = ?`), rev, rev, lastError, id)
	return err
}

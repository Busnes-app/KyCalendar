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
	Synced                              string // digest of the last PUT KyMessages accepted; "" repeats it
	LastError                           string // a fixed code, never a message
	GuestsError                         string // why KyMessages took no guests last time, or ""
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

const meetingCols = `id, user_id, calendar_id, name, state, member_url, synced, last_error, guests_error, rev, done_rev, attempts, next_attempt_at, created_at`

func scanMeeting(row interface{ Scan(...any) error }) (*VideoMeeting, error) {
	var m VideoMeeting
	err := row.Scan(&m.ID, &m.UserID, &m.CalendarID, &m.Name, &m.State, &m.MemberURL, &m.Synced, &m.LastError, &m.GuestsError,
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
	return c.meetings(ctx, `WHERE state <> 'closed' AND rev > done_rev AND next_attempt_at <= ? AND `+orgServes("(SELECT org_id FROM users WHERE users.id = video_meetings.user_id)")+` ORDER BY created_at, id LIMIT ?`, now.UTC(), limit)
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

// MeetingGuest is an attendee outside the organisation on a meeting: Key is all KyMessages
// knows of them; SealedURL their guest link, sealed by the caller, "" until KyMessages gave one.
type MeetingGuest struct{ Email, Key, SealedURL string }

func (c *calendarStore) MeetingGuests(ctx context.Context, meetingID string) ([]MeetingGuest, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT email, guest_key, sealed_url FROM meeting_guests WHERE meeting_id = ? ORDER BY email`), meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MeetingGuest
	for rows.Next() {
		var g MeetingGuest
		if err := rows.Scan(&g.Email, &g.Key, &g.SealedURL); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetMeetingGuests replaces the meeting's guests and records guestsError and refreshAt (nil:
// no daily refresh) in one transaction.
func (c *calendarStore) SetMeetingGuests(ctx context.Context, meetingID string, guests []MeetingGuest, guestsError string, refreshAt *time.Time) error {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM meeting_guests WHERE meeting_id = ?`), meetingID); err != nil {
		return err
	}
	for _, g := range guests {
		if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO meeting_guests (meeting_id, email, guest_key, sealed_url) VALUES (?, ?, ?, ?)`), meetingID, g.Email, g.Key, g.SealedURL); err != nil {
			return err
		}
	}
	var at any
	if refreshAt != nil {
		at = refreshAt.UTC()
	}
	if _, err := tx.ExecContext(ctx, c.q(`UPDATE video_meetings SET guests_error = ?, refresh_at = ? WHERE id = ?`), guestsError, at, meetingID); err != nil {
		return err
	}
	return tx.Commit()
}

// RefreshMeetings makes every open meeting whose refresh time has come due again, its PUT
// repeated even if unchanged.
func (c *calendarStore) RefreshMeetings(ctx context.Context, now time.Time) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE video_meetings SET rev = rev + 1, attempts = 0, next_attempt_at = ?, refresh_at = NULL, synced = ''
WHERE state = 'open' AND refresh_at IS NOT NULL AND refresh_at <= ?`), now.UTC(), now.UTC())
	return err
}

// GuestLink finds email's sealed guest link on the open meeting senderID runs for event uid.
// pending: that meeting has a change KyMessages has not answered yet. ErrNotFound: no meeting.
func (c *calendarStore) GuestLink(ctx context.Context, senderID, uid, email string) (sealed string, pending bool, err error) {
	var due int
	err = c.store.db.QueryRowContext(ctx, c.q(`SELECT COALESCE(g.sealed_url, ''), CASE WHEN m.rev > m.done_rev THEN 1 ELSE 0 END
FROM video_meetings m JOIN calendar_objects o ON o.calendar_id = m.calendar_id AND o.name = m.name
LEFT JOIN meeting_guests g ON g.meeting_id = m.id AND g.email = ?
WHERE m.user_id = ? AND m.state = 'open' AND o.uid = ?`), email, senderID, uid).Scan(&sealed, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	return sealed, due == 1, err
}

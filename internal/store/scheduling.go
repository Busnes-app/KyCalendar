package store

import (
	"context"
	"database/sql"
	"time"
)

// OutboxMessage is one queued iTIP message for one recipient.
type OutboxMessage struct {
	ID          string
	DedupeKey   string // unique: queueing the same message again is a no-op
	SenderID    string // the user the message speaks for
	Method      string
	Recipient   string // calendar user address; internal or external is decided at delivery
	Body        []byte
	Status      string // pending, delivered or failed
	Attempts    int
	NextAttempt time.Time
	LastError   string
	CreatedAt   time.Time
}

// Planner runs inside an object write's transaction with the object's previous bytes (nil when
// it is new). Returned data, when non-nil, replaces what is stored (on a delete it is ignored);
// returned rows are queued in the same transaction, so a message exists exactly when its write does.
type Planner func(previous []byte) (data []byte, rows []*OutboxMessage, err error)

func (c *calendarStore) runPlan(ctx context.Context, tx *sql.Tx, o *CalendarObject, exists bool, plan Planner) ([]*OutboxMessage, error) {
	if plan == nil {
		return nil, nil
	}
	var previous []byte
	if exists {
		if err := tx.QueryRowContext(ctx, c.q(`SELECT data FROM calendar_objects WHERE calendar_id = ? AND name = ?`), o.CalendarID, o.Name).Scan(&previous); err != nil {
			return nil, err
		}
	}
	data, rows, err := plan(previous)
	if err != nil {
		return nil, err
	}
	if data != nil {
		o.Data = data
	}
	return rows, nil
}

// queue inserts rows in the write's transaction. Pending bodies count against lim: one sender's
// against MaxBytes, and every pending body plus every stored object against MaxTotalBytes, so
// a meeting with many attendees cannot fill the disk or the backup capsule. Over either limit
// the whole write is refused (ErrQuotaExceeded).
func (c *calendarStore) queue(ctx context.Context, tx *sql.Tx, rows []*OutboxMessage, lim OwnerLimits) error {
	if len(rows) == 0 {
		return nil
	}
	var adding int64
	for _, m := range rows {
		adding += int64(len(m.Body))
	}
	if lim.MaxBytes > 0 {
		var pending int64
		if err := tx.QueryRowContext(ctx, c.q(`SELECT COALESCE(SUM(LENGTH(body)), 0) FROM scheduling_outbox WHERE status = 'pending' AND sender_id = ?`),
			rows[0].SenderID).Scan(&pending); err != nil {
			return err
		}
		if pending+adding > lim.MaxBytes {
			return ErrQuotaExceeded
		}
	}
	if lim.MaxTotalBytes > 0 {
		var used int64
		if err := tx.QueryRowContext(ctx, c.q(`SELECT (SELECT bytes FROM calendar_usage WHERE id = 1) +
(SELECT COALESCE(SUM(LENGTH(body)), 0) FROM scheduling_outbox WHERE status = 'pending')`)).Scan(&used); err != nil {
			return err
		}
		if used+adding > lim.MaxTotalBytes {
			return ErrQuotaExceeded
		}
	}
	now := time.Now().UTC()
	for _, m := range rows {
		_, err := tx.ExecContext(ctx, c.q(`INSERT INTO scheduling_outbox
(id, dedupe_key, sender_id, method, recipient, body, status, attempts, next_attempt_at, last_error, created_at)
VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, '', ?) ON CONFLICT (dedupe_key) DO NOTHING`),
			m.ID, m.DedupeKey, m.SenderID, m.Method, m.Recipient, m.Body, now, now)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *calendarStore) DueOutbox(ctx context.Context, now time.Time, limit int) ([]*OutboxMessage, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT id, dedupe_key, sender_id, method, recipient, body, status, attempts, next_attempt_at, last_error, created_at
FROM scheduling_outbox WHERE status = 'pending' AND next_attempt_at <= ? ORDER BY created_at, id LIMIT ?`), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OutboxMessage
	for rows.Next() {
		var m OutboxMessage
		if err := rows.Scan(&m.ID, &m.DedupeKey, &m.SenderID, &m.Method, &m.Recipient, &m.Body, &m.Status,
			&m.Attempts, &m.NextAttempt, &m.LastError, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (c *calendarStore) FinishOutbox(ctx context.Context, id, status string, next time.Time, lastError string) error {
	// A finished message keeps its dedupe key and outcome, never its body.
	res, err := c.store.db.ExecContext(ctx, c.q(`UPDATE scheduling_outbox SET status = ?, attempts = attempts + 1, next_attempt_at = ?, last_error = ?,
body = CASE WHEN ? = 'pending' THEN body ELSE ? END
WHERE id = ? AND status = 'pending'`), status, next.UTC(), lastError, status, []byte{}, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (c *calendarStore) PruneOutbox(ctx context.Context, before time.Time) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`DELETE FROM scheduling_outbox WHERE status <> 'pending' AND created_at < ?`), before.UTC())
	return err
}

func (u *userStore) CalendarUser(ctx context.Context, email string) (*User, error) {
	rows, err := u.store.db.QueryContext(ctx, u.store.rebind(`SELECT id FROM users
WHERE LOWER(email) = LOWER(?) AND email <> '' AND role = 'user' AND status = 'active' LIMIT 2`), email)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		return nil, ErrNotFound
	}
	return u.GetUserByID(ctx, ids[0])
}

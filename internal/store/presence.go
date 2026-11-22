package store

import (
	"context"
	"database/sql"
	"time"
)

// PresencePush is one member's busy-block outbox row: due while Rev > DoneRev.
type PresencePush struct {
	UserID   string
	Rev      int64
	Attempts int
}

// PresenceRefresh is how often a member's busy blocks are sent again without a change.
const PresenceRefresh = 6 * time.Hour

// touchPresence runs in every object write's transaction: the owner of a personal calendar is
// owed a push.
func (c *calendarStore) touchPresence(ctx context.Context, tx *sql.Tx, calendarID string) error {
	_, err := tx.ExecContext(ctx, c.q(`UPDATE presence_pushes SET rev = rev + 1, attempts = 0, next_attempt_at = ?
WHERE user_id = (SELECT owner_id FROM calendars WHERE id = ? AND owner_kind = 'user')`), time.Now().UTC(), calendarID)
	return err
}

func (c *calendarStore) RefreshPresence(ctx context.Context, now time.Time) error {
	now = now.UTC()
	if _, err := c.store.db.ExecContext(ctx, c.q(`INSERT INTO presence_pushes (user_id, next_attempt_at)
SELECT u.id, ? FROM users u WHERE u.role = 'user' AND u.sso_subject <> ''
AND NOT EXISTS (SELECT 1 FROM presence_pushes p WHERE p.user_id = u.id)`), now); err != nil {
		return err
	}
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE presence_pushes SET rev = rev + 1, attempts = 0, next_attempt_at = ?, refresh_at = NULL
WHERE refresh_at IS NOT NULL AND refresh_at <= ?`), now, now)
	return err
}

func (c *calendarStore) DuePresence(ctx context.Context, now time.Time, limit int) ([]PresencePush, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT user_id, rev, attempts FROM presence_pushes
WHERE rev > done_rev AND next_attempt_at <= ? AND `+orgServes("(SELECT org_id FROM users WHERE users.id = presence_pushes.user_id)")+` ORDER BY next_attempt_at, user_id LIMIT ?`), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PresencePush
	for rows.Next() {
		var p PresencePush
		if err := rows.Scan(&p.UserID, &p.Rev, &p.Attempts); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (c *calendarStore) PushedPresence(ctx context.Context, userID string, rev int64, refresh time.Time) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE presence_pushes SET done_rev = CASE WHEN done_rev > ? THEN done_rev ELSE ? END,
attempts = 0, refresh_at = ? WHERE user_id = ?`), rev, rev, refresh.UTC(), userID)
	return err
}

func (c *calendarStore) RetryPresence(ctx context.Context, userID string, next time.Time) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`UPDATE presence_pushes SET attempts = attempts + 1, next_attempt_at = ? WHERE user_id = ?`), next.UTC(), userID)
	return err
}

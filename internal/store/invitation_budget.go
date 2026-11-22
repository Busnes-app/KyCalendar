package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const InviteBytesLimit int64 = 64 << 20

// InviteLimit is how many people one sender may newly invite per hour. Re-sending a meeting to
// people already on it does not count, so one meeting of the largest size (1000 attendees)
// always fits; the limit bounds how fast one account can fill other people's calendars.
const InviteLimit = 1000

// InviteLimitError refuses a write that would invite more people than InviteLimit this hour.
type InviteLimitError struct{ Until time.Time }

func (e InviteLimitError) Error() string {
	return fmt.Sprintf("You have invited more than %d people in the last hour. You can send new invitations again at %s UTC.",
		InviteLimit, e.Until.UTC().Format("15:04"))
}

// admitInvitees counts n new invitees in sender's window, a security_windows row on database
// time, in the write's own transaction: every replica counts against one limit, a write that
// fails spends nothing, and no second connection is taken while calendar-total is held.
func (c *calendarStore) admitInvitees(ctx context.Context, tx *sql.Tx, sender string, n int) error {
	if n == 0 {
		return nil
	}
	now := (&coordinationStore{c.store}).nowMillis()
	key := "invites:" + sender
	if n <= InviteLimit {
		res, err := tx.ExecContext(ctx, c.q(`INSERT INTO security_windows (key, hits, expires_at) VALUES (?, ?, `+now+` + ?)
ON CONFLICT (key) DO UPDATE SET
hits = CASE WHEN security_windows.expires_at <= `+now+` THEN excluded.hits ELSE security_windows.hits + excluded.hits END,
expires_at = CASE WHEN security_windows.expires_at <= `+now+` THEN excluded.expires_at ELSE security_windows.expires_at END
WHERE security_windows.expires_at <= `+now+` OR security_windows.hits + excluded.hits <= ?`), key, n, time.Hour.Milliseconds(), InviteLimit)
		if err != nil {
			return err
		}
		if k, err := res.RowsAffected(); err != nil || k == 1 {
			return err
		}
	}
	left := time.Hour.Milliseconds()
	if err := tx.QueryRowContext(ctx, c.q(`SELECT expires_at - `+now+` FROM security_windows WHERE key = ? AND expires_at > `+now), key).Scan(&left); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return InviteLimitError{Until: time.Now().Add(time.Duration(left) * time.Millisecond)}
}

type InviteByteLimitError struct{ Until time.Time }

func (e InviteByteLimitError) Error() string {
	return fmt.Sprintf("You have reached the %d MiB hourly invitation byte budget. You can send invitations again at %s UTC.", InviteBytesLimit>>20, e.Until.UTC().Format("15:04"))
}

// Admission commits with the queue under calendar-total. Failed saves spend no bytes, and
// deduplicated rows are not billed twice.
func (c *calendarStore) admitInviteBytes(ctx context.Context, tx *sql.Tx, sender string, adding int64, now time.Time) error {
	if adding == 0 {
		return nil
	}
	var start, used int64
	err := tx.QueryRowContext(ctx, c.q(`SELECT window_start, bytes FROM invitation_byte_budget WHERE sender_id = ?`), sender).Scan(&start, &used)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) || now.Unix()-start >= 3600 {
		start, used = now.Unix(), 0
	}
	if used+adding > InviteBytesLimit {
		return InviteByteLimitError{Until: time.Unix(start, 0).Add(time.Hour)}
	}
	_, err = tx.ExecContext(ctx, c.q(`INSERT INTO invitation_byte_budget (sender_id,window_start,bytes,org_id) VALUES (?,?,?,`+userOrg+`) ON CONFLICT (sender_id) DO UPDATE SET window_start=excluded.window_start, bytes=excluded.bytes`), sender, start, used+adding, sender)
	return err
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const InviteBytesLimit int64 = 64 << 20

type InviteByteLimitError struct{ Until time.Time }

func (e InviteByteLimitError) Error() string {
	return fmt.Sprintf("You have reached the %d MiB hourly invitation byte budget. You can send invitations again at %s UTC.", InviteBytesLimit>>20, e.Until.UTC().Format("15:04"))
}

// Admission commits with the queue under calendar-total. Failed saves spend no bytes, and
// deduplicated rows are not billed twice. Unlike the older head-count limiter this survives restart.
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
	_, err = tx.ExecContext(ctx, c.q(`INSERT INTO invitation_byte_budget (sender_id,window_start,bytes) VALUES (?,?,?) ON CONFLICT (sender_id) DO UPDATE SET window_start=excluded.window_start, bytes=excluded.bytes`), sender, start, used+adding)
	return err
}

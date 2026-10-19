package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

func (c *calendarStore) CreateSubscribedCalendar(ctx context.Context, cal *Calendar, sub *Subscription, maxCalendars, maxSubscriptions int) error {
	cal.CreatedAt = time.Now().UTC()
	cal.Subscribed = true
	tx, err := c.store.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.lockOwner(ctx, tx, cal.OwnerKind, cal.OwnerID); err != nil {
		return err
	}
	var cals, subs int
	err = tx.QueryRowContext(ctx, c.q(`SELECT COUNT(*), COUNT(s.calendar_id) FROM calendars c
LEFT JOIN calendar_subscriptions s ON s.calendar_id = c.id WHERE c.owner_kind = ? AND c.owner_id = ?`), cal.OwnerKind, cal.OwnerID).Scan(&cals, &subs)
	if err != nil {
		return err
	}
	if (maxCalendars > 0 && cals >= maxCalendars) || (maxSubscriptions > 0 && subs >= maxSubscriptions) {
		return ErrQuotaExceeded
	}
	if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, color, description, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), cal.ID, cal.OwnerKind, cal.OwnerID, cal.Slug, cal.Name, cal.Color, cal.Description, cal.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return err
	}
	cal.Components = []string{"VEVENT"} // the column default: a feed mirrors events only
	sub.CalendarID, sub.OwnerID = cal.ID, cal.OwnerID
	if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendar_subscriptions
(calendar_id, url_sealed, url_host, etag, last_modified, refreshed_at, checked_at, next_check_at, failures, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), sub.CalendarID, sub.URLSealed, sub.URLHost, sub.ETag, sub.LastModified,
		utcPtr(sub.RefreshedAt), utcPtr(sub.CheckedAt), sub.NextCheckAt.UTC(), sub.Failures, sub.Error); err != nil {
		return err
	}
	return tx.Commit()
}

func utcPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

const subscriptionSelect = `SELECT s.calendar_id, c.owner_id, s.url_sealed, s.url_host, s.etag, s.last_modified,
s.refreshed_at, s.checked_at, s.next_check_at, s.failures, s.error
FROM calendar_subscriptions s JOIN calendars c ON c.id = s.calendar_id `

func scanSubscription(row interface{ Scan(...any) error }) (*Subscription, error) {
	var s Subscription
	var refreshed, checked sql.NullTime
	err := row.Scan(&s.CalendarID, &s.OwnerID, &s.URLSealed, &s.URLHost, &s.ETag, &s.LastModified,
		&refreshed, &checked, &s.NextCheckAt, &s.Failures, &s.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if refreshed.Valid {
		s.RefreshedAt = &refreshed.Time
	}
	if checked.Valid {
		s.CheckedAt = &checked.Time
	}
	return &s, err
}

func (c *calendarStore) GetSubscription(ctx context.Context, calendarID string) (*Subscription, error) {
	return scanSubscription(c.store.db.QueryRowContext(ctx, c.q(subscriptionSelect+`WHERE s.calendar_id = ?`), calendarID))
}

func (c *calendarStore) DueSubscriptions(ctx context.Context, now time.Time, limit int) ([]*Subscription, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(subscriptionSelect+`WHERE s.next_check_at <= ? ORDER BY s.next_check_at LIMIT ?`), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Subscription
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (c *calendarStore) SetSubscriptionSource(ctx context.Context, calendarID, urlSealed, urlHost string) error {
	res, err := c.store.db.ExecContext(ctx, c.q(`UPDATE calendar_subscriptions SET url_sealed = ?, url_host = ?,
etag = '', last_modified = '', failures = 0, error = '' WHERE calendar_id = ?`), urlSealed, urlHost, calendarID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (c *calendarStore) ClaimSubscriptionCheck(ctx context.Context, calendarID string, now, notAfter time.Time) (bool, error) {
	res, err := c.store.db.ExecContext(ctx, c.q(`UPDATE calendar_subscriptions SET checked_at = ?
WHERE calendar_id = ? AND (checked_at IS NULL OR checked_at <= ?)`), now.UTC(), calendarID, notAfter.UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (c *calendarStore) RecordSubscriptionCheck(ctx context.Context, s *Subscription) error {
	res, err := c.store.db.ExecContext(ctx, c.q(`UPDATE calendar_subscriptions SET etag = ?, last_modified = ?,
refreshed_at = ?, checked_at = ?, next_check_at = ?, failures = ?, error = ? WHERE calendar_id = ?`),
		s.ETag, s.LastModified, utcPtr(s.RefreshedAt), utcPtr(s.CheckedAt), s.NextCheckAt.UTC(), s.Failures, s.Error, s.CalendarID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (c *calendarStore) ReplaceSubscriptionObjects(ctx context.Context, calendarID string, objs []*CalendarObject, lim OwnerLimits) error {
	now := time.Now().UTC()
	var newBytes int64
	for _, o := range objs {
		sum := sha256.Sum256(o.Data)
		o.CalendarID, o.ETag, o.ModifiedAt = calendarID, hex.EncodeToString(sum[:]), now
		newBytes += int64(len(o.Data))
	}

	tx, err := c.store.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The same lock order as PutObject: instance, owner, then the calendar row in bump.
	if lim.MaxTotalBytes > 0 {
		if err := c.store.lockKey(ctx, tx, "calendar-total"); err != nil {
			return err
		}
	}
	var ownerKind, ownerID string
	var subscribed int
	err = tx.QueryRowContext(ctx, c.q(`SELECT c.owner_kind, c.owner_id, (SELECT COUNT(*) FROM calendar_subscriptions s WHERE s.calendar_id = c.id)
FROM calendars c WHERE c.id = ?`), calendarID).Scan(&ownerKind, &ownerID, &subscribed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && subscribed == 0) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := c.lockOwner(ctx, tx, ownerKind, ownerID); err != nil {
		return err
	}

	if lim.MaxObjects > 0 || lim.MaxBytes > 0 {
		// The owner's other calendars plus the new content; this calendar's current objects do not count.
		var n int
		var used int64
		err := tx.QueryRowContext(ctx, c.q(`SELECT COUNT(*), COALESCE(SUM(LENGTH(o.data)), 0) FROM calendar_objects o JOIN calendars c ON c.id = o.calendar_id
WHERE c.owner_kind = ? AND c.owner_id = ? AND o.calendar_id <> ?`), ownerKind, ownerID, calendarID).Scan(&n, &used)
		if err != nil {
			return err
		}
		if (lim.MaxObjects > 0 && n+len(objs) > lim.MaxObjects) || (lim.MaxBytes > 0 && used+newBytes > lim.MaxBytes) {
			return ErrQuotaExceeded
		}
	}
	if lim.MaxTotalBytes > 0 {
		var used int64
		err := tx.QueryRowContext(ctx, c.q(`SELECT bytes - COALESCE((SELECT SUM(LENGTH(data)) FROM calendar_objects WHERE calendar_id = ?), 0)
+ `+pendingOutboxBytes+` FROM calendar_usage WHERE id = 1`), calendarID).Scan(&used)
		if err != nil {
			return err
		}
		if used+newBytes > lim.MaxTotalBytes {
			return ErrQuotaExceeded
		}
	}

	current := map[string]string{} // name -> etag
	rows, err := tx.QueryContext(ctx, c.q(`SELECT name, etag FROM calendar_objects WHERE calendar_id = ?`), calendarID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, etag string
		if err := rows.Scan(&name, &etag); err != nil {
			rows.Close()
			return err
		}
		current[name] = etag
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, o := range objs {
		etag, exists := current[o.Name]
		delete(current, o.Name)
		if exists && etag == o.ETag {
			continue
		}
		if err := c.bump(ctx, tx, calendarID, o.Name, false); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendar_objects (`+objectCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (calendar_id, name) DO UPDATE SET component = excluded.component, uid = excluded.uid, etag = excluded.etag, data = excluded.data,
first_start = excluded.first_start, last_end = excluded.last_end, modified_at = excluded.modified_at`),
			o.CalendarID, o.Name, o.Component, o.UID, o.ETag, o.Data, o.FirstStart, o.LastEnd, o.ModifiedAt)
		if err != nil {
			return err
		}
	}
	for name := range current {
		if err := c.bump(ctx, tx, calendarID, name, true); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM calendar_objects WHERE calendar_id = ? AND name = ?`), calendarID, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

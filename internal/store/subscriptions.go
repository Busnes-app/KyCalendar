package store

import (
	"context"
	"crypto/hmac"
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
	org, err := c.ownerOrg(ctx, tx, cal)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, color, description, created_at, org_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`), cal.ID, cal.OwnerKind, cal.OwnerID, cal.Slug, cal.Name, cal.Color, cal.Description, cal.CreatedAt, org); err != nil {
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
	rows, err := c.store.db.QueryContext(ctx, c.q(subscriptionSelect+`WHERE s.next_check_at <= ? AND `+c.store.orgServes("c.org_id")+` ORDER BY s.next_check_at LIMIT ?`), now.UTC(), limit)
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

func (c *calendarStore) ReplaceSubscriptionObjects(ctx context.Context, calendarID string, objs []*CalendarObject, lim OwnerLimits, feedKey []byte) error {
	now := time.Now().UTC()
	var newBytes int64
	for _, o := range objs {
		o.CalendarID, o.ModifiedAt = calendarID, now
		newBytes += int64(len(o.Data))
	}

	tx, err := c.store.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The same lock order as PutObject: instance, owner, then the calendar row in bump.
	{
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
	lim, guard, err := c.orgLimits(ctx, tx, calendarID, lim)
	if err != nil {
		return err
	}

	if lim.MaxObjects > 0 || lim.MaxBytes > 0 {
		// The owner's other calendars plus the new content; this calendar's current objects do not count.
		n, used, err := c.ownerUsage(ctx, tx, ownerKind, ownerID, calendarID, "")
		if err != nil {
			return err
		}
		if (lim.MaxObjects > 0 && n+len(objs) > lim.MaxObjects) || (lim.MaxBytes > 0 && used+newBytes > lim.MaxBytes) {
			return ErrQuotaExceeded
		}
	}
	if lim.MaxTotalBytes > 0 {
		var used int64
		err := tx.QueryRowContext(ctx, c.q(`SELECT bytes - COALESCE((SELECT SUM(`+objBytes+`) FROM calendar_objects WHERE calendar_id = ?), 0)
+ `+pendingOutboxBytes+` + `+cancellationReserveBytes+` FROM calendar_usage WHERE id = 1`), calendarID).Scan(&used)
		if err != nil {
			return err
		}
		growth, err := c.cancellationGrowth(ctx, tx, calendarID, "", nil)
		if err != nil {
			return err
		}
		if used+newBytes+sumGrowth(growth) > lim.MaxTotalBytes {
			return ErrQuotaExceeded
		}
	}

	// A calendar with a key takes each fetched object as a base sealed to its public key, and an
	// ETag that is an HMAC under the deployment's feed key, never the plaintext's digest (Readers,
	// feed refresh). Without a key the rows stay as today.
	version, pub, keyed, err := c.store.currentCalendarKey(ctx, tx, calendarID)
	if err != nil {
		return err
	}
	if keyed && len(feedKey) == 0 {
		return errors.New("store: a subscribed calendar with a key needs the feed key")
	}
	for _, o := range objs {
		o.ETag = subscriptionETag(keyed, feedKey, o)
	}

	type row struct {
		etag    string
		sealed  []byte
		version int64
	}
	current := map[string]row{}
	rows, err := tx.QueryContext(ctx, c.q(`SELECT name, etag, sealed, parts_version FROM calendar_objects WHERE calendar_id = ?`), calendarID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		var r row
		if err := rows.Scan(&name, &r.etag, &r.sealed, &r.version); err != nil {
			rows.Close()
			return err
		}
		current[name] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, o := range objs {
		cur, exists := current[o.Name]
		delete(current, o.Name)
		// A row is unchanged only in the format the calendar has now: a legacy row in a keyed
		// calendar, or a sealed one beside a plaintext refresh, is written again.
		if exists && cur.etag == o.ETag && (cur.sealed != nil) == keyed {
			continue
		}
		if err := c.bump(ctx, tx, calendarID, o.Name, false); err != nil {
			return err
		}
		w := sealedWrite{data: o.Data, version: cur.version}
		if keyed {
			if w, err = c.workerBase(ctx, tx, o, w, version, pub); err != nil {
				return err
			}
		}
		if err := c.dropWorkerKeys(ctx, tx, calendarID, cur.sealed); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendar_objects (`+objectCols+`, sealed, parts_version, plain_size, org_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, `+calendarOrg+`)
ON CONFLICT (calendar_id, name) DO UPDATE SET component = excluded.component, uid = excluded.uid, etag = excluded.etag, data = excluded.data,
first_start = excluded.first_start, last_end = excluded.last_end, modified_at = excluded.modified_at,
sealed = excluded.sealed, parts_version = excluded.parts_version, plain_size = excluded.plain_size`),
			o.CalendarID, o.Name, o.Component, o.UID, o.ETag, w.data, o.FirstStart, o.LastEnd, o.ModifiedAt, "", "", "",
			w.sealed, w.version, w.plainSize, o.CalendarID)
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
		if err := c.dropWorkerKeys(ctx, tx, calendarID, current[name].sealed); err != nil {
			return err
		}
	}
	if err := guard.check(ctx, c, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// subscriptionETag is a feed object's ETag: the SHA-256 of its bytes, as before, in a calendar
// without a key; in a keyed one an HMAC of the calendar, name and bytes under the deployment's
// feed key, so it stays the same across refreshes of an unchanged object (no refetch) and a
// database reader cannot confirm a guess with it (Residual risks, keyed fingerprints).
func subscriptionETag(keyed bool, feedKey []byte, o *CalendarObject) string {
	if !keyed {
		sum := sha256.Sum256(o.Data)
		return hex.EncodeToString(sum[:])
	}
	return feedETag(feedKey, o.CalendarID, o.Name, o.Data)
}

func feedETag(feedKey []byte, calendarID, name string, data []byte) string {
	m := hmac.New(sha256.New, feedKey)
	m.Write([]byte(calendarID + "\x00" + name + "\x00"))
	m.Write(data)
	return hex.EncodeToString(m.Sum(nil))
}

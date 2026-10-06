package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type calendarStore struct{ store *SQLStore }

func (c *calendarStore) q(query string) string { return c.store.rebind(query) }

func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

// lockOwner serializes quota-checked writes per owner for the rest of tx. A transaction-scoped
// advisory lock also covers an owner with no calendar rows yet, which row locks cannot. SQLite
// needs nothing: its single connection already serializes transactions.
func (c *calendarStore) lockOwner(ctx context.Context, tx *sql.Tx, ownerKind, ownerID string) error {
	return c.lockKey(ctx, tx, "calendar-owner:"+ownerKind+":"+ownerID)
}

func (c *calendarStore) lockKey(ctx context.Context, tx *sql.Tx, key string) error {
	if c.store.driver != "postgres" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

// quotaTx begins a transaction for lockOwner callers. Postgres pins READ COMMITTED so each
// statement after the lock sees writes committed before it; a server default of REPEATABLE READ
// would freeze the snapshot first. SQLite takes the default.
func (c *calendarStore) quotaTx(ctx context.Context) (*sql.Tx, error) {
	var opts *sql.TxOptions
	if c.store.driver == "postgres" {
		opts = &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	}
	return c.store.db.BeginTx(ctx, opts)
}

func (c *calendarStore) CreateCalendar(ctx context.Context, cal *Calendar, maxPerOwner int) error {
	cal.CreatedAt = time.Now().UTC()
	tx, err := c.quotaTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if maxPerOwner > 0 {
		if err := c.lockOwner(ctx, tx, cal.OwnerKind, cal.OwnerID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, c.q(`SELECT COUNT(*) FROM calendars WHERE owner_kind = ? AND owner_id = ?`), cal.OwnerKind, cal.OwnerID).Scan(&n); err != nil {
			return err
		}
		if n >= maxPerOwner {
			return ErrQuotaExceeded
		}
	}
	_, err = tx.ExecContext(ctx, c.q(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, color, description, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), cal.ID, cal.OwnerKind, cal.OwnerID, cal.Slug, cal.Name, cal.Color, cal.Description, cal.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return tx.Commit()
}

const calendarCols = `id, owner_kind, owner_id, slug, name, color, description, seq, created_at`

func scanCalendar(row interface{ Scan(...any) error }) (*Calendar, error) {
	var cal Calendar
	err := row.Scan(&cal.ID, &cal.OwnerKind, &cal.OwnerID, &cal.Slug, &cal.Name, &cal.Color, &cal.Description, &cal.Seq, &cal.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &cal, err
}

func (c *calendarStore) GetCalendarBySlug(ctx context.Context, ownerKind, ownerID, slug string) (*Calendar, error) {
	return scanCalendar(c.store.db.QueryRowContext(ctx, c.q(`SELECT `+calendarCols+` FROM calendars
WHERE owner_kind = ? AND owner_id = ? AND slug = ?`), ownerKind, ownerID, slug))
}

func (c *calendarStore) ListCalendarsByOwner(ctx context.Context, ownerKind, ownerID string) ([]*Calendar, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT `+calendarCols+` FROM calendars
WHERE owner_kind = ? AND owner_id = ? ORDER BY created_at, id`), ownerKind, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Calendar
	for rows.Next() {
		cal, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cal)
	}
	return out, rows.Err()
}

func (c *calendarStore) UpdateCalendar(ctx context.Context, id string, name, description, color *string) error {
	res, err := c.store.db.ExecContext(ctx, c.q(`UPDATE calendars SET
name = COALESCE(?, name), description = COALESCE(?, description), color = COALESCE(?, color) WHERE id = ?`),
		name, description, color, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const objectCols = `calendar_id, name, uid, etag, data, first_start, last_end, modified_at`

func scanObject(row interface{ Scan(...any) error }) (*CalendarObject, error) {
	var o CalendarObject
	var last sql.NullInt64
	err := row.Scan(&o.CalendarID, &o.Name, &o.UID, &o.ETag, &o.Data, &o.FirstStart, &last, &o.ModifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if last.Valid {
		o.LastEnd = &last.Int64
	}
	return &o, err
}

func (c *calendarStore) queryObjects(ctx context.Context, query string, args ...any) ([]*CalendarObject, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CalendarObject
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (c *calendarStore) GetObject(ctx context.Context, calendarID, name string) (*CalendarObject, error) {
	return scanObject(c.store.db.QueryRowContext(ctx, c.q(`SELECT `+objectCols+` FROM calendar_objects
WHERE calendar_id = ? AND name = ?`), calendarID, name))
}

func (c *calendarStore) ListObjects(ctx context.Context, calendarID string) ([]*CalendarObject, error) {
	return c.queryObjects(ctx, `SELECT `+objectCols+` FROM calendar_objects WHERE calendar_id = ? ORDER BY name`, calendarID)
}

func (c *calendarStore) ListObjectsInRange(ctx context.Context, calendarID string, start, end int64) ([]*CalendarObject, error) {
	return c.queryObjects(ctx, `SELECT `+objectCols+` FROM calendar_objects
WHERE calendar_id = ? AND first_start < ? AND (last_end IS NULL OR last_end > ?) ORDER BY name`, calendarID, end, start)
}

// bump advances the calendar sequence and logs one change, inside tx.
func (c *calendarStore) bump(ctx context.Context, tx *sql.Tx, calendarID, name string, deleted bool) error {
	var seq int64
	if err := tx.QueryRowContext(ctx, c.q(`UPDATE calendars SET seq = seq + 1 WHERE id = ? RETURNING seq`), calendarID).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	flag := 0
	if deleted {
		flag = 1
	}
	_, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendar_changes (calendar_id, seq, name, deleted, changed_at) VALUES (?, ?, ?, ?, ?)`),
		calendarID, seq, name, flag, time.Now().UTC())
	return err
}

func (c *calendarStore) currentETag(ctx context.Context, tx *sql.Tx, calendarID, name string) (string, bool, error) {
	var etag string
	err := tx.QueryRowContext(ctx, c.q(`SELECT etag FROM calendar_objects WHERE calendar_id = ? AND name = ?`), calendarID, name).Scan(&etag)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return etag, err == nil, err
}

func (c *calendarStore) PutObject(ctx context.Context, o *CalendarObject, ifMatch string, ifNoneMatch bool, lim OwnerLimits) (bool, error) {
	sum := sha256.Sum256(o.Data)
	o.ETag = hex.EncodeToString(sum[:])
	o.ModifiedAt = time.Now().UTC()

	tx, err := c.quotaTx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if lim.MaxTotalBytes > 0 {
		// Instance lock before any owner lock: one order for every writer that takes both.
		if err := c.lockKey(ctx, tx, "calendar-total"); err != nil {
			return false, err
		}
	}
	limited := lim.MaxObjects > 0 || lim.MaxBytes > 0
	var ownerKind, ownerID string
	if limited {
		err := tx.QueryRowContext(ctx, c.q(`SELECT owner_kind, owner_id FROM calendars WHERE id = ?`), o.CalendarID).Scan(&ownerKind, &ownerID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		} else if err != nil {
			return false, err
		}
		// Owner lock before the calendar row lock in bump: one order for every quota-checked writer.
		if err := c.lockOwner(ctx, tx, ownerKind, ownerID); err != nil {
			return false, err
		}
	}

	// bump first: its row lock serializes writers per calendar, so the checks below are race-free.
	if err := c.bump(ctx, tx, o.CalendarID, o.Name, false); err != nil {
		return false, err
	}
	etag, exists, err := c.currentETag(ctx, tx, o.CalendarID, o.Name)
	if err != nil {
		return false, err
	}
	if (ifNoneMatch && exists) || (ifMatch != "" && (!exists || (ifMatch != "*" && etag != ifMatch))) {
		return false, ErrPreconditionFailed
	}
	var other string
	err = tx.QueryRowContext(ctx, c.q(`SELECT name FROM calendar_objects WHERE calendar_id = ? AND uid = ? AND name <> ?`),
		o.CalendarID, o.UID, o.Name).Scan(&other)
	if err == nil {
		return false, ErrUIDConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if limited {
		// Usage of every other object the owner has; the one being replaced does not count.
		var n int
		var used int64
		err := tx.QueryRowContext(ctx, c.q(`SELECT COUNT(*), COALESCE(SUM(LENGTH(o.data)), 0) FROM calendar_objects o JOIN calendars c ON c.id = o.calendar_id
WHERE c.owner_kind = ? AND c.owner_id = ? AND NOT (o.calendar_id = ? AND o.name = ?)`), ownerKind, ownerID, o.CalendarID, o.Name).Scan(&n, &used)
		if err != nil {
			return false, err
		}
		if (lim.MaxObjects > 0 && !exists && n >= lim.MaxObjects) || (lim.MaxBytes > 0 && used+int64(len(o.Data)) > lim.MaxBytes) {
			return false, ErrQuotaExceeded
		}
	}
	if lim.MaxTotalBytes > 0 {
		var used int64
		err := tx.QueryRowContext(ctx, c.q(`SELECT COALESCE(SUM(LENGTH(data)), 0) FROM calendar_objects WHERE NOT (calendar_id = ? AND name = ?)`),
			o.CalendarID, o.Name).Scan(&used)
		if err != nil {
			return false, err
		}
		if used+int64(len(o.Data)) > lim.MaxTotalBytes {
			return false, ErrQuotaExceeded
		}
	}

	_, err = tx.ExecContext(ctx, c.q(`INSERT INTO calendar_objects (`+objectCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (calendar_id, name) DO UPDATE SET uid = excluded.uid, etag = excluded.etag, data = excluded.data,
first_start = excluded.first_start, last_end = excluded.last_end, modified_at = excluded.modified_at`),
		o.CalendarID, o.Name, o.UID, o.ETag, o.Data, o.FirstStart, o.LastEnd, o.ModifiedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return false, ErrUIDConflict
		}
		return false, err
	}
	return !exists, tx.Commit()
}

func (c *calendarStore) DeleteObject(ctx context.Context, calendarID, name, ifMatch string) error {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.bump(ctx, tx, calendarID, name, true); err != nil {
		return err
	}
	etag, exists, err := c.currentETag(ctx, tx, calendarID, name)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if ifMatch != "" && ifMatch != "*" && etag != ifMatch {
		return ErrPreconditionFailed
	}
	if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM calendar_objects WHERE calendar_id = ? AND name = ?`), calendarID, name); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *calendarStore) ChangesSince(ctx context.Context, calendarID string, seq int64) ([]CalendarChange, error) {
	var minSeq int64
	if err := c.store.db.QueryRowContext(ctx, c.q(`SELECT min_sync_seq FROM calendars WHERE id = ?`), calendarID).Scan(&minSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if seq < minSeq {
		return nil, ErrSyncTokenExpired
	}
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT seq, name, deleted FROM calendar_changes
WHERE calendar_id = ? AND seq > ? ORDER BY seq`), calendarID, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarChange
	for rows.Next() {
		var ch CalendarChange
		var deleted int
		if err := rows.Scan(&ch.Seq, &ch.Name, &deleted); err != nil {
			return nil, err
		}
		ch.Deleted = deleted == 1
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (c *calendarStore) PruneChanges(ctx context.Context, before time.Time) error {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, c.q(`UPDATE calendars SET min_sync_seq = (
SELECT MAX(seq) FROM calendar_changes WHERE calendar_changes.calendar_id = calendars.id AND changed_at < ?)
WHERE EXISTS (SELECT 1 FROM calendar_changes WHERE calendar_changes.calendar_id = calendars.id AND changed_at < ?)`),
		before.UTC(), before.UTC())
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM calendar_changes WHERE changed_at < ?`), before.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *calendarStore) SyncEpoch(ctx context.Context) (string, error) {
	var v string
	err := c.store.db.QueryRowContext(ctx, c.q(`SELECT value FROM calendar_meta WHERE key = 'sync_epoch'`)).Scan(&v)
	return v, err
}

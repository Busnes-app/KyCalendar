package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// BookingType is one of a member's booking pages. Windows is JSON (booking.Window list); the
// rules are validated by the caller.
type BookingType struct {
	ID, UserID, Slug, Title, Zone, CalendarID, Windows                             string
	Enabled, MeetingRoom                                                           bool
	DurationMin, BufferBeforeMin, BufferAfterMin, NoticeMin, HorizonDays, DailyCap int
	CreatedAt, UpdatedAt                                                           time.Time
}

// Booking is a hold (Status held, until ExpiresAt) or a confirmed booking whose event is the
// object CalendarID/ObjectName. Start, End and ExpiresAt are unix seconds. ConfirmHash and
// ManageHash are SHA-256 hex of the link tokens.
type Booking struct {
	ID, TypeID, UserID, Status                            string
	Email, EmailHash, Name, Note, ConfirmHash, ManageHash string
	CalendarID, ObjectName, UID                           string
	Start, End, ExpiresAt                                 int64
	CreatedAt                                             time.Time
}

// Booking statuses.
const (
	BookingHeld      = "held"
	BookingConfirmed = "confirmed"
)

var (
	// ErrBookingState is a booking that is not, or no longer, in the state the call needs: an
	// expired or already confirmed hold, a booking that is not confirmed.
	ErrBookingState = errors.New("store: booking is not in that state")
	// ErrTooManyHolds is an email address already holding the most open holds allowed.
	ErrTooManyHolds = errors.New("store: too many open holds for this address")
)

// BookingCheck decides inside a booking's transaction whether its slot may be taken, given the
// owner's event objects and other open holds overlapping the span asked for. Its error aborts
// the transaction and is returned as is.
type BookingCheck func(objs [][]byte, holds []*Booking) error

// BookingStore keeps booking types and bookings. Hold, Confirm, Reschedule and Cancel run in one
// transaction under the calendar-total lock every object write takes, so their check sees every
// committed event and hold and nothing commits in between.
type BookingStore interface {
	ListTypes(ctx context.Context, userID string) ([]*BookingType, error)
	GetType(ctx context.Context, userID, id string) (*BookingType, error)
	TypeBySlug(ctx context.Context, slug string) (*BookingType, error)
	// CreateType refuses with ErrQuotaExceeded when the user already has max types.
	CreateType(ctx context.Context, t *BookingType, max int) error
	UpdateType(ctx context.Context, t *BookingType) error
	DeleteType(ctx context.Context, userID, id string) error

	// OwnerEvents is the data of userID's event objects in personal, non-subscribed calendars
	// that may overlap [from, to); skipCal/skipName (when set) leaves one object out.
	OwnerEvents(ctx context.Context, userID string, from, to int64, skipCal, skipName string) ([][]byte, error)
	// OpenHolds is userID's holds open at now overlapping [from, to).
	OpenHolds(ctx context.Context, userID string, from, to, now int64) ([]*Booking, error)

	Get(ctx context.Context, id string) (*Booking, error)
	// Hold inserts b as a hold after check, refusing ErrTooManyHolds when b.EmailHash already has
	// maxPerEmail open holds.
	Hold(ctx context.Context, b *Booking, now, from, to int64, maxPerEmail int, check BookingCheck) error
	DeleteHold(ctx context.Context, id string) error
	// Confirm turns hold id, open at now, into a booking: check (other holds only), then o is
	// written through plan as a new object and the row records it with manageHash. The row
	// forgets the visitor's email, name and note: the event holds them now.
	Confirm(ctx context.Context, id string, now, from, to int64, check BookingCheck, o *CalendarObject, manageHash string, lim OwnerLimits, plan Planner) error
	// Reschedule moves confirmed booking id to [start, end): check (without its own event), then o
	// replaces its event against ifMatch.
	Reschedule(ctx context.Context, id string, start, end, now, from, to int64, check BookingCheck, o *CalendarObject, ifMatch string, lim OwnerLimits, plan Planner) error
	// Cancel deletes confirmed booking id's event through plan (an event already gone is fine) and
	// the booking row with it.
	Cancel(ctx context.Context, id string, lim OwnerLimits, plan Planner) error
	// Forget deletes booking id's row only: its event is gone already.
	Forget(ctx context.Context, id string) error
	// Prune deletes holds expired at now and bookings that ended before endedBefore.
	Prune(ctx context.Context, now, endedBefore int64) error
}

type bookingStore struct{ c *calendarStore }

func (s *bookingStore) q(query string) string { return s.c.q(query) }
func (s *bookingStore) db() *sql.DB           { return s.c.store.db }

const typeCols = `id, user_id, slug, enabled, title, duration_min, zone, windows, buffer_before_min, buffer_after_min,
notice_min, horizon_days, daily_cap, calendar_id, meeting_room, created_at, updated_at`

func scanType(row interface{ Scan(...any) error }) (*BookingType, error) {
	var t BookingType
	err := row.Scan(&t.ID, &t.UserID, &t.Slug, &t.Enabled, &t.Title, &t.DurationMin, &t.Zone, &t.Windows, &t.BufferBeforeMin,
		&t.BufferAfterMin, &t.NoticeMin, &t.HorizonDays, &t.DailyCap, &t.CalendarID, &t.MeetingRoom, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (s *bookingStore) ListTypes(ctx context.Context, userID string) ([]*BookingType, error) {
	rows, err := s.db().QueryContext(ctx, s.q(`SELECT `+typeCols+` FROM booking_types WHERE user_id = ? ORDER BY created_at, id`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BookingType
	for rows.Next() {
		t, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *bookingStore) GetType(ctx context.Context, userID, id string) (*BookingType, error) {
	return scanType(s.db().QueryRowContext(ctx, s.q(`SELECT `+typeCols+` FROM booking_types WHERE id = ? AND user_id = ?`), id, userID))
}

func (s *bookingStore) TypeBySlug(ctx context.Context, slug string) (*BookingType, error) {
	return scanType(s.db().QueryRowContext(ctx, s.q(`SELECT `+typeCols+` FROM booking_types WHERE slug = ?`), slug))
}

func (s *bookingStore) CreateType(ctx context.Context, t *BookingType, max int) error {
	tx, err := s.c.store.lockedTx(ctx, "booking-types:"+t.UserID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM booking_types WHERE user_id = ?`), t.UserID).Scan(&n); err != nil {
		return err
	}
	if n >= max {
		return ErrQuotaExceeded
	}
	t.CreatedAt = time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO booking_types (`+typeCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		t.ID, t.UserID, t.Slug, t.Enabled, t.Title, t.DurationMin, t.Zone, t.Windows, t.BufferBeforeMin, t.BufferAfterMin,
		t.NoticeMin, t.HorizonDays, t.DailyCap, t.CalendarID, t.MeetingRoom, t.CreatedAt, t.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *bookingStore) UpdateType(ctx context.Context, t *BookingType) error {
	t.UpdatedAt = time.Now().UTC()
	res, err := s.db().ExecContext(ctx, s.q(`UPDATE booking_types SET slug = ?, enabled = ?, title = ?, duration_min = ?, zone = ?, windows = ?,
buffer_before_min = ?, buffer_after_min = ?, notice_min = ?, horizon_days = ?, daily_cap = ?, calendar_id = ?, meeting_room = ?, updated_at = ?
WHERE id = ? AND user_id = ?`), t.Slug, t.Enabled, t.Title, t.DurationMin, t.Zone, t.Windows, t.BufferBeforeMin, t.BufferAfterMin,
		t.NoticeMin, t.HorizonDays, t.DailyCap, t.CalendarID, t.MeetingRoom, t.UpdatedAt, t.ID, t.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *bookingStore) DeleteType(ctx context.Context, userID, id string) error {
	res, err := s.db().ExecContext(ctx, s.q(`DELETE FROM booking_types WHERE id = ? AND user_id = ?`), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *bookingStore) ownerEvents(ctx context.Context, q querier, userID string, from, to int64, skipCal, skipName string) ([][]byte, error) {
	rows, err := q.QueryContext(ctx, s.q(`SELECT o.data FROM calendar_objects o JOIN calendars c ON c.id = o.calendar_id
WHERE c.owner_kind = 'user' AND c.owner_id = ? AND o.component = 'VEVENT'
AND NOT EXISTS (SELECT 1 FROM calendar_subscriptions s WHERE s.calendar_id = c.id)
AND o.first_start < ? AND (o.last_end IS NULL OR o.last_end >= ?) AND NOT (o.calendar_id = ? AND o.name = ?)
ORDER BY o.calendar_id, o.name`), userID, to, from, skipCal, skipName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var d []byte
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *bookingStore) OwnerEvents(ctx context.Context, userID string, from, to int64, skipCal, skipName string) ([][]byte, error) {
	return s.ownerEvents(ctx, s.db(), userID, from, to, skipCal, skipName)
}

const bookingCols = `id, type_id, user_id, status, email, email_hash, name, note, confirm_hash, manage_hash, calendar_id, object_name, uid,
start_at, end_at, expires_at, created_at`

func scanBooking(row interface{ Scan(...any) error }) (*Booking, error) {
	var b Booking
	err := row.Scan(&b.ID, &b.TypeID, &b.UserID, &b.Status, &b.Email, &b.EmailHash, &b.Name, &b.Note, &b.ConfirmHash, &b.ManageHash,
		&b.CalendarID, &b.ObjectName, &b.UID, &b.Start, &b.End, &b.ExpiresAt, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

func (s *bookingStore) openHolds(ctx context.Context, q querier, userID string, from, to, now int64) ([]*Booking, error) {
	rows, err := q.QueryContext(ctx, s.q(`SELECT `+bookingCols+` FROM bookings
WHERE user_id = ? AND status = 'held' AND expires_at > ? AND start_at < ? AND end_at > ? ORDER BY start_at, id`), userID, now, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *bookingStore) OpenHolds(ctx context.Context, userID string, from, to, now int64) ([]*Booking, error) {
	return s.openHolds(ctx, s.db(), userID, from, to, now)
}

func (s *bookingStore) Get(ctx context.Context, id string) (*Booking, error) {
	return scanBooking(s.db().QueryRowContext(ctx, s.q(`SELECT `+bookingCols+` FROM bookings WHERE id = ?`), id))
}

func without(holds []*Booking, id string) []*Booking {
	out := holds[:0]
	for _, h := range holds {
		if h.ID != id {
			out = append(out, h)
		}
	}
	return out
}

// run calls check, inside tx (which holds calendar-total, the lock every object write takes
// first), over what is committed now.
func (s *bookingStore) run(ctx context.Context, tx *sql.Tx, userID, selfID string, now, from, to int64, skipCal, skipName string, check BookingCheck) error {
	objs, err := s.ownerEvents(ctx, tx, userID, from, to, skipCal, skipName)
	if err != nil {
		return err
	}
	holds, err := s.openHolds(ctx, tx, userID, from, to, now)
	if err != nil {
		return err
	}
	return check(objs, without(holds, selfID))
}

func (s *bookingStore) Hold(ctx context.Context, b *Booking, now, from, to int64, maxPerEmail int, check BookingCheck) error {
	tx, err := s.c.store.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.run(ctx, tx, b.UserID, "", now, from, to, "", "", check); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM bookings WHERE email_hash = ? AND status = 'held' AND expires_at > ?`), b.EmailHash, now).Scan(&n); err != nil {
		return err
	}
	if n >= maxPerEmail {
		return ErrTooManyHolds
	}
	b.Status, b.CreatedAt = BookingHeld, time.Now().UTC()
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO bookings (`+bookingCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		b.ID, b.TypeID, b.UserID, b.Status, b.Email, b.EmailHash, b.Name, b.Note, b.ConfirmHash, b.ManageHash, b.CalendarID, b.ObjectName, b.UID,
		b.Start, b.End, b.ExpiresAt, b.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *bookingStore) DeleteHold(ctx context.Context, id string) error {
	_, err := s.db().ExecContext(ctx, s.q(`DELETE FROM bookings WHERE id = ? AND status = 'held'`), id)
	return err
}

// locked reads booking id inside tx and requires status (and, for a hold, still open at now).
func (s *bookingStore) locked(ctx context.Context, tx *sql.Tx, id, status string, now int64) (*Booking, error) {
	b, err := scanBooking(tx.QueryRowContext(ctx, s.q(`SELECT `+bookingCols+` FROM bookings WHERE id = ?`), id))
	if err != nil {
		return nil, err
	}
	if b.Status != status || (status == BookingHeld && b.ExpiresAt <= now) {
		return nil, ErrBookingState
	}
	return b, nil
}

func (s *bookingStore) Confirm(ctx context.Context, id string, now, from, to int64, check BookingCheck, o *CalendarObject, manageHash string, lim OwnerLimits, plan Planner) error {
	tx, err := s.c.store.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := s.locked(ctx, tx, id, BookingHeld, now)
	if err != nil {
		return err
	}
	if err := s.run(ctx, tx, b.UserID, id, now, from, to, "", "", check); err != nil {
		return err
	}
	if _, err := s.c.putObjectPlanned(ctx, o, "", true, lim, plan, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE bookings SET status = 'confirmed', expires_at = 0, email = '', name = '', note = '', manage_hash = ?,
calendar_id = ?, object_name = ?, uid = ? WHERE id = ?`), manageHash, o.CalendarID, o.Name, o.UID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *bookingStore) Reschedule(ctx context.Context, id string, start, end, now, from, to int64, check BookingCheck, o *CalendarObject, ifMatch string, lim OwnerLimits, plan Planner) error {
	tx, err := s.c.store.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := s.locked(ctx, tx, id, BookingConfirmed, now)
	if err != nil {
		return err
	}
	if err := s.run(ctx, tx, b.UserID, id, now, from, to, b.CalendarID, b.ObjectName, check); err != nil {
		return err
	}
	if _, err := s.c.putObjectPlanned(ctx, o, ifMatch, false, lim, plan, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE bookings SET start_at = ?, end_at = ? WHERE id = ?`), start, end, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *bookingStore) Cancel(ctx context.Context, id string, lim OwnerLimits, plan Planner) error {
	tx, err := s.c.store.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := s.locked(ctx, tx, id, BookingConfirmed, 0)
	if err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM calendar_objects WHERE calendar_id = ? AND name = ?`), b.CalendarID, b.ObjectName).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		if err := s.c.deleteObjectPlanned(ctx, b.CalendarID, b.ObjectName, "", lim, plan, tx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM bookings WHERE id = ?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *bookingStore) Forget(ctx context.Context, id string) error {
	_, err := s.db().ExecContext(ctx, s.q(`DELETE FROM bookings WHERE id = ?`), id)
	return err
}

func (s *bookingStore) Prune(ctx context.Context, now, endedBefore int64) error {
	_, err := s.db().ExecContext(ctx, s.q(`DELETE FROM bookings WHERE (status = 'held' AND expires_at <= ?) OR end_at < ?`), now, endedBefore)
	return err
}

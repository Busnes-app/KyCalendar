package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/emersion/go-ical"
)

const cancellationReserveBytes = `(SELECT COALESCE(SUM(bytes),0) FROM cancellation_reservations)`

type cancellationReservation struct {
	sender string
	bytes  int64
}

// Only the writer's scheduled REQUEST establishes cancellation liability. Subsequent writes
// of that same organizer/UID recompute it, including replies delivered without a client writer.
// Legacy/user-supplied organizer fields alone never reserve another person's quota.
func (c *calendarStore) cancellationReserve(ctx context.Context, tx *sql.Tx, o *CalendarObject, rows []*OutboxMessage) (*cancellationReservation, error) {
	var sender, previousUID string
	var previous []byte
	err := tx.QueryRowContext(ctx, c.q(`SELECT r.sender_id,o.uid,o.data FROM cancellation_reservations r JOIN calendar_objects o ON o.calendar_id=r.calendar_id AND o.name=r.name WHERE r.calendar_id=? AND r.name=?`), o.CalendarID, o.Name).Scan(&sender, &previousUID, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	authorized := false
	for _, m := range rows {
		if o.WriterID != "" && m.Method == itip.MethodRequest && m.SenderID == o.WriterID {
			sender = o.WriterID
			authorized = true
			break
		}
	}
	if sender == "" {
		return nil, nil
	}
	next, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return nil, err
	}
	org, ok := itip.Organizer(next)
	if !ok {
		return nil, nil
	}
	if !authorized {
		before, err := ical.NewDecoder(bytes.NewReader(previous)).Decode()
		if err != nil {
			return nil, err
		}
		oldOrg, ok := itip.Organizer(before)
		if !ok || oldOrg != org || previousUID != o.UID {
			return nil, nil
		}
	}
	cancels, err := itip.Outgoing(org, next, nil, time.Now())
	if err != nil {
		return nil, err
	}
	var size int64
	for _, m := range cancels {
		if m.Method != itip.MethodCancel {
			continue
		}
		var raw bytes.Buffer
		if err := ical.NewEncoder(&raw).Encode(m.Calendar); err != nil {
			return nil, err
		}
		size += int64(raw.Len()) * int64(len(m.Recipients))
	}
	if size == 0 {
		return nil, nil
	}
	return &cancellationReservation{sender: sender, bytes: size}, nil
}

func (c *calendarStore) writeCancellationReserve(ctx context.Context, tx *sql.Tx, o *CalendarObject, r *cancellationReservation) error {
	if r == nil {
		_, err := tx.ExecContext(ctx, c.q(`DELETE FROM cancellation_reservations WHERE calendar_id=? AND name=?`), o.CalendarID, o.Name)
		return err
	}
	_, err := tx.ExecContext(ctx, c.q(`INSERT INTO cancellation_reservations(calendar_id,name,sender_id,bytes) VALUES(?,?,?,?) ON CONFLICT(calendar_id,name) DO UPDATE SET sender_id=excluded.sender_id,bytes=excluded.bytes`), o.CalendarID, o.Name, r.sender, r.bytes)
	return err
}

func (c *calendarStore) reservedBytes(ctx context.Context, tx *sql.Tx, sender, excludeCalendar, excludeName string) (int64, error) {
	var used int64
	err := tx.QueryRowContext(ctx, c.q(`SELECT COALESCE(SUM(bytes),0) FROM cancellation_reservations WHERE sender_id=? AND NOT(calendar_id=? AND (?='' OR name=?))`), sender, excludeCalendar, excludeName, excludeName).Scan(&used)
	return used, err
}

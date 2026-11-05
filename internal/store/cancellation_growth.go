package store

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/emersion/go-ical"
)

// Copy growth is derived from durable trusted sponsorship, source reservations and pending
// CANCELs. It therefore survives source deletion without another reservation table or migration.
// Conservatively include every notice for the same trusted sender/UID: recipient address or
// grant changes must not create unreserved growth. Actual delivery still checks stable recipient,
// organizer, recurrence and sequence bindings. Legacy and paid content never enter this set.
// ponytail: scan the quota-bounded copy set under calendar-total; index/cache only if measured.
func (c *calendarStore) cancellationGrowth(ctx context.Context, tx *sql.Tx, excludeCalendar, excludeName string, extra []*OutboxMessage) (map[string]int64, error) {
	type sponsoredCopy struct {
		sender, uid string
		data        []byte
	}
	var copies []sponsoredCopy
	rows, err := tx.QueryContext(ctx, c.q(`SELECT invitation_sender,uid,data FROM calendar_objects WHERE invitation_sender<>'' AND invitation_recipient<>'' AND NOT(calendar_id=? AND (?='' OR name=?))`), excludeCalendar, excludeName, excludeName)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var o sponsoredCopy
		if err := rows.Scan(&o.sender, &o.uid, &o.data); err != nil {
			rows.Close()
			return nil, err
		}
		copies = append(copies, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	growth := map[string]int64{}
	if len(copies) == 0 {
		return growth, nil
	}
	type identity struct{ sender, uid string }
	needed := map[identity]bool{}
	for _, o := range copies {
		needed[identity{o.sender, o.uid}] = true
	}
	messages := map[identity][]*ical.Calendar{}
	add := func(sender, uid string, cal *ical.Calendar) {
		key := identity{sender, uid}
		if needed[key] {
			messages[key] = append(messages[key], cal)
		}
	}
	// Only source rows whose scheduled writer established a future reservation are trusted.
	rows, err = tx.QueryContext(ctx, c.q(`SELECT r.sender_id,o.uid,o.data FROM cancellation_reservations r JOIN calendar_objects o ON o.calendar_id=r.calendar_id AND o.name=r.name WHERE NOT(o.calendar_id=? AND (?='' OR o.name=?))`), excludeCalendar, excludeName, excludeName)
	if err != nil {
		return nil, err
	}
	type source struct {
		sender, uid string
		data        []byte
	}
	var sources []source
	for rows.Next() {
		var o source
		if err := rows.Scan(&o.sender, &o.uid, &o.data); err != nil {
			rows.Close()
			return nil, err
		}
		sources = append(sources, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, o := range sources {
		if !needed[identity{o.sender, o.uid}] {
			continue
		}
		cal, err := ical.NewDecoder(bytes.NewReader(o.data)).Decode()
		if err != nil {
			return nil, err
		}
		org, ok := itip.Organizer(cal)
		if !ok {
			continue
		}
		future, err := itip.Outgoing(org, cal, nil, time.Now())
		if err != nil {
			return nil, err
		}
		for _, m := range future {
			if m.Method == itip.MethodCancel {
				// Match the future fan-out to its eventual individual pending receipts.
				for range m.Recipients {
					add(o.sender, o.uid, m.Calendar)
				}
			}
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT sender_id,body FROM scheduling_outbox WHERE status='pending' AND method='CANCEL'`)
	if err != nil {
		return nil, err
	}
	var pending []source
	for rows.Next() {
		var o source
		if err := rows.Scan(&o.sender, &o.data); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, m := range extra {
		if m.Method == itip.MethodCancel {
			pending = append(pending, source{sender: m.SenderID, data: m.Body})
		}
	}
	for _, o := range pending {
		cal, err := itip.Decode(o.data)
		if err != nil {
			// Invalid messages cannot mutate a copy.
			continue
		}
		method, uid, err := itip.Validate(cal)
		if err != nil || method != itip.MethodCancel {
			continue
		}
		add(o.sender, uid, cal)
	}
	for _, o := range copies {
		liability, err := cancellationCopyGrowth(o.data, messages[identity{o.sender, o.uid}]...)
		if err != nil {
			return nil, err
		}
		if int64(len(o.data))+liability > calendar.MaxObjectSize {
			return nil, ErrQuotaExceeded
		}
		growth[o.sender] += liability
	}
	return growth, nil
}

// A whole sponsored cancellation deletes its copy. For a partial notice, DeleteOne adds
// at most one EXDATE in the master's DTSTART form per named occurrence, then touches the
// master. Count those positive additions without expanding recurrence or crediting removals.
// Recipient, stale and occurrence checks can only reduce this conservative sizing bound.
func cancellationCopyGrowth(raw []byte, notices ...*ical.Calendar) (int64, error) {
	var partial []*ical.Calendar
	for _, msg := range notices {
		whole, events := false, 0
		for _, ev := range msg.Children {
			if ev.Name != ical.CompEvent {
				continue
			}
			events++
			if ev.Props.Get(ical.PropRecurrenceID) == nil {
				whole = true
				break
			}
		}
		if !whole && events > 0 {
			partial = append(partial, msg)
		}
	}
	if len(partial) == 0 {
		return 0, nil
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return 0, err
	}
	org, ok := itip.Organizer(cal)
	if !ok {
		return 0, nil
	}
	var noticeCount, occurrences int64
	for _, msg := range partial {
		other, ok := itip.Organizer(msg)
		if !ok || other != org {
			continue
		}
		noticeCount++
		for _, ev := range msg.Children {
			if ev.Name == ical.CompEvent {
				occurrences++
			}
		}
	}
	if noticeCount == 0 {
		return 0, nil
	}
	// The bound is linear in notices and named components. Decode/canonicalize once per
	// physical copy, preserving exactly the sum of the individual-notice reservations.
	var canonical bytes.Buffer
	if err := ical.NewEncoder(&canonical).Encode(cal); err != nil {
		return 0, err
	}
	master, _ := calendar.Master(cal)
	var exdateBytes, touchBytes int64
	if master != nil {
		start := master.Props.Get(ical.PropDateTimeStart)
		if start == nil {
			return 0, nil
		} // cancellation cannot create a missing DTSTART
		// Use formProp's generated width, including leniently parsed short DTSTART values.
		// Retaining all stored parameters can only overcount its DATE/TZID parameters.
		exdate := *start
		exdate.Name = ical.PropExceptionDates
		switch {
		case start.ValueType() == ical.ValueDate:
			exdate.Value = "20060102"
		case start.Params.Get(ical.ParamTimezoneID) != "":
			// A four-digit UTC key can cross into local year -1 or 10000.
			exdate.Value = "100000101T150405"
		case strings.HasSuffix(start.Value, "Z"):
			exdate.Value = "20060102T150405Z"
		default:
			exdate.Value = "20060102T150405"
		}
		master.Props.Add(&exdate)
		var encoded bytes.Buffer
		if err := ical.NewEncoder(&encoded).Encode(cal); err != nil {
			return 0, err
		}
		exdateBytes = max(0, int64(encoded.Len()-canonical.Len()))
		// touch writes two UTC timestamps and a signed-int sequence without parameters.
		// Reserve complete fields, never reusable credits for shrinking their old values.
		touchBytes = int64(len("DTSTAMP:20060102T150405Z\r\nLAST-MODIFIED:20060102T150405Z\r\nSEQUENCE:-9223372036854775808\r\n"))
	}
	growth := (max(0, int64(canonical.Len()-len(raw)))+touchBytes)*noticeCount + exdateBytes*occurrences
	if int64(len(raw))+growth > calendar.MaxObjectSize {
		return 0, ErrQuotaExceeded
	}
	return growth, nil
}

func sumGrowth(growth map[string]int64) int64 {
	var total int64
	for _, n := range growth {
		total += n
	}
	return total
}

// Called after object/source changes but before allocating fresh queue bodies. Both the old
// pending liabilities and the proposed notices participate in the same locked admission.
func (c *calendarStore) checkCancellationGrowth(ctx context.Context, tx *sql.Tx, lim OwnerLimits, extra []*OutboxMessage) error {
	current, err := c.cancellationGrowth(ctx, tx, "", "", nil)
	if err != nil {
		return err
	}
	next := current
	if len(extra) > 0 {
		next, err = c.cancellationGrowth(ctx, tx, "", "", extra)
		if err != nil {
			return err
		}
	}
	adding := map[string]int64{}
	var totalAdding int64
	for _, m := range extra {
		n := int64(len(m.Body))
		adding[m.SenderID] += n
		totalAdding += n
	}
	if lim.MaxBytes > 0 {
		for sender, n := range next {
			if n == 0 {
				continue
			}
			_, used, err := c.ownerUsage(ctx, tx, "user", sender, "", "")
			if err != nil {
				return err
			}
			if used+n-current[sender]+adding[sender] > lim.MaxBytes {
				return ErrQuotaExceeded
			}
		}
	}
	if lim.MaxTotalBytes > 0 {
		var used int64
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT bytes FROM calendar_usage WHERE id=1)+`+pendingOutboxBytes+`+`+cancellationReserveBytes).Scan(&used); err != nil {
			return err
		}
		if used+sumGrowth(next)+totalAdding > lim.MaxTotalBytes {
			return ErrQuotaExceeded
		}
	}
	return nil
}

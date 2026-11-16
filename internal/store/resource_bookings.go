package store

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/resource"
)

// ResourceBooking is the answer KyCalendar gave for one resource on one event object.
type ResourceBooking struct {
	ID, ResourceID, CalendarID, ObjectName, BookedBy string
	Status, Reason, Detail                           string
	OpenAfter                                        int64
}

// BusySpan is one reserved interval of a resource (unix seconds), never what it is for.
type BusySpan struct {
	Start, End int64
	Pending    bool
}

// ResourceRequest is a pending booking as its approvers see it: no title, no attendees.
type ResourceRequest struct {
	ID, ResourceID, ResourceName, Organizer string
	First, Last                             BusySpan
	Occurrences                             int
}

// resourceNow is the clock bookings are decided by; tests move it.
var resourceNow = time.Now

// mentionsResource reports whether data, unfolded, may name a resource attendee.
func mentionsResource(data []byte) bool {
	u := bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("\r\n "), nil), []byte("\r\n\t"), nil)
	u = bytes.ReplaceAll(bytes.ReplaceAll(u, []byte("\n "), nil), []byte("\n\t"), nil)
	return bytes.Contains(bytes.ToLower(u), []byte("@"+resource.Domain))
}

type txQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// mayBook reports whether userID, an active everyday user, may book r.
func (s *SQLStore) mayBook(ctx context.Context, q txQuerier, r *resource.Resource, userID string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, s.rebind(`SELECT COUNT(*) FROM users WHERE id = ? AND status = 'active' AND role <> 'admin'`), userID).Scan(&n)
	if err != nil || n == 0 || r.Audience == resource.Everyone {
		return n > 0, err
	}
	err = q.QueryRowContext(ctx, s.rebind(`SELECT COUNT(*) FROM resource_groups rg JOIN group_members gm ON gm.group_id = rg.group_id
WHERE rg.resource_id = ? AND gm.user_id = ?`), r.ID, userID).Scan(&n)
	return n > 0, err
}

func (c *calendarStore) objectBookings(ctx context.Context, q txQuerier, calendarID, name string) (map[string]*ResourceBooking, map[string]string, error) {
	rows, err := q.QueryContext(ctx, c.q(`SELECT id, resource_id, booked_by, status, reason, detail, fingerprint, open_after FROM resource_bookings
WHERE calendar_id = ? AND object_name = ?`), calendarID, name)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out, prints := map[string]*ResourceBooking{}, map[string]string{}
	for rows.Next() {
		b := &ResourceBooking{CalendarID: calendarID, ObjectName: name}
		var fp string
		if err := rows.Scan(&b.ID, &b.ResourceID, &b.BookedBy, &b.Status, &b.Reason, &b.Detail, &fp, &b.OpenAfter); err != nil {
			return nil, nil, err
		}
		out[b.ResourceID], prints[b.ResourceID] = b, fp
	}
	return out, prints, rows.Err()
}

func (c *calendarStore) resourceDefs(ctx context.Context, tx *sql.Tx, ids []string) (map[string]*resource.Resource, error) {
	defs := map[string]*resource.Resource{}
	for _, id := range ids {
		rs, err := c.store.resources.load(ctx, tx, "WHERE id = ?", id)
		if err != nil {
			return nil, err
		}
		if len(rs) == 1 {
			defs[id] = rs[0]
		}
	}
	return defs, nil
}

// settleResources answers for every resource o names, inside o's write transaction (which holds
// calendar-total, so busy rows cannot change underneath). It rewrites o.Data with the answers
// and patches queued REQUEST bodies to carry them. The returned func writes the ledger once the
// object row exists.
func (c *calendarStore) settleResources(ctx context.Context, tx *sql.Tx, o *CalendarObject, rows []*OutboxMessage) (func() error, error) {
	if o.Component != ical.CompEvent {
		return nil, nil
	}
	existing, prints, err := c.objectBookings(ctx, tx, o.CalendarID, o.Name)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 && !mentionsResource(o.Data) {
		return nil, nil
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return nil, calendar.ErrInvalidData
	}
	var self itip.Address
	if o.WriterID != "" {
		var email string
		err := tx.QueryRowContext(ctx, c.q(`SELECT email FROM users WHERE id = ?`), o.WriterID).Scan(&email)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		self, _ = itip.ParseAddress("mailto:" + email)
	}
	// A delivered copy names the organizer's rooms: only the organizer's object books.
	if org, ok := itip.Organizer(cal); len(existing) == 0 && ok && org != self {
		return nil, nil
	}
	ids := resource.IDs(cal)
	defs, err := c.resourceDefs(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	fp, series := resource.Fingerprint(cal), resource.SeriesFingerprint(cal)
	prevSeries := ""
	var prevCal *ical.Calendar
	if len(existing) > 0 {
		var prev []byte
		err := tx.QueryRowContext(ctx, c.q(`SELECT data FROM calendar_objects WHERE calendar_id = ? AND name = ?`), o.CalendarID, o.Name).Scan(&prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if pc, err := ical.NewDecoder(bytes.NewReader(prev)).Decode(); err == nil && prev != nil {
			prevSeries, prevCal = resource.SeriesFingerprint(pc), pc
		}
	}
	now := resourceNow()
	answers := map[string]string{}
	type change struct {
		r *resource.Resource
		d resource.Decision
	}
	var changes []change
	for i, id := range ids {
		if i >= resource.MaxPerEvent {
			continue // declined by SetAnswers; no row
		}
		// Same times: the answer stands. A decline holds nothing, so it is asked again, unless an
		// approver gave it.
		if b := existing[id]; b != nil && prints[id] == fp && (b.Status != resource.Declined || b.Reason == resource.ReasonRejected) {
			answers[id] = b.Status
			continue
		}
		r := defs[id]
		d := resource.Plan(cal, r, o.LastEnd, now)
		// The same rule and no occurrence the previous object did not have (an attendee's answer
		// to one occurrence, a cancelled one): the answer stands, whoever writes it.
		if b := existing[id]; b != nil && r != nil && r.Enabled && (d.Status != resource.Declined || d.Reason == resource.ReasonBeyond) && b.Status != resource.Declined &&
			prevCal != nil && prevSeries == series && resource.Within(prevCal, cal, now) {
			if d.Reason == resource.ReasonBeyond {
				// Keep the ledger's open range, but bind its answer to the current times.
				if _, err := tx.ExecContext(ctx, c.q(`UPDATE resource_bookings SET fingerprint = ?, updated_at = ? WHERE id = ?`), fp, now.UTC(), b.ID); err != nil {
					return nil, err
				}
				answers[id] = b.Status
				continue
			}
			d.Status = b.Status
			answers[id] = d.Status
			changes = append(changes, change{r, d})
			continue
		}
		if d.Status != resource.Declined {
			ok, err := c.store.mayBook(ctx, tx, r, o.WriterID)
			if err != nil {
				return nil, err
			}
			if !ok {
				d = resource.Decision{Status: resource.Declined, Reason: resource.ReasonNotAllowed}
			}
		}
		if d.Status != resource.Declined && len(d.Spans) > 0 {
			last := int64(0)
			for _, sp := range d.Spans {
				last = max(last, sp.End)
			}
			busy, err := c.busyOf(ctx, tx, id, d.Spans[0].Start, last, o.CalendarID, o.Name)
			if err != nil {
				return nil, err
			}
			spans := make([]resource.Span, len(busy))
			for i, b := range busy {
				spans[i] = resource.Span{Start: b.Start, End: b.End}
			}
			if hit := resource.Conflicts(d.Spans, spans); len(hit) > 0 {
				d = resource.Decision{Status: resource.Declined, Reason: resource.ReasonBusy, Detail: resource.ConflictDetail(hit)}
			}
		}
		if d.Status == resource.Declined {
			d.Spans = nil
		}
		answers[id] = d.Status
		if r != nil {
			changes = append(changes, change{r, d})
		}
	}
	if resource.SetAnswers(cal, answers, defs) {
		var buf bytes.Buffer
		if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
			return nil, calendar.ErrInvalidData
		}
		o.Data = buf.Bytes()
	}
	for _, m := range rows {
		if m.Method != itip.MethodRequest {
			continue
		}
		msg, err := ical.NewDecoder(bytes.NewReader(m.Body)).Decode()
		if err != nil {
			continue
		}
		if resource.SetAnswers(msg, answers, defs) {
			var buf bytes.Buffer
			if ical.NewEncoder(&buf).Encode(msg) == nil {
				m.Body = buf.Bytes()
			}
		}
	}
	return func() error {
		for id, b := range existing {
			if _, ok := answers[id]; !ok {
				if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM resource_bookings WHERE id = ?`), b.ID); err != nil {
					return err
				}
			}
		}
		for _, ch := range changes {
			if b := existing[ch.r.ID]; b != nil {
				if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM resource_bookings WHERE id = ?`), b.ID); err != nil {
					return err
				}
			}
			id := "rbk_" + crypto.RandomHex(12)
			if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO resource_bookings (id, resource_id, calendar_id, object_name, booked_by, status, reason, detail,
fingerprint, open_after, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
				id, ch.r.ID, o.CalendarID, o.Name, o.WriterID, ch.d.Status, ch.d.Reason, ch.d.Detail, fp, ch.d.OpenAfter, now.UTC(), now.UTC()); err != nil {
				return err
			}
			for _, sp := range ch.d.Spans {
				if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO resource_busy (booking_id, resource_id, start_at, end_at) VALUES (?, ?, ?, ?)`),
					id, ch.r.ID, sp.Start, sp.End); err != nil {
					return err
				}
			}
		}
		return nil
	}, nil
}

// busyOf is resource id's busy spans overlapping [from, to): the stored spans of accepted and
// pending bookings, and the later occurrences of series that go on past their stored spans.
// skip leaves out the bookings of one event (calendar ID and object name; empty: none).
func (c *calendarStore) busyOf(ctx context.Context, q txQuerier, id string, from, to int64, skipCal, skipName string) ([]BusySpan, error) {
	rows, err := q.QueryContext(ctx, c.q(`SELECT s.start_at, s.end_at, b.status FROM resource_busy s JOIN resource_bookings b ON b.id = s.booking_id
WHERE s.resource_id = ? AND s.start_at < ? AND s.end_at > ? AND NOT (b.calendar_id = ? AND b.object_name = ?) ORDER BY s.start_at`),
		id, to, from, skipCal, skipName)
	if err != nil {
		return nil, err
	}
	var out []BusySpan
	for rows.Next() {
		var b BusySpan
		var status string
		if err := rows.Scan(&b.Start, &b.End, &status); err != nil {
			rows.Close()
			return nil, err
		}
		b.Pending = status == resource.Pending
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, c.q(`SELECT b.status, b.open_after, o.data FROM resource_bookings b
JOIN calendar_objects o ON o.calendar_id = b.calendar_id AND o.name = b.object_name
WHERE b.resource_id = ? AND b.status <> 'declined' AND b.open_after > 0 AND b.open_after < ? AND NOT (b.calendar_id = ? AND b.object_name = ?)`),
		id, to, skipCal, skipName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var after int64
		var data []byte
		if err := rows.Scan(&status, &after, &data); err != nil {
			return nil, err
		}
		cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
		if err != nil {
			out = append(out, BusySpan{Start: max(from, after), End: to, Pending: status == resource.Pending})
			continue
		}
		for _, sp := range resource.OpenSpans(cal, after, from, to) {
			out = append(out, BusySpan{Start: sp.Start, End: sp.End, Pending: status == resource.Pending})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b BusySpan) int { return cmp.Compare(a.Start, b.Start) })
	return out, nil
}

// Bookable lists the enabled resources userID may book.
func (s *resourceStore) Bookable(ctx context.Context, userID string) ([]*resource.Resource, error) {
	rs, err := s.load(ctx, s.store.db, "WHERE enabled = ?", true)
	if err != nil {
		return nil, err
	}
	var out []*resource.Resource
	for _, r := range rs {
		ok, err := s.store.mayBook(ctx, s.store.db, r, userID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// Busy is resource id's busy spans overlapping [from, to), leaving out the bookings of event uid
// in calendarID (when both are set).
func (s *resourceStore) Busy(ctx context.Context, id string, from, to int64, calendarID, uid string) ([]BusySpan, error) {
	var name string
	if calendarID != "" && uid != "" {
		err := s.store.db.QueryRowContext(ctx, s.q(`SELECT name FROM calendar_objects WHERE calendar_id = ? AND uid = ?`), calendarID, uid).Scan(&name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	out, err := s.store.calendars.busyOf(ctx, s.store.db, id, from, to, calendarID, name)
	if out == nil {
		out = []BusySpan{}
	}
	return out, err
}

// BookingsIn is the bookings of calendarID's objects, by object name and resource ID.
func (s *resourceStore) BookingsIn(ctx context.Context, calendarID string) (map[string]map[string]*ResourceBooking, error) {
	rows, err := s.store.db.QueryContext(ctx, s.q(`SELECT id, resource_id, object_name, booked_by, status, reason, detail FROM resource_bookings WHERE calendar_id = ?`), calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]*ResourceBooking{}
	for rows.Next() {
		b := &ResourceBooking{CalendarID: calendarID}
		if err := rows.Scan(&b.ID, &b.ResourceID, &b.ObjectName, &b.BookedBy, &b.Status, &b.Reason, &b.Detail); err != nil {
			return nil, err
		}
		if out[b.ObjectName] == nil {
			out[b.ObjectName] = map[string]*ResourceBooking{}
		}
		out[b.ObjectName][b.ResourceID] = b
	}
	return out, rows.Err()
}

const approverJoin = `JOIN resource_approvers ra ON ra.resource_id = b.resource_id AND ra.user_id = ?
JOIN users au ON au.id = ra.user_id AND au.status = 'active' AND au.role <> 'admin'`

// Requests lists the pending bookings of the resources approverID approves.
func (s *resourceStore) Requests(ctx context.Context, approverID string) ([]*ResourceRequest, error) {
	rows, err := s.store.db.QueryContext(ctx, s.q(`SELECT b.id, b.resource_id, r.name, COALESCE(NULLIF(u.display_name, ''), u.username, ''),
MIN(sp.start_at), MIN(sp.end_at), MAX(sp.start_at), MAX(sp.end_at), COUNT(sp.start_at)
FROM resource_bookings b `+approverJoin+`
JOIN resources r ON r.id = b.resource_id
LEFT JOIN users u ON u.id = b.booked_by
LEFT JOIN resource_busy sp ON sp.booking_id = b.id
WHERE b.status = 'pending'
GROUP BY b.id, b.resource_id, r.name, u.display_name, u.username
ORDER BY MIN(sp.start_at), b.id`), approverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ResourceRequest{}
	for rows.Next() {
		var q ResourceRequest
		var fs, fe, ls, le sql.NullInt64
		if err := rows.Scan(&q.ID, &q.ResourceID, &q.ResourceName, &q.Organizer, &fs, &fe, &ls, &le, &q.Occurrences); err != nil {
			return nil, err
		}
		q.First, q.Last = BusySpan{Start: fs.Int64, End: fe.Int64}, BusySpan{Start: ls.Int64, End: le.Int64}
		out = append(out, &q)
	}
	return out, rows.Err()
}

// IsApprover reports whether userID approves any resource.
func (s *resourceStore) IsApprover(ctx context.Context, userID string) (bool, error) {
	var n int
	err := s.store.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM resource_approvers WHERE user_id = ?`), userID).Scan(&n)
	return n > 0, err
}

// Decide accepts or declines pending booking id as approverID, and writes the answer into the
// organizer's event, in one transaction under calendar-total. A booking approverID does not
// approve is ErrNotFound; one no longer pending is ErrBookingState.
func (s *resourceStore) Decide(ctx context.Context, id, approverID string, accept bool) error {
	tx, err := s.store.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var b ResourceBooking
	err = tx.QueryRowContext(ctx, s.q(`SELECT b.id, b.resource_id, b.calendar_id, b.object_name, b.status FROM resource_bookings b `+approverJoin+` WHERE b.id = ?`),
		approverID, id).Scan(&b.ID, &b.ResourceID, &b.CalendarID, &b.ObjectName, &b.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if b.Status != resource.Pending {
		return ErrBookingState
	}
	status, reason := resource.Accepted, ""
	if !accept {
		status, reason = resource.Declined, resource.ReasonRejected
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM resource_busy WHERE booking_id = ?`), id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE resource_bookings SET status = ?, reason = ?, decided_by = ?, updated_at = ? WHERE id = ?`),
		status, reason, approverID, time.Now().UTC(), id); err != nil {
		return err
	}
	c := s.store.calendars
	var data []byte
	if err := tx.QueryRowContext(ctx, c.q(`SELECT data FROM calendar_objects WHERE calendar_id = ? AND name = ?`), b.CalendarID, b.ObjectName).Scan(&data); err != nil {
		return err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return err
	}
	all, _, err := c.objectBookings(ctx, tx, b.CalendarID, b.ObjectName)
	if err != nil {
		return err
	}
	answers := map[string]string{}
	for rid, x := range all {
		answers[rid] = x.Status
	}
	ids := resource.IDs(cal)
	defs, err := c.resourceDefs(ctx, tx, ids)
	if err != nil {
		return err
	}
	if !resource.SetAnswers(cal, answers, defs) {
		return tx.Commit()
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return err
	}
	if err := c.bump(ctx, tx, b.CalendarID, b.ObjectName, false); err != nil {
		return err
	}
	sum := sha256.Sum256(buf.Bytes())
	if _, err := tx.ExecContext(ctx, c.q(`UPDATE calendar_objects SET data = ?, etag = ?, modified_at = ? WHERE calendar_id = ? AND name = ?`),
		buf.Bytes(), hex.EncodeToString(sum[:]), time.Now().UTC(), b.CalendarID, b.ObjectName); err != nil {
		return err
	}
	return tx.Commit()
}

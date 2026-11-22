package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// OrgQuotas are one org's quotas from /api/suite/orgs (tenancy spec D11). A nil field falls back
// to the install setting: the key is absent, or not a positive integer (a boolean for
// BookingPages).
type OrgQuotas struct {
	MaxBytes        *int64 // the org's total: stored objects, pending messages, reservations
	MaxBytesPerUser *int64 // replaces KY_CALENDAR_MAX_BYTES_PER_USER for the org's owners
	BookingPages    *bool
}

func parseOrgQuotas(raw string) OrgQuotas {
	var keys map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &keys)
	var q OrgQuotas
	for name, dst := range map[string]**int64{"max_bytes": &q.MaxBytes, "max_bytes_per_user": &q.MaxBytesPerUser} {
		var n *int64
		if json.Unmarshal(keys[name], &n) == nil && n != nil && *n > 0 {
			*dst = n
		}
	}
	var b *bool
	if json.Unmarshal(keys["booking_pages"], &b) == nil {
		q.BookingPages = b
	}
	return q
}

// OrgQuotas reads org's quotas from the mirror.
func (s *SQLStore) OrgQuotas(ctx context.Context, org OrgID) (OrgQuotas, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT quotas_json FROM orgs WHERE id = ?`), org).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return OrgQuotas{}, ErrNotFound
	}
	return parseOrgQuotas(raw), err
}

// orgGuard is the org's usage before a write that max_bytes bounds: the write is refused if it
// leaves the org over max_bytes and larger than before, so a lower quota never deletes data and
// a write that shrinks the org always passes.
type orgGuard struct {
	org         OrgID
	max, before int64
}

// orgLimits applies the quotas of calendarID's org to a limited write (lim not zero), under
// calendar-total: max_bytes_per_user replaces lim.MaxBytes, and max_bytes gives a guard to check
// before commit.
func (c *calendarStore) orgLimits(ctx context.Context, tx *sql.Tx, calendarID string, lim OwnerLimits) (OwnerLimits, *orgGuard, error) {
	if lim == (OwnerLimits{}) {
		return lim, nil, nil
	}
	var org OrgID
	var raw string
	err := tx.QueryRowContext(ctx, c.q(`SELECT c.org_id, o.quotas_json FROM calendars c JOIN orgs o ON o.id = c.org_id WHERE c.id = ?`), calendarID).Scan(&org, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return lim, nil, ErrNotFound
	} else if err != nil {
		return lim, nil, err
	}
	q := parseOrgQuotas(raw)
	if q.MaxBytesPerUser != nil && lim.MaxBytes > 0 {
		lim.MaxBytes = *q.MaxBytesPerUser
	}
	if q.MaxBytes == nil {
		return lim, nil, nil
	}
	before, err := c.orgUsed(ctx, tx, org)
	return lim, &orgGuard{org: org, max: *q.MaxBytes, before: before}, err
}

func (g *orgGuard) check(ctx context.Context, c *calendarStore, tx *sql.Tx) error {
	if g == nil {
		return nil
	}
	after, err := c.orgUsed(ctx, tx, g.org)
	if err != nil {
		return err
	}
	if after > g.max && after > g.before {
		return ErrQuotaExceeded
	}
	return nil
}

// orgUsed counts what max_bytes bounds, as ownerUsage does for one owner: the org's stored
// objects (org_usage), its pending outbox bodies, the reservations on its calendars and its
// senders' pending cancellation growth. A missing org_usage row is an error, never zero.
func (c *calendarStore) orgUsed(ctx context.Context, tx *sql.Tx, org OrgID) (int64, error) {
	var used int64
	err := tx.QueryRowContext(ctx, c.q(`SELECT u.bytes
 + (SELECT COALESCE(SUM(LENGTH(body)), 0) FROM scheduling_outbox WHERE status = 'pending' AND org_id = u.org_id)
 + (SELECT COALESCE(SUM(r.bytes), 0) FROM cancellation_reservations r JOIN calendars c ON c.id = r.calendar_id WHERE c.org_id = u.org_id)
 FROM org_usage u WHERE u.org_id = ?`), org).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("store: org %s has no usage row", org)
	} else if err != nil {
		return 0, err
	}
	growth, err := c.cancellationGrowth(ctx, tx, "", "", nil)
	if err != nil || len(growth) == 0 {
		return used, err
	}
	senders := make([]string, 0, len(growth))
	for id := range growth {
		senders = append(senders, id)
	}
	in, args := inList(senders)
	rows, err := tx.QueryContext(ctx, c.q(`SELECT id FROM users WHERE org_id = ? AND id IN (`+in+`)`), append([]any{org}, args...)...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		used += growth[id]
	}
	return used, rows.Err()
}

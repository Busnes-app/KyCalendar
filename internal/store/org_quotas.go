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
	org               OrgID
	max, before       int64
	pool              *int64 // nil exempts single/staff/default orgs and the initial bounded grace
	reported, ceiling int64
}

// orgLimits applies the quotas of calendarID's org to a write, under calendar-total:
// max_bytes_per_user replaces a per-owner byte cap, and max_bytes gives a guard to check before
// commit.
func (c *calendarStore) orgLimits(ctx context.Context, tx *sql.Tx, calendarID string, lim OwnerLimits) (OwnerLimits, *orgGuard, error) {
	var org OrgID
	var raw, mode, staff string
	var since, seq, expires, used, now int64
	var ceiling sql.NullInt64
	var inflight bool
	err := tx.QueryRowContext(ctx, c.q(`SELECT c.org_id, o.quotas_json,
COALESCE((SELECT value FROM server_settings WHERE key='tenancy_mode'), ''),
COALESCE((SELECT value FROM server_settings WHERE key='tenancy_staff_org'), ''),
o.pool_since,o.pool_seq,o.pool_ceiling,o.pool_expires_at,o.pool_inflight,o.pool_report_used,`+c.store.dbNow()+`
FROM calendars c JOIN orgs o ON o.id = c.org_id WHERE c.id = ?`), calendarID).
		Scan(&org, &raw, &mode, &staff, &since, &seq, &ceiling, &expires, &inflight, &used, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return lim, nil, ErrNotFound
	} else if err != nil {
		return lim, nil, err
	}
	q := parseOrgQuotas(raw)
	if q.MaxBytesPerUser != nil && lim.MaxBytes > 0 {
		lim.MaxBytes = *q.MaxBytesPerUser
	}
	var pool *int64
	if mode != "single" && org != DefaultOrg && string(org) != staff {
		budget := int64(0)
		if ceiling.Valid && expires > now {
			budget = ceiling.Int64
			if inflight {
				budget = min(budget, used)
			}
			pool = &budget
		} else if seq != 0 || since <= now-StorageGrantTTL || since > now {
			pool = &budget // no first grace, or an expired grant: shrinking only
		}
	}
	if q.MaxBytes == nil && pool == nil {
		return lim, nil, nil
	}
	before, err := c.orgUsed(ctx, tx, org)
	guard := &orgGuard{org: org, before: before, pool: pool, reported: used, ceiling: ceiling.Int64}
	if q.MaxBytes != nil {
		guard.max = *q.MaxBytes
	}
	return lim, guard, err
}

// senderByteLimit applies the sender's own org quota to the install's per-user limit.
func (c *calendarStore) senderByteLimit(ctx context.Context, tx *sql.Tx, sender string, fallback int64) (int64, error) {
	if fallback <= 0 {
		return fallback, nil
	}
	var raw string
	err := tx.QueryRowContext(ctx, c.q(`SELECT o.quotas_json FROM users u JOIN orgs o ON o.id=u.org_id WHERE u.id=?`), sender).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	} else if err != nil {
		return 0, err
	}
	if q := parseOrgQuotas(raw); q.MaxBytesPerUser != nil {
		return *q.MaxBytesPerUser, nil
	}
	return fallback, nil
}

func (g *orgGuard) check(ctx context.Context, c *calendarStore, tx *sql.Tx) error {
	if g == nil {
		return nil
	}
	after, err := c.orgUsed(ctx, tx, g.org)
	if err != nil {
		return err
	}
	if after > g.before && (g.max > 0 && after > g.max || g.pool != nil && after > *g.pool) {
		return ErrQuotaExceeded
	}
	// Signal after half the granted headroom is consumed, and promptly release shrunk
	// usage. The flag commits with the successful write, never with a refused one.
	grant := max(g.ceiling-g.reported, 0)
	if g.pool != nil && after != g.before && (after < g.before || after-g.reported >= (grant+1)/2) {
		_, err = tx.ExecContext(ctx, c.q(`UPDATE orgs SET pool_refresh=TRUE WHERE id=?`), g.org)
		return err
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
	growth, err := c.cancellationGrowthOf(ctx, tx, org, "", "", nil)
	return used + sumGrowth(growth), err
}

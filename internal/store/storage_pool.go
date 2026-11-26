package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// StorageGrantTTL is KyIdentity's maximum grant lifetime, in seconds. Database time is
// used both when snapshotting the report and checking writes; receipt never extends it.
const StorageGrantTTL int64 = 900
const maxStorageReportBytes int64 = 1 << 53

var ErrStorageSequence = errors.New("storage report sequence requires operator recovery")
var ErrStorageGrant = errors.New("invalid storage grant")

// ErrStorageGrantRefreshing is returned when a write is momentarily blocked by an in-flight
// storage-pool report snapshot (pool_inflight) even though the org holds an unexpired grant
// whose ceiling would admit the write. It wraps ErrQuotaExceeded so callers that do not
// retry still fail closed with 507.
var ErrStorageGrantRefreshing = fmt.Errorf("storage grant refresh in flight: %w", ErrQuotaExceeded)

// storageRefreshWaits bounds how long live write boundaries wait for an in-flight KyIdentity
// storage-pool grant refresh to commit (~1.575s total).
var storageRefreshWaits = []time.Duration{
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	400 * time.Millisecond,
	400 * time.Millisecond,
}

// RetryStorageRefresh runs fn and, if it fails with ErrStorageGrantRefreshing, retries with
// bounded backoff while respecting ctx. Callers must invoke it outside any DB transaction so
// ApplyStorageGrant can acquire calendar-total and commit between attempts.
func RetryStorageRefresh(ctx context.Context, fn func() error) error {
	err := fn()
	for _, wait := range storageRefreshWaits {
		if !errors.Is(err, ErrStorageGrantRefreshing) {
			return err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		err = fn()
	}
	return err
}

// StorageReport is an immutable usage snapshot, owned by this product rather than its
// transport. An ambiguous request is retried with this same sequence, bytes and send time.
type StorageReport struct {
	Org                               OrgID
	Seq, UsedBytes, WantBytes, SentAt int64
}

const storageCustomer = `id = ? AND id <> '` + string(DefaultOrg) + `'
AND id <> COALESCE((SELECT value FROM server_settings WHERE key = 'tenancy_staff_org'), '')
AND COALESCE((SELECT value FROM server_settings WHERE key = 'tenancy_mode'), '') <> 'single'`

// BeginStorageReport snapshots orgUsed and allocates a sequence under calendar-total.
// While the request is ambiguous, writes may not exceed its snapshot: otherwise the
// report could release an old reservation while unreported writes still spent it.
// No SQL lock is held across a network request. Retries return the exact frozen report.
func (s *SQLStore) BeginStorageReport(ctx context.Context, org OrgID, want int64) (StorageReport, error) {
	if want < 0 || want > maxStorageReportBytes {
		return StorageReport{}, ErrStorageGrant
	}
	tx, err := s.lockedTx(ctx, "calendar-total")
	if err != nil {
		return StorageReport{}, err
	}
	defer tx.Rollback()
	r := StorageReport{Org: org}
	var inflight bool
	var stale int64
	err = tx.QueryRowContext(ctx, s.rebind(`SELECT pool_seq,pool_report_used,pool_report_want,pool_sent_at,pool_inflight,pool_stale_seq FROM orgs WHERE `+storageCustomer), org).Scan(&r.Seq, &r.UsedBytes, &r.WantBytes, &r.SentAt, &inflight, &stale)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if stale > 0 {
		return r, ErrStorageSequence
	}
	if inflight {
		return r, tx.Commit()
	}
	if r.Seq == math.MaxInt64 {
		return r, ErrStorageSequence
	}
	previousUsed := r.UsedBytes
	r.UsedBytes, err = s.calendars.orgUsed(ctx, tx, org)
	if err != nil {
		return r, err
	}
	if r.UsedBytes < 0 || r.UsedBytes > maxStorageReportBytes {
		return r, ErrStorageGrant
	}
	r.Seq++
	if want > 0 {
		want = min(max(want, 2*max(r.UsedBytes-previousUsed, 0)), maxStorageReportBytes)
	}
	r.WantBytes = want
	err = tx.QueryRowContext(ctx, `SELECT `+s.dbNow()).Scan(&r.SentAt)
	if err != nil {
		return r, err
	}
	_, err = tx.ExecContext(ctx, s.rebind(`UPDATE orgs SET pool_seq=?,pool_report_used=?,pool_report_want=?,pool_sent_at=?,pool_inflight=TRUE,pool_refresh=FALSE WHERE id=?`), r.Seq, r.UsedBytes, r.WantBytes, r.SentAt, org)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// ApplyStorageGrant accepts only the outstanding report's sequence. Replays and old
// replies change nothing. The adapter calls it only for a verified "ok" answer.
func (s *SQLStore) ApplyStorageGrant(ctx context.Context, org OrgID, seq, ceiling, ttl int64) error {
	if seq <= 0 || ceiling < 0 || ttl <= 0 || ttl > StorageGrantTTL {
		return ErrStorageGrant
	}
	tx, err := s.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current, applied, used, want, sent, stale int64
	var inflight bool
	err = tx.QueryRowContext(ctx, s.rebind(`SELECT pool_seq,pool_applied_seq,pool_report_used,pool_report_want,pool_sent_at,pool_stale_seq,pool_inflight FROM orgs WHERE `+storageCustomer), org).Scan(&current, &applied, &used, &want, &sent, &stale, &inflight)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if seq != current || seq <= applied || stale > 0 || !inflight {
		return nil
	}
	if ceiling < used || ceiling-used > want {
		return ErrStorageGrant
	}
	_, err = tx.ExecContext(ctx, s.rebind(`UPDATE orgs SET pool_applied_seq=?,pool_ceiling=?,pool_expires_at=?,pool_inflight=FALSE WHERE id=?`), seq, ceiling, sent+ttl, org)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// StaleStorageReport fences an outstanding stale answer until an operator advances
// the sequence. It never jumps to KyIdentity's number automatically, including after restore.
func (s *SQLStore) StaleStorageReport(ctx context.Context, org OrgID, seq, remoteSeq int64) error {
	if seq <= 0 || remoteSeq < seq {
		return ErrStorageGrant
	}
	tx, err := s.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, s.rebind(`UPDATE orgs SET pool_refresh=FALSE,pool_stale_seq=CASE WHEN pool_stale_seq<? THEN ? ELSE pool_stale_seq END
WHERE `+storageCustomer+` AND pool_seq=? AND pool_applied_seq<? AND pool_inflight`), remoteSeq, remoteSeq, org, seq, seq)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// AdvanceStorageSequence is the operator's restore recovery, not a poller action. It
// releases the frozen report and every old grant; the next request uses a higher sequence.
func (s *SQLStore) AdvanceStorageSequence(ctx context.Context, org OrgID, past int64) error {
	if past < 0 || past == math.MaxInt64 {
		return ErrStorageSequence
	}
	tx, err := s.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, s.rebind(`UPDATE orgs SET pool_seq=?,pool_stale_seq=0,pool_inflight=FALSE,pool_ceiling=NULL,pool_expires_at=0,pool_since=0,pool_refresh=TRUE
WHERE `+storageCustomer+` AND pool_seq<=? AND pool_stale_seq<=?`), past, org, past, past)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStorageSequence
	}
	if err := s.insertAudit(ctx, tx, AuditRecord{Action: "platform.storage_sequence_advanced", Resource: "org:" + string(org), Details: "past=" + strconv.FormatInt(past, 10)}); err != nil {
		return err
	}
	return tx.Commit()
}

// ForgetStorageGrant applies unknown_org for the outstanding report. An older reply
// cannot revoke a newer grant, and a revoked grant never re-enters initial grace.
func (s *SQLStore) ForgetStorageGrant(ctx context.Context, org OrgID, seq int64) error {
	if seq <= 0 {
		return ErrStorageGrant
	}
	tx, err := s.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, s.rebind(`UPDATE orgs SET pool_ceiling=NULL,pool_expires_at=0,pool_since=0,pool_inflight=FALSE,pool_refresh=FALSE,pool_applied_seq=?
 WHERE `+storageCustomer+` AND pool_seq=? AND pool_applied_seq<? AND pool_inflight`), seq, org, seq, seq)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// MarkStorageRefresh makes a committed lifecycle byte change visible to the elected worker.
func (s *SQLStore) MarkStorageRefresh(ctx context.Context, org OrgID) error {
	return s.markStorageRefresh(ctx, s.db, org)
}
func (s *SQLStore) markStorageRefresh(ctx context.Context, db txQuerier, org OrgID) error {
	_, err := db.ExecContext(ctx, s.rebind(`UPDATE orgs SET pool_refresh=TRUE WHERE `+storageCustomer+` AND status<>'deleted'`), org)
	return err
}

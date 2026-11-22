package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// purgeBatchRows and purgeBatchBytes bound one object batch of a purge. Each batch holds
// calendar-total, the lock every object write in every org takes, so other orgs wait at most
// one batch (tenancy spec D12).
var purgeBatchRows, purgeBatchBytes = 500, int64(4 << 20)

// ErrNotPurgeable refuses a purge of an org that is not deleted, or of the staff or default org.
var ErrNotPurgeable = errors.New("org is not deleted, or is the staff or default org")

// purgeTables are the org-owned tables the final purge transaction empties; every other row of
// the org cascades from them or from its calendars, deleted before (TestPurgeLeavesNothingOfTheOrg).
var purgeTables = []string{"groups", "scheduling_outbox", "invitation_byte_budget", "device_pairings", "users", "audit_records"}

// OrgsToPurge lists the deleted orgs whose data is not purged yet.
func (s *SQLStore) OrgsToPurge(ctx context.Context) ([]OrgID, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM orgs WHERE status = 'deleted' AND purged_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrgID
	for rows.Next() {
		var id OrgID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TombstoneOrg marks a stale org deleted, for an operator who confirmed in KyIdentity that it
// is gone (kycalendar purge-org). A poll never purges on absence; only this or KyIdentity's
// own deleted answer does.
func (s *SQLStore) TombstoneOrg(ctx context.Context, staff, org OrgID) error {
	if org == staff || org == DefaultOrg {
		return ErrNotPurgeable
	}
	res, err := s.db.ExecContext(ctx, s.rebind(`UPDATE orgs SET status = 'deleted' WHERE id = ? AND status IN ('stale', 'deleted')`), org)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPurgeable
	}
	return nil
}

// PurgeOrg deletes every row of a deleted org (tenancy spec, Lifecycle, Deleted) and keeps its
// orgs row as a tombstone. The platform audit row comes first: if it cannot be written, nothing
// is deleted. Objects go in batches, then each empty calendar, then the rest in one
// transaction that sets purged_at. A purge cut short anywhere resumes from what is left.
func (s *SQLStore) PurgeOrg(ctx context.Context, staff, org OrgID) error {
	if org == staff || org == DefaultOrg {
		return ErrNotPurgeable
	}
	return s.purgeOrg(ctx, org)
}

func (s *SQLStore) purgeOrg(ctx context.Context, org OrgID) error {
	var status string
	var purged sql.NullInt64
	if err := s.db.QueryRowContext(ctx, s.rebind(`SELECT status, purged_at FROM orgs WHERE id = ?`), org).Scan(&status, &purged); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if purged.Valid {
		return nil
	}
	if status != "deleted" {
		return ErrNotPurgeable
	}
	if err := s.insertAudit(ctx, s.db, AuditRecord{Action: "platform.org_purge_started", Resource: "org:" + string(org)}); err != nil {
		return err
	}
	objects := 0
	for {
		n, err := s.purgeObjectBatch(ctx, org)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		objects += n
	}
	cals, err := s.purgeCalendars(ctx, org)
	if err != nil {
		return err
	}
	return s.purgeRest(ctx, org, objects, cals)
}

// purgeObjectBatch deletes up to purgeBatchRows of org's objects, stopping past purgeBatchBytes;
// the usage triggers follow in the same transaction.
func (s *SQLStore) purgeObjectBatch(ctx context.Context, org OrgID) (int, error) {
	tx, err := s.lockedTx(ctx, "calendar-total")
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT calendar_id, name, LENGTH(data) FROM calendar_objects WHERE org_id = ? ORDER BY calendar_id, name LIMIT ?`), org, purgeBatchRows)
	if err != nil {
		return 0, err
	}
	type key struct{ cal, name string }
	var batch []key
	var bytes int64
	for (len(batch) == 0 || bytes < purgeBatchBytes) && rows.Next() {
		var k key
		var n int64
		if err := rows.Scan(&k.cal, &k.name, &n); err != nil {
			rows.Close()
			return 0, err
		}
		batch, bytes = append(batch, k), bytes+n
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	for _, k := range batch {
		if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM calendar_objects WHERE calendar_id = ? AND name = ?`), k.cal, k.name); err != nil {
			return 0, err
		}
	}
	return len(batch), tx.Commit()
}

// purgeCalendars deletes org's empty calendars one transaction each: a calendar's change log
// and grants go with it.
func (s *SQLStore) purgeCalendars(ctx context.Context, org OrgID) (int, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT id FROM calendars WHERE org_id = ? ORDER BY id`), org)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM calendars WHERE id = ? AND org_id = ?`), id, org); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// purgeRest deletes the org's remaining rows, its usage row and its accounts' logout fences,
// sets purged_at and writes platform.org_deleted with the row counts, in one transaction.
func (s *SQLStore) purgeRest(ctx context.Context, org OrgID, objects, cals int) error {
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	counts := []string{fmt.Sprintf("calendar_objects=%d calendars=%d", objects, cals)}
	// sso_subject_logouts is platform-wide: the org's users are its only map to the org.
	res, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM sso_subject_logouts WHERE EXISTS (SELECT 1 FROM users u
WHERE u.org_id = ? AND u.sso_issuer = sso_subject_logouts.issuer AND u.sso_subject = sso_subject_logouts.subject)`), org)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	counts = append(counts, fmt.Sprintf("sso_subject_logouts=%d", n))
	for _, table := range purgeTables {
		res, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM `+table+` WHERE org_id = ?`), org)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			counts = append(counts, fmt.Sprintf("%s=%d", table, n))
		}
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM org_usage WHERE org_id = ?`), org); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE orgs SET purged_at = ? WHERE id = ?`), time.Now().Unix(), org); err != nil {
		return err
	}
	if err := s.insertAudit(ctx, tx, AuditRecord{Action: "platform.org_deleted", Resource: "org:" + string(org), Details: strings.Join(counts, " ")}); err != nil {
		return err
	}
	return tx.Commit()
}

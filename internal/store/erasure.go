package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// transferCalendars runs after the user's deletion, inside the same transaction and under
// calendar-total. The erased user's memberships and credentials are already gone; another
// erasure cannot select a successor until this transaction commits. Single-tenant deletion
// keeps its original behavior. Missing mode fails closed as elsewhere in the store.
func (u *userStore) transferCalendars(ctx context.Context, tx *sql.Tx, erased string) error {
	s := u.store
	var mode string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM server_settings WHERE key = 'tenancy_mode'), '')`).Scan(&mode); err != nil {
		return err
	}
	if mode == "single" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT id FROM calendars WHERE owner_kind = 'user' AND owner_id = ? ORDER BY id`), erased)
	if err != nil {
		return err
	}
	var calendars []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		calendars = append(calendars, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range calendars {
		// A member's strongest role across all grants wins, then account age and ID.
		// Lock the selected account against concurrent access revocation on PostgreSQL.
		query := `SELECT u.id FROM users u JOIN calendars c ON c.org_id = u.org_id
WHERE c.id = ? AND u.status = 'active' AND u.role = 'user'
AND EXISTS (SELECT 1 FROM calendar_grants g JOIN group_members m ON m.group_id = g.group_id
 WHERE g.calendar_id = c.id AND m.user_id = u.id AND g.org_id = c.org_id AND m.org_id = c.org_id)
ORDER BY (SELECT MIN(CASE g.role WHEN 'manager' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END)
 FROM calendar_grants g JOIN group_members m ON m.group_id = g.group_id
 WHERE g.calendar_id = c.id AND m.user_id = u.id AND g.org_id = c.org_id AND m.org_id = c.org_id),
 u.created_at, u.id LIMIT 1`
		if s.driver == "postgres" {
			query += " FOR UPDATE OF u"
		}
		var next string
		if err := tx.QueryRowContext(ctx, s.rebind(query), id).Scan(&next); errors.Is(err, sql.ErrNoRows) {
			continue // no surviving member: the caller deletes this private calendar
		} else if err != nil {
			return err
		}
		// Only ownership changes: no quota admission, slug, sequence or content rewrite.
		// A conflicting per-owner slug refuses the entire erasure instead of losing data.
		if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE calendars SET owner_id = ? WHERE id = ?`), next, id); err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, AuditRecord{UserID: next, Action: "calendar.owner_transferred", Resource: id,
			Details: fmt.Sprintf("from=%s to=%s", erased, next)}); err != nil {
			return err
		}
	}
	return nil
}

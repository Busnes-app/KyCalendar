package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

const maxCalendarSlugLen = 64

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
	type ownedCalendar struct {
		id, slug string
	}
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT id, slug FROM calendars WHERE owner_kind = 'user' AND owner_id = ? ORDER BY id`), erased)
	if err != nil {
		return err
	}
	var calendars []ownedCalendar
	for rows.Next() {
		var cal ownedCalendar
		if err := rows.Scan(&cal.id, &cal.slug); err != nil {
			rows.Close()
			return err
		}
		calendars = append(calendars, cal)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, cal := range calendars {
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
		if err := tx.QueryRowContext(ctx, s.rebind(query), cal.id).Scan(&next); errors.Is(err, sql.ErrNoRows) {
			continue // no surviving member: the caller deletes this private calendar
		} else if err != nil {
			return err
		}
		// No quota admission, sequence or content rewrite; only disambiguate the per-owner slug
		// when the successor already owns a personal calendar with the same slug.
		slug, err := s.uniqueTransferSlug(ctx, tx, next, cal.slug)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE calendars SET owner_id = ?, slug = ? WHERE id = ?`), next, slug, cal.id); err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, AuditRecord{UserID: next, Action: "calendar.owner_transferred", Resource: cal.id,
			Details: fmt.Sprintf("from=%s to=%s", erased, next)}); err != nil {
			return err
		}
	}
	return nil
}

// uniqueTransferSlug keeps slug when ownerID has no personal calendar using it, otherwise picks
// the first free "<slug>-2", "<slug>-3", ... truncated to fit VARCHAR(64).
func (s *SQLStore) uniqueTransferSlug(ctx context.Context, tx *sql.Tx, ownerID, slug string) (string, error) {
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT slug FROM calendars WHERE owner_kind = 'user' AND owner_id = ?`), ownerID)
	if err != nil {
		return "", err
	}
	taken := map[string]struct{}{}
	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			rows.Close()
			return "", err
		}
		taken[existing] = struct{}{}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if _, busy := taken[slug]; !busy {
		return slug, nil
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		base := slug
		if len(base)+len(suffix) > maxCalendarSlugLen {
			if maxBase := maxCalendarSlugLen - len(suffix); maxBase > 0 {
				base = base[:maxBase]
			} else {
				base = ""
			}
		}
		candidate := base + suffix
		if base == "" {
			candidate = strconv.Itoa(n)
		}
		if _, busy := taken[candidate]; !busy {
			return candidate, nil
		}
	}
}

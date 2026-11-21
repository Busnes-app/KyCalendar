package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

// SuiteGroup is one entry of KyIdentity's suite group list, as reconcile needs it. ID is the
// KyIdentity group ID, the only key. Subjects are the members' SSO subjects (Active only).
type SuiteGroup struct {
	ID, Name string
	Active   bool
	Subjects []string
}

// SuiteGroupChange counts what one reconcile wrote; the zero value wrote nothing.
type SuiteGroupChange struct {
	Created, Renamed, Archived, Unarchived, GrantRestored bool
	Added, Removed                                        int
}

// Changed reports whether anything was written.
func (c SuiteGroupChange) Changed() bool { return c != SuiteGroupChange{} }

// SuiteGroupDisplayName is the local group name holding a suite group's members. It is fixed by
// the KyIdentity ID, never the suite name, so it never collides with a local or SCIM group
// that happens to share the name.
func SuiteGroupDisplayName(id string) string { return "suite-group:" + id }

// reconcileSuiteGroup brings org's group calendar for g in line with KyIdentity, in one
// transaction under the group-names lock. Active: ensure the calendar (created with an editor
// grant to a GroupSourceSuite group, un-archived), its name, and the group's members as exactly
// the active everyday accounts in scope with g's subjects; subjects with no account yet are
// skipped. Archived: remove every member and mark the calendar archived (read-only), when it
// exists. Nothing is ever deleted, and the same input again writes nothing.
func (g *groupStore) reconcileSuiteGroup(ctx context.Context, org OrgID, scope SCIMScope, sg SuiteGroup) (SuiteGroupChange, error) {
	var ch SuiteGroupChange
	q := g.store.rebind
	tx, err := g.store.lockedTx(ctx, "group-names")
	if err != nil {
		return ch, err
	}
	defer tx.Rollback()
	var calID, groupID string
	var archived bool
	// Another org's row for this KyIdentity ID reads as missing; its insert below then fails
	// on the key and nothing is written.
	err = tx.QueryRowContext(ctx, q(`SELECT calendar_id, group_id, archived FROM suite_group_calendars WHERE suite_group_id = ? AND org_id = ?`), sg.ID, org).Scan(&calID, &groupID, &archived)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return ch, err
	}
	now := time.Now().UTC()
	if !sg.Active {
		if missing {
			// The calendar may be gone (an administrator deleted it) while its local group
			// keeps members and other grants: empty that group anyway.
			err := tx.QueryRowContext(ctx, q(`SELECT id FROM groups WHERE source = ? AND external_id = ? AND org_id = ?`), GroupSourceSuite, sg.ID, org).Scan(&groupID)
			if errors.Is(err, sql.ErrNoRows) {
				return ch, nil
			}
			if err != nil {
				return ch, err
			}
			if ch.Removed, err = g.setSuiteMembers(ctx, tx, org, groupID, nil); err != nil {
				return ch, err
			}
			return ch, commitIf(tx, ch)
		}
		if ch.Removed, err = g.setSuiteMembers(ctx, tx, org, groupID, nil); err != nil {
			return ch, err
		}
		if !archived {
			if _, err := tx.ExecContext(ctx, q(`UPDATE suite_group_calendars SET archived = TRUE WHERE suite_group_id = ?`), sg.ID); err != nil {
				return ch, err
			}
			ch.Archived = true
		}
		return ch, commitIf(tx, ch)
	}
	if missing {
		if groupID, err = g.suiteLocalGroup(ctx, tx, org, sg.ID, now); err != nil {
			return ch, err
		}
		calID = "cal_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, q(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, components, created_at, org_id) VALUES (?, 'group', ?, 'group', ?, 'VEVENT', ?, ?)`),
			calID, calID, sg.Name, now, org); err != nil {
			return ch, err
		}
		if _, err := tx.ExecContext(ctx, q(`INSERT INTO suite_group_calendars (suite_group_id, calendar_id, group_id, org_id) VALUES (?, ?, ?, ?)`), sg.ID, calID, groupID, org); err != nil {
			return ch, err
		}
		ch.Created = true
	} else {
		if archived {
			if _, err := tx.ExecContext(ctx, q(`UPDATE suite_group_calendars SET archived = FALSE WHERE suite_group_id = ?`), sg.ID); err != nil {
				return ch, err
			}
			ch.Unarchived = true
		}
		res, err := tx.ExecContext(ctx, q(`UPDATE calendars SET name = ? WHERE id = ? AND name <> ?`), sg.Name, calID, sg.Name)
		if err != nil {
			return ch, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			ch.Renamed = true
		}
	}
	// The ordinary member role, never higher or lower: a suite group grants no more than an
	// ordinary member, so a grant an administrator changed or removed is put back to editor.
	res, err := tx.ExecContext(ctx, q(`INSERT INTO calendar_grants (calendar_id, group_id, role, org_id) VALUES (?, ?, 'editor', ?)
ON CONFLICT (calendar_id, group_id) DO UPDATE SET role = 'editor' WHERE calendar_grants.role <> 'editor'`), calID, groupID, org)
	if err != nil {
		return ch, err
	}
	if n, _ := res.RowsAffected(); n > 0 && !ch.Created {
		ch.GrantRestored = true
	}
	want, err := g.suiteMemberIDs(ctx, tx, org, scope, sg.Subjects)
	if err != nil {
		return ch, err
	}
	if ch.Added, ch.Removed, err = g.diffSuiteMembers(ctx, tx, org, groupID, want); err != nil {
		return ch, err
	}
	return ch, commitIf(tx, ch)
}

// commitIf commits tx when ch wrote something; otherwise the deferred rollback ends it.
func commitIf(tx *sql.Tx, ch SuiteGroupChange) error {
	if !ch.Changed() {
		return nil
	}
	return tx.Commit()
}

// suiteLocalGroup is the GroupSourceSuite group for suite group id: the one a previous calendar
// (since deleted by an administrator) used, or a new one.
func (g *groupStore) suiteLocalGroup(ctx context.Context, tx *sql.Tx, org OrgID, id string, now time.Time) (string, error) {
	var groupID string
	err := tx.QueryRowContext(ctx, g.store.rebind(`SELECT id FROM groups WHERE source = ? AND external_id = ? AND org_id = ?`), GroupSourceSuite, id, org).Scan(&groupID)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return groupID, err
	}
	groupID = "grp_" + uuid.NewString()
	name := SuiteGroupDisplayName(id)
	if err := g.refuseTwin(ctx, tx, groupID, name); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, g.store.rebind("INSERT INTO groups ("+groupColumns+") VALUES (?, ?, ?, ?, ?, ?, ?)"), groupID, name, id, GroupSourceSuite, now, now, org)
	return groupID, err
}

// suiteMemberIDs maps subjects to org's active everyday accounts in scope that hold them.
// Administrators are never members.
// ponytail: one IN list per group; KyIdentity sends at most a group's live members. Chunk the
// query if a group can outgrow the SQLite variable limit (32766).
func (g *groupStore) suiteMemberIDs(ctx context.Context, tx *sql.Tx, org OrgID, scope SCIMScope, subjects []string) ([]string, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	in, args := inList(subjects)
	clause, sargs := scope.where()
	rows, err := tx.QueryContext(ctx, g.store.rebind("SELECT id FROM users WHERE "+clause+" AND org_id = ? AND status = 'active' AND role <> 'admin' AND sso_subject IN ("+in+")"), append(append(sargs, org), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// setSuiteMembers makes groupID's members exactly want and returns how many it removed.
func (g *groupStore) setSuiteMembers(ctx context.Context, tx *sql.Tx, org OrgID, groupID string, want []string) (int, error) {
	_, removed, err := g.diffSuiteMembers(ctx, tx, org, groupID, want)
	return removed, err
}

// diffSuiteMembers writes only the difference between groupID's members and want.
func (g *groupStore) diffSuiteMembers(ctx context.Context, tx *sql.Tx, org OrgID, groupID string, want []string) (added, removed int, err error) {
	have, err := g.getMembers(ctx, tx, groupID)
	if err != nil {
		return 0, 0, err
	}
	for _, id := range have {
		if !slices.Contains(want, id) {
			if _, err := tx.ExecContext(ctx, g.store.rebind("DELETE FROM group_members WHERE group_id = ? AND user_id = ?"), groupID, id); err != nil {
				return 0, 0, err
			}
			removed++
		}
	}
	for _, id := range want {
		if !slices.Contains(have, id) {
			if _, err := tx.ExecContext(ctx, g.store.rebind("INSERT INTO group_members (group_id, user_id, org_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING"), groupID, id, org); err != nil {
				return 0, 0, err
			}
			added++
		}
	}
	return added, removed, nil
}

package store

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

// TenancySetting records the install's mode at first start. Every install is single-tenant
// until multi-tenant mode ships (docs/superpowers/specs/2026-11-20-tenancy-design.md, D1).
const TenancySetting = "tenancy_mode"

// ensureOrgs gives a single-tenant install its default org, the org's usage row and the
// recorded mode, idempotently and race-free across replicas. Migration 37 already made them for
// an install that had users; a fresh database gets them here, before its first admin. Copy
// targets (snapshots) clear every table before copying, so the rows never collide.
func (s *SQLStore) ensureOrgs(ctx context.Context) error {
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO orgs (id, status) VALUES (?, 'active') ON CONFLICT (id) DO NOTHING`, []any{migrations.DefaultOrgID}},
		{`INSERT INTO org_usage (org_id, bytes) VALUES (?, 0) ON CONFLICT (org_id) DO NOTHING`, []any{migrations.DefaultOrgID}},
		{`INSERT INTO server_settings (key, value, updated_at) VALUES (?, 'single', ?) ON CONFLICT (key) DO NOTHING`, []any{TenancySetting, time.Now().UTC()}},
	} {
		if _, err := tx.ExecContext(ctx, s.rebind(q.sql), q.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StaffOrgSetting records a multi-tenant install's staff org (tenancy spec D7): only its accounts
// may be administrators.
const StaffOrgSetting = "tenancy_staff_org"

// Tenancy is an install's mode. Anything but a recorded "single" is multi-tenant, so a lost
// row fails closed; ensureOrgs writes it back at the next open.
type Tenancy struct {
	Multi bool
	Staff OrgID // multi-tenant only; empty means no account is an administrator
}

func (s *SQLStore) Tenancy(ctx context.Context) (Tenancy, error) {
	v, err := s.Settings().GetSettings(ctx, TenancySetting, StaffOrgSetting)
	if err != nil {
		return Tenancy{}, err
	}
	if v[TenancySetting] == "single" {
		return Tenancy{}, nil
	}
	return Tenancy{Multi: true, Staff: OrgID(v[StaffOrgSetting])}, nil
}

func (s *SQLStore) OrgServes(ctx context.Context, id OrgID) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT COUNT(*) FROM orgs WHERE id = ? AND status = 'active' AND enabled`), id).Scan(&n)
	return n == 1, err
}

// OrgsCursorSetting is the /api/suite/orgs revision the next mirror poll starts after.
const OrgsCursorSetting = "tenancy_orgs_since"

// MirroredOrg is one org as KyIdentity's /api/suite/orgs lists it to this app.
type MirroredOrg struct {
	ID       OrgID
	Status   string // active, suspended, deletion_scheduled or deleted
	Revision int64
	Enabled  bool
	Quotas   string // a JSON object
}

// ApplyOrgs writes one mirror poll (tenancy spec D6) and its next cursor in one transaction. A
// row moves only forward in revision, a tombstone never serves again, and the staff and default
// orgs come from configuration, so a poll never touches them. A new org gets its usage row here,
// and an org that stops serving loses its sessions.
func (s *SQLStore) ApplyOrgs(ctx context.Context, staff OrgID, orgs []MirroredOrg, next int64) error {
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, o := range orgs {
		if o.ID == staff || o.ID == DefaultOrg {
			continue
		}
		serving := `SELECT ` + orgServes("?")
		var before, after bool
		if err := tx.QueryRowContext(ctx, s.rebind(serving), o.ID).Scan(&before); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO orgs (id, status, revision, enabled, quotas_json, seen_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET status = excluded.status, revision = excluded.revision, enabled = excluded.enabled, quotas_json = excluded.quotas_json, seen_at = excluded.seen_at
WHERE orgs.revision <= excluded.revision AND orgs.status <> 'deleted'`), o.ID, o.Status, o.Revision, o.Enabled, o.Quotas, now.Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO org_usage (org_id, bytes) VALUES (?, 0) ON CONFLICT (org_id) DO NOTHING`), o.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, s.rebind(serving), o.ID).Scan(&after); err != nil {
			return err
		}
		// An org that stops serving loses its sessions with the audit row (tenancy spec,
		// Lifecycle); app passwords stay, so a resume brings DAV and MCP clients back.
		if before && !after {
			if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE org_id = ?)`), o.ID); err != nil {
				return err
			}
			if err := s.insertAudit(ctx, tx, AuditRecord{Action: "platform.org_stopped", Resource: "org:" + string(o.ID), Details: "status=" + o.Status + " enabled=" + strconv.FormatBool(o.Enabled)}); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO server_settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`), OrgsCursorSetting, strconv.FormatInt(next, 10), now); err != nil {
		return err
	}
	return tx.Commit()
}

// orgServes is a SQL condition: the org whose ID is col is active and enabled in the mirror
// (tenancy spec D6). col is code, never input. Workers pick only such rows, so a suspended org's
// work stays queued and never fills a batch.
func orgServes(col string) string {
	return "EXISTS (SELECT 1 FROM orgs WHERE orgs.id = " + col + " AND orgs.status = 'active' AND orgs.enabled)"
}

// OrgID names an org. Build one only from an authenticated principal (OrgOf) or, on a
// single-tenant path, DefaultOrg: never from a request.
type OrgID string

// PeerOrg is the org_id a KyPost or KyMessages body carries for u (tenancy spec D10): u's org in
// multi-tenant mode, none in single-tenant mode, so those bodies stay as they were.
func PeerOrg(ctx context.Context, st Store, u *User) (string, error) {
	ten, err := st.Tenancy(ctx)
	if err != nil || !ten.Multi {
		return "", err
	}
	return string(OrgOf(u)), nil
}

// DefaultOrg is the one org of a single-tenant install.
const DefaultOrg OrgID = migrations.DefaultOrgID

// OrgOf is u's org.
func OrgOf(u *User) OrgID { return u.OrgID }

// OrgStore reaches one org's rows. A row of another org answers exactly as a missing one
// (ErrNotFound), so a cross-org ID reveals nothing. Tenancy PR 2 moves the org-owned methods
// here area by area; these are users, groups, memberships, grants and suite groups.
type OrgStore interface {
	GetUser(ctx context.Context, id string) (*User, error)
	// GetGroup returns the group with its member IDs.
	GetGroup(ctx context.Context, id string) (*Group, error)
	// AddGroupMember is ErrNotFound unless the group and the user are both in this org.
	AddGroupMember(ctx context.Context, groupID, userID string) error
	// SetGrant creates or replaces one group's role; ErrNotFound unless the calendar is a
	// group calendar and the group exists, both in this org.
	SetGrant(ctx context.Context, g CalendarGrant) error
	// ReconcileSuiteGroup makes g's group calendar and members here match KyIdentity's suite
	// group list in one transaction; members are this org's accounts in scope. See
	// suite_groups.go.
	ReconcileSuiteGroup(ctx context.Context, scope SCIMScope, g SuiteGroup) (SuiteGroupChange, error)
}

type orgStore struct {
	s  *SQLStore
	id OrgID
}

func (s *SQLStore) Org(id OrgID) OrgStore { return orgStore{s: s, id: id} }

func (o orgStore) GetUser(ctx context.Context, id string) (*User, error) {
	return o.s.users.scanUser(o.s.db.QueryRowContext(ctx, o.s.rebind(`
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, sso_issuer, totp_secret_enc, totp_enabled,
       recovery_codes_hash, push_device_id, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at, org_id, `+orgServes("users.org_id")+`
FROM users WHERE id = ? AND org_id = ?`), id, o.id))
}

func (o orgStore) GetGroup(ctx context.Context, id string) (*Group, error) {
	grp, err := scanGroup(o.s.db.QueryRowContext(ctx, o.s.rebind("SELECT "+groupColumns+" FROM groups WHERE id = ? AND org_id = ?"), id, o.id))
	if err != nil {
		return nil, err
	}
	if grp.Members, err = o.s.groups.getMembers(ctx, o.s.db, grp.ID); err != nil {
		return nil, err
	}
	return grp, nil
}

func (o orgStore) AddGroupMember(ctx context.Context, groupID, userID string) error {
	tx, err := o.s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := o.bothInOrg(ctx, tx, "groups", groupID, "users", userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, o.s.rebind("INSERT INTO group_members (group_id, user_id, org_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING"), groupID, userID, o.id); err != nil {
		return err
	}
	return tx.Commit()
}

func (o orgStore) SetGrant(ctx context.Context, g CalendarGrant) error {
	tx, err := o.s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := o.bothInOrg(ctx, tx, "calendars", g.CalendarID, "groups", g.GroupID); err != nil {
		return err
	}
	var groupCal int
	if err := tx.QueryRowContext(ctx, o.s.rebind(`SELECT COUNT(*) FROM calendars WHERE id = ? AND owner_kind = 'group'`), g.CalendarID).Scan(&groupCal); err != nil {
		return err
	}
	if groupCal != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, o.s.rebind(`INSERT INTO calendar_grants (calendar_id, group_id, role, org_id) VALUES (?, ?, ?, ?)
ON CONFLICT (calendar_id, group_id) DO UPDATE SET role = excluded.role`), g.CalendarID, g.GroupID, g.Role, o.id); err != nil {
		return err
	}
	return tx.Commit()
}

// bothInOrg is ErrNotFound unless row a of table ta and row b of table tb are both this org's.
// The tables are this file's constants, never input.
func (o orgStore) bothInOrg(ctx context.Context, tx *sql.Tx, ta, a, tb, b string) error {
	var n int
	if err := tx.QueryRowContext(ctx, o.s.rebind("SELECT (SELECT COUNT(*) FROM "+ta+" WHERE id = ? AND org_id = ?) + (SELECT COUNT(*) FROM "+tb+" WHERE id = ? AND org_id = ?)"), a, o.id, b, o.id).Scan(&n); err != nil {
		return err
	}
	if n != 2 {
		return ErrNotFound
	}
	return nil
}

func (o orgStore) ReconcileSuiteGroup(ctx context.Context, scope SCIMScope, g SuiteGroup) (SuiteGroupChange, error) {
	return o.s.groups.reconcileSuiteGroup(ctx, o.id, scope, g)
}

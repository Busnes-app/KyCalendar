package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	var ok bool
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT `+s.orgServes("?")), id).Scan(&ok)
	return ok, err
}

// OrgExportable reports whether org id is mirrored deletion_scheduled and enabled, with an
// answer at most OrgFresh old: the only state in which an export_only sign-in is admitted
// (tenancy spec, Export).
func (s *SQLStore) OrgExportable(ctx context.Context, id OrgID) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT COUNT(*) FROM orgs WHERE id = ? AND status = 'deletion_scheduled' AND enabled AND seen_at >= `+
		s.dbNow()+` - `+strconv.Itoa(int(OrgFresh.Seconds()))+s.inCell()), id).Scan(&n)
	return n == 1, err
}

// ServingOrgs lists the orgs that serve now (orgServes), by ID: those whose suite groups the
// poll reconciles in multi-tenant mode.
func (s *SQLStore) ServingOrgs(ctx context.Context) ([]OrgID, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id FROM orgs o WHERE `+s.orgServes("o.id")+` ORDER BY o.id`)
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

// MirroredOrgView is one org as staff see it: the mirror row and its stored object bytes.
type MirroredOrgView struct {
	ID       OrgID  `json:"id"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
	Enabled  bool   `json:"enabled"`
	Quotas   string `json:"quotas"`  // a JSON object, as KyIdentity sent it
	SeenAt   int64  `json:"seen_at"` // unix seconds of the last poll that listed it
	Bytes    int64  `json:"bytes"`
}

func (s *SQLStore) ListMirroredOrgs(ctx context.Context) ([]MirroredOrgView, error) {
	return s.listMirroredOrgs(ctx, "")
}

// StorageRefreshOrgs lists changed or ambiguous reports for early renewal,
// excluding fenced/deleted orgs. The regular mirror pass still reports every customer.
func (s *SQLStore) StorageRefreshOrgs(ctx context.Context) ([]MirroredOrgView, error) {
	return s.listMirroredOrgs(ctx, "WHERE (o.pool_refresh OR o.pool_inflight) AND o.pool_stale_seq=0 AND o.status<>'deleted'")
}

func (s *SQLStore) listMirroredOrgs(ctx context.Context, where string) ([]MirroredOrgView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.status, o.revision, o.enabled, o.quotas_json, o.seen_at, COALESCE(u.bytes, 0)
FROM orgs o LEFT JOIN org_usage u ON u.org_id = o.id `+where+` ORDER BY o.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MirroredOrgView{}
	for rows.Next() {
		var v MirroredOrgView
		if err := rows.Scan(&v.ID, &v.Status, &v.Revision, &v.Enabled, &v.Quotas, &v.SeenAt, &v.Bytes); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// OrgsCursorSetting is the /api/suite/orgs revision the next mirror poll starts after.
const OrgsCursorSetting = "tenancy_orgs_since"

// ErasedPendingSetting is set by ResetAfterRestore in multi-tenant mode: the next mirror poll
// first deletes every account KyIdentity lists as erased (/api/suite/erased from 0), and only
// then answers for any org, so a restore cannot bring an erased person back.
const ErasedPendingSetting = "tenancy_erased_pending"

// MirroredOrg is one org as KyIdentity's /api/suite/orgs lists it to this app.
type MirroredOrg struct {
	ID       OrgID
	Status   string // active, suspended, deletion_scheduled or deleted
	Revision int64
	Enabled  bool
	Quotas   string // a JSON object
	// HomeRegion is the org's cell (tenancy spec, Cells); "" from a KyIdentity without them.
	HomeRegion string
}

// ApplyOrgs writes one mirror poll (tenancy spec D6) and its next cursor in one transaction. A
// row moves only forward in revision, a tombstone never serves again, and the staff and default
// orgs come from configuration, so a poll never touches them. A new org gets its usage row here,
// and an org that stops serving loses its sessions. A listing that reached the end (no orgs, or
// a cursor at its last revision) vouches for every org, so it stamps them all seen (orgServes).
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
		serving := `SELECT ` + s.orgActive("?")
		var before, after bool
		if err := tx.QueryRowContext(ctx, s.rebind(serving), o.ID).Scan(&before); err != nil {
			return err
		}
		// home_region follows KyIdentity: an org moved to another cell stops serving here (C5).
		if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO orgs (id, status, revision, enabled, quotas_json, seen_at, pool_since, home_region) VALUES (?, ?, ?, ?, ?, ?, `+s.dbNow()+`, ?)
ON CONFLICT (id) DO UPDATE SET status = excluded.status, revision = excluded.revision, enabled = excluded.enabled, quotas_json = excluded.quotas_json, seen_at = excluded.seen_at, home_region = excluded.home_region
WHERE orgs.revision <= excluded.revision AND orgs.status <> 'deleted'`), o.ID, o.Status, o.Revision, o.Enabled, o.Quotas, now.Unix(), o.HomeRegion); err != nil {
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
	if len(orgs) == 0 || next >= orgs[len(orgs)-1].Revision {
		if _, err := tx.ExecContext(ctx, `UPDATE orgs SET seen_at = `+s.dbNow()+` WHERE status <> 'deleted'`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// OrgFresh is how long a customer org serves on its last poll answer. Past it, with KyIdentity
// unreachable, every customer org stops serving until a poll reaches the end of the listing
// (tenancy spec, Suite answers). The default and staff orgs come from configuration and never age.
const OrgFresh = 15 * time.Minute

// orgServes is a SQL condition: the org whose ID is col is active and enabled in the mirror
// (tenancy spec D6), and a customer org's answer is at most OrgFresh old by the database's clock.
// col is code, never input. Workers pick only such rows, so a suspended org's work stays queued
// and never fills a batch.
func (s *SQLStore) orgServes(col string) string {
	return "EXISTS (SELECT 1 FROM orgs WHERE orgs.id = " + col + " AND orgs.status = 'active' AND orgs.enabled AND (orgs.seen_at >= " +
		s.dbNow() + " - " + strconv.Itoa(int(OrgFresh.Seconds())) + " OR orgs.id = '" + string(DefaultOrg) +
		"' OR orgs.id = (SELECT value FROM server_settings WHERE key = '" + StaffOrgSetting + "'))" + s.inCell() + ")"
}

// orgActive is orgServes without the age bound: whether KyIdentity's last answer serves. ApplyOrgs
// compares it before and after a write, so an org already stale that turns suspended, or that
// moves to another cell, still loses its sessions.
func (s *SQLStore) orgActive(col string) string {
	return "EXISTS (SELECT 1 FROM orgs WHERE orgs.id = " + col + " AND orgs.status = 'active' AND orgs.enabled" + s.inCell() + ")"
}

// inCell is a SQL condition on the orgs row in scope (tenancy spec, Cells, C5): with cells set,
// only the staff org and orgs whose home_region is one of them serve here, so two deployments
// never both accept one org. The cells are validated slugs, never input.
func (s *SQLStore) inCell() string {
	if len(s.cells) == 0 {
		return ""
	}
	return " AND (orgs.home_region IN ('" + strings.Join(s.cells, "', '") + "') OR orgs.id = (SELECT value FROM server_settings WHERE key = '" + StaffOrgSetting + "'))"
}

// cellSlug is KyIdentity's home region slug (C1), ^[a-z][a-z0-9-]{1,31}$.
var cellSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func (s *SQLStore) ServeCells(cells []string) error {
	for _, c := range cells {
		if !cellSlug.MatchString(c) {
			return fmt.Errorf("cell %q is not a home region slug", c)
		}
	}
	s.cells = slices.Clone(cells)
	return nil
}

// dbNow is the database's clock in Unix seconds, so no replica's clock decides an org's age.
func (s *SQLStore) dbNow() string {
	if s.driver == "postgres" {
		return "CAST(EXTRACT(EPOCH FROM statement_timestamp()) AS BIGINT)"
	}
	return "CAST(unixepoch() AS INTEGER)"
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
	// ListCalendars is every calendar in this org, its members' and its group calendars, owner
	// by owner (export-org).
	ListCalendars(ctx context.Context) ([]*Calendar, error)
}

type orgStore struct {
	s  *SQLStore
	id OrgID
}

func (s *SQLStore) Org(id OrgID) OrgStore { return orgStore{s: s, id: id} }

func (o orgStore) ListCalendars(ctx context.Context) ([]*Calendar, error) {
	return o.s.calendars.queryCalendars(ctx, `SELECT `+calendarCols+` FROM calendars WHERE org_id = ? ORDER BY owner_kind, owner_id, created_at, id`, o.id)
}

func (o orgStore) GetUser(ctx context.Context, id string) (*User, error) {
	return o.s.users.scanUser(o.s.db.QueryRowContext(ctx, o.s.rebind(`
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, sso_issuer, totp_secret_enc, totp_enabled,
       recovery_codes_hash, push_device_id, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at, org_id, `+o.s.orgServes("users.org_id")+`
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

package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

// tableClass is each table's class in the tenancy spec's Inventory
// (docs/superpowers/specs/2026-11-20-tenancy-design.md): o carries org_id, d is scoped through
// its parent, p is platform-wide and off is a business feature off in multi-tenant mode.
var tableClass = map[string]string{
	"orgs": "p", "org_usage": "o",
	"users": "o", "sessions": "d", "mfa_challenges": "d", "device_pairings": "o",
	"groups": "o", "group_members": "o", "audit_records": "o", "server_settings": "p",
	"calendars": "o", "calendar_meta": "p", "calendar_objects": "o", "calendar_usage": "p", "calendar_changes": "d",
	"calendar_grants": "o", "calendar_subscriptions": "d",
	"app_passwords": "d", "mcp_events": "d",
	"scheduling_outbox": "o", "invitation_byte_budget": "o", "cancellation_reservations": "d",
	"sso_logouts": "p", "sso_subject_logouts": "p",
	"video_meetings": "d", "meeting_guests": "d", "presence_pushes": "d",
	"booking_types": "d", "bookings": "d",
	"resources": "off", "resource_groups": "off", "resource_approvers": "off", "resource_bookings": "off", "resource_busy": "off",
	"kypost_out_of_office": "d", "suite_group_calendars": "o",
	"task_areas": "d", "task_views": "d", "task_search": "d", "task_completions": "d", "task_goals": "d", "task_prefs": "d",
	"security_windows": "p", "spent_challenges": "p", "kypost_replay": "p",
	"account_keys": "d", "key_slots": "d", "calendar_keys": "d", "calendar_key_holders": "d", "pending_key_shares": "d", "worker_keys": "d",
}

// TestEveryTableIsClassified fails when a table has no tenancy class, when the org-owned
// tables and migration 37 disagree, or when an org-owned table would be copied before orgs.
func TestEveryTableIsClassified(t *testing.T) {
	for _, table := range migrationTables {
		if _, ok := tableClass[table]; !ok {
			t.Errorf("%s has no tenancy class", table)
		}
	}
	for table := range tableClass {
		if !slices.Contains(migrationTables, table) {
			t.Errorf("%s is classified but not a table", table)
		}
	}
	var owned []string
	for table, class := range tableClass {
		if class == "o" && table != "org_usage" {
			owned = append(owned, table)
		}
	}
	want := slices.Clone(migrations.OrgOwnedTables)
	slices.Sort(owned)
	slices.Sort(want)
	if !slices.Equal(owned, want) {
		t.Errorf("org-owned tables %v, migration 37 gives org_id to %v", owned, want)
	}
	if migrationTables[0] != "orgs" {
		t.Errorf("orgs must be copied first, not %s", migrationTables[0])
	}
}

// eachDialect runs fn on a migrated SQLite store and, with KY_TEST_POSTGRES_DSN, a PostgreSQL one.
func eachDialect(t *testing.T, fn func(t *testing.T, s *SQLStore)) {
	t.Run("sqlite", func(t *testing.T) {
		st, err := openSQLite(context.Background(), filepath.Join(t.TempDir(), "x.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		fn(t, st.(*SQLStore))
	})
	t.Run("postgres", func(t *testing.T) {
		st, err := openPostgres(context.Background(), pgTarget(t), 4, 4, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		fn(t, st.(*SQLStore))
	})
}

// TestOrgOwnedTablesHaveOrgID checks the catalog: every org-owned table has org_id, NOT NULL
// apart from audit_records, the immutability trigger, and the existence check (a SQLite trigger,
// a PostgreSQL foreign key).
func TestOrgOwnedTablesHaveOrgID(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		pg := s.driver == "postgres"
		for _, table := range migrations.OrgOwnedTables {
			var nullable bool
			var err error
			if pg {
				var yes string
				err = s.db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = 'org_id'`, table).Scan(&yes)
				nullable = yes == "YES"
			} else {
				var notNull int
				err = s.db.QueryRowContext(ctx, `SELECT "notnull" FROM pragma_table_info(?) WHERE name = 'org_id'`, table).Scan(&notNull)
				nullable = notNull == 0
			}
			if err != nil {
				t.Errorf("%s has no org_id: %v", table, err)
				continue
			}
			if nullable != (table == "audit_records") {
				t.Errorf("%s.org_id nullable = %v", table, nullable)
			}
			triggers := []string{table + "_org_fixed"}
			if !pg {
				triggers = append(triggers, table+"_org_exists")
			}
			for _, name := range triggers {
				var n int
				q := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`
				if pg {
					q = `SELECT COUNT(*) FROM pg_trigger WHERE tgname = $1 AND tgrelid = to_regclass($2)`
				}
				args := []any{name}
				if pg {
					args = append(args, table)
				}
				if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil || n != 1 {
					t.Errorf("%s: trigger %s missing (%v)", table, name, err)
				}
			}
			if pg {
				var n int
				if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE contype = 'f' AND conrelid = to_regclass($1) AND confrelid = to_regclass('orgs')`, table).Scan(&n); err != nil || n != 1 {
					t.Errorf("%s: %d foreign keys to orgs (%v)", table, n, err)
				}
			}
		}
	})
}

// TestOrgGuardsRefuseCrossOrgRows proves each guard refuses: an unknown org, a changed org, a
// calendar in another org than its owner, a membership, grant or object joining two orgs. It
// also proves org_usage follows writes and a cascaded calendar delete per org.
func TestOrgGuardsRefuseCrossOrgRows(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		now := time.Now().UTC()
		exec := func(q string, args ...any) error {
			_, err := s.db.ExecContext(ctx, s.rebind(q), args...)
			return err
		}
		must := func(q string, args ...any) {
			t.Helper()
			if err := exec(q, args...); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		refused := func(what, q string, args ...any) {
			t.Helper()
			// The refusal must come from an org guard, not some other constraint.
			if err := exec(q, args...); err == nil || !strings.Contains(err.Error(), "org") {
				t.Errorf("%s: %v, want an org refusal", what, err)
			}
		}
		const b = "org-b"
		must(`INSERT INTO orgs (id, status) VALUES (?, 'active')`, b)
		must(`INSERT INTO org_usage (org_id, bytes) VALUES (?, 0)`, b)
		must(`INSERT INTO users (id, username, created_at, updated_at) VALUES ('ua', 'a', ?, ?)`, now, now)
		must(`INSERT INTO users (id, username, created_at, updated_at, org_id) VALUES ('ub', 'b', ?, ?, ?)`, now, now, b)
		must(`INSERT INTO groups (id, display_name, created_at, updated_at) VALUES ('ga', 'A', ?, ?)`, now, now)
		must(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at) VALUES ('ca', 'user', 'ua', 's', 'A', ?)`, now)
		must(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at, org_id) VALUES ('cb', 'user', 'ub', 's', 'B', ?, ?)`, now, b)
		must(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at) VALUES ('cg', 'group', 'cg', 'g', 'G', ?)`, now)

		refused("an unknown org", `INSERT INTO users (id, username, created_at, updated_at, org_id) VALUES ('ux', 'x', ?, ?, 'nope')`, now, now)
		refused("an org change", `UPDATE users SET org_id = ? WHERE id = 'ua'`, b)
		refused("a calendar outside its owner's org", `INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at) VALUES ('cx', 'user', 'ub', 'x', 'X', ?)`, now)
		refused("a membership across orgs", `INSERT INTO group_members (group_id, user_id) VALUES ('ga', 'ub')`)
		refused("a grant across orgs", `INSERT INTO calendar_grants (calendar_id, group_id, role, org_id) VALUES ('cb', 'ga', 'reader', ?)`, b)
		refused("an object outside its calendar's org", `INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at, component, org_id) VALUES ('ca', 'x.ics', 'x', 'e', ?, 0, ?, 'VEVENT', ?)`, []byte("x"), now, b)

		data := []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
		must(`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at, component) VALUES ('ca', 'a.ics', 'a', 'e', ?, 0, ?, 'VEVENT')`, data, now)
		must(`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at, component, org_id) VALUES ('cb', 'b.ics', 'b', 'e', ?, 0, ?, 'VEVENT', ?)`, data, now, b)
		must(`UPDATE calendar_objects SET data = ? WHERE calendar_id = 'cb'`, append(data, 'x'))
		usage := func(org string) int64 {
			t.Helper()
			var n int64
			if err := s.db.QueryRowContext(ctx, s.rebind(`SELECT bytes FROM org_usage WHERE org_id = ?`), org).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if a, bb := usage(migrations.DefaultOrgID), usage(b); a != int64(len(data)) || bb != int64(len(data)+1) {
			t.Errorf("org_usage: default %d, b %d", a, bb)
		}
		must(`DELETE FROM calendars WHERE id = 'cb'`) // cascades to its object
		if bb := usage(b); bb != 0 {
			t.Errorf("org_usage after a cascaded delete: %d", bb)
		}
		if err := checkUsage(ctx, s.db); err != nil {
			t.Error(err)
		}
	})
}

// TestOrgStoreAnswersOtherOrgsLikeMissing: through an org's handle, another org's user, group
// or calendar is ErrNotFound, the same answer as an ID that exists nowhere, and nothing is
// written. Suite-group members come only from the handle's org.
func TestOrgStoreAnswersOtherOrgsLikeMissing(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		const b OrgID = "org-b"
		if _, err := s.db.ExecContext(ctx, s.rebind(`INSERT INTO orgs (id, status) VALUES (?, 'active')`), b); err != nil {
			t.Fatal(err)
		}
		const binding = "kyidentity https://id.example"
		for _, u := range []*User{
			{ID: "ua", Username: "a", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sa", SSOIssuer: binding},
			{ID: "ub", Username: "b", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sb", SSOIssuer: binding, OrgID: b},
		} {
			if err := s.Users().CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
		for _, g := range []*Group{{ID: "ga", DisplayName: "A"}, {ID: "gb", DisplayName: "B", OrgID: b}} {
			if err := s.Groups().CreateGroup(ctx, g); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Calendars().CreateCalendar(ctx, &Calendar{ID: "cg", OwnerKind: "group", OwnerID: "cg", Slug: "g", Name: "G"}, 0); err != nil {
			t.Fatal(err)
		}
		a, ob := s.Org(DefaultOrg), s.Org(b)
		if u, err := ob.GetUser(ctx, "ub"); err != nil || u.OrgID != b {
			t.Fatalf("own user: %+v %v", u, err)
		}
		for name, err := range map[string]error{
			"another org's user":         second(a.GetUser(ctx, "ub")),
			"a missing user":             second(a.GetUser(ctx, "nobody")),
			"another org's group":        second(a.GetGroup(ctx, "gb")),
			"a missing group":            second(a.GetGroup(ctx, "nothing")),
			"a member from another org":  a.AddGroupMember(ctx, "ga", "ub"),
			"a missing member":           a.AddGroupMember(ctx, "ga", "nobody"),
			"a grant to another org":     a.SetGrant(ctx, CalendarGrant{CalendarID: "cg", GroupID: "gb", Role: "reader"}),
			"a grant to a missing group": a.SetGrant(ctx, CalendarGrant{CalendarID: "cg", GroupID: "nothing", Role: "reader"}),
			"another org's calendar":     ob.SetGrant(ctx, CalendarGrant{CalendarID: "cg", GroupID: "gb", Role: "reader"}),
		} {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: %v, want ErrNotFound", name, err)
			}
		}
		var rows int
		if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM group_members) + (SELECT COUNT(*) FROM calendar_grants)`).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("refused writes left %d rows (%v)", rows, err)
		}

		scope := SCIMScope{Issuer: binding, Providers: []string{"kysignon"}}
		if _, err := ob.ReconcileSuiteGroup(ctx, scope, SuiteGroup{ID: "sg1", Name: "Family", Active: true, Subjects: []string{"sa", "sb"}}); err != nil {
			t.Fatal(err)
		}
		var members []string
		if err := func() error {
			r, err := s.db.QueryContext(ctx, s.rebind(`SELECT gm.user_id FROM group_members gm JOIN suite_group_calendars sgc ON sgc.group_id = gm.group_id WHERE sgc.suite_group_id = 'sg1' AND sgc.org_id = ?`), b)
			if err != nil {
				return err
			}
			defer r.Close()
			for r.Next() {
				var id string
				if err := r.Scan(&id); err != nil {
					return err
				}
				members = append(members, id)
			}
			return r.Err()
		}(); err != nil || !slices.Equal(members, []string{"ub"}) {
			t.Fatalf("suite group members %v (%v), want only ub", members, err)
		}
		// The same KyIdentity group reconciled into another org writes nothing.
		if _, err := a.ReconcileSuiteGroup(ctx, scope, SuiteGroup{ID: "sg1", Name: "Family", Active: true, Subjects: []string{"sa"}}); err == nil {
			t.Error("a suite group moved to another org")
		}
	})
}

func second[T any](_ T, err error) error { return err }

// TestNewRowsJoinTheirParentsOrg: a calendar, its objects, a feed's mirror, a queued message and
// the invite budget of another org's user carry that org, not the default.
func TestNewRowsJoinTheirParentsOrg(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		const b = "org-b"
		if _, err := s.db.ExecContext(ctx, s.rebind(`INSERT INTO orgs (id, status) VALUES (?, 'active')`), b); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, s.rebind(`INSERT INTO org_usage (org_id, bytes) VALUES (?, 0)`), b); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().CreateUser(ctx, &User{ID: "ub", Username: "b", Role: "user", Status: "active", SSOProvider: "local", OrgID: b}); err != nil {
			t.Fatal(err)
		}
		cs := s.Calendars()
		if err := cs.CreateCalendar(ctx, &Calendar{ID: "cb", OwnerKind: "user", OwnerID: "ub", Slug: "default", Name: "B"}, 0); err != nil {
			t.Fatal(err)
		}
		ics := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:t\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
		plan := func([]byte) ([]byte, []*OutboxMessage, error) {
			return nil, []*OutboxMessage{{ID: "m1", DedupeKey: "d1", SenderID: "ub", Method: "REQUEST", Recipient: "mailto:x@example.com", Body: ics, CountInviteBytes: true}}, nil
		}
		if _, err := cs.PutObjectPlanned(ctx, &CalendarObject{CalendarID: "cb", Name: "e1.ics", Component: "VEVENT", UID: "e1", ETag: "1", Data: ics, WriterID: "ub"}, "", true, OwnerLimits{}, plan); err != nil {
			t.Fatal(err)
		}
		if err := cs.CreateSubscribedCalendar(ctx, &Calendar{ID: "fb", OwnerKind: "user", OwnerID: "ub", Slug: "feed", Name: "F"}, &Subscription{URLSealed: "x", URLHost: "example.com"}, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := cs.ReplaceSubscriptionObjects(ctx, "fb", []*CalendarObject{{CalendarID: "fb", Name: "f1.ics", Component: "VEVENT", UID: "f1", ETag: "1", Data: ics}}, OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{
			`SELECT org_id FROM calendars WHERE id = 'cb'`,
			`SELECT org_id FROM calendars WHERE id = 'fb'`,
			`SELECT org_id FROM calendar_objects WHERE calendar_id = 'cb'`,
			`SELECT org_id FROM calendar_objects WHERE calendar_id = 'fb'`,
			`SELECT org_id FROM scheduling_outbox WHERE id = 'm1'`,
			`SELECT org_id FROM invitation_byte_budget WHERE sender_id = 'ub'`,
		} {
			var org string
			if err := s.db.QueryRowContext(ctx, q).Scan(&org); err != nil || org != b {
				t.Errorf("%s: %q %v", q, org, err)
			}
		}
	})
}

// TestCalendarUserIsScopedToTheOrg (tenancy spec D5): in multi-tenant mode a scheduling peer is
// an account in the org asked for, never one in another org. Without a tenancy_mode row the org
// still decides, so a lost row can never leak across orgs; single-tenant mode is unchanged.
func TestCalendarUserIsScopedToTheOrg(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		const b OrgID = "org-b"
		if err := s.ApplyOrgs(ctx, "", []MirroredOrg{{ID: b, Status: "active", Revision: 1, Enabled: true, Quotas: "{}"}}, 1); err != nil {
			t.Fatal(err)
		}
		for _, u := range []*User{
			{ID: "ua", Username: "a", Email: "a@example.com", Role: "user", Status: "active", SSOProvider: "local"},
			{ID: "ub", Username: "b", Email: "b@example.com", Role: "user", Status: "active", SSOProvider: "local", OrgID: b},
		} {
			if err := s.Users().CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
		peer := func(org OrgID, email string) string {
			u, err := s.Users().CalendarUser(ctx, org, email)
			if errors.Is(err, ErrNotFound) {
				return "-"
			} else if err != nil {
				t.Fatal(err)
			}
			return u.ID
		}
		check := func(mode string, want map[[2]string]string) {
			t.Helper()
			for k, id := range want {
				if got := peer(OrgID(k[0]), k[1]); got != id {
					t.Errorf("%s: CalendarUser(%s, %s) = %s, want %s", mode, k[0], k[1], got, id)
				}
			}
		}
		check("single", map[[2]string]string{{string(DefaultOrg), "a@example.com"}: "ua", {string(DefaultOrg), "b@example.com"}: "ub"})
		if err := s.Settings().SetSetting(ctx, TenancySetting, "multi"); err != nil {
			t.Fatal(err)
		}
		multi := map[[2]string]string{
			{string(DefaultOrg), "a@example.com"}: "ua", {string(DefaultOrg), "b@example.com"}: "-",
			{string(b), "b@example.com"}: "ub", {string(b), "a@example.com"}: "-", {"", "a@example.com"}: "-",
		}
		check("multi", multi)
		if err := s.Settings().DeleteSetting(ctx, TenancySetting); err != nil {
			t.Fatal(err)
		}
		check("no tenancy_mode", multi)
		if u, err := s.Users().EmailOwner(ctx, "b@example.com"); err != nil || u.ID != "ub" {
			t.Fatalf("own address without tenancy_mode: %+v %v", u, err)
		}
	})
}

// TestAuditRowsJoinTheirUsersOrg: an audit row joins the org of the user it names; one naming
// no user ("system", the installer) is a platform row, org_id NULL. A pairing takes its user's org.
func TestAuditRowsJoinTheirUsersOrg(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		const b = "org-b"
		if _, err := s.db.ExecContext(ctx, s.rebind(`INSERT INTO orgs (id, status) VALUES (?, 'active')`), b); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().CreateUser(ctx, &User{ID: "ub", Username: "b", Role: "user", Status: "active", SSOProvider: "local", OrgID: b}); err != nil {
			t.Fatal(err)
		}
		for _, r := range []*AuditRecord{{UserID: "ub", Action: "t.user"}, {UserID: "system", Action: "t.system"}, {Action: "t.none"}} {
			if err := s.Audit().LogAudit(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Settings().SetSettingAudited(ctx, "t_key", "v", &AuditRecord{UserID: "ub", Action: "t.setting"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Devices().CreatePairing(ctx, &DevicePairing{Secret: "pb", UserID: "ub", Status: "pending", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		for q, want := range map[string]string{
			`SELECT org_id FROM audit_records WHERE action = 't.user'`:    b,
			`SELECT org_id FROM audit_records WHERE action = 't.setting'`: b,
			`SELECT org_id FROM audit_records WHERE action = 't.system'`:  "NULL",
			`SELECT org_id FROM audit_records WHERE action = 't.none'`:    "NULL",
			`SELECT org_id FROM device_pairings WHERE secret = 'pb'`:      b,
		} {
			var org sql.NullString
			if err := s.db.QueryRowContext(ctx, q).Scan(&org); err != nil {
				t.Fatal(q, err)
			}
			if got := cmp.Or(org.String, "NULL"); got != want {
				t.Errorf("%s: %s, want %s", q, got, want)
			}
		}
	})
}

// PeerOrg names the account's org only in multi-tenant mode, and a lost mode row reads as
// multi-tenant (D10, fail closed).
func TestPeerOrgOnlyInMultiTenantMode(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		u := &User{OrgID: "org-a"}
		for _, c := range []struct{ mode, want string }{{"single", ""}, {"multi", "org-a"}, {"", "org-a"}} {
			q := `UPDATE server_settings SET value = ? WHERE key = ?`
			args := []any{c.mode, TenancySetting}
			if c.mode == "" {
				q, args = `DELETE FROM server_settings WHERE key = ?`, []any{TenancySetting}
			}
			if _, err := s.db.ExecContext(ctx, s.rebind(q), args...); err != nil {
				t.Fatal(err)
			}
			if got, err := PeerOrg(ctx, s, u); err != nil || got != c.want {
				t.Errorf("mode %q: %q %v, want %q", c.mode, got, err, c.want)
			}
		}
	})
}

// TestApplyOrgsMirrorsThePoll: a poll adds orgs with a usage row and records its cursor; a row
// never moves back in revision, a tombstone never serves again, the staff and default orgs are
// never touched, and an object written into a polled org grows its usage.
func TestApplyOrgsMirrorsThePoll(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		const staff OrgID = "org-staff"
		if _, err := s.db.ExecContext(ctx, s.rebind(`INSERT INTO orgs (id, status) VALUES (?, 'active')`), staff); err != nil {
			t.Fatal(err)
		}
		state := func(id OrgID) string {
			var status, quotas string
			var rev int64
			var enabled bool
			if err := s.db.QueryRowContext(ctx, s.rebind(`SELECT status, revision, enabled, quotas_json FROM orgs WHERE id = ?`), id).Scan(&status, &rev, &enabled, &quotas); err != nil {
				return err.Error()
			}
			return fmt.Sprintf("%s %d %t %s", status, rev, enabled, quotas)
		}
		cursor := func() string {
			v, err := s.Settings().GetSettings(ctx, OrgsCursorSetting)
			if err != nil {
				t.Fatal(err)
			}
			return v[OrgsCursorSetting]
		}
		if err := s.ApplyOrgs(ctx, staff, []MirroredOrg{
			{ID: "org-a", Status: "active", Revision: 3, Enabled: true, Quotas: `{"max_bytes":100}`},
			{ID: "org-gone", Status: "deleted", Revision: 4, Quotas: `{}`},
			{ID: staff, Status: "suspended", Revision: 5, Quotas: `{}`},
			{ID: DefaultOrg, Status: "suspended", Revision: 5, Quotas: `{}`},
		}, 5); err != nil {
			t.Fatal(err)
		}
		if got := state("org-a"); got != `active 3 true {"max_bytes":100}` {
			t.Errorf("org-a: %s", got)
		}
		for _, id := range []OrgID{staff, DefaultOrg} {
			if got := state(id); got != "active 0 true {}" {
				t.Errorf("the poll changed %s: %s", id, got)
			}
		}
		if got := cursor(); got != "5" {
			t.Errorf("cursor %q", got)
		}
		if ok, err := s.OrgServes(ctx, "org-a"); err != nil || !ok {
			t.Fatalf("org-a serves: %t %v", ok, err)
		}

		// An older answer (another replica's slower poll) and a revived tombstone change nothing.
		if err := s.ApplyOrgs(ctx, staff, []MirroredOrg{
			{ID: "org-a", Status: "suspended", Revision: 2, Quotas: `{}`},
			{ID: "org-gone", Status: "active", Revision: 9, Enabled: true, Quotas: `{}`},
		}, 9); err != nil {
			t.Fatal(err)
		}
		if got := state("org-a"); got != `active 3 true {"max_bytes":100}` {
			t.Errorf("an older revision moved org-a: %s", got)
		}
		if got := state("org-gone"); got != "deleted 4 false {}" {
			t.Errorf("a tombstone changed: %s", got)
		}
		if err := s.ApplyOrgs(ctx, staff, []MirroredOrg{{ID: "org-a", Status: "suspended", Revision: 10, Enabled: true, Quotas: `{}`}}, 10); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.OrgServes(ctx, "org-a"); err != nil || ok || cursor() != "10" {
			t.Fatalf("suspended org-a serves: %t %v, cursor %s", ok, err, cursor())
		}

		if err := s.ApplyOrgs(ctx, staff, []MirroredOrg{{ID: "org-new", Status: "active", Revision: 11, Enabled: true, Quotas: `{}`}}, 11); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().CreateUser(ctx, &User{ID: "un", Username: "n", Role: "user", Status: "active", SSOProvider: "local", OrgID: "org-new"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Calendars().CreateCalendar(ctx, &Calendar{ID: "cn", OwnerKind: "user", OwnerID: "un", Slug: "default", Name: "N"}, 0); err != nil {
			t.Fatal(err)
		}
		ics := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:t\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
		if _, err := s.Calendars().PutObjectPlanned(ctx, &CalendarObject{CalendarID: "cn", Name: "e1.ics", Component: "VEVENT", UID: "e1", ETag: "1", Data: ics, WriterID: "un"}, "", true, OwnerLimits{}, nil); err != nil {
			t.Fatal(err)
		}
		var bytes int64
		if err := s.db.QueryRowContext(ctx, s.rebind(`SELECT bytes FROM org_usage WHERE org_id = ?`), "org-new").Scan(&bytes); err != nil || bytes != int64(len(ics)) {
			t.Fatalf("org-new usage %d (%v), want %d", bytes, err, len(ics))
		}
	})
}

// TestAnOrgThatStopsServingIsHeld: when a poll stops an org serving, its sessions end with a
// platform audit row and its app passwords stay; no worker picks its due outbox, feed, meeting
// or presence rows, and a resume picks them up again.
func TestAnOrgThatStopsServingIsHeld(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		const b OrgID = "org-b"
		poll := func(status string, rev int64) {
			t.Helper()
			if err := s.ApplyOrgs(ctx, "org-staff", []MirroredOrg{{ID: b, Status: status, Revision: rev, Enabled: true, Quotas: `{}`}}, rev); err != nil {
				t.Fatal(err)
			}
		}
		poll("active", 1)
		u := &User{ID: "ub", Username: "b", Role: "user", Status: "active", SSOProvider: "local", OrgID: b}
		if err := s.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		cs := s.Calendars()
		if err := cs.CreateCalendar(ctx, &Calendar{ID: "cb", OwnerKind: "user", OwnerID: "ub", Slug: "default", Name: "B"}, 0); err != nil {
			t.Fatal(err)
		}
		ics := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:t\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
		plan := func([]byte) ([]byte, []*OutboxMessage, error) {
			return nil, []*OutboxMessage{{ID: "m1", DedupeKey: "d1", SenderID: "ub", Method: "REQUEST", Recipient: "mailto:x@example.com", Body: ics}}, nil
		}
		if _, err := cs.PutObjectPlanned(ctx, &CalendarObject{CalendarID: "cb", Name: "e1.ics", Component: "VEVENT", UID: "e1", ETag: "1", Data: ics, WriterID: "ub"}, "", true, OwnerLimits{}, plan); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-time.Hour).UTC()
		if err := cs.CreateSubscribedCalendar(ctx, &Calendar{ID: "fb", OwnerKind: "user", OwnerID: "ub", Slug: "feed", Name: "F"}, &Subscription{URLSealed: "x", URLHost: "example.com", NextCheckAt: past}, 0, 0); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{
			`INSERT INTO video_meetings (id, user_id, calendar_id, name, state, rev, done_rev, next_attempt_at, created_at) VALUES ('v1', 'ub', 'cb', 'e1.ics', 'open', 3, 2, ?, ?)`,
			`INSERT INTO presence_pushes (user_id, rev, done_rev, next_attempt_at) VALUES ('ub', 4, 1, ?)`,
		} {
			args := []any{past, past}[:strings.Count(q, "?")]
			if _, err := s.db.ExecContext(ctx, s.rebind(q), args...); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now().UTC()
		if err := s.Sessions().CreateSession(ctx, &Session{TokenHash: "tok_b", UserID: "ub", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.Credentials()); err != nil {
			t.Fatal(err)
		}
		if err := s.AppPasswords().Create(ctx, Seed, &AppPassword{ID: "ap_b", UserID: "ub", Label: "t", Hash: "h", Scope: ScopeDAV}, 0); err != nil {
			t.Fatal(err)
		}
		due := func() string {
			t.Helper()
			out, err1 := cs.DueOutbox(ctx, now, 10)
			subs, err2 := cs.DueSubscriptions(ctx, now, 10)
			meets, err3 := cs.DueMeetings(ctx, now, 10)
			pres, err4 := cs.DuePresence(ctx, now, 10)
			if err := errors.Join(err1, err2, err3, err4); err != nil {
				t.Fatal(err)
			}
			return fmt.Sprint(len(out), len(subs), len(meets), len(pres))
		}
		audits := func() string {
			var n int
			var org sql.NullString
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(org_id) FROM audit_records WHERE action = 'platform.org_stopped'`).Scan(&n, &org); err != nil {
				t.Fatal(err)
			}
			return fmt.Sprint(n, " ", org.Valid)
		}
		if got := due(); got != "1 1 1 1" {
			t.Fatalf("control: due %s", got)
		}

		poll("suspended", 2)
		if got := due(); got != "0 0 0 0" {
			t.Errorf("a suspended org's work is due: %s", got)
		}
		if _, err := s.Sessions().GetSession(ctx, "tok_b"); !errors.Is(err, ErrNotFound) {
			t.Errorf("the session outlived the suspension: %v", err)
		}
		if _, err := s.AppPasswords().Get(ctx, "ap_b"); err != nil {
			t.Errorf("the app password was removed: %v", err)
		}
		if got := audits(); got != "1 false" {
			t.Errorf("platform.org_stopped rows (count, has org): %s", got)
		}
		poll("suspended", 3) // still stopped: nothing more to end
		if got := audits(); got != "1 false" {
			t.Errorf("a repeated suspension audited again: %s", got)
		}

		poll("active", 4)
		if got := due(); got != "1 1 1 1" {
			t.Errorf("a resumed org's work is not due: %s", got)
		}
		if got := audits(); got != "1 false" {
			t.Errorf("the resume audited a stop: %s", got)
		}
	})
}

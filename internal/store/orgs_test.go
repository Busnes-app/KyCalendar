package store

import (
	"context"
	"errors"
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

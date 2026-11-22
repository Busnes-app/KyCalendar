package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// refs counts, per table, the rows any text column of which holds one of ids. orgs is left
// out: a purged org keeps its row as a tombstone.
func refs(t *testing.T, s *SQLStore, ids []string) map[string]int {
	t.Helper()
	ctx := context.Background()
	in := "'" + strings.Join(ids, "','") + "'"
	out := map[string]int{}
	for _, table := range migrationTables {
		if table == "orgs" {
			continue
		}
		rows, err := s.db.QueryContext(ctx, "SELECT * FROM "+table+" WHERE 1 = 0")
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		var conds []string
		for _, ct := range types {
			if name := strings.ToUpper(ct.DatabaseTypeName()); strings.Contains(name, "TEXT") || strings.Contains(name, "CHAR") {
				conds = append(conds, ct.Name()+" IN ("+in+")")
			}
		}
		if len(conds) == 0 {
			continue
		}
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+strings.Join(conds, " OR ")).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			out[table] = n
		}
	}
	return out
}

func values(t *testing.T, s *SQLStore, q string, args ...any) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), s.rebind(q), args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// purgeWorld is the round-trip fixture (a row in every table, all in the default org, which
// is purged here through the unguarded purgeOrg) beside org-a, which must survive. Both orgs'
// accounts have logout fences for the same subject under different issuers.
func purgeWorld(t *testing.T, s *SQLStore) (gone, kept []string) {
	t.Helper()
	ctx := context.Background()
	for _, f := range fixture {
		if _, err := s.db.ExecContext(ctx, s.rebind(f.sql), f.args...); err != nil {
			t.Fatalf("%s: %v", f.sql, err)
		}
	}
	for _, q := range []string{
		`UPDATE users SET sso_subject = 'sub-1' WHERE id = 'u1'`,
		`INSERT INTO sso_subject_logouts (issuer, subject, cutoff, expires_at) VALUES ('kyidentity https://id.example', 'sub-1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO orgs (id, status) VALUES ('org-a', 'active')`,
		`INSERT INTO org_usage (org_id, bytes) VALUES ('org-a', 0)`,
		`INSERT INTO users (id, username, sso_provider, sso_subject, sso_issuer, created_at, updated_at, org_id) VALUES ('ua', 'a', 'kysignon', 'sub-1', 'kyidentity https://other.example', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'org-a')`,
		`INSERT INTO sso_subject_logouts (issuer, subject, cutoff, expires_at) VALUES ('kyidentity https://other.example', 'sub-1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at, org_id) VALUES ('ca', 'user', 'ua', 'default', 'A', CURRENT_TIMESTAMP, 'org-a')`,
		`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at, component, org_id) VALUES ('ca', 'x.ics', 'uid-x', 'e', 'BEGIN:VCALENDAR', 1, CURRENT_TIMESTAMP, 'VEVENT', 'org-a')`,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('sa', 'ua', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO groups (id, display_name, created_at, updated_at, org_id) VALUES ('ga', 'A team', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'org-a')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	org := string(DefaultOrg)
	gone = append([]string{org}, values(t, s, `SELECT id FROM users WHERE org_id = ?`, org)...)
	gone = append(gone, values(t, s, `SELECT id FROM calendars WHERE org_id = ?`, org)...)
	gone = append(gone, values(t, s, `SELECT id FROM groups WHERE org_id = ?`, org)...)
	if _, err := s.db.ExecContext(ctx, s.rebind(`UPDATE orgs SET status = 'deleted' WHERE id = ?`), org); err != nil {
		t.Fatal(err)
	}
	return gone, []string{"org-a", "ua", "ca", "ga"}
}

// TestPurgeLeavesNothingOfTheOrg is the tenancy spec's purge test: after a purge cut short
// between batches and run again, no row in any table names the org, its accounts, calendars
// or groups; the other org's rows are untouched; the tombstone stays, marked purged, with the
// started and deleted audit rows; a second run changes nothing.
func TestPurgeLeavesNothingOfTheOrg(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		defer func(r int, b int64) { purgeBatchRows, purgeBatchBytes = r, b }(purgeBatchRows, purgeBatchBytes)
		purgeBatchRows = 1
		gone, kept := purgeWorld(t, s)
		before := refs(t, s, kept)
		if got := refs(t, s, gone); got["calendar_objects"] != 2 || len(got) < 20 {
			t.Fatalf("control: the fixture's org has %v", got)
		}

		// One batch, then a crash: the next run finishes.
		if n, err := s.purgeObjectBatch(ctx, DefaultOrg); err != nil || n != 1 {
			t.Fatalf("first batch: %d %v", n, err)
		}
		if got := refs(t, s, gone)["calendar_objects"]; got != 1 {
			t.Fatalf("after one batch of one: %d objects", got)
		}
		if err := s.purgeOrg(ctx, DefaultOrg); err != nil {
			t.Fatal(err)
		}
		if got := refs(t, s, gone); len(got) != 0 {
			t.Errorf("rows naming the purged org remain: %v", got)
		}
		if got := refs(t, s, kept); fmt.Sprint(got) != fmt.Sprint(before) {
			t.Errorf("the other org's rows changed: %v, were %v", got, before)
		}
		if got := values(t, s, `SELECT issuer FROM sso_subject_logouts WHERE subject = 'sub-1'`); fmt.Sprint(got) != "[kyidentity https://other.example]" {
			t.Errorf("logout fences left: %v", got)
		}
		var status string
		var purged sql.NullInt64
		if err := s.db.QueryRowContext(ctx, s.rebind(`SELECT status, purged_at FROM orgs WHERE id = ?`), DefaultOrg).Scan(&status, &purged); err != nil || status != "deleted" || !purged.Valid {
			t.Fatalf("tombstone: %s %v %v", status, purged, err)
		}
		audit := func() string {
			return fmt.Sprint(values(t, s, `SELECT action FROM audit_records WHERE action LIKE 'platform.org_%' AND org_id IS NULL ORDER BY id`))
		}
		if got := audit(); got != "[platform.org_purge_started platform.org_deleted]" {
			t.Errorf("platform audit %s", got)
		}
		if err := s.purgeOrg(ctx, DefaultOrg); err != nil {
			t.Fatal(err)
		}
		if got := audit(); got != "[platform.org_purge_started platform.org_deleted]" {
			t.Errorf("a second run audited again: %s", got)
		}
	})
}

// A batch stops at its byte bound, and the purge refuses without its audit row (fail closed),
// any org that is not deleted, and the staff and default orgs.
func TestPurgeIsBoundedAndFailsClosed(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		defer func(r int, b int64) { purgeBatchRows, purgeBatchBytes = r, b }(purgeBatchRows, purgeBatchBytes)
		purgeBatchBytes = 1
		gone, _ := purgeWorld(t, s)
		if n, err := s.purgeObjectBatch(ctx, DefaultOrg); err != nil || n != 1 {
			t.Fatalf("a batch past its byte bound: %d %v", n, err)
		}
		for _, org := range []OrgID{DefaultOrg, "org-staff"} {
			if err := s.PurgeOrg(ctx, "org-staff", org); !errors.Is(err, ErrNotPurgeable) {
				t.Errorf("purge of %s: %v", org, err)
			}
		}
		if err := s.PurgeOrg(ctx, "org-staff", "org-a"); !errors.Is(err, ErrNotPurgeable) {
			t.Errorf("purge of an active org: %v", err)
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE audit_records RENAME TO audit_gone`); err != nil {
			t.Fatal(err)
		}
		if err := s.purgeOrg(ctx, DefaultOrg); err == nil {
			t.Fatal("purged without its audit row")
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE audit_gone RENAME TO audit_records`); err != nil {
			t.Fatal(err)
		}
		if got := refs(t, s, gone)["calendar_objects"]; got != 1 {
			t.Errorf("objects after a refused purge: %d, want the 1 the batch left", got)
		}
	})
}

// TestRestoreMakesCustomerOrgsStale is the tenancy spec's restore test at the store: after
// ResetAfterRestore every customer org but a tombstone is stale, refused with every account,
// and the poll cursor is gone; the staff org is untouched. A full poll then brings back the
// listed orgs, a deletion since the capsule is purged, and an org the listing omits stays stale
// with its data. A single-tenant install keeps its default org active.
func TestRestoreMakesCustomerOrgsStale(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		const staff OrgID = "org-staff"
		for _, q := range []string{
			`INSERT INTO orgs (id, status) VALUES ('org-staff', 'active')`,
			`INSERT INTO orgs (id, status, revision) VALUES ('org-a', 'active', 4)`,
			`INSERT INTO orgs (id, status, revision) VALUES ('org-b', 'suspended', 4)`,
			`INSERT INTO orgs (id, status, revision) VALUES ('org-c', 'active', 4)`,
			`INSERT INTO orgs (id, status, revision, purged_at) VALUES ('org-d', 'deleted', 3, 1)`,
		} {
			if _, err := s.db.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range []OrgID{"org-a", "org-b", "org-c"} {
			if err := s.Users().CreateUser(ctx, &User{ID: "u-" + string(id), Username: string(id), Role: "user", Status: "active", SSOProvider: "local", OrgID: id}); err != nil {
				t.Fatal(err)
			}
		}
		state := func() string {
			return fmt.Sprint(values(t, s, `SELECT id || ':' || status || ':' || CAST(revision AS TEXT) FROM orgs WHERE id LIKE 'org-%' ORDER BY id`))
		}

		// Single-tenant: nothing about orgs changes.
		if err := s.ResetAfterRestore(ctx); err != nil {
			t.Fatal(err)
		}
		if got := state(); got != "[org-a:active:4 org-b:suspended:4 org-c:active:4 org-d:deleted:3 org-staff:active:0]" {
			t.Fatalf("single-tenant restore changed orgs: %s", got)
		}

		for k, v := range map[string]string{TenancySetting: "multi", StaffOrgSetting: string(staff), OrgsCursorSetting: "9"} {
			if err := s.Settings().SetSetting(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ResetAfterRestore(ctx); err != nil {
			t.Fatal(err)
		}
		if got := state(); got != "[org-a:stale:0 org-b:stale:0 org-c:stale:0 org-d:deleted:3 org-staff:active:0]" {
			t.Errorf("after the reset: %s", got)
		}
		if u, err := s.Users().GetUserByID(ctx, "u-org-a"); err != nil || u.OrgActive {
			t.Errorf("a stale org's account serves: %+v %v", u, err)
		}
		if v, err := s.Settings().GetSettings(ctx, OrgsCursorSetting); err != nil || v[OrgsCursorSetting] != "" {
			t.Errorf("cursor %q %v, want none: the next poll reads from revision 0", v[OrgsCursorSetting], err)
		}

		// The full poll: org-a still active, org-b deleted since the capsule, org-c not listed.
		if err := s.ApplyOrgs(ctx, staff, []MirroredOrg{
			{ID: "org-a", Status: "active", Revision: 5, Enabled: true, Quotas: `{}`},
			{ID: "org-b", Status: "deleted", Revision: 6, Quotas: `{}`},
			{ID: "org-d", Status: "deleted", Revision: 3, Quotas: `{}`},
		}, 6); err != nil {
			t.Fatal(err)
		}
		due, err := s.OrgsToPurge(ctx)
		if err != nil || fmt.Sprint(due) != "[org-b]" {
			t.Fatalf("orgs to purge %v %v", due, err)
		}
		if err := s.PurgeOrg(ctx, staff, "org-b"); err != nil {
			t.Fatal(err)
		}
		if got := state(); got != "[org-a:active:5 org-b:deleted:6 org-c:stale:0 org-d:deleted:3 org-staff:active:0]" {
			t.Errorf("after the poll: %s", got)
		}
		if got := values(t, s, `SELECT id FROM users ORDER BY id`); fmt.Sprint(got) != "[u-org-a u-org-c]" {
			t.Errorf("accounts after the purge: %v", got)
		}
		if u, err := s.Users().GetUserByID(ctx, "u-org-a"); err != nil || !u.OrgActive {
			t.Errorf("org-a does not serve after the poll: %+v %v", u, err)
		}
	})
}

// TestMeasurePurge times the purge of a large org on PostgreSQL: the slowest batch is the
// longest any other org's write waits. Run with KY_MEASURE_PURGE=<objects>.
func TestMeasurePurge(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("KY_MEASURE_PURGE"))
	if n == 0 {
		t.Skip("set KY_MEASURE_PURGE to an object count")
	}
	ctx := context.Background()
	st, err := openPostgres(ctx, pgTarget(t), 4, 4, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := st.(*SQLStore)
	for _, q := range []string{
		`INSERT INTO orgs (id, status) VALUES ('org-m', 'deleted')`,
		`INSERT INTO users (id, username, created_at, updated_at, org_id) VALUES ('um', 'm', now(), now(), 'org-m')`,
		`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at, org_id) VALUES ('cm', 'user', 'um', 'default', 'M', now(), 'org-m')`,
		`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at, component, org_id)
		 SELECT 'cm', 'o' || g || '.ics', 'u' || g, 'e', convert_to(repeat('x', 2000), 'UTF8'), 1, now(), 'VEVENT', 'org-m' FROM generate_series(1, ` + strconv.Itoa(n) + `) g`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	var slowest time.Duration
	batches := 0
	for {
		b := time.Now()
		k, err := s.purgeObjectBatch(ctx, "org-m")
		if err != nil {
			t.Fatal(err)
		}
		if k == 0 {
			break
		}
		batches++
		slowest = max(slowest, time.Since(b))
	}
	if err := s.purgeOrg(ctx, "org-m"); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d objects of 2000 bytes: %d batches of %d rows / %d bytes, slowest %v, total %v", n, batches, purgeBatchRows, purgeBatchBytes, slowest, time.Since(start))
}

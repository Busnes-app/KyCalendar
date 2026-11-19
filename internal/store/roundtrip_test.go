package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixtureTime has nanoseconds, which PostgreSQL truncates to microseconds: the digest must
// normalize it, or the round trip fails.
var fixtureTime = time.Date(2026, 11, 18, 12, 30, 45, 123456789, time.UTC)

// fixture writes one row into every listed table (two where a second is needed), with booleans
// set, non-ASCII bytes and nanosecond times, through raw SQL so no table is left to a code
// path. TestFixtureCoversEveryTable fails when a listed table gets no row.
var fixture = []struct {
	sql  string
	args []any
}{
	{`INSERT INTO users (id, username, email, created_at, updated_at, last_login_at, must_change_password, totp_enabled, sso_issuer)
	  VALUES ('u1', 'alice', 'alice@example.test', ?, ?, ?, TRUE, TRUE, 'kyidentity https://id.example')`, []any{fixtureTime, fixtureTime, fixtureTime}},
	{`INSERT INTO users (id, username, created_at, updated_at) VALUES ('u2', 'bob', ?, ?)`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, unproven, sso_sid) VALUES ('s1', 'u1', ?, ?, TRUE, 'sid')`, []any{fixtureTime, fixtureTime.Add(time.Hour)}},
	{`INSERT INTO mfa_challenges (token_hash, user_id, expires_at) VALUES ('m1', 'u1', ?)`, []any{fixtureTime}},
	{`INSERT INTO device_pairings (secret, user_id, created_at, expires_at, authenticated_at) VALUES ('d1', 'u1', ?, ?, ?)`, []any{fixtureTime, fixtureTime, fixtureTime}},
	{`INSERT INTO groups (id, display_name, created_at, updated_at, source) VALUES ('g1', 'Équipe', ?, ?, 'suite')`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO group_members (group_id, user_id) VALUES ('g1', 'u2')`, nil},
	{`INSERT INTO audit_records (id, user_id, action, details, created_at) VALUES (41, 'u1', 'fixture', 'x=1', ?)`, []any{fixtureTime}},
	{`INSERT INTO server_settings (key, value, updated_at) VALUES ('site_theme', 'patina', ?)`, []any{fixtureTime}},
	{`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, seq, min_sync_seq, created_at, components)
	  VALUES ('c1', 'user', 'u1', 'default', 'Café', 5, 2, ?, 'VEVENT,VTODO')`, []any{fixtureTime}},
	{`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at) VALUES ('c2', 'group', 'c2', 'team', 'Team', ?)`, []any{fixtureTime}},
	{`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, last_end, modified_at, component, invitation_sender)
	  VALUES ('c1', 'a.ics', 'uid-a', 'etag-a', ?, 1700000000, NULL, ?, 'VEVENT', 'u2')`, []any{[]byte("BEGIN:VCALENDAR\r\nSUMMARY:Réunion ☕\r\nEND:VCALENDAR\r\n"), fixtureTime}},
	{`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, last_end, modified_at, component)
	  VALUES ('c1', 't.ics', 'uid-t', 'etag-t', ?, 1700000000, 1700003600, ?, 'VTODO')`, []any{[]byte("BEGIN:VCALENDAR\r\nBEGIN:VTODO\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"), fixtureTime}},
	{`INSERT INTO calendar_changes (calendar_id, seq, name, deleted, changed_at) VALUES ('c1', 5, 'a.ics', 1, ?)`, []any{fixtureTime}},
	{`INSERT INTO calendar_grants (calendar_id, group_id, role) VALUES ('c2', 'g1', 'editor')`, nil},
	{`INSERT INTO calendar_subscriptions (calendar_id, url_sealed, url_host, next_check_at, refreshed_at) VALUES ('c2', 'sealed', 'example.test', ?, ?)`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO app_passwords (id, user_id, label, hash, created_at, last_used_at, scope) VALUES ('p1', 'u1', 'agent', 'h', ?, ?, 'mcp:read')`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO mcp_events (token_id, calendar_id, uid) VALUES ('p1', 'c1', 'uid-a')`, nil},
	{`INSERT INTO scheduling_outbox (id, dedupe_key, sender_id, method, recipient, body, next_attempt_at, created_at) VALUES ('o1', 'dk', 'u1', 'REQUEST', 'x@example.test', ?, ?, ?)`, []any{[]byte("body ✉"), fixtureTime, fixtureTime}},
	// A finished message keeps an empty body: an empty BLOB reads back as nil and must stay non-NULL.
	{`INSERT INTO scheduling_outbox (id, dedupe_key, sender_id, method, recipient, body, status, next_attempt_at, created_at) VALUES ('o2', 'dk2', 'u1', 'REPLY', 'y@example.test', ?, 'sent', ?, ?)`, []any{[]byte{}, fixtureTime, fixtureTime}},
	{`INSERT INTO invitation_byte_budget (sender_id, window_start, bytes) VALUES ('u1', 1700000000, 42)`, nil},
	{`INSERT INTO cancellation_reservations (calendar_id, name, sender_id, bytes) VALUES ('c1', 'a.ics', 'u1', 99)`, nil},
	{`INSERT INTO sso_logouts (sid, expires_at) VALUES ('sid', ?)`, []any{fixtureTime}},
	{`INSERT INTO sso_subject_logouts (issuer, subject, cutoff, expires_at) VALUES ('iss', 'sub', ?, ?)`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO video_meetings (id, user_id, calendar_id, name, state, rev, done_rev, next_attempt_at, created_at, refresh_at) VALUES ('v1', 'u1', 'c1', 'a.ics', 'open', 3, 2, ?, ?, ?)`, []any{fixtureTime, fixtureTime, fixtureTime}},
	{`INSERT INTO meeting_guests (meeting_id, email, guest_key) VALUES ('v1', 'g@example.test', 'k')`, nil},
	{`INSERT INTO presence_pushes (user_id, rev, done_rev, next_attempt_at) VALUES ('u1', 4, 1, ?)`, []any{fixtureTime}},
	{`INSERT INTO booking_types (id, user_id, slug, enabled, title, duration_min, zone, windows, notice_min, horizon_days, calendar_id, meeting_room, created_at, updated_at)
	  VALUES ('b1', 'u1', 'slug', TRUE, 'Chat', 30, 'UTC', '[]', 60, 14, 'c1', TRUE, ?, ?)`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO bookings (id, type_id, user_id, status, start_at, end_at, uid, created_at) VALUES ('bk1', 'b1', 'u1', 'held', 1, 2, 'uid-b', ?)`, []any{fixtureTime}},
	{`INSERT INTO resources (id, kind, name, audience, approval, max_minutes, horizon_days, enabled, created_at, updated_at) VALUES ('r1', 'room', 'Salle', 'groups', 'auto', 60, 30, FALSE, ?, ?)`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO resource_groups (resource_id, group_id) VALUES ('r1', 'g1')`, nil},
	{`INSERT INTO resource_approvers (resource_id, user_id) VALUES ('r1', 'u2')`, nil},
	{`INSERT INTO resource_bookings (id, resource_id, calendar_id, object_name, status, fingerprint, created_at, updated_at) VALUES ('rb1', 'r1', 'c1', 'a.ics', 'accepted', 'fp', ?, ?)`, []any{fixtureTime, fixtureTime}},
	{`INSERT INTO resource_busy (booking_id, resource_id, start_at, end_at) VALUES ('rb1', 'r1', 10, 20)`, nil},
	{`INSERT INTO kypost_out_of_office (user_id, revision, away, start_at, end_at, all_day, zone, tentative, comment_local, comment_remote) VALUES ('u1', 7, TRUE, 1, 2, FALSE, 'UTC', TRUE, 'out', 'away')`, nil},
	{`INSERT INTO suite_group_calendars (suite_group_id, calendar_id, group_id, archived) VALUES ('sg1', 'c2', 'g1', TRUE)`, nil},
	{`INSERT INTO task_areas (user_id, data) VALUES ('u1', '[]')`, nil},
	{`INSERT INTO task_views (id, user_id, name, query, created_at) VALUES ('tv1', 'u1', 'Today', '{}', 1700000000)`, nil},
	{`INSERT INTO task_search (calendar_id, uid, etag, body) VALUES ('c1', 'uid-t', 'etag-t', 'cafe crème')`, nil},
	{`INSERT INTO task_completions (user_id, calendar_id, uid, occurrence, completed_at) VALUES ('u1', 'c1', 'uid-t', 'at:1', 1700000000)`, nil},
	{`INSERT INTO task_goals (id, user_id, title, period, starts, ends, areas, lists, created_at) VALUES ('tg1', 'u1', 'Read', 'week', '2026-11-16', '2026-11-22', '[]', '[]', 1700000000)`, nil},
	{`INSERT INTO task_prefs (user_id, kypost_flags) VALUES ('u1', FALSE)`, nil},
	{`INSERT INTO security_windows (key, hits, expires_at) VALUES ('acct:x', 3, 9999999999999)`, nil},
	{`INSERT INTO spent_challenges (salt, expires_at) VALUES ('salt', 9999999999999)`, nil},
	{`INSERT INTO kypost_replay (event_id, expires_at) VALUES ('evt', 9999999999999)`, nil},
}

// fixtureSource writes the fixture into a new SQLite database and returns its path.
func fixtureSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kycalendar.db")
	st, err := openSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := st.(*SQLStore).db
	for _, f := range fixture {
		if _, err := db.Exec(f.sql, f.args...); err != nil {
			t.Fatalf("%s: %v", f.sql, err)
		}
	}
	return path
}

func onPostgres(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("KY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	return dsn
}

// pgTarget is an empty PostgreSQL schema's DSN, dropped when the test ends.
func pgTarget(t *testing.T) string {
	t.Helper()
	dsn := onPostgres(t)
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "rt_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

// TestMigrationTablesCoverTheSchema fails when a table exists on either dialect that the copy
// list does not name (or names one that does not exist).
func TestMigrationTablesCoverTheSchema(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		st, err := openSQLite(context.Background(), filepath.Join(t.TempDir(), "x.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := checkCatalog(context.Background(), st.(*SQLStore).db, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("postgres", func(t *testing.T) {
		st, err := openPostgres(context.Background(), pgTarget(t), 4, 4, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := checkCatalog(context.Background(), st.(*SQLStore).db, true); err != nil {
			t.Fatal(err)
		}
	})
}

// TestFixtureCoversEveryTable fails when a listed table has no fixture row.
func TestFixtureCoversEveryTable(t *testing.T) {
	db, err := sql.Open("sqlite", fixtureSource(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range migrationTables {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quote(table)).Scan(&n); err != nil || n == 0 {
			t.Errorf("table %s has no fixture row (%v)", table, err)
		}
	}
}

// sqliteSums reads every listed table of a SQLite file with the PostgreSQL column types.
func sqliteSums(t *testing.T, path string, cols map[string][]column) map[string]tableSum {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string]tableSum{}
	for _, table := range migrationTables {
		s, err := eachRow(context.Background(), db, table, cols[table], nil)
		if err != nil {
			t.Fatal(err)
		}
		out[table] = s
	}
	return out
}

// SQLite -> migrate-postgres -> SnapshotToSQLite reproduces every table row for row, with the
// usage row equal to the objects' bytes on each side, and sequences past the copied maximum.
func TestWholeSchemaRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := fixtureSource(t)
	dsn := pgTarget(t)
	counts, err := MigrateFromSQLite(ctx, src, dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range migrationTables {
		if counts[table] == 0 {
			t.Errorf("%s: no rows copied", table)
		}
	}
	pg, err := openPostgres(ctx, dsn, 8, 4, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if err := checkUsage(ctx, pg.(*SQLStore).db); err != nil {
		t.Fatal(err)
	}
	cols, err := pgColumns(ctx, pg.(*SQLStore).db)
	if err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(t.TempDir(), "back.db")
	if err := pg.SnapshotToSQLite(ctx, back); err != nil {
		t.Fatal(err)
	}
	want, got := sqliteSums(t, src, cols), sqliteSums(t, back, cols)
	for _, table := range migrationTables {
		if want[table] != got[table] {
			t.Errorf("%s differs after the round trip (%d rows, %d back)", table, want[table].rows, got[table].rows)
		}
	}
	lite, _ := sql.Open("sqlite", back)
	defer lite.Close()
	if err := checkUsage(ctx, lite); err != nil {
		t.Fatal(err)
	}
	assertIdentitiesAdvanced(t, pg.(*SQLStore).db)
}

// identityInserts insert into each table with an owned sequence or identity column without
// naming it. A new serial or identity column with no entry fails the test.
var identityInserts = map[string]string{
	"audit_records.id": `INSERT INTO audit_records (action, created_at) VALUES ('after', now()) RETURNING id`,
}

// assertIdentitiesAdvanced: every owned sequence or identity, found from the catalog, hands
// out an ID above the largest copied one.
func assertIdentitiesAdvanced(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT table_name, column_name FROM information_schema.columns
WHERE table_schema = current_schema() AND (column_default LIKE 'nextval(%' OR is_identity = 'YES')`)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		found = append(found, table+"."+col)
	}
	rows.Close()
	if len(found) == 0 {
		t.Fatal("no owned sequence found; the catalog query is wrong")
	}
	for _, tc := range found {
		insert, ok := identityInserts[tc]
		if !ok {
			t.Errorf("%s is an owned sequence or identity with no fixture in identityInserts", tc)
			continue
		}
		table, col, _ := strings.Cut(tc, ".")
		var maxID, newID int64
		if err := db.QueryRow(`SELECT MAX(` + quote(col) + `) FROM ` + quote(table) + ` WHERE ` + quote(col) + ` < 1000000`).Scan(&maxID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(insert).Scan(&newID); err != nil {
			t.Fatal(err)
		}
		if newID <= maxID {
			t.Errorf("%s handed out %d, not above the copied %d", tc, newID, maxID)
		}
	}
}

// The round trip is mutation-sensitive: leaving a table out of the list, or skipping one
// kind's normalization, makes the copy fail.
func TestRoundTripDetectsAnOmittedTable(t *testing.T) {
	ctx := context.Background()
	src := fixtureSource(t)
	t.Run("omitted table", func(t *testing.T) {
		saved := migrationTables
		migrationTables = slices.DeleteFunc(slices.Clone(saved), func(s string) bool { return s == "meeting_guests" })
		defer func() { migrationTables = saved }()
		if _, err := MigrateFromSQLite(ctx, src, pgTarget(t), true); !errors.Is(err, ErrCopyMismatch) {
			t.Fatalf("a table missing from the list was copied without complaint: %v", err)
		}
	})
	for _, kind := range []string{kindTime, kindBool} {
		t.Run("unnormalized "+kind, func(t *testing.T) {
			skipNormalize = kind
			defer func() { skipNormalize = "" }()
			if _, err := MigrateFromSQLite(ctx, src, pgTarget(t), true); !errors.Is(err, ErrCopyMismatch) {
				t.Fatalf("copy passed without normalizing %s values: %v", kind, err)
			}
		})
	}
}

// Every refusal leaves the target as it was; --check rolls everything back, DDL included.
func TestMigrateRefusesAndChecks(t *testing.T) {
	ctx := context.Background()
	src := fixtureSource(t)
	dsn := pgTarget(t)
	if _, err := MigrateFromSQLite(ctx, src, dsn, true); err != nil {
		t.Fatal(err)
	}
	pg, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	tables, err := catalogTables(ctx, pg, true)
	if err != nil || len(tables) != 0 {
		t.Fatalf("--check left tables behind: %v %v", tables, err)
	}
	if _, err := MigrateFromSQLite(ctx, src, dsn, false); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateFromSQLite(ctx, src, dsn, false); !errors.Is(err, ErrTargetPopulated) {
		t.Fatalf("a populated target was not refused: %v", err)
	}
	var users int
	if err := pg.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 2 {
		t.Fatalf("users after a refused repeat: %d %v", users, err)
	}
}

// At the PostgreSQL pool floor of four connections, the worker lock, the backup-run lock and a
// snapshot transaction still leave one for a request.
func TestPoolFloorLeavesARequestConnection(t *testing.T) {
	dsn := pgTarget(t)
	ctx := context.Background()
	st, err := openPostgres(ctx, dsn, 4, 4, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := st.(*SQLStore)
	held := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	for _, name := range []string{"scheduled-worker", "backup-run"} {
		go func() {
			_, _ = s.Coordination().WithLock(ctx, name, func(context.Context) error {
				held <- struct{}{}
				<-release
				return nil
			})
		}()
		<-held
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	quick, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := st.Users().CountUsers(quick); err != nil {
		t.Fatalf("no connection left for a request at four: %v", err)
	}
}

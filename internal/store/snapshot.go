package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

// migrationTables is every table, in foreign-key order, that a PostgreSQL snapshot and
// migrate-postgres copy. calendar_usage follows calendar_objects: the target's usage row is
// deleted before the copy, so the object triggers find nothing to add to, and the copied
// row lands last. TestMigrationTablesCoverTheSchema fails when a table is missing.
var migrationTables = []string{
	"users", "sessions", "mfa_challenges", "device_pairings",
	"groups", "group_members", "audit_records", "server_settings",
	"calendars", "calendar_meta", "calendar_objects", "calendar_usage", "calendar_changes",
	"calendar_grants", "calendar_subscriptions",
	"app_passwords", "mcp_events",
	"scheduling_outbox", "invitation_byte_budget", "cancellation_reservations",
	"sso_logouts", "sso_subject_logouts",
	"video_meetings", "meeting_guests", "presence_pushes",
	"booking_types", "bookings",
	"resources", "resource_groups", "resource_approvers", "resource_bookings", "resource_busy",
	"kypost_out_of_office", "suite_group_calendars",
	"task_areas", "task_views", "task_search", "task_completions", "task_goals", "task_prefs",
	"security_windows", "spent_challenges", "kypost_replay",
}

// notData are tables that exist but are never copied: the migration record (the target is
// migrated to the same version instead) and SQLite's own bookkeeping and FTS5 shadow tables.
var notData = []string{"schema_migrations", "sqlite_sequence",
	"task_search_config", "task_search_content", "task_search_data", "task_search_docsize", "task_search_idx"}

// ErrCopyMismatch is a copy whose target does not match its source.
var ErrCopyMismatch = errors.New("store: copy verification failed")

// column is one column as PostgreSQL types it; both dialects share the names.
type column struct{ name, kind string }

// Value kinds, from PostgreSQL's data_type. Everything else is text.
const (
	kindBool  = "bool"
	kindTime  = "time"
	kindInt   = "int"
	kindFloat = "float"
	kindBytes = "bytes"
	kindText  = "text"
)

func kindOf(pgType string) string {
	switch pgType {
	case "boolean":
		return kindBool
	case "timestamp with time zone", "timestamp without time zone", "date":
		return kindTime
	case "integer", "bigint", "smallint":
		return kindInt
	case "double precision", "real", "numeric":
		return kindFloat
	case "bytea":
		return kindBytes
	}
	return kindText
}

// skipNormalize disables one kind's normalization (tests prove the digest needs it).
var skipNormalize string

type dbConn interface { // a database or one transaction
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// pgColumns reads the copy's column types from the PostgreSQL side of it.
func pgColumns(ctx context.Context, pg dbConn) (map[string][]column, error) {
	rows, err := pg.QueryContext(ctx, `SELECT table_name, column_name, data_type FROM information_schema.columns
WHERE table_schema = current_schema() ORDER BY table_name, ordinal_position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]column{}
	for rows.Next() {
		var t, c, typ string
		if err := rows.Scan(&t, &c, &typ); err != nil {
			return nil, err
		}
		out[t] = append(out[t], column{c, kindOf(typ)})
	}
	return out, rows.Err()
}

// catalogTables lists the base tables a database holds.
func catalogTables(ctx context.Context, q dbConn, postgres bool) ([]string, error) {
	query := `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`
	if postgres {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name`
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// checkCatalog refuses a schema holding a table the copy would leave behind.
func checkCatalog(ctx context.Context, q dbConn, postgres bool) error {
	tables, err := catalogTables(ctx, q, postgres)
	if err != nil {
		return err
	}
	for _, t := range tables {
		if !slices.Contains(migrationTables, t) && !slices.Contains(notData, t) {
			return fmt.Errorf("%w: table %s is not in the copy list", ErrCopyMismatch, t)
		}
	}
	for _, t := range migrationTables {
		if !slices.Contains(tables, t) {
			return fmt.Errorf("%w: listed table %s does not exist", ErrCopyMismatch, t)
		}
	}
	return nil
}

// tableSum is a table's row count and an order-independent digest of its normalized rows.
type tableSum struct {
	rows   int64
	digest [32]byte
}

type summer struct {
	n      int64
	hashes [][32]byte
}

func (s *summer) add(cols []column, vals []any) error {
	h := sha256.New()
	for i, c := range cols {
		v, err := normalize(vals[i], c.kind)
		if err != nil {
			return fmt.Errorf("column %s: %w", c.name, err)
		}
		_ = binary.Write(h, binary.BigEndian, uint64(len(v)))
		h.Write([]byte(v))
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	s.hashes = append(s.hashes, sum)
	s.n++
	return nil
}

func (s *summer) sum() tableSum {
	slices.SortFunc(s.hashes, func(a, b [32]byte) int { return bytes.Compare(a[:], b[:]) })
	h := sha256.New()
	for _, r := range s.hashes {
		h.Write(r[:])
	}
	var out tableSum
	out.rows = s.n
	copy(out.digest[:], h.Sum(nil))
	return out
}

// normalize is v as both dialects agree on it: booleans as 0/1, times in UTC at PostgreSQL's
// microsecond precision, text and bytes as their bytes.
func normalize(v any, kind string) (string, error) {
	if v == nil {
		return "\x00", nil
	}
	if kind == skipNormalize {
		return fmt.Sprintf("%T%v", v, v), nil
	}
	switch kind {
	case kindBool:
		b, err := asBool(v)
		if b {
			return "b1", err
		}
		return "b0", err
	case kindTime:
		t, err := asTime(v)
		return "t" + t.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), err
	case kindInt:
		i, err := asInt(v)
		return "i" + strconv.FormatInt(i, 10), err
	case kindFloat:
		f, ok := v.(float64)
		if !ok {
			return "", fmt.Errorf("unexpected %T for a number", v)
		}
		return "f" + strconv.FormatFloat(f, 'g', -1, 64), nil
	}
	switch x := v.(type) {
	case []byte:
		return "s" + string(x), nil
	case string:
		return "s" + x, nil
	}
	return "", fmt.Errorf("unexpected %T for text", v)
}

func asBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case int64:
		return x != 0, nil
	}
	return false, fmt.Errorf("unexpected %T for a boolean", v)
}

func asInt(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int32:
		return int64(x), nil
	}
	return 0, fmt.Errorf("unexpected %T for an integer", v)
}

// sqliteTimeLayouts are the forms a SQLite DATETIME column holds: modernc's own, and RFC 3339.
var sqliteTimeLayouts = []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"}

func asTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x, nil
	case string:
		for _, l := range sqliteTimeLayouts {
			if t, err := time.Parse(l, x); err == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("unexpected %T %q for a time", v, fmt.Sprint(v))
}

// toTarget converts a source value for the target's column.
func toTarget(v any, kind string) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch kind {
	case kindBool:
		return asBool(v)
	case kindTime:
		t, err := asTime(v)
		return t.UTC(), err
	case kindInt:
		return asInt(v)
	case kindFloat:
		return v, nil
	}
	b, isBytes := v.([]byte)
	str, isString := v.(string)
	switch {
	case !isBytes && !isString:
		return nil, fmt.Errorf("unexpected %T for text", v)
	case kind == kindBytes && isString:
		return []byte(str), nil // a BLOB stays bytes: SQLite's LENGTH counts text in characters
	case kind == kindBytes && b == nil:
		return []byte{}, nil // an empty BLOB reads back as nil, which the driver would send as NULL
	case kind == kindText && isBytes:
		return string(b), nil
	}
	return v, nil
}

func quote(name string) string { return `"` + name + `"` }

// copyTables copies every listed table from src to dst, which must be empty apart from the
// rows migrations seed, and returns the source's sums. dst rows are deleted first, last table
// first. toPG says which side is PostgreSQL; cols come from it.
func copyTables(ctx context.Context, src, dst dbConn, cols map[string][]column, toPG bool) (map[string]tableSum, error) {
	for i := len(migrationTables) - 1; i >= 0; i-- {
		if _, err := dst.ExecContext(ctx, "DELETE FROM "+quote(migrationTables[i])); err != nil {
			return nil, fmt.Errorf("clear %s: %w", migrationTables[i], err)
		}
	}
	sums := map[string]tableSum{}
	for _, t := range migrationTables {
		cs := cols[t]
		if len(cs) == 0 {
			return nil, fmt.Errorf("%w: no columns for %s", ErrCopyMismatch, t)
		}
		names := make([]string, len(cs))
		marks := make([]string, len(cs))
		for i, c := range cs {
			names[i] = quote(c.name)
			marks[i] = "?"
			if toPG {
				marks[i] = "$" + strconv.Itoa(i+1)
			}
		}
		list := strings.Join(names, ", ")
		insert := "INSERT INTO " + quote(t) + " (" + list + ") VALUES (" + strings.Join(marks, ", ") + ")"
		sum, err := eachRow(ctx, src, t, cs, func(vals []any) error {
			args := make([]any, len(vals))
			for i, v := range vals {
				var err error
				if args[i], err = toTarget(v, cs[i].kind); err != nil {
					return fmt.Errorf("%s.%s: %w", t, cs[i].name, err)
				}
			}
			_, err := dst.ExecContext(ctx, insert, args...)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", t, err)
		}
		sums[t] = sum
	}
	return sums, nil
}

// eachRow reads every row of t, sums it and hands it to fn (nil reads only).
func eachRow(ctx context.Context, q dbConn, t string, cs []column, fn func([]any) error) (tableSum, error) {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = quote(c.name)
	}
	rows, err := q.QueryContext(ctx, "SELECT "+strings.Join(names, ", ")+" FROM "+quote(t))
	if err != nil {
		return tableSum{}, err
	}
	defer rows.Close()
	var s summer
	vals := make([]any, len(cs))
	ptrs := make([]any, len(cs))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return tableSum{}, err
		}
		if err := s.add(cs, vals); err != nil {
			return tableSum{}, fmt.Errorf("%s: %w", t, err)
		}
		if fn != nil {
			if err := fn(vals); err != nil {
				return tableSum{}, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return tableSum{}, err
	}
	return s.sum(), nil
}

// verifyCopy compares dst with the source sums, table by table, and checks the usage row.
func verifyCopy(ctx context.Context, dst dbConn, cols map[string][]column, want map[string]tableSum, dstPG bool) error {
	if err := checkCatalog(ctx, dst, dstPG); err != nil {
		return err
	}
	for _, t := range migrationTables {
		got, err := eachRow(ctx, dst, t, cols[t], nil)
		if err != nil {
			return fmt.Errorf("verify %s: %w", t, err)
		}
		if got != want[t] {
			return fmt.Errorf("%w: table %s differs (%d rows copied of %d)", ErrCopyMismatch, t, got.rows, want[t].rows)
		}
	}
	return checkUsage(ctx, dst)
}

// checkUsage proves the copied calendar_usage row equals the bytes the objects hold.
func checkUsage(ctx context.Context, q dbConn) error {
	var usage, sum int64
	if err := q.QueryRowContext(ctx, `SELECT bytes FROM calendar_usage WHERE id = 1`).Scan(&usage); err != nil {
		return fmt.Errorf("%w: calendar_usage: %v", ErrCopyMismatch, err)
	}
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(data)), 0) FROM calendar_objects`).Scan(&sum); err != nil {
		return err
	}
	if usage != sum {
		return fmt.Errorf("%w: calendar_usage is %d bytes but the objects hold %d", ErrCopyMismatch, usage, sum)
	}
	return nil
}

// advanceSequences moves every owned sequence and identity past the copied maximum, found
// from the catalog so a new serial column is covered without a code change.
func advanceSequences(ctx context.Context, tx dbConn) error {
	rows, err := tx.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns
WHERE table_schema = current_schema() AND (column_default LIKE 'nextval(%' OR is_identity = 'YES')`)
	if err != nil {
		return err
	}
	var cols [][2]string
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			rows.Close()
			return err
		}
		cols = append(cols, [2]string{t, c})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, tc := range cols {
		q := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence($1, $2), COALESCE(MAX(%s), 0) + 1, false) FROM %s`, quote(tc[1]), quote(tc[0]))
		if _, err := tx.ExecContext(ctx, q, tc[0], tc[1]); err != nil {
			return fmt.Errorf("advance %s.%s: %w", tc[0], tc[1], err)
		}
	}
	return nil
}

// SnapshotToSQLite writes this PostgreSQL database to a new SQLite file at path, from one
// REPEATABLE READ READ ONLY transaction: the schema is migrated to this build's version, every
// listed table copied and compared row for row (counts and normalized digests against the
// same transaction), and SQLite's integrity and foreign-key checks run. path must not exist.
func (s *SQLStore) SnapshotToSQLite(ctx context.Context, path string) error {
	if s.driver != "postgres" {
		return errors.New("store: SnapshotToSQLite reads PostgreSQL; snapshot SQLite with VACUUM INTO")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkSchemaVersion(ctx, tx); err != nil {
		return err
	}
	if err := checkCatalog(ctx, tx, true); err != nil {
		return err
	}
	cols, err := pgColumns(ctx, tx)
	if err != nil {
		return err
	}
	lite, err := openSQLite(ctx, path+"?_pragma=journal_mode(DELETE)")
	if err != nil {
		return err
	}
	defer lite.Close()
	db := lite.(*SQLStore).db
	ltx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer ltx.Rollback()
	sums, err := copyTables(ctx, tx, ltx, cols, false)
	if err != nil {
		return err
	}
	if err := ltx.Commit(); err != nil {
		return err
	}
	if err := verifyCopy(ctx, db, cols, sums, false); err != nil {
		return err
	}
	return CheckSQLite(ctx, db)
}

// CheckSQLite runs SQLite's integrity and foreign-key checks.
func CheckSQLite(ctx context.Context, db dbConn) error {
	var ok string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ok); err != nil {
		return err
	}
	if ok != "ok" {
		return fmt.Errorf("SQLite integrity_check: %s", ok)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("SQLite foreign_key_check found rows referring to nothing")
	}
	return rows.Err()
}

// checkSchemaVersion refuses a database this build did not fully migrate, newer or older.
func checkSchemaVersion(ctx context.Context, q dbConn) error {
	v, err := SchemaVersion(ctx, q)
	if err != nil {
		return err
	}
	if want := migrations.LatestVersion(); v != want {
		return fmt.Errorf("store: schema version %d, this build has %d", v, want)
	}
	return nil
}

// SchemaVersion is the highest migration a database records.
func SchemaVersion(ctx context.Context, q dbConn) (int, error) {
	var v sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

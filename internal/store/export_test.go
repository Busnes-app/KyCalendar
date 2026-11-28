package store

import (
	"context"
	"database/sql"
)

// SetBetweenSCIMWritesForTest runs f between UpdateSCIMUser's removal and grant transactions and
// returns the restore func. Test-only.
func SetBetweenSCIMWritesForTest(f func()) func() {
	old := betweenSCIMWrites
	betweenSCIMWrites = f
	return func() { betweenSCIMWrites = old }
}

// OutboxBodyLenForTest is the stored body length of outbox row id, and whether the row exists.
func OutboxBodyLenForTest(st Store, id string) (int, bool) {
	s := st.(*SQLStore)
	var n int
	err := s.db.QueryRow(s.rebind(`SELECT LENGTH(body) FROM scheduling_outbox WHERE id = ?`), id).Scan(&n)
	return n, err == nil
}

// LockHolderForTest is the backend pid holding name's session lock in this schema.
func LockHolderForTest(st Store, name string) (int, error) {
	s := st.(*SQLStore)
	var pid int
	err := s.db.QueryRow(`SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND granted AND classid = 3
AND objid = ((hashtext(current_schema() || ':' || $1)::bigint + 4294967296) % 4294967296)::text::oid
AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`, name).Scan(&pid)
	return pid, err
}

// TerminateBackendForTest ends backend pid, as a database restart or an operator would.
func TerminateBackendForTest(st Store, pid int) error {
	_, err := st.(*SQLStore).db.Exec(`SELECT pg_terminate_backend($1)`, pid)
	return err
}

// RowCountForTest counts table's rows matching where.
func RowCountForTest(st Store, table, where string) (int, error) {
	var n int
	err := st.(*SQLStore).db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE ` + where).Scan(&n)
	return n, err
}

// HoldLockForTest begins a transaction holding key's advisory lock, as BindSignIn holds
// local-admins, and returns a func that runs stmt in it and commits.
func HoldLockForTest(st Store, key string) (func(stmt string, args ...any) error, error) {
	s := st.(*SQLStore)
	tx, err := s.lockedTx(context.Background(), key)
	if err != nil {
		return nil, err
	}
	return func(stmt string, args ...any) error {
		defer tx.Rollback()
		if _, err := tx.Exec(s.rebind(stmt), args...); err != nil {
			return err
		}
		return tx.Commit()
	}, nil
}

// DBForTest is st's database and its placeholder rebinding. Test-only.
func DBForTest(st Store) (*sql.DB, func(string) string) {
	s := st.(*SQLStore)
	return s.db, s.rebind
}

// SetSweepHookForTest runs f after each row a sweep batch seals; an error aborts the batch.
func SetSweepHookForTest(f func(sealed int) error) func() {
	old := sweepHook
	sweepHook = f
	return func() { sweepHook = old }
}

// SetSweepReadHookForTest runs f between a sweep batch's read and its transaction.
func SetSweepReadHookForTest(f func()) func() {
	old := sweepReadHook
	sweepReadHook = f
	return func() { sweepReadHook = old }
}

// WaitingOnLockForTest reports whether some transaction is waiting for key's advisory lock.
func WaitingOnLockForTest(st Store, key string) (bool, error) {
	var waiting bool
	err := st.(*SQLStore).db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND NOT granted
AND objsubid = 1 AND ((classid::bigint << 32) | objid::bigint) = hashtextextended($1, 0))`, key).Scan(&waiting)
	return waiting, err
}

// OpenObjectForTest merges o, an object read earlier, for ctx's credential, as a read does.
func OpenObjectForTest(ctx context.Context, st Store, o *CalendarObject) error {
	return st.(*SQLStore).calendars.openObjects(ctx, []*CalendarObject{o})
}

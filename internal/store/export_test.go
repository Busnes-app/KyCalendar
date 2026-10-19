package store

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

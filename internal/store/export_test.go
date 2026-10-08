package store

// SetBetweenSCIMWritesForTest runs f between UpdateSCIMUser's removal and grant transactions and
// returns the restore func. Test-only.
func SetBetweenSCIMWritesForTest(f func()) func() {
	old := betweenSCIMWrites
	betweenSCIMWrites = f
	return func() { betweenSCIMWrites = old }
}

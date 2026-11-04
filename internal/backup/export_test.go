package backup

// SetPartBytesForTest splits databases larger than n bytes, so tests need no 64 MiB database.
func SetPartBytesForTest(n int) func() {
	old := partBytes
	partBytes = n
	return func() { partBytes = old }
}

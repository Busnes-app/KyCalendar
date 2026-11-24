package backup

import "net/http"

// SetPartBytesForTest splits databases larger than n bytes, so tests need no 64 MiB database.
func SetPartBytesForTest(n int) func() {
	old := partBytes
	partBytes = n
	return func() { partBytes = old }
}

// SetObjectHTTPForTest points the S3 client at a test server's TLS roots.
func SetObjectHTTPForTest(c *http.Client) func() {
	old := objectHTTP
	objectHTTP = c
	return func() { objectHTTP = old }
}

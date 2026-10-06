package calendar

import (
	"errors"
	"strconv"
	"strings"
)

const syncTokenPrefix = "urn:kycalendar:sync:"

var errBadSyncToken = errors.New("calendar: malformed sync token")

// FormatSyncToken encodes the instance sync epoch and a calendar sequence as a URI.
func FormatSyncToken(epoch string, seq int64) string {
	return syncTokenPrefix + epoch + ":" + strconv.FormatInt(seq, 10)
}

// ParseSyncToken is the inverse of FormatSyncToken.
func ParseSyncToken(token string) (string, int64, error) {
	rest, ok := strings.CutPrefix(token, syncTokenPrefix)
	if !ok {
		return "", 0, errBadSyncToken
	}
	epoch, seqText, ok := strings.Cut(rest, ":")
	if !ok || epoch == "" {
		return "", 0, errBadSyncToken
	}
	seq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil || seq < 0 {
		return "", 0, errBadSyncToken
	}
	return epoch, seq, nil
}

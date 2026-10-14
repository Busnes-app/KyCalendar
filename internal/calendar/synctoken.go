package calendar

import (
	"errors"
	"strconv"
	"strings"
)

const syncTokenPrefix = "urn:kycalendar:sync:"

var errBadSyncToken = errors.New("calendar: malformed sync token")

// FormatSyncToken encodes the instance sync epoch, the calendar's ID and its sequence as a URI.
// The ID ties the token to one calendar: one recreated at the same path restarts its sequence.
func FormatSyncToken(epoch, calendarID string, seq int64) string {
	return syncTokenPrefix + epoch + ":" + calendarID + ":" + strconv.FormatInt(seq, 10)
}

// ParseSyncToken is the inverse of FormatSyncToken.
func ParseSyncToken(token string) (epoch, calendarID string, seq int64, err error) {
	rest, ok := strings.CutPrefix(token, syncTokenPrefix)
	parts := strings.Split(rest, ":")
	if !ok || len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return "", "", 0, errBadSyncToken
	}
	seq, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil || seq < 0 {
		return "", "", 0, errBadSyncToken
	}
	return parts[0], parts[1], seq, nil
}

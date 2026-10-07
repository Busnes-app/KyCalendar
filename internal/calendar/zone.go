package calendar

import (
	"strings"
	"time"
)

//go:generate go run ./gen/windowszones -out windowszones_gen.go

// ResolveZone maps an iCalendar TZID to a location. It accepts an IANA name, a Windows zone name
// (Outlook) through CLDR's default mapping, or an old Thunderbird "/mozilla.org/<version>/<IANA>"
// path. ok is false when none matches; callers show such times in UTC and flag them.
func ResolveZone(tzid string) (*time.Location, bool) {
	if tzid == "" || tzid == "Local" {
		return nil, false
	}
	// Check Windows zones map first, in case a Windows name shadows an IANA name
	if iana, ok := windowsZones[tzid]; ok {
		if loc, err := time.LoadLocation(iana); err == nil {
			return loc, true
		}
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc, true
	}
	if rest, ok := strings.CutPrefix(tzid, "/mozilla.org/"); ok {
		if _, name, ok := strings.Cut(rest, "/"); ok {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc, true
			}
		}
	}
	return nil, false
}

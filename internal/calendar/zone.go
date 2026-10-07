package calendar

import (
	"strings"
	"time"
)

//go:generate go run ./gen/windowszones -out windowszones_gen.go

// loadIANA safely loads an IANA zone name, rejecting empty and Local.
func loadIANA(name string) (*time.Location, bool) {
	if name == "" || name == "Local" {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	return loc, err == nil
}

// ResolveZone maps an iCalendar TZID to a location. It accepts an IANA name, a Windows zone name
// (Outlook) through CLDR's default mapping, or an old Thunderbird "/mozilla.org/<version>/<IANA>"
// path. ok is false when none matches; callers show such times in UTC and flag them.
func ResolveZone(tzid string) (*time.Location, bool) {
	if tzid == "" || tzid == "Local" {
		return nil, false
	}
	if loc, ok := loadIANA(tzid); ok {
		return loc, true
	}
	if iana, ok := windowsZones[tzid]; ok {
		if loc, ok := loadIANA(iana); ok {
			return loc, true
		}
	}
	if rest, ok := strings.CutPrefix(tzid, "/mozilla.org/"); ok {
		if _, name, ok := strings.Cut(rest, "/"); ok {
			if loc, ok := loadIANA(name); ok {
				return loc, true
			}
		}
	}
	return nil, false
}

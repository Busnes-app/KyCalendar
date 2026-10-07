package calendar

import "testing"

func TestResolveZone(t *testing.T) {
	cases := []struct {
		tzid, want string
		ok         bool
	}{
		{"Europe/Berlin", "Europe/Berlin", true},
		{"Eastern Standard Time", "America/New_York", true},
		{"W. Europe Standard Time", "Europe/Berlin", true},
		{"Tokyo Standard Time", "Asia/Tokyo", true},
		{"/mozilla.org/20050126_1/America/New_York", "America/New_York", true},
		{"Custom/Nowhere", "", false},
		{"", "", false},
		{"Local", "", false},
		{"../../etc/passwd", "", false},
	}
	for _, tc := range cases {
		loc, ok := ResolveZone(tc.tzid)
		if ok != tc.ok || (ok && loc.String() != tc.want) {
			t.Errorf("ResolveZone(%q) = %v, %v; want %q, %v", tc.tzid, loc, ok, tc.want, tc.ok)
		}
	}
}

// Every IANA zone the CLDR table names must load from Go's data, or the mapping is useless.
func TestWindowsZonesAllLoad(t *testing.T) {
	if len(windowsZones) < 130 {
		t.Fatalf("windows table has %d entries; regenerate it", len(windowsZones))
	}
	for win, iana := range windowsZones {
		if loc, ok := ResolveZone(win); !ok || loc.String() != iana {
			t.Errorf("%q -> %q does not load", win, iana)
		}
	}
}

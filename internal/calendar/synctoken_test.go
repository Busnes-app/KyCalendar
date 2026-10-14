package calendar

import "testing"

func TestSyncTokenRoundTrip(t *testing.T) {
	tok := FormatSyncToken("ab12", "cal_x", 42)
	epoch, id, seq, err := ParseSyncToken(tok)
	if err != nil || epoch != "ab12" || id != "cal_x" || seq != 42 {
		t.Fatalf("%q -> %q %q %d %v", tok, epoch, id, seq, err)
	}
}

func TestSyncTokenGarbage(t *testing.T) {
	for _, s := range []string{"", "nope", "urn:kycalendar:sync:ab12", "urn:kycalendar:sync:ab12:4", "urn:kycalendar:sync:ab12:c:-1",
		"urn:kycalendar:sync::c:4", "urn:kycalendar:sync:ab12::4", "urn:kycalendar:sync:ab12:c:x", "urn:kycalendar:sync:ab12:c:d:4"} {
		if _, _, _, err := ParseSyncToken(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

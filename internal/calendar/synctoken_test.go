package calendar

import "testing"

func TestSyncTokenRoundTrip(t *testing.T) {
	tok := FormatSyncToken("ab12", 42)
	epoch, seq, err := ParseSyncToken(tok)
	if err != nil || epoch != "ab12" || seq != 42 {
		t.Fatalf("%q -> %q %d %v", tok, epoch, seq, err)
	}
}

func TestSyncTokenGarbage(t *testing.T) {
	for _, s := range []string{"", "nope", "urn:kycalendar:sync:ab12", "urn:kycalendar:sync:ab12:-1", "urn:kycalendar:sync::4", "urn:kycalendar:sync:ab12:x"} {
		if _, _, err := ParseSyncToken(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

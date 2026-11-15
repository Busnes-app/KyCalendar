package itip

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func decodeForTest(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(s)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

// A REPLY carries the attendee's COMMENT only when they changed it with their answer, so the
// organizer's own note in the copy is never echoed back.
func TestAttendeeReplyCarriesANewComment(t *testing.T) {
	copyOf := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:u\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261112T090000Z\r\n" +
		"COMMENT:organizer note\r\nORGANIZER:mailto:org@example.org\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:me@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	self, org := Address("mailto:me@example.com"), Address("mailto:org@example.org")
	for name, comment := range map[string]string{"new comment": "I am away.", "unchanged": ""} {
		old, next := decodeForTest(t, copyOf), decodeForTest(t, copyOf)
		if err := SetPartstat(next, "", self, "TENTATIVE"); err != nil {
			t.Fatal(err)
		}
		if comment != "" {
			SetComment(next, "", comment)
		}
		m := attendeeReply(self, org, old, next, time.Now())
		var buf bytes.Buffer
		if m == nil || ical.NewEncoder(&buf).Encode(m.Calendar) != nil {
			t.Fatalf("%s: no reply", name)
		}
		got := buf.String()
		if !strings.Contains(got, "PARTSTAT=TENTATIVE") || strings.Contains(got, "organizer note") {
			t.Fatalf("%s: %s", name, got)
		}
		if comment != "" && !strings.Contains(got, "COMMENT:"+comment) {
			t.Fatalf("%s: no comment in %s", name, got)
		}
	}
}

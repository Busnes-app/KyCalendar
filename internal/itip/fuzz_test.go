package itip

import (
	"strings"
	"testing"
)

const storedObject = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261001T000000Z\r\n" +
	"DTSTART:20261110T090000Z\r\nSUMMARY:Planning\r\nORGANIZER:mailto:alice@example.com\r\n" +
	"ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:carol@example.net\r\n" +
	"END:VEVENT\r\nEND:VCALENDAR\r\n"

// FuzzInbound holds every inbound message to the hostile-input rules: Decode, Validate and
// ApplyReply never panic, and a reply never changes anything but the sender's PARTSTAT.
func FuzzInbound(f *testing.F) {
	f.Add("BEGIN:VCALENDAR\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:m1\r\nORGANIZER:mailto:alice@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	f.Add("BEGIN:VCALENDAR\r\nX;A=\"b\"c:v\r\nEND:VCALENDAR\r\n")
	f.Add(storedObject)
	f.Fuzz(func(t *testing.T, body string) {
		msg, err := Decode([]byte(body))
		if err != nil {
			return
		}
		if _, _, err := Validate(msg); err != nil {
			return
		}
		stored, err := Decode([]byte(storedObject))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyReply(stored, msg, bob); err != nil {
			return
		}
		after := encode(t, stored)
		for _, keep := range []string{"SUMMARY:Planning", "DTSTART:20261110T090000Z", "PARTSTAT=NEEDS-ACTION:mailto:carol@example.net", "ORGANIZER:mailto:alice@example.com"} {
			if !strings.Contains(after, keep) {
				t.Fatalf("a reply changed %q:\n%s", keep, after)
			}
		}
	})
}

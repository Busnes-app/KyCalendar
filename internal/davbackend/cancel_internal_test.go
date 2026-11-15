package davbackend

import (
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/itip"
)

// Cancelling one answered occurrence marks only its override: STATUS, the CANCEL's SEQUENCE and
// DTSTAMP, and no alarm. The series keeps its own reminder.
func TestCancelledOccurrenceTakesTheMessageAndDropsAlarms(t *testing.T) {
	copy := decodeICS(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nBEGIN:VEVENT\nUID:m\nDTSTAMP:20261001T000000Z\nSEQUENCE:1\n"+
		"DTSTART:20261102T090000Z\nDTEND:20261102T100000Z\nRRULE:FREQ=DAILY;COUNT=5\nORGANIZER:mailto:alice@example.com\n"+
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com\nX-KYCALENDAR-ORGANIZER-SEQUENCE:1\n"+
		"BEGIN:VALARM\nACTION:DISPLAY\nDESCRIPTION:Soon\nTRIGGER:-PT10M\nEND:VALARM\nEND:VEVENT\nEND:VCALENDAR\n")
	msg := decodeICS(t, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nMETHOD:CANCEL\nBEGIN:VEVENT\nUID:m\nDTSTAMP:20261105T120000Z\nSEQUENCE:3\n"+
		"RECURRENCE-ID:20261104T090000Z\nDTSTART:20261104T090000Z\nORGANIZER:mailto:alice@example.com\nATTENDEE:mailto:bob@example.com\n"+
		"STATUS:CANCELLED\nEND:VEVENT\nEND:VCALENDAR\n")
	next, err := deliverOccurrences(copy, itip.Address("mailto:bob@example.com"), itip.MethodCancel, msg, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range strings.Split(encodeICS(t, next), "BEGIN:VEVENT")[1:] {
		if strings.Contains(part, "RECURRENCE-ID") {
			if !strings.Contains(part, "STATUS:CANCELLED") || !strings.Contains(part, "SEQUENCE:3") || !strings.Contains(part, "DTSTAMP:20261105T120000Z") ||
				!strings.Contains(part, "PARTSTAT=ACCEPTED") || strings.Contains(part, "VALARM") {
				t.Fatalf("the cancelled occurrence:\n%s", part)
			}
		} else if strings.Contains(part, "CANCELLED") || !strings.Contains(part, "VALARM") {
			t.Fatalf("the series:\n%s", part)
		}
	}
}

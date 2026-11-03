package davbackend

import (
	"fmt"
	"strings"
	"testing"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/itip"
)

func decodeICS(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(s, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func overrideCount(cal *ical.Calendar) int {
	n := 0
	for _, c := range cal.Children {
		if c.Props.Get(ical.PropRecurrenceID) != nil {
			n++
		}
	}
	return n
}

// A CANCEL naming more occurrences than maxNewOverrides is refused whole, before any copy of the
// master is made.
func TestDeliverOccurrencesIsBounded(t *testing.T) {
	copyICS := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nBEGIN:VEVENT\nUID:m\nDTSTAMP:20261001T000000Z\n" +
		"DTSTART:20261102T090000Z\nDTEND:20261102T100000Z\nRRULE:FREQ=DAILY;COUNT=60\n" +
		"ORGANIZER:mailto:dave@example.org\nATTENDEE:mailto:bob@example.com\nEND:VEVENT\nEND:VCALENDAR\n"
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nMETHOD:CANCEL\n")
	for d := 3; d < 3+maxNewOverrides+1; d++ {
		fmt.Fprintf(&b, "BEGIN:VEVENT\nUID:m\nDTSTAMP:20261001T000000Z\nRECURRENCE-ID:202611%02dT090000Z\nORGANIZER:mailto:dave@example.org\nEND:VEVENT\n", d)
	}
	b.WriteString("END:VCALENDAR\n")
	existing := decodeICS(t, copyICS)
	if _, err := deliverOccurrences(existing, "mailto:bob@example.com", itip.MethodCancel, decodeICS(t, b.String())); err != errTooManyOccurrences || overrideCount(existing) != 0 {
		t.Fatalf("err %v, %d overrides", err, overrideCount(existing))
	}
}

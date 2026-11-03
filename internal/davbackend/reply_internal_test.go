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

// A reply adds overrides only for an attendee of the series answering an occurrence differently,
// as of the series' SEQUENCE; one needing more than maxNewOverrides adds none and is refused.
func TestAddOverridesIsBounded(t *testing.T) {
	series := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nBEGIN:VEVENT\nUID:m\nDTSTAMP:20261001T000000Z\nSEQUENCE:2\n" +
		"DTSTART;TZID=Europe/Berlin:20261102T090000\nDTEND;TZID=Europe/Berlin:20261102T100000\nRRULE:FREQ=DAILY;COUNT=60\n" +
		"ORGANIZER:mailto:alice@example.com\nATTENDEE:mailto:bob@example.com\nEND:VEVENT\nEND:VCALENDAR\n"
	reply := func(who, ps string, days, seq int) *ical.Calendar {
		var b strings.Builder
		b.WriteString("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nMETHOD:REPLY\n")
		for d := 3; d < 3+days; d++ {
			fmt.Fprintf(&b, "BEGIN:VEVENT\nUID:m\nDTSTAMP:20261001T000000Z\nSEQUENCE:%d\nORGANIZER:mailto:alice@example.com\n", seq)
			fmt.Fprintf(&b, "RECURRENCE-ID;TZID=Europe/Berlin:202611%02dT090000\n", d)
			b.WriteString("ATTENDEE;PARTSTAT=" + ps + ":mailto:" + who + "@example.com\nEND:VEVENT\n")
		}
		b.WriteString("END:VCALENDAR\n")
		return decodeICS(t, b.String())
	}
	for _, tc := range []struct {
		who, ps   string
		days, seq int
		want      int
		refused   bool
	}{
		{"mallory", "ACCEPTED", 5, 2, 0, false},
		{"bob", "NEEDS-ACTION", 5, 2, 0, false},
		{"bob", "ACCEPTED", 5, 1, 0, false},
		{"bob", "ACCEPTED", 5, 2, 5, false},
		{"bob", "ACCEPTED", maxNewOverrides + 5, 2, 0, true},
	} {
		stored := decodeICS(t, series)
		err := addOverrides(stored, reply(tc.who, tc.ps, tc.days, tc.seq), itip.Address("mailto:"+tc.who+"@example.com"))
		if got := overrideCount(stored); got != tc.want || (err != nil) != tc.refused {
			t.Errorf("%s %s x%d seq %d: %d overrides, err %v; want %d", tc.who, tc.ps, tc.days, tc.seq, got, err, tc.want)
		}
	}
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

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
// and at most maxReplyOverrides of them.
func TestAddOverridesIsBounded(t *testing.T) {
	series := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nBEGIN:VEVENT\nUID:m\nDTSTAMP:20261001T000000Z\n" +
		"DTSTART;TZID=Europe/Berlin:20261102T090000\nDTEND;TZID=Europe/Berlin:20261102T100000\nRRULE:FREQ=DAILY;COUNT=60\n" +
		"ORGANIZER:mailto:alice@example.com\nATTENDEE:mailto:bob@example.com\nEND:VEVENT\nEND:VCALENDAR\n"
	reply := func(who, ps string, days int) *ical.Calendar {
		var b strings.Builder
		b.WriteString("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nMETHOD:REPLY\n")
		for d := 3; d < 3+days; d++ {
			b.WriteString("BEGIN:VEVENT\nUID:m\nDTSTAMP:20261001T000000Z\nORGANIZER:mailto:alice@example.com\n")
			fmt.Fprintf(&b, "RECURRENCE-ID;TZID=Europe/Berlin:202611%02dT090000\n", d)
			b.WriteString("ATTENDEE;PARTSTAT=" + ps + ":mailto:" + who + "@example.com\nEND:VEVENT\n")
		}
		b.WriteString("END:VCALENDAR\n")
		return decodeICS(t, b.String())
	}
	for _, tc := range []struct {
		who, ps string
		want    int
	}{{"mallory", "ACCEPTED", 0}, {"bob", "NEEDS-ACTION", 0}, {"bob", "ACCEPTED", maxReplyOverrides}} {
		stored := decodeICS(t, series)
		addOverrides(stored, reply(tc.who, tc.ps, 25), itip.Address("mailto:"+tc.who+"@example.com"))
		if got := overrideCount(stored); got != tc.want {
			t.Errorf("%s %s: %d overrides, want %d", tc.who, tc.ps, got, tc.want)
		}
	}
}

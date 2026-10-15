package feed

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// go-ical panics on some malformed parameters. Parse must turn that into a refusal, or a
// background refresh would take the whole server down.
func TestParseContainsDecoderPanics(t *testing.T) {
	for name, body := range map[string]string{
		"junk after a quoted value": "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nX-A;P=\"v\"x:1\r\nEND:VCALENDAR\r\n",
		"parameter at end of line":  "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nX-A;P=\r\nEND:VCALENDAR\r\n",
		"missing END":               "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\n",
	} {
		if _, _, err := Parse("c", []byte(body)); Message(err) != string(errNotCalendar) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// go-ical decodes parameter values a byte at a time, so a megabyte one would take hours. Every
// shape the decoder would read as a parameter value is refused before decoding, at once.
func TestParseRefusesHugeParameters(t *testing.T) {
	head := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:u\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260101T090000Z\r\n"
	tail := "END:VEVENT\r\nEND:VCALENDAR\r\n"
	big := strings.Repeat("a", 4<<20)
	for name, line := range map[string]string{
		"plain":                  "X-A;P=" + big + ":v",
		"folded":                 "X-A;P=" + strings.Repeat("a", 70) + strings.Repeat("\r\n "+strings.Repeat("a", 70), 60000) + ":v",
		"name past a colon":      "X-A;A:=" + big + ":v", // the decoder finds '=' after the ':'
		"bare carriage returns":  "X-A;P=" + strings.Repeat("a\r", 2<<20) + ":v",
		"quoted, colons inside":  `X-A;P="` + strings.Repeat("a:", 2<<20) + `":v`,
		"quoted, unterminated":   `X-A;P="` + big,
		"unquoted, then a quote": "X-A;P=" + big + `"`,
		"second value of a list": "X-A;P=a," + big + ":v",
		"second parameter":       "X-A;Q=1;P=" + big + ":v",
	} {
		start := time.Now()
		_, _, err := Parse("c", []byte(head+line+"\r\n"+tail))
		if Message(err) != string(errNotCalendar) || time.Since(start) > 2*time.Second {
			t.Errorf("%s: %v after %v", name, err, time.Since(start))
		}
	}
	for name, line := range map[string]string{
		"long value":        "DESCRIPTION:" + strings.Repeat("x", 70) + strings.Repeat("\r\n "+strings.Repeat("x", 70), 2000),
		"value with colons": "DESCRIPTION;LANGUAGE=en:" + strings.Repeat("a:b;c=", 20000),
		"a long, real one":  "ATTENDEE;CN=\"Doe, Jane\";ROLE=REQ-PARTICIPANT:mailto:jane@example.com",
	} {
		if objs, _, err := Parse("c", []byte(head+line+"\r\n"+tail)); err != nil || len(objs) != 1 {
			t.Errorf("%s: %d objects, %v", name, len(objs), err)
		}
	}
}

// Time zones are copied into each object that names them, so a small feed can expand far past
// the download cap. The parsed total is capped.
func TestParseCapsExpandedSize(t *testing.T) {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VTIMEZONE\r\nTZID:Big\r\n")
	for i := range 3000 {
		fmt.Fprintf(&b, "BEGIN:STANDARD\r\nDTSTART:%04d0101T000000\r\nTZOFFSETFROM:+0100\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\n", 1601+i%400)
	}
	b.WriteString("END:VTIMEZONE\r\n")
	for i := range 300 {
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:%d\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=Big:20260101T090000\r\nEND:VEVENT\r\n", i)
	}
	b.WriteString("END:VCALENDAR\r\n")
	if b.Len() > MaxBody {
		t.Fatalf("fixture is %d bytes, over the download cap", b.Len())
	}
	if _, _, err := Parse("c", []byte(b.String())); Message(err) != string(errTooLarge) {
		t.Errorf("a %d-byte feed expanding past %d: %v", b.Len(), maxParsed, err)
	}
}

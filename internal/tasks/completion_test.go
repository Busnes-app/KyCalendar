package tasks

import (
	"strings"
	"testing"
	"time"
)

const todoOpen = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VTODO
UID:t1
DTSTAMP:20261101T000000Z
CREATED:20261101T000000Z
SUMMARY:Water
DUE;VALUE=DATE:20261110
STATUS:NEEDS-ACTION
END:VTODO
END:VCALENDAR
`

func TestCompletionChanges(t *testing.T) {
	recv := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	open := decode(t, todoOpen)
	doneAt := func(c string) string {
		return strings.Replace(todoOpen, "STATUS:NEEDS-ACTION", "STATUS:COMPLETED\nCOMPLETED:"+c, 1)
	}
	// A phone's completion keeps its own older time; one in the future or before CREATED is clamped.
	for stamp, want := range map[string]time.Time{
		"20261112T080000Z": time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC),
		"20261201T000000Z": recv,
		"20251001T000000Z": recv,
	} {
		c, ok := Change(open, decode(t, doneAt(stamp)), recv)
		if !ok || !c.Done || !c.At.Equal(want) || c.Occurrence != "20261110" {
			t.Errorf("%s: %+v %v", stamp, c, ok)
		}
	}
	// Reopening removes it; an unrelated edit is nothing.
	if c, ok := Change(decode(t, doneAt("20261112T080000Z")), open, recv); !ok || c.Done || c.Occurrence != "20261110" {
		t.Errorf("reopen: %+v %v", c, ok)
	}
	if _, ok := Change(open, decode(t, strings.Replace(todoOpen, "Water", "Water plants", 1)), recv); ok {
		t.Error("an edit counted")
	}
	// A repeating task moved to its next occurrence (as Tasks.org writes it) is a completion of the
	// one it left; a manual reschedule is not.
	daily := strings.Replace(todoOpen, "STATUS:NEEDS-ACTION", "STATUS:NEEDS-ACTION\nRRULE:FREQ=DAILY", 1)
	moved := strings.Replace(daily, "DUE;VALUE=DATE:20261110", "DUE;VALUE=DATE:20261111", 1)
	if c, ok := Change(decode(t, daily), decode(t, moved), recv); !ok || !c.Done || c.Occurrence != "20261110" {
		t.Errorf("advance: %+v %v", c, ok)
	}
	far := strings.Replace(daily, "DUE;VALUE=DATE:20261110", "DUE;VALUE=DATE:20261125", 1)
	if _, ok := Change(decode(t, daily), decode(t, far), recv); ok {
		t.Error("a reschedule counted")
	}
	// A new object written completed counts.
	if c, ok := Change(nil, decode(t, doneAt("20261112T080000Z")), recv); !ok || !c.Done {
		t.Errorf("new completed: %+v %v", c, ok)
	}
}

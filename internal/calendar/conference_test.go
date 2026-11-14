package calendar

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

const room = "https://chat.example/#/room/!abc:example"

func conferenceEvent(t *testing.T, desc string) *ical.Calendar {
	t.Helper()
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	cal := NewEvent("uid-1", EventInput{Title: "Sync", Description: desc, Start: start, End: start.Add(time.Hour), Zone: time.UTC}, start)
	SetConference(cal, room)
	return cal
}

func encoded(t *testing.T, cal *ical.Calendar) string {
	t.Helper()
	var b bytes.Buffer
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSetConferenceWritesPropertyAndJoinLine(t *testing.T) {
	cal := conferenceEvent(t, "Agenda")
	ev, _ := Master(cal)
	if got := Conference(ev); got != room {
		t.Fatalf("conference %q", got)
	}
	out := encoded(t, cal)
	if !strings.Contains(out, "CONFERENCE;FEATURE=VIDEO;LABEL=KyMessages;VALUE=URI:"+room) {
		t.Fatalf("no CONFERENCE property:\n%s", out)
	}
	if d, _ := ev.Props.Text(ical.PropDescription); d != "Agenda\n\nJoin: "+room {
		t.Fatalf("description %q", d)
	}
	// Setting it again changes nothing; removing it leaves the user's text and other links.
	other := ical.NewProp(propConference)
	other.Value = "https://other.example/call"
	ev.Props.Add(other)
	SetConference(cal, room)
	if d, _ := ev.Props.Text(ical.PropDescription); d != "Agenda\n\nJoin: "+room {
		t.Fatalf("description after a second set %q", d)
	}
	SetConference(cal, "")
	if d, _ := ev.Props.Text(ical.PropDescription); d != "Agenda" || Conference(ev) != "" || len(ev.Props.Values(propConference)) != 1 {
		t.Fatalf("after removal: %q, %v", d, ev.Props.Values(propConference))
	}
}

// A web edit replaces SUMMARY and DESCRIPTION but mutates the object in place: CONFERENCE
// survives, and KeepJoin puts the Join line back on the description the form sent.
func TestWebEditKeepsTheRoom(t *testing.T) {
	cal := conferenceEvent(t, "")
	start := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if err := EditAll(cal, EventInput{Title: "Renamed", Description: "New notes", Start: start, End: start.Add(time.Hour), Zone: time.UTC}, start); err != nil {
		t.Fatal(err)
	}
	ev, _ := Master(cal)
	if Conference(ev) != room {
		t.Fatal("EditAll dropped CONFERENCE")
	}
	KeepJoin(cal)
	if d, _ := ev.Props.Text(ical.PropDescription); d != "New notes\n\nJoin: "+room {
		t.Fatalf("description %q", d)
	}
	// One occurrence's override is a copy of the master: the same room.
	repeat := EventInput{Title: "Renamed", Start: start, End: start.Add(time.Hour), Zone: time.UTC, Repeat: Repeat{Freq: "weekly"}}
	if err := EditAll(cal, repeat, start); err != nil {
		t.Fatal(err)
	}
	next := start.AddDate(0, 0, 7)
	if err := EditOne(cal, next.Format("20060102T150405Z"), EventInput{Title: "Moved", Start: next.Add(time.Hour), End: next.Add(2 * time.Hour), Zone: time.UTC}, start); err != nil {
		t.Fatal(err)
	}
	KeepJoin(cal)
	n := 0
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			n++
			if d, _ := c.Props.Text(ical.PropDescription); Conference(c) != room || !strings.HasSuffix(d, "Join: "+room) {
				t.Fatalf("component %d: conference %q description %q", n, Conference(c), d)
			}
		}
	}
	if n != 2 {
		t.Fatalf("%d VEVENTs", n)
	}
}

func TestStripJoin(t *testing.T) {
	for in, want := range map[string]string{
		"Join: " + room:                 "",
		"Notes\n\nJoin: " + room + "\n": "Notes",
		"Join: " + room + " and more":   "Join: " + room + " and more",
		"Join: https://elsewhere/":      "Join: https://elsewhere/",
	} {
		if got := StripJoin(in, room); got != want {
			t.Errorf("StripJoin(%q) = %q", in, got)
		}
	}
}

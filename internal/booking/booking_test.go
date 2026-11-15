package booking

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func everyDay(start, end string) []Window {
	var ws []Window
	for d := 0; d < 7; d++ {
		ws = append(ws, Window{Day: d, Start: start, End: end})
	}
	return ws
}

func starts(slots []Interval, loc *time.Location) []string {
	var out []string
	for _, s := range slots {
		out = append(out, s.Start.In(loc).Format("01-02 15:04"))
	}
	return out
}

func TestSlotsFollowWallClockAcrossDST(t *testing.T) {
	ny := zone(t, "America/New_York")
	r := Rules{Duration: time.Hour, HorizonDays: 3, Zone: ny, Windows: everyDay("09:00", "11:00")}
	// Spring forward: 2026-03-08 02:00 EST -> 03:00 EDT.
	now := time.Date(2026, 3, 7, 0, 0, 0, 0, ny)
	got := starts(r.Slots(now, nil, nil), ny)
	want := []string{"03-07 09:00", "03-07 10:00", "03-08 09:00", "03-08 10:00", "03-09 09:00", "03-09 10:00"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("spring slots %v, want %v", got, want)
	}
	// Each local 09:00 is a different UTC hour on either side of the change.
	s := r.Slots(now, nil, nil)
	if s[0].Start.UTC().Hour() != 14 || s[2].Start.UTC().Hour() != 13 {
		t.Fatalf("UTC hours %v %v, want 14 then 13", s[0].Start.UTC(), s[2].Start.UTC())
	}
	// Fall back: 2026-11-01 02:00 EDT -> 01:00 EST. A 00:00-04:00 window is five real hours.
	r.Windows = everyDay("00:00", "04:00")
	r.HorizonDays = 1
	now = time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	var nov1 int
	for _, x := range r.Slots(now, nil, nil) {
		if x.Start.In(ny).Day() == 1 {
			nov1++
		}
	}
	if nov1 != 5 {
		t.Fatalf("fall-back day has %d hourly slots, want 5", nov1)
	}
}

func TestSlotsInADifferentZoneThanTheVisitor(t *testing.T) {
	tokyo := zone(t, "Asia/Tokyo")
	r := Rules{Duration: 30 * time.Minute, HorizonDays: 1, Zone: tokyo, Windows: []Window{{Day: int(time.Monday), Start: "09:00", End: "10:00"}}}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) // Sunday 21:00 in Tokyo
	got := r.Slots(now, nil, nil)
	if len(got) != 2 || !got[0].Start.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("slots %v, want Monday 09:00 JST = 00:00 UTC and 09:30", got)
	}
}

func TestBuffersAndNoticeEdges(t *testing.T) {
	r := Rules{Duration: 30 * time.Minute, BufferBefore: 15 * time.Minute, BufferAfter: 10 * time.Minute,
		Notice: 4 * time.Hour, HorizonDays: 1, Zone: time.UTC, Windows: everyDay("09:00", "12:00")}
	now := time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC)
	// Notice: 09:00 is exactly now+4h and is offered; at 05:01 it is not.
	if s := r.Slots(now, nil, nil); s[0].Start.Hour() != 9 || s[0].Start.Minute() != 0 {
		t.Fatalf("first slot %v, want 09:00 at exactly the notice", s[0].Start)
	}
	if s := r.Slots(now.Add(time.Minute), nil, nil); s[0].Start.Minute() != 30 {
		t.Fatalf("first slot %v, want 09:30 one minute later", s[0].Start)
	}
	// An event 10:00-10:30: 09:30 ends 10:00 but needs 10 minutes after; 10:30 starts at its end but
	// needs 15 before. 11:00 is free; so is 09:00, whose after-buffer ends at 09:40.
	busy := []Interval{{time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 10, 30, 0, 0, time.UTC)}}
	got := strings.Join(starts(r.Slots(now, busy, nil), time.UTC), ",")
	if got != "11-16 09:00,11-16 11:00,11-16 11:30" {
		t.Fatalf("slots around an event %s", got)
	}
	// Exactly touching the buffer is free: an event ending 08:45 leaves 09:00 (15 minutes before).
	busy = []Interval{{time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 8, 45, 0, 0, time.UTC)}}
	if s := r.Slots(now, busy, nil); s[0].Start.Hour() != 9 || s[0].Start.Minute() != 0 {
		t.Fatalf("an event ending at the buffer's edge took 09:00")
	}
	busy[0].End = busy[0].End.Add(time.Minute)
	if s := r.Slots(now, busy, nil); s[0].Start.Minute() != 30 {
		t.Fatalf("an event one minute into the buffer left 09:00 offered")
	}
}

func TestHorizonCapAndGrid(t *testing.T) {
	r := Rules{Duration: time.Hour, HorizonDays: 2, DailyCap: 1, Zone: time.UTC, Windows: everyDay("09:00", "11:00")}
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	got := r.Slots(now, nil, map[string]int{"2026-11-16": 1})
	for _, s := range got {
		if s.Start.Day() == 16 {
			t.Fatalf("a full day offered %v", s.Start)
		}
		if !s.Start.Before(now.AddDate(0, 0, 2)) {
			t.Fatalf("%v is past the horizon", s.Start)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d slots, want the two on the 17th", len(got))
	}
	if _, ok := r.Offered(now, nil, nil, time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)); ok {
		t.Fatal("09:30 is off the hourly grid but was offered")
	}
	if _, ok := r.Offered(now, nil, nil, time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)); !ok {
		t.Fatal("10:00 is on the grid and free but was refused")
	}
	if _, ok := r.Offered(now, nil, nil, time.Date(2026, 11, 17, 11, 0, 0, 0, time.UTC)); ok {
		t.Fatal("11:00 would end past the window but was offered")
	}
}

func TestCheckWindows(t *testing.T) {
	for _, ws := range [][]Window{
		nil,
		{{Day: 7, Start: "09:00", End: "10:00"}},
		{{Day: 1, Start: "10:00", End: "09:00"}},
		{{Day: 1, Start: "9:00", End: "10:00"}},
		{{Day: 1, Start: "09:00", End: "24:30"}},
		{{Day: 1, Start: "09:00", End: "11:00"}, {Day: 1, Start: "10:00", End: "12:00"}},
	} {
		if err := CheckWindows(ws); !errors.Is(err, ErrInvalid) {
			t.Errorf("CheckWindows(%v) = %v, want ErrInvalid", ws, err)
		}
	}
	if err := CheckWindows([]Window{{Day: 1, Start: "09:00", End: "12:00"}, {Day: 1, Start: "13:00", End: "24:00"}}); err != nil {
		t.Fatal(err)
	}
}

func TestVisitorText(t *testing.T) {
	for _, bad := range []string{"", "  ", strings.Repeat("a", 101), "Eve\r\nATTENDEE:x", "a\"b", "zero​width", "bidi‮"} {
		if _, err := CleanName(bad); err == nil {
			t.Errorf("CleanName(%q) accepted", bad)
		}
	}
	if n, err := CleanName("  Zoë O'Brien "); err != nil || n != "Zoë O'Brien" {
		t.Fatalf("CleanName = %q, %v", n, err)
	}
	if _, err := CleanNote(strings.Repeat("x", 1001)); err == nil {
		t.Fatal("an over-long note was accepted")
	}
	if _, err := CleanNote("hi\x07"); err == nil {
		t.Fatal("a bell character was accepted")
	}
	if n, err := CleanNote("line one\r\nline\ttwo"); err != nil || n != "line one\nline\ttwo" {
		t.Fatalf("CleanNote = %q, %v", n, err)
	}
}

func TestBookedEscapesTextAndHoldCarriesNoVisitorText(t *testing.T) {
	slot := Interval{time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)}
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	cal := Booked("uid-1", "bt1", "Intro", "Eve; X", "line\nBEGIN:VEVENT,;", "https://cal.example/book/m#x", time.UTC, slot, now)
	var b strings.Builder
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "\r\nBEGIN:VEVENT,;") || !strings.Contains(out, `line\nBEGIN:VEVENT\,\;`) {
		t.Fatalf("note not escaped:\n%s", out)
	}
	if !strings.Contains(out, "SEQUENCE:1") || !strings.Contains(out, TypeProp+":bt1") {
		t.Fatalf("booked event lacks SEQUENCE:1 or its type:\n%s", out)
	}
	hold := HoldMessage("uid-1", "Intro", "Sam", "sam@example.com", "eve@example.net", "https://cal.example/book/c#y", slot, now.Add(HoldFor), now)
	b.Reset()
	if err := ical.NewEncoder(&b).Encode(hold); err != nil {
		t.Fatal(err)
	}
	out = b.String()
	for _, want := range []string{"METHOD:REQUEST", "STATUS:TENTATIVE", "ORGANIZER:mailto:sam@example.com", "mailto:eve@example.net", "SEQUENCE:0"} {
		if !strings.Contains(out, want) {
			t.Errorf("hold message lacks %q", want)
		}
	}
	if strings.Contains(out, "CN=") {
		t.Fatal("the hold message names the visitor")
	}
}

func TestBusyCountsOnlyThisTypesBookings(t *testing.T) {
	r := Rules{Duration: time.Hour, Zone: time.UTC}
	slot := Interval{time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)}
	enc := func(cal *ical.Calendar) []byte {
		var b strings.Builder
		if err := ical.NewEncoder(&b).Encode(cal); err != nil {
			t.Fatal(err)
		}
		return []byte(b.String())
	}
	mine := Booked("a", "bt1", "Intro", "Eve", "", "u", time.UTC, slot, slot.Start)
	other := Booked("b", "bt2", "Other", "Eve", "", "u", time.UTC, Interval{slot.End, slot.End.Add(time.Hour)}, slot.Start)
	cancelled := Booked("c", "bt1", "Intro", "Eve", "", "u", time.UTC, Interval{slot.End.Add(time.Hour), slot.End.Add(2 * time.Hour)}, slot.Start)
	setRaw(eventIn(cancelled), ical.PropStatus, "CANCELLED")
	busy, perDay, err := r.Busy([][]byte{enc(mine), enc(other), enc(cancelled)}, "bt1", Interval{slot.Start.Add(-time.Hour), slot.Start.Add(12 * time.Hour)}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(busy) != 2 || perDay["2026-11-17"] != 1 {
		t.Fatalf("busy %v perDay %v, want two busy and one bt1 booking", busy, perDay)
	}
}

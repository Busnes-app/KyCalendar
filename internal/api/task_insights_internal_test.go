package api

import (
	"testing"
	"time"
)

func TestHabitStats(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, ny) // Sunday, after the DST change
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, ny) }
	daily := habitStats([]time.Time{at(10, 20, 9), at(10, 21, 9), at(10, 22, 9), at(10, 23, 9), at(11, 6, 23), at(11, 7, 1), at(11, 8, 7)}, false, now)
	if daily.Current != 3 || daily.Longest != 4 || len(daily.Days) != 7 {
		t.Fatalf("daily %+v", daily)
	}
	// Weekly: weeks from Monday; the current week not yet done keeps last week's streak.
	weekly := habitStats([]time.Time{at(10, 13, 9), at(10, 21, 9), at(10, 28, 9), at(11, 4, 9)}, true, now)
	if weekly.Current != 4 || weekly.Longest != 4 || weekly.Freq != "weekly" {
		t.Fatalf("weekly %+v", weekly)
	}
	if empty := habitStats(nil, false, now); empty.Current != 0 || empty.Longest != 0 || empty.Days == nil {
		t.Fatalf("empty %+v", empty)
	}
}

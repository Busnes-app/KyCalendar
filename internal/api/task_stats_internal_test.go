package api

import (
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestCompletionStats(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	// Sunday 8 November 2026, 10:00; DST ended on 1 November.
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, ny)
	at := func(d, h int) store.TaskCompletion { return store.TaskCompletion{At: time.Date(2026, 11, d, h, 0, 0, 0, ny)} }
	got := completionStats([]store.TaskCompletion{at(1, 23), at(2, 9), at(6, 8), at(7, 22), at(8, 1), at(8, 9)}, now)
	// Week from Monday 2 Nov: 5; today 2; streak 8, 7, 6 (5 is missing) = 3.
	if got != (taskStats{DoneToday: 2, DoneThisWeek: 5, Streak: 3}) {
		t.Fatalf("%+v", got)
	}
	// Nothing yet today: the streak still counts up to yesterday.
	if got := completionStats([]store.TaskCompletion{at(6, 8), at(7, 22)}, now); got.Streak != 2 || got.DoneToday != 0 {
		t.Fatalf("%+v", got)
	}
}

package api

import (
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/booking"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// A zero-notice hold can be admitted just before its slot starts, then correctly refused
// at confirmation. Concurrency fixtures need a future margin, not a relaxed started-slot guard.
func TestBookingConfirmationAtHourBoundary(t *testing.T) {
	start := time.Date(2030, 1, 2, 11, 0, 0, 0, time.UTC)
	before := start.Add(-100 * time.Millisecond)
	h := &bookingHost{t: &store.BookingType{ID: "boundary"}, rules: booking.Rules{
		Zone: time.UTC, Duration: time.Hour, HorizonDays: 3,
		Windows: []booking.Window{{Day: int(start.Weekday()), Start: "00:00", End: "24:00"}},
	}}
	if err := h.offeredCheck(before, start)(nil, nil); err != nil {
		t.Fatalf("zero-notice slot before start: %v", err)
	}
	slot := booking.Interval{Start: start, End: start.Add(time.Hour)}
	if err := h.heldCheck(before, slot)(nil, nil); err != nil {
		t.Fatalf("confirm before start: %v", err)
	}
	for _, now := range []time.Time{start, start.Add(time.Millisecond)} {
		if err := h.heldCheck(now, slot)(nil, nil); err != errSlotTaken {
			t.Errorf("confirm at %s: got %v, want slot unavailable", now, err)
		}
	}
	// The shared integration fixture uses this notice period: its first slot is still
	// confirmable after the same boundary, without changing production's zero-notice policy.
	h.rules.Notice = time.Hour
	slots := h.rules.Slots(before, nil, nil)
	if len(slots) == 0 {
		t.Fatal("no future slots")
	}
	if err := h.heldCheck(start, slots[0])(nil, nil); err != nil {
		t.Fatalf("future slot after boundary: %v", err)
	}
}

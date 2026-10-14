package davbackend_test

import (
	"context"
	"testing"
	"time"

	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// A calendar recreated at the same path restarts its sequence; the old token must not read as a
// valid point in the new one, or the client gets a partial delta and never sees the rest.
func TestSyncTokenOfARecreatedCalendarIsInvalid(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	b := &davbackend.Backend{Store: st, User: &store.User{ID: "usr_s"}}
	path := "/dav/usr_s/calendars/work/"
	fill := func(id string, n int) {
		for i := range n {
			uid := id + string(rune('a'+i))
			raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
			if _, err := davbackend.Write(ctx, st, id, uid+".ics", decode(t, raw), []byte(raw), "", true, store.OwnerLimits{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	create := func(id string) {
		if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: id, OwnerKind: "user", OwnerID: "usr_s", Slug: "work", Name: "W"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	create("cal_old")
	fill("cal_old", 2)
	res, err := b.SyncCalendar(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().DeleteCalendar(ctx, "cal_old"); err != nil {
		t.Fatal(err)
	}
	create("cal_new")
	fill("cal_new", 4)
	if _, err := b.SyncCalendar(ctx, path, res.SyncToken); err != caldav.ErrInvalidSyncToken {
		t.Fatalf("old token on the recreated calendar: %v, want ErrInvalidSyncToken", err)
	}
}

// A recurring object is returned on index overlap alone, never expanded by caldav.Match, however
// its RRULE is spelled. The range falls between two weekly occurrences, so Match would drop it.
func TestRangedQuerySkipsMatchForAnyRuleSpelling(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := davbackend.EnsureDefault(ctx, st, "usr_q", 0); err != nil {
		t.Fatal(err)
	}
	cals, _ := st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_q")
	b := &davbackend.Backend{Store: st, User: &store.User{ID: "usr_q"}}
	home := "/dav/usr_q/calendars/default/"

	for name, rule := range map[string]string{
		"upper":  "RRULE:FREQ=WEEKLY;COUNT=3",
		"lower":  "rrule:FREQ=WEEKLY;COUNT=3",
		"folded": "RR\r\n ULE:FREQ=WEEKLY;COUNT=3",
	} {
		raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + name + "\r\nDTSTAMP:20261001T000000Z\r\n" +
			"DTSTART:20261007T090000Z\r\nDTEND:20261007T100000Z\r\n" + rule + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		if _, err := davbackend.Write(ctx, st, cals[0].ID, name+".ics", decode(t, raw), []byte(raw), "", true, store.OwnerLimits{}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	q := &caldav.CalendarQuery{CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{
		Name: "VEVENT", Start: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
	}}}}
	got, err := b.QueryCalendarObjects(ctx, home, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		var paths []string
		for _, o := range got {
			paths = append(paths, o.Path)
		}
		t.Fatalf("want all 3 recurring objects, got %v", paths)
	}
}

package davbackend_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

const event = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:w1\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nDTEND:20261007T100000Z\r\nSUMMARY:W\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

const todo = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:t1\r\nDTSTAMP:20261001T000000Z\r\nDUE:20261009T170000Z\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"

func decode(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(bytes.NewReader([]byte(s))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func TestWrite(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := davbackend.EnsureDefault(ctx, st, "usr_w", 0); err != nil {
		t.Fatal(err)
	}
	cals, _ := st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_w")
	if len(cals) != 1 || cals[0].Slug != "default" {
		t.Fatalf("default calendar: %+v", cals)
	}
	if err := davbackend.EnsureDefault(ctx, st, "usr_w", 0); err != nil {
		t.Fatal("EnsureDefault must be idempotent:", err)
	}
	id := cals[0]

	o, err := davbackend.Write(ctx, st, id, "w1.ics", decode(t, event), []byte(event), "", true, store.OwnerLimits{}, nil)
	if err != nil || o.ETag == "" {
		t.Fatalf("write: %+v %v", o, err)
	}
	if _, err := davbackend.Write(ctx, st, id, "w1.ics", decode(t, event), []byte(event), "", true, store.OwnerLimits{}, nil); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("If-None-Match on an existing object: %v", err)
	}
	if _, err := davbackend.Write(ctx, st, id, "w1.ics", decode(t, event), []byte(event), "stale", false, store.OwnerLimits{}, nil); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale If-Match: %v", err)
	}
	if _, err := davbackend.Write(ctx, st, id, "t1.ics", decode(t, todo), []byte(todo), "", true, store.OwnerLimits{}, nil); !errors.Is(err, calendar.ErrUnsupportedComponent) {
		t.Fatalf("VTODO in an events calendar: %v", err)
	}
	journal := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VJOURNAL\r\nUID:j1\r\nDTSTAMP:20261001T000000Z\r\nEND:VJOURNAL\r\nEND:VCALENDAR\r\n"
	if _, err := davbackend.Write(ctx, st, id, "j1.ics", decode(t, journal), []byte(journal), "", true, store.OwnerLimits{}, nil); !errors.Is(err, calendar.ErrUnsupportedComponent) {
		t.Fatalf("VJOURNAL: %v", err)
	}
	method := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := davbackend.Write(ctx, st, id, "m1.ics", decode(t, method), []byte(method), "", true, store.OwnerLimits{}, nil); !errors.Is(err, davbackend.ErrInvalidResource) {
		t.Fatalf("METHOD: %v", err)
	}
}

func TestWriteRefusesAnOverlargeObject(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	raw := make([]byte, calendar.MaxObjectSize+1)
	_, err = davbackend.Write(ctx, st, &store.Calendar{ID: "cal_none"}, "a.ics", decode(t, event), raw, "", true, store.OwnerLimits{}, nil)
	if !errors.Is(err, davbackend.ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

// A calendar takes only the components in its set: a task list refuses events and an events
// calendar refuses tasks; a calendar holding both takes either.
func TestWriteEnforcesComponentSet(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mk := func(id string, comps ...string) *store.Calendar {
		c := &store.Calendar{ID: id, OwnerKind: "user", OwnerID: "usr_c", Slug: id, Name: id, Components: comps}
		if err := st.Calendars().CreateCalendar(ctx, c, 0); err != nil {
			t.Fatal(err)
		}
		return c
	}
	tasks, both := mk("tasks", "VTODO"), mk("both", "VEVENT", "VTODO")
	o, err := davbackend.Write(ctx, st, tasks, "t1.ics", decode(t, todo), []byte(todo), "", true, store.OwnerLimits{}, nil)
	if err != nil || o.Component != "VTODO" {
		t.Fatalf("VTODO in a task list: %+v %v", o, err)
	}
	if _, err := davbackend.Write(ctx, st, tasks, "w1.ics", decode(t, event), []byte(event), "", true, store.OwnerLimits{}, nil); !errors.Is(err, calendar.ErrUnsupportedComponent) {
		t.Fatalf("VEVENT in a task list: %v", err)
	}
	if _, err := davbackend.Write(ctx, st, both, "t1.ics", decode(t, todo), []byte(todo), "", true, store.OwnerLimits{}, nil); err != nil {
		t.Fatalf("VTODO in a mixed calendar: %v", err)
	}
	if _, err := davbackend.Write(ctx, st, both, "w1.ics", decode(t, event), []byte(event), "", true, store.OwnerLimits{}, nil); err != nil {
		t.Fatalf("VEVENT in a mixed calendar: %v", err)
	}
}

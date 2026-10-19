package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func outboxObject(t *testing.T, st store.Store, name string) *store.CalendarObject {
	t.Helper()
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_" + name, Username: name, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	c := &store.Calendar{ID: "cal_" + name, OwnerKind: "user", OwnerID: "usr_" + name, Slug: "default", Name: "C"}
	if err := st.Calendars().CreateCalendar(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	return &store.CalendarObject{CalendarID: c.ID, Name: "m.ics", Component: "VEVENT", UID: "m", Data: []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")}
}

func plan(rows ...*store.OutboxMessage) store.Planner {
	return func([]byte) ([]byte, []*store.OutboxMessage, error) { return nil, rows, nil }
}

func row(id, sender string, size int) *store.OutboxMessage {
	return &store.OutboxMessage{ID: id, DedupeKey: id, SenderID: sender, Method: "REQUEST", Recipient: "mailto:b@example.com", Body: make([]byte, size)}
}

// Pending bodies count against the sender's bytes and the instance total; over either, the
// write and its messages are refused together.
func TestOutboxQuotaRefusesTheWholeWrite(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	o := outboxObject(t, st, "alice")
	lim := store.OwnerLimits{MaxBytes: 10000}
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, "", false, lim, plan(row("a", "usr_alice", 6000), row("b", "usr_alice", 6000))); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("over the sender's bytes: %v", err)
	}
	if _, err := st.Calendars().GetObject(ctx, o.CalendarID, o.Name); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the refused write stored its object: %v", err)
	}
	if due, _ := st.Calendars().DueOutbox(ctx, time.Now().Add(time.Hour), 10); len(due) != 0 {
		t.Fatalf("the refused write queued %d messages", len(due))
	}
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, "", false, lim, plan(row("c", "usr_alice", 4000))); err != nil {
		t.Fatalf("within the sender's bytes: %v", err)
	}
	total := store.OwnerLimits{MaxTotalBytes: 6000}
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, "", false, total, plan(row("d", "usr_alice", 3000))); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("pending bodies past the instance total: %v", err)
	}
	if err := st.Calendars().DeleteObjectPlanned(ctx, o.CalendarID, o.Name, "", lim, plan(row("e", "usr_alice", 7000))); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("a delete whose cancellations do not fit: %v", err)
	}
	if _, err := st.Calendars().GetObject(ctx, o.CalendarID, o.Name); err != nil {
		t.Fatalf("the refused delete removed the object: %v", err)
	}
}

func TestOutboxFinishClearsBodyAndPrunes(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	o := outboxObject(t, st, "alice")
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, "", false, store.OwnerLimits{}, plan(row("a", "usr_alice", 500), row("b", "usr_alice", 500))); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().FinishOutbox(ctx, "a", "delivered", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().FinishOutbox(ctx, "b", "pending", time.Now().Add(time.Minute), "conflict"); err != nil {
		t.Fatal(err)
	}
	if n, ok := store.OutboxBodyLenForTest(st, "a"); !ok || n != 0 {
		t.Errorf("a delivered message keeps %d body bytes", n)
	}
	if n, _ := store.OutboxBodyLenForTest(st, "b"); n != 500 {
		t.Errorf("a pending retry lost its body: %d", n)
	}
	if err := st.Calendars().PruneOutbox(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.OutboxBodyLenForTest(st, "a"); ok {
		t.Error("a finished message survived the prune")
	}
	if _, ok := store.OutboxBodyLenForTest(st, "b"); !ok {
		t.Error("the prune removed a pending message")
	}
}

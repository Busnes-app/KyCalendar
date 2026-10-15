package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func subscribed(t *testing.T, cs store.CalendarStore, id string) *store.Calendar {
	t.Helper()
	c := &store.Calendar{ID: id, OwnerKind: "user", OwnerID: "usr_a", Slug: "s-" + id, Name: "Work"}
	sub := &store.Subscription{URLSealed: "sealed", URLHost: "outlook.office365.com", NextCheckAt: time.Now()}
	if err := cs.CreateSubscribedCalendar(context.Background(), c, sub, 0, 0); err != nil {
		t.Fatal(err)
	}
	return c
}

func names(t *testing.T, cs store.CalendarStore, calID string) map[string]string {
	t.Helper()
	objs, err := cs.ListObjects(context.Background(), calID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, o := range objs {
		out[o.Name] = string(o.Data)
	}
	return out
}

func TestReplaceSubscriptionObjectsDiff(t *testing.T) {
	ctx := context.Background()
	cs, _ := calStore(t)
	c := subscribed(t, cs, "cal_sub")
	if got, err := cs.GetCalendarByID(ctx, c.ID); err != nil || !got.Subscribed {
		t.Fatalf("calendar reads back Subscribed=%v, %v", got != nil && got.Subscribed, err)
	}

	first := []*store.CalendarObject{obj("", "a.ics", "a", "A1", 1, i64(2)), obj("", "b.ics", "b", "B1", 1, i64(2)), obj("", "c.ics", "c", "C1", 1, nil)}
	if err := cs.ReplaceSubscriptionObjects(ctx, c.ID, first, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	after1, _ := cs.GetCalendarByID(ctx, c.ID)

	// Unchanged feed: no writes, no changes, no seq bump.
	same := []*store.CalendarObject{obj("", "a.ics", "a", "A1", 1, i64(2)), obj("", "b.ics", "b", "B1", 1, i64(2)), obj("", "c.ics", "c", "C1", 1, nil)}
	if err := cs.ReplaceSubscriptionObjects(ctx, c.ID, same, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if again, _ := cs.GetCalendarByID(ctx, c.ID); again.Seq != after1.Seq {
		t.Fatalf("an unchanged feed moved seq %d -> %d", after1.Seq, again.Seq)
	}

	// a unchanged, b moved, c gone, d new: exactly three changes.
	next := []*store.CalendarObject{obj("", "a.ics", "a", "A1", 1, i64(2)), obj("", "b.ics", "b", "B2", 5, i64(6)), obj("", "d.ics", "d", "D1", 1, i64(2))}
	if err := cs.ReplaceSubscriptionObjects(ctx, c.ID, next, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	changes, err := cs.ChangesSince(ctx, c.ID, after1.Seq)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ch := range changes {
		got[ch.Name] = ch.Deleted
	}
	if len(changes) != 3 || got["b.ics"] || !got["c.ics"] || got["d.ics"] {
		t.Fatalf("changes %+v, want b and d changed and c deleted", changes)
	}
	if n := names(t, cs, c.ID); len(n) != 3 || n["b.ics"] != "B2" || n["c.ics"] != "" || n["d.ics"] != "D1" {
		t.Fatalf("objects %v", n)
	}

	// An empty feed empties the calendar.
	if err := cs.ReplaceSubscriptionObjects(ctx, c.ID, nil, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if n := names(t, cs, c.ID); len(n) != 0 {
		t.Fatalf("objects after an empty feed: %v", n)
	}
}

func TestSubscribedCalendarRefusesEventWrites(t *testing.T) {
	ctx := context.Background()
	cs, plain := calStore(t)
	c := subscribed(t, cs, "cal_sub")
	if err := cs.ReplaceSubscriptionObjects(ctx, c.ID, []*store.CalendarObject{obj("", "a.ics", "a", "A1", 1, i64(2))}, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "new.ics", "n", "N", 1, i64(2)), "", false, store.OwnerLimits{}); !errors.Is(err, store.ErrReadOnly) {
		t.Errorf("create: %v, want ErrReadOnly", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "a", "edited", 1, i64(2)), "*", false, store.OwnerLimits{}); !errors.Is(err, store.ErrReadOnly) {
		t.Errorf("overwrite: %v, want ErrReadOnly", err)
	}
	if err := cs.DeleteObject(ctx, c.ID, "a.ics", ""); !errors.Is(err, store.ErrReadOnly) {
		t.Errorf("delete: %v, want ErrReadOnly", err)
	}
	if n := names(t, cs, c.ID); n["a.ics"] != "A1" || len(n) != 1 {
		t.Fatalf("objects changed: %v", n)
	}
	// Renaming the calendar is still the owner's to do.
	name := "Work (Contoso)"
	if err := cs.UpdateCalendar(ctx, c.ID, &name, nil, nil); err != nil {
		t.Errorf("rename: %v", err)
	}
	// A normal calendar is untouched by the guard, and refresh never writes there.
	if _, err := cs.PutObject(ctx, obj(plain.ID, "p.ics", "p", "P", 1, i64(2)), "", false, store.OwnerLimits{}); err != nil {
		t.Errorf("plain calendar: %v", err)
	}
	if err := cs.ReplaceSubscriptionObjects(ctx, plain.ID, nil, store.OwnerLimits{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("replace on a plain calendar: %v, want ErrNotFound", err)
	}
}

func TestSubscriptionLimitsAndQuota(t *testing.T) {
	ctx := context.Background()
	cs, _ := calStore(t) // usr_a already owns one calendar
	sub := func(id string, maxCals, maxSubs int) error {
		return cs.CreateSubscribedCalendar(ctx, &store.Calendar{ID: id, OwnerKind: "user", OwnerID: "usr_a", Slug: id, Name: id},
			&store.Subscription{URLSealed: "x", URLHost: "h", NextCheckAt: time.Now()}, maxCals, maxSubs)
	}
	if err := sub("s1", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := sub("s2", 0, 1); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Errorf("second subscription over a limit of 1: %v", err)
	}
	if err := sub("s3", 2, 0); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Errorf("third calendar over a limit of 2: %v", err)
	}

	// The owner's object quota counts the new feed, not the content it replaces.
	lim := store.OwnerLimits{MaxObjects: 3}
	if _, err := cs.PutObject(ctx, obj("cal_1", "p.ics", "p", "P", 1, i64(2)), "", false, lim); err != nil {
		t.Fatal(err)
	}
	two := []*store.CalendarObject{obj("", "a.ics", "a", "A", 1, i64(2)), obj("", "b.ics", "b", "B", 1, i64(2))}
	if err := cs.ReplaceSubscriptionObjects(ctx, "s1", two, lim); err != nil {
		t.Fatalf("2 + 1 within 3: %v", err)
	}
	if err := cs.ReplaceSubscriptionObjects(ctx, "s1", two, lim); err != nil {
		t.Fatalf("the same 2 again: %v", err)
	}
	three := append(two, obj("", "c.ics", "c", "C", 1, i64(2)))
	if err := cs.ReplaceSubscriptionObjects(ctx, "s1", three, lim); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("3 + 1 over 3: %v", err)
	}
	if n := names(t, cs, "s1"); len(n) != 2 {
		t.Fatalf("a refused feed changed the calendar: %v", n)
	}
	if err := cs.ReplaceSubscriptionObjects(ctx, "s1", two, store.OwnerLimits{MaxTotalBytes: 2}); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("instance byte cap: %v", err)
	}
}

func TestSubscriptionCheckAndCascade(t *testing.T) {
	ctx := context.Background()
	cs, _ := calStore(t)
	c := subscribed(t, cs, "cal_sub")
	due, err := cs.DueSubscriptions(ctx, time.Now().Add(time.Second), 10)
	if err != nil || len(due) != 1 || due[0].OwnerID != "usr_a" || due[0].RefreshedAt != nil {
		t.Fatalf("due: %v %+v", err, due)
	}
	now := time.Now().UTC().Truncate(time.Second)
	sub := due[0]
	sub.ETag, sub.Failures, sub.Error, sub.CheckedAt, sub.RefreshedAt, sub.NextCheckAt = `"v2"`, 0, "", &now, &now, now.Add(time.Hour)
	if err := cs.RecordSubscriptionCheck(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if due, _ := cs.DueSubscriptions(ctx, now.Add(time.Minute), 10); len(due) != 0 {
		t.Fatalf("not due for an hour, listed: %+v", due)
	}
	got, err := cs.GetSubscription(ctx, c.ID)
	if err != nil || got.ETag != `"v2"` || got.RefreshedAt == nil || !got.RefreshedAt.Equal(now) {
		t.Fatalf("read back %+v %v", got, err)
	}
	if err := cs.SetSubscriptionSource(ctx, c.ID, "sealed2", "calendar.google.com"); err != nil {
		t.Fatal(err)
	}
	if got, _ := cs.GetSubscription(ctx, c.ID); got.ETag != "" || got.URLHost != "calendar.google.com" || got.URLSealed != "sealed2" {
		t.Fatalf("new source kept the old validators: %+v", got)
	}
	if err := cs.DeleteCalendar(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetSubscription(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("subscription outlived its calendar: %v", err)
	}
}

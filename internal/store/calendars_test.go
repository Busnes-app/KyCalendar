package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func calStore(t *testing.T) (store.CalendarStore, *store.Calendar) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &store.Calendar{ID: "cal_1", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Calendar"}
	if err := st.Calendars().CreateCalendar(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return st.Calendars(), c
}

func obj(cal, name, uid, data string, first int64, last *int64) *store.CalendarObject {
	return &store.CalendarObject{CalendarID: cal, Name: name, UID: uid, Data: []byte(data), FirstStart: first, LastEnd: last}
}

func i64(v int64) *int64 { return &v }

func TestCreateCalendarSlugUnique(t *testing.T) {
	cs, _ := calStore(t)
	dup := &store.Calendar{ID: "cal_2", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "x"}
	if err := cs.CreateCalendar(context.Background(), dup); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
}

func TestPutObjectSeqAndChanges(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	o := obj(c.ID, "a.ics", "u1", "v1", 100, i64(200))
	created, err := cs.PutObject(ctx, o, "", false)
	if err != nil || !created || o.ETag == "" {
		t.Fatalf("put: created=%v etag=%q err=%v", created, o.ETag, err)
	}
	first := o.ETag
	o2 := obj(c.ID, "a.ics", "u1", "v2", 100, i64(200))
	if created, err := cs.PutObject(ctx, o2, first, false); err != nil || created {
		t.Fatalf("update: created=%v err=%v", created, err)
	}
	if err := cs.DeleteObject(ctx, c.ID, "a.ics", ""); err != nil {
		t.Fatal(err)
	}
	changes, err := cs.ChangesSince(ctx, c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].Seq != 1 || changes[2].Seq != 3 || !changes[2].Deleted || changes[1].Deleted {
		t.Fatalf("changes %+v", changes)
	}
	got, _ := cs.GetCalendarBySlug(ctx, "user", "usr_a", "default")
	if got.Seq != 3 {
		t.Fatalf("seq %d", got.Seq)
	}
}

func TestPutObjectPreconditions(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	o := obj(c.ID, "a.ics", "u1", "v1", 100, i64(200))
	if _, err := cs.PutObject(ctx, o, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v2", 100, i64(200)), "", true); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("If-None-Match on existing: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v2", 100, i64(200)), "stale", false); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale If-Match: %v", err)
	}
	if err := cs.DeleteObject(ctx, c.ID, "a.ics", "stale"); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale delete: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "b.ics", "u1", "v1", 100, i64(200)), "", false); !errors.Is(err, store.ErrUIDConflict) {
		t.Fatalf("UID reuse: %v", err)
	}
	got, _ := cs.GetObject(ctx, c.ID, "a.ics")
	if string(got.Data) != "v1" {
		t.Fatalf("failed writes changed data: %q", got.Data)
	}
	cal, _ := cs.GetCalendarBySlug(ctx, "user", "usr_a", "default")
	if cal.Seq != 1 {
		t.Fatalf("failed writes advanced seq to %d", cal.Seq)
	}
}

func TestListObjectsInRange(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	cs.PutObject(ctx, obj(c.ID, "past.ics", "p", "x", 10, i64(20)), "", false)
	cs.PutObject(ctx, obj(c.ID, "in.ics", "i", "x", 100, i64(200)), "", false)
	cs.PutObject(ctx, obj(c.ID, "forever.ics", "f", "x", 5, nil), "", false)
	cs.PutObject(ctx, obj(c.ID, "future.ics", "u", "x", 1000, i64(2000)), "", false)
	got, err := cs.ListObjectsInRange(ctx, c.ID, 150, 500)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, o := range got {
		names[o.Name] = true
	}
	if len(got) != 2 || !names["in.ics"] || !names["forever.ics"] {
		t.Fatalf("got %v", names)
	}
}

func TestChangesSinceExpired(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v1", 1, i64(2)), "", false)
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "v1", 1, i64(2)), "", false)
	if err := cs.PruneChanges(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ChangesSince(ctx, c.ID, 1); !errors.Is(err, store.ErrSyncTokenExpired) {
		t.Fatalf("token older than pruned rows: %v", err)
	}
	if ch, err := cs.ChangesSince(ctx, c.ID, 2); err != nil || len(ch) != 0 {
		t.Fatalf("current token: %v %v", ch, err)
	}
}

func TestSyncEpochStable(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := st.Calendars().SyncEpoch(ctx)
	b, _ := st.Calendars().SyncEpoch(ctx)
	if err != nil || a == "" || a != b {
		t.Fatalf("epoch %q %q %v", a, b, err)
	}
}

func TestCountObjectsByOwner(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "x", 1, i64(2)), "", false)
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "x", 1, i64(2)), "", false)
	if n, err := cs.CountObjectsByOwner(ctx, "user", "usr_a"); err != nil || n != 2 {
		t.Fatalf("count %d %v", n, err)
	}
}

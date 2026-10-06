package store_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
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
	if err := st.Calendars().CreateCalendar(context.Background(), c, 0); err != nil {
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
	if err := cs.CreateCalendar(context.Background(), dup, 0); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
}

func TestPutObjectSeqAndChanges(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	o := obj(c.ID, "a.ics", "u1", "v1", 100, i64(200))
	created, err := cs.PutObject(ctx, o, "", false, store.OwnerLimits{})
	if err != nil || !created || o.ETag == "" {
		t.Fatalf("put: created=%v etag=%q err=%v", created, o.ETag, err)
	}
	first := o.ETag
	o2 := obj(c.ID, "a.ics", "u1", "v2", 100, i64(200))
	if created, err := cs.PutObject(ctx, o2, first, false, store.OwnerLimits{}); err != nil || created {
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
	if _, err := cs.PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v2", 100, i64(200)), "", true, store.OwnerLimits{}); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("If-None-Match on existing: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v2", 100, i64(200)), "stale", false, store.OwnerLimits{}); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale If-Match: %v", err)
	}
	if err := cs.DeleteObject(ctx, c.ID, "a.ics", "stale"); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale delete: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "b.ics", "u1", "v1", 100, i64(200)), "", false, store.OwnerLimits{}); !errors.Is(err, store.ErrUIDConflict) {
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
	cs.PutObject(ctx, obj(c.ID, "past.ics", "p", "x", 10, i64(20)), "", false, store.OwnerLimits{})
	cs.PutObject(ctx, obj(c.ID, "in.ics", "i", "x", 100, i64(200)), "", false, store.OwnerLimits{})
	cs.PutObject(ctx, obj(c.ID, "forever.ics", "f", "x", 5, nil), "", false, store.OwnerLimits{})
	cs.PutObject(ctx, obj(c.ID, "future.ics", "u", "x", 1000, i64(2000)), "", false, store.OwnerLimits{})
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
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v1", 1, i64(2)), "", false, store.OwnerLimits{})
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "v1", 1, i64(2)), "", false, store.OwnerLimits{})
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
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "x", 1, i64(2)), "", false, store.OwnerLimits{})
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "x", 1, i64(2)), "", false, store.OwnerLimits{})
	if n, err := cs.CountObjectsByOwner(ctx, "user", "usr_a"); err != nil || n != 2 {
		t.Fatalf("count %d %v", n, err)
	}
}

func TestChangesSincePartialPrune(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v1", 1, i64(2)), "", false, store.OwnerLimits{})
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "v1", 1, i64(2)), "", false, store.OwnerLimits{})
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(20 * time.Millisecond)
	cs.PutObject(ctx, obj(c.ID, "c.ics", "u3", "v1", 1, i64(2)), "", false, store.OwnerLimits{})
	if err := cs.PruneChanges(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ChangesSince(ctx, c.ID, 1); !errors.Is(err, store.ErrSyncTokenExpired) {
		t.Fatalf("token 1: %v", err)
	}
	ch, err := cs.ChangesSince(ctx, c.ID, 2)
	if err != nil || len(ch) != 1 || ch[0].Seq != 3 || ch[0].Name != "c.ics" {
		t.Fatalf("token 2: %+v %v", ch, err)
	}
}

func TestConcurrentPuts(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t.Logf("driver=%s", st.Driver())
	cs := st.Calendars()
	c := &store.Calendar{ID: "cal_1", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Calendar"}
	if err := cs.CreateCalendar(ctx, c, 0); err != nil {
		t.Fatal(err)
	}

	run := func(n int, f func(i int) error) []error {
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = f(i)
			}()
		}
		close(start)
		wg.Wait()
		return errs
	}
	count := func(errs []error, want error) (ok, match int) {
		for _, e := range errs {
			if e == nil {
				ok++
			} else if errors.Is(e, want) {
				match++
			} else {
				t.Errorf("unexpected error: %v", e)
			}
		}
		return
	}

	errs := run(20, func(int) error {
		_, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v", 1, i64(2)), "", true, store.OwnerLimits{})
		return err
	})
	if ok, pf := count(errs, store.ErrPreconditionFailed); ok != 1 || pf != 19 {
		t.Fatalf("If-None-Match: %d ok, %d precondition", ok, pf)
	}
	cal, _ := cs.GetCalendarBySlug(ctx, "user", "usr_a", "default")
	if cal.Seq != 1 {
		t.Fatalf("seq %d, want 1", cal.Seq)
	}

	names := []string{"x.ics", "y.ics"}
	errs = run(2, func(i int) error {
		_, err := cs.PutObject(ctx, obj(c.ID, names[i], "same", "v", 1, i64(2)), "", false, store.OwnerLimits{})
		return err
	})
	if ok, uc := count(errs, store.ErrUIDConflict); ok != 1 || uc != 1 {
		t.Fatalf("same UID: %d ok, %d conflict", ok, uc)
	}
	cal, _ = cs.GetCalendarBySlug(ctx, "user", "usr_a", "default")
	if cal.Seq != 2 {
		t.Fatalf("seq %d, want 2", cal.Seq)
	}
}

func TestSumObjectBytesByOwner(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	if n, err := cs.SumObjectBytesByOwner(ctx, "user", "usr_a"); err != nil || n != 0 {
		t.Fatalf("empty sum %d %v", n, err)
	}
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "héllo", 1, i64(2)), "", false, store.OwnerLimits{}) // 6 bytes, 5 characters
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "xyz", 1, i64(2)), "", false, store.OwnerLimits{})
	if n, err := cs.SumObjectBytesByOwner(ctx, "user", "usr_a"); err != nil || n != 9 {
		t.Fatalf("sum %d %v", n, err)
	}
	if n, err := cs.SumObjectBytesByOwner(ctx, "user", "usr_other"); err != nil || n != 0 {
		t.Fatalf("other owner %d %v", n, err)
	}
}

func TestPutObjectOwnerLimits(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	lim := store.OwnerLimits{MaxObjects: 2, MaxBytes: 10}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "aaaa", 1, i64(2)), "", false, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "aaaaaaaaaa", 1, i64(2)), "", false, lim); err != nil {
		t.Fatalf("replacement counts only its new size: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "b", 1, i64(2)), "", false, lim); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("bytes over cap: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "aaaa", 1, i64(2)), "", false, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "b", 1, i64(2)), "", false, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "c.ics", "u3", "c", 1, i64(2)), "", false, lim); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("objects over cap: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "bb", 1, i64(2)), "", false, lim); err != nil {
		t.Fatalf("update at the object cap: %v", err)
	}
	cal, _ := cs.GetCalendarBySlug(ctx, "user", "usr_a", "default")
	if cal.Seq != 5 {
		t.Fatalf("refused writes must not bump seq: %d", cal.Seq)
	}
	if err := cs.CreateCalendar(ctx, &store.Calendar{ID: "cal_2", OwnerKind: "user", OwnerID: "usr_a", Slug: "two", Name: "x"}, 1); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("calendar cap: %v", err)
	}
}

func concurrently(n int, f func(i int) error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = f(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

func tally(t *testing.T, errs []error) (ok, quota int) {
	t.Helper()
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, store.ErrQuotaExceeded):
			quota++
		default:
			t.Errorf("unexpected error: %v", e)
		}
	}
	return ok, quota
}

// Quotas span calendars, so a per-calendar row lock alone would let concurrent writers overshoot.
func TestConcurrentQuota(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t.Logf("driver=%s", st.Driver())
	cs := st.Calendars()
	var cals []string
	for _, owner := range []string{"usr_a", "usr_b"} {
		for i := 0; i < 4; i++ {
			id := "cal_" + owner + strconv.Itoa(i)
			if err := cs.CreateCalendar(ctx, &store.Calendar{ID: id, OwnerKind: "user", OwnerID: owner, Slug: "s" + strconv.Itoa(i), Name: "x"}, 0); err != nil {
				t.Fatal(err)
			}
			cals = append(cals, id)
		}
	}
	put := func(cal string, i int, lim store.OwnerLimits) error {
		n := strconv.Itoa(i)
		_, err := cs.PutObject(ctx, obj(cal, "o"+n+".ics", "u"+n, "12345", 1, i64(2)), "", false, lim)
		return err
	}
	errs := concurrently(20, func(i int) error { return put(cals[i%4], i, store.OwnerLimits{MaxObjects: 5}) })
	if ok, q := tally(t, errs); ok != 5 || q != 15 {
		t.Fatalf("objects: %d ok, %d quota", ok, q)
	}
	errs = concurrently(20, func(i int) error { return put(cals[4+i%4], i, store.OwnerLimits{MaxBytes: 25}) })
	if ok, q := tally(t, errs); ok != 5 || q != 15 {
		t.Fatalf("bytes: %d ok, %d quota", ok, q)
	}
	errs = concurrently(10, func(i int) error {
		n := strconv.Itoa(i)
		return cs.CreateCalendar(ctx, &store.Calendar{ID: "cal_c" + n, OwnerKind: "user", OwnerID: "usr_c", Slug: "s" + n, Name: "x"}, 3)
	})
	if ok, q := tally(t, errs); ok != 3 || q != 7 {
		t.Fatalf("calendars: %d ok, %d quota", ok, q)
	}
}

func TestIfMatchWildcard(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v1", 1, i64(2)), "*", false, store.OwnerLimits{}); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("* on missing: %v", err)
	}
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v1", 1, i64(2)), "", false, store.OwnerLimits{})
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v2", 1, i64(2)), "*", false, store.OwnerLimits{}); err != nil {
		t.Fatalf("* on existing PUT: %v", err)
	}
	if err := cs.DeleteObject(ctx, c.ID, "a.ics", "*"); err != nil {
		t.Fatalf("* on existing DELETE: %v", err)
	}
}

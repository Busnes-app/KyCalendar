package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// A calendar migrated to PostgreSQL keeps what clients hold: the CTag parts (seq and the
// sync epoch), every ETag, min_sync_seq, the answer to an issued sync token, an expired
// token's refusal, and the task index (spec AC24).
func TestCalendarContinuityAcrossMigration(t *testing.T) {
	onPostgres(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kycalendar.db")
	src, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	cal := &store.Calendar{ID: "cal_1", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Calendar", Components: []string{"VEVENT", "VTODO"}}
	if err := src.Calendars().CreateCalendar(ctx, cal, 0); err != nil {
		t.Fatal(err)
	}
	put := func(name, uid, data string) {
		o := &store.CalendarObject{CalendarID: cal.ID, Name: name, UID: uid, Component: "VEVENT", Data: []byte(data), FirstStart: 100}
		if _, err := src.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	put("a.ics", "a", "A")
	put("b.ics", "b", "B")
	if err := src.Calendars().PruneChanges(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	put("c.ics", "c", "C")
	if err := src.Calendars().DeleteObject(ctx, cal.ID, "b.ics", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Tasks().IndexTasks(ctx, cal.ID, []store.TaskDoc{{UID: "t", ETag: "e", Body: "café crème"}}); err != nil {
		t.Fatal(err)
	}
	type view struct {
		seq     int64
		epoch   string
		etags   []string
		changes []store.CalendarChange
	}
	read := func(st store.Store) view {
		t.Helper()
		c, err := st.Calendars().GetCalendarByID(ctx, cal.ID)
		if err != nil {
			t.Fatal(err)
		}
		epoch, err := st.Calendars().SyncEpoch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		objs, err := st.Calendars().ListObjects(ctx, cal.ID)
		if err != nil {
			t.Fatal(err)
		}
		var etags []string
		for _, o := range objs {
			etags = append(etags, o.Name+"="+o.ETag)
		}
		slices.Sort(etags)
		changes, err := st.Calendars().ChangesSince(ctx, cal.ID, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Calendars().ChangesSince(ctx, cal.ID, 1); !errors.Is(err, store.ErrSyncTokenExpired) {
			t.Fatalf("a token below min_sync_seq: %v, want ErrSyncTokenExpired", err)
		}
		found, err := st.Tasks().SearchTasks(ctx, []string{cal.ID}, []string{"cafe"})
		if err != nil || !found[cal.ID+"/t"] {
			t.Fatalf("search did not find the task: %v %v", found, err)
		}
		return view{c.Seq, epoch, etags, changes}
	}
	before := read(src)
	src.Close()

	target := testdb.Config(t)
	if _, err := store.MigrateFromSQLite(ctx, path, target.DSN, false); err != nil {
		t.Fatal(err)
	}
	pg, err := store.Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	after := read(pg)
	if before.seq != after.seq || before.epoch != after.epoch || !slices.Equal(before.etags, after.etags) || !slices.Equal(before.changes, after.changes) {
		t.Fatalf("calendar state changed in migration:\nbefore %+v\nafter  %+v", before, after)
	}
	if len(before.changes) != 2 {
		t.Fatalf("the issued token should see two changes, saw %+v", before.changes)
	}
}

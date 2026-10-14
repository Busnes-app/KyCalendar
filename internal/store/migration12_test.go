package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func rawDB(t *testing.T, cfg config.DatabaseConfig) *sql.DB {
	t.Helper()
	db, err := sql.Open(map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver], cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// usage returns the maintained counter and the real total; they must agree.
func usage(t *testing.T, db *sql.DB) (counter, actual int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT bytes FROM calendar_usage WHERE id = 1`).Scan(&counter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(LENGTH(data)), 0) FROM calendar_objects`).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	return counter, actual
}

// The instance byte counter follows inserts, replacements (upserts), deletes and cascades.
func TestCalendarUsageTracksEveryWrite(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db := rawDB(t, cfg)
	cs := st.Calendars()
	for _, id := range []string{"cal_a", "cal_b"} {
		if err := cs.CreateCalendar(ctx, &store.Calendar{ID: id, OwnerKind: "user", OwnerID: "usr_" + id, Slug: "default", Name: "C"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	check := func(step string, want int64) {
		t.Helper()
		if counter, actual := usage(t, db); counter != want || actual != want {
			t.Fatalf("%s: counter %d, actual %d, want %d", step, counter, actual, want)
		}
	}
	lim := store.OwnerLimits{MaxTotalBytes: 1 << 20}
	cs.PutObject(ctx, obj("cal_a", "1.ics", "u1", "aaaa", 1, i64(2)), "", false, lim)
	cs.PutObject(ctx, obj("cal_b", "2.ics", "u2", "bbbbbb", 1, i64(2)), "", false, lim)
	check("insert", 10)
	cs.PutObject(ctx, obj("cal_a", "1.ics", "u1", "a", 1, i64(2)), "", false, lim)
	check("replace smaller", 7)
	cs.PutObject(ctx, obj("cal_a", "1.ics", "u1", "aaaaaaaa", 1, i64(2)), "", false, store.OwnerLimits{})
	check("replace without a cap", 14)
	if err := cs.DeleteObject(ctx, "cal_b", "2.ics", ""); err != nil {
		t.Fatal(err)
	}
	check("delete", 8)
	cs.PutObject(ctx, obj("cal_b", "3.ics", "u3", "ccc", 1, i64(2)), "", false, lim)
	if err := cs.DeleteCalendar(ctx, "cal_a"); err != nil {
		t.Fatal(err)
	}
	check("calendar cascade", 3)
	// A refused write leaves the counter alone.
	if _, err := cs.PutObject(ctx, obj("cal_b", "4.ics", "u4", "dddd", 1, i64(2)), "", false, store.OwnerLimits{MaxTotalBytes: 6}); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("over the instance cap: %v", err)
	}
	check("refused", 3)
}

// Migration 12 backfills the counter from objects stored before it.
func TestMigration12BackfillsUsage(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cs := st.Calendars()
	if err := cs.CreateCalendar(ctx, &store.Calendar{ID: "cal_m", OwnerKind: "user", OwnerID: "usr_m", Slug: "default", Name: "C"}, 0); err != nil {
		t.Fatal(err)
	}
	cs.PutObject(ctx, obj("cal_m", "1.ics", "u1", "12345", 1, i64(2)), "", false, store.OwnerLimits{})
	cs.PutObject(ctx, obj("cal_m", "2.ics", "u2", "123", 1, i64(2)), "", false, store.OwnerLimits{})
	_ = st.Close()

	db := rawDB(t, cfg)
	drop := []string{"DROP TRIGGER calendar_usage_insert", "DROP TRIGGER calendar_usage_update", "DROP TRIGGER calendar_usage_delete"}
	if cfg.Driver == "postgres" {
		drop = []string{"DROP TRIGGER calendar_usage_track ON calendar_objects", "DROP FUNCTION calendar_usage_track()"}
	}
	for _, q := range append(drop, "DROP TABLE calendar_usage", "DELETE FROM schema_migrations WHERE version = 12") {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if counter, actual := usage(t, db); counter != 8 || actual != 8 {
		t.Fatalf("counter %d, actual %d, want 8", counter, actual)
	}
}

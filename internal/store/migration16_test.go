package store_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 16 adds component sets. Every existing calendar and object is VEVENT.
func TestMigration16ExistingCalendarsHoldEvents(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cs := st.Calendars()
	if err := cs.CreateCalendar(ctx, &store.Calendar{ID: "cal_old", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Calendar"}, 0); err != nil {
		t.Fatal(err)
	}
	last := int64(200)
	if _, err := cs.PutObject(ctx, &store.CalendarObject{CalendarID: "cal_old", Name: "a.ics", Component: "VEVENT", UID: "u", Data: []byte("x"), FirstStart: 100, LastEnd: &last}, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Pretend the database predates migration 16, then reopen to run it.
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE calendars DROP COLUMN components", "ALTER TABLE calendar_objects DROP COLUMN component", "DELETE FROM schema_migrations WHERE version = 16"} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = raw.Close()
	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if c, err := st.Calendars().GetCalendarByID(ctx, "cal_old"); err != nil || strings.Join(c.Components, ",") != "VEVENT" {
		t.Fatalf("existing calendar after migration: %+v %v, want VEVENT", c, err)
	}
	if got, err := st.Calendars().ListObjectsInRange(ctx, "cal_old", "VEVENT", 150, 500); err != nil || len(got) != 1 {
		t.Fatalf("existing event after migration: %d objects, %v", len(got), err)
	}
}

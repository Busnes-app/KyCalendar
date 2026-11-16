package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 31 gives each logged completion an occurrence under a unique index. Rows from
// before it may share user, task and second (a repeating task completed twice in a second);
// the upgrade keeps both instead of failing (security review, PR 163).
func TestMigration31KeepsSameSecondCompletions(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_a", Username: "a", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_t", OwnerKind: "user", OwnerID: "usr_a", Slug: "t", Name: "T", Components: []string{"VTODO"}}, 0); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// The log as migration 30 left it, with two completions in the same second.
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC).Unix()
	for _, q := range []string{
		"DROP TABLE task_goals",
		"DROP TABLE task_completions",
		"CREATE TABLE task_completions (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE, uid TEXT NOT NULL, completed_at BIGINT NOT NULL)",
		"CREATE INDEX task_completions_user ON task_completions(user_id, completed_at)",
		"DELETE FROM schema_migrations WHERE version = 31",
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for range 2 {
		if _, err := raw.ExecContext(ctx, rebind(cfg.Driver, "INSERT INTO task_completions (user_id, calendar_id, uid, completed_at) VALUES (?, ?, ?, ?)"), "usr_a", "cal_t", "t1", at); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()

	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	got, err := st.Tasks().Completions(ctx, "usr_a", time.Time{})
	if err != nil || len(got) != 2 || got[0].Occurrence == got[1].Occurrence {
		t.Fatalf("after upgrade: %+v %v", got, err)
	}
	// The unique index works for new rows.
	for range 2 {
		if err := st.Tasks().LogCompletion(ctx, store.TaskCompletion{UserID: "usr_a", CalendarID: "cal_t", UID: "t1", Occurrence: "20261111", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := st.Tasks().Completions(ctx, "usr_a", time.Time{}); len(got) != 3 {
		t.Fatalf("a repeated occurrence was logged twice: %d rows", len(got))
	}
}

func rebind(driver, q string) string {
	if driver != "postgres" {
		return q
	}
	out, n := []byte{}, 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			out = append(out, []byte("$"+string(rune('0'+n)))...)
			continue
		}
		out = append(out, q[i])
	}
	return string(out)
}

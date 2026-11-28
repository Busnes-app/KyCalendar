package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func taskStore(t *testing.T) (store.Store, *store.Calendar) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, id := range []string{"usr_a", "usr_b"} {
		if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: id, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	c := &store.Calendar{ID: "cal_t", OwnerKind: "user", OwnerID: "usr_a", Slug: "tasks", Name: "Tasks", Components: []string{"VTODO"}}
	if err := st.Calendars().CreateCalendar(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	return st, c
}

func TestTaskViewsAreOwnedAndCapped(t *testing.T) {
	ctx := context.Background()
	st, _ := taskStore(t)
	ts := st.Tasks()
	for i, id := range []string{"v1", "v2"} {
		if err := ts.CreateView(ctx, &store.TaskView{ID: id, UserID: "usr_a", Name: id, Query: "{}", CreatedAt: time.Unix(int64(i), 0)}, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := ts.CreateView(ctx, &store.TaskView{ID: "v3", UserID: "usr_a", Name: "x", Query: "{}"}, 2); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("third view: %v", err)
	}
	if err := ts.UpdateView(ctx, "usr_b", "v1", "stolen", "{}"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another user's update: %v", err)
	}
	if err := ts.DeleteView(ctx, "usr_b", "v1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another user's delete: %v", err)
	}
	if err := ts.UpdateView(ctx, "usr_a", "v1", "Renamed", `{"q":"x"}`); err != nil {
		t.Fatal(err)
	}
	vs, err := ts.Views(ctx, "usr_a")
	if err != nil || len(vs) != 2 || vs[0].Name != "Renamed" {
		t.Fatalf("views %+v %v", vs, err)
	}
}

func TestTaskCompletionsLogAndUndo(t *testing.T) {
	ctx := context.Background()
	st, c := taskStore(t)
	ts := st.Tasks()
	day := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for i, h := range []int{-30, 9, 10, 10} {
		occ := []string{"a", "b", "c", "c"}[i] // the last repeats an occurrence: ignored
		if err := ts.LogCompletion(ctx, store.TaskCompletion{UserID: "usr_a", CalendarID: c.ID, UID: "t1", Occurrence: occ, At: day.Add(time.Duration(h) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ts.UndoCompletion(ctx, "usr_a", c.ID, "t1", "c"); err != nil {
		t.Fatal(err)
	}
	got, err := ts.Completions(ctx, "usr_a", day.AddDate(0, 0, -7))
	if err != nil || len(got) != 2 || got[1].At.Hour() != 9 {
		t.Fatalf("after undo %+v %v", got, err)
	}
	if err := ts.PruneTasks(ctx, day); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.Completions(ctx, "usr_a", time.Time{}); len(got) != 1 {
		t.Fatalf("after prune %+v", got)
	}
}

func TestTaskSearchIndex(t *testing.T) {
	ctx := context.Background()
	st, c := taskStore(t)
	ts := st.Tasks()
	docs := []store.TaskDoc{{UID: "a", ETag: "1", Body: "Compress the hero images"}, {UID: "b", ETag: "1", Body: "Café receipts 50%_off"}}
	if _, err := ts.IndexTasks(ctx, c.ID, docs); err != nil {
		t.Fatal(err)
	}
	for words, want := range map[string]string{"hero": "a", "comp ima": "a", "cafe": "b", "50%_": "b"} {
		got, err := ts.SearchTasks(ctx, []string{c.ID}, splitWords(words))
		if err != nil || len(got) != 1 || !got[c.ID+"/"+want] {
			t.Errorf("%q: %v %v", words, got, err)
		}
	}
	if got, _ := ts.SearchTasks(ctx, []string{"cal_other"}, []string{"hero"}); len(got) != 0 {
		t.Fatalf("searched outside the given calendars: %v", got)
	}
	if got, _ := ts.SearchTasks(ctx, []string{c.ID}, []string{`"`}); len(got) != 0 {
		t.Fatalf("a quote matched: %v", got)
	}
	// A changed ETag replaces the row; a missing doc deletes it.
	if _, err := ts.IndexTasks(ctx, c.ID, []store.TaskDoc{{UID: "a", ETag: "2", Body: "Resize banners"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.SearchTasks(ctx, []string{c.ID}, []string{"hero"}); len(got) != 0 {
		t.Fatalf("stale row: %v", got)
	}
	if got, _ := ts.SearchTasks(ctx, []string{c.ID}, []string{"cafe"}); len(got) != 0 {
		t.Fatalf("deleted row: %v", got)
	}
	if err := st.Calendars().DeleteCalendar(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := ts.PruneTasks(ctx, time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func splitWords(s string) []string { return strings.Fields(s) }

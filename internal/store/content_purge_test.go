package store_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// An encrypted account's task terms leave every byte of the SQLite database once setup and the
// sweep are through: the file, its WAL and a VACUUM INTO snapshot. Another account's index
// keeps working. Search text is indexed folded, so the bytes are compared lowercased.
func TestEncryptedAccountsTaskTermsLeaveTheSQLiteFiles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kycalendar.db")
	open := func() store.Store {
		t.Helper()
		st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: path})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	st := open()
	exec := func(q string, args ...any) {
		t.Helper()
		db, _ := store.DBForTest(st)
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	a := newContentFixture(t, st, "usr_pa", false)
	b := newContentFixture(t, st, "usr_pb", false)
	putTodo := func(f contentFixture, uid, title, notes, tag string) {
		t.Helper()
		body := fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VTODO\r\nUID:%s\r\nDTSTAMP:20261127T000000Z\r\nSUMMARY:%s\r\nDESCRIPTION:%s\r\nCATEGORIES:%s\r\nEND:VTODO\r\nEND:VCALENDAR\r\n", uid, title, notes, tag)
		o := &store.CalendarObject{CalendarID: f.cal.ID, Name: uid + ".ics", Component: "VTODO", UID: uid, Data: []byte(body), FirstStart: 1}
		if _, err := st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	putTodo(a, "ta1", "Zanzibarflux", "Quorvexnote", "Plumtrellis")
	putTodo(b, "tb1", "Brightkeep", "Ordinary", "Errand")
	docA := []store.TaskDoc{{UID: "ta1", ETag: "e1", Body: "Zanzibarflux\nQuorvexnote\nPlumtrellis"}}
	docB := []store.TaskDoc{{UID: "tb1", ETag: "e1", Body: "Brightkeep\nOrdinary\nErrand"}}

	// An install from before migration 44: a task indexed, then deleted with a tombstone only.
	exec(`INSERT INTO task_search(task_search, rank) VALUES ('secure-delete', 0)`)
	for _, docs := range [][]store.TaskDoc{append(docA, store.TaskDoc{UID: "ta0", ETag: "e0", Body: "Vintrespook"}), docA} {
		if _, err := st.Tasks().IndexTasks(ctx, a.cal.ID, docs); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Tasks().IndexTasks(ctx, b.cal.ID, docB); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM schema_migrations WHERE version = 44`)
	read := func(p string) []byte {
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return bytes.ToLower(data)
	}
	snapshot := func() []byte {
		t.Helper()
		p := filepath.Join(t.TempDir(), "snap.db")
		exec(`VACUUM INTO ?`, p)
		return read(p)
	}
	if !bytes.Contains(snapshot(), []byte("vintrespook")) {
		t.Fatal("fixture: the deleted task's terms are not in the index to begin with")
	}
	_ = st.Close()
	st = open() // migration 44 runs again
	a.st, b.st = st, st
	if bytes.Contains(snapshot(), []byte("vintrespook")) {
		t.Fatal("the upgrade left a deleted task's terms in the index")
	}

	setUp(t, a)
	if n := sweepAll(t, st); n == 0 {
		t.Fatal("the sweep sealed nothing")
	}
	if err := st.Content().PurgeFreed(ctx); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"database": read(path), "wal": read(path + "-wal"), "snapshot": snapshot()}
	for name, data := range files {
		for _, term := range []string{"zanzibarflux", "quorvexnote", "plumtrellis", "vintrespook"} {
			if bytes.Contains(data, []byte(term)) {
				t.Errorf("%s still holds %q", name, term)
			}
		}
	}
	if !bytes.Contains(files["snapshot"], []byte("brightkeep")) {
		t.Error("the other account's index is missing from the snapshot")
	}
	got, err := st.Tasks().SearchTasks(ctx, []string{b.cal.ID}, []string{"bright"})
	if err != nil || !got[b.cal.ID+"/tb1"] {
		t.Fatalf("the other account's task is no longer found: %v %v", got, err)
	}
}

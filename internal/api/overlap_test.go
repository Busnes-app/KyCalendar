package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/kypost"
)

// eventIDs is a KyPost that records the event ID of every signed send.
type eventIDs struct {
	mu  sync.Mutex
	ids []string
}

func (f *eventIDs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h := syncauth.FromRequest(r)
	if _, err := syncauth.Verify(kypostSecret, h, body, syncauth.Options{}); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var req kypost.SendRequest
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.ids = append(f.ids, h.EventID)
	f.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// Single-owner scheduling is not exactly-once, so two servers delivering the same due rows
// at once (no election) must still give one internal copy per recipient, one sponsorship
// charge, external sends that all carry the outbox dedupe key as their event ID, and every row
// finished (spec AC12).
func TestRacingDeliveriesAreIdempotent(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	secretFile := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(secretFile, append(kypostSecret, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", secretFile)
	a, st, cfg := setupTestServer(t)
	b, _ := replica(t, cfg)
	fake := &eventIDs{}
	kp := httptest.NewTLSServer(fake)
	defer kp.Close()
	for _, srv := range []*api.Server{a, b} {
		api.SetKyPostClientForTest(srv, &kypost.Client{BaseURL: kp.URL, Secret: kypostSecret, HTTP: kp.Client()})
	}
	ts := httptest.NewServer(a)
	defer ts.Close()
	aliceTok := ssoUser(t, st, "alice")
	ssoUser(t, st, "bob")

	db := rawDB(t, cfg.Database.Driver, cfg.Database.DSN)
	body := meeting("race", "alice", "ATTENDEE:mailto:bob@example.com", "ATTENDEE:mailto:carol@example.net")
	if r := rawDAV(t, ts, "PUT", "/dav/usr_alice/calendars/default/race.ics", "alice", aliceTok, body, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("PUT %d", r.StatusCode)
	}
	keys := map[string]bool{}
	rows, err := db.Query(`SELECT dedupe_key FROM scheduling_outbox WHERE recipient LIKE '%carol@example.net%'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		keys[k] = true
	}
	rows.Close()
	usage := func() (n int64) {
		if err := db.QueryRow(`SELECT bytes FROM calendar_usage WHERE id = 1`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := usage()

	var wg sync.WaitGroup
	for _, srv := range []*api.Server{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); srv.DeliverScheduled(context.Background()) }()
	}
	wg.Wait()

	c, err := st.Calendars().GetCalendarBySlug(context.Background(), "user", "usr_bob", "default")
	if err != nil {
		t.Fatal(err)
	}
	objs, err := st.Calendars().ListObjects(context.Background(), c.ID)
	if err != nil || len(objs) != 1 {
		t.Fatalf("bob holds %d objects (%v), want one copy", len(objs), err)
	}
	if delta := usage() - before; delta != int64(len(objs[0].Data)) {
		t.Fatalf("usage grew %d bytes for one %d-byte copy", delta, len(objs[0].Data))
	}
	if len(keys) != 1 || len(fake.ids) == 0 {
		t.Fatalf("outbox keys %v, sends %v", keys, fake.ids)
	}
	for _, id := range fake.ids {
		if !keys[id] {
			t.Fatalf("a send carried event ID %q, not the outbox dedupe key", id)
		}
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scheduling_outbox WHERE status = 'pending'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("%d rows still pending (%v)", pending, err)
	}
}

func rawDB(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	name := "sqlite"
	if driver == "postgres" {
		name = "pgx"
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

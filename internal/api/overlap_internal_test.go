package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// twin is a second server on s's database, as another replica without election.
func twin(t *testing.T, s *Server) *Server {
	t.Helper()
	st, err := store.Open(context.Background(), s.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	b := NewServer(s.config, st)
	b.signin.Store(s.signin.Load())
	return b
}

// Two servers syncing one meeting at once leave one room linked into the event and the
// outbox row done at its revision: a KyMessages PUT reconciles, so a repeat is harmless
// (spec AC12).
func TestRacingMeetingSyncsLeaveOneRoom(t *testing.T) {
	r := newMeetRig(t)
	b := twin(t, r.s)
	b.kymessages.HTTP = r.km.Client()
	b.kymessages.Set(&kymessages.Peer{URL: r.km.URL, Secret: meetSecret, Source: kymessages.SourceEnvironment})
	uid := r.create(t, map[string]any{"meeting": true})
	r.clock = r.clock.Add(2 * 3600 * 1e9)
	var wg sync.WaitGroup
	for _, srv := range []*Server{r.s, b} {
		wg.Add(1)
		go func() { defer wg.Done(); srv.SyncMeetings(context.Background()) }()
	}
	wg.Wait()
	ids := map[string]bool{}
	for _, c := range r.km.seen() {
		ids[c.ID] = true
	}
	if len(ids) != 1 {
		t.Fatalf("racing syncs used %d meeting IDs: %v", len(ids), ids)
	}
	data := string(r.object(t, uid).Data)
	if n := strings.Count(data, "CONFERENCE;"); n != 1 {
		t.Fatalf("%d CONFERENCE lines in the event:\n%s", n, data)
	}
	db := rawSQL(t, r.s)
	var rev, done int64
	if err := db.QueryRow(`SELECT rev, done_rev FROM video_meetings`).Scan(&rev, &done); err != nil {
		t.Fatal(err)
	}
	// Linking the room into the event bumps rev once more; one more pass settles it.
	r.s.SyncMeetings(context.Background())
	if err := db.QueryRow(`SELECT rev, done_rev FROM video_meetings`).Scan(&rev, &done); err != nil || done != rev {
		t.Fatalf("rev %d done_rev %d (%v), want equal", rev, done, err)
	}
}

// Two servers reconciling suite groups at once write one calendar per group.
func TestRacingSuiteGroupSyncsWriteOneCalendar(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := newSuiteGroupsIdP(t)
	s.signin.Store(idp.provider())
	idp.answer(http.StatusOK, `{"groups":[{"id":"kg1","name":"Sales","slug":"sales","state":"active","members":[]}]}`)
	b := twin(t, s)
	var wg sync.WaitGroup
	for _, srv := range []*Server{s, b, s, b} {
		wg.Add(1)
		go func() { defer wg.Done(); srv.SyncSuiteGroups(context.Background()) }()
	}
	wg.Wait()
	cals, err := s.store.Calendars().ListCalendarsByKind(context.Background(), "group")
	if err != nil || len(cals) != 1 {
		t.Fatalf("racing reconciles made %d group calendars (%v), want 1", len(cals), err)
	}
}

func rawSQL(t *testing.T, s *Server) *sql.DB {
	t.Helper()
	name := "sqlite"
	if s.config.Database.Driver == "postgres" {
		name = "pgx"
	}
	db, err := sql.Open(name, s.config.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

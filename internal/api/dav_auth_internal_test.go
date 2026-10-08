package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func davInternalServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, _ := config.LoadFromEnv()
	db := testdb.Config(t)
	db.DataDir = cfg.Database.DataDir
	cfg.Database = db
	st, err := store.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := NewServer(cfg, st)
	return s, s.withDAVAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
}

func davAttempt(h http.Handler, user, pass string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	req.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func auditCount(t *testing.T, s *Server) int {
	t.Helper()
	_, total, err := s.store.Audit().ListAuditRecords(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func TestDAVAuthInternalOversizeUsername(t *testing.T) {
	s, h := davInternalServer(t)
	for _, name := range []string{strings.Repeat("a", 10<<10), "bad\x01name"} {
		if w := davAttempt(h, name, "kc_x_y"); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("want 401 challenge, got %d", w.Code)
		}
	}
	if n := auditCount(t, s); n != 0 {
		t.Fatalf("audit rows: %d", n)
	}
	if n := len(s.davFails.seen); n != 0 {
		t.Fatalf("limiter entries: %d", n)
	}
}

func TestDAVAuthInternalAuditsIDsOnly(t *testing.T) {
	s, h := davInternalServer(t)
	ctx := context.Background()
	if err := s.store.Users().CreateUser(ctx, &store.User{ID: "usr_alice", Username: "alice", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	id, token, hash, _ := apppass.Generate()
	if err := s.store.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: id, UserID: "usr_alice", Label: "t", Hash: hash}, 0); err != nil {
		t.Fatal(err)
	}
	_, garbage, _, _ := apppass.Generate()
	davAttempt(h, "nobody-secret-pw", token)
	davAttempt(h, "alice", garbage)
	davAttempt(h, "alice", token+"x")
	recs, _, err := s.store.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || len(recs) != 3 {
		t.Fatalf("records %d %v", len(recs), err)
	}
	got := map[string]bool{}
	for _, r := range recs {
		got[r.Resource] = true
	}
	if !got["user:unknown"] || !got["usr_alice"] || len(got) != 2 {
		t.Fatalf("resources %v", got)
	}
}

func TestDAVFailuresFullKeepsLockouts(t *testing.T) {
	var f davFailures
	now := time.Now()
	for i := 0; i < davFailKeysCap-1; i++ {
		f.fail("ip:f"+strconv.Itoa(i), now.Add(-time.Minute))
	}
	for i := 0; i < davFailsPerIP; i++ {
		f.fail("ip:victim", now)
	}
	f.fail("ip:new", now)
	if b, _ := f.blocked("ip:victim", davFailsPerIP, now); !b {
		t.Fatal("full limiter lost a live lockout")
	}
	if len(f.seen) > davFailKeysCap {
		t.Fatalf("limiter grew to %d", len(f.seen))
	}
}

func TestDAVFailuresEvictsExpiredFirst(t *testing.T) {
	var f davFailures
	now := time.Now()
	f.fail("ip:expired", now.Add(-davFailWindow-time.Minute))
	for i := 0; i < davFailKeysCap-1; i++ {
		f.fail("ip:live"+strconv.Itoa(i), now.Add(-time.Minute))
	}
	f.fail("ip:new", now)
	if _, ok := f.seen["ip:expired"]; ok {
		t.Fatal("expired entry kept")
	}
	if _, ok := f.seen["ip:live0"]; !ok {
		t.Fatal("live entry evicted before the expired one")
	}
}

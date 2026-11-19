package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var errInjected = errors.New("injected store failure")

// flaky is a store whose parts fail or stall on demand.
type flaky struct {
	store.Store
	failSessions, failApps, failSettings, failWindows, stall atomic.Bool
	pings                                                    atomic.Int32
	driver                                                   string
}

func (f *flaky) Driver() string {
	if f.driver != "" {
		return f.driver
	}
	return f.Store.Driver()
}

func (f *flaky) Ping(ctx context.Context) error {
	f.pings.Add(1)
	if f.stall.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.Store.Ping(ctx)
}

func (f *flaky) Sessions() store.SessionStore { return flakySessions{f.Store.Sessions(), f} }
func (f *flaky) AppPasswords() store.AppPasswordStore {
	return flakyApps{f.Store.AppPasswords(), f}
}
func (f *flaky) Settings() store.SettingsStore { return flakySettings{f.Store.Settings(), f} }
func (f *flaky) Coordination() store.CoordinationStore {
	return flakyWindows{f.Store.Coordination(), f}
}

type flakySessions struct {
	store.SessionStore
	f *flaky
}

func (s flakySessions) GetSession(ctx context.Context, hash string) (*store.Session, error) {
	if s.f.stall.Load() {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.f.failSessions.Load() {
		return nil, errInjected
	}
	return s.SessionStore.GetSession(ctx, hash)
}

type flakyApps struct {
	store.AppPasswordStore
	f *flaky
}

func (a flakyApps) Get(ctx context.Context, id string) (*store.AppPassword, error) {
	if a.f.failApps.Load() {
		return nil, errInjected
	}
	return a.AppPasswordStore.Get(ctx, id)
}

type flakySettings struct {
	store.SettingsStore
	f *flaky
}

func (s flakySettings) GetSettings(ctx context.Context, keys ...string) (map[string]string, error) {
	if s.f.failSettings.Load() {
		return nil, errInjected
	}
	return s.SettingsStore.GetSettings(ctx, keys...)
}

type flakyWindows struct {
	store.CoordinationStore
	f *flaky
}

func (c flakyWindows) TakeAttempt(ctx context.Context, key string, limit int, window time.Duration) (int64, time.Duration, bool, error) {
	if c.f.failWindows.Load() {
		return 0, 0, false, errInjected
	}
	return c.CoordinationStore.TakeAttempt(ctx, key, limit, window)
}

// flakyServer is a test server over a flaky store.
func flakyServer(t *testing.T) (*api.Server, *flaky, store.Store, *config.Config) {
	t.Helper()
	_, st, cfg := setupTestServer(t)
	f := &flaky{Store: st}
	return api.NewServer(cfg, f), f, st, cfg
}

// replica is a second server on cfg's database: its own data directory on PostgreSQL; on
// SQLite, which is one host, the same one.
func replica(t *testing.T, cfg *config.Config) (*api.Server, store.Store) {
	t.Helper()
	c := *cfg
	if c.Database.Driver == "postgres" {
		c.Database.DataDir = t.TempDir()
	}
	st, err := store.Open(context.Background(), c.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return api.NewServer(&c, st), st
}

func requirePostgres(t *testing.T) {
	t.Helper()
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
}

func probe(srv *api.Server, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

// /livez never touches the database; /readyz follows it within its ping bound, both send
// no-store, and HEAD answers as GET does (spec AC1).
func TestLivezAndReadyz(t *testing.T) {
	srv, f, _, _ := flakyServer(t)
	for _, m := range []string{"GET", "HEAD"} {
		for path, want := range map[string]int{"/livez": 200, "/readyz": 200} {
			w := probe(srv, m, path)
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s %s: %d %q", m, path, w.Code, w.Header().Get("Cache-Control"))
			}
		}
	}
	if w := probe(srv, "GET", "/readyz"); !strings.Contains(w.Body.String(), `"ready"`) || strings.Contains(w.Body.String(), "sqlite") {
		t.Fatalf("readyz body: %s", w.Body.String())
	}
	f.stall.Store(true)
	time.Sleep(1100 * time.Millisecond) // past the cached result
	start := time.Now()
	if w := probe(srv, "GET", "/readyz"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz on a frozen database: %d", w.Code)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("readyz took %v on a frozen database, want within 2 s", d)
	}
	if w := probe(srv, "GET", "/livez"); w.Code != 200 {
		t.Fatalf("livez touched the database: %d", w.Code)
	}
	f.stall.Store(false)
	f.Store.Close()
	time.Sleep(1100 * time.Millisecond)
	if w := probe(srv, "HEAD", "/readyz"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with the database closed: %d", w.Code)
	}
	if w := probe(srv, "GET", "/livez"); w.Code != 200 {
		t.Fatalf("livez with the database closed: %d", w.Code)
	}
}

// A burst of probes shares one ping a second.
func TestReadyzPingsAtMostOncePerSecond(t *testing.T) {
	srv, f, _, _ := flakyServer(t)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() { defer wg.Done(); probe(srv, "GET", "/readyz") }()
	}
	wg.Wait()
	if n := f.pings.Load(); n != 1 {
		t.Fatalf("50 concurrent probes cost %d pings, want 1", n)
	}
}

// A request held up by a stalled store answers 503 JSON at the deadline (spec AC3, with the
// deadline shortened; the production value is pinned in TestRequestDeadlineFitsTheWriteTimeout).
func TestStalledRequestAnswersAtTheDeadline(t *testing.T) {
	defer api.SetRequestDeadlineForTest(300 * time.Millisecond)()
	srv, f, st, _ := flakyServer(t)
	cookie := loginAs(t, srv, st, "alice", "user")
	f.stall.Store(true)
	defer f.stall.Store(false)
	start := time.Now()
	w := do(t, srv, "GET", "/api/calendars", cookie)
	if d := time.Since(start); w.Code != http.StatusServiceUnavailable || d > 1300*time.Millisecond {
		t.Fatalf("stalled request: %d after %v: %s", w.Code, d, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"request_timeout"`) || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("deadline answer: %q %s", w.Header().Get("Content-Type"), w.Body.String())
	}
}

// The exemption list is exactly the named long routes, each registered and with its reason.
func TestLongRoutesAreExactlyTheNamedExemptions(t *testing.T) {
	want := []string{
		"POST /api/backup/deposit", "POST /api/backup/drill", "POST /api/backup/export-capsule",
		"POST /api/backup/pair-remote", "POST /api/calendars/{id}/import",
		"POST /api/subscriptions", "PATCH /api/subscriptions/{id}", "POST /api/calendars/{id}/refresh",
		"/api/sso/kysignon/callback", "PUT /api/admin/signin", "POST /api/admin/signin/test",
		"POST /api/book/{slug}/hold", "POST /api/presence", "GET /api/presence/me", "PUT /api/presence/me",
		"POST /api/suite/chat",
	}
	got := api.LongRoutesForTest()
	srv, _, _ := setupTestServer(t)
	routes := map[string]bool{}
	for _, r := range api.RoutesForTest(srv) {
		routes[r] = true
	}
	for _, r := range want {
		if got[r] == "" {
			t.Errorf("%s is not exempt, or has no reason", r)
		}
		if !routes[r] {
			t.Errorf("exempt route %s is not registered", r)
		}
		delete(got, r)
	}
	for r := range got {
		t.Errorf("%s is exempt but not named here", r)
	}
}

// A database error is never a sign-out: session routes and /api/auth/me answer 503, DAV and
// MCP answer 503 with Retry-After, and no failure is counted or audited; a missing credential
// still gets 401 (spec AC4).
func TestStoreFailureIsUnavailableNotSignedOut(t *testing.T) {
	srv, f, st, _ := flakyServer(t)
	cookie := loginAs(t, srv, st, "alice", "user")
	token := davUser(t, st, "bob", "user")
	mcpToken := mcpTokenFor(t, st, "carol")

	f.failSessions.Store(true)
	for _, path := range []string{"/api/calendars", "/api/suite/apps"} {
		w := do(t, srv, "GET", path, cookie)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"unavailable"`) {
			t.Fatalf("%s with the session store failing: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := do(t, srv, "GET", "/api/auth/me", cookie); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("/api/auth/me with the session store failing: %d %s", w.Code, w.Body.String())
	}
	f.failSessions.Store(false)

	auditsBefore := countAudit(t, st, "dav.auth_failed") + countAudit(t, st, "mcp.auth_failed")
	keysBefore := len(api.DAVFailKeysForTest(srv))
	f.failApps.Store(true)
	for range 12 { // more than the per-IP ten
		if code := davForwarded(srv, "192.0.2.50:4000", "", "bob", token); code != http.StatusServiceUnavailable {
			t.Fatalf("DAV with the app-password store failing: %d", code)
		}
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
		req.RemoteAddr = "192.0.2.50:4000"
		req.Header.Set("Authorization", "Bearer "+mcpToken)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("MCP with the app-password store failing: %d %q", w.Code, w.Header().Get("Retry-After"))
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	req.SetBasicAuth("bob", token)
	srv.ServeHTTP(w, req)
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("DAV 503 without Retry-After")
	}
	f.failApps.Store(false)
	if n := len(api.DAVFailKeysForTest(srv)); n != keysBefore {
		t.Fatalf("an unavailable store counted failures: %d keys, was %d", n, keysBefore)
	}
	if n := countAudit(t, st, "dav.auth_failed") + countAudit(t, st, "mcp.auth_failed"); n != auditsBefore {
		t.Fatalf("an unavailable store audited %d failed sign-ins", n-auditsBefore)
	}
	if code := davForwarded(srv, "192.0.2.50:4000", "", "bob", token); code == http.StatusServiceUnavailable || code == http.StatusUnauthorized || code == http.StatusTooManyRequests {
		t.Fatalf("DAV once the store answers again: %d", code)
	}

	// Real sign-outs stay 401.
	if w := do(t, srv, "GET", "/api/calendars", &http.Cookie{Name: auth.SessionCookieName, Value: "bogus"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("a bad cookie: %d", w.Code)
	}
	req = httptest.NewRequest("GET", "/api/auth/me", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatalf("/api/auth/me signed out: %d %s", w.Code, w.Body.String())
	}
}

func countAudit(t *testing.T, st store.Store, action string) int {
	t.Helper()
	recs, _, err := st.Audit().ListAuditRecords(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if r.Action == action {
			n++
		}
	}
	return n
}

// A counter the database cannot answer denies: login answers 429, it never falls back to
// memory (spec AC6).
func TestFailingCounterStoreDenies(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	f := &flaky{Store: st, driver: "postgres"} // the shared windows are PostgreSQL's
	srv := api.NewServer(cfg, f)
	f.failWindows.Store(true)
	if code := limitStatus(t, srv, "192.0.2.9:4000", ""); code != http.StatusTooManyRequests {
		t.Fatalf("login with the counter store failing: %d, want 429", code)
	}
}

// SQLite keeps its windows in memory and writes none.
func TestSQLiteWritesNoSharedWindows(t *testing.T) {
	srv, _, cfg := setupSQLiteServer(t)
	for range 3 {
		limitStatus(t, srv, "192.0.2.9:4000", "")
	}
	db, err := sql.Open("sqlite", cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM security_windows`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("SQLite wrote %d security_windows rows (%v)", n, err)
	}
}

func loginWith(srv *api.Server, peer, user, pass string) int {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = peer
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Code
}

// Two servers on one PostgreSQL database spend one budget: the per-account login window, the
// DAV per-IP and per-user failure windows and the office budget each admit exactly the limit
// in total, under concurrency (spec AC6).
func TestLimitsAreSharedAcrossReplicas(t *testing.T) {
	requirePostgres(t)
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	a, st, cfg := setupTestServer(t)
	b, _ := replica(t, cfg)
	servers := []*api.Server{a, b}
	loginAs(t, a, st, "erin", "user")

	t.Run("login per account", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := range 10 {
			wg.Add(1)
			go func() { defer wg.Done(); loginWith(servers[i%2], "198.51.100.20:4000", "erin", "WrongPass123!") }()
		}
		wg.Wait()
		for _, srv := range servers {
			if code := loginWith(srv, "198.51.100.20:4000", "erin", "SuperSecretPass123!"); code != http.StatusUnauthorized {
				t.Fatalf("the right password after 10 failures across two servers: %d, want 401", code)
			}
		}
	})

	t.Run("DAV per IP", func(t *testing.T) {
		var wg sync.WaitGroup
		var refused atomic.Int32
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if davForwarded(servers[i%2], "198.51.100.30:4000", "", "nobody", "kc_bad_bad") == http.StatusTooManyRequests {
					refused.Add(1)
				}
			}()
		}
		wg.Wait()
		if n := refused.Load(); n != 10 {
			t.Fatalf("%d of 20 refused across two servers, want 10 past the shared ten", n)
		}
	})

	t.Run("DAV per user", func(t *testing.T) {
		token := davUser(t, st, "frank", "user")
		id, _, _ := strings.Cut(strings.TrimPrefix(token, "kc_"), "_")
		wrong := "kc_" + id + "_" + strings.Repeat("a", 52)
		for i := range 50 { // fifty addresses, so only the per-user window counts
			if code := davForwarded(servers[i%2], fmt.Sprintf("198.51.101.%d:4000", i+1), "", "frank", wrong); code != http.StatusUnauthorized {
				t.Fatalf("failure %d: %d", i, code)
			}
		}
		for _, srv := range servers {
			if code := davForwarded(srv, "198.51.102.1:4000", "", "frank", wrong); code != http.StatusTooManyRequests {
				t.Fatalf("the 51st failure for one user: %d, want 429", code)
			}
		}
	})

	t.Run("office budget", func(t *testing.T) {
		api.SpendOfficeBudgetForTest(a, "login", api.OfficeBudgetForTest)
		// Spent on A, B's office request meets the per-IP window again: 20 then 429.
		for i := 1; i <= 20; i++ {
			if code := limitStatus(t, b, officePeer, ""); code == http.StatusTooManyRequests {
				t.Fatalf("attempt %d throttled early", i)
			}
		}
		if code := limitStatus(t, b, officePeer, ""); code != http.StatusTooManyRequests {
			t.Fatalf("B still exempted the office after A spent its budget: %d", code)
		}
	})
}

// A password-change window is shared too: five attempts per account a minute in total.
func TestPasswordChangeWindowIsShared(t *testing.T) {
	requirePostgres(t)
	a, st, cfg := setupTestServer(t)
	b, _ := replica(t, cfg)
	cookie := loginAs(t, a, st, "gail", "user")
	codes := []int{}
	for i := range 6 {
		srv := []*api.Server{a, b}[i%2]
		w := doJSON(t, srv, "POST", "/api/auth/change-password", cookie, map[string]string{"current_password": "nope-nope-nope", "new_password": "AnotherGoodPass123!"})
		codes = append(codes, w.Code)
	}
	if codes[5] != http.StatusTooManyRequests {
		t.Fatalf("change-password across two servers: %v, want the sixth refused", codes)
	}
}

// A CAPTCHA solution admits one request across replicas (spec AC7).
func TestPoWSolutionIsSpentOnceAcrossReplicas(t *testing.T) {
	requirePostgres(t)
	a, _, cfg := setupTestServer(t)
	cfg.Captcha.Provider = "pow"
	b, _ := replica(t, cfg)
	a = api.NewServer(cfg, mustReopen(t, cfg))
	token := solvePoW(t, a)
	post := func(srv *api.Server) int {
		body, _ := json.Marshal(map[string]string{"username": "nobody", "password": "WrongPass123!", "captcha_token": token})
		req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}
	if code := post(a); code != http.StatusUnauthorized {
		t.Fatalf("first use: %d, want the CAPTCHA passed and a 401", code)
	}
	if code := post(b); code != http.StatusForbidden {
		t.Fatalf("replay on another replica: %d, want 403", code)
	}
}

func mustReopen(t *testing.T, cfg *config.Config) store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A network removed on A stops being exempt on B within 10 s (spec AC9).
func TestOfficeListFollowsTheDatabase(t *testing.T) {
	requirePostgres(t)
	a, st, cfg := setupTestServer(t)
	b, _ := replica(t, cfg)
	admin := loginAs(t, a, st, "hank", "admin")
	if w := doJSON(t, a, "PUT", "/api/settings/rate-limit-networks", admin, map[string]string{"networks": officeRange}); w.Code != 200 {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	time.Sleep(6 * time.Second) // B's copy is at most 5 s old
	for i := range 25 {
		if code := limitStatus(t, b, officePeer, ""); code == http.StatusTooManyRequests {
			t.Fatalf("B did not exempt the office network A added (attempt %d)", i+1)
		}
	}
	if w := doJSON(t, a, "PUT", "/api/settings/rate-limit-networks", admin, map[string]string{"networks": ""}); w.Code != 200 {
		t.Fatalf("remove: %d", w.Code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if limitStatus(t, b, "203.0.113.99:4000", "") == http.StatusTooManyRequests {
			return // B applies the per-IP limit to the removed network again
		}
	}
	t.Fatal("B kept exempting a network A removed, 10 s later")
}

// Back up now, an export and a CLI run hold one backup-run lock: a second run is refused 409
// busy and sends nothing (spec AC13).
func TestBackupRunLockRefusesASecondRun(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	k, _ := recoverykey.Generate()
	if err := backup.PinRecoveryKey(context.Background(), cfg, st, recoveryclient.RecoveryKey{Public: k.Public(), Threshold: 2, TotalShares: 3}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDepositor{}
	api.SetRecoveryClientForTest(srv, fake)
	admin := loginAs(t, srv, st, "ivy", "admin")
	held, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = backup.WithRunLock(context.Background(), st, func(context.Context) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	for _, path := range []string{"/api/backup/deposit", "/api/backup/export-capsule"} {
		w := adminPost(t, srv, admin, path)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"busy"`) {
			t.Fatalf("%s while a run holds the lock: %d %s", path, w.Code, w.Body.String())
		}
	}
	close(release)
	if fake.got != nil {
		t.Fatal("a refused run sent a capsule")
	}
}

// Two servers pin different keys at once over HTTP: one 200, one 409, both report the winner.
// The pinned key with another split is 409 on pin-key and on pairing (spec AC18).
func TestConcurrentHTTPPinsAndWriteOnceTopology(t *testing.T) {
	a, st, cfg := setupTestServer(t)
	b, stB := replica(t, cfg)
	adminA := loginAs(t, a, st, "jo", "admin")
	adminB := sessionOn(t, b, stB, "jo")
	keys := []recoverykey.PublicKey{mustKey(t), mustKey(t)}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, srv := range []*api.Server{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cookie := []*http.Cookie{adminA, adminB}[i]
			codes[i] = adminDo(t, srv, cookie, "POST", "/api/backup/pin-key", pinBody(keys[i], 2, 3)).Code
		}()
	}
	wg.Wait()
	winner := 0
	if codes[1] == 200 {
		winner = 1
	}
	if codes[winner] != 200 || codes[1-winner] != http.StatusConflict {
		t.Fatalf("concurrent pins: %v, want one 200 and one 409", codes)
	}
	for i, srv := range []*api.Server{a, b} {
		cookie := []*http.Cookie{adminA, adminB}[i]
		if got := statusOf(t, srv, cookie)["recovery_key_id"]; got != keys[winner].ID() {
			t.Fatalf("server %d reports %v, not the winner", i, got)
		}
	}
	if w := adminDo(t, a, adminA, "POST", "/api/backup/pin-key", pinBody(keys[winner], 2, 5)); w.Code != http.StatusConflict {
		t.Fatalf("the pinned key with another split: %d", w.Code)
	}
	if w := adminDo(t, a, adminA, "POST", "/api/backup/pin-key", pinBody(keys[winner], 2, 3)); w.Code != 200 {
		t.Fatalf("the pinned key and split again: %d %s", w.Code, w.Body.String())
	}
	api.SetRecoveryClientForTest(a, fakePairer{result: recoveryclient.PairingResult{APIToken: "kyrec_live_t",
		Key: recoveryclient.RecoveryKey{Public: keys[winner], Threshold: 3, TotalShares: 3}}})
	w := adminDo(t, a, adminA, "POST", "/api/backup/pair-remote", map[string]string{"recovery_url": "https://recovery.busnes.app", "pairing_code": "123456"})
	if w.Code != http.StatusConflict {
		t.Fatalf("pairing with the pinned key and another split: %d %s", w.Code, w.Body.String())
	}
	if got := statusOf(t, a, adminA); got["threshold"] != float64(2) || got["total_shares"] != float64(3) || got["paired"] != false {
		t.Fatalf("a refused pairing changed the pin or paired: %v", got)
	}
}

func mustKey(t *testing.T) recoverykey.PublicKey {
	t.Helper()
	k, err := recoverykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k.Public()
}

// sessionOn signs an existing user in on another server.
func sessionOn(t *testing.T, srv *api.Server, st store.Store, user string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": "SuperSecretPass123!"})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("login on the second server: %d %s", w.Code, w.Body.String())
	return nil
}

// A pairing or pin that cannot be read is 503 on status and 5xx on deposit and export, never
// "not paired" or a 412 (spec AC20).
func TestUnreadablePairingIsNotUnpaired(t *testing.T) {
	srv, f, st, cfg := flakyServer(t)
	k, _ := recoverykey.Generate()
	if err := backup.PinRecoveryKey(context.Background(), cfg, st, recoveryclient.RecoveryKey{Public: k.Public(), Threshold: 2, TotalShares: 3}); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, srv, st, "kim", "admin")
	f.failSettings.Store(true)
	if w := adminDo(t, srv, admin, "GET", "/api/backup/status", nil); w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), `"paired"`) {
		t.Fatalf("status with settings unreadable: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/backup/deposit", "/api/backup/export-capsule"} {
		if w := adminPost(t, srv, admin, path); w.Code < 500 {
			t.Fatalf("%s with settings unreadable: %d %s, want 5xx", path, w.Code, w.Body.String())
		}
	}
}

// The replica record never leaves the server.
func TestReplicaRecordIsNotInSettings(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	for _, key := range []string{config.ReplicaConfigSetting, backup.RecoveryKeyRecordSetting} {
		if err := st.Settings().SetSetting(ctx, key, "secret-ish"); err != nil {
			t.Fatal(err)
		}
	}
	admin := loginAs(t, srv, st, "lee", "admin")
	if w := do(t, srv, "GET", "/api/settings", admin); w.Code != 200 || strings.Contains(w.Body.String(), "secret-ish") {
		t.Fatalf("settings carried a hidden row: %d %s", w.Code, w.Body.String())
	}
}

// mcpTokenFor makes an everyday user with an MCP token.
func mcpTokenFor(t *testing.T, st store.Store, username string) string {
	t.Helper()
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_" + username, Username: username, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{Scope: store.ScopeMCPRead, ID: id, UserID: "usr_" + username, Label: "agent", Hash: hash}, 0); err != nil {
		t.Fatal(err)
	}
	return token
}

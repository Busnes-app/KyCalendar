package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
	"github.com/Busnes-app/kycalendar/internal/worker"
)

// The stop budget fits the grace periods: PostgreSQL drains 5 s, then 5 s of Shutdown and 10 s
// of waiting, 20 s; SQLite skips the drain, 15 s; Compose grants 30 s.
func TestShutdownBudgetFitsTheGracePeriods(t *testing.T) {
	pg := drainDelay("postgres") + shutdownTimeout + backupWaitTimeout
	lite := drainDelay("sqlite") + shutdownTimeout + backupWaitTimeout
	if pg != 20*time.Second || lite != 15*time.Second {
		t.Fatalf("budgets: PostgreSQL %s, SQLite %s; want 5+5+10 = 20 s and 5+10 = 15 s", pg, lite)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexpFind(`(?m)^\s*stop_grace_period:\s*(\S+)\s*$`, raw)
	grace, err := time.ParseDuration(m)
	if err != nil {
		t.Fatalf("stop_grace_period %q: %v", m, err)
	}
	if limit := 30 * time.Second; pg > limit || grace < limit || grace <= pg {
		t.Fatalf("stop_grace_period %s must cover %s and Kubernetes' 30 s default", grace, pg)
	}
}

// startServer serves srv on a loopback listener and returns its base URL and the http.Server.
func startServer(t *testing.T, srv http.Handler) (string, *http.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: srv, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })
	return "http://" + ln.Addr().String(), hs
}

// signIn creates a local admin and returns a client holding its session cookie.
func signIn(t *testing.T, st store.Store, base, user, role string) *http.Client {
	t.Helper()
	hash, _ := password.Hash("SuperSecretPass123!")
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_" + user, Username: user, PasswordHash: hash, Role: role, Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": user, "password": "SuperSecretPass123!"})
	res, err := c.Post(base+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("login: %v %v", res, err)
	}
	res.Body.Close()
	return c
}

// csrfPost posts with the session's CSRF token.
func csrfPost(c *http.Client, base, path string) (*http.Response, error) {
	u, _ := url.Parse(base)
	req, _ := http.NewRequest("POST", base+path, nil)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.CSRFCookieName {
			req.Header.Set(auth.HeaderCSRF, ck.Value)
		}
	}
	return c.Do(req)
}

func status(c *http.Client, method, u string) int {
	req, _ := http.NewRequest(method, u, nil)
	res, err := c.Do(req)
	if err != nil {
		return -1
	}
	res.Body.Close()
	return res.StatusCode
}

// The stop sequence on a real listener: /readyz is 503 through the drain while /livez and an
// authenticated GET still answer, new backup work is refused, a request in flight finishes,
// and only then does the listener close. SQLite does not drain (spec AC2).
func TestGracefulStopDrainsBeforeClosing(t *testing.T) {
	cfg := serverConfig(t, config.DatabaseConfig{Driver: "sqlite"})
	st := openStore(t, cfg)
	srv := api.NewServer(cfg, st)
	slow := make(chan struct{})
	mux := http.NewServeMux()
	mux.Handle("/", srv)
	mux.HandleFunc("/test/in-flight", func(w http.ResponseWriter, _ *http.Request) { <-slow; w.WriteHeader(204) })
	base, hs := startServer(t, mux)
	admin := signIn(t, st, base, "admin", "admin")
	user := signIn(t, st, base, "ann", "user")

	inFlight := make(chan int, 1)
	go func() { inFlight <- status(http.DefaultClient, "GET", base+"/test/in-flight") }()
	time.Sleep(100 * time.Millisecond)

	var stopped atomic.Bool
	workDone := make(chan struct{})
	stopWork := func() { stopped.Store(true); close(workDone) }
	start := time.Now()
	finished := make(chan struct{})
	go func() {
		gracefulStop(srv, hs, stopWork, func() {}, workDone, drainDelay("postgres"))
		close(finished)
	}()
	time.Sleep(200 * time.Millisecond)
	if !stopped.Load() {
		t.Fatal("elected work was not cancelled at the start of the drain")
	}
	for time.Since(start) < 4*time.Second {
		if c := status(http.DefaultClient, "GET", base+"/readyz"); c != 503 {
			t.Fatalf("readyz during the drain: %d", c)
		}
		if c := status(http.DefaultClient, "HEAD", base+"/livez"); c != 200 {
			t.Fatalf("livez during the drain: %d", c)
		}
		if c := status(user, "GET", base+"/api/calendars"); c != 200 {
			t.Fatalf("an authenticated GET during the drain: %d", c)
		}
		for _, path := range []string{"/api/backup/deposit", "/api/backup/drill", "/api/backup/export-capsule"} {
			res, err := csrfPost(admin, base, path)
			if err != nil || res.StatusCode != 503 {
				t.Fatalf("%s during the drain: %v %v", path, res, err)
			}
			res.Body.Close()
		}
		time.Sleep(250 * time.Millisecond)
	}
	close(slow)
	if c := <-inFlight; c != 204 {
		t.Fatalf("the request in flight got %d", c)
	}
	<-finished
	if d := time.Since(start); d < readinessDrainDelay {
		t.Fatalf("the listener closed after %v, inside the drain", d)
	}
	if c := status(http.DefaultClient, "GET", base+"/livez"); c != -1 {
		t.Fatalf("the listener still answers after the stop: %d", c)
	}

	// SQLite: no drain.
	cfg2 := serverConfig(t, config.DatabaseConfig{Driver: "sqlite"})
	srv2 := api.NewServer(cfg2, openStore(t, cfg2))
	_, hs2 := startServer(t, srv2)
	done2 := make(chan struct{})
	close(done2)
	start = time.Now()
	gracefulStop(srv2, hs2, func() {}, func() {}, done2, drainDelay("sqlite"))
	if d := time.Since(start); d > time.Second {
		t.Fatalf("SQLite stop took %v; it must not drain", d)
	}
}

func regexpFind(pattern string, raw []byte) string {
	m := mustRegexp(pattern).FindSubmatch(raw)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// lockHolder is the backend holding name's session lock in st's schema.
func lockHolder(t *testing.T, cfg *config.Config, name string) int {
	t.Helper()
	db := pgDB(t, cfg.Database.DSN)
	var pid int
	err := db.QueryRow(`SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND granted AND classid = 3
AND objid = ((hashtext(current_schema() || ':' || $1)::bigint + 4294967296) % 4294967296)::text::oid
AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`, name).Scan(&pid)
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func terminate(t *testing.T, cfg *config.Config, pid int) {
	t.Helper()
	if _, err := pgDB(t, cfg.Database.DSN).Exec(`SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatal(err)
	}
}

// Three servers elect one owner: one tick counter advances at a time. Ending the owner's lock
// session cancels its jobs within 3 s and another server takes over within two retries
// (spec AC10).
func TestOneOfThreeServersRunsTheJobs(t *testing.T) {
	requirePostgres(t)
	db := testdb.Config(t)
	cfg := serverConfig(t, db)
	opts := worker.Options{Retry: time.Second, Grace: 3 * time.Second}
	var ticks [3]atomic.Int32
	cancelled := make(chan time.Time, 3)
	for i := range 3 {
		st := openStore(t, cfg)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go worker.Run(ctx, st.Coordination(), opts, []worker.Job{{Name: "tick", Every: 50 * time.Millisecond, Run: func(ctx context.Context) {
			ticks[i].Add(1)
			go func() { <-ctx.Done(); cancelled <- time.Now() }()
		}}})
	}
	owner := func() int {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var running []int
			for i := range ticks {
				if ticks[i].Load() > 0 {
					running = append(running, i)
				}
			}
			if len(running) > 1 {
				t.Fatalf("servers %v all ran jobs", running)
			}
			if len(running) == 1 {
				return running[0]
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("no server was elected")
		return -1
	}
	first := owner()
	time.Sleep(time.Second)
	killed := time.Now()
	terminate(t, cfg, lockHolder(t, cfg, worker.Lock))
	select {
	case at := <-cancelled:
		if d := at.Sub(killed); d > 3*time.Second {
			t.Fatalf("the owner's jobs were cancelled %v after its session ended", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the owner's jobs were never cancelled")
	}
	ticks[first].Store(0)
	drain := time.Now()
	for i := range ticks {
		if i != first {
			ticks[i].Store(0)
		}
	}
	next := -1
	for deadline := time.Now().Add(2*opts.Retry + opts.Grace + 2*time.Second); time.Now().Before(deadline) && next < 0; time.Sleep(50 * time.Millisecond) {
		for i := range ticks {
			if ticks[i].Load() > 0 {
				next = i
			}
		}
	}
	if next < 0 {
		t.Fatalf("no server took over within two retries and the grace (%v)", time.Since(drain))
	}
}

// A second SQLite server process on the same data directory never runs jobs while the first
// is alive, and takes over once it dies.
func TestSecondSQLiteProcessNeverRunsJobs(t *testing.T) {
	if os.Getenv("KYCAL_WORKER_HELPER") != "" {
		return
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "kycalendar.db")
	start := func(mark string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerHelperProcess$")
		cmd.Env = append(os.Environ(), "KYCAL_WORKER_HELPER=1", "KYCAL_WORKER_DB="+db, "KYCAL_WORKER_MARK="+mark, "PUSH_RELAY_URL=", "APNS_RELAY_URL=")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	waitFor := func(path string, within time.Duration) bool {
		for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
		return false
	}
	a := start(filepath.Join(dir, "a.ran"))
	if !waitFor(filepath.Join(dir, "a.ran"), 10*time.Second) {
		t.Fatal("the first process never ran a job")
	}
	start(filepath.Join(dir, "b.ran"))
	if waitFor(filepath.Join(dir, "b.ran"), 3*time.Second) {
		t.Fatal("a second process ran jobs while the first was alive")
	}
	_ = a.Process.Kill()
	_ = a.Wait()
	if !waitFor(filepath.Join(dir, "b.ran"), 5*time.Second) {
		t.Fatal("the second process did not take over")
	}
}

// TestWorkerHelperProcess is the child of TestSecondSQLiteProcessNeverRunsJobs.
func TestWorkerHelperProcess(t *testing.T) {
	if os.Getenv("KYCAL_WORKER_HELPER") == "" {
		t.Skip("helper process")
	}
	st, err := store.Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", DSN: os.Getenv("KYCAL_WORKER_DB")})
	if err != nil {
		t.Fatal(err)
	}
	mark := os.Getenv("KYCAL_WORKER_MARK")
	worker.Run(context.Background(), st.Coordination(), worker.Options{Retry: 200 * time.Millisecond}, []worker.Job{{Name: "mark", Every: 100 * time.Millisecond, AtStart: true, Run: func(context.Context) {
		_ = os.WriteFile(mark, []byte("ran"), 0o600)
	}}})
}

// intervals records each deposit's [start, end]. The first blocks until its context ends.
type intervals struct {
	mu    sync.Mutex
	spans [][2]time.Time
	first chan struct{}
}

func (d *intervals) Deposit(ctx context.Context, _, _ string, raw []byte) (recoveryclient.Receipt, error) {
	start := time.Now()
	d.mu.Lock()
	n := len(d.spans)
	d.spans = append(d.spans, [2]time.Time{start})
	d.mu.Unlock()
	end := func() {
		d.mu.Lock()
		d.spans[n][1] = time.Now()
		d.mu.Unlock()
	}
	if n == 0 {
		close(d.first)
		<-ctx.Done()
		end()
		return recoveryclient.Receipt{}, fmt.Errorf("%w: %w", recoveryclient.ErrRemote, ctx.Err())
	}
	end()
	return receiptFor(raw), nil
}

// pairedBackup pins a key and pairs cfg's database, due now.
func pairedBackup(t *testing.T, cfg *config.Config, st store.Store) {
	t.Helper()
	ctx := context.Background()
	priv, _ := recoverykey.Generate()
	if err := backup.PinRecoveryKey(ctx, cfg, st, recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3}); err != nil {
		t.Fatal(err)
	}
	sealer, _ := backup.NewSealer(cfg)
	if err := recoveryclient.StorePairing(backup.Settings(ctx, st.Settings()), sealer, "https://recovery.busnes.app", "kyrec_live_t"); err != nil {
		t.Fatal(err)
	}
	cfg.Backup.DepositInterval = time.Hour
}

// A deposit in flight on A when A's lock session is terminated is cancelled; B's first
// deposit starts only after A's ended; both runs are audited. The lib's Seal and local-copy
// write are not cancellable mid-call: that window is the documented exception, not fenced here
// (spec AC11).
func TestTakeoverNeverOverlapsDeposits(t *testing.T) {
	requirePostgres(t)
	db := testdb.Config(t)
	cfgA := serverConfig(t, db)
	stA := openStore(t, cfgA)
	pairedBackup(t, cfgA, stA)
	cfgB := serverConfig(t, db)
	cfgB.Backup.DepositInterval = time.Hour
	stB := openStore(t, cfgB)
	dep := &intervals{first: make(chan struct{})}
	opts := worker.Options{Retry: time.Second, Grace: 3 * time.Second}
	run := func(cfg *config.Config, st store.Store) {
		job, ok := backupJob(cfg, st, dep, 100*time.Millisecond)
		if !ok {
			t.Fatal("no backup job")
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go worker.Run(ctx, st.Coordination(), opts, []worker.Job{job})
	}
	run(cfgA, stA)
	select {
	case <-dep.first:
	case <-time.After(15 * time.Second):
		t.Fatal("A never started its deposit")
	}
	run(cfgB, stB)
	terminate(t, cfgA, lockHolder(t, cfgA, worker.Lock))
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		dep.mu.Lock()
		n := len(dep.spans)
		done := n >= 2 && !dep.spans[1][1].IsZero()
		dep.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B never deposited after the takeover (%d deposits)", n)
		}
	}
	dep.mu.Lock()
	a, b := dep.spans[0], dep.spans[1]
	dep.mu.Unlock()
	if a[1].IsZero() || !b[0].After(a[1]) {
		t.Fatalf("deposits overlap: A %v-%v, B from %v", a[0], a[1], b[0])
	}
	recs, _, _ := stA.Audit().ListAuditRecords(context.Background(), 0, 50)
	var interrupted, succeeded bool
	for _, r := range recs {
		if r.Action == "admin.backup_run" && strings.Contains(r.Details, "interrupted") {
			interrupted = true
		}
		if r.Action == "admin.backup_run" && strings.Contains(r.Details, `outcome="success"`) {
			succeeded = true
		}
	}
	if !interrupted || !succeeded {
		t.Fatalf("runs not audited (interrupted %v, success %v): %+v", interrupted, succeeded, recs)
	}
}

// Due is read after the backup-run lock: three servers ticking at once make one capsule
// (spec AC13), on either driver.
func TestConcurrentDueTicksMakeOneCapsule(t *testing.T) {
	db := testdb.Config(t)
	base := serverConfig(t, db)
	var count atomic.Int32
	dep := countingDepositor{&count}
	var jobs []worker.Job
	for i := range 3 {
		cfg := serverConfig(t, db) // its own data directory on PostgreSQL
		if db.Driver == "sqlite" {
			cfg.Database = base.Database // one host: one file, one directory
		}
		st := openStore(t, cfg)
		if i == 0 {
			pairedBackup(t, cfg, st)
		}
		cfg.Backup.DepositInterval = time.Hour
		job, _ := backupJob(cfg, st, dep, time.Minute)
		jobs = append(jobs, job)
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() { defer wg.Done(); j.Run(context.Background()) }()
	}
	wg.Wait()
	for _, j := range jobs { // and again: no longer due
		j.Run(context.Background())
	}
	if n := count.Load(); n != 1 {
		t.Fatalf("%d capsules from concurrent due ticks, want 1", n)
	}
}

type countingDepositor struct{ n *atomic.Int32 }

func (c countingDepositor) Deposit(_ context.Context, _, _ string, raw []byte) (recoveryclient.Receipt, error) {
	c.n.Add(1)
	return receiptFor(raw), nil
}

func mustRegexp(p string) *regexp.Regexp { return regexp.MustCompile(p) }

func pgDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// receiptFor is the receipt KyRecovery returns for raw.
func receiptFor(raw []byte) recoveryclient.Receipt {
	m, _ := capsule.ReadUnverifiedManifest(raw)
	sum := sha256.Sum256(raw)
	return recoveryclient.Receipt{CapsuleID: m.CapsuleID, Digest: hex.EncodeToString(sum[:]), SizeBytes: int64(len(raw)), DepositedAt: time.Now().UTC()}
}

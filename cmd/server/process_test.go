package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store/migrations"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// buildServer builds the real binary once per test.
func buildServer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "kycalendar")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// proc is one server process.
type proc struct {
	cmd  *exec.Cmd
	base string
	log  *bytes.Buffer
	done chan error
}

// replicaEnv is what every replica shares, plus its own port and data directory. The relay
// variables are empty: a throwaway server never reaches the production relays.
func replicaEnv(dsn string, port int, dataDir string, extra ...string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"PUSH_RELAY_URL=", "APNS_RELAY_URL=",
		"KY_DB_DRIVER=postgres", "KY_DB_DSN=" + dsn,
		"KY_SESSION_SECRET=" + strings.Repeat("p", 40),
		"KY_ENCRYPTION_KEY=" + strings.Repeat("ab", 32),
		"KYCALENDAR_SCHEDULING=false", "KY_CAPTCHA_PROVIDER=none",
		"KY_APP_URL=http://localhost:8080", "KY_HOST=127.0.0.1",
		"KY_PORT=" + strconv.Itoa(port), "KY_DATA_DIR=" + dataDir,
		"KY_SSO_ENABLED=false",
	}
	return append(env, extra...)
}

func startProc(t *testing.T, bin string, env []string, port int) *proc {
	t.Helper()
	p := &proc{cmd: exec.Command(bin), base: fmt.Sprintf("http://127.0.0.1:%d", port), log: &bytes.Buffer{}, done: make(chan error, 1)}
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.log, p.log
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		if t.Failed() {
			t.Logf("server %s log:\n%s", p.base, p.log.String())
		}
	})
	return p
}

func get(c *http.Client, u string) int {
	res, err := c.Get(u)
	if err != nil {
		return -1
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}

func waitStatus(t *testing.T, u string, want int, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	c := &http.Client{Timeout: 2 * time.Second}
	for time.Since(start) < within {
		if get(c, u) == want {
			return time.Since(start)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s did not answer %d within %v", u, want, within)
	return 0
}

// session signs user in on base and returns a client holding the cookie; a forced password
// change is done first.
func session(t *testing.T, base, user, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	login := func(pw string) map[string]any {
		body, _ := json.Marshal(map[string]string{"username": user, "password": pw})
		res, err := c.Post(base+"/api/auth/login", "application/json", bytes.NewReader(body))
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("login %s: %v %v", user, res, err)
		}
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		return out
	}
	out := login(pass)
	if u, _ := out["user"].(map[string]any); u != nil && u["must_change_password"] == true {
		res := send(t, c, base, "POST", "/api/auth/change-password", map[string]string{"current_password": pass, "new_password": pass + "-new"})
		if res.StatusCode != 200 {
			t.Fatalf("change password: %d", res.StatusCode)
		}
		login(pass + "-new")
	}
	return c
}

func send(t *testing.T, c *http.Client, base, method, path string, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, base+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	u, _ := url.Parse(base)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.CSRFCookieName {
			req.Header.Set(auth.HeaderCSRF, ck.Value)
		}
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func davStatus(base, user, token string, timeout time.Duration) int {
	req, _ := http.NewRequest("PROPFIND", base+"/dav/", strings.NewReader(`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><current-user-principal/></prop></propfind>`))
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml")
	req.SetBasicAuth(user, token)
	res, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return -1
	}
	res.Body.Close()
	return res.StatusCode
}

// Two replicas of the real binary on PostgreSQL through a freezable relay (spec AC31). A
// frozen database: /readyz 503 within 4 s, /livez 200, an authenticated GET 503 within 16 s,
// DAV 503 and not 401, no exit; thawed: ready within 15 s, the cookie still works and a backup
// runs. SIGTERM to one: its /readyz is 503 before its listener closes, it exits 0 within 25 s,
// and the other keeps authorizing the same cookie and app password.
func TestReplicaProcessesSurviveFreezeAndStop(t *testing.T) {
	dsn := requirePostgres(t)
	bin := buildServer(t)
	db := testdb.Config(t)
	relay := testdb.NewRelay(t, testdb.PostgresTarget(t, dsn))
	via := testdb.Via(t, db, relay).DSN
	ports := []int{freePort(t), freePort(t)}
	var procs []*proc
	for i, port := range ports {
		backups := t.TempDir()
		env := replicaEnv(via, port, t.TempDir(), "KY_ADMIN_PASSWORD=AdminPassword-123!", "KYCALENDAR_BACKUP_DIR="+backups)
		procs = append(procs, startProc(t, bin, env, port))
		waitStatus(t, procs[i].base+"/readyz", 200, 30*time.Second)
	}
	a, b := procs[0], procs[1]

	// An everyday user from the CLI, signed in on A, with a DAV app password.
	create := exec.Command(bin, "create-user", "-username", "ann")
	create.Env = replicaEnv(via, freePort(t), t.TempDir())
	create.Stdin = strings.NewReader("TemporaryPass-123!\n")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create-user: %v\n%s", err, out)
	}
	ann := session(t, a.base, "ann", "TemporaryPass-123!")
	res := send(t, ann, a.base, "POST", "/api/app-passwords", map[string]string{"label": "phone", "scope": "dav"})
	var created struct{ Password string }
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil || created.Password == "" {
		t.Fatalf("app password: %d %v", res.StatusCode, err)
	}
	moveCookies(ann, a.base, b.base)
	if c := get(ann, b.base+"/api/calendars"); c != 200 {
		t.Fatalf("the cookie on B: %d", c)
	}
	if c := davStatus(b.base, "ann", created.Password, 10*time.Second); c != 207 {
		t.Fatalf("the app password on B: %d", c)
	}

	// Freeze the database.
	relay.Freeze()
	for _, p := range procs {
		if d := waitStatus(t, p.base+"/readyz", 503, 4*time.Second); d > 4*time.Second {
			t.Fatalf("readyz took %v", d)
		}
		if c := get(&http.Client{Timeout: 2 * time.Second}, p.base+"/livez"); c != 200 {
			t.Fatalf("livez on a frozen database: %d", c)
		}
	}
	start := time.Now()
	if c := get(ann, b.base+"/api/calendars"); c != 503 || time.Since(start) > 16*time.Second {
		t.Fatalf("an authenticated GET on a frozen database: %d after %v", c, time.Since(start))
	}
	if c := davStatus(b.base, "ann", created.Password, 20*time.Second); c != 503 {
		t.Fatalf("DAV on a frozen database: %d, want 503 (never 401)", c)
	}
	for _, p := range procs {
		select {
		case err := <-p.done:
			t.Fatalf("a server exited on a frozen database: %v", err)
		default:
		}
	}
	relay.Thaw()
	for _, p := range procs {
		waitStatus(t, p.base+"/readyz", 200, 15*time.Second)
	}
	if c := get(ann, b.base+"/api/calendars"); c != 200 {
		t.Fatalf("the cookie after the thaw: %d", c)
	}
	admin := session(t, a.base, "admin", "AdminPassword-123!")
	priv, _ := recoverykey.Generate()
	if res := send(t, admin, a.base, "POST", "/api/backup/pin-key", map[string]any{"public_key": base64.StdEncoding.EncodeToString(priv.Public().Bytes()), "threshold": 2, "total_shares": 3}); res.StatusCode != 200 {
		t.Fatalf("pin: %d", res.StatusCode)
	}
	if res := send(t, admin, a.base, "POST", "/api/backup/deposit", nil); res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("a backup after the thaw: %d %s", res.StatusCode, body)
	}

	// SIGTERM to A: /readyz turns 503 while A still listens, then A exits 0.
	if err := a.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a.base+"/readyz", 503, 2*time.Second)
	stopped := time.Now()
	for {
		if c := get(ann, b.base+"/api/calendars"); c != 200 {
			t.Fatalf("B stopped authorizing the cookie during A's stop: %d", c)
		}
		if c := davStatus(b.base, "ann", created.Password, 10*time.Second); c != 207 {
			t.Fatalf("B stopped authorizing the app password during A's stop: %d", c)
		}
		select {
		case err := <-a.done:
			if err != nil {
				t.Fatalf("A exited with %v", err)
			}
			if d := time.Since(stopped); d > 25*time.Second {
				t.Fatalf("A took %v to stop", d)
			}
			return
		case <-time.After(500 * time.Millisecond):
		}
		if time.Since(stopped) > 25*time.Second {
			t.Fatal("A did not exit within 25 s")
		}
	}
}

// moveCookies copies c's cookies for one replica to another (one site behind a proxy).
func moveCookies(c *http.Client, from, to string) {
	f, _ := url.Parse(from)
	u, _ := url.Parse(to)
	c.Jar.SetCookies(u, c.Jar.Cookies(f))
}

// Three replicas starting together on an empty schema apply each migration once, create one
// admin and write one initial-admin-password file between them (spec AC29).
func TestConcurrentFirstStart(t *testing.T) {
	requirePostgres(t)
	bin := buildServer(t)
	db := testdb.Config(t)
	var dirs []string
	var procs []*proc
	for range 3 {
		port, dir := freePort(t), t.TempDir()
		dirs = append(dirs, dir)
		procs = append(procs, startProc(t, bin, replicaEnv(db.DSN, port, dir), port))
	}
	for _, p := range procs {
		waitStatus(t, p.base+"/readyz", 200, 60*time.Second)
	}
	conn := pgDB(t, db.DSN)
	var admins, versions, distinct int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'admin'`).Scan(&admins); err != nil || admins != 1 {
		t.Fatalf("%d admins (%v)", admins, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_migrations`).Scan(&versions, &distinct); err != nil || versions != distinct || versions != migrations.LatestVersion() {
		t.Fatalf("schema_migrations: %d rows, %d versions (%v)", versions, distinct, err)
	}
	files := 0
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, initialAdminPasswordFile)); err == nil {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("%d initial-admin-password files, want 1", files)
	}
}

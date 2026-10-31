package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	suiteClientID     = "kycalendar"
	suiteClientSecret = "kycalendar-client-secret-for-tests"
)

// fakeIdentity is KyIdentity's GET /api/suite/peer: it checks KyCalendar's client credentials
// and answers with whatever the test set.
type fakeIdentity struct {
	mu     sync.Mutex
	status int // 0: 200 with url and secret
	url    string
	secret []byte
	calls  atomic.Int32
}

func (f *fakeIdentity) set(status int, url string, secret []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.url, f.secret = status, url, secret
}

func (f *fakeIdentity) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	id, secret, _ := r.BasicAuth()
	if r.URL.Path != "/api/suite/peer" || r.URL.Query().Get("client") != "kypost" || id != suiteClientID || secret != suiteClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.status {
	case 0:
		_ = json.NewEncoder(w).Encode(map[string]string{"client": "kypost", "url": f.url, "secret": base64.StdEncoding.EncodeToString(f.secret)})
	case http.StatusNotFound:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no_peer"}`))
	case http.StatusUnauthorized:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	default:
		w.WriteHeader(f.status)
	}
}

func pairSecret(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// kyIdentityServer starts a fake KyIdentity and points the environment's sign-in at it.
func kyIdentityServer(t *testing.T) (*fakeIdentity, *httptest.Server) {
	t.Helper()
	f := &fakeIdentity{url: "https://mail.example", secret: pairSecret('a')}
	ts := httptest.NewServer(f) // loopback http: suitedir admits it for tests
	t.Cleanup(ts.Close)
	t.Setenv("KY_KYSIGNON_ISSUER", ts.URL)
	t.Setenv("KY_KYSIGNON_CLIENT_ID", suiteClientID)
	t.Setenv("KY_KYSIGNON_SECRET", suiteClientSecret)
	return f, ts
}

// startLinked builds the server the way cmd/server does: sign-in, then the first KyPost lookup.
func startLinked(t *testing.T) (*api.Server, store.Store, *config.Config) {
	t.Helper()
	t.Cleanup(api.SetKyPostTimingForTest(2*time.Second, time.Hour, 20*time.Millisecond, 20*time.Millisecond))
	srv, st, cfg := setupTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	srv.StartKyPost(ctx)
	return srv, st, cfg
}

type kypostStatus struct {
	Connected      bool   `json:"connected"`
	Source         string `json:"source"`
	URL            string `json:"url"`
	Reason         string `json:"reason"`
	CheckedAt      string `json:"checked_at"`
	Scheduling     bool   `json:"scheduling"`
	SchedulingMode string `json:"scheduling_mode"`
}

func kypostStatusOf(t *testing.T, srv *api.Server, admin *http.Cookie) (kypostStatus, string) {
	t.Helper()
	w := do(t, srv, "GET", "/api/admin/kypost", admin)
	if w.Code != 200 {
		t.Fatalf("GET /api/admin/kypost: %d %s", w.Code, w.Body.String())
	}
	var v kypostStatus
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v, w.Body.String()
}

// inbound posts a signed inbound message for a user who does not exist: 404 no_user means the
// route is live and the signature was accepted.
func inbound(t *testing.T, srv http.Handler, secret []byte, id string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(kypost.InboundRequest{User: kypost.User{Sub: "nobody", Issuer: "https://id.example"}})
	h, err := syncauth.Sign(secret, time.Now(), kypost.EventInbound, id, body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/itip/inbound", bytes.NewReader(body))
	h.Apply(r)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKyPostLinkedThroughKyIdentity(t *testing.T) {
	kyIdentityServer(t)
	srv, st, _ := startLinked(t)
	admin := loginAs(t, srv, st, "root", "admin")

	v, raw := kypostStatusOf(t, srv, admin)
	if !v.Connected || v.Source != "kyidentity" || v.URL != "https://mail.example" || !v.Scheduling || v.SchedulingMode != "auto" || v.CheckedAt == "" {
		t.Fatalf("status %+v", v)
	}
	if strings.Contains(raw, base64.StdEncoding.EncodeToString(pairSecret('a'))) || strings.Contains(raw, "aaaa") {
		t.Fatalf("status carries the secret: %s", raw)
	}
	// Inbound takes the pair secret and nothing else.
	if w := inbound(t, srv, pairSecret('a'), "ev-1"); w.Code != 404 || !strings.Contains(w.Body.String(), "no_user") {
		t.Errorf("inbound under the pair secret: %d %s", w.Code, w.Body.String())
	}
	if w := inbound(t, srv, pairSecret('b'), "ev-2"); w.Code != 401 {
		t.Errorf("inbound under another secret: %d", w.Code)
	}
	everyday := loginAs(t, srv, st, "walter", "user")
	if w := do(t, srv, "GET", "/api/admin/kypost", everyday); w.Code != 403 {
		t.Errorf("everyday user reads the KyPost status: %d", w.Code)
	}
}

func TestKyPostEnvironmentWins(t *testing.T) {
	f, _ := kyIdentityServer(t)
	secretFile := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(secretFile, kypostSecret, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", secretFile)
	srv, st, _ := startLinked(t)
	if n := f.calls.Load(); n != 0 {
		t.Errorf("KyIdentity asked %d times with an environment link", n)
	}
	v, _ := kypostStatusOf(t, srv, loginAs(t, srv, st, "root", "admin"))
	if !v.Connected || v.Source != "environment" || v.URL != "https://kypost.example" || !v.Scheduling {
		t.Fatalf("status %+v", v)
	}
	if w := inbound(t, srv, kypostSecret, "ev-1"); w.Code != 404 || !strings.Contains(w.Body.String(), "no_user") {
		t.Errorf("inbound under the environment secret: %d %s", w.Code, w.Body.String())
	}
	if w := inbound(t, srv, pairSecret('a'), "ev-2"); w.Code != 401 {
		t.Errorf("inbound under KyIdentity's secret: %d", w.Code)
	}
}

func TestKyPostLinkFailuresLeaveItOff(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		url    string
		down   bool
		reason string
	}{
		"no peer":      {status: 404, reason: "no_peer"},
		"unauthorized": {status: 401, reason: "unauthorized"},
		"server error": {status: 500, reason: "unreachable"},
		"down":         {down: true, reason: "unreachable"},
		"a path":       {url: "https://mail.example/mail", reason: "invalid_peer"},
	} {
		t.Run(name, func(t *testing.T) {
			f, ts := kyIdentityServer(t)
			f.set(c.status, "https://mail.example", pairSecret('a'))
			if c.url != "" {
				f.set(0, c.url, pairSecret('a'))
			}
			if c.down {
				ts.Close()
			}
			srv, st, _ := startLinked(t)
			v, _ := kypostStatusOf(t, srv, loginAs(t, srv, st, "root", "admin"))
			if v.Connected || v.Reason != c.reason || v.Scheduling || v.URL != "" {
				t.Fatalf("status %+v", v)
			}
			if w := inbound(t, srv, pairSecret('a'), "ev-1"); w.Code != 404 || strings.Contains(w.Body.String(), "no_user") {
				t.Errorf("inbound while unlinked: %d %s", w.Code, w.Body.String())
			}
			if c.down {
				return
			}
			// Retried with backoff: once KyIdentity answers, the link comes up without a restart.
			f.set(0, "https://mail.example", pairSecret('a'))
			waitFor(t, "the link", func() bool { return api.KyPostLinkForTest(srv).Peer() != nil })
			if api.SchedulingOnForTest(srv) {
				t.Error("automatic scheduling turned on after startup")
			}
		})
	}
}

func TestSchedulingModes(t *testing.T) {
	for name, c := range map[string]struct {
		env        string
		sso, peer  bool
		scheduling bool
		reason     string
		lookups    bool
	}{
		"auto, linked":           {sso: true, peer: true, scheduling: true, lookups: true},
		"auto, no peer":          {sso: true, scheduling: false, reason: "no_peer", lookups: true},
		"auto, no KyIdentity":    {scheduling: false, reason: "no_kyidentity"},
		"forced on, no peer":     {env: "true", sso: true, scheduling: true, reason: "no_peer", lookups: true},
		"forced off, KyIdentity": {env: "false", sso: true, peer: true, scheduling: false, reason: "scheduling_off"},
	} {
		t.Run(name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("KYCALENDAR_SCHEDULING", c.env)
			}
			var f *fakeIdentity
			if c.sso {
				f, _ = kyIdentityServer(t)
				if !c.peer {
					f.set(404, "", nil)
				}
			}
			srv, st, _ := startLinked(t)
			v, _ := kypostStatusOf(t, srv, loginAs(t, srv, st, "root", "admin"))
			if v.Scheduling != c.scheduling || v.Reason != c.reason || v.Connected != (c.reason == "") {
				t.Fatalf("status %+v", v)
			}
			if f != nil && (f.calls.Load() > 0) != c.lookups {
				t.Errorf("KyIdentity asked %d times", f.calls.Load())
			}
			var settings struct {
				Scheduling bool `json:"scheduling"`
			}
			w := do(t, srv, "GET", "/api/settings", loginAs(t, srv, st, "walter", "user"))
			if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil || settings.Scheduling != c.scheduling {
				t.Errorf("settings scheduling %v (%v), want %v", settings.Scheduling, err, c.scheduling)
			}
		})
	}
}

// rotatingKyPost accepts sends signed under its current secret only.
type rotatingKyPost struct {
	mu     sync.Mutex
	secret []byte
	sent   []kypost.SendRequest
}

func (k *rotatingKyPost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, err := syncauth.Verify(k.secret, syncauth.FromRequest(r), body, syncauth.Options{}); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Bad signature","code":"bad_signature"}`))
		return
	}
	var req kypost.SendRequest
	_ = json.Unmarshal(body, &req)
	k.sent = append(k.sent, req)
	w.WriteHeader(http.StatusAccepted)
}

func (k *rotatingKyPost) take() []kypost.SendRequest {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := k.sent
	k.sent = nil
	return out
}

func TestKyPostLinkFollowsARotatedSecret(t *testing.T) {
	kp := &rotatingKyPost{secret: pairSecret('b')} // KyPost already holds the new pair secret
	kts := httptest.NewTLSServer(kp)
	defer kts.Close()
	f, _ := kyIdentityServer(t)
	f.set(0, kts.URL, pairSecret('a'))
	srv, st, _ := startLinked(t)
	link := api.KyPostLinkForTest(srv)
	link.HTTP = kts.Client() // the guarded client refuses httptest's loopback address
	ts := httptest.NewServer(srv)
	defer ts.Close()
	token := ssoUser(t, st, "alice")
	cal := "/dav/usr_alice/calendars/default/"

	// The old secret: KyPost refuses it, KyCalendar asks KyIdentity again and finds the new one.
	f.set(0, kts.URL, pairSecret('b'))
	if r := rawDAV(t, ts, "PUT", cal+"r1.ics", "alice", token, meeting("r1", "alice", "ATTENDEE:mailto:carol@example.net"), map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("PUT %d", r.StatusCode)
	}
	srv.DeliverScheduled(context.Background())
	if sent := kp.take(); len(sent) != 0 {
		t.Fatalf("sent under the old secret: %+v", sent)
	}
	waitFor(t, "the rotated secret", func() bool { p := link.Peer(); return p != nil && bytes.Equal(p.Secret, pairSecret('b')) })

	// A new message goes out under the new secret, no restart.
	if r := rawDAV(t, ts, "PUT", cal+"r2.ics", "alice", token, meeting("r2", "alice", "ATTENDEE:mailto:dave@example.net"), map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("PUT %d", r.StatusCode)
	}
	srv.DeliverScheduled(context.Background())
	if sent := kp.take(); len(sent) != 1 || sent[0].Recipients[0] != "dave@example.net" {
		t.Fatalf("sent after rotation: %+v", sent)
	}
	// Inbound follows too.
	if w := inbound(t, srv, pairSecret('b'), "ev-1"); w.Code != 404 || !strings.Contains(w.Body.String(), "no_user") {
		t.Errorf("inbound under the new secret: %d %s", w.Code, w.Body.String())
	}
}

func TestKyPostLinkFollowsASignInChange(t *testing.T) {
	f := &fakeIdentity{url: "https://mail.example", secret: pairSecret('a')}
	ts := httptest.NewServer(f)
	defer ts.Close()
	srv, st, cfg := startLinked(t)
	admin := loginAs(t, srv, st, "root", "admin")
	if v, _ := kypostStatusOf(t, srv, admin); v.Connected || v.Reason != "no_kyidentity" || v.Scheduling {
		t.Fatalf("before sign-in: %+v", v)
	}
	// Sign-in now goes to KyIdentity (what a save on the Sign-in screen does): the link follows.
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.KySignOnSecret = ts.URL, suiteClientID, suiteClientSecret
	if err := srv.LoadSignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { return api.KyPostLinkForTest(srv).Peer() != nil })
	if v, _ := kypostStatusOf(t, srv, admin); !v.Connected || v.Source != "kyidentity" || v.Scheduling {
		t.Fatalf("after sign-in: %+v (scheduling is decided at startup)", v)
	}
	// And unlinks when sign-in is closed.
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.KySignOnSecret = "", "", ""
	if err := srv.LoadSignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the unlink", func() bool { return api.KyPostLinkForTest(srv).Peer() == nil })
}

func TestKyPostLinkKeepsThePrivateAddressRule(t *testing.T) {
	for name, c := range map[string]struct {
		allowPrivate string
		url          string
	}{
		"a private address":            {url: "https://10.255.255.1"},
		"loopback, even allow private": {allowPrivate: "true", url: "https://127.0.0.1:1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("KYCALENDAR_KYPOST_ALLOW_PRIVATE", c.allowPrivate)
			f, _ := kyIdentityServer(t)
			f.set(0, c.url, pairSecret('a'))
			srv, _, _ := startLinked(t)
			link := api.KyPostLinkForTest(srv)
			if p := link.Peer(); p == nil || p.URL != c.url {
				t.Fatalf("peer %+v", p)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := link.Send(ctx, "id-1", kypost.SendRequest{})
			if !errors.Is(err, kypost.ErrRetry) || !strings.Contains(err.Error(), "address refused") {
				t.Fatalf("send to %s: %v", c.url, err)
			}
		})
	}
}

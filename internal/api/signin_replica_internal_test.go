package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Two replicas on one PostgreSQL database. A saves a new issuer while a callback on B is in
// its code exchange under the old provider: B refuses with the usual 400, writes no account
// and issues no session, even for a new subject, and serves the new provider within 10 s
// (spec AC8).
func TestCallbackOnAnotherReplicaAcrossASaveIsRefused(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	a, _ := davInternalServer(t)
	ctx := context.Background()
	stB, err := store.Open(ctx, a.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	b := NewServer(a.config, stB)
	idp := newSigningIdP(t)
	if _, _, err := a.bindAccounts(ctx, kyidentityAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	b.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", idp.Client()))

	next := "https://next.example"
	nextSt := kyidentityAt(next)
	values, err := a.signinValues(nextSt, "sec")
	if err != nil {
		t.Fatal(err)
	}
	req := idp.login(t, b, "sub-new")
	idp.onToken = func() { // A saves while B exchanges the code
		a.signinMu.Lock()
		defer a.signinMu.Unlock()
		if _, _, err := a.commitSignIn(ctx, store.System, nextSt, values); err != nil {
			t.Error(err)
		}
	}
	w := httptest.NewRecorder()
	b.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "start again") {
		t.Fatalf("callback on B across A's save: %d %s, want 400 start again", w.Code, w.Body.String())
	}
	if u, err := stB.Users().GetUserBySSO(ctx, "kysignon", "sub-new"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("B created %+v %v under a binding the database no longer holds", u, err)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("B set cookies %v", w.Result().Cookies())
	}

	followCtx, stop := context.WithCancel(ctx)
	defer stop()
	go b.FollowSignIn(followCtx)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if p := b.signin.Load(); p != nil && p.Binding == "kyidentity "+next {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("B still serves %v 10 s after A saved", b.signin.Load())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// failingReplays is a coordination store whose replay record cannot be read.
type failingReplays struct{ store.CoordinationStore }

func (failingReplays) RecordReplay(context.Context, string, string, time.Duration) (bool, error) {
	return false, errors.New("injected")
}

// A KyPost event ID verified on A is a replay on B; one B forgets (a 429 or 500 it will
// retry) is accepted again on A; a guard that cannot read its record refuses (spec AC7).
func TestKyPostReplayIsSharedAcrossReplicas(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	t.Setenv("KYCALENDAR_SCHEDULING", "true") // NewServer builds the guard, as in production
	a, _ := davInternalServer(t)
	stB, err := store.Open(context.Background(), a.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	b := NewServer(a.config, stB)
	now := time.Now()
	if err := a.kypostReplay.Check("evt-shared", now); err != nil {
		t.Fatal(err)
	}
	if err := b.kypostReplay.Check("evt-shared", now); !errors.Is(err, syncauth.ErrReplay) {
		t.Fatalf("B on A's event: %v, want ErrReplay", err)
	}
	b.kypostReplay.(syncauth.Forgetter).Forget("evt-shared")
	if err := a.kypostReplay.Check("evt-shared", now); err != nil {
		t.Fatalf("a forgotten event on A: %v", err)
	}
	broken := sharedReplay{failingReplays{a.shared}}
	if err := broken.Check("evt-other", now); !errors.Is(err, syncauth.ErrReplayGuardFull) {
		t.Fatalf("a guard that cannot read: %v, want the fail-closed ErrReplayGuardFull", err)
	}
}

// faultyReplays fails every RecordReplay; with commit set, the record is written first, as when
// a commit's answer is lost.
type faultyReplays struct {
	store.CoordinationStore
	commit bool
}

func (f faultyReplays) RecordReplay(ctx context.Context, id, attempt string, window time.Duration) (bool, error) {
	if f.commit {
		if _, err := f.CoordinationStore.RecordReplay(ctx, id, attempt, window); err != nil {
			return false, err
		}
	}
	return false, errors.New("injected")
}

// Replicas linked to KyPost from the environment share the replay guard (spec AC7). A replay
// whose guard check fails must not release the original's record, or the next copy is
// processed twice; an attempt whose own record committed before the failure undoes it, so
// KyPost's retry is processed, not called a duplicate.
func TestFailedReplayCheckReleasesOnlyItsOwnRecord(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	secret := []byte("kypost-test-secret-0123456789abcdef")
	file := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(file, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", file)
	s, _ := davInternalServer(t)
	stB, err := store.Open(context.Background(), s.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	b := NewServer(s.config, stB) // replica B, linked to KyPost from the environment as well
	body := []byte(`{"user":{"sub":"nobody","issuer":"https://id.example"}}`)
	sendTo := func(srv *Server, h syncauth.Headers) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/itip/inbound", strings.NewReader(string(body)))
		h.Apply(r)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	send := func(h syncauth.Headers) *httptest.ResponseRecorder { return sendTo(s, h) }
	sign := func(id string) syncauth.Headers {
		h, err := syncauth.Sign(secret, time.Now(), kypost.EventInbound, id, body)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	real := s.kypostReplay
	accepted := func(w *httptest.ResponseRecorder) bool { return w.Code == http.StatusNotFound } // verified; no such user

	original := sign("evt-original")
	if w := send(original); !accepted(w) {
		t.Fatalf("original: %d %s", w.Code, w.Body)
	}
	if w := sendTo(b, original); !strings.Contains(w.Body.String(), "duplicate") {
		t.Fatalf("A's event on replica B: %d %s, want a duplicate", w.Code, w.Body)
	}
	s.kypostReplay = sharedReplay{faultyReplays{s.shared, false}}
	if w := send(original); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("replay through a failing guard: %d %s, want 503", w.Code, w.Body)
	}
	s.kypostReplay = real
	if w := send(original); !strings.Contains(w.Body.String(), "duplicate") {
		t.Fatalf("the next replay: %d %s; the failed check released the original's record", w.Code, w.Body)
	}

	lost := sign("evt-lost-commit")
	s.kypostReplay = sharedReplay{faultyReplays{s.shared, true}}
	if w := send(lost); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a lost commit: %d %s, want 503", w.Code, w.Body)
	}
	s.kypostReplay = real
	if w := send(lost); !accepted(w) {
		t.Fatalf("KyPost's retry after a lost commit: %d %s, want it processed", w.Code, w.Body)
	}
}

// saveBetween is B's store with A's save run once, right before the account lookup (after
// signinCurrent passed) or right before the session write.
type saveBetween struct {
	store.Store
	save          func()
	beforeLookup  bool
	afterLookup   bool
	beforeSession bool
	once          sync.Once
}

func (s *saveBetween) Users() store.UserStore { return saveUsers{s.Store.Users(), s} }
func (s *saveBetween) Sessions() store.SessionStore {
	return saveSessions{s.Store.Sessions(), s}
}

type saveUsers struct {
	store.UserStore
	s *saveBetween
}

func (u saveUsers) GetUserBySSO(ctx context.Context, provider, subject string) (*store.User, error) {
	if u.s.beforeLookup {
		u.s.once.Do(u.s.save)
	}
	user, err := u.UserStore.GetUserBySSO(ctx, provider, subject)
	if u.s.afterLookup && err == nil {
		u.s.once.Do(u.s.save)
	}
	return user, err
}

type saveSessions struct {
	store.SessionStore
	s *saveBetween
}

func (ss saveSessions) CreateSession(ctx context.Context, sess *store.Session, c store.Credentials) error {
	if ss.s.beforeSession {
		ss.s.once.Do(ss.s.save)
	}
	return ss.SessionStore.CreateSession(ctx, sess, c)
}

// A save on A that lands after B's binding check but before B writes the account, or the
// session, is still refused: the binding is rechecked in the write's own transaction under the
// lock the save holds. Deterministic; both a new subject and an existing account.
func TestSaveBetweenCheckAndWriteIsRefused(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	for _, tc := range []struct {
		name     string
		existing bool
	}{{"new subject", false}, {"existing account", true}} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := davInternalServer(t)
			ctx := context.Background()
			stB, err := store.Open(ctx, a.config.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer stB.Close()
			idp := newSigningIdP(t)
			if _, _, err := a.bindAccounts(ctx, kyidentityAt(idp.URL)); err != nil {
				t.Fatal(err)
			}
			sub := "sub-new"
			if tc.existing {
				sub = "sub-carol"
				createUsers(t, a, &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: sub, SSOIssuer: "kyidentity " + idp.URL})
			}
			// A keeps the provider kind and moves the issuer. Its save disables the old kind's
			// rows, so an existing account would also fail the row check (a 500); the binding
			// recheck answers the usual 400 instead.
			nextSt := kyidentityAt("https://next.example")
			values, err := a.signinValues(nextSt, "sec")
			if err != nil {
				t.Fatal(err)
			}
			hook := &saveBetween{Store: stB, beforeLookup: !tc.existing, beforeSession: tc.existing, save: func() {
				a.signinMu.Lock()
				defer a.signinMu.Unlock()
				if _, _, err := a.commitSignIn(ctx, store.System, nextSt, values); err != nil {
					t.Error(err)
				}
			}}
			b := NewServer(a.config, hook)
			b.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", idp.Client()))
			w := httptest.NewRecorder()
			b.ServeHTTP(w, idp.login(t, b, sub))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "start again") {
				t.Fatalf("callback across a save between check and write: %d %s", w.Code, w.Body.String())
			}
			for _, c := range w.Result().Cookies() {
				if c.Name == "ky_session" && c.Value != "" {
					t.Fatal("a session cookie was set under a binding the database no longer holds")
				}
			}
			if !tc.existing {
				if u, err := stB.Users().GetUserBySSO(ctx, "kysignon", sub); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("an account was created under the old binding: %+v %v", u, err)
				}
			} else {
				var n int
				if err := rawSQL(t, a).QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = 'usr_carol'`).Scan(&n); err != nil || n != 0 {
					t.Fatalf("carol got %d sessions under the old binding (%v)", n, err)
				}
			}
		})
	}
}

// A save disables the old provider's accounts, and SetOIDCEmail has no status check: a save
// landing after B looked the account up must not let the old provider rewrite its email. The
// email and role writes recheck the binding in their own transaction (code review round 2, F3).
func TestSaveBetweenLookupAndAccountWriteIsRefused(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	a, _ := davInternalServer(t)
	ctx := context.Background()
	stB, err := store.Open(ctx, a.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	idp := newSigningIdP(t)
	idp.extra = map[string]any{"email": "new@example.com", "email_verified": true}
	if _, _, err := a.bindAccounts(ctx, oidcAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	createUsers(t, a, &store.User{ID: "usr_dana", Username: "dana", Email: "old@example.com", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "sub-dana", SSOIssuer: oidcAt(idp.URL).Identity()})
	nextSt := kyidentityAt("https://next.example")
	values, err := a.signinValues(nextSt, "sec")
	if err != nil {
		t.Fatal(err)
	}
	hook := &saveBetween{Store: stB, afterLookup: true, save: func() {
		a.signinMu.Lock()
		defer a.signinMu.Unlock()
		if _, _, err := a.commitSignIn(ctx, store.System, nextSt, values); err != nil {
			t.Error(err)
		}
	}}
	b := NewServer(a.config, hook)
	b.signin.Store(sso.NewProvider(sso.KindOIDC, "Acme", idp.URL, "kc", "", idp.Client()))
	w := httptest.NewRecorder()
	b.ServeHTTP(w, idp.login(t, b, "sub-dana"))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "start again") {
		t.Fatalf("callback across a save between lookup and account write: %d %s", w.Code, w.Body.String())
	}
	u, err := stB.Users().GetUserByID(ctx, "usr_dana")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "old@example.com" || u.Role != "user" {
		t.Fatalf("dana is %s %s; the old provider changed the account after the save", u.Email, u.Role)
	}
}

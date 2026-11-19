package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
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

func (failingReplays) RecordReplay(context.Context, string, time.Duration) (bool, error) {
	return false, errors.New("injected")
}

// A KyPost event ID verified on A is a replay on B; one B forgets (a 429 or 500 it will
// retry) is accepted again on A; a guard that cannot read its record refuses (spec AC7).
func TestKyPostReplayIsSharedAcrossReplicas(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	a, _ := davInternalServer(t)
	stB, err := store.Open(context.Background(), a.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	b := NewServer(a.config, stB)
	a.startScheduling()
	b.startScheduling()
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

// saveBetween is B's store with A's save run once, right before the account lookup (after
// signinCurrent passed) or right before the session write.
type saveBetween struct {
	store.Store
	save          func()
	beforeLookup  bool
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
	return u.UserStore.GetUserBySSO(ctx, provider, subject)
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
			// A keeps the provider kind and only moves the issuer, so nothing is disabled: the
			// binding alone must stop B.
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

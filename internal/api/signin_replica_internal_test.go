package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

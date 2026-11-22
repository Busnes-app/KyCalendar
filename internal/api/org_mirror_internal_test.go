package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// orgList answers /api/suite/orgs with orgs (JSON objects, in revision order) and records each
// request's since.
type orgList struct {
	mu     sync.Mutex
	status int
	orgs   string
	since  []string
}

func (l *orgList) serve(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.since = append(l.since, r.URL.Query().Get("since"))
	if l.status != 0 {
		w.WriteHeader(l.status)
		return
	}
	fmt.Fprintf(w, `{"orgs":[%s]}`, l.orgs)
}

func (l *orgList) set(status int, orgs string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status, l.orgs = status, orgs
}

func (l *orgList) requests() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.since...)
}

func serves(t *testing.T, s *Server, org store.OrgID) bool {
	t.Helper()
	ok, err := s.store.OrgServes(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// A new customer's first sign-in lands before the 60 s poll: the unknown org polls once and the
// sign-in waits for it. An org still unknown after the poll is refused, and asking again at once
// does not poll again.
func TestFirstSignInOfANewOrgWaitsForThePoll(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-staff": "active"})
	list := &orgList{orgs: `{"id":"org-new","status":"active","revision":1,"enabled":true,"quotas":{}}`}
	idp.suite = list.serve

	if code := loginWithOrg(t, s, idp, "alice", "org-new"); code != http.StatusFound && code != http.StatusSeeOther {
		t.Fatalf("first sign-in of a new org: %d", code)
	}
	if u, err := s.store.Users().GetUserBySSO(context.Background(), "kysignon", "alice"); err != nil || store.OrgOf(u) != "org-new" {
		t.Fatalf("account: %+v %v", u, err)
	}
	for range 2 {
		if code := loginWithOrg(t, s, idp, "bob", "org-unlisted"); code != http.StatusForbidden {
			t.Fatalf("unlisted org: %d", code)
		}
	}
	if got := list.requests(); fmt.Sprint(got) != "[0 1]" {
		t.Fatalf("polls (since) %v, want one per unknown org", got)
	}
}

// The poll writes KyIdentity's status, so a suspension stops an org serving and a resume brings
// it back; a failed poll changes nothing; each poll starts after the last one's revision.
func TestOrgPollFollowsKyIdentity(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-staff": "active"})
	list := &orgList{}
	idp.suite = list.serve
	ctx := context.Background()
	org := func(status string, rev int) string {
		return fmt.Sprintf(`{"id":"org-a","status":%q,"revision":%d,"enabled":true,"quotas":{}}`, status, rev)
	}

	list.set(0, org("active", 4))
	s.SyncOrgs(ctx)
	if !serves(t, s, "org-a") {
		t.Fatal("an active org does not serve")
	}
	list.set(0, org("suspended", 7))
	s.SyncOrgs(ctx)
	if serves(t, s, "org-a") {
		t.Fatal("a suspended org serves")
	}
	list.set(http.StatusInternalServerError, "")
	s.SyncOrgs(ctx)
	list.set(0, "")
	s.SyncOrgs(ctx)
	if serves(t, s, "org-a") {
		t.Fatal("a failed or empty poll changed the org")
	}
	// An entry without a quotas object fails the whole poll in suitedir, so nothing invalid lands.
	for _, bad := range []string{`{"id":"org-a","status":"active","revision":8,"enabled":true}`, `{"id":"org-a","status":"active","revision":8,"enabled":true,"quotas":null}`} {
		list.set(0, bad)
		s.SyncOrgs(ctx)
	}
	if serves(t, s, "org-a") {
		t.Fatal("an entry without a quotas object was applied")
	}
	list.set(0, org("active", 9))
	s.SyncOrgs(ctx)
	if !serves(t, s, "org-a") {
		t.Fatal("a resumed org does not serve")
	}
	if got := list.requests(); fmt.Sprint(got) != "[0 4 7 7 7 7 7]" {
		t.Fatalf("polls (since) %v", got)
	}
}

// A sign-in that stops waiting does not cancel the poll it started: the org it named is
// mirrored once that poll answers.
func TestAPollOutlivesTheSignInThatStartedIt(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-staff": "active"})
	defer func(w time.Duration) { orgAskWait = w }(orgAskWait)
	orgAskWait = time.Millisecond
	release, answered := make(chan struct{}), 0
	var mu sync.Mutex
	idp.suite = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		answered++
		first := answered == 1
		mu.Unlock()
		if !first {
			w.WriteHeader(http.StatusInternalServerError) // only the first poll can mirror the org
			return
		}
		<-release
		fmt.Fprint(w, `{"orgs":[{"id":"org-new","status":"active","revision":1,"enabled":true,"quotas":{}}]}`)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.awaitOrg(ctx, "org-new")
	cancel() // as a request's context ends with its handler
	if serves(t, s, "org-new") {
		t.Fatal("served before the poll answered")
	}
	close(release)
	s.SyncOrgs(context.Background()) // joins the poll in flight, or runs after it
	if !serves(t, s, "org-new") {
		t.Fatal("the poll was cancelled with the sign-in that stopped waiting")
	}
}

// A single-tenant install has no org list to poll.
func TestSingleTenantNeverPollsOrgs(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", nil)
	if err := s.store.Settings().SetSetting(context.Background(), store.TenancySetting, "single"); err != nil {
		t.Fatal(err)
	}
	list := &orgList{}
	idp.suite = list.serve
	s.SyncOrgs(context.Background())
	if got := list.requests(); len(got) != 0 {
		t.Fatalf("single-tenant polled: %v", got)
	}
}

// A stop ends a poll stuck on KyIdentity: the worker job returns with its context, and
// StopDetached cancels the shared poll so WaitDetached (before the store closes) returns.
func TestAStopEndsAHungPoll(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-staff": "active"})
	reached := make(chan struct{})
	var once sync.Once
	idp.suite = func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(reached) })
		<-r.Context().Done() // hangs until the poll is cancelled
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := make(chan struct{})
	go func() { s.SyncOrgs(ctx); close(job) }()
	<-reached
	c := s.detached.signal()
	c.L.Lock()
	inFlight := s.detached.n
	c.L.Unlock()
	if inFlight != 1 {
		t.Fatalf("%d detached tasks during a poll, want 1: the store could close under it", inFlight)
	}
	cancel()
	select {
	case <-job:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker job ignored its cancelled context")
	}
	s.StopDetached()
	waited := make(chan struct{})
	go func() { s.WaitDetached(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop waited on a hung poll")
	}
}

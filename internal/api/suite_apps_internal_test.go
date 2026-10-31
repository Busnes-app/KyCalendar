package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// suiteIdP is KyIdentity's suite directory: it answers each sub with its own app, or fails.
type suiteIdP struct {
	*httptest.Server
	mu   sync.Mutex
	subs []string
	fail bool
}

func newSuiteIdP(t *testing.T) *suiteIdP {
	idp := &suiteIdP{}
	idp.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, secret, ok := r.BasicAuth(); r.URL.Path != "/api/suite/apps" || !ok || id != "kycalendar" || secret != "suite-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		sub := r.URL.Query().Get("sub")
		idp.mu.Lock()
		idp.subs = append(idp.subs, sub)
		fail := idp.fail
		idp.mu.Unlock()
		if fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, `{"apps":[{"id":"kycalendar","name":"KyCalendar","url":"https://cal.example.com/api/sso/kysignon/login"},{"id":"app-%s","name":"App %s","url":"https://%s.example.com/"}]}`, sub, sub, sub)
	}))
	t.Cleanup(idp.Close)
	return idp
}

func (idp *suiteIdP) calls() []string {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	return append([]string(nil), idp.subs...)
}

func (idp *suiteIdP) provider() *sso.Provider {
	return sso.NewProvider(sso.KindKyIdentity, "single sign-on", idp.URL, "kycalendar", "suite-secret", idp.Client())
}

func ssoUser(id, sub string, p *sso.Provider) *store.User {
	return &store.User{ID: id, Username: id, Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: sub, SSOIssuer: p.Binding}
}

// suiteApps calls GET /api/suite/apps as u and returns the app IDs.
func suiteApps(t *testing.T, s *Server, u *store.User) []string {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleSuiteApps(w, withSessionUser(httptest.NewRequest(http.MethodGet, "/api/suite/apps", nil), u, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Apps []struct{ ID, Name, URL string } `json:"apps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Apps == nil {
		t.Fatalf("body %s: %v", w.Body, err)
	}
	ids := []string{}
	for _, a := range body.Apps {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestSuiteAppsForAKyIdentityUser(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := newSuiteIdP(t)
	p := idp.provider()
	s.signin.Store(p)
	got := suiteApps(t, s, ssoUser("usr_ann", "sub-ann", p))
	if fmt.Sprint(got) != "[kycalendar app-sub-ann]" {
		t.Fatalf("apps %v", got)
	}
	// A SCIM-provisioned account the KyIdentity sign-in took over is the same person.
	scim := ssoUser("usr_sam", "sub-sam", p)
	scim.SSOProvider = "scim"
	if got := suiteApps(t, s, scim); len(got) != 2 {
		t.Fatalf("scim account apps %v", got)
	}
	if fmt.Sprint(idp.calls()) != "[sub-ann sub-sam]" {
		t.Fatalf("KyIdentity saw %v", idp.calls())
	}
}

func TestSuiteAppsEmptyWithoutAKyIdentityAccount(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := newSuiteIdP(t)
	p := idp.provider()
	local := &store.User{ID: "usr_loc", Username: "loc", Role: "user", Status: "active", SSOProvider: "local"}
	// No sign-in provider at all.
	if got := suiteApps(t, s, local); len(got) != 0 {
		t.Fatalf("no provider: %v", got)
	}
	s.signin.Store(p)
	other := ssoUser("usr_old", "sub-old", p)
	other.SSOIssuer = sso.KindKyIdentity + " https://old.example.com"
	oidcRow := ssoUser("usr_oidc", "sub-oidc", p)
	oidcRow.SSOProvider = "oidc"
	for _, u := range []*store.User{local, other, oidcRow} {
		if got := suiteApps(t, s, u); len(got) != 0 {
			t.Fatalf("%s: %v", u.ID, got)
		}
	}
	// Another OIDC provider has no suite directory, even for its own accounts.
	op := sso.NewProvider(sso.KindOIDC, "Other", idp.URL, "kycalendar", "suite-secret", idp.Client())
	s.signin.Store(op)
	oidcRow.SSOIssuer = op.Binding
	if got := suiteApps(t, s, oidcRow); len(got) != 0 {
		t.Fatalf("oidc provider: %v", got)
	}
	if calls := idp.calls(); len(calls) != 0 {
		t.Fatalf("KyIdentity was asked: %v", calls)
	}
}

func TestSuiteAppsEmptyWhenKyIdentityFails(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := newSuiteIdP(t)
	idp.mu.Lock()
	idp.fail = true
	idp.mu.Unlock()
	p := idp.provider()
	s.signin.Store(p)
	if got := suiteApps(t, s, ssoUser("usr_ann", "sub-ann", p)); len(got) != 0 {
		t.Fatalf("apps %v", got)
	}
	// Refused credentials too.
	bad := sso.NewProvider(sso.KindKyIdentity, "single sign-on", idp.URL, "kycalendar", "wrong", idp.Client())
	s.signin.Store(bad)
	if got := suiteApps(t, s, ssoUser("usr_bob", "sub-bob", bad)); len(got) != 0 {
		t.Fatalf("apps %v", got)
	}
}

func TestSuiteAppsCachedPerUser(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := newSuiteIdP(t)
	p := idp.provider()
	s.signin.Store(p)
	ann, bob := ssoUser("usr_ann", "sub-ann", p), ssoUser("usr_bob", "sub-bob", p)
	for range 3 {
		if got := suiteApps(t, s, ann); fmt.Sprint(got) != "[kycalendar app-sub-ann]" {
			t.Fatalf("ann %v", got)
		}
	}
	// Bob never gets Ann's cached list.
	if got := suiteApps(t, s, bob); fmt.Sprint(got) != "[kycalendar app-sub-bob]" {
		t.Fatalf("bob %v", got)
	}
	if fmt.Sprint(idp.calls()) != "[sub-ann sub-bob]" {
		t.Fatalf("KyIdentity saw %v", idp.calls())
	}
	// A failure is cached as well, so an outage is not asked again on every page load.
	idp.mu.Lock()
	idp.fail = true
	idp.mu.Unlock()
	carl := ssoUser("usr_carl", "sub-carl", p)
	suiteApps(t, s, carl)
	suiteApps(t, s, carl)
	if n := len(idp.calls()); n != 3 {
		t.Fatalf("%d calls after a cached failure", n)
	}
	idp.mu.Lock()
	idp.fail = false
	idp.mu.Unlock()
	// A new sign-in provider never serves the old one's list.
	p2 := idp.provider()
	s.signin.Store(p2)
	ann.SSOIssuer = p2.Binding
	suiteApps(t, s, ann)
	if n := len(idp.calls()); n != 4 {
		t.Fatalf("%d calls after a provider change", n)
	}
	// Expired entries are fetched again.
	old := suiteAppsTTL
	suiteAppsTTL = -time.Second
	t.Cleanup(func() { suiteAppsTTL = old })
	suiteApps(t, s, bob)
	suiteApps(t, s, bob)
	if fmt.Sprint(idp.calls()[4:]) != "[sub-bob sub-bob]" {
		t.Fatalf("KyIdentity saw %v", idp.calls())
	}
}

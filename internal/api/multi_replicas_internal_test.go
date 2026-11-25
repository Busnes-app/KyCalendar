package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Tenancy spec step 6: two multi-tenant servers on one PostgreSQL database, sharing nothing
// in memory. A sign-in on one is a session on the other; a suspension the elected worker polls
// (SyncOrgs, the job it runs) refuses both at their next request; and the password sign-in
// limit admits its value once across both. The DAV-failure and invite limits are the same
// shared windows, proven across replicas by TestLimitsAreSharedAcrossReplicas and
// TestInviteLimitIsOneWindowAcrossReplicas; none of them reads the mode.
const staffPassword = "StaffPassword-123!"

func TestTwoMultiTenantReplicasShareOneDatabase(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	a, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-staff": "active"})
	a.config.Captcha.Provider = "none" // password sign-in below; the PoW puzzle is not the subject
	cfg := *a.config
	cfg.Database.DataDir = t.TempDir()
	st, err := store.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := NewServer(&cfg, st)
	t.Cleanup(b.StopDetached)
	b.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", idp.Client()))

	idp.extra, idp.roles = map[string]any{"org_id": "org-a"}, nil
	session := ssoSession(ssoLogin(t, a, idp, "alice", "sid-alice"))
	if session == nil || !signedIn(a, session) || !signedIn(b, session) {
		t.Fatal("a sign-in on A is not a session on B")
	}

	list := &orgList{orgs: `{"id":"org-a","status":"suspended","revision":2,"enabled":true,"quotas":{}}`}
	idp.suite = list.serve
	a.SyncOrgs(context.Background())
	if signedIn(a, session) || signedIn(b, session) {
		t.Fatal("a suspension polled on A did not refuse both servers")
	}

	// The bootstrap-style break-glass account: ten failures across both, then even the right
	// password is refused on each.
	hash, err := password.Hash(staffPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.Users().CreateUser(context.Background(), &store.User{ID: "usr_breakglass", Username: "breakglass", PasswordHash: hash,
		Role: "admin", Status: "active", SSOProvider: "local", OrgID: "org-staff"}); err != nil {
		t.Fatal(err)
	}
	login := func(s *Server, pw string) int {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(fmt.Sprintf(`{"username":"breakglass","password":%q}`, pw)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "198.51.100.40:4000"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if code := login(b, staffPassword); code != http.StatusOK {
		t.Fatalf("control: the staff break-glass sign-in on B: %d", code)
	}
	for i := range 10 {
		_ = login([]*Server{a, b}[i%2], "WrongPass123!")
	}
	for name, s := range map[string]*Server{"A": a, "B": b} {
		if code := login(s, staffPassword); code != http.StatusUnauthorized && code != http.StatusTooManyRequests {
			t.Errorf("%s: the right password after ten failures across both: %d", name, code)
		}
	}
}

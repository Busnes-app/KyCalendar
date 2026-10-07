package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// fakeIdP serves discovery and a token endpoint that returns no id_token.
func fakeIdP(t *testing.T) *httptest.Server {
	t.Helper()
	var idp *httptest.Server
	idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" && r.FormValue("code") == "leak" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "leaked-detail-10.9.8.7"})
			return
		}
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "Bearer"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize",
			"token_endpoint": idp.URL + "/token", "jwks_uri": idp.URL + "/keys",
		})
	}))
	t.Cleanup(idp.Close)
	return idp
}

// startLogin runs the login route and returns a callback request carrying its state and cookies.
func startLogin(t *testing.T, s *Server) *http.Request {
	t.Helper()
	login := httptest.NewRecorder()
	s.ServeHTTP(login, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	loc, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/sso/kysignon/callback?code=c&state="+loc.Query().Get("state"), nil)
	for _, c := range login.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

// A login started under one provider build fails once if a save swaps the provider before the
// callback, even to identical settings: the nonce cookie carries the build's ID.
func TestCallbackAfterProviderSwapFailsOnce(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := fakeIdP(t)
	s.signin.Store(sso.NewProvider(sso.KindOIDC, "Acme", idp.URL, "kc", "", nil))
	req := startLogin(t, s)

	s.signin.Store(sso.NewProvider(sso.KindOIDC, "Acme", idp.URL, "kc", "", nil))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "start again") {
		t.Fatalf("callback across a swap: %d %s", w.Code, w.Body.String())
	}

	s.signin.Store(nil)
	none := httptest.NewRecorder()
	s.ServeHTTP(none, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if none.Code != http.StatusNotFound {
		t.Fatalf("login with no provider: %d, want 404", none.Code)
	}
}

// NewServer makes no provider live, even one the environment names: LoadSignIn does that after
// binding accounts, so a failed binding leaves sign-in closed.
func TestNewServerLeavesSignInClosed(t *testing.T) {
	t.Setenv("KY_KYSIGNON_ISSUER", "https://idp.example")
	t.Setenv("KY_KYSIGNON_CLIENT_ID", "client")
	s, _ := davInternalServer(t)
	if s.config.SSO.KySignOnIssuer == "" {
		t.Fatal("the environment issuer did not reach the config")
	}
	if p := s.signin.Load(); p != nil {
		t.Fatalf("NewServer stored a provider: %+v", p)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("login before LoadSignIn: %d, want 404", w.Code)
	}
}

type recordingTransport struct {
	mu    sync.Mutex
	paths []string
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.paths = append(rt.paths, r.URL.Path)
	rt.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

func signinSettings(source, issuer string) sso.Settings {
	return sso.Settings{
		Provider: sso.Field{Value: sso.KindOIDC, Source: source},
		Issuer:   sso.Field{Value: issuer, Source: source},
		ClientID: sso.Field{Value: "kc", Source: source},
	}
}

// An admin-entered issuer is reached only through s.signinHTTP: discovery and the code exchange
// both. An environment issuer keeps the default transport.
func TestSavedIssuerGoesThroughTheGuardedClient(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := fakeIdP(t)
	rt := &recordingTransport{}
	s.signinHTTP = &http.Client{Transport: rt}

	s.signin.Store(s.buildProvider(signinSettings(sso.SourceSaved, idp.URL)))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, startLogin(t, s))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("callback with no id_token: %d %s, want 401", w.Code, w.Body.String())
	}
	if !slices.Contains(rt.paths, "/.well-known/openid-configuration") || !slices.Contains(rt.paths, "/token") {
		t.Fatalf("saved issuer requests through signinHTTP: %v, want discovery and /token", rt.paths)
	}

	rt.paths = nil
	s.signin.Store(s.buildProvider(signinSettings(sso.SourceEnvironment, idp.URL)))
	w = httptest.NewRecorder()
	s.ServeHTTP(w, startLogin(t, s))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("environment issuer callback: %d %s, want 401", w.Code, w.Body.String())
	}
	if len(rt.paths) != 0 {
		t.Fatalf("environment issuer went through signinHTTP: %v", rt.paths)
	}

	if s.buildProvider(signinSettings(sso.SourceSaved, "")) != nil {
		t.Fatal("settings that are not live built a provider")
	}
}

// The guarded client's address policy refuses a saved issuer on loopback; with the policy
// relaxed the same login goes through, so the refusal is the policy's.
func TestSavedLoopbackIssuerIsRefused(t *testing.T) {
	s, _ := davInternalServer(t)
	var idp *httptest.Server
	idp = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize",
			"token_endpoint": idp.URL + "/token", "jwks_uri": idp.URL + "/keys",
		})
	}))
	t.Cleanup(idp.Close)
	roots := x509.NewCertPool()
	roots.AddCert(idp.Certificate())
	for _, tc := range []struct {
		policy sso.AddrPolicy
		want   int
	}{
		{sso.RefuseLocal, http.StatusBadGateway},
		{func(netip.Addr) error { return nil }, http.StatusFound},
	} {
		s.signinHTTP = sso.NewGuardedClient(tc.policy, roots)
		s.signin.Store(s.buildProvider(signinSettings(sso.SourceSaved, idp.URL)))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
		if w.Code != tc.want {
			t.Fatalf("login against a loopback saved issuer: %d %s, want %d", w.Code, w.Body.String(), tc.want)
		}
	}
}

// A failed code exchange answers a generic 401: the token endpoint's error text and transport
// errors stay in the log, out of reach of anyone who posts a junk code.
func TestCallbackExchangeErrorIsGeneric(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := fakeIdP(t)
	s.signin.Store(sso.NewProvider(sso.KindOIDC, "Acme", idp.URL, "kc", "", nil))
	for _, code := range []string{"leak", "c"} {
		req := startLogin(t, s)
		q := req.URL.Query()
		q.Set("code", code)
		req.URL.RawQuery = q.Encode()
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		body := w.Body.String()
		if w.Code != http.StatusUnauthorized || !strings.Contains(body, "Sign-in failed") ||
			strings.Contains(body, "leaked-detail") || strings.Contains(body, "invalid_grant") || strings.Contains(body, "id_token") {
			t.Fatalf("code %q: %d %s, want a generic 401", code, w.Code, body)
		}
	}
}

// An oidc account stored as admin (from before the provider rules) is demoted at its next login.
func TestOIDCLoginDemotesAStoredAdmin(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if err := s.store.Users().CreateUser(ctx, &store.User{ID: "usr_olga", Username: "olga", Role: "admin", Status: "active", SSOProvider: "oidc", SSOSubject: "o-9", SSOIssuer: "oidc https://acme.example"}); err != nil {
		t.Fatal(err)
	}
	u, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "oidc", Subject: "o-9", PreferredUsername: "olga", Roles: []string{access.AdminAppRole}}, "oidc https://acme.example")
	if err != nil || u.ID != "usr_olga" || u.Role != "user" {
		t.Fatalf("oidc login of a stored admin: %+v %v, want usr_olga demoted to user", u, err)
	}
	stored, err := s.store.Users().GetUserByID(ctx, "usr_olga")
	if err != nil || stored.Role != "user" {
		t.Fatalf("stored role: %+v %v, want user", stored, err)
	}
}

// An oidc provider never makes an administrator and never signs in as a SCIM row.
func TestOIDCLoginsAreEverydayAndNeverAdoptSCIM(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if err := s.store.Users().CreateUser(ctx, &store.User{ID: "usr_scim", Username: "sam", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "o-2"}); err != nil {
		t.Fatal(err)
	}
	u, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "oidc", Subject: "o-1", PreferredUsername: "olive", Roles: []string{access.AdminAppRole}}, "oidc https://acme.example")
	if err != nil || u.Role != "user" || u.SSOProvider != "oidc" {
		t.Fatalf("oidc login with the admin role: %+v %v, want an everyday oidc user", u, err)
	}
	u, err = s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "oidc", Subject: "o-2", PreferredUsername: "oscar"}, "oidc https://acme.example")
	if err != nil || u.ID == "usr_scim" {
		t.Fatalf("oidc sub matching a SCIM row: %+v %v, want a separate account", u, err)
	}
}

// The test route reaches an environment issuer with a bounded client that never follows a
// redirect.
func TestEnvironmentIssuerClientIsBoundedAndRefusesRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			t.Error("the redirect was followed")
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(target.Close)
	if envIssuerHTTP.Timeout != 20*time.Second {
		t.Fatalf("timeout %v, want 20s", envIssuerHTTP.Timeout)
	}
	resp, err := envIssuerHTTP.Get(target.URL + "/.well-known/openid-configuration")
	if err == nil {
		resp.Body.Close()
		t.Fatal("redirect not refused")
	}
}

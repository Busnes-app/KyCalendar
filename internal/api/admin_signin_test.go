package api_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// tlsIdP is an httptest TLS server whose discovery document names itself as the issuer.
func tlsIdP(t *testing.T) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": ts.URL, "authorization_endpoint": ts.URL + "/authorize", "token_endpoint": ts.URL + "/token",
			"jwks_uri": ts.URL + "/keys", "code_challenge_methods_supported": []string{"S256"},
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// trustIdPs lets srv reach httptest TLS servers, which all share one certificate and listen on
// loopback (refused by the production policy).
func trustIdPs(srv *api.Server, ts *httptest.Server) {
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	api.SetSignInHTTPClientForTest(srv, sso.NewGuardedClient(func(netip.Addr) error { return nil }, pool))
}

func startLogin(t *testing.T, srv *api.Server) *url.URL {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func saveSignIn(t *testing.T, srv *api.Server, admin *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, srv, "PUT", "/api/admin/signin", body, admin)
}

func oidcBody(issuer, extra string) string {
	return `{"provider":"oidc","display_name":"Acme","issuer":"` + issuer + `","client_id":"kc"` + extra + `}`
}

func setting(t *testing.T, st store.Store, key string) string {
	t.Helper()
	v, err := st.Settings().GetSetting(context.Background(), key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	return v
}

func TestSignInSaveSealsTheSecretAndSwapsLive(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	a, b := tlsIdP(t), tlsIdP(t)
	trustIdPs(srv, a)

	if w := call(t, srv, "GET", "/api/sso/kysignon/login", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("login with no provider: %d, want 404", w.Code)
	}
	if bytes.Contains(call(t, srv, "GET", "/api/settings", "", nil).Body.Bytes(), []byte("signin_name")) {
		t.Fatal("the login button is offered with no provider")
	}

	if w := saveSignIn(t, srv, admin, oidcBody(a.URL, `,"client_secret":"s3cret-value"`)); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	sealed := setting(t, st, sso.KeySecretSealed)
	if sealed == "" || strings.Contains(sealed, "s3cret") {
		t.Fatalf("stored secret %q, want it sealed", sealed)
	}
	if loc := startLogin(t, srv); loc.Scheme+"://"+loc.Host != a.URL || loc.Path != "/authorize" || loc.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("login goes to %s", loc)
	}
	if w := call(t, srv, "GET", "/api/settings", "", nil); !bytes.Contains(w.Body.Bytes(), []byte(`"signin_name":"Acme"`)) {
		t.Fatalf("public settings: %s", w.Body.String())
	}
	for _, path := range []string{"/api/settings", "/api/admin/signin"} {
		body := call(t, srv, "GET", path, "", admin).Body.Bytes()
		if bytes.Contains(body, []byte("s3cret")) || bytes.Contains(body, []byte(sealed)) || bytes.Contains(body, []byte(sso.KeySecretSealed)) {
			t.Errorf("%s leaked the client secret: %s", path, body)
		}
	}
	if w := call(t, srv, "GET", "/api/admin/signin", "", admin); !bytes.Contains(w.Body.Bytes(), []byte(`"client_secret":{"set":true,"source":"saved"}`)) {
		t.Errorf("admin view: %s", w.Body.String())
	}

	// Same provider, issuer and client: a blank secret keeps the sealed one.
	if w := saveSignIn(t, srv, admin, `{"provider":"oidc","display_name":"Acme Two","issuer":"`+a.URL+`/","client_id":"kc"}`); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a trailing slash the provider does not name: %d %s", w.Code, w.Body.String())
	}
	if w := saveSignIn(t, srv, admin, `{"provider":"oidc","display_name":"Acme Two","issuer":"`+a.URL+`","client_id":"kc"}`); w.Code != http.StatusOK {
		t.Fatalf("relabel: %d %s", w.Code, w.Body.String())
	}
	if setting(t, st, sso.KeySecretSealed) != sealed {
		t.Error("a blank secret replaced the stored one")
	}
	if w := call(t, srv, "GET", "/api/settings", "", nil); !bytes.Contains(w.Body.Bytes(), []byte(`"signin_name":"Acme Two"`)) {
		t.Fatalf("the relabel is not live: %s", w.Body.String())
	}

	// Another issuer or client never gets the stored secret: a new one is required.
	for _, body := range []string{oidcBody(b.URL, ""), `{"provider":"oidc","display_name":"Acme","issuer":"` + a.URL + `","client_id":"other"}`} {
		if w := saveSignIn(t, srv, admin, body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "client secret") {
			t.Fatalf("%s with a blank secret: %d %s", body, w.Code, w.Body.String())
		}
	}
	if setting(t, st, sso.KeySecretSealed) != sealed || setting(t, st, sso.KeyIssuer) != a.URL {
		t.Fatal("a refused save changed the settings")
	}
	if w := saveSignIn(t, srv, admin, oidcBody(b.URL, `,"client_secret":"b-secret"`)); w.Code != http.StatusOK {
		t.Fatalf("save b: %d %s", w.Code, w.Body.String())
	}
	if loc := startLogin(t, srv); loc.Scheme+"://"+loc.Host != b.URL {
		t.Fatalf("after the swap the login goes to %s", loc)
	}
	again := setting(t, st, sso.KeySecretSealed)
	if again == sealed || again == "" {
		t.Error("b's secret was not stored")
	}
	if n := len(auditDetails(t, st, "admin.signin_save")); n != 4 {
		t.Errorf("want 4 admin.signin_save rows (three saves, one refusal), got %d", n)
	}
	for _, d := range auditDetails(t, st, "admin.signin_save") {
		if strings.Contains(d, "secret") {
			t.Errorf("audit carries the secret: %s", d)
		}
	}

	cfg.SSO.Enabled = false
	if bytes.Contains(call(t, srv, "GET", "/api/settings", "", nil).Body.Bytes(), []byte("signin_name")) {
		t.Error("KY_SSO_ENABLED=false must hide the login button")
	}
	cfg.SSO.Enabled = true

	// none closes sign-in and drops the secret.
	if w := saveSignIn(t, srv, admin, `{"provider":"none","issuer":"http://ignored","client_id":"x"}`); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"client_secret":{"set":false,"source":"unset"}`) || !strings.Contains(w.Body.String(), `"live":false`) {
		t.Fatalf("save none: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/sso/kysignon/login", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("login after none: %d", w.Code)
	}
	for _, key := range []string{sso.KeySecretSealed, sso.KeyIssuer, sso.KeyClientID} {
		if v := setting(t, st, key); v != "" {
			t.Errorf("%s = %q after none", key, v)
		}
	}
}

func TestSignInProviderChangeNeedsConfirmationAndDisables(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	a, b := tlsIdP(t), tlsIdP(t)
	trustIdPs(srv, a)
	if w := saveSignIn(t, srv, admin, `{"provider":"kyidentity","issuer":"`+a.URL+`","client_id":"kc","client_secret":"a-secret"}`); w.Code != http.StatusOK {
		t.Fatalf("save kyidentity: %d %s", w.Code, w.Body.String())
	}
	carol := &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-1"}
	if err := st.Users().CreateUser(ctx, carol); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: "ap_carol", UserID: carol.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_carol", OwnerKind: "user", OwnerID: carol.ID, Slug: "home", Name: "Home"}, 0); err != nil {
		t.Fatal(err)
	}

	for _, extra := range []string{`,"client_secret":"b-secret"`, `,"client_secret":"b-secret","confirm_disable":2`} {
		w := saveSignIn(t, srv, admin, oidcBody(b.URL, extra))
		var refusal struct {
			Code  string `json:"code"`
			Count int    `json:"count"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &refusal)
		if w.Code != http.StatusConflict || refusal.Code != "confirm_provider_change" || refusal.Count != 1 {
			t.Fatalf("unconfirmed change (%s): %d %s", extra, w.Code, w.Body.String())
		}
	}
	if v := setting(t, st, sso.KeyDisplayName); v != "" {
		t.Fatalf("the default KyIdentity label was stored as %q", v)
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.Status != "active" {
		t.Fatal("an unconfirmed change disabled an account")
	}
	if setting(t, st, sso.KeyProvider) != sso.KindKyIdentity {
		t.Fatal("an unconfirmed change saved settings")
	}

	if w := saveSignIn(t, srv, admin, oidcBody(b.URL, `,"client_secret":"b-secret","confirm_disable":1`)); w.Code != http.StatusOK {
		t.Fatalf("confirmed change: %d %s", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.Status != "inactive" {
		t.Fatalf("previous provider's account still %s", u.Status)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, carol.ID); len(list) != 0 {
		t.Fatal("the disabled account kept its app passwords")
	}
	if cals, err := st.Calendars().ListCalendarsByOwner(ctx, "user", carol.ID); err != nil || len(cals) != 1 || cals[0].ID != "cal_carol" {
		t.Fatalf("calendars must stay: %v %v", cals, err)
	}
	if d := auditDetails(t, st, "admin.signin_provider_change"); len(d) != 1 || !strings.Contains(d[0], "disabled=1") {
		t.Fatalf("provider change audit: %v", d)
	}
	if loc := startLogin(t, srv); loc.Scheme+"://"+loc.Host != b.URL {
		t.Fatalf("login goes to %s", loc)
	}
}

// failBind fails the one sign-in transaction, as a store error would after its rollback.
type failBind struct{ store.UserStore }

func (failBind) BindSignIn(context.Context, store.Actor, []string, map[string]string) (int, error) {
	return 0, errors.New("disk full")
}

// A save whose transaction fails changes nothing and leaves sign-in closed, never the old
// provider live; a retry finishes it.
func TestSignInFailedSaveClosesSignIn(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	a, b := tlsIdP(t), tlsIdP(t)
	trustIdPs(srv, a)
	if w := saveSignIn(t, srv, admin, `{"provider":"kyidentity","issuer":"`+a.URL+`","client_id":"kc","client_secret":"a"}`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	carol := &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-1"}
	if err := st.Users().CreateUser(ctx, carol); err != nil {
		t.Fatal(err)
	}

	api.SetStoreForTest(srv, revokingStore{Store: st, users: failBind{st.Users()}})
	if w := saveSignIn(t, srv, admin, oidcBody(b.URL, `,"client_secret":"b","confirm_disable":1`)); w.Code != http.StatusInternalServerError {
		t.Fatalf("failing save: %d %s", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.Status != "active" {
		t.Fatalf("a failed save disabled carol: %s", u.Status)
	}
	if w := call(t, srv, "GET", "/api/sso/kysignon/login", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("sign-in after a failed save: %d, want closed", w.Code)
	}
	if setting(t, st, sso.KeyIssuer) != a.URL || setting(t, st, sso.KeyProvider) != sso.KindKyIdentity {
		t.Fatal("a failed save wrote part of the settings")
	}

	// Nothing was bound, so the retry asks for the same confirmation and then disables.
	api.SetStoreForTest(srv, st)
	if w := saveSignIn(t, srv, admin, oidcBody(b.URL, `,"client_secret":"b","confirm_disable":1`)); w.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.Status != "inactive" {
		t.Fatalf("after the retry carol is %s", u.Status)
	}
	if loc := startLogin(t, srv); loc.Scheme+"://"+loc.Host != b.URL {
		t.Fatalf("login goes to %s", loc)
	}
}

// revokeOnBind signs the acting admin out between authorisation and the sign-in transaction.
type revokeOnBind struct {
	store.UserStore
	revoke func()
}

func (u revokeOnBind) BindSignIn(ctx context.Context, a store.Actor, disable []string, settings map[string]string) (int, error) {
	u.revoke()
	return u.UserStore.BindSignIn(ctx, a, disable, settings)
}

func TestSignInSaveRechecksTheActor(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	a := tlsIdP(t)
	trustIdPs(srv, a)
	api.SetStoreForTest(srv, revokingStore{Store: st, users: revokeOnBind{UserStore: st.Users(), revoke: func() {
		if err := st.Sessions().DeleteUserSessions(ctx, "usr_root"); err != nil {
			t.Fatal(err)
		}
	}}})
	w := saveSignIn(t, srv, admin, oidcBody(a.URL, `,"client_secret":"x"`))
	if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "actor_revoked" {
		t.Fatalf("revoked actor: %d %s", w.Code, w.Body.String())
	}
	for _, key := range []string{sso.KeyProvider, sso.KeyBound, sso.KeySecretSealed} {
		if v := setting(t, st, key); v != "" {
			t.Errorf("%s = %q written by a revoked admin", key, v)
		}
	}
}

func TestSignInRejectsInvalidValues(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	a := tlsIdP(t)
	trustIdPs(srv, a)
	long := strings.Repeat("x", 256)
	for _, body := range []string{
		`{"issuer":"` + a.URL + `","client_id":"kc","client_secret":"s"}`,
		`{}`,
		`{"provider":"saml","issuer":"` + a.URL + `","client_id":"kc","client_secret":"s"}`,
		`{"provider":"oidc","issuer":"` + a.URL + `","client_id":"kc","client_secret":"s"}`,
		`{"provider":"oidc","display_name":"A\u0007","issuer":"` + a.URL + `","client_id":"kc","client_secret":"s"}`,
		`{"provider":"kyidentity","issuer":"` + a.URL + `","client_id":"k\nc","client_secret":"s"}`,
		`{"provider":"kyidentity","issuer":"` + a.URL + `","client_id":"` + long + `","client_secret":"s"}`,
		`{"provider":"kyidentity","issuer":"` + a.URL + `/` + strings.Repeat("p", 2048) + `","client_id":"kc","client_secret":"s"}`,
		`{"provider":"kyidentity","issuer":"` + a.URL + `","client_id":"kc","client_secret":"` + strings.Repeat("s", 1025) + `"}`,
		`{"provider":"kyidentity","issuer":"","client_id":"kc","client_secret":"s"}`,
		`{"provider":"kyidentity","issuer":"` + a.URL + `","client_id":"kc"}`,
	} {
		if w := saveSignIn(t, srv, admin, body); w.Code != http.StatusBadRequest {
			t.Errorf("%.80s: %d %s, want 400", body, w.Code, w.Body.String())
		}
	}
	for _, issuer := range []string{a.URL + "?q=1", "https://user@" + strings.TrimPrefix(a.URL, "https://"), a.URL + "#f"} {
		if w := saveSignIn(t, srv, admin, oidcBody(issuer, `,"client_secret":"s"`)); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 422", issuer, w.Code, w.Body.String())
		}
	}
	if v := setting(t, st, sso.KeyProvider); v != "" {
		t.Fatalf("an invalid save stored provider %q", v)
	}
}

func TestSignInEnvironmentLocksFields(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	a := tlsIdP(t)
	trustIdPs(srv, a)
	// A provider saved before the environment was set.
	if err := st.Settings().SetSetting(context.Background(), sso.KeyProvider, sso.KindOIDC); err != nil {
		t.Fatal(err)
	}
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.KySignOnSecret = a.URL, "env-client", "env-secret"

	w := call(t, srv, "GET", "/api/admin/signin", "", admin)
	for _, want := range []string{
		`"provider":{"value":"kyidentity","source":"environment"}`,
		`"issuer":{"value":"` + a.URL + `","source":"environment"}`,
		`"client_secret":{"set":true,"source":"environment"}`,
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("admin view lacks %s: %s", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "env-secret") {
		t.Fatal("the admin view leaked the environment secret")
	}

	body := `{"provider":"oidc","display_name":"Mine","issuer":"https://evil.example","client_id":"evil","client_secret":"evil-secret"}`
	if w := saveSignIn(t, srv, admin, body); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	for _, key := range []string{sso.KeyProvider, sso.KeyIssuer, sso.KeyClientID, sso.KeySecretSealed} {
		if v, err := st.Settings().GetSetting(context.Background(), key); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s saved as %q over the environment", key, v)
		}
	}
	if v := setting(t, st, sso.KeyDisplayName); v != "Mine" {
		t.Errorf("the unlocked label was not saved: %q", v)
	}
	// The label survives a restart: it is what the resolved settings carry.
	if w := call(t, srv, "GET", "/api/admin/signin", "", admin); !strings.Contains(w.Body.String(), `"display_name":{"value":"Mine","source":"saved"}`) {
		t.Errorf("admin view after save: %s", w.Body.String())
	}
}

func TestSignInSaveNeedsStepUp(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	restore := api.SetStepUpWindowForTest(0)
	defer restore()
	w := saveSignIn(t, srv, admin, `{"provider":"none"}`)
	if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
		t.Fatalf("stale session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Settings().GetSetting(context.Background(), sso.KeyProvider); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a refused save stored settings")
	}
}

// The test route uses the production policy here: httptest listens on loopback, so it is refused.
func TestSignInTestRouteRefusesAndSavesNothing(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	a := tlsIdP(t)
	for _, tc := range []struct {
		body string
		code int
		want string
	}{
		{oidcBody(a.URL, ""), http.StatusUnprocessableEntity, "loopback"},
		{oidcBody("http://idp.example", ""), http.StatusUnprocessableEntity, "https://"},
		{`{"provider":"oidc","display_name":"Acme","issuer":"https://idp.example"}`, http.StatusBadRequest, "client ID"},
		{`{"provider":"none"}`, http.StatusBadRequest, "Choose a provider"},
	} {
		w := call(t, srv, "POST", "/api/admin/signin/test", tc.body, admin)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want %d mentioning %q", tc.body, w.Code, w.Body.String(), tc.code, tc.want)
		}
	}
	trustIdPs(srv, a)
	if w := call(t, srv, "POST", "/api/admin/signin/test", oidcBody(a.URL, ""), admin); w.Code != http.StatusOK {
		t.Fatalf("reachable provider: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Settings().GetSetting(context.Background(), sso.KeyIssuer); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the test route saved settings")
	}
	if n := len(auditDetails(t, st, "admin.signin_test")); n != 3 {
		t.Errorf("want 3 admin.signin_test rows (two refusals, one success), got %d", n)
	}
}

// P42: an environment secret without the environment issuer is ignored, so it can never reach
// an issuer an admin typed in: the save needs its own secret and the token request carries it.
func TestSignInEnvSecretNeverReachesAnAdminIssuer(t *testing.T) {
	for _, kind := range []string{"oidc", "kyidentity"} {
		t.Run(kind, func(t *testing.T) {
			srv, st, cfg := setupTestServer(t)
			admin := loginAs(t, srv, st, "root", "admin")
			cfg.SSO.KySignOnSecret = "env-secret"
			var secrets []string
			var ts *httptest.Server
			ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					_ = json.NewEncoder(w).Encode(map[string]any{
						"issuer": ts.URL, "authorization_endpoint": ts.URL + "/authorize", "token_endpoint": ts.URL + "/token",
						"jwks_uri": ts.URL + "/keys", "code_challenge_methods_supported": []string{"S256"},
					})
				case "/token":
					if _, pw, ok := r.BasicAuth(); ok {
						secrets = append(secrets, pw)
					}
					if err := r.ParseForm(); err == nil && r.PostForm.Get("client_secret") != "" {
						secrets = append(secrets, r.PostForm.Get("client_secret"))
					}
					http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			trustIdPs(srv, ts)

			if w := call(t, srv, "GET", "/api/admin/signin", "", admin); !strings.Contains(w.Body.String(), `"client_secret":{"set":false,"source":"unset"}`) {
				t.Fatalf("an ignored env secret is reported: %s", w.Body.String())
			}
			body := `{"provider":"` + kind + `","display_name":"Acme","issuer":"` + ts.URL + `","client_id":"kc"`
			if w := saveSignIn(t, srv, admin, body+`}`); w.Code != http.StatusBadRequest {
				t.Fatalf("save without a secret: %d %s", w.Code, w.Body.String())
			}
			if w := saveSignIn(t, srv, admin, body+`,"client_secret":"submitted"}`); w.Code != http.StatusOK {
				t.Fatalf("save: %d %s", w.Code, w.Body.String())
			}

			login := httptest.NewRecorder()
			srv.ServeHTTP(login, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
			loc, err := url.Parse(login.Header().Get("Location"))
			if login.Code != http.StatusFound || err != nil {
				t.Fatalf("login: %d %v", login.Code, err)
			}
			req := httptest.NewRequest("GET", "/api/sso/kysignon/callback?code=c&state="+loc.Query().Get("state"), nil)
			for _, c := range login.Result().Cookies() {
				req.AddCookie(c)
			}
			cb := httptest.NewRecorder()
			srv.ServeHTTP(cb, req)
			if cb.Code != http.StatusUnauthorized {
				t.Fatalf("callback: %d %s", cb.Code, cb.Body.String())
			}
			if len(secrets) == 0 {
				t.Fatal("the token request carried no client secret")
			}
			for _, s := range secrets {
				if s != "submitted" {
					t.Fatalf("the token request carried %q, want the submitted secret", s)
				}
			}
		})
	}
}

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fmt"
	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"strings"
)

func TestInstallerLiveActivationRolesAndSignedRevocation(t *testing.T) {
	ctx := context.Background()
	_, st, cfg := setupTestServer(t)
	cfg.Server.AppURL = "https://message.example.test"
	dir := t.TempDir()
	secret, directory := filepath.Join(dir, "client"), filepath.Join(dir, "directory")
	for _, p := range []string{secret, directory} {
		if err := os.WriteFile(p, []byte("private-fixture-secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kycalendar", SetupID: "271333e2-c221-4601-b249-563b3d828530", SSO: &applysetup.SSO{IssuerURL: "https://identity.example.test", ClientID: "message", ClientSecretFile: secret, DirectorySecretFile: directory, RedirectURI: cfg.Server.AppURL + "/api/sso/kysignon/callback"}, Admins: []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "owner-sub", Username: "owner-admin"}}, Everyday: []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "daily-sub", Username: "everyday"}}}
	raw, _ := json.Marshal(b)
	makeServer := func() *api.Server { return api.NewServer(cfg, st) }
	srv := makeServer()
	socket, err := config.InstallerSocket(cfg.Database.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	closeSetup, err := applysetup.StartLocal(ctx, socket, srv.ApplyInstaller, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := applysetup.CallLocal(ctx, socket, raw, false)
	if err != nil || len(report.Created) != 3 || len(report.Pending) != 1 {
		t.Fatal(report, err)
	}
	if cfg.SSO.KySignOnIssuer != "" {
		t.Fatal("mutable runtime snapshot")
	}
	if err = closeSetup(); err != nil {
		t.Fatal(err)
	}
	if err = config.ApplyInstallerConfiguration(cfg, b); err != nil {
		t.Fatal(err)
	}
	srv = makeServer()
	closeSetup, err = applysetup.StartLocal(ctx, socket, srv.ApplyInstaller, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSetup()
	report, err = applysetup.CallLocal(ctx, socket, raw, true)
	if err != nil || len(report.Present) != 3 || len(report.Pending) != 0 {
		t.Fatal(report, err)
	}
	for _, sub := range []string{"owner-sub", "daily-sub"} {
		user, err := st.Users().GetUserBySSO(ctx, "kysignon", sub)
		if err != nil {
			t.Fatal(err)
		}
		login := httptest.NewRecorder()
		if _, _, err = auth.NewSessionManager(st, cfg.Security).IssueSession(ctx, login, httptest.NewRequest("GET", "/", nil), user); err != nil {
			t.Fatal(err)
		}
		request := func() *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodGet, "/api/backup/status", nil)
			for _, c := range login.Result().Cookies() {
				r.AddCookie(c)
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			return w
		}
		expected := 200
		if sub == "daily-sub" {
			expected = 403
		}
		if w := request(); w.Code != expected {
			t.Fatal(sub, w.Code, w.Body.String())
		}
		body := fmt.Sprintf(`{"id":%q,"active":false,"meta":{"version":"W/\"2\""}}`, sub)
		syncRequest := httptest.NewRequest("POST", "/api/installer/directory", strings.NewReader(body))
		headers, err := syncauth.Sign([]byte(cfg.SSO.KySignOnHMACSecret), time.Now(), "user.deactivated", "fixture-"+sub, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		headers.Apply(syncRequest)
		synced := httptest.NewRecorder()
		srv.ServeHTTP(synced, syncRequest)
		if synced.Code != 204 {
			t.Fatalf("signed offboard %d %s", synced.Code, synced.Body)
		}
		if w := request(); w.Code != 401 {
			t.Fatal("inactive identity retained session", sub, w.Code)
		}
	}
}

func TestInstallerNetworkRejectsUntrustedForwardedHeaders(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	for _, trusted := range []bool{false, true} {
		cfg.Security.TrustedProxies = nil
		if trusted {
			cfg.Security.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")}
		}
		srv := api.NewServer(cfg, st)
		r := httptest.NewRequest("GET", "/api/installer/network", nil)
		r.RemoteAddr = "192.0.2.10:1234"
		r.Header.Set("X-Forwarded-For", "198.51.100.8")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		var result struct {
			IP      string `json:"clientIp"`
			Trusted bool   `json:"proxyHeadersTrusted"`
			Scheme  string `json:"scheme"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		ip, scheme := "192.0.2.10", "http"
		if trusted {
			ip, scheme = "198.51.100.8", "https"
		}
		if result.IP != ip || result.Scheme != scheme || result.Trusted != trusted || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("network %+v", result)
		}
	}
}

func TestInstallerOffboardingRevokesDAVAndPreservesCalendars(t *testing.T) {
	ctx := context.Background()
	_, st, cfg := setupTestServer(t)
	cfg.Server.AppURL = "https://calendar.example.test"
	dir := t.TempDir()
	secret, directory := filepath.Join(dir, "client"), filepath.Join(dir, "directory")
	for _, p := range []string{secret, directory} {
		if err := os.WriteFile(p, []byte("private-fixture-credential"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kycalendar", SetupID: "271333e2-c221-4601-b249-563b3d828530", SSO: &applysetup.SSO{IssuerURL: "https://identity.example", ClientID: "kc", ClientSecretFile: secret, DirectorySecretFile: directory, RedirectURI: cfg.Server.AppURL + "/api/sso/kysignon/callback"}, Admins: []applysetup.Identity{{Issuer: "https://identity.example", Subject: "owner", Username: "owner-admin"}}, Everyday: []applysetup.Identity{{Issuer: "https://identity.example", Subject: "daily", Username: "everyday"}}}
	if _, err := st.ApplyInstaller(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if err := config.ApplyInstallerConfiguration(cfg, b); err != nil {
		t.Fatal(err)
	}
	srv := api.NewServer(cfg, st)
	for _, sub := range []string{"owner", "daily"} {
		user, err := st.Users().GetUserBySSO(ctx, "kysignon", sub)
		if err != nil {
			t.Fatal(err)
		}
		id, token, hash, err := apppass.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if err = st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{Scope: store.ScopeDAV, ID: id, UserID: user.ID, Label: "fixture", Hash: hash}, 0); err != nil {
			t.Fatal(err)
		}
		if got := davDo(srv, "OPTIONS", "/dav/", user.Username, token); sub == "owner" && got.Code != 403 || sub == "daily" && got.Code != 204 {
			t.Fatal(sub, got.Code, got.Body)
		}
		if sub == "owner" {
			continue
		}
		cal := &store.Calendar{ID: "cal_fixture", OwnerKind: "user", OwnerID: user.ID, Slug: "personal", Name: "Retained calendar"}
		if err = st.Calendars().CreateCalendar(ctx, cal, 0); err != nil {
			t.Fatal(err)
		}
		if err = st.ApplyInstallerDirectory(ctx, b.SSO.IssuerURL, sub, "user.deleted", "5", "delete", false, false); err != nil {
			t.Fatal(err)
		}
		if got := davDo(srv, "OPTIONS", "/dav/", user.Username, token); got.Code != 401 {
			t.Fatal("DAV survived offboarding", got.Code)
		}
		if passwords, err := st.AppPasswords().ListByUser(ctx, user.ID); err != nil || len(passwords) != 0 {
			t.Fatal(passwords, err)
		}
		if _, err = st.Calendars().GetCalendarByID(ctx, cal.ID); err != nil {
			t.Fatal("offboarding erased calendar", err)
		}
		if _, err = st.Users().GetUserByID(ctx, user.ID); err != nil {
			t.Fatal("offboarding erased identity", err)
		}
	}
}

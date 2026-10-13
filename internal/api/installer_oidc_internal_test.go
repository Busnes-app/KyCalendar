package api

import (
	"context"
	"encoding/json"
	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallerVerifiedOIDCAndAdminSeparation(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	idp := signingIdPFixture(t, true)
	dir := t.TempDir()
	secret, directory := filepath.Join(dir, "client"), filepath.Join(dir, "directory")
	for _, p := range []string{secret, directory} {
		if err := os.WriteFile(p, []byte("disposable-private-credential"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.config.Server.AppURL = "https://calendar.example"
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kycalendar", SetupID: "271333e2-c221-4601-b249-563b3d828530", SSO: &applysetup.SSO{IssuerURL: idp.URL, ClientID: "kc", ClientSecretFile: secret, DirectorySecretFile: directory, RedirectURI: s.config.Server.AppURL + "/api/sso/kysignon/callback"}, Admins: []applysetup.Identity{{Issuer: idp.URL, Subject: "owner", Username: "owner-admin"}}, Everyday: []applysetup.Identity{{Issuer: idp.URL, Subject: "daily", Username: "daily-user"}}}
	if _, err := s.store.ApplyInstaller(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if err := config.ApplyInstallerConfiguration(s.config, b); err != nil {
		t.Fatal(err)
	}
	s.signinHTTP = idp.Client()
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sub   string
		roles []string
		want  int
	}{{"owner", []string{"kycalendar.admin"}, 302}, {"daily", []string{}, 302}, {"stranger", []string{"kycalendar.admin"}, 403}} {
		idp.roles = tc.roles
		w := httptest.NewRecorder()
		s.ServeHTTP(w, idp.login(t, s, tc.sub))
		if w.Code != tc.want {
			t.Fatalf("%s callback %d %s", tc.sub, w.Code, w.Body)
		}
		if tc.want != 302 {
			continue
		}
		for _, path := range []string{"/api/backup/status", "/api/calendars", "/api/app-passwords"} {
			req := httptest.NewRequest("GET", path, nil)
			for _, c := range w.Result().Cookies() {
				req.AddCookie(c)
			}
			got := httptest.NewRecorder()
			s.ServeHTTP(got, req)
			want := 200
			if (tc.sub == "owner" && path != "/api/backup/status") || (tc.sub == "daily" && path == "/api/backup/status") {
				want = 403
			}
			if got.Code != want {
				t.Fatalf("%s %s %d %s", tc.sub, path, got.Code, got.Body)
			}
		}
	}
	// Owned application grants cannot be overwritten by the native sign-in screen.
	raw, _ := json.Marshal(b)
	if report, err := s.ApplyInstaller(ctx, raw, true); err != nil || len(report.Pending) != 0 {
		t.Fatal(report, err)
	}
	idp.roles = []string{}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, idp.login(t, s, "owner"))
	if w.Code != 403 {
		t.Fatal("removed app role accepted", w.Code, w.Body)
	}
	if _, allowed, err := s.store.InstallerIdentity(ctx, idp.URL, "owner"); err != nil || allowed {
		t.Fatal(allowed, err)
	}
	if err := os.Remove(secret); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	s.ServeHTTP(login, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if login.Code != 502 {
		t.Fatal("missing protected credential accepted", login.Code, login.Body)
	}
}

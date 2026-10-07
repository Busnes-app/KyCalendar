package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
)

// recordingIdP records every request it receives, headers and body, and answers nothing useful.
type recordingIdP struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newRecordingIdP(t *testing.T) *recordingIdP {
	idp := &recordingIdP{}
	idp.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		idp.mu.Lock()
		idp.seen = append(idp.seen, r.Header.Get("Authorization")+" "+r.URL.String()+" "+string(body))
		idp.mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(idp.Close)
	return idp
}

func (idp *recordingIdP) saw(secret string) bool {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	for _, s := range idp.seen {
		if strings.Contains(s, secret) {
			return true
		}
	}
	return false
}

// A saved secret is used only for the registration it was entered for. When the environment
// moves the effective issuer or client ID, or the record is missing, startup leaves sign-in closed
// and the previous registration's secret never reaches the new provider.
func TestStartupNeverSendsASavedSecretToAnotherRegistration(t *testing.T) {
	idp := newRecordingIdP(t)
	for name, tc := range map[string]struct {
		issuer, clientID string
		record           bool
	}{
		"environment issuer":    {issuer: idp.URL},
		"environment client ID": {issuer: "https://a.example", clientID: "env-client", record: true},
		"no record":             {record: false},
	} {
		s, _ := davInternalServer(t)
		ctx := context.Background()
		sealed, err := sso.SealSecret(s.config.Security.EncryptionKey, "s1-previous")
		if err != nil {
			t.Fatal(err)
		}
		saved := map[string]string{
			sso.KeyProvider: sso.KindKyIdentity, sso.KeyIssuer: "https://a.example", sso.KeyClientID: "c",
			sso.KeySecretSealed: sealed, sso.KeyBound: "kyidentity https://a.example",
		}
		if tc.record || tc.issuer != "" {
			saved[sso.KeySecretRegistration] = sso.SecretRegistration(sso.KindKyIdentity, "https://a.example", "c")
		}
		for k, v := range saved {
			if err := s.store.Settings().SetSetting(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
		s.config.SSO.KySignOnIssuer, s.config.SSO.KySignOnClientID = tc.issuer, tc.clientID
		if err := s.LoadSignIn(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p := s.signin.Load(); p != nil {
			t.Errorf("%s: sign-in live under another registration (%s)", name, p.Binding)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: login %d, want 404 (closed)", name, w.Code)
		}
		if idp.saw("s1-previous") {
			t.Fatalf("%s: the previous registration's secret reached the new provider", name)
		}
	}
}

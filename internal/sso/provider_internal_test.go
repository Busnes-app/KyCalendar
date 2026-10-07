package sso

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
)

// Only KyIdentity's roles can make an administrator: an oidc provider's roles claim is dropped.
func TestStampKeepsRolesForKyIdentityOnly(t *testing.T) {
	ky := (&Provider{Kind: KindKyIdentity}).stamp(&IdentityClaims{Subject: "s", Roles: []string{access.AdminAppRole}})
	if ky.Provider != "kysignon" || !access.IsAdmin(ky.Roles) {
		t.Fatalf("kyidentity: %+v", ky)
	}
	oidc := (&Provider{Kind: KindOIDC}).stamp(&IdentityClaims{Subject: "s", Roles: []string{access.AdminAppRole}})
	if oidc.Provider != "oidc" || oidc.Roles != nil {
		t.Fatalf("oidc: %+v", oidc)
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

// A provider built with a client sends discovery and the token exchange through it, never
// through http.DefaultClient.
func TestProviderClientCarriesDiscoveryAndExchange(t *testing.T) {
	var idp *httptest.Server
	idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	rt := &recordingTransport{}
	p := NewProvider(KindOIDC, "Acme", idp.URL, "kc", "", &http.Client{Transport: rt})
	if _, err := p.Exchange(context.Background(), "code", "verifier", "https://app.example/cb", "nonce"); err == nil {
		t.Fatal("an exchange with no id_token succeeded")
	}
	// oauth2 may try the token endpoint twice while it detects the auth style.
	if !slices.Contains(rt.paths, "/.well-known/openid-configuration") || !slices.Contains(rt.paths, "/token") {
		t.Fatalf("requests through the provider's client: %v, want discovery and /token", rt.paths)
	}
}

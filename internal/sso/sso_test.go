package sso_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestOAuthAuthorizationURLUsesDiscoveryAndPKCE(t *testing.T) {
	var issuer string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
		})
	}))
	defer idp.Close()
	issuer = idp.URL

	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnIssuer: issuer, KySignOnClientID: "client"}, nil)
	authURL, err := client.BuildAuthURL(context.Background(), "https://app.example/callback", "state", "verifier", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/authorize" || query.Get("state") != "state" || query.Get("nonce") != "nonce" || query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		t.Fatalf("unexpected authorization URL: %s", authURL)
	}
}

func TestKySignOnWebhookSync(t *testing.T) {
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	hmacSecret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{
		KySignOnHMACSecret: hmacSecret,
	}, st)

	payload := sso.KySignOnSyncPayload{
		Event:       "user.created",
		ID:          "ext-usr-456",
		Username:    "bob",
		Email:       "bob@busnes.app",
		DisplayName: "Bob Engineer",
		Status:      "active",
		Timestamp:   time.Now().Unix(),
	}
	body, _ := json.Marshal(payload)
	sig := crypto.ComputeHMACSHA256(body, hmacSecret)

	// 1. Sync create user
	if err := client.HandleSyncWebhook(context.Background(), body, sig); err != nil {
		t.Fatalf("HandleSyncWebhook failed: %v", err)
	}

	created, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-usr-456")
	if err != nil {
		t.Fatalf("GetUserBySSO failed: %v", err)
	}
	if created.Username != "bob" || created.DisplayName != "Bob Engineer" {
		t.Errorf("unexpected created user: %+v", created)
	}

	// 2. Sync deactivation
	payload.Event = "user.deactivated"
	body, _ = json.Marshal(payload)
	sig = crypto.ComputeHMACSHA256(body, hmacSecret)

	if err := client.HandleSyncWebhook(context.Background(), body, sig); err != nil {
		t.Fatalf("HandleSyncWebhook deactivation failed: %v", err)
	}

	updated, _ := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-usr-456")
	if updated.Status != "inactive" {
		t.Errorf("expected inactive status, got %s", updated.Status)
	}
}

func TestSAMLServiceProvider(t *testing.T) {
	sp := sso.NewSAMLServiceProvider("https://app.busnes.app/saml/metadata", "https://app.busnes.app/saml/acs")

	metadata := sp.GenerateMetadata()
	if len(metadata) == 0 || !testing.Verbose() && len(metadata) < 50 {
		if len(metadata) == 0 {
			t.Errorf("expected non-empty metadata")
		}
	}

}

// The webhook's legacy role is the global KyIdentity role: it never grants admin.
func TestKySignOnWebhookIgnoresGlobalRole(t *testing.T) {
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	body, _ := json.Marshal(map[string]any{
		"event": "user.created", "id": "ext-admin", "username": "root", "role": "admin",
		"status": "active", "timestamp": time.Now().Unix(),
	})
	if err := client.HandleSyncWebhook(context.Background(), body, crypto.ComputeHMACSHA256(body, secret)); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-admin")
	if err != nil || u.Role != "user" {
		t.Fatalf("webhook role leaked: %+v %v", u, err)
	}
}

// The webhook cannot see app roles: an update for a stored admin ends their sessions so the
// role is re-proved at the next sign-in; an everyday user's session survives.
func TestKySignOnWebhookUpdateRevokesAdminSessionsOnly(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)

	for _, tc := range []struct {
		role        string
		wantSession bool
	}{{"admin", false}, {"user", true}} {
		id := "usr_" + tc.role
		if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: tc.role, Role: tc.role, Status: "active", SSOProvider: "kysignon", SSOSubject: "ext-" + tc.role}); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_" + tc.role, UserID: id, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, ""); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{
			"event": "user.updated", "id": "ext-" + tc.role, "username": tc.role,
			"status": "active", "timestamp": time.Now().Unix(),
		})
		if err := client.HandleSyncWebhook(ctx, body, crypto.ComputeHMACSHA256(body, secret)); err != nil {
			t.Fatal(err)
		}
		_, err := st.Sessions().GetSession(ctx, "tok_"+tc.role)
		if (err == nil) != tc.wantSession {
			t.Fatalf("%s: session survived=%v, want %v (%v)", tc.role, err == nil, tc.wantSession, err)
		}
		u, err := st.Users().GetUserByID(ctx, id)
		if err != nil || u.Role != tc.role {
			t.Fatalf("%s: stored role changed: %+v %v", tc.role, u, err)
		}
	}
}

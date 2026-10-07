package sso_test

import (
	"context"
	"encoding/json"
	"errors"
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

	p := sso.NewProvider(sso.KindKyIdentity, "KyIdentity", issuer, "client", "", nil)
	authURL, err := p.AuthURL(context.Background(), "https://app.example/callback", "state", "verifier", "nonce")
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
	st, err := boundStore(t)
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
	if created.SSOIssuer != sso.KindKyIdentity+" https://idp.example" {
		t.Errorf("created user stamped %q, want the binding", created.SSOIssuer)
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
	st, err := boundStore(t)
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
	st, err := boundStore(t)
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

// failingUpdates refuses the webhook's profile write and every whole-row UpdateUser.
type failingUpdates struct{ store.Store }
type failingUserStore struct{ store.UserStore }

func (f failingUpdates) Users() store.UserStore { return failingUserStore{f.Store.Users()} }
func (failingUserStore) UpdateUser(context.Context, *store.User) error {
	return errors.New("update refused")
}
func (failingUserStore) UpdateKySignOnProfile(context.Context, string, string, string) error {
	return errors.New("update refused")
}

func webhook(t *testing.T, client *sso.KySignOnClient, secret string, fields map[string]any) error {
	t.Helper()
	fields["timestamp"] = time.Now().Unix()
	body, _ := json.Marshal(fields)
	return client.HandleSyncWebhook(context.Background(), body, crypto.ComputeHMACSHA256(body, secret))
}

// KyIdentity's SCIM externalId is its user ID, the webhook's id: a SCIM-provisioned user is
// the same user, so the webhook reaches it (deactivating it) instead of colliding or ignoring it.
func TestKySignOnWebhookReachesSCIMUsers(t *testing.T) {
	ctx := context.Background()
	st, err := boundStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_s", Username: "sam", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "ext-s"}); err != nil {
		t.Fatal(err)
	}
	if err := webhook(t, client, secret, map[string]any{"event": "user.updated", "id": "ext-s", "username": "sam", "display_name": "Sam S", "status": "active"}); err != nil {
		t.Fatalf("update of a SCIM user: %v", err)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_s"); u.DisplayName != "" {
		t.Fatalf("webhook rewrote a SCIM-owned profile: %+v", u)
	}
	if n, _ := st.Users().CountUsers(ctx); n != 1 {
		t.Fatalf("webhook created a second user: %d users", n)
	}
	if err := webhook(t, client, secret, map[string]any{"event": "user.deactivated", "id": "ext-s"}); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_s"); u.Status != "inactive" {
		t.Fatalf("SCIM user not deactivated: %q", u.Status)
	}
}

// Revocation comes before the profile write: a failed write still signs a stored admin out.
func TestKySignOnWebhookRevokesBeforeStoring(t *testing.T) {
	ctx := context.Background()
	real, err := boundStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer real.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, failingUpdates{real})
	if err := real.Users().CreateUser(ctx, &store.User{ID: "usr_k", Username: "kim", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "ext-k"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := real.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_k", UserID: "usr_k", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	if err := webhook(t, client, secret, map[string]any{"event": "user.updated", "id": "ext-k", "display_name": "Kim", "status": "active"}); err == nil {
		t.Fatal("update succeeded despite the failing write")
	}
	if _, err := real.Sessions().GetSession(ctx, "tok_k"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("admin session survived a failed update: %v", err)
	}
	// Deactivation never takes the whole-row path, so the refused UpdateUser cannot stop it.
	if err := webhook(t, client, secret, map[string]any{"event": "user.deactivated", "id": "ext-k"}); err != nil {
		t.Fatal(err)
	}
	if u, _ := real.Users().GetUserByID(ctx, "usr_k"); u.Status != "inactive" || u.Role != "admin" {
		t.Fatalf("want inactive with the stored role kept: %+v", u)
	}
}

// After a move from KyIdentity issuer A to issuer B (both bound "kyidentity"), A's webhook still
// holds the HMAC secret. It must not re-activate the row the binding disabled: a B login with a
// colliding subject would adopt it. The webhook only takes access away.
func TestKySignOnWebhookNeverReactivatesAfterIssuerMove(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_a", Username: "ann", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "ext-a", SSOIssuer: sso.KindKyIdentity + " https://issuer-a.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{Disable: []string{"kysignon"}}, map[string]string{sso.KeyBound: sso.KindKyIdentity + " https://issuer-b.example"}); err != nil {
		t.Fatal(err)
	}
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, failingUpdates{st})
	for _, event := range []string{"user.updated", "user.created"} {
		if err := webhook(t, client, secret, map[string]any{"event": event, "id": "ext-a", "username": "ann", "display_name": "Ann " + event, "status": "active"}); err == nil {
			t.Fatalf("%s: want the refused profile write reported", event)
		}
	}
	client = sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	if err := webhook(t, client, secret, map[string]any{"event": "user.updated", "id": "ext-a", "display_name": "Ann A", "status": "active"}); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserByID(ctx, "usr_a")
	if err != nil || u.Status != "inactive" || u.DisplayName != "Ann A" || u.SSOIssuer != sso.KindKyIdentity+" https://issuer-a.example" {
		t.Fatalf("want the profile updated and the row still inactive: %+v %v", u, err)
	}
	if n, _ := st.Users().CountUsers(ctx); n != 1 {
		t.Fatalf("webhook created a second row for a known id: %d users", n)
	}
}

// disableAfterRead disables every kysignon account right after the webhook reads its row, the
// interleaving where a stale whole-row write would put status=active back.
type disableAfterRead struct{ store.Store }
type disableAfterReadUsers struct{ store.UserStore }

func (d disableAfterRead) Users() store.UserStore { return disableAfterReadUsers{d.Store.Users()} }
func (d disableAfterReadUsers) GetUserBySSO(ctx context.Context, provider, subject string) (*store.User, error) {
	u, err := d.UserStore.GetUserBySSO(ctx, provider, subject)
	if err == nil {
		if _, err := d.BindSignIn(ctx, store.System, store.SignInBinding{Disable: []string{"kysignon"}}, nil); err != nil {
			return nil, err
		}
	}
	return u, err
}

func TestKySignOnWebhookCannotUndoAConcurrentDisable(t *testing.T) {
	ctx := context.Background()
	st, err := boundStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_r", Username: "rex", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "ext-r"}); err != nil {
		t.Fatal(err)
	}
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, disableAfterRead{st})
	if err := webhook(t, client, secret, map[string]any{"event": "user.updated", "id": "ext-r", "display_name": "Rex", "status": "active"}); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_r"); u.Status != "inactive" {
		t.Fatalf("webhook wrote back a stale status over a concurrent disable: %+v", u)
	}
}

// SCIM users created without an externalId have an empty subject; an empty webhook id must
// never resolve to one of them.
func TestKySignOnWebhookRefusesEmptyID(t *testing.T) {
	ctx := context.Background()
	st, err := boundStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_n", Username: "nobody", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if err := webhook(t, client, secret, map[string]any{"event": "user.deleted", "id": ""}); err == nil {
		t.Fatal("empty id accepted")
	}
	if _, err := st.Users().GetUserByID(ctx, "usr_n"); err != nil {
		t.Fatalf("empty id reached a SCIM user without a subject: %v", err)
	}
}

// SCIM owns a SCIM-provisioned user: the webhook may take access away, never give it back.
func TestKySignOnWebhookCannotRestoreSCIMUsers(t *testing.T) {
	ctx := context.Background()
	st, err := boundStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_o", Username: "olga", DisplayName: "Olga", Role: "user", Status: "inactive", SSOProvider: "scim", SSOSubject: "ext-o"}); err != nil {
		t.Fatal(err)
	}
	if err := webhook(t, client, secret, map[string]any{"event": "user.updated", "id": "ext-o", "username": "root", "display_name": "Root", "status": "active"}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.Users().GetUserByID(ctx, "usr_o")
	if u.Status != "inactive" || u.Username != "olga" || u.DisplayName != "Olga" {
		t.Fatalf("webhook overrode a SCIM-owned user: %+v", u)
	}
}

// Deleting a user deletes their personal calendars; for a SCIM-owned user that is SCIM's call.
// The webhook's delete only deactivates them.
func TestKySignOnWebhookDeleteOnlyDeactivatesSCIMUsers(t *testing.T) {
	ctx := context.Background()
	st, err := boundStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_d", Username: "dora", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "ext-d"}); err != nil {
		t.Fatal(err)
	}
	if err := webhook(t, client, secret, map[string]any{"event": "user.deleted", "id": "ext-d"}); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserByID(ctx, "usr_d")
	if err != nil || u.Status != "inactive" {
		t.Fatalf("want the SCIM user kept and inactive: %+v %v", u, err)
	}
}

// boundStore is a test store whose accounts are bound to a KyIdentity provider, the only
// binding the directory webhook writes under.
func boundStore(t *testing.T) (store.Store, error) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		return nil, err
	}
	return st, st.Settings().SetSetting(context.Background(), sso.KeyBound, sso.KindKyIdentity+" https://idp.example")
}

// A webhook secret left over from KyIdentity must not create or re-activate rows once sign-in
// is bound elsewhere (or nowhere): another provider's login could adopt them.
func TestKySignOnWebhookIgnoredUnlessBoundToKyIdentity(t *testing.T) {
	ctx := context.Background()
	secret := "webhook-secret-999"
	for _, bound := range []string{"", sso.KindOIDC + " https://other.example"} {
		st, err := store.Open(ctx, testdb.Config(t))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if bound != "" {
			if err := st.Settings().SetSetting(ctx, sso.KeyBound, bound); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_off", Username: "off", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "ext-off"}); err != nil {
			t.Fatal(err)
		}
		client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
		for _, fields := range []map[string]any{
			{"event": "user.created", "id": "ext-new", "username": "newbie", "status": "active"},
			{"event": "user.updated", "id": "ext-off", "username": "off", "status": "active"},
		} {
			if err := webhook(t, client, secret, fields); err != nil {
				t.Fatalf("bound %q: %v, want the webhook ignored without error", bound, err)
			}
		}
		if _, err := st.Users().GetUserBySSO(ctx, "kysignon", "ext-new"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("bound %q: webhook created a user (%v)", bound, err)
		}
		if u, _ := st.Users().GetUserByID(ctx, "usr_off"); u.Status != "inactive" {
			t.Errorf("bound %q: webhook re-activated a row", bound)
		}
	}
}

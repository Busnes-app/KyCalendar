package sso_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
)

var savedOIDC = map[string]string{
	sso.KeyProvider: sso.KindOIDC, sso.KeyDisplayName: "Acme", sso.KeyIssuer: "https://saved.example",
	sso.KeyClientID: "saved-client", sso.KeySecretSealed: "sealed-blob",
}

var savedKy = map[string]string{
	sso.KeyProvider: sso.KindKyIdentity, sso.KeyDisplayName: "Acme", sso.KeyIssuer: "https://saved.example",
	sso.KeyClientID: "saved-client", sso.KeySecretSealed: "sealed-blob",
}

func TestResolveEnvironmentWinsAndLocks(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{KySignOnIssuer: "https://id.example/", KySignOnSecret: "env-secret"}, savedKy)
	want := sso.Settings{
		Provider:    sso.Field{Value: sso.KindKyIdentity, Source: sso.SourceEnvironment},
		DisplayName: sso.Field{Value: "Acme", Source: sso.SourceSaved},
		Issuer:      sso.Field{Value: "https://id.example", Source: sso.SourceEnvironment},
		ClientID:    sso.Field{Value: "saved-client", Source: sso.SourceSaved},
		Secret:      sso.SecretField{Value: "env-secret", Source: sso.SourceEnvironment},
	}
	if st != want {
		t.Fatalf("got %+v\nwant %+v", st, want)
	}
}

// P27: an environment that fixes kyidentity drops a saved row of another kind entirely.
func TestResolveEnvironmentKyIdentityDropsForeignSavedRow(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{KySignOnIssuer: "https://id.example/", KySignOnSecret: "env-secret"}, savedOIDC)
	want := sso.Settings{
		Provider:    sso.Field{Value: sso.KindKyIdentity, Source: sso.SourceEnvironment},
		DisplayName: sso.Field{Value: "KyIdentity", Source: sso.SourceUnset},
		Issuer:      sso.Field{Value: "https://id.example", Source: sso.SourceEnvironment},
		ClientID:    sso.Field{Source: sso.SourceUnset},
		Secret:      sso.SecretField{Value: "env-secret", Source: sso.SourceEnvironment},
	}
	if st != want {
		t.Fatalf("got %+v\nwant %+v", st, want)
	}
	if st.Live() {
		t.Fatal("kyidentity without a client id is live")
	}
}

// Env client credentials must not pair kyidentity with a generic IdP's issuer, whose roles
// claim would then grant administrator.
func TestResolveEnvClientWithSavedOIDCIssuerIsNotLive(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{KySignOnClientID: "env-client", KySignOnSecret: "env-secret"}, savedOIDC)
	if st.Issuer != (sso.Field{Source: sso.SourceUnset}) || st.Live() {
		t.Fatalf("kyidentity carries the generic issuer: %+v", st)
	}
	if st.Identity() != "kyidentity " {
		t.Fatalf("identity %q", st.Identity())
	}
}

// Env issuer must not receive the generic IdP's client credentials.
func TestResolveEnvIssuerWithSavedOIDCCredentialsIsNotLive(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{KySignOnIssuer: "https://id.example"}, savedOIDC)
	if st.ClientID != (sso.Field{Source: sso.SourceUnset}) || st.Secret != (sso.SecretField{Source: sso.SourceUnset}) || st.Live() {
		t.Fatalf("kyidentity carries the generic credentials: %+v", st)
	}
}

func TestSecretNeverPrintedOrMarshalled(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{KySignOnIssuer: "https://id.example", KySignOnClientID: "c", KySignOnSecret: "plain-env-secret"}, nil)
	if st.Secret.Value != "plain-env-secret" {
		t.Fatalf("secret %q", st.Secret.Value)
	}
	js, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"json": string(js), "%v": fmt.Sprintf("%v", st), "%+v": fmt.Sprintf("%+v", st),
		"%#v": fmt.Sprintf("%#v", st), "%s": fmt.Sprintf("%s", st.Secret), "field %+v": fmt.Sprintf("%+v", st.Secret),
	} {
		if strings.Contains(out, "plain-env-secret") {
			t.Errorf("%s carries the secret: %s", name, out)
		}
	}
	if !strings.Contains(string(js), `"Secret":{"source":"environment"}`) {
		t.Errorf("json %s", js)
	}
}

func TestResolveEachEnvironmentFieldLocksOnlyItself(t *testing.T) {
	envKy := sso.Field{Value: sso.KindKyIdentity, Source: sso.SourceEnvironment}
	cases := []struct {
		name  string
		env   config.SSOConfig
		field func(sso.Settings) sso.Field
		want  sso.Field
	}{
		{"issuer", config.SSOConfig{KySignOnIssuer: "https://env.example"}, func(s sso.Settings) sso.Field { return s.Issuer }, sso.Field{Value: "https://env.example", Source: sso.SourceEnvironment}},
		{"client id", config.SSOConfig{KySignOnClientID: "env-client"}, func(s sso.Settings) sso.Field { return s.ClientID }, sso.Field{Value: "env-client", Source: sso.SourceEnvironment}},
		{"secret", config.SSOConfig{KySignOnSecret: "env-secret"}, func(s sso.Settings) sso.Field { return sso.Field(s.Secret) }, sso.Field{Value: "env-secret", Source: sso.SourceEnvironment}},
	}
	for _, c := range cases {
		st := sso.Resolve(c.env, savedKy)
		if got := c.field(st); got != c.want {
			t.Errorf("%s: field %+v, want %+v", c.name, got, c.want)
		}
		if st.Provider != envKy {
			t.Errorf("%s: provider %+v, want kyidentity from the environment", c.name, st.Provider)
		}
		// Every other field stays saved.
		saved := 0
		for _, f := range []sso.Field{st.DisplayName, st.Issuer, st.ClientID, sso.Field(st.Secret)} {
			if f.Source == sso.SourceSaved {
				saved++
			}
		}
		if saved != 3 {
			t.Errorf("%s: %d saved fields, want 3: %+v", c.name, saved, st)
		}
	}
}

// P14: the webhook secret only authenticates the directory webhook. It must not turn a saved
// generic provider into kyidentity, whose roles claim grants administrator.
func TestResolveWebhookSecretAloneLeavesProvider(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{KySignOnHMACSecret: "h"}, savedOIDC)
	if st.Provider != (sso.Field{Value: sso.KindOIDC, Source: sso.SourceSaved}) {
		t.Fatalf("HMAC secret only: provider %+v, want saved oidc", st.Provider)
	}
	if st.Issuer.Source != sso.SourceSaved || st.ClientID.Source != sso.SourceSaved || st.Secret.Source != sso.SourceSaved {
		t.Fatalf("HMAC secret only locked a field: %+v", st)
	}
	if st := sso.Resolve(config.SSOConfig{KySignOnHMACSecret: "h"}, nil); st.Provider != (sso.Field{Value: sso.KindNone, Source: sso.SourceUnset}) {
		t.Fatalf("HMAC secret only, nothing saved: provider %+v", st.Provider)
	}
}

func TestResolveSavedAndUnset(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{}, map[string]string{
		sso.KeyProvider: sso.KindOIDC, sso.KeyIssuer: "https://saved.example", sso.KeyClientID: "c", sso.KeySecretSealed: "sealed-blob",
	})
	if st.Provider.Source != sso.SourceSaved || st.Issuer.Value != "https://saved.example" || !st.Live() {
		t.Fatalf("saved oidc: %+v", st)
	}
	if st.Secret != (sso.SecretField{Source: sso.SourceSaved}) {
		t.Fatalf("a saved secret must stay sealed until opened: %+v", st.Secret)
	}
	if st.Identity() != "oidc https://saved.example" {
		t.Fatalf("identity %q", st.Identity())
	}
	empty := sso.Resolve(config.SSOConfig{}, nil)
	if empty.Provider != (sso.Field{Value: sso.KindNone, Source: sso.SourceUnset}) || empty.Live() {
		t.Fatalf("nothing configured: %+v", empty)
	}
	if ky := sso.Resolve(config.SSOConfig{}, map[string]string{sso.KeyProvider: sso.KindKyIdentity}); ky.DisplayName.Value != "KyIdentity" {
		t.Fatalf("kyidentity default label: %+v", ky.DisplayName)
	}
}

func TestSealedSecretRoundTripAndLabel(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	sealed, err := sso.SealSecret(master, "s3cret-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "s3cret") {
		t.Fatal("the sealed form carries the plain secret")
	}
	if got, err := sso.OpenSecret(master, sealed); err != nil || got != "s3cret-value" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := crypto.DecryptAESGCM(sealed, crypto.DeriveKey(master, "kycalendar:setting:other")); err == nil {
		t.Fatal("the secret opened under another label's key")
	}
	if _, err := crypto.DecryptAESGCM(sealed, master); err == nil {
		t.Fatal("the secret opened under the master key itself")
	}
	if _, err := sso.OpenSecret(bytes.Repeat([]byte{8}, 32), sealed); err == nil {
		t.Fatal("the secret opened under another data-volume key")
	}
	if sso.SecretLabel != "kycalendar:setting:signin_client_secret" {
		t.Fatalf("label %q", sso.SecretLabel)
	}
}

func TestSealedSecretTamperFails(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	sealed, err := sso.SealSecret(master, "s3cret-value")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		flipped := bytes.Clone(raw)
		flipped[i] ^= 0x01
		if _, err := sso.OpenSecret(master, base64.RawURLEncoding.EncodeToString(flipped)); err == nil {
			t.Fatalf("a flipped bit at byte %d still opened", i)
		}
	}
	if _, err := sso.OpenSecret(master, sealed[:len(sealed)-2]); err == nil {
		t.Fatal("a truncated secret opened")
	}
}

func TestAccountProviders(t *testing.T) {
	if got := sso.AccountProviders(sso.KindKyIdentity); strings.Join(got, ",") != "kysignon,scim" {
		t.Errorf("kyidentity: %v", got)
	}
	if got := sso.AccountProviders(sso.KindOIDC); strings.Join(got, ",") != "oidc" {
		t.Errorf("oidc: %v", got)
	}
	if got := sso.AccountProviders(sso.KindNone); got != nil {
		t.Errorf("none: %v", got)
	}
}

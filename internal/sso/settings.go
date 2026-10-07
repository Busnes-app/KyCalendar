package sso

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Provider kinds. Both run the same OIDC flow; only kyidentity logins can be administrators or
// adopt SCIM accounts.
const (
	KindNone       = "none"
	KindKyIdentity = "kyidentity"
	KindOIDC       = "oidc"
)

// Settings-store keys. The client secret is stored only sealed.
const (
	KeyProvider     = "signin_provider"
	KeyDisplayName  = "signin_display_name"
	KeyIssuer       = "signin_issuer"
	KeyClientID     = "signin_client_id"
	KeySecretSealed = "signin_client_secret_sealed"
	// KeySecretRegistration is the registration the sealed secret was entered for
	// (SecretRegistration). Its prefix keeps it out of every settings response.
	KeySecretRegistration = "signin_client_secret_registration"
	// KeyBound is the Identity of the provider existing SSO accounts belong to.
	KeyBound = "signin_bound"
)

// SecretLabel derives the key that seals the client secret from the data-volume key.
const SecretLabel = "kycalendar:setting:signin_client_secret"

// Where a field's value comes from.
const (
	SourceEnvironment = "environment"
	SourceSaved       = "saved"
	SourceUnset       = "unset"
)

type Field struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// SecretField holds the client secret. It prints and marshals only its source, so neither a
// log line nor an API response carries the plain secret.
type SecretField struct {
	Value  string
	Source string
}

func (f SecretField) String() string   { return "{Source:" + f.Source + "}" }
func (f SecretField) GoString() string { return f.String() }
func (f SecretField) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Source string `json:"source"`
	}{f.Source})
}

// Settings is the effective sign-in configuration. Secret.Value is the plain secret once the
// caller has opened a saved one.
type Settings struct {
	Provider, DisplayName, Issuer, ClientID Field
	Secret                                  SecretField
}

// Resolve merges the environment over saved settings. KY_KYSIGNON_ISSUER fixes the provider to
// kyidentity and locks the issuer; KY_KYSIGNON_CLIENT_ID and _SECRET then win and lock their
// fields. Without the environment issuer they are ignored (IgnoredEnv): an environment secret
// must never reach an issuer an admin typed in. KY_KYSIGNON_HMAC_SECRET only authenticates the
// directory webhook and leaves the provider alone: it must not turn a generic provider into one
// whose roles claim grants admin. When the environment fixes kyidentity over a saved row of
// another kind, every saved field is dropped: kyidentity is never paired with a foreign issuer,
// nor sent another provider's credentials. A saved secret applies only to the registration it was
// entered for (SavedSecretFits), else it is unset and the settings are not Live. It is reported
// as set but left sealed: Secret.Value is empty until opened.
func Resolve(env config.SSOConfig, saved map[string]string) Settings {
	envKy := env.KySignOnIssuer != ""
	if !envKy {
		env.KySignOnClientID, env.KySignOnSecret = "", ""
	}
	if envKy && saved[KeyProvider] != "" && saved[KeyProvider] != KindKyIdentity {
		saved = nil
	}
	pick := func(envValue, key string) Field {
		switch {
		case envValue != "":
			return Field{envValue, SourceEnvironment}
		case saved[key] != "":
			return Field{saved[key], SourceSaved}
		}
		return Field{"", SourceUnset}
	}
	st := Settings{
		Provider:    pick("", KeyProvider),
		DisplayName: pick("", KeyDisplayName),
		Issuer:      pick(strings.TrimRight(env.KySignOnIssuer, "/"), KeyIssuer),
		ClientID:    pick(env.KySignOnClientID, KeyClientID),
	}
	secret := pick(env.KySignOnSecret, KeySecretSealed)
	st.Secret = SecretField{Value: secret.Value, Source: secret.Source}
	if st.Secret.Source == SourceSaved {
		st.Secret.Value = ""
	}
	if envKy {
		st.Provider = Field{KindKyIdentity, SourceEnvironment}
	}
	if st.Provider.Value == "" {
		st.Provider = Field{KindNone, SourceUnset}
	}
	if st.DisplayName.Value == "" && st.Provider.Value == KindKyIdentity {
		st.DisplayName.Value = "KyIdentity"
	}
	if st.Secret.Source == SourceSaved && !SavedSecretFits(saved, st) {
		st.Secret = SecretField{Source: SourceUnset}
	}
	return st
}

// SavedSecretFits reports whether the saved sealed secret was entered for st's registration:
// the same kind, exact issuer and client ID. A missing record never fits, so one registration's
// secret is never sent to another, whether an admin or the environment changed it.
func SavedSecretFits(saved map[string]string, st Settings) bool {
	return saved[KeySecretSealed] != "" && saved[KeySecretRegistration] == SecretRegistration(st.Provider.Value, st.Issuer.Value, st.ClientID.Value)
}

// SecretRegistration records the registration a secret belongs to: kind, exact issuer and
// client ID, as JSON so no two registrations share a record.
func SecretRegistration(kind, issuer, clientID string) string {
	b, _ := json.Marshal([3]string{kind, issuer, clientID})
	return string(b)
}

// IgnoredEnv names the environment variables Resolve ignores because KY_KYSIGNON_ISSUER is
// not set. Names only, never values.
func IgnoredEnv(env config.SSOConfig) []string {
	if env.KySignOnIssuer != "" {
		return nil
	}
	var names []string
	if env.KySignOnClientID != "" {
		names = append(names, "KY_KYSIGNON_CLIENT_ID")
	}
	if env.KySignOnSecret != "" {
		names = append(names, "KY_KYSIGNON_SECRET")
	}
	return names
}

// Live reports whether the settings name a provider people can sign in with: a kind, an issuer,
// a client ID and a secret for that registration.
func (st Settings) Live() bool {
	return st.Provider.Value != KindNone && st.Issuer.Value != "" && st.ClientID.Value != "" && st.Secret.Source != SourceUnset
}

// Identity is what account binding compares: the provider kind and its issuer.
func (st Settings) Identity() string { return st.Provider.Value + " " + st.Issuer.Value }

// Bound is the stored binding accounts belong to (an Identity), "" when nothing is bound yet.
// New SSO rows provisioned outside a login (SCIM, the directory webhook) are stamped with it.
func Bound(ctx context.Context, settings store.SettingsStore) (string, error) {
	bound, err := settings.GetSetting(ctx, KeyBound)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return bound, err
}

// AccountProviders lists the users.sso_provider values a kind's logins reach. A KyIdentity
// login also signs in as the SCIM row with its sub, so those rows are KyIdentity's too.
func AccountProviders(kind string) []string {
	switch kind {
	case KindKyIdentity:
		return []string{"kysignon", "scim"}
	case KindOIDC:
		return []string{"oidc"}
	}
	return nil
}

// SealSecret encrypts the client secret under a key derived for this one setting.
func SealSecret(master []byte, secret string) (string, error) {
	return crypto.EncryptAESGCM([]byte(secret), crypto.DeriveKey(master, SecretLabel))
}

// OpenSecret reverses SealSecret; a wrong key or any tampering fails.
func OpenSecret(master []byte, sealed string) (string, error) {
	plain, err := crypto.DecryptAESGCM(sealed, crypto.DeriveKey(master, SecretLabel))
	return string(plain), err
}

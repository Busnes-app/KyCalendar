package sso

import (
	"strings"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
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

// Settings is the effective sign-in configuration. Secret.Value is the plain secret once the
// caller has opened a saved one; it is never serialised.
type Settings struct {
	Provider, DisplayName, Issuer, ClientID, Secret Field
}

// Resolve merges the environment over saved settings. KY_KYSIGNON_ISSUER, _CLIENT_ID and
// _SECRET each win and lock their field, and any of them fixes the provider to kyidentity.
// KY_KYSIGNON_HMAC_SECRET only authenticates the directory webhook and leaves the provider
// alone: it must not turn a generic provider into one whose roles claim grants admin. A saved
// secret is reported as set but left sealed: Secret.Value is empty until the caller opens it.
func Resolve(env config.SSOConfig, saved map[string]string) Settings {
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
		Secret:      pick(env.KySignOnSecret, KeySecretSealed),
	}
	if st.Secret.Source == SourceSaved {
		st.Secret.Value = ""
	}
	if env.KySignOnIssuer != "" || env.KySignOnClientID != "" || env.KySignOnSecret != "" {
		st.Provider = Field{KindKyIdentity, SourceEnvironment}
	}
	if st.Provider.Value == "" {
		st.Provider = Field{KindNone, SourceUnset}
	}
	if st.DisplayName.Value == "" && st.Provider.Value == KindKyIdentity {
		st.DisplayName.Value = "KyIdentity"
	}
	return st
}

// Live reports whether the settings name a provider people can sign in with.
func (st Settings) Live() bool {
	return st.Provider.Value != KindNone && st.Issuer.Value != "" && st.ClientID.Value != ""
}

// Identity is what account binding compares: the provider kind and its issuer.
func (st Settings) Identity() string { return st.Provider.Value + " " + st.Issuer.Value }

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

func OpenSecret(master []byte, sealed string) (string, error) {
	plain, err := crypto.DecryptAESGCM(sealed, crypto.DeriveKey(master, SecretLabel))
	return string(plain), err
}

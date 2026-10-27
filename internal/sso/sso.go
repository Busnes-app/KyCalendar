package sso

import (
	"errors"
	"time"
)

var (
	ErrInvalidState       = errors.New("invalid or expired SSO state")
	ErrInvalidIDToken     = errors.New("invalid ID token")
	ErrProviderDisabled   = errors.New("SSO provider is not enabled")
	ErrInvalidLogoutToken = errors.New("invalid logout token")
)

// IdentityClaims holds the unified identity extracted from any SSO provider.
type IdentityClaims struct {
	Issuer            string `json:"-"`
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	// Roles are the token's `roles` claim: KyIdentity app roles for this client only.
	Roles    []string `json:"roles,omitempty"`
	Provider string   `json:"provider"` // "kysignon", "oidc", "saml"
	// SessionID is the ID token's `sid`; back-channel logout names the login by it.
	SessionID string `json:"sid,omitempty"`
}

// SSOState represents ephemeral state held during OAuth/OIDC authorization flow.
type SSOState struct {
	State        string    `json:"state"`
	Verifier     string    `json:"verifier"`
	Provider     string    `json:"provider"`
	RedirectBack string    `json:"redirect_back"`
	CreatedAt    time.Time `json:"created_at"`
}

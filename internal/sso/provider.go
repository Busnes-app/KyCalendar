package sso

import (
	"context"
	"github.com/Busnes-app/ky-primitives/applysetup"
	"net/http"

	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// Provider is the live sign-in provider. Saving sign-in settings replaces it whole; ID is new on
// every build, so a login started under one provider cannot finish under the next.
type Provider struct {
	CredentialFile string // immutable after construction; managed credentials are re-read per flow
	Kind           string // KindKyIdentity or KindOIDC
	DisplayName    string
	ID             string
	Binding        string // Settings.Identity: what the accounts it signs in are stamped with
	flow           *oauthFlow
}

// NewProvider builds a provider; a nil client uses the default transport.
func NewProvider(kind, displayName, issuer, clientID, secret string, client *http.Client) *Provider {
	return &Provider{Kind: kind, DisplayName: displayName, ID: crypto.RandomHex(8), Binding: kind + " " + issuer, flow: newOAuthFlow(issuer, clientID, secret, client)}
}

// AuthURL is the authorization request with PKCE S256 and the nonce.
func (p *Provider) AuthURL(ctx context.Context, redirectURI, state, verifier, nonce string) (string, error) {
	flow, err := p.currentFlow()
	if err != nil {
		return "", err
	}
	return flow.authCodeURL(ctx, redirectURI, state, verifier, nonce)
}

// Exchange redeems the code and returns the verified claims, labelled with the account provider.
func (p *Provider) Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (*IdentityClaims, error) {
	flow, err := p.currentFlow()
	if err != nil {
		return nil, err
	}
	claims, err := flow.exchange(ctx, code, verifier, redirectURI, nonce)
	if err != nil {
		return nil, err
	}
	return p.stamp(claims), nil
}

// VerifyLogout verifies a back-channel logout token this provider signed for this client.
// Every token failure is ErrInvalidLogoutToken; any other error means the provider is unusable.
func (p *Provider) VerifyLogout(ctx context.Context, raw string) (*LogoutClaims, error) {
	flow, err := p.currentFlow()
	if err != nil {
		return nil, err
	}
	return flow.verifyLogout(ctx, raw)
}

// stamp sets the account provider. Only KyIdentity's roles mean anything here: an oidc
// provider's roles are dropped, so no outside IdP can make an administrator.
func (p *Provider) stamp(c *IdentityClaims) *IdentityClaims {
	c.Provider = p.accountProvider()
	if p.Kind != KindKyIdentity {
		c.Roles = nil
	}
	return c
}

// accountProvider is users.sso_provider for this provider's accounts.
func (p *Provider) accountProvider() string {
	if p.Kind == KindKyIdentity {
		return "kysignon"
	}
	return "oidc"
}

func (p *Provider) currentFlow() (*oauthFlow, error) {
	if p.CredentialFile == "" {
		return p.flow, nil
	}
	secret, err := applysetup.Secret(p.CredentialFile)
	if err != nil {
		return nil, err
	}
	return p.flow.withSecret(secret), nil
}

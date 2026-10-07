package sso

import (
	"context"
	"net/http"

	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// Provider is the live sign-in provider. Saving sign-in settings replaces it whole; ID is new on
// every build, so a login started under one provider cannot finish under the next.
type Provider struct {
	Kind        string // KindKyIdentity or KindOIDC
	DisplayName string
	ID          string
	flow        *oauthFlow
}

// NewProvider builds a provider; a nil client uses the default transport.
func NewProvider(kind, displayName, issuer, clientID, secret string, client *http.Client) *Provider {
	return &Provider{Kind: kind, DisplayName: displayName, ID: crypto.RandomHex(8), flow: newOAuthFlow(issuer, clientID, secret, client)}
}

// AuthURL is the authorization request with PKCE S256 and the nonce.
func (p *Provider) AuthURL(ctx context.Context, redirectURI, state, verifier, nonce string) (string, error) {
	return p.flow.authCodeURL(ctx, redirectURI, state, verifier, nonce)
}

// Exchange redeems the code and returns the verified claims, labelled with the account provider.
func (p *Provider) Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (*IdentityClaims, error) {
	claims, err := p.flow.exchange(ctx, code, verifier, redirectURI, nonce)
	if err != nil {
		return nil, err
	}
	return p.stamp(claims), nil
}

// stamp sets the account provider. Only KyIdentity's roles mean anything here: an oidc
// provider's roles are dropped, so no outside IdP can make an administrator.
func (p *Provider) stamp(c *IdentityClaims) *IdentityClaims {
	if p.Kind == KindKyIdentity {
		c.Provider = "kysignon"
		return c
	}
	c.Provider, c.Roles = "oidc", nil
	return c
}

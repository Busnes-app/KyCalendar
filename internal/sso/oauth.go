package sso

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oauthFlow struct {
	issuer       string
	clientID     string
	clientSecret string
	client       *http.Client // nil: the default transport
	mu           sync.Mutex
	provider     *oidc.Provider
}

func newOAuthFlow(issuer, clientID, clientSecret string, client *http.Client) *oauthFlow {
	return &oauthFlow{issuer: issuer, clientID: clientID, clientSecret: clientSecret, client: client}
}

// withClient routes discovery, the token exchange and key fetches through f.client: go-oidc and
// oauth2 both read it from the context's oauth2.HTTPClient value.
func (f *oauthFlow) withClient(ctx context.Context) context.Context {
	if f.client == nil {
		return ctx
	}
	return oidc.ClientContext(ctx, f.client)
}

func (f *oauthFlow) getProvider(ctx context.Context) (*oidc.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provider != nil {
		return f.provider, nil
	}
	provider, err := oidc.NewProvider(f.withClient(ctx), f.issuer)
	if err != nil {
		return nil, err
	}
	f.provider = provider
	return provider, nil
}

func (f *oauthFlow) config(ctx context.Context, redirectURI string) (*oauth2.Config, *oidc.Provider, error) {
	if f.issuer == "" || f.clientID == "" {
		return nil, nil, ErrProviderDisabled
	}
	provider, err := f.getProvider(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &oauth2.Config{ClientID: f.clientID, ClientSecret: f.clientSecret, Endpoint: provider.Endpoint(), RedirectURL: redirectURI, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, provider, nil
}

func (f *oauthFlow) authCodeURL(ctx context.Context, redirectURI, state, verifier, nonce string) (string, error) {
	config, _, err := f.config(ctx, redirectURI)
	if err != nil {
		return "", err
	}
	return config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce)), nil
}

func (f *oauthFlow) exchange(ctx context.Context, code, verifier, redirectURI, expectedNonce string) (*IdentityClaims, error) {
	config, provider, err := f.config(ctx, redirectURI)
	if err != nil {
		return nil, err
	}
	token, err := config.Exchange(f.withClient(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, err
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, errors.New("missing id_token in token response")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: f.clientID}).Verify(f.withClient(ctx), rawIDToken)
	if err != nil || expectedNonce == "" || idToken.Nonce != expectedNonce {
		return nil, ErrInvalidIDToken
	}
	return claimsFromIDToken(idToken)
}

func claimsFromIDToken(idToken *oidc.IDToken) (*IdentityClaims, error) {
	var raw struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Roles             any    `json:"roles"`
	}
	if err := idToken.Claims(&raw); err != nil || raw.Sub == "" {
		return nil, ErrInvalidIDToken
	}
	username := raw.PreferredUsername
	if username == "" {
		username = raw.Email
	}
	if username == "" {
		username = raw.Sub
	}
	// The global `role` claim is deliberately not read: it is never a KyCalendar admin.
	return &IdentityClaims{Subject: raw.Sub, Email: raw.Email, Name: raw.Name, PreferredUsername: username, Roles: access.RoleValues(raw.Roles)}, nil
}

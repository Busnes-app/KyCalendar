package sso

import (
	"context"
	"crypto/fips140"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

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
	verifier     *oidc.IDTokenVerifier
}

func newOAuthFlow(issuer, clientID, clientSecret string, client *http.Client) *oauthFlow {
	return &oauthFlow{issuer: issuer, clientID: clientID, clientSecret: clientSecret, client: client}
}

// withClient routes discovery, the token exchange and key fetches through f.client: go-oidc and
// oauth2 both read it from the context's oauth2.HTTPClient value.
func (f *oauthFlow) withClient(ctx context.Context) context.Context {
	if f.client != nil {
		ctx = oidc.ClientContext(ctx, f.client)
	}
	return strongOIDCContext(ctx)
}

func (f *oauthFlow) getProvider(ctx context.Context) (*oidc.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provider != nil {
		return f.provider, nil
	}
	if fips140.Enabled() {
		if err := requireOIDCHTTPS(f.issuer); err != nil {
			return nil, err
		}
	}
	ctx = f.withClient(ctx)
	provider, err := oidc.NewProvider(ctx, f.issuer)
	if err != nil {
		return nil, err
	}
	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, err
	}
	if fips140.Enabled() {
		if err := requireOIDCHTTPS(provider.Endpoint().AuthURL, provider.Endpoint().TokenURL, metadata.JWKSURI); err != nil {
			return nil, err
		}
	}
	client := http.DefaultClient
	if configured, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		client = configured
	}
	jwksClient := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	jwksClient.Transport = strongJWKSTransport{base: transport}
	if jwksClient.Timeout == 0 {
		jwksClient.Timeout = 20 * time.Second
	}
	// Initialize with discovery under the same lock; the upstream remote key
	// set retains its normal rotation/cache behavior behind this input guard.
	f.verifier = provider.VerifierContext(oidc.ClientContext(ctx, &jwksClient), &oidc.Config{ClientID: f.clientID})
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
	config, _, err := f.config(ctx, redirectURI)
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
	idToken, err := f.verifier.Verify(f.withClient(ctx), rawIDToken)
	if err != nil || expectedNonce == "" || idToken.Nonce != expectedNonce {
		return nil, ErrInvalidIDToken
	}
	return claimsFromIDToken(idToken)
}

func requireOIDCHTTPS(endpoints ...string) error {
	for _, raw := range endpoints {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("strong-crypto mode requires HTTPS OIDC issuer and endpoints without URL credentials")
		}
	}
	return nil
}

// Preserve the caller's roots and destination policy while refusing redirects
// from any discovery/token/key request in the supported process profile.
func strongOIDCContext(ctx context.Context) context.Context {
	if !fips140.Enabled() {
		return ctx
	}
	client := http.DefaultClient
	if configured, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		client = configured
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("OIDC redirects are refused in strong-crypto mode")
	}
	if copy.Timeout == 0 {
		copy.Timeout = 20 * time.Second
	}
	return oidc.ClientContext(ctx, &copy)
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
	return &IdentityClaims{Issuer: idToken.Issuer, Subject: raw.Sub, Email: raw.Email, Name: raw.Name, PreferredUsername: username, Roles: access.RoleValues(raw.Roles)}, nil
}

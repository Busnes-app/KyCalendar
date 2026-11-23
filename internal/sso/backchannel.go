package sso

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/oidcverify"
)

const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// logoutLeeway is the clock skew a logout token's iat may show, as oidcverify allows.
const logoutLeeway = time.Minute

// LogoutClaims names the login an OpenID Connect Back-Channel Logout token ended. SessionID is
// empty when the provider ended every login of Subject issued by IssuedAt.
type LogoutClaims struct {
	Subject   string
	SessionID string
	IssuedAt  time.Time
	OrgID     string // KyIdentity sends org_id in multi-tenant mode only
	// JWTID and ReplayUntil feed the receiver's replay ledger: the token is accepted at most
	// once, and only until ReplayUntil.
	JWTID       string
	ReplayUntil time.Time
}

// verifyLogout checks a logout token per OpenID Connect Back-Channel Logout 1.0 §2.6: the ID
// token checks (signature, issuer, audience, expiry), then the claims that keep an ID token
// from passing as one. Like oidcverify.VerifyLogout it requires a jti and an iat neither in the
// future nor older than oidcverify.MaxLogoutTokenAge, both with logoutLeeway.
func (f *oauthFlow) verifyLogout(ctx context.Context, raw string) (*LogoutClaims, error) {
	if _, _, err := f.config(ctx, ""); err != nil {
		return nil, err
	}
	token, err := f.verifier.Verify(f.withClient(ctx), raw)
	if err != nil {
		return nil, ErrInvalidLogoutToken
	}
	if typ, err := jwtType(raw); err != nil || (typ != "" && typ != "logout+jwt" && typ != "application/logout+jwt") {
		return nil, ErrInvalidLogoutToken
	}
	var claims struct {
		Sub    string                     `json:"sub"`
		JTI    string                     `json:"jti"`
		SID    string                     `json:"sid"`
		IAT    int64                      `json:"iat"`
		Nonce  json.RawMessage            `json:"nonce"` // not a pointer: a null nonce is present too
		Events map[string]json.RawMessage `json:"events"`
		OrgID  string                     `json:"org_id"`
	}
	if err := token.Claims(&claims); err != nil || claims.Nonce != nil || claims.IAT == 0 || claims.JTI == "" || (claims.Sub == "" && claims.SID == "") {
		return nil, ErrInvalidLogoutToken
	}
	now, iat := time.Now(), time.Unix(claims.IAT, 0)
	until := iat.Add(oidcverify.MaxLogoutTokenAge + logoutLeeway)
	if exp := token.Expiry.Add(logoutLeeway); exp.Before(until) {
		until = exp
	}
	if iat.After(now.Add(logoutLeeway)) || !now.Before(until) {
		return nil, ErrInvalidLogoutToken
	}
	var event map[string]any
	if err := json.Unmarshal(claims.Events[backchannelLogoutEvent], &event); err != nil || event == nil {
		return nil, ErrInvalidLogoutToken
	}
	return &LogoutClaims{Subject: claims.Sub, SessionID: claims.SID, IssuedAt: iat.UTC(), OrgID: claims.OrgID, JWTID: claims.JTI, ReplayUntil: until}, nil
}

// jwtType reads the protected header's typ; call it only after the signature verified.
func jwtType(raw string) (string, error) {
	header, _, _ := strings.Cut(raw, ".")
	decoded, err := base64.RawURLEncoding.DecodeString(header)
	if err != nil {
		return "", err
	}
	var h struct {
		Typ string `json:"typ"`
	}
	err = json.Unmarshal(decoded, &h)
	return strings.ToLower(h.Typ), err
}

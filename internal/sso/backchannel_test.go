package sso_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/sso"
)

const logoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// One rejection case per logout-token rule (OpenID Connect Back-Channel Logout 1.0 §2.6).
func TestVerifyLogoutToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
				"kty": "RSA", "alg": "RS256", "kid": "fixture",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	ctx := context.Background()

	valid := func() map[string]any {
		return map[string]any{
			"iss": issuer, "aud": "client", "sub": "subject", "sid": "sid-1", "jti": "jti-1",
			"iat": time.Now().Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
			"events": map[string]any{logoutEvent: map[string]any{}},
		}
	}
	typed := func() map[string]string {
		return map[string]string{"alg": "RS256", "kid": "fixture", "typ": "logout+jwt"}
	}

	for _, kind := range []string{sso.KindKyIdentity, sso.KindOIDC} {
		p := sso.NewProvider(kind, "fixture", issuer, "client", "", idp.Client())
		got, err := p.VerifyLogout(ctx, signJWT(t, key, typed(), valid()))
		if err != nil || got.SessionID != "sid-1" || got.Subject != "subject" || got.IssuedAt.IsZero() {
			t.Fatalf("%s valid token: claims=%+v error=%v", kind, got, err)
		}
	}
	p := sso.NewProvider(sso.KindKyIdentity, "fixture", issuer, "client", "", idp.Client())
	subOnly := valid()
	delete(subOnly, "sid")
	appTyped := typed()
	appTyped["typ"] = "application/logout+jwt"
	for name, raw := range map[string]string{
		"untyped subject-wide": signJWT(t, key, map[string]string{"alg": "RS256", "kid": "fixture"}, subOnly),
		"media type typ":       signJWT(t, key, appTyped, valid()),
	} {
		if _, err := p.VerifyLogout(ctx, raw); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	type mutation func(h map[string]string, c map[string]any) *rsa.PrivateKey
	cases := map[string]mutation{
		"foreign key":    func(h map[string]string, c map[string]any) *rsa.PrivateKey { return other },
		"wrong audience": func(h map[string]string, c map[string]any) *rsa.PrivateKey { c["aud"] = "other"; return key },
		"wrong issuer": func(h map[string]string, c map[string]any) *rsa.PrivateKey {
			c["iss"] = "https://evil.example"
			return key
		},
		"expired": func(h map[string]string, c map[string]any) *rsa.PrivateKey {
			c["exp"] = time.Now().Add(-time.Minute).Unix()
			return key
		},
		"no exp":           func(h map[string]string, c map[string]any) *rsa.PrivateKey { delete(c, "exp"); return key },
		"no iat":           func(h map[string]string, c map[string]any) *rsa.PrivateKey { delete(c, "iat"); return key },
		"nonce (ID token)": func(h map[string]string, c map[string]any) *rsa.PrivateKey { c["nonce"] = "n"; return key },
		"null nonce":       func(h map[string]string, c map[string]any) *rsa.PrivateKey { c["nonce"] = nil; return key },
		"no events":        func(h map[string]string, c map[string]any) *rsa.PrivateKey { delete(c, "events"); return key },
		"other event": func(h map[string]string, c map[string]any) *rsa.PrivateKey {
			c["events"] = map[string]any{"x": map[string]any{}}
			return key
		},
		"event not object": func(h map[string]string, c map[string]any) *rsa.PrivateKey {
			c["events"] = map[string]any{logoutEvent: "x"}
			return key
		},
		"event null": func(h map[string]string, c map[string]any) *rsa.PrivateKey {
			c["events"] = map[string]any{logoutEvent: nil}
			return key
		},
		"no sid or sub": func(h map[string]string, c map[string]any) *rsa.PrivateKey {
			delete(c, "sid")
			delete(c, "sub")
			return key
		},
		"JWT typ":  func(h map[string]string, c map[string]any) *rsa.PrivateKey { h["typ"] = "JWT"; return key },
		"alg none": func(h map[string]string, c map[string]any) *rsa.PrivateKey { h["alg"] = "none"; return key },
	}
	raws := map[string]string{"garbage": "not.a.jwt", "empty": "", "two parts": "a.b", "ID token": signedIDToken(t, key, issuer)}
	for name, mutate := range cases {
		h, c := typed(), valid()
		raws[name] = signJWT(t, mutate(h, c), h, c)
	}
	for name, raw := range raws {
		t.Run(name, func(t *testing.T) {
			if got, err := p.VerifyLogout(ctx, raw); !errors.Is(err, sso.ErrInvalidLogoutToken) || got != nil {
				t.Fatalf("accepted: claims=%+v error=%v", got, err)
			}
		})
	}
}

func signJWT(t *testing.T, key *rsa.PrivateKey, header map[string]string, claims map[string]any) string {
	t.Helper()
	h, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	c, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

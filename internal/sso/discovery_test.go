package sso_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
)

func goodDoc(issuer string) map[string]any {
	return map[string]any{
		"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
		"jwks_uri": issuer + "/keys", "code_challenge_methods_supported": []string{"plain", "S256"},
	}
}

// tlsIssuer serves doc(issuer) as the discovery document of an httptest TLS server.
func tlsIssuer(t *testing.T, doc func(issuer string) map[string]any) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(doc(ts.URL))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// trusting is a guarded client that trusts httptest's certificate. httptest binds loopback, so
// tests that need a successful dial pass an allow-all policy.
func trusting(ts *httptest.Server, policy sso.AddrPolicy) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return sso.NewGuardedClient(policy, pool)
}

func allowAll(netip.Addr) error { return nil }

func TestDiscoverAcceptsAnExactHTTPSIssuer(t *testing.T) {
	ts := tlsIssuer(t, goodDoc)
	p, err := sso.Discover(context.Background(), trusting(ts, allowAll), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if p.Endpoint().AuthURL != ts.URL+"/authorize" {
		t.Fatalf("endpoint %+v", p.Endpoint())
	}
}

func TestDiscoverRefusals(t *testing.T) {
	good := tlsIssuer(t, goodDoc)
	wrongIssuer := tlsIssuer(t, func(string) map[string]any { return goodDoc("https://other.example") })
	noS256 := tlsIssuer(t, func(issuer string) map[string]any {
		d := goodDoc(issuer)
		delete(d, "code_challenge_methods_supported")
		return d
	})
	httpEndpoint := func(field string) *httptest.Server {
		return tlsIssuer(t, func(issuer string) map[string]any {
			d := goodDoc(issuer)
			d[field] = "http" + strings.TrimPrefix(d[field].(string), "https")
			return d
		})
	}
	httpAuth, httpToken, httpKeys := httpEndpoint("authorization_endpoint"), httpEndpoint("token_endpoint"), httpEndpoint("jwks_uri")
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, good.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	for _, tc := range []struct {
		name, issuer string
		client       *http.Client
		want         string
	}{
		{"plain http", "http://" + good.Listener.Addr().String(), trusting(good, allowAll), "https://"},
		{"issuer mismatch", wrongIssuer.URL, trusting(wrongIssuer, allowAll), `"https://other.example"`},
		{"no S256", noS256.URL, trusting(noS256, allowAll), "S256"},
		{"redirect", redirect.URL, trusting(redirect, allowAll), "redirect"},
		{"http authorization endpoint", httpAuth.URL, trusting(httpAuth, allowAll), "authorization_endpoint"},
		{"http token endpoint", httpToken.URL, trusting(httpToken, allowAll), "token_endpoint"},
		{"http jwks", httpKeys.URL, trusting(httpKeys, allowAll), "jwks_uri"},
		{"loopback", good.URL, trusting(good, sso.RefuseLocal), "loopback or link-local"},
		{"cloud metadata", "https://169.254.169.254", trusting(good, sso.RefuseLocal), "loopback or link-local"},
		{"link-local v6", "https://[fe80::1]", trusting(good, sso.RefuseLocal), "loopback or link-local"},
		{"mapped metadata", "https://[::ffff:169.254.169.254]", trusting(good, sso.RefuseLocal), "loopback or link-local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sso.Discover(context.Background(), tc.client, tc.issuer)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// A name and a mapped address that reach loopback are refused at the dial, after DNS: the
// server never sees a request.
func TestDiscoverRefusesLoopbackAfterResolution(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	t.Cleanup(ts.Close)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	for _, host := range []string{"localhost", "[::ffff:127.0.0.1]", "127.0.0.1"} {
		issuer := "https://" + net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
		_, err := sso.Discover(context.Background(), trusting(ts, sso.RefuseLocal), issuer)
		if err == nil || !strings.Contains(err.Error(), "loopback or link-local") {
			t.Fatalf("%s: got %v", issuer, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

// A discovery document may name endpoints elsewhere; the client refuses any that is not https.
func TestGuardedClientRefusesPlainHTTP(t *testing.T) {
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	if _, err := sso.NewGuardedClient(allowAll, nil).Get(plain.URL); err == nil {
		t.Fatal("an http:// request went through")
	}
}

func TestRefuseLocal(t *testing.T) {
	for addr, refused := range map[string]bool{
		"127.0.0.1": true, "::1": true, "169.254.169.254": true, "fe80::1": true, "::ffff:127.0.0.1": true, "::ffff:169.254.169.254": true,
		"0.0.0.0": true, "224.0.0.1": true, "fd00:ec2::254": true, "100.100.100.200": true,
		"10.0.0.5": false, "192.168.1.2": false, "172.16.0.1": false, "100.64.0.1": false, "203.0.113.7": false,
	} {
		if err := sso.RefuseLocal(netip.MustParseAddr(addr)); (err != nil) != refused {
			t.Errorf("%s: refused=%v, want %v", addr, err != nil, refused)
		}
	}
}

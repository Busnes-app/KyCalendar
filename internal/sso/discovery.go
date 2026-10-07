package sso

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	// ErrAddressRefused is a dial to a loopback, link-local, unspecified or multicast address.
	ErrAddressRefused = errors.New("address refused")
	errRedirect       = errors.New("redirect refused")
	errNotHTTPS       = errors.New("not an https URL")
)

// AddrPolicy decides whether a resolved address may be dialled.
type AddrPolicy func(netip.Addr) error

// RefuseLocal admits public and private LAN addresses and refuses loopback, link-local (cloud
// metadata answers at 169.254.169.254), unspecified and multicast ones.
func RefuseLocal(a netip.Addr) error {
	a = a.Unmap()
	if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return fmt.Errorf("%w: %s", ErrAddressRefused, a)
	}
	return nil
}

// NewGuardedClient is the HTTP client for identity providers an admin typed in: HTTPS only, no
// redirects, no proxy, bodies capped at 1 MiB, and policy applied to the address actually dialled
// (after DNS), so a name that resolves to the metadata service is refused too. nil roots means
// the system pool.
func NewGuardedClient(policy AddrPolicy, roots *x509.CertPool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		a, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		return policy(a.Unmap())
	}}
	transport := &http.Transport{
		DialContext:            dialer.DialContext,
		TLSClientConfig:        &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport:     httpsOnly{transport},
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
	}
}

// httpsOnly refuses any request that is not https, which covers endpoints a discovery document
// names as well as the issuer, and caps every response body.
type httpsOnly struct{ next http.RoundTripper }

func (h httpsOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return nil, errNotHTTPS
	}
	resp, err := h.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, 1<<20), resp.Body}
	return resp, nil
}

// Discover fetches issuer's discovery document through client and checks what sign-in relies
// on: an https issuer, the exact issuer string back, https authorization, token and JWKS
// endpoints, and PKCE S256. The error text is for the
// admin screen; it never carries the provider's response.
func Discover(ctx context.Context, client *http.Client, issuer string) (*oidc.Provider, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("the issuer must be an https:// URL with no user, query or fragment")
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, client), issuer)
	if err != nil {
		return nil, discoveryError(err)
	}
	var meta struct {
		Auth    string   `json:"authorization_endpoint"`
		Token   string   `json:"token_endpoint"`
		JWKS    string   `json:"jwks_uri"`
		Methods []string `json:"code_challenge_methods_supported"`
	}
	if err := p.Claims(&meta); err != nil {
		return nil, errors.New("could not read the provider's discovery document")
	}
	for _, e := range []struct{ name, raw string }{
		{"authorization_endpoint", meta.Auth}, {"token_endpoint", meta.Token}, {"jwks_uri", meta.JWKS},
	} {
		if u, err := url.Parse(e.raw); err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, fmt.Errorf("the provider's %s is not an https:// URL, which is refused", e.name)
		}
	}
	if !slices.Contains(meta.Methods, "S256") {
		return nil, errors.New("the provider does not list S256 in code_challenge_methods_supported; KyCalendar requires PKCE S256")
	}
	return p, nil
}

func discoveryError(err error) error {
	var mismatch *oidc.IssuerMismatchError
	switch {
	case errors.As(err, &mismatch) && len(mismatch.Discovered) <= 256:
		return fmt.Errorf("the provider names its issuer %q; enter exactly that", mismatch.Discovered)
	case errors.Is(err, ErrAddressRefused):
		return errors.New("the issuer resolves to a loopback or link-local address, which is refused")
	case errors.Is(err, errRedirect):
		return errors.New("the provider answered with a redirect, which is refused; enter the final issuer URL")
	case errors.Is(err, errNotHTTPS):
		return errors.New("the provider pointed at a URL that is not https, which is refused")
	}
	return errors.New("could not read a discovery document at the issuer's /.well-known/openid-configuration")
}

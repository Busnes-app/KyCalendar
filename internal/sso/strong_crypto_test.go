package sso_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/fips140"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	appcrypto "github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
)

func TestDirectoryWebhookKeyFloorBeforeMutation(t *testing.T) {
	st, err := boundStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := strings.Repeat("s", 15)
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	body, err := json.Marshal(sso.KySignOnSyncPayload{Event: "user.created", ID: "short-key", Username: "short-key-user", Timestamp: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HandleSyncWebhook(context.Background(), body, appcrypto.ComputeHMACSHA256(body, secret)); err == nil || !strings.Contains(err.Error(), "16") {
		t.Fatalf("short-key result: %v", err)
	}
	if _, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "short-key"); err == nil {
		t.Fatal("short-key webhook created a user")
	}
}

func TestOIDCStrongModeRequiresDirectHTTPS(t *testing.T) {
	for _, field := range []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri", "redirect"} {
		t.Run(field, func(t *testing.T) {
			var issuer string
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if field == "redirect" && r.URL.Path != "/discovery" {
					http.Redirect(w, r, "/discovery", http.StatusFound)
					return
				}
				metadata := goodDoc(issuer)
				if strings.HasSuffix(field, "endpoint") || field == "jwks_uri" {
					metadata[field] = "http://id.example.invalid/endpoint"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(metadata)
			})
			makeServer := httptest.NewTLSServer
			if field == "issuer" {
				makeServer = httptest.NewServer
			}
			idp := makeServer(handler)
			defer idp.Close()
			issuer = idp.URL
			p := sso.NewProvider(sso.KindOIDC, "fixture", issuer, "client", "", idp.Client())
			_, err := p.AuthURL(context.Background(), "https://app.example/callback", "state", "verifier", "nonce")
			if (err != nil) != fips140.Enabled() {
				t.Fatalf("configured=%v, error=%v", fips140.Enabled(), err)
			}
			if field == "issuer" && fips140.Enabled() && calls.Load() != 0 {
				t.Fatal("contacted a plaintext issuer in the strong profile")
			}
		})
	}
}

// The peer has a strong leaf, and a control client trusts that leaf directly.
// The product client instead trusts its CA: failure therefore proves client
// enforcement of the entire verified chain, including an explicit trust anchor.
func TestGuardedTLSChecksTrustedRootKeyFloor(t *testing.T) {
	for _, bits := range []int{1024, 2047, 2048} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			rootKey, err := rsa.GenerateKey(rand.Reader, bits)
			if err != nil {
				t.Fatal(err)
			}
			leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			root := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
			rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
			if err != nil {
				t.Fatal(err)
			}
			root, err = x509.ParseCertificate(rootDER)
			if err != nil {
				t.Fatal(err)
			}
			leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, rootKey)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) }))
			peer.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: leafKey}}}
			peer.StartTLS()
			defer peer.Close()
			control, err := peer.Client().Get(peer.URL)
			if err != nil {
				t.Fatalf("strong-leaf control: %v", err)
			}
			control.Body.Close()
			pool := x509.NewCertPool()
			pool.AddCert(root)
			client := sso.NewGuardedClient(allowAll, pool)
			defer client.CloseIdleConnections()
			before := calls.Load()
			resp, err := client.Get(peer.URL)
			if resp != nil {
				resp.Body.Close()
			}
			refuse := bits < 2048 && fips140.Enabled()
			if (err != nil) != refuse {
				t.Fatalf("RSA%d configured=%v: %v", bits, fips140.Enabled(), err)
			}
			if refuse && calls.Load() != before {
				t.Fatal("HTTP request crossed weak-root TLS")
			}
		})
	}
}

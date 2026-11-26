package sso_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Busnes-app/ky-primitives/pqtls"
)

func TestGuardedClientNegotiatesHybridPQTLS(t *testing.T) {
	var serverState atomic.Pointer[tls.ConnectionState]
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			state := *r.TLS
			serverState.Store(&state)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	ts.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	t.Cleanup(ts.Close)

	client := trusting(ts, allowAll)
	t.Cleanup(client.CloseIdleConnections)

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("guarded client GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if resp.TLS == nil {
		t.Fatal("client response missing TLS connection state")
	}
	if resp.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("client TLS version = %#x, want TLS 1.3 (%#x)", resp.TLS.Version, tls.VersionTLS13)
	}
	pqtls.RequireHybrid(t, *resp.TLS)

	gotServer := serverState.Load()
	if gotServer == nil {
		t.Fatal("server request missing TLS connection state")
	}
	pqtls.RequireHybrid(t, *gotServer)
}

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	officeRange = "203.0.113.0/24"
	officePeer  = "203.0.113.5:41000" // inside officeRange
	outsidePeer = "198.51.100.9:41000"
	networksURL = "/api/settings/rate-limit-networks"
)

func putNetworks(t *testing.T, srv *api.Server, session *http.Cookie, networks string) *httptest.ResponseRecorder {
	t.Helper()
	return adminDo(t, srv, session, "PUT", networksURL, map[string]string{"networks": networks})
}

func networksStatus(t *testing.T, w *httptest.ResponseRecorder) (networks, environment string, exempted float64) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out["networks"].(string), out["environment"].(string), out["exempted"].(float64)
}

func loginFrom(t *testing.T, srv *api.Server, peer, username, pass string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": pass})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = peer
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Code
}

// throttled reports whether 25 failing sign-ins from peer met the per-IP window.
func throttled(t *testing.T, srv *api.Server, peer string) bool {
	t.Helper()
	for range 25 {
		if limitStatus(t, srv, peer, "") == http.StatusTooManyRequests {
			return true
		}
	}
	return false
}

// An allowlisted office goes past the per-IP sign-in window; everyone else keeps it.
func TestExemptNetworkSkipsThePerIPLoginLimit(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, _, _ := setupTestServer(t)
	for i := 1; i <= 60; i++ {
		if code := limitStatus(t, srv, officePeer, ""); code != http.StatusUnauthorized {
			t.Fatalf("office attempt %d answered %d, want the wrong-password 401", i, code)
		}
	}
	for i := 1; i <= 20; i++ {
		if code := limitStatus(t, srv, outsidePeer, ""); code == http.StatusTooManyRequests {
			t.Fatalf("outside attempt %d throttled early", i)
		}
	}
	if code := limitStatus(t, srv, outsidePeer, ""); code != http.StatusTooManyRequests {
		t.Fatalf("outside attempt 21 answered %d, want 429", code)
	}
}

// The per-account-and-address failure window still closes for an office client.
func TestExemptNetworkKeepsThePerAccountLoginWindow(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	loginAs(t, srv, st, "carol", "user")
	for i := 1; i <= 10; i++ {
		if code := loginFrom(t, srv, officePeer, "carol", "WrongPass123!"); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i, code)
		}
	}
	if code := loginFrom(t, srv, officePeer, "carol", "SuperSecretPass123!"); code != http.StatusUnauthorized {
		t.Fatalf("exhausted account window from the office answered %d, want 401", code)
	}
}

// With no trusted proxy, X-Forwarded-For is the caller's own claim and buys no exemption.
func TestForgedForwardedForCannotClaimAnExemptNetwork(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	for range 20 {
		limitStatus(t, srv, outsidePeer, "203.0.113.5")
	}
	if code := limitStatus(t, srv, outsidePeer, "203.0.113.5"); code != http.StatusTooManyRequests {
		t.Fatalf("attempt 21 answered %d, want 429: a forged header claimed the office", code)
	}

	token := davUser(t, st, "alice", "user")
	for range 10 {
		davForwarded(srv, outsidePeer, "203.0.113.5", "alice", "kc_bad_bad")
	}
	if code := davForwarded(srv, outsidePeer, "203.0.113.5", "bob", token); code != http.StatusTooManyRequests {
		t.Fatalf("DAV after 10 failures with a forged office header answered %d, want 429", code)
	}
}

// Behind a trusted proxy the forwarded client is matched, and a client that prepends an
// office address is matched on the entry the proxy appended.
func TestExemptNetworkMatchesTheClientATrustedProxyReports(t *testing.T) {
	t.Setenv("KY_TRUSTED_PROXIES", "192.0.2.0/24")
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, _, _ := setupTestServer(t)
	for i := 1; i <= 30; i++ {
		if code := limitStatus(t, srv, "192.0.2.7:41000", "203.0.113.5"); code == http.StatusTooManyRequests {
			t.Fatalf("office client behind the proxy throttled at attempt %d", i)
		}
	}
	for range 20 {
		limitStatus(t, srv, "192.0.2.7:41000", "203.0.113.5, 198.51.100.9")
	}
	if code := limitStatus(t, srv, "192.0.2.7:41000", "203.0.113.5, 198.51.100.9"); code != http.StatusTooManyRequests {
		t.Fatalf("attempt 21 answered %d, want 429: a prepended office address was believed", code)
	}
}

// Password change: the office skips the per-IP window; the per-account one still applies.
func TestExemptNetworkPasswordChange(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	sessions := []*http.Cookie{loginAs(t, srv, st, "dave", "user"), loginAs(t, srv, st, "erin", "user"), loginAs(t, srv, st, "fred", "user")}
	change := func(session *http.Cookie, peer string) int {
		req := httptest.NewRequest("POST", "/api/auth/change-password", strings.NewReader(`{"current_password":"nope","new_password":"AnotherPass456!"}`))
		req.RemoteAddr = peer
		req.AddCookie(session)
		addCSRF(req, session)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}
	// 15 attempts from the office across three accounts, five each: past the per-IP 10.
	for i := range 15 {
		if code := change(sessions[i%3], officePeer); code == http.StatusTooManyRequests {
			t.Fatalf("office attempt %d throttled", i+1)
		}
	}
	if code := change(sessions[0], "203.0.113.6:41000"); code != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt for one account answered %d, want 429 from the per-account limit", code)
	}
}

func davForwarded(srv *api.Server, peer, xff, user, pass string) int {
	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	req.RemoteAddr = peer
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	req.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Code
}

// CalDAV from the office: one misconfigured device locks out only its own account there, the
// account's failures from the office stay bounded, and the per-user limit still applies.
func TestExemptNetworkDAVFailuresAreCountedPerAccount(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	alice := davUser(t, st, "alice", "user")
	bob := davUser(t, st, "bob", "user")

	for i := 1; i <= 15; i++ {
		code := davForwarded(srv, officePeer, "", "alice", "kc_bad_bad")
		if want := map[bool]int{true: http.StatusUnauthorized, false: http.StatusTooManyRequests}[i <= 10]; code != want {
			t.Fatalf("office failure %d for alice: %d, want %d", i, code, want)
		}
	}
	if code := davForwarded(srv, officePeer, "", "ALICE", alice); code != http.StatusTooManyRequests {
		t.Fatalf("alice from the office after 10 failures: %d, want 429", code)
	}
	if code := davForwarded(srv, officePeer, "", "bob", bob); code == http.StatusTooManyRequests || code == http.StatusUnauthorized {
		t.Fatalf("bob locked out by alice's device: %d", code)
	}

	// Outside the office every account still shares the address's ten failures.
	for range 10 {
		davForwarded(srv, outsidePeer, "", "nobody", "kc_bad_bad")
	}
	if code := davForwarded(srv, outsidePeer, "", "bob", bob); code != http.StatusTooManyRequests {
		t.Fatalf("outside after 10 failures: %d, want 429", code)
	}

	// Wrong secrets for bob's real token still count against bob, office or not.
	for i := range 50 {
		davForwarded(srv, fmt.Sprintf("203.0.113.%d:1", 100+i/5), "", "bob", bob+"x")
	}
	if code := davForwarded(srv, "203.0.113.200:1", "", "bob", bob); code != http.StatusTooManyRequests {
		t.Fatalf("bob after 50 wrong secrets from the office: %d, want 429", code)
	}
}

func mcpFrom(srv *api.Server, peer, token string) int {
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = peer
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Code
}

// MCP from the office: failures count per token, so a stale client locks out only itself.
func TestExemptNetworkMCPFailuresAreCountedPerToken(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	w := newMCPWorld(t)
	_, one := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	_, two := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)

	for range 12 {
		_, garbage, _, _ := apppass.Generate()
		if code := mcpFrom(w.srv, officePeer, garbage); code != http.StatusUnauthorized && code != http.StatusTooManyRequests {
			t.Fatalf("unknown token: %d", code)
		}
	}
	_, garbage, _, _ := apppass.Generate()
	if code := mcpFrom(w.srv, officePeer, garbage); code != http.StatusTooManyRequests {
		t.Fatalf("unknown tokens from the office after 12 failures: %d, want 429", code)
	}
	if code := mcpFrom(w.srv, officePeer, one); code != http.StatusOK {
		t.Fatalf("a valid token after unknown-token failures from the office: %d, want 200", code)
	}
	for range 10 {
		mcpFrom(w.srv, officePeer, one[:len(one)-4]+"zzzz")
	}
	if code := mcpFrom(w.srv, officePeer, one); code != http.StatusTooManyRequests {
		t.Fatalf("token one after 10 wrong secrets: %d, want 429", code)
	}
	if code := mcpFrom(w.srv, officePeer, two); code != http.StatusOK {
		t.Fatalf("token two locked out by token one: %d, want 200", code)
	}

	for range 10 {
		mcpFrom(w.srv, outsidePeer, garbage)
	}
	if code := mcpFrom(w.srv, outsidePeer, two); code != http.StatusTooManyRequests {
		t.Fatalf("outside after 10 failures: %d, want 429", code)
	}
}

// An admin's list takes effect at once, is stored canonically, audited, counted, and still
// there after a restart; the environment's list stays alongside it.
func TestAdminSetsExemptNetworks(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", "198.18.7.0/24")
	srv, st, cfg := setupTestServer(t)
	admin := loginAs(t, srv, st, "alice", "admin")

	networks, environment, exempted := networksStatus(t, do(t, srv, "GET", networksURL, admin))
	if networks != "" || environment != "198.18.7.0/24" || exempted != 0 {
		t.Fatalf("before: networks=%q environment=%q exempted=%v", networks, environment, exempted)
	}
	if networks, _, _ = networksStatus(t, putNetworks(t, srv, admin, " 203.0.113.77/24 , 2001:db8:1::/48")); networks != "203.0.113.0/24,2001:db8:1::/48" {
		t.Fatalf("stored %q, want the canonical list", networks)
	}
	if throttled(t, srv, officePeer) || throttled(t, srv, "198.18.7.50:41000") {
		t.Fatal("an allowlisted network was throttled")
	}
	if _, _, exempted = networksStatus(t, do(t, srv, "GET", networksURL, admin)); exempted != 50 {
		t.Fatalf("exempted = %v, want 50", exempted)
	}

	rows := auditRows(t, st, "admin.rate_limit_networks")
	if len(rows) != 1 || rows[0].UserID != "usr_alice" || rows[0].Details != `from="" to="203.0.113.0/24,2001:db8:1::/48"` {
		t.Fatalf("audit rows %+v", rows)
	}

	if restarted := api.NewServer(cfg, st); throttled(t, restarted, officePeer) {
		t.Fatal("the stored list was not loaded at startup")
	}

	// Clearing it restores the limit for the office; the environment's network stays exempt.
	if networks, _, _ = networksStatus(t, putNetworks(t, srv, admin, "")); networks != "" {
		t.Fatalf("cleared list reads %q", networks)
	}
	if !throttled(t, srv, "203.0.113.200:41000") {
		t.Fatal("a removed network is still exempt")
	}
	if throttled(t, srv, "198.18.7.51:41000") {
		t.Fatal("clearing the admin list dropped the environment's network")
	}
}

// A list that would switch the limit off widely is refused and nothing changes.
func TestExemptNetworksRefuseBroadOrInvalidRanges(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "alice", "admin")
	if w := putNetworks(t, srv, admin, officeRange); w.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/8", "2001:db8::/32", "office", "127.0.0.1", "203.0.113.0/24,nope"} {
		w := putNetworks(t, srv, admin, bad)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "netallow") {
			t.Errorf("PUT %q answered %d %s, want 400 with the reason", bad, w.Code, w.Body.String())
		}
	}
	if networks, _, _ := networksStatus(t, do(t, srv, "GET", networksURL, admin)); networks != officeRange {
		t.Fatalf("a refused list replaced the stored one: %q", networks)
	}
	if !throttled(t, srv, "10.9.9.9:41000") {
		t.Fatal("an address in a refused range was exempt")
	}
	if n := len(auditRows(t, st, "admin.rate_limit_networks")); n != 1 {
		t.Fatalf("%d audit rows, want 1: refusals change nothing", n)
	}
}

// A stored list that no longer parses is dropped at startup, never widened.
func TestUnreadableStoredExemptNetworksAreDropped(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", "198.18.7.0/24")
	_, st, cfg := setupTestServer(t)
	if err := st.Settings().SetSetting(context.Background(), "rate_limit_exempt_networks", "10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	srv := api.NewServer(cfg, st)
	if !throttled(t, srv, "10.9.9.9:41000") {
		t.Fatal("a too-broad stored list was honoured")
	}
	if throttled(t, srv, "198.18.7.9:41000") {
		t.Fatal("the environment's network was dropped with the stored list")
	}
}

// Changing the list needs a recent sign-in, like the other sign-in settings.
func TestExemptNetworksSaveNeedsStepUp(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	restore := api.SetStepUpWindowForTest(0)
	defer restore()
	w := putNetworks(t, srv, admin, officeRange)
	if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
		t.Fatalf("stale session: %d %s, want 403 reauth_required", w.Code, w.Body.String())
	}
	if v, err := st.Settings().GetSetting(context.Background(), "rate_limit_exempt_networks"); err == nil {
		t.Fatalf("a refused save stored %q", v)
	}
}

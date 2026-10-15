package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// postLogin sends one login from its own IPv4 address, so the per-IP limiter stays out of the way.
func postLogin(srv *api.Server, ip int, username, pass, captcha string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username, "password": pass, "captcha_token": captcha})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = fmt.Sprintf("192.0.2.%d:40000", ip)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func solvePoW(t *testing.T, srv *api.Server) string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/pow-challenge", nil))
	var c auth.PoWChallenge
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatalf("challenge: %d %s", w.Code, w.Body.String())
	}
	for n := 1; n <= c.MaxNumber; n++ {
		h := sha256.Sum256([]byte(c.Salt + strconv.Itoa(n)))
		if hex.EncodeToString(h[:]) == c.Challenge {
			sol, _ := json.Marshal(auth.PoWSolution{Algorithm: c.Algorithm, Salt: c.Salt, Challenge: c.Challenge, Number: n, MaxNumber: c.MaxNumber, ExpiresAt: c.ExpiresAt, Signature: c.Signature})
			return base64.StdEncoding.EncodeToString(sol)
		}
	}
	t.Fatal("no solution")
	return ""
}

func TestPoWSolutionIsSingleUse(t *testing.T) {
	srv, _, cfg := setupTestServer(t)
	cfg.Captcha.Provider = "pow"
	token := solvePoW(t, srv)
	if w := postLogin(srv, 1, "nobody", "WrongPass123!", token); w.Code != http.StatusUnauthorized {
		t.Fatalf("first use: %d %s", w.Code, w.Body.String())
	}
	if w := postLogin(srv, 2, "nobody", "WrongPass123!", token); w.Code != http.StatusForbidden {
		t.Fatalf("replayed solution: %d, want 403", w.Code)
	}
}

// A challenge signed with the bare session secret must not verify: PoW signs under its own key.
func TestPoWRefusesTheSessionSecretAsKey(t *testing.T) {
	srv, _, cfg := setupTestServer(t)
	cfg.Captcha.Provider = "pow"
	salt, n, maxN := "00112233445566778899aabbccddeeff", 1, 1
	h := sha256.Sum256([]byte(salt + strconv.Itoa(n)))
	challenge := hex.EncodeToString(h[:])
	exp := time.Now().Add(time.Minute).Unix()
	sig := crypto.ComputeHMACSHA256([]byte(fmt.Sprintf("%s:%s:%d:%d", salt, challenge, maxN, exp)), cfg.Security.SessionSecret)
	sol, _ := json.Marshal(auth.PoWSolution{Algorithm: "SHA-256", Salt: salt, Challenge: challenge, Number: n, MaxNumber: maxN, ExpiresAt: exp, Signature: sig})
	if w := postLogin(srv, 1, "nobody", "WrongPass123!", base64.StdEncoding.EncodeToString(sol)); w.Code != http.StatusForbidden {
		t.Fatalf("session-secret signature: %d, want 403", w.Code)
	}
}

// Unknown, wrong password, disabled and password-less accounts all answer the same 401.
func TestLoginFailuresLookAlike(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	const pass = "SuperSecretPass123!"
	hash, _ := password.Hash(pass)
	for _, u := range []*store.User{
		{ID: "usr_on", Username: "on", PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_off", Username: "off", PasswordHash: hash, Role: "user", Status: "inactive", SSOProvider: "local"},
		{ID: "usr_sso", Username: "sso", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "s"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	want := postLogin(srv, 1, "nobody", pass, "")
	if want.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d", want.Code)
	}
	for i, c := range []struct{ user, pass string }{{"on", "WrongPass123!"}, {"off", pass}, {"sso", pass}} {
		w := postLogin(srv, i+2, c.user, c.pass, "")
		if w.Code != want.Code || w.Body.String() != want.Body.String() {
			t.Errorf("%s: %d %s, want %d %s", c.user, w.Code, w.Body.String(), want.Code, want.Body.String())
		}
	}
}

// An unknown user name must cost a password derivation, or response time enumerates accounts.
func TestLoginUnknownUserCostsAPasswordCheck(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	hash, _ := password.Hash("SuperSecretPass123!")
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_k", Username: "known", PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	fastest := func(base int, user string) time.Duration {
		best := time.Hour
		for i := range 3 {
			start := time.Now()
			postLogin(srv, base+i, user, "WrongPass123!", "")
			best = min(best, time.Since(start))
		}
		return best
	}
	postLogin(srv, 99, "nobody", "WrongPass123!", "") // mints the dummy hash
	known, unknown := fastest(1, "known"), fastest(10, "nobody")
	if unknown < known/4 {
		t.Fatalf("unknown user took %v, known user %v", unknown, known)
	}
}

func TestLoginLocksAnAccountAfterTenFailures(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	const pass = "SuperSecretPass123!"
	hash, _ := password.Hash(pass)
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_v", Username: "victim", PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	// Successes never count against the account.
	for i := range 12 {
		if w := postLogin(srv, 100+i, "victim", pass, ""); w.Code != http.StatusOK {
			t.Fatalf("login %d: %d", i, w.Code)
		}
	}
	for i := range 10 {
		if w := postLogin(srv, i+1, "victim", "WrongPass123!", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, w.Code)
		}
	}
	if w := postLogin(srv, 50, "victim", pass, ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after 10 failures from 10 addresses: %d, want 429", w.Code)
	}
}

// A stolen session must not mint a CalDAV credential that outlives logout without a fresh sign-in.
func TestCreateAppPasswordRequiresStepUp(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	t.Cleanup(api.SetStepUpWindowForTest(-time.Second))
	w := doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "stolen", "scope": "dav"})
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusForbidden || body["code"] != "reauth_required" {
		t.Fatalf("stale session: %d %s", w.Code, w.Body.String())
	}
	if list, _ := st.AppPasswords().ListByUser(t.Context(), "usr_alice"); len(list) != 0 {
		t.Fatalf("created %d app passwords", len(list))
	}
}

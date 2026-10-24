package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/totp"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const mfaPassword = "SuperSecretPass123!" // loginAs's password

// mfaPost posts body to an enrolment route with session and its CSRF token.
func mfaPost(t *testing.T, srv *api.Server, session *http.Cookie, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(session)
	addCSRF(req, session)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func reload(t *testing.T, st store.Store, id string) *store.User {
	t.Helper()
	u, err := st.Users().GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// startEnrolment runs POST /api/auth/mfa/enroll and returns the secret it handed out.
func startEnrolment(t *testing.T, srv *api.Server, session *http.Cookie) string {
	t.Helper()
	w := mfaPost(t, srv, session, "/api/auth/mfa/enroll", map[string]string{"password": mfaPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("enroll: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_uri"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Secret == "" ||
		!strings.HasPrefix(out.URI, "otpauth://totp/") || !strings.Contains(out.URI, "secret="+out.Secret) {
		t.Fatalf("enroll returned %s", w.Body.String())
	}
	return out.Secret
}

// enrol takes session's account all the way to TOTP on, confirming with the previous step's
// code so the current step stays unspent for the sign-in that follows.
func enrol(t *testing.T, srv *api.Server, session *http.Cookie) (secret string, recovery []string) {
	t.Helper()
	secret = startEnrolment(t, srv, session)
	code, _ := totp.Code(secret, time.Now().Add(-totp.Period*time.Second))
	w := mfaPost(t, srv, session, "/api/auth/mfa/enroll/confirm", map[string]string{"code": code})
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Enabled bool     `json:"totp_enabled"`
		Codes   []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.Enabled || len(out.Codes) != 10 {
		t.Fatalf("confirm returned %s", w.Body.String())
	}
	return secret, out.Codes
}

// signIn posts the password as a script would and decodes the answer.
func signIn(t *testing.T, srv *api.Server, username string) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+username+`","password":"`+mfaPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out, w
}

func issuedSession(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

// The whole path a local user takes: start, confirm with a current code, and from then on the
// password alone no longer signs in.
func TestTOTPEnrolmentEndToEnd(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	session := loginAs(t, srv, st, "tess", "user")
	other := issuedSession(func() *httptest.ResponseRecorder { _, w := signIn(t, srv, "tess"); return w }())

	secret := startEnrolment(t, srv, session)
	u := reload(t, st, "usr_tess")
	if u.TOTPEnabled {
		t.Fatal("starting an enrolment turned TOTP on")
	}
	if strings.Contains(u.TOTPSecretEnc, secret) {
		t.Fatal("the secret is stored in the clear")
	}
	if opened, err := auth.OpenTOTPSecret(cfg.Security.EncryptionKey, u.TOTPSecretEnc); err != nil || opened != secret {
		t.Fatalf("stored secret does not open to the one handed out: %v", err)
	}
	// Sealed under the "kycalendar:totp" subkey, not the bare deployment key.
	if _, err := crypto.DecryptAESGCM(u.TOTPSecretEnc, cfg.Security.EncryptionKey); err == nil {
		t.Fatal("secret is sealed under the bare deployment key")
	}
	// A pending enrolment does not change sign-in.
	if out, _ := signIn(t, srv, "tess"); out["authenticated"] != true {
		t.Fatalf("pending enrolment changed sign-in: %v", out)
	}

	code, _ := totp.Code(secret, time.Now().Add(-totp.Period*time.Second))
	if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll/confirm", map[string]string{"code": code}); w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	if !reload(t, st, "usr_tess").TOTPEnabled {
		t.Fatal("confirm did not enable TOTP")
	}
	// Enabling ends every other session, keeps the caller's.
	if other == nil || createAppPassword(srv, other, "", auth.SessionCSRFToken(testSessionSecret, other.Value)) != http.StatusUnauthorized {
		t.Error("another session survived enabling TOTP")
	}
	sessionToken(t, srv, session)

	// From now on the password alone gets an MFA transaction, never a session.
	out, lw := signIn(t, srv, "tess")
	if out["mfa_required"] != true || out["authenticated"] == true || issuedSession(lw) != nil {
		t.Fatalf("enrolled account signed in on its password alone: %v", out)
	}
	mfaToken, _ := out["mfa_token"].(string)
	now, _ := totp.Code(secret, time.Now())
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/totp", strings.NewReader(`{"mfa_token":"`+mfaToken+`","code":"`+now+`"}`))
	req.Header.Set("Content-Type", "application/json")
	mw := httptest.NewRecorder()
	srv.ServeHTTP(mw, req)
	if mw.Code != http.StatusOK || issuedSession(mw) == nil {
		t.Fatalf("TOTP sign-in with a current code: %d %s", mw.Code, mw.Body.String())
	}
}

// A wrong code, a replayed one, or none never turns TOTP on.
func TestTOTPEnrolmentNeedsAValidCode(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "wade", "user")
	if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll/confirm", map[string]string{"code": "123456"}); w.Code != http.StatusConflict {
		t.Fatalf("confirm with nothing pending: got %d, want 409", w.Code)
	}
	secret := startEnrolment(t, srv, session)
	good, _ := totp.Code(secret, time.Now())
	wrong := "000000"
	if wrong == good {
		wrong = "111111"
	}
	stale, _ := totp.Code(secret, time.Now().Add(-5*time.Minute))
	for _, code := range []string{wrong, "", "12345", stale} {
		if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll/confirm", map[string]string{"code": code}); w.Code != http.StatusUnauthorized {
			t.Errorf("confirm with %q: got %d, want 401", code, w.Code)
		}
	}
	// Five tries per account in five minutes: guessing stops here, even with the right code.
	if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll/confirm", map[string]string{"code": good}); w.Code != http.StatusTooManyRequests {
		t.Errorf("sixth confirmation: got %d, want 429", w.Code)
	}
	if reload(t, st, "usr_wade").TOTPEnabled {
		t.Fatal("a bad code enrolled the account")
	}
	if out, _ := signIn(t, srv, "wade"); out["authenticated"] != true {
		t.Fatalf("failed confirmations changed sign-in: %v", out)
	}
}

// Starting an enrolment needs the password, and so does stopping one; the session alone (say
// one left open on a shared machine) can neither add a factor nor remove one.
func TestTOTPEnrolmentNeedsThePassword(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "pat", "user")
	for _, pw := range []string{"", "WrongPassword123!"} {
		if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll", map[string]string{"password": pw}); w.Code != http.StatusBadRequest && w.Code != http.StatusUnauthorized {
			t.Errorf("enroll with password %q: got %d", pw, w.Code)
		}
	}
	if u := reload(t, st, "usr_pat"); u.TOTPSecretEnc != "" {
		t.Fatal("enroll stored a secret without the password")
	}
}

// Re-enrolling while TOTP is on is refused and leaves the enrolled secret in place; the only
// way to a new authenticator is disable, which needs the password and a current code.
func TestTOTPReenrolmentCannotBypassTheFactor(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "rita", "user")
	secret, _ := enrol(t, srv, session)
	before := reload(t, st, "usr_rita").TOTPSecretEnc

	if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll", map[string]string{"password": mfaPassword}); w.Code != http.StatusConflict {
		t.Fatalf("re-enroll while on: got %d, want 409", w.Code)
	}
	if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll/confirm", map[string]string{"code": "123456"}); w.Code != http.StatusConflict {
		t.Fatalf("confirm while on: got %d, want 409", w.Code)
	}
	// Nor does the store let anything replace an enabled secret.
	if err := st.Users().SetPendingTOTP(context.Background(), "usr_rita", before, "attacker"); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("SetPendingTOTP on an enabled account: %v", err)
	}
	if u := reload(t, st, "usr_rita"); !u.TOTPEnabled || u.TOTPSecretEnc != before {
		t.Fatal("re-enrolment replaced the enrolled secret")
	}

	now, _ := totp.Code(secret, time.Now())
	for name, body := range map[string]map[string]string{
		"no code":        {"password": mfaPassword},
		"wrong code":     {"password": mfaPassword, "code": "000000"},
		"no password":    {"code": now},
		"wrong password": {"password": "WrongPassword123!", "code": now},
	} {
		if w := mfaPost(t, srv, session, "/api/auth/mfa/disable", body); w.Code != http.StatusBadRequest && w.Code != http.StatusUnauthorized {
			t.Errorf("disable with %s: got %d", name, w.Code)
		}
	}
	if !reload(t, st, "usr_rita").TOTPEnabled {
		t.Fatal("TOTP was turned off without password and code")
	}
	if out, _ := signIn(t, srv, "rita"); out["mfa_required"] != true {
		t.Fatalf("enrolled account signed in on its password alone: %v", out)
	}
}

// Disabling with the password and a recovery code turns TOTP off, clears the secret and codes,
// and lets the account enrol a new authenticator.
func TestTOTPDisableThenReenrol(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "dina", "user")
	_, recovery := enrol(t, srv, session)
	if w := mfaPost(t, srv, session, "/api/auth/mfa/disable", map[string]string{"password": mfaPassword, "code": recovery[0]}); w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	u := reload(t, st, "usr_dina")
	if u.TOTPEnabled || u.TOTPSecretEnc != "" || u.RecoveryCodesHash != "[]" {
		t.Fatalf("disable left TOTP state: enabled=%v secret=%q codes=%s", u.TOTPEnabled, u.TOTPSecretEnc, u.RecoveryCodesHash)
	}
	if out, _ := signIn(t, srv, "dina"); out["authenticated"] != true {
		t.Fatalf("after disable, sign-in: %v", out)
	}
	startEnrolment(t, srv, session)
}

// Enrolment routes are session routes: the /api/auth/mfa/ prefix they share with the
// pre-session sign-in steps does not exempt them from the session's CSRF token.
func TestTOTPEnrolmentRoutesNeedTheSessionCSRFToken(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "cora", "user")
	other := loginAs(t, srv, st, "otto", "user")
	for _, path := range []string{"/api/auth/mfa/enroll", "/api/auth/mfa/enroll/confirm", "/api/auth/mfa/disable"} {
		for name, token := range map[string]string{
			"no token":            "",
			"another session's":   auth.SessionCSRFToken(testSessionSecret, other.Value),
			"a pre-session token": preSessionToken(t, srv),
		} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"password":"`+mfaPassword+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(session)
			if token != "" {
				req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
				req.Header.Set(auth.HeaderCSRF, token)
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s with %s: got %d, want 403", path, name, w.Code)
			}
		}
	}
	if reload(t, st, "usr_cora").TOTPSecretEnc != "" {
		t.Fatal("a forged request started an enrolment")
	}
}

// An SSO account's second factor belongs to its identity provider.
func TestTOTPEnrolmentRefusesSSOAccounts(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_sso", Username: "sso", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-sso"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: crypto.SHA256Hex([]byte("tok-sso")), UserID: "usr_sso", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, store.Credentials{PasswordHash: ""}); err != nil {
		t.Fatal(err)
	}
	session := &http.Cookie{Name: auth.SessionCookieName, Value: "tok-sso"}
	for _, path := range []string{"/api/auth/mfa/enroll", "/api/auth/mfa/enroll/confirm", "/api/auth/mfa/disable"} {
		if w := mfaPost(t, srv, session, path, map[string]string{"password": "x", "code": "123456"}); w.Code != http.StatusConflict {
			t.Errorf("%s for an SSO account: got %d, want 409", path, w.Code)
		}
	}
}

// Wrong passwords on disable count per account and address, so a session without the password
// cannot use up the owner's attempts; code guesses, which need the password, stay limited.
func TestTOTPDisableLimits(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "lena", "user")
	_, recovery := enrol(t, srv, session)
	disableFrom := func(addr, password, code string) int {
		data, _ := json.Marshal(map[string]string{"password": password, "code": code})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/disable", bytes.NewReader(data))
		req.RemoteAddr = addr
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(session)
		addCSRF(req, session)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		if code := disableFrom("198.51.100.7:1000", "wrong", recovery[0]); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i, code)
		}
	}
	if code := disableFrom("198.51.100.7:1000", "wrong", recovery[0]); code != http.StatusTooManyRequests {
		t.Fatalf("sixth wrong password from one address: %d", code)
	}
	// Correct passwords are refunded; wrong codes are not.
	for i := 0; i < 5; i++ {
		if code := disableFrom("203.0.113.9:1000", mfaPassword, "not-a-code"); code != http.StatusUnauthorized {
			t.Fatalf("wrong code %d: %d", i, code)
		}
	}
	if code := disableFrom("203.0.113.9:1000", mfaPassword, recovery[0]); code != http.StatusTooManyRequests {
		t.Fatalf("sixth code guess: %d", code)
	}
	if !reload(t, st, "usr_lena").TOTPEnabled {
		t.Fatal("TOTP went off past the limits")
	}
}

// Enrolment and disabling have separate limits: wrong passwords on one do not lock the other.
func TestTOTPEnrolLimitIsSeparateFromDisable(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "nora", "user")
	for i := 0; i < 6; i++ {
		mfaPost(t, srv, session, "/api/auth/mfa/enroll", map[string]string{"password": "wrong"})
	}
	if w := mfaPost(t, srv, session, "/api/auth/mfa/enroll", map[string]string{"password": mfaPassword}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("enrol after six wrong passwords: %d", w.Code)
	}
	if w := mfaPost(t, srv, session, "/api/auth/mfa/disable", map[string]string{"password": mfaPassword, "code": "x"}); w.Code != http.StatusConflict {
		t.Fatalf("disable shares enrolment's limit: %d %s", w.Code, w.Body.String())
	}
}

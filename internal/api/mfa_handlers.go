package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// recoveryCodeCount is how many single-use recovery codes an enrolment hands out.
const recoveryCodeCount = 10

type mfaManageRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// decodeMFA reads the body of an enrolment route, or writes 400 and returns false.
func (s *Server) decodeMFA(w http.ResponseWriter, r *http.Request, req *mfaManageRequest) bool {
	if err := json.NewDecoder(r.Body).Decode(req); err != nil || len(req.Password) > 1024 || len(req.Code) > 64 {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	return true
}

// mfaAccount refuses accounts that do not sign in with a local password: TOTP guards that
// sign-in only, and changing it needs the password.
func (s *Server) mfaAccount(w http.ResponseWriter, user *store.User) bool {
	if user.SSOProvider != "local" || user.PasswordHash == "" {
		s.writeError(w, http.StatusConflict, "Two-factor authentication is managed by your identity provider")
		return false
	}
	return true
}

// verifyPassword is the re-authentication every enrolment change needs: the session alone,
// say one left open on a shared machine, cannot add or remove a factor.
func (s *Server) verifyPassword(w http.ResponseWriter, user *store.User, plain string) bool {
	if plain == "" {
		s.writeError(w, http.StatusBadRequest, "Password is required")
		return false
	}
	if ok, err := password.Verify(plain, user.PasswordHash); err != nil || !ok {
		s.writeError(w, http.StatusUnauthorized, "Password or code is incorrect")
		return false
	}
	return true
}

// handleMFAEnroll starts an enrolment: a fresh secret, sealed and stored as pending (TOTP stays
// off until a code confirms it), returned once with its otpauth:// URI. An account that already
// has TOTP on is refused; the way to a new authenticator is disable (password and current code)
// then enrol.
func (s *Server) handleMFAEnroll(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	if !s.mfaAccount(w, user) {
		return
	}
	if !s.allowAccountAttempt("mfa-manage:"+user.ID, 5, 5*time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	var req mfaManageRequest
	if !s.decodeMFA(w, r, &req) {
		return
	}
	if user.TOTPEnabled {
		s.writeError(w, http.StatusConflict, "Two-factor authentication is already on; turn it off first")
		return
	}
	if !s.verifyPassword(w, user, req.Password) {
		return
	}
	s.refundAccountAttempt("mfa-manage:" + user.ID)
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to start enrolment")
		return
	}
	sealed, err := auth.SealTOTPSecret(s.config.Security.EncryptionKey, secret)
	if err == nil {
		err = s.store.Users().SetPendingTOTP(r.Context(), user.ID, user.TOTPSecretEnc, sealed)
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		s.writeError(w, http.StatusConflict, "Account changed; try again")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to start enrolment")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_uri": auth.GenerateTOTPURL(s.config.Server.AppName, user.Username, secret),
	})
}

// handleMFAEnrollConfirm turns TOTP on once a current code from the pending secret proves the
// authenticator holds it, and returns the recovery codes, once.
func (s *Server) handleMFAEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	if !s.mfaAccount(w, user) {
		return
	}
	if !s.allowAccountAttempt("mfa-confirm:"+user.ID, 5, 5*time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	var req mfaManageRequest
	if !s.decodeMFA(w, r, &req) {
		return
	}
	if user.TOTPEnabled || user.TOTPSecretEnc == "" {
		s.writeError(w, http.StatusConflict, "No enrolment is pending")
		return
	}
	secret, err := auth.OpenTOTPSecret(s.config.Security.EncryptionKey, user.TOTPSecretEnc)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to decrypt MFA secret")
		return
	}
	counter, ok := auth.ValidateTOTP(secret, req.Code)
	if !ok {
		s.writeError(w, http.StatusUnauthorized, "Invalid TOTP verification code")
		return
	}
	if err := s.store.Users().SpendTOTPCounter(r.Context(), user.ID, counter); err != nil {
		s.writeError(w, http.StatusUnauthorized, "TOTP code already used")
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate recovery codes")
		return
	}
	err = s.store.Users().EnableTOTP(r.Context(), user.ID, user.PasswordHash, user.TOTPSecretEnc, hashes, sessionOf(r.Context()).TokenHash, s.requestIP(r))
	if errors.Is(err, store.ErrAlreadyExists) || errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusConflict, "Enrolment changed; start again")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to enable two-factor authentication")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"totp_enabled": true, "recovery_codes": codes})
}

// handleMFADisable turns TOTP off. It needs the password and a current code or unused recovery
// code: the factor being removed must be proven, not just the session.
func (s *Server) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	if !s.mfaAccount(w, user) {
		return
	}
	if !s.allowAccountAttempt("mfa-manage:"+user.ID, 5, 5*time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	var req mfaManageRequest
	if !s.decodeMFA(w, r, &req) {
		return
	}
	if !user.TOTPEnabled {
		s.writeError(w, http.StatusConflict, "Two-factor authentication is off")
		return
	}
	if !s.verifyPassword(w, user, req.Password) {
		return
	}
	if !s.spendSecondFactor(r, user, req.Code) {
		s.writeError(w, http.StatusUnauthorized, "Password or code is incorrect")
		return
	}
	err := s.store.Users().DisableTOTP(r.Context(), user.ID, user.PasswordHash, sessionOf(r.Context()).TokenHash, s.requestIP(r))
	if errors.Is(err, store.ErrAlreadyExists) || errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusConflict, "Account changed; try again")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to disable two-factor authentication")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"totp_enabled": false})
}

// spendSecondFactor accepts a current TOTP code or an unused recovery code, spending it.
func (s *Server) spendSecondFactor(r *http.Request, user *store.User, code string) bool {
	if code == "" {
		return false
	}
	secret, err := auth.OpenTOTPSecret(s.config.Security.EncryptionKey, user.TOTPSecretEnc)
	if err != nil {
		log.Printf("totp: secret for %s unreadable: %v", user.ID, err)
	} else if counter, ok := auth.ValidateTOTP(secret, code); ok {
		return s.store.Users().SpendTOTPCounter(r.Context(), user.ID, counter) == nil
	}
	next, ok := auth.RedeemRecoveryCode(code, user.RecoveryCodesHash)
	return ok && s.store.Users().UpdateRecoveryCodes(r.Context(), user.ID, user.RecoveryCodesHash, next) == nil
}

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type LoginRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	CaptchaToken string `json:"captcha_token,omitempty"`
}

func (s *Server) handlePoWChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	challenge, err := auth.GeneratePoWChallenge(s.config.Captcha.DifficultyPoW, s.config.Security.SessionSecret)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate PoW challenge")
		return
	}

	s.writeJSON(w, http.StatusOK, challenge)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !s.perIPExempt(r) && !s.allowAttempt("login:"+s.limitIP(r), 20, time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many login attempts")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		s.writeError(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	// Verify PoW CAPTCHA if enabled
	if s.config.Captcha.Provider == "pow" {
		if req.CaptchaToken == "" || !auth.VerifyPoWSolution(req.CaptchaToken, s.config.Security.SessionSecret, &s.powSpent) {
			s.writeError(w, http.StatusForbidden, "Security check failed. Please complete the CAPTCHA puzzle.")
			return
		}
	}

	user, err := s.store.Users().GetUserByUsername(r.Context(), req.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Authentication error")
		return
	}
	// Every failure is the same 401 and costs one password derivation, so neither body nor time
	// tells an unknown, password-less or disabled account from a wrong password.
	if err != nil || user.PasswordHash == "" {
		password.DummyVerify()
		s.writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	// Failures per account and client address (IPv6 by /64), so a stranger cannot keep someone
	// else locked out; a correct password is refunded. An exhausted window answers like a wrong
	// password, so the limit does not reveal which accounts exist.
	accountKey := "login:" + user.ID + "@" + s.limitIP(r)
	if !s.allowAccountAttempt(accountKey, 10, 15*time.Minute) {
		password.DummyVerify()
		s.writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if ok, err := password.Verify(req.Password, user.PasswordHash); err != nil || !ok {
		s.writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	s.refundAccountAttempt(accountKey)
	if user.Status != "active" {
		s.writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	// Issue active session
	_, _, err = s.sessions.IssueSession(r.Context(), w, r, user)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to issue session")
		return
	}

	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{
		UserID:   user.ID,
		Action:   "auth.login",
		Resource: "session",
	})

	s.writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          user,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	_ = s.sessions.RevokeSession(r.Context(), w, r)
	s.writeJSON(w, http.StatusOK, map[string]bool{"logged_out": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, sess, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		// The sign-in form that follows needs a CSRF token bound to this browser.
		s.sessions.IssuePreSession(w)
		s.writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
		return
	}
	s.sessions.RefreshCSRF(w, r, sess)

	s.writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          user,
	})
}

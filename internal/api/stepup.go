package api

import (
	"net/http"
	"time"
)

// stepUpWindow is how recently the session's credentials must have been verified for a step-up
// action: deleting a group calendar or a group, unpairing, and the People and Sign-in changes
// that can lock someone out.
var stepUpWindow = 10 * time.Minute

// requireStepUp reports whether the request's session was signed in within stepUpWindow. When it
// was not, it writes 403 reauth_required ("Sign in again to <what>") and returns false.
func (s *Server) requireStepUp(w http.ResponseWriter, r *http.Request, what string) bool {
	_, sess, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return false
	}
	if time.Since(sess.CreatedAt) > stepUpWindow {
		s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign in again to " + what, "code": "reauth_required"})
		return false
	}
	return true
}

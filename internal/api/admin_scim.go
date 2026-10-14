package api

import (
	"net/http"

	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// handleSCIMStatus reports whether SCIM provisioning is on, the URL the IdP connects to, and
// the accounts SCIM manages (its scope), newest first. Only whether a token is set ever leaves
// the server, never the token.
func (s *Server) handleSCIMStatus(w http.ResponseWriter, r *http.Request) {
	scope, err := scim.Scope(r.Context(), s.store.Settings())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the sign-in binding")
		return
	}
	offset, limit := listPage(r)
	users, total, err := s.store.Users().ListUsers(r.Context(), offset, limit, store.UserFilter{SCIM: &scope})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list people")
		return
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, userViewOf(u))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"enabled":          s.config.SCIM.Enabled,
		"token_configured": s.config.SCIM.BearerToken != "",
		"endpoint":         s.config.Server.AppURL + "/scim/v2",
		"users":            out,
		"total":            total,
	})
}

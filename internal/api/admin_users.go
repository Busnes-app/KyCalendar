package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// userView is a person as the admin screens see them: never a hash, a TOTP secret or a code.
type userView struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"display_name"`
	Email              string     `json:"email"`
	Role               string     `json:"role"`
	Status             string     `json:"status"`
	Source             string     `json:"source"`
	MFA                bool       `json:"mfa"`
	MustChangePassword bool       `json:"must_change_password"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

func userViewOf(u *store.User) userView {
	return userView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email, Role: u.Role, Status: u.Status,
		Source: u.SSOProvider, MFA: u.TOTPEnabled, MustChangePassword: u.MustChangePassword, LastLoginAt: u.LastLoginAt}
}

// handleListUsers pages people, newest first; q is a case-insensitive substring of username,
// email or display name.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	offset, limit := listPage(r)
	var filter store.UserFilter
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		if len(q) > 255 {
			s.writeError(w, http.StatusBadRequest, "Search is too long")
			return
		}
		filter = store.UserFilter{Field: store.UserFieldSearch, Value: q}
	}
	users, total, err := s.store.Users().ListUsers(r.Context(), offset, limit, filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list people")
		return
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, userViewOf(u))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"users": out, "total": total})
}

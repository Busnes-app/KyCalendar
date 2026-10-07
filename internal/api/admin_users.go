package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
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

// newTemporaryPassword returns a one-time password (120 random bits, 20 characters) and its
// hash. The plain text goes to the admin once, in the response; only the hash is stored.
func newTemporaryPassword() (plain, hash string, err error) {
	plain = crypto.RandomBase64URL(15)
	if err := auth.ValidatePassword(plain); err != nil {
		return "", "", err
	}
	hash, err = password.Hash(plain)
	return plain, hash, err
}

// adminUser loads the {id} person: 404 when missing; with local set, 409 managed_externally for
// an account an identity provider owns.
func (s *Server) adminUser(w http.ResponseWriter, r *http.Request, local bool) *store.User {
	u, err := s.store.Users().GetUserByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such person")
		return nil
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the person")
		return nil
	}
	if local && u.SSOProvider != "local" {
		s.writeManaged(w, "person")
		return nil
	}
	return u
}

// writeUsernameTaken answers a username another account already holds in some case.
func (s *Server) writeUsernameTaken(w http.ResponseWriter) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Another account already uses this username", "code": "name_taken"})
}

// usernameFree is false, with 409 name_taken written, when another account holds name in any
// case: a case twin would split one person's sign-in across two rows.
func (s *Server) usernameFree(w http.ResponseWriter, r *http.Request, name, self string) bool {
	other, err := s.store.Users().GetUserByUsername(r.Context(), name)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && other.ID == self):
		return true
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to check the username")
	default:
		s.writeUsernameTaken(w)
	}
	return false
}

// writeOnce sends a response carrying a one-time password; nothing may cache it.
func (s *Server) writeOnce(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, status, body)
}

// handleCreateUser adds a local person with a server-generated temporary password, returned
// once. Creating an administrator is a step-up action.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	body.Role = cmp.Or(body.Role, "user")
	if body.Role != "user" && body.Role != "admin" {
		s.writeError(w, http.StatusBadRequest, "Role must be user or admin")
		return
	}
	if body.Role == "admin" && !s.requireStepUp(w, r, "add an administrator") {
		return
	}
	body.Username, body.Email = strings.TrimSpace(body.Username), strings.TrimSpace(body.Email)
	if err := auth.ValidateUsername(body.Username); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidateEmail(body.Email); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	display, ok := cleanName(cmp.Or(strings.TrimSpace(body.DisplayName), body.Username))
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Display name must be 1-255 characters with no control characters")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if !s.usernameFree(w, r, body.Username, "") {
		return
	}
	plain, hash, err := newTemporaryPassword()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate a password")
		return
	}
	u := &store.User{ID: "usr_" + crypto.RandomHex(12), Username: body.Username, DisplayName: display, Email: body.Email,
		PasswordHash: hash, Role: body.Role, Status: "active", SSOProvider: "local", MustChangePassword: true}
	if err := s.store.Users().CreateUser(ctx, u); errors.Is(err, store.ErrAlreadyExists) {
		s.writeUsernameTaken(w)
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to create the person")
		return
	}
	s.auditAction(ctx, r, "admin.user_create", u.ID, "role="+u.Role)
	s.writeOnce(w, http.StatusCreated, map[string]any{"user": userViewOf(u), "temporary_password": plain})
}

// handleResetUserPassword sets a new temporary password on a local person, returned once; the
// store's operator reset revokes their sessions, MFA challenges and app passwords.
func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "reset a password") {
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	plain, hash, err := newTemporaryPassword()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate a password")
		return
	}
	if err := s.store.Users().ResetPassword(r.Context(), u.ID, hash); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to reset the password")
		return
	}
	s.auditAction(r.Context(), r, "admin.user_reset_password", u.ID, "")
	s.writeOnce(w, http.StatusOK, map[string]string{"temporary_password": plain})
}

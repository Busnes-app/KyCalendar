package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
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
	// BoundTo is an SSO account's sign-in binding (`<kind> <issuer>`, "" when unstamped).
	BoundTo string `json:"bound_to,omitempty"`
	// NeedsReattach: an SSO account the live sign-in reaches by kind but refuses, because it is
	// bound to another issuer (or none). One disabled under the live binding is the IdP's.
	NeedsReattach bool `json:"needs_reattach"`
}

func userViewOf(u *store.User) userView {
	v := userView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email, Role: u.Role, Status: u.Status,
		Source: u.SSOProvider, MFA: u.TOTPEnabled, MustChangePassword: u.MustChangePassword, LastLoginAt: u.LastLoginAt}
	if u.SSOProvider != "local" {
		v.BoundTo = u.SSOIssuer
	}
	return v
}

// viewWithSignIn is userViewOf plus whether p, the live provider (nil: none), needs a reattach.
func viewWithSignIn(u *store.User, p *sso.Provider) userView {
	v := userViewOf(u)
	v.NeedsReattach = p != nil && slices.Contains(sso.AccountProviders(p.Kind), u.SSOProvider) && u.SSOIssuer != p.Binding
	return v
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
	p := s.signin.Load()
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, viewWithSignIn(u, p))
	}
	binding := ""
	if p != nil {
		binding = p.Binding
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"users": out, "total": total, "signin_binding": binding})
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

// validEmail checks an optional email: syntax, and at most 255 bytes.
func validEmail(email string) error {
	if len(email) > 255 {
		return errors.New("Email must be at most 255 characters")
	}
	return auth.ValidateEmail(email)
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
	if err := validEmail(body.Email); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	display, ok := cleanName(cmp.Or(strings.TrimSpace(body.DisplayName), body.Username))
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Display name must be 1-255 characters with no control characters")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	plain, hash, err := newTemporaryPassword()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate a password")
		return
	}
	u := &store.User{ID: "usr_" + crypto.RandomHex(12), Username: body.Username, DisplayName: display, Email: body.Email,
		PasswordHash: hash, Role: body.Role, Status: "active", SSOProvider: "local", MustChangePassword: true}
	if err := s.store.Users().CreateUserAs(ctx, actorOf(ctx), u); errors.Is(err, store.ErrAlreadyExists) {
		s.writeUsernameTaken(w)
		return
	} else if errors.Is(err, store.ErrActorRevoked) {
		s.writeActorRevoked(w)
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
	if err := s.store.Users().ResetPassword(r.Context(), actorOf(r.Context()), u.ID, hash); errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such person")
		return
	} else if errors.Is(err, store.ErrActorRevoked) {
		s.writeActorRevoked(w)
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to reset the password")
		return
	}
	s.auditAction(r.Context(), r, "admin.user_reset_password", u.ID, "")
	s.writeOnce(w, http.StatusOK, map[string]string{"temporary_password": plain})
}

// handleUpdateUser changes a local person's username, display name or email. Every field is
// checked before any is written; the store refuses a case twin of another account.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    *string `json:"username"`
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	username, display, email := u.Username, u.DisplayName, u.Email
	if body.Username != nil {
		username = strings.TrimSpace(*body.Username)
		if err := auth.ValidateUsername(username); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.DisplayName != nil {
		var ok bool
		if display, ok = cleanName(*body.DisplayName); !ok {
			s.writeError(w, http.StatusBadRequest, "Display name must be 1-255 characters with no control characters")
			return
		}
	}
	if body.Email != nil {
		email = strings.TrimSpace(*body.Email)
		if err := validEmail(email); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var changed []string
	if username != u.Username {
		err := s.store.Users().RenameUser(r.Context(), sessionUser(r.Context()).ID, u.ID, username)
		switch {
		case errors.Is(err, store.ErrAlreadyExists):
			s.writeUsernameTaken(w)
			return
		case errors.Is(err, store.ErrNotFound):
			s.writeError(w, http.StatusNotFound, "No such person")
			return
		case err != nil:
			s.writeError(w, http.StatusInternalServerError, "Failed to rename the person")
			return
		}
		changed = append(changed, "username")
	}
	if display != u.DisplayName || email != u.Email {
		if err := s.store.Users().UpdateProfile(r.Context(), u.ID, display, email); err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to update the person")
			return
		}
		changed = append(changed, "profile")
	}
	if len(changed) > 0 {
		s.auditAction(r.Context(), r, "admin.user_update", u.ID, "fields="+strings.Join(changed, ","))
	}
	s.writeUser(w, r, u.ID)
}

// writeUser answers 200 with the {id} person as stored now.
func (s *Server) writeUser(w http.ResponseWriter, r *http.Request, id string) {
	u, err := s.store.Users().GetUserByID(r.Context(), id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the person")
		return
	}
	s.writeJSON(w, http.StatusOK, viewWithSignIn(u, s.signin.Load()))
}

// writeAccessError answers a refused SetRole or SetStatus.
func (s *Server) writeAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "This is the last active local administrator; make another one first", "code": "last_admin"})
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "No such person")
	case errors.Is(err, store.ErrActorRevoked):
		s.writeActorRevoked(w)
	default:
		s.writeError(w, http.StatusInternalServerError, "Failed to change the person")
	}
}

// writeActorRevoked answers a write whose administrator lost the role or the session mid-request.
func (s *Server) writeActorRevoked(w http.ResponseWriter) {
	s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Your administrator access ended; sign in again", "code": "actor_revoked"})
}

func (s *Server) writeSelf(w http.ResponseWriter, what string) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "You cannot " + what + " yourself; ask another administrator", "code": "self"})
}

// handleSetUserRole makes a local person an administrator or an everyday user. Sessions and app
// passwords are revoked with the change, so nothing runs under the old role.
func (s *Server) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "change a role") {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Role != "user" && body.Role != "admin") {
		s.writeError(w, http.StatusBadRequest, "Role must be user or admin")
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	if u.ID == sessionUser(r.Context()).ID {
		s.writeSelf(w, "change the role of")
		return
	}
	if u.Role != body.Role {
		if err := s.store.Users().SetRole(r.Context(), actorOf(r.Context()), u.ID, body.Role); err != nil {
			s.writeAccessError(w, err)
			return
		}
		s.auditAction(r.Context(), r, "admin.user_role", u.ID, "from="+u.Role+" to="+body.Role)
	}
	s.writeUser(w, r, u.ID)
}

// handleDisableUser deactivates a local person and revokes their sessions and app passwords.
func (s *Server) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "disable a person") {
		return
	}
	s.setUserStatus(w, r, "inactive", "admin.user_disable")
}

// handleEnableUser reactivates a local person; it can restore an administrator, so it is a
// step-up action like disable.
func (s *Server) handleEnableUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "enable a person") {
		return
	}
	s.setUserStatus(w, r, "active", "admin.user_enable")
}

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request, status, action string) {
	r = r.WithContext(context.WithoutCancel(r.Context()))
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	if u.ID == sessionUser(r.Context()).ID {
		s.writeSelf(w, "enable or disable")
		return
	}
	if u.Status != status {
		if err := s.store.Users().SetStatus(r.Context(), actorOf(r.Context()), u.ID, status); err != nil {
			s.writeAccessError(w, err)
			return
		}
		s.auditAction(r.Context(), r, action, u.ID, "")
	}
	s.writeUser(w, r, u.ID)
}

// answerBound answers a reattach of a person already bound to the live sign-in, writing nothing:
// 200 when active; 409 managed_externally when inactive, since their IdP disabled and restores them.
func (s *Server) answerBound(w http.ResponseWriter, r *http.Request, u *store.User) {
	if u.Status != "active" {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Your identity provider disabled this person; restore them there", "code": "managed_externally"})
		return
	}
	s.writeUser(w, r, u.ID)
}

// handleReattachUser binds an SSO person bound to another issuer to the live sign-in and
// reactivates them, revoking every grant so they sign in fresh: the one admin write on an identity
// provider's account. The admin vouches it is the same person and names the binding they
// confirmed; the role is re-proven at the next sign-in. A person disabled under the live binding
// was disabled by the IdP, which restores them.
func (s *Server) handleReattachUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "reattach a person") {
		return
	}
	var body struct {
		Binding string `json:"binding"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Binding == "" {
		s.writeError(w, http.StatusBadRequest, "Name the sign-in binding you confirmed")
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	// A save holds the write lock through its binding commit: the binding cannot move under us.
	s.signinMu.RLock()
	defer s.signinMu.RUnlock()
	u := s.adminUser(w, r, false)
	if u == nil {
		return
	}
	if u.SSOProvider == "local" {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Local accounts sign in with a password; there is nothing to reattach", "code": "local_account"})
		return
	}
	p := s.signin.Load()
	if p == nil {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "No sign-in provider is live; set one up under Sign-in first", "code": "no_signin"})
		return
	}
	if body.Binding != p.Binding {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "The sign-in provider changed since you confirmed; review the person again", "code": "binding_changed"})
		return
	}
	if !slices.Contains(sso.AccountProviders(p.Kind), u.SSOProvider) {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "The current sign-in provider never signs this account in", "code": "other_provider"})
		return
	}
	if u.SSOIssuer == p.Binding {
		s.answerBound(w, r, u)
		return
	}
	from, err := s.store.Users().ReattachSSOUser(r.Context(), actorOf(r.Context()), u.ID, p.Binding)
	if errors.Is(err, store.ErrAlreadyBound) {
		// Bound by a concurrent reattach since we read it: answer as if we had read it bound.
		if u, err = s.store.Users().GetUserByID(r.Context(), u.ID); err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to load the person")
			return
		}
		s.answerBound(w, r, u)
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "No such person")
		return
	case errors.Is(err, store.ErrActorRevoked):
		s.writeActorRevoked(w)
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to reattach the person")
		return
	}
	s.auditAction(r.Context(), r, "admin.user_reattach", u.ID, fmt.Sprintf("from=%q to=%q", from, p.Binding))
	s.writeUser(w, r, u.ID)
}

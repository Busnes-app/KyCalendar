package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const maxAppPasswordsPerUser = 20

// requireEveryday admits signed-in non-admin users; admin identities never use calendars.
func (s *Server) requireEveryday(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, sess := s.authenticate(w, r)
		if user == nil {
			return
		}
		if user.Role == "admin" {
			s.writeError(w, http.StatusForbidden, "Administrator accounts cannot use calendars")
			return
		}
		h(w, withSessionUser(r, user, sess))
	}
}

type appPasswordView struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func (s *Server) handleListAppPasswords(w http.ResponseWriter, r *http.Request) {
	user, _, _ := s.sessions.AuthenticateRequest(r)
	list, err := s.store.AppPasswords().ListByUser(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not list app passwords")
		return
	}
	out := make([]appPasswordView, 0, len(list))
	for _, p := range list {
		out = append(out, appPasswordView{ID: p.ID, Label: p.Label, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt})
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateAppPassword(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	sess, _ := r.Context().Value(sessionKey{}).(*store.Session)
	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || len([]rune(label)) > 64 {
		s.writeError(w, http.StatusBadRequest, "Label must be 1 to 64 characters")
		return
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create app password")
		return
	}
	p := &store.AppPassword{ID: id, UserID: user.ID, Label: label, Hash: hash}
	// The store rechecks the session under the user-row lock: a purge that committed since
	// authentication (a role or status change, a reattach) refuses the credential.
	switch err := s.store.AppPasswords().Create(r.Context(), store.SessionGrantor(sess.TokenHash, user.PasswordHash), p, maxAppPasswordsPerUser); {
	case errors.Is(err, store.ErrSessionExpired):
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeError(w, http.StatusConflict, "Revoke an app password before creating another")
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Could not create app password")
		return
	}
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "app_password.create", Resource: "app_password:" + id, IPAddress: s.requestIP(r)})
	s.writeJSON(w, http.StatusCreated, map[string]any{"id": id, "label": label, "password": token, "created_at": p.CreatedAt})
}

func (s *Server) handleDeleteAppPassword(w http.ResponseWriter, r *http.Request) {
	user, _, _ := s.sessions.AuthenticateRequest(r)
	id := r.PathValue("id")
	if err := s.store.AppPasswords().Delete(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "App password not found")
		} else {
			s.writeError(w, http.StatusInternalServerError, "Could not revoke app password")
		}
		return
	}
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "app_password.revoke", Resource: "app_password:" + id, IPAddress: s.requestIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

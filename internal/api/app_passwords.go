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

// mcpIdleLimit is how long an MCP token may go unused before it is revoked.
const mcpIdleLimit = 90 * 24 * time.Hour

type appPasswordView struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Scope       string     `json:"scope"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	RevokeAfter *time.Time `json:"revoke_after,omitempty"` // MCP tokens: revoked if unused by then
}

func viewOfAppPassword(p *store.AppPassword) appPasswordView {
	v := appPasswordView{ID: p.ID, Label: p.Label, Scope: p.Scope, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt}
	if store.IsMCPScope(p.Scope) {
		at := lastActive(p).Add(mcpIdleLimit)
		v.RevokeAfter = &at
	}
	return v
}

// lastActive is when p was last used, or created if never used.
func lastActive(p *store.AppPassword) time.Time {
	if p.LastUsedAt != nil {
		return *p.LastUsedAt
	}
	return p.CreatedAt
}

func (s *Server) handleListAppPasswords(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	list, err := s.store.AppPasswords().ListByUser(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not list app passwords")
		return
	}
	out := make([]appPasswordView, 0, len(list))
	for _, p := range list {
		out = append(out, viewOfAppPassword(p))
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateAppPassword(w http.ResponseWriter, r *http.Request) {
	// The credential outlives the session, so a stolen session must not mint one.
	if !s.requireStepUp(w, r, "create an app password") {
		return
	}
	user := sessionUser(r.Context())
	sess, _ := r.Context().Value(sessionKey{}).(*store.Session)
	var req struct {
		Label string `json:"label"`
		Scope string `json:"scope"`
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
	// Required: no client gets calendar or agent access by default.
	if req.Scope != store.ScopeDAV && !store.IsMCPScope(req.Scope) {
		s.writeError(w, http.StatusBadRequest, "scope must be dav, mcp:read, mcp:write-own or mcp:write-any")
		return
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create app password")
		return
	}
	p := &store.AppPassword{ID: id, UserID: user.ID, Label: label, Hash: hash, Scope: req.Scope}
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
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "app_password.create", Resource: "app_password:" + id, Details: "scope=" + p.Scope, IPAddress: s.requestIP(r)})
	v := viewOfAppPassword(p)
	s.writeJSON(w, http.StatusCreated, map[string]any{"id": id, "label": label, "scope": p.Scope, "password": token, "created_at": p.CreatedAt, "revoke_after": v.RevokeAfter})
}

// handleSetAppPasswordScope moves an MCP token between MCP scopes. A phone's dav token never
// becomes an agent's, nor the reverse.
func (s *Server) handleSetAppPasswordScope(w http.ResponseWriter, r *http.Request) {
	// Widening an agent's access is as sensitive as minting a token.
	if !s.requireStepUp(w, r, "change an agent's access") {
		return
	}
	user := sessionUser(r.Context())
	id := r.PathValue("id")
	var req struct {
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !store.IsMCPScope(req.Scope) {
		s.writeError(w, http.StatusBadRequest, "scope must be mcp:read, mcp:write-own or mcp:write-any")
		return
	}
	p, err := s.store.AppPasswords().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.UserID != user.ID) {
		s.writeError(w, http.StatusNotFound, "App password not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not change app password")
		return
	}
	if !store.IsMCPScope(p.Scope) {
		s.writeError(w, http.StatusBadRequest, "Only AI agent tokens have this setting")
		return
	}
	switch err := s.store.AppPasswords().SetScope(r.Context(), user.ID, id, req.Scope); {
	case errors.Is(err, store.ErrNotFound): // revoked since the read
		s.writeError(w, http.StatusNotFound, "App password not found")
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Could not change app password")
		return
	}
	if p.Scope != req.Scope {
		_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "app_password.scope_change", Resource: "app_password:" + id, Details: "from=" + p.Scope + " to=" + req.Scope, IPAddress: s.requestIP(r)})
	}
	p.Scope = req.Scope
	s.writeJSON(w, http.StatusOK, viewOfAppPassword(p))
}

func (s *Server) handleDeleteAppPassword(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
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

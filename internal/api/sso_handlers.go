package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
	"golang.org/x/oauth2"
)

var (
	errNotProvisioned  = errors.New("user account not provisioned")
	errAccountInactive = errors.New("account is not active")
	// errUsernameTaken: a different account already holds the IdP username. Never linked by name.
	errUsernameTaken = errors.New("username already used by another account")
	errOtherIssuer   = errors.New("account belongs to another sign-in provider")
)

// upsertSSOUser maps a verified login through the provider bound as binding onto a local user,
// which must carry that binding (new rows are stamped with it). A KyIdentity login's admin grant
// follows the token's `roles` claim on every login; any other provider's users are everyday. A
// change revokes every grant in the role write's own transaction (SetSSORole).
func (s *Server) upsertSSOUser(ctx context.Context, claims *sso.IdentityClaims, binding string) (*store.User, error) {
	role := "user"
	if claims.Provider == "kysignon" && access.IsAdmin(claims.Roles) {
		role = "admin"
	}
	user, err := s.store.Users().GetUserBySSO(ctx, claims.Provider, claims.Subject)
	if errors.Is(err, store.ErrNotFound) && claims.Provider == "kysignon" {
		// KyIdentity's SCIM externalId is this sub: a provisioned user is the same person.
		user, err = s.store.Users().GetUserBySSO(ctx, "scim", claims.Subject)
	}
	if errors.Is(err, store.ErrNotFound) {
		if !s.config.SSO.AutoProvision {
			return nil, errNotProvisioned
		}
		user = &store.User{
			ID:          fmt.Sprintf("usr_%s", crypto.RandomHex(12)),
			Username:    claims.PreferredUsername,
			Email:       claims.Email,
			DisplayName: claims.Name,
			Role:        role,
			Status:      "active",
			SSOProvider: claims.Provider,
			SSOSubject:  claims.Subject,
			SSOIssuer:   binding,
		}
		if err := s.store.Users().CreateUser(ctx, user); errors.Is(err, store.ErrAlreadyExists) {
			return nil, errUsernameTaken
		} else if err != nil {
			return nil, err
		}
		return user, nil
	}
	if err != nil {
		return nil, err
	}
	if user.Status != "active" {
		return nil, errAccountInactive
	}
	// The row belongs to the binding it was provisioned under. Re-activation (SCIM) does not move
	// it, so a later issuer's colliding sub never takes it over; an unstamped row is no issuer's.
	if user.SSOIssuer == "" || user.SSOIssuer != binding {
		return nil, errOtherIssuer
	}
	if user.Role == role {
		return user, nil
	}
	// Role only (a status read above may be stale), with the grants revoked in the same
	// transaction, row first: no old session runs under the new role. A failed promotion changes
	// nothing; a failed demotion still revokes the grants below.
	if err := s.store.Users().SetSSORole(ctx, user.ID, role); errors.Is(err, store.ErrNotFound) {
		return nil, errAccountInactive
	} else if err != nil {
		// A demotion fails closed: the role stays, but the administrator's grants do not.
		if user.Role == "admin" {
			if rerr := s.store.Users().RevokeSSOUser(ctx, user.ID, false); rerr != nil {
				log.Printf("sso: revoking %s after a failed demotion: %v", user.ID, rerr)
			}
		}
		return nil, err
	}
	from := user.Role
	user.Role = role
	_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: user.ID, Action: "sso.role_changed", Resource: user.ID, Details: "from=" + from + " to=" + role})
	return user, nil
}

func (s *Server) handleKySignOnLogin(w http.ResponseWriter, r *http.Request) {
	p := s.signin.Load()
	if p == nil {
		s.writeError(w, http.StatusNotFound, "Single sign-on is not configured")
		return
	}
	state := crypto.RandomHex(16)
	nonce := crypto.RandomHex(16)
	verifier := oauth2.GenerateVerifier()

	redirectURI := fmt.Sprintf("%s/api/sso/kysignon/callback", s.config.Server.AppURL)
	authURL, err := p.AuthURL(r.Context(), redirectURI, state, verifier, nonce)
	if err != nil {
		log.Printf("sso: %s authorization URL failed: %v", p.Kind, err)
		s.writeError(w, http.StatusBadGateway, "The sign-in provider could not be reached")
		return
	}

	// Store verifier in short-lived cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "ky_pkce_" + state,
		Value:    verifier,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   s.config.Security.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	// The nonce cookie carries the provider's ID: a callback after a provider swap fails.
	http.SetCookie(w, &http.Cookie{
		Name:     "ky_nonce_" + state,
		Value:    p.ID + "." + nonce,
		Path:     "/api/sso/kysignon/callback",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   s.config.Security.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleKySignOnCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	cookie, err := r.Cookie("ky_pkce_" + state)
	if err != nil || cookie.Value == "" {
		s.writeError(w, http.StatusBadRequest, "Invalid or expired SSO state")
		return
	}
	verifier := cookie.Value
	nonceCookie, err := r.Cookie("ky_nonce_" + state)
	if err != nil || nonceCookie.Value == "" {
		s.writeError(w, http.StatusBadRequest, "Invalid or expired SSO nonce")
		return
	}

	p := s.signin.Load()
	providerID, nonce, _ := strings.Cut(nonceCookie.Value, ".")
	if p == nil || providerID != p.ID || nonce == "" {
		s.writeError(w, http.StatusBadRequest, "Sign-in settings changed while you were signing in; start again")
		return
	}
	redirectURI := fmt.Sprintf("%s/api/sso/kysignon/callback", s.config.Server.AppURL)
	claims, err := p.Exchange(r.Context(), code, verifier, redirectURI, nonce)
	if err != nil {
		// The error can carry the token endpoint's response or a refused address: logged only.
		log.Printf("sso: %s code exchange failed: %v", p.Kind, err)
		s.writeError(w, http.StatusUnauthorized, "Sign-in failed")
		return
	}

	// A save may have rebound sign-in during the exchange. Holding the read lock until the session
	// is issued keeps the next save out until this login is done.
	s.signinMu.RLock()
	defer s.signinMu.RUnlock()
	if s.signin.Load() != p {
		s.writeError(w, http.StatusBadRequest, "Sign-in settings changed while you were signing in; start again")
		return
	}
	user, err := s.upsertSSOUser(r.Context(), claims, p.Binding)
	switch {
	case errors.Is(err, errNotProvisioned):
		s.writeError(w, http.StatusForbidden, "User account not provisioned")
		return
	case errors.Is(err, errAccountInactive):
		s.writeError(w, http.StatusForbidden, "Account is not active")
		return
	case errors.Is(err, errOtherIssuer):
		log.Printf("sso: sign-in for subject %s refused: the account belongs to another sign-in provider", claims.Subject)
		s.writeError(w, http.StatusForbidden, "This account belongs to a previous sign-in provider")
		return
	case errors.Is(err, errUsernameTaken):
		log.Printf("sso: sign-in for subject %s refused: username %q belongs to another account", claims.Subject, claims.PreferredUsername)
		s.writeError(w, http.StatusConflict, "Another KyCalendar account already uses this username; an administrator must rename it on the People screen")
		return
	case err != nil:
		log.Printf("sso: provisioning subject %s failed: %v", claims.Subject, err)
		s.writeError(w, http.StatusInternalServerError, "Failed to provision SSO user")
		return
	}

	_, _, err = s.sessions.IssueSession(r.Context(), w, r, user)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Session creation failed")
		return
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleKySignOnSyncWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	sig := r.Header.Get("X-KySignOn-Signature")
	if sig == "" {
		sig = r.Header.Get("X-Signature-SHA256")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	if err := s.kysignon.HandleSyncWebhook(r.Context(), body, sig); err != nil {
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]bool{"synced": true})
}

func (s *Server) handleSAMLMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(s.saml.GenerateMetadata()))
}

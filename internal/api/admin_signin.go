package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type signinRequest struct {
	Provider     string `json:"provider"`
	DisplayName  string `json:"display_name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// ConfirmDisable must equal the number of previous-provider accounts a provider change
	// disables; the screen learns it from a 409 confirm_provider_change.
	ConfirmDisable int `json:"confirm_disable"`
}

type secretView struct {
	Set    bool   `json:"set"`
	Source string `json:"source"`
}

// signinView never carries the secret, only whether one is set and where it comes from.
type signinView struct {
	Provider     sso.Field  `json:"provider"`
	DisplayName  sso.Field  `json:"display_name"`
	Issuer       sso.Field  `json:"issuer"`
	ClientID     sso.Field  `json:"client_id"`
	ClientSecret secretView `json:"client_secret"`
	CallbackURL  string     `json:"callback_url"`
	SSOEnabled   bool       `json:"sso_enabled"`
	Live         bool       `json:"live"`
}

func (s *Server) signinViewOf(st sso.Settings) signinView {
	return signinView{
		Provider: st.Provider, DisplayName: st.DisplayName, Issuer: st.Issuer, ClientID: st.ClientID,
		ClientSecret: secretView{Set: st.Secret.Source != sso.SourceUnset, Source: st.Secret.Source},
		CallbackURL:  s.config.Server.AppURL + "/api/sso/kysignon/callback",
		SSOEnabled:   s.config.SSO.Enabled,
		Live:         s.signin.Load() != nil,
	}
}

func (s *Server) handleGetSignIn(w http.ResponseWriter, r *http.Request) {
	saved, err := s.store.Settings().GetAllSettings(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load sign-in settings")
		return
	}
	s.writeJSON(w, http.StatusOK, s.signinViewOf(sso.Resolve(s.config.SSO, saved)))
}

// submitted is req resolved under the environment: a field the environment sets keeps its value
// whatever req says. The secret is left to the caller. Provider none keeps no other field.
func (s *Server) submitted(req signinRequest) sso.Settings {
	m := map[string]string{
		sso.KeyProvider:    strings.TrimSpace(req.Provider),
		sso.KeyDisplayName: strings.TrimSpace(req.DisplayName),
		sso.KeyIssuer:      strings.TrimSpace(req.Issuer),
		sso.KeyClientID:    strings.TrimSpace(req.ClientID),
	}
	st := sso.Resolve(s.config.SSO, m)
	if st.Provider.Source == sso.SourceEnvironment && m[sso.KeyProvider] != st.Provider.Value {
		// Resolve drops a foreign provider's fields; the unlocked label still applies.
		m[sso.KeyProvider] = st.Provider.Value
		st = sso.Resolve(s.config.SSO, m)
	}
	if st.Provider.Value == sso.KindNone {
		return sso.Settings{Provider: st.Provider}
	}
	return st
}

// storedLabel is the saved sign-in button label, "" when none or unreadable.
func (s *Server) storedLabel(ctx context.Context) string {
	v, _ := s.store.Settings().GetSetting(ctx, sso.KeyDisplayName)
	return v
}

// signinProblem is what is wrong with req resolved as st, "" when nothing is. The provider must
// be named even when the environment fixes it, so a body without one never turns sign-in off.
// A label equal to storedLabel is not revalidated, so one saved under an older rule never blocks
// a save.
func signinProblem(req signinRequest, st sso.Settings, storedLabel string) string {
	switch strings.TrimSpace(req.Provider) {
	case sso.KindNone, sso.KindKyIdentity, sso.KindOIDC:
	default:
		return "Provider must be none, kyidentity or oidc"
	}
	if st.Provider.Value == sso.KindNone {
		return ""
	}
	if st.Issuer.Value == "" || st.ClientID.Value == "" {
		return "The issuer and the client ID are required"
	}
	if _, ok := cleanName(st.DisplayName.Value); !ok && (st.DisplayName.Value == "" || st.DisplayName.Value != storedLabel) {
		return "The button label is required: 1-255 bytes with no control or invisible characters"
	}
	if len(st.Issuer.Value) > 2048 || strings.IndexFunc(st.Issuer.Value, unicode.IsControl) >= 0 {
		return "The issuer is not valid"
	}
	if len(st.ClientID.Value) > 255 || strings.IndexFunc(st.ClientID.Value, unicode.IsControl) >= 0 {
		return "The client ID must be 1-255 bytes with no control characters"
	}
	if len(req.ClientSecret) > 1024 {
		return "The client secret is too long"
	}
	return ""
}

// envIssuerHTTP tests the operator's environment issuer: no address policy (the operator owns
// it), but bounded and never redirected.
var envIssuerHTTP = &http.Client{
	Timeout:       20 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are refused") },
}

// discover runs discovery for st's issuer: through the guarded client unless the environment
// set the issuer.
func (s *Server) discover(ctx context.Context, st sso.Settings) error {
	client := s.signinHTTP
	if st.Issuer.Source == sso.SourceEnvironment {
		client = envIssuerHTTP
	}
	_, err := sso.Discover(ctx, client, st.Issuer.Value)
	return err
}

// handleTestSignIn runs discovery for the submitted values without saving anything.
func (s *Server) handleTestSignIn(w http.ResponseWriter, r *http.Request) {
	var req signinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	st := s.submitted(req)
	msg := signinProblem(req, st, s.storedLabel(r.Context()))
	if msg == "" && st.Provider.Value == sso.KindNone {
		msg = "Choose a provider to test"
	}
	if msg != "" {
		s.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.discover(r.Context(), st); err != nil {
		s.auditAction(r.Context(), r, "admin.signin_test", st.Provider.Value, "outcome=failure issuer="+strconv.Quote(st.Issuer.Value))
		s.writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.auditAction(r.Context(), r, "admin.signin_test", st.Provider.Value, "outcome=success issuer="+strconv.Quote(st.Issuer.Value))
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSaveSignIn validates, tests, binds, saves and makes the settings live with no restart.
// It holds signinMu throughout, so saves run one at a time and no callback writes an account
// between the commit and the swap. A provider or issuer change needs the admin to confirm the
// count of previous-provider accounts it disables. The commit is one BindSignIn transaction:
// disable, record the binding and write the settings, or write nothing. A failed commit closes
// sign-in (nil provider) and a retry asks for the same confirmation.
func (s *Server) handleSaveSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "change sign-in settings") {
		return
	}
	var req signinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	st := s.submitted(req)
	if msg := signinProblem(req, st, s.storedLabel(r.Context())); msg != "" {
		s.writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	s.signinMu.Lock()
	defer s.signinMu.Unlock()

	saved, err := s.store.Settings().GetAllSettings(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load sign-in settings")
		return
	}
	// newSecret is what to seal; "" keeps the stored one (same registration) or the environment's.
	newSecret := ""
	if st.Provider.Value != sso.KindNone && st.Secret.Source != sso.SourceEnvironment {
		switch {
		case req.ClientSecret != "":
			newSecret = req.ClientSecret
			st.Secret = sso.SecretField{Value: newSecret, Source: sso.SourceSaved}
		case sso.SavedSecretFits(saved, st):
			v, err := sso.OpenSecret(s.config.Security.EncryptionKey, saved[sso.KeySecretSealed])
			if err != nil {
				log.Printf("[SSO] the saved client secret could not be opened: %v", err)
				s.writeError(w, http.StatusInternalServerError, "The saved client secret could not be opened; enter it again")
				return
			}
			st.Secret = sso.SecretField{Value: v, Source: sso.SourceSaved}
		default:
			// Never send one registration's secret to another.
			s.writeError(w, http.StatusBadRequest, "Enter the client secret for this provider")
			return
		}
	}
	// The environment's issuer is the operator's and is not rediscovered here.
	if st.Provider.Value != sso.KindNone && st.Issuer.Source != sso.SourceEnvironment {
		if err := s.discover(ctx, st); err != nil {
			s.auditAction(ctx, r, "admin.signin_save", st.Provider.Value, "outcome=failure issuer="+strconv.Quote(st.Issuer.Value))
			s.writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	n, err := s.pendingDisable(ctx, st)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count accounts")
		return
	}
	if n > 0 && req.ConfirmDisable != n {
		s.writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("Saving disables %d accounts of the previous provider", n),
			"code":  "confirm_provider_change", "count": n,
		})
		return
	}
	values, err := s.signinValues(st, newSecret)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save sign-in settings")
		return
	}
	// One transaction: the acting admin is rechecked (these settings decide who becomes an
	// administrator), the previous provider's accounts are disabled and the settings written.
	prev, disabled, err := s.commitSignIn(ctx, actorOf(ctx), st, values)
	if errors.Is(err, store.ErrActorRevoked) {
		// Nothing was written; the live provider still matches the stored settings.
		s.writeActorRevoked(w)
		return
	} else if err != nil {
		s.signin.Store(nil)
		log.Printf("[SSO] sign-in save failed: %v", err)
		s.auditAction(ctx, r, "admin.signin_save", st.Provider.Value, "outcome=failure step=commit")
		s.writeError(w, http.StatusInternalServerError, "Sign-in is closed: the settings could not be saved; nothing changed, save again")
		return
	}
	if prev != "" {
		s.auditAction(ctx, r, "admin.signin_provider_change", st.Provider.Value, bindDetails(prev, st, disabled))
	}
	s.signin.Store(s.buildProvider(st))
	s.auditAction(ctx, r, "admin.signin_save", st.Provider.Value, "outcome=success issuer="+strconv.Quote(st.Issuer.Value))
	// The view is what was stored, read back as GET reads it.
	saved, err = s.store.Settings().GetAllSettings(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Saved; reload to see the settings")
		return
	}
	s.writeJSON(w, http.StatusOK, s.signinViewOf(sso.Resolve(s.config.SSO, saved)))
}

// signinValues is the settings write for st: every field the environment does not set, written
// together so a failure never pairs one registration's secret with another's issuer. newSecret
// is sealed over the stored one; "" keeps it, except that provider none drops it.
func (s *Server) signinValues(st sso.Settings, newSecret string) (map[string]string, error) {
	values := map[string]string{}
	for key, f := range map[string]sso.Field{sso.KeyProvider: st.Provider, sso.KeyDisplayName: st.DisplayName, sso.KeyIssuer: st.Issuer, sso.KeyClientID: st.ClientID} {
		switch {
		case f.Source == sso.SourceEnvironment && key == sso.KeyProvider:
			// A saved foreign kind under the environment's would make Resolve drop every saved field.
			values[key] = ""
		case f.Source == sso.SourceEnvironment:
		case f.Source == sso.SourceUnset: // a default such as the KyIdentity label is not stored
			values[key] = ""
		default:
			values[key] = f.Value
		}
	}
	switch {
	case st.Provider.Value == sso.KindNone:
		values[sso.KeySecretSealed], values[sso.KeySecretRegistration] = "", ""
	case newSecret != "":
		sealed, err := sso.SealSecret(s.config.Security.EncryptionKey, newSecret)
		if err != nil {
			return nil, err
		}
		// The secret and the registration it belongs to are written together, in one transaction.
		values[sso.KeySecretSealed] = sealed
		values[sso.KeySecretRegistration] = sso.SecretRegistration(st.Provider.Value, st.Issuer.Value, st.ClientID.Value)
	}
	return values, nil
}

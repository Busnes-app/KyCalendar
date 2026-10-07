package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// buildProvider is the provider for st, or nil when st is not live. An issuer the operator set
// in the environment keeps the default transport, as before this screen existed; one an admin
// typed in goes through the guarded client.
func (s *Server) buildProvider(st sso.Settings) *sso.Provider {
	if !st.Live() {
		return nil
	}
	client := s.signinHTTP
	if st.Issuer.Source == sso.SourceEnvironment {
		client = nil
	}
	return sso.NewProvider(st.Provider.Value, st.DisplayName.Value, st.Issuer.Value, st.ClientID.Value, st.Secret.Value, client)
}

// signInSettings resolves the saved settings under the environment and opens a saved secret.
func (s *Server) signInSettings(ctx context.Context) (sso.Settings, error) {
	saved, err := s.store.Settings().GetAllSettings(ctx)
	if err != nil {
		return sso.Settings{}, err
	}
	st := sso.Resolve(s.config.SSO, saved)
	if st.Secret.Source == sso.SourceSaved {
		if st.Secret.Value, err = sso.OpenSecret(s.config.Security.EncryptionKey, saved[sso.KeySecretSealed]); err != nil {
			return sso.Settings{}, fmt.Errorf("open the saved client secret: %w", err)
		}
	}
	return st, nil
}

// LoadSignIn makes the sign-in settings live and binds accounts to them, disabling the previous
// provider's accounts if the provider or issuer changed while the server was down (an
// environment edit). A provider is stored only once binding succeeds; any error leaves sign-in
// closed. cmd/server calls it once at startup.
func (s *Server) LoadSignIn(ctx context.Context) error {
	s.signin.Store(nil)
	st, err := s.signInSettings(ctx)
	if err != nil {
		return err
	}
	prev, n, err := s.bindAccounts(ctx, st)
	if err != nil {
		return err
	}
	if prev != "" {
		// The binding is committed: a lost audit row is logged, not a reason to keep sign-in closed.
		if err := s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: "system", Action: "admin.signin_provider_change", Resource: st.Provider.Value, Details: bindDetails(prev, st, n)}); err != nil {
			log.Printf("[SSO] audit of the sign-in provider change failed: %v", err)
		}
		log.Printf("[SSO] sign-in provider changed from %q to %q: %d accounts of the previous provider disabled", prev, bindingIdentity(st), n)
	}
	s.signin.Store(s.buildProvider(st))
	return nil
}

func bindDetails(prev string, st sso.Settings, n int) string {
	return "from=" + strconv.Quote(prev) + " to=" + strconv.Quote(bindingIdentity(st)) + " disabled=" + strconv.Itoa(n)
}

// boundTo is the identity accounts are bound to, "" when nothing is bound yet.
func (s *Server) boundTo(ctx context.Context) (string, error) {
	bound, err := s.store.Settings().GetSetting(ctx, sso.KeyBound)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return bound, err
}

// ssoAccountProviders is every users.sso_provider value a sign-in provider can reach.
var ssoAccountProviders = []string{"kysignon", "scim", "oidc"}

// bindingIdentity is the identity accounts bind to: the kind and the issuer with every
// trailing '/' trimmed, so a cosmetic slash is not a provider change. Discovery still gets the
// issuer as entered.
func bindingIdentity(st sso.Settings) string {
	return st.Provider.Value + " " + strings.TrimRight(st.Issuer.Value, "/")
}

// unchanged reports whether binding st keeps the stored binding.
func unchanged(bound string, st sso.Settings) bool {
	return strings.TrimRight(bound, "/") == bindingIdentity(st)
}

// disableList is the account providers a change from bound to st disables: the previous kind's,
// or every SSO provider when the stored kind is unknown, so a damaged binding disables too much
// rather than nothing. KyIdentity logins adopt scim rows by sub, so moving to KyIdentity from
// any other kind disables scim rows too: one provisioned under the old binding is not adoptable.
func disableList(bound string, st sso.Settings) []string {
	kind, _, _ := strings.Cut(bound, " ")
	p := sso.AccountProviders(kind)
	if p == nil {
		return ssoAccountProviders
	}
	if st.Provider.Value == sso.KindKyIdentity && !slices.Contains(p, "scim") {
		p = append(p, "scim")
	}
	return p
}

// pendingDisable is how many accounts binding to st would disable.
func (s *Server) pendingDisable(ctx context.Context, st sso.Settings) (int, error) {
	bound, err := s.boundTo(ctx)
	if err != nil || !st.Live() || bound == "" || unchanged(bound, st) {
		return 0, err
	}
	return s.store.Users().CountSSOAccounts(ctx, disableList(bound, st))
}

// bindAccounts makes st the provider SSO accounts belong to. On a change of kind or issuer the
// previous provider's accounts are disabled first, so a colliding sub from the new provider
// reaches a disabled account (403), never a takeover. The first binding disables nothing, and
// settings that are not live keep the binding. It returns the previous identity ("" when
// nothing changed) and how many accounts it disabled. A rerun after a failure between the two
// writes disables again (a no-op) and records the binding.
func (s *Server) bindAccounts(ctx context.Context, st sso.Settings) (string, int, error) {
	if !st.Live() {
		return "", 0, nil
	}
	bound, err := s.boundTo(ctx)
	if err != nil || unchanged(bound, st) {
		return "", 0, err
	}
	n := 0
	if bound != "" {
		if n, err = s.store.Users().DisableSSOAccounts(ctx, disableList(bound, st)); err != nil {
			return "", 0, err
		}
	}
	if err := s.store.Settings().SetSetting(ctx, sso.KeyBound, bindingIdentity(st)); err != nil {
		return "", 0, err
	}
	return bound, n, nil
}

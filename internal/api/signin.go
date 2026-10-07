package api

import (
	"context"
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
	if names := sso.IgnoredEnv(s.config.SSO); names != nil {
		log.Printf("[SSO] %s ignored: KY_KYSIGNON_ISSUER is not set", strings.Join(names, " and "))
	}
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
		log.Printf("[SSO] sign-in provider changed from %q to %q: %d accounts of the previous provider disabled", prev, st.Identity(), n)
	}
	s.signin.Store(s.buildProvider(st))
	return nil
}

func bindDetails(prev string, st sso.Settings, n int) string {
	return "from=" + strconv.Quote(prev) + " to=" + strconv.Quote(st.Identity()) + " disabled=" + strconv.Itoa(n)
}

// boundTo is the identity accounts are bound to, "" when nothing is bound yet.
func (s *Server) boundTo(ctx context.Context) (string, error) {
	return sso.Bound(ctx, s.store.Settings())
}

// ssoAccountProviders is every users.sso_provider value a sign-in provider can reach.
var ssoAccountProviders = []string{"kysignon", "scim", "oidc"}

// unchanged reports whether binding st keeps the stored binding. Issuers compare exactly: OIDC
// issuer identifiers are exact strings, so "https://a" and "https://a/" are two issuers.
func unchanged(bound string, st sso.Settings) bool {
	return bound == st.Identity()
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

// bindAccounts makes st the provider SSO accounts belong to, as the operator at startup.
func (s *Server) bindAccounts(ctx context.Context, st sso.Settings) (string, int, error) {
	return s.commitSignIn(ctx, store.System, st, nil)
}

// commitSignIn binds accounts to st and writes settings in one store transaction under the
// actor's recheck. On a change of kind or issuer the previous provider's accounts are disabled
// in it, so a colliding sub from the new provider reaches a disabled account (403), never a
// takeover; a failure writes nothing. Every SSO account carries the binding it was provisioned
// under (users.sso_issuer) and login refuses any other. The first binding disables nothing and
// stamps its kind's unstamped rows; a change stamps nothing, so no row of an earlier binding
// ever carries a later one. Settings that are not live keep the binding. It returns the previous
// identity ("" when the binding did not change) and how many accounts it disabled.
func (s *Server) commitSignIn(ctx context.Context, actor store.Actor, st sso.Settings, settings map[string]string) (string, int, error) {
	bound, err := s.boundTo(ctx)
	if err != nil {
		return "", 0, err
	}
	changed := st.Live() && !unchanged(bound, st)
	if !changed && len(settings) == 0 {
		return "", 0, nil
	}
	var b store.SignInBinding
	if changed {
		if settings == nil {
			settings = map[string]string{}
		}
		settings[sso.KeyBound] = st.Identity()
		if bound == "" {
			b.Stamp, b.StampProviders = st.Identity(), sso.AccountProviders(st.Provider.Value)
		} else {
			b.Disable = disableList(bound, st)
		}
	}
	n, err := s.store.Users().BindSignIn(ctx, actor, b, settings)
	if err != nil || !changed {
		return "", 0, err
	}
	return bound, n, nil
}

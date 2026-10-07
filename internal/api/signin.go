package api

import "github.com/Busnes-app/kycalendar/internal/sso"

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

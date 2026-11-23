package api

import (
	"net/http"
	"regexp"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// Multi-tenant admin surface (tenancy spec, Admin surface, build step 5). Administrators are staff
// (D7, D8); customer lifecycle is KyIdentity's. Single-tenant installs keep every route as it is.

// singleTenantOnly answers a plain 404, the same for every caller, in multi-tenant mode: a business
// feature (D9) or an account write staff do in KyIdentity (D8). It runs before any sign-in check,
// so the answer is the same for everyone.
func (s *Server) singleTenantOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ten, err := s.store.Tenancy(r.Context())
		if err != nil {
			s.writeError(w, http.StatusServiceUnavailable, "Try again in a moment")
			return
		}
		if ten.Multi {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// multiTenantOnly is the reverse, for staff routes that exist only in multi-tenant mode.
func (s *Server) multiTenantOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ten, err := s.store.Tenancy(r.Context())
		if err != nil {
			s.writeError(w, http.StatusServiceUnavailable, "Try again in a moment")
			return
		}
		if !ten.Multi {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// signInFixedInMulti refuses changing or testing the sign-in provider in multi-tenant mode, where
// KY_KYSIGNON_ISSUER fixes it (D2).
func (s *Server) signInFixedInMulti(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ten, err := s.store.Tenancy(r.Context())
		if err != nil {
			s.writeError(w, http.StatusServiceUnavailable, "Try again in a moment")
			return
		}
		if ten.Multi {
			s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign-in is fixed by the platform's configuration in multi-tenant mode", "code": "sign_in_fixed"})
			return
		}
		h(w, r)
	}
}

// orgIDPattern bounds a request-chosen org ID before it reaches a query or an audit row.
var orgIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// staffOrg is the ?org= of a staff read in multi-tenant mode, written to a platform audit row
// (staff.read) before anything is read. ok is false when an answer was written.
func (s *Server) staffOrg(w http.ResponseWriter, r *http.Request, required bool) (org store.OrgID, ok bool) {
	q := r.URL.Query().Get("org")
	if q == "" {
		if required {
			s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose an org: ?org=<id>", "code": "org_required"})
			return "", false
		}
		return "", true
	}
	if !orgIDPattern.MatchString(q) {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Not an org ID", "code": "bad_org"})
		return "", false
	}
	// The read is recorded first, so no staff read of an org goes unrecorded; the row is a
	// platform row and outlives the org's purge (tenancy spec, Admin surface).
	rec := &store.AuditRecord{UserID: sessionUser(r.Context()).ID, Action: "staff.read", Resource: q,
		Details: "org=" + q + " route=" + r.Method + " " + r.URL.Path, IPAddress: s.requestIP(r)}
	if err := s.store.Audit().LogPlatformAudit(r.Context(), rec); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "Try again in a moment")
		return "", false
	}
	return store.OrgID(q), true
}

// handleListOrgs is the org mirror for staff: status, revision, enabled, quotas, stored bytes and
// when a poll last listed it. Staff need it to answer a customer about a quota.
func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.store.ListMirroredOrgs(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list orgs")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"orgs": orgs})
}

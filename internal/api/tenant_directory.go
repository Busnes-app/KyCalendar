package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// directoryReplay keeps KyIdentity's directory event IDs apart from KyPost's in the shared guard.
// The guard must forget: a refused event's retry has to be applied, not acknowledged as a replay.
type directoryReplay struct{ inner syncauth.Forgetter }

func (d directoryReplay) Check(id string, at time.Time) error {
	return d.inner.Check("directory:"+id, at)
}

func (d directoryReplay) Forget(id string) { d.inner.Forget("directory:" + id) }

// directoryUser is the part of KyIdentity's signed SCIM User body this receiver reads.
type directoryUser struct {
	ID          string `json:"id"`
	Active      *bool  `json:"active"`
	OrgID       string `json:"org_id"`
	DisplayName string `json:"displayName"`
	Emails      []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
	} `json:"emails"`
}

// handleTenantDirectory is a multi-tenant install's directory receiver (tenancy spec, Directory
// webhook): KyIdentity's syncauth-signed SCIM User bodies under the connector secret. It only
// takes access away. Removals apply whatever the body's org; a profile change applies, and
// erasure deletes the account's data, only when org_id is the account's own. It never creates an
// account (the first sign-in does) and never activates one. Every event it acts on or ignores
// answers 204, so KyIdentity stops retrying. A replay applies again everything that takes access
// away (it is idempotent), so a retry racing a first delivery that then fails loses nothing; only
// its profile write is skipped.
func (s *Server) handleTenantDirectory(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		s.writeError(w, http.StatusBadRequest, "Invalid directory body")
		return
	}
	secret := []byte(s.config.SSO.KySignOnHMACSecret)
	if len(secret) < syncauth.MinKeyBytes {
		s.writeError(w, http.StatusServiceUnavailable, "Directory credential unavailable")
		return
	}
	guard := directoryReplay{s.directoryReplay}
	h := syncauth.FromRequest(r)
	event, err := syncauth.Verify(secret, h, raw, syncauth.Options{Replay: guard})
	replayed := errors.Is(err, syncauth.ErrReplay) // signed and fresh: Verify checks those first
	switch {
	case replayed:
		event = syncauth.Event{Type: h.EventType, ID: h.EventID}
	case errors.Is(err, syncauth.ErrReplayGuardFull):
		w.Header().Set("Retry-After", "5")
		s.writeError(w, http.StatusServiceUnavailable, "Temporarily unavailable")
		return
	case err != nil:
		s.writeError(w, http.StatusUnauthorized, "Invalid directory signature")
		return
	}
	fail := func(status int, msg string) {
		if !replayed { // the ID is the first delivery's to keep or release
			guard.Forget(event.ID) // KyIdentity's retry is applied, not taken for a replay
		}
		s.writeError(w, status, msg)
	}
	var in directoryUser
	if json.Unmarshal(raw, &in) != nil || in.ID == "" || len(in.ID) > 256 {
		fail(http.StatusBadRequest, "Invalid directory resource")
		return
	}
	removal := false
	switch event.Type {
	case "user.deactivated", "user.deleted", "user.erased":
		removal = true
	case "user.created", "user.updated":
		if in.Active == nil {
			fail(http.StatusBadRequest, "Invalid directory resource")
			return
		}
		removal = !*in.Active
	case "user.mfa_reset":
	default:
		fail(http.StatusBadRequest, "Unsupported directory event")
		return
	}

	ctx := r.Context()
	bound, err := sso.Bound(ctx, s.store.Settings())
	if err != nil {
		fail(http.StatusServiceUnavailable, "Directory unavailable")
		return
	}
	u, err := s.store.Users().GetUserBySSO(ctx, "kysignon", in.ID)
	if errors.Is(err, store.ErrNotFound) || err == nil && u.SSOIssuer != bound {
		w.WriteHeader(http.StatusNoContent) // not this directory's account: nothing to take away
		return
	}
	if err != nil {
		fail(http.StatusServiceUnavailable, "Directory unavailable")
		return
	}
	own := in.OrgID != "" && in.OrgID == string(store.OrgOf(u))
	if !own {
		details := "reason=no_org event=" + event.Type
		if in.OrgID != "" {
			details = "org=" + string(store.OrgOf(u)) + " token_org=" + in.OrgID + " event=" + event.Type
		}
		_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: u.ID, Action: "sso.webhook_org_mismatch", Details: details, IPAddress: "kyidentity"})
	}
	users := s.store.Users()
	switch {
	case event.Type == "user.erased" && own: // Delete everything
		err = users.DeleteUser(ctx, u.ID)
	case event.Type == "user.mfa_reset":
		err = users.RevokeSSOUser(ctx, u.ID, false)
	case removal:
		err = users.RevokeSSOUser(ctx, u.ID, true)
	default:
		// The body cannot see app roles: an administrator re-proves the role at the next sign-in.
		if u.Role == "admin" {
			err = users.RevokeSSOUser(ctx, u.ID, false)
		}
		if err == nil && own && !replayed { // a replay never rewrites the profile
			err = users.UpdateKySignOnProfile(ctx, u.ID, cmp.Or(in.DisplayName, u.DisplayName), cmp.Or(in.primaryEmail(), u.Email))
		}
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("[SSO] directory %s for %s: %v", event.Type, u.ID, err)
		fail(http.StatusServiceUnavailable, "Directory unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (in directoryUser) primaryEmail() string {
	for _, e := range in.Emails {
		if e.Primary {
			return e.Value
		}
	}
	return ""
}

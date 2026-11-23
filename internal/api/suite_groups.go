package api

import (
	"context"
	"log"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// SuiteGroupsInterval is how often cmd/server reconciles KyIdentity's suite groups.
const SuiteGroupsInterval = time.Minute

// SyncSuiteGroups gives each KyIdentity suite group one group calendar here (KyIdentity-server
// docs/SUITE-DIRECTORY.md, GET /api/suite/groups). Only a live KyIdentity provider has the
// list. In multi-tenant mode each serving org's groups are listed with its org_id and reconciled
// into that org alone (tenancy spec, build step 4g); one org's failed list skips only that org.
// A failed list changes nothing, and a group missing from the list is never archived: only an
// explicit archived entry archives. Logs carry IDs and counts only, never names or subjects.
func (s *Server) SyncSuiteGroups(ctx context.Context) {
	p := s.signin.Load()
	if p == nil || p.Kind != sso.KindKyIdentity {
		return
	}
	dir, err := p.SuiteDirectory()
	if err != nil {
		log.Printf("suite: groups: %v", err)
		return
	}
	ten, err := s.store.Tenancy(ctx)
	if err != nil {
		log.Printf("suite: groups: tenancy: %v", err)
		return
	}
	scope := store.SCIMScope{Issuer: p.Binding, Providers: sso.AccountProviders(p.Kind)}
	if !ten.Multi {
		s.syncOrgSuiteGroups(ctx, scope, store.DefaultOrg, func(ctx context.Context) ([]suitedir.Group, error) { return dir.Groups(ctx) })
		return
	}
	orgs, err := s.store.ServingOrgs(ctx)
	if err != nil {
		log.Printf("suite: groups: orgs: %v", err)
		return
	}
	for _, org := range orgs {
		if ctx.Err() != nil {
			return
		}
		if org == store.DefaultOrg {
			continue // the single-tenant org (D4); KyIdentity has no such org to list
		}
		s.syncOrgSuiteGroups(ctx, scope, org, func(ctx context.Context) ([]suitedir.Group, error) { return dir.OrgGroups(ctx, string(org)) })
	}
}

// syncOrgSuiteGroups lists one org's suite groups and reconciles them into that org.
func (s *Server) syncOrgSuiteGroups(ctx context.Context, scope store.SCIMScope, org store.OrgID, list func(context.Context) ([]suitedir.Group, error)) {
	groups, err := func() ([]suitedir.Group, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return list(ctx)
	}()
	if err != nil {
		// suitedir errors carry neither the credentials nor the response body.
		log.Printf("suite: groups: org %s: %v", org, err)
		return
	}
	for _, g := range groups {
		if ctx.Err() != nil {
			return
		}
		if len(g.ID) > 128 {
			continue // past what a group name column holds; KyIdentity IDs are far shorter
		}
		name := g.Name
		if calendar.CheckProps(&name, nil, nil) != nil {
			name = g.Slug // validated by suitedir
		}
		ch, err := s.store.Org(org).ReconcileSuiteGroup(ctx, scope, store.SuiteGroup{ID: g.ID, Name: name, Active: g.State == suitedir.GroupActive, Subjects: g.Members})
		if err != nil {
			log.Printf("suite: group %s: reconcile failed: %v", g.ID, err)
			continue
		}
		if ch.Changed() {
			log.Printf("suite: group %s: created=%t renamed=%t archived=%t unarchived=%t grant=%t added=%d removed=%d",
				g.ID, ch.Created, ch.Renamed, ch.Archived, ch.Unarchived, ch.GrantRestored, ch.Added, ch.Removed)
		}
	}
}

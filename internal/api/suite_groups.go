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
// list. A failed list changes nothing, and a group missing from the list is never archived:
// only an explicit archived entry archives. Logs carry IDs and counts only, never names or
// subjects.
func (s *Server) SyncSuiteGroups(ctx context.Context) {
	p := s.signin.Load()
	if p == nil || p.Kind != sso.KindKyIdentity {
		return
	}
	groups, err := func() ([]suitedir.Group, error) {
		dir, err := p.SuiteDirectory()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return dir.Groups(ctx)
	}()
	if err != nil {
		// suitedir errors carry neither the credentials nor the response body.
		log.Printf("suite: groups: %v", err)
		return
	}
	scope := store.SCIMScope{Issuer: p.Binding, Providers: sso.AccountProviders(p.Kind)}
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
		// Single-tenant: one org. Tenancy PR 3 polls each org and reconciles into it.
		ch, err := s.store.Org(store.DefaultOrg).ReconcileSuiteGroup(ctx, scope, store.SuiteGroup{ID: g.ID, Name: name, Active: g.State == suitedir.GroupActive, Subjects: g.Members})
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

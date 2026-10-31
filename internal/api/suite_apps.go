package api

import (
	"context"
	"log"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// suiteAppsTTL is how long one user's app list is reused: KyIdentity rate limits each client.
var suiteAppsTTL = time.Minute

// suiteAppsCache holds each user's app list, keyed by user ID and never shared between users.
type suiteAppsCache struct {
	mu     sync.Mutex
	byUser map[string]suiteAppsEntry
}

type suiteAppsEntry struct {
	provider string // the sign-in provider's ID: a new provider never reuses an old list
	apps     []suitedir.App
	until    time.Time
}

func (c *suiteAppsCache) get(user, provider string) ([]suitedir.App, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byUser[user]
	if !ok || e.provider != provider || time.Now().After(e.until) {
		return nil, false
	}
	return e.apps, true
}

func (c *suiteAppsCache) put(user, provider string, apps []suitedir.App) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.byUser == nil {
		c.byUser = map[string]suiteAppsEntry{}
	}
	for id, e := range c.byUser {
		if now.After(e.until) {
			delete(c.byUser, id)
		}
	}
	c.byUser[user] = suiteAppsEntry{provider: provider, apps: apps, until: now.Add(suiteAppsTTL)}
}

// handleSuiteApps is the app rail: the suite apps KyIdentity lets this user open.
func (s *Server) handleSuiteApps(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string][]suitedir.App{"apps": s.suiteAppsFor(r.Context(), sessionUser(r.Context()))})
}

// suiteAppsFor asks KyIdentity's suite directory with the user's subject. Accounts the live
// KyIdentity provider did not sign in, and every failure, get an empty list.
func (s *Server) suiteAppsFor(ctx context.Context, u *store.User) []suitedir.App {
	none := []suitedir.App{}
	p := s.signin.Load()
	if p == nil || p.Kind != sso.KindKyIdentity || u.SSOSubject == "" || u.SSOIssuer != p.Binding || !slices.Contains(sso.AccountProviders(p.Kind), u.SSOProvider) {
		return none
	}
	if apps, ok := s.suiteApps.get(u.ID, p.ID); ok {
		return apps
	}
	apps, err := func() ([]suitedir.App, error) {
		dir, err := p.SuiteDirectory()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return dir.Apps(ctx, u.SSOSubject)
	}()
	if err != nil {
		// suitedir errors carry neither the request URL (the sub) nor the credentials.
		log.Printf("suite: app list for %s: %v", u.ID, err)
		apps = none
	}
	// A request the browser abandoned says nothing about KyIdentity: do not cache it.
	if ctx.Err() == nil {
		s.suiteApps.put(u.ID, p.ID, apps)
	}
	return apps
}

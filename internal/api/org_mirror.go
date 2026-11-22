package api

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// OrgsInterval is how often cmd/server polls KyIdentity's org list (tenancy spec D6).
const OrgsInterval = time.Minute

// A sign-in naming an org that does not serve polls at once, at most every orgAskEvery per org,
// and waits up to orgAskWait for the poll (tenancy spec, Sign-in step 2).
var orgAskEvery, orgAskWait = 10 * time.Second, 5 * time.Second

// orgPoll runs at most one mirror poll per process; callers arriving during one join it.
type orgPoll struct {
	flight singleflight.Group
	mu     sync.Mutex
	asked  map[store.OrgID]time.Time
}

// SyncOrgs mirrors /api/suite/orgs into the orgs table. Multi-tenant installs with a live
// KyIdentity provider only; a failed poll changes nothing. Logs carry no org data.
func (s *Server) SyncOrgs(ctx context.Context) {
	select {
	case res := <-s.pollOrgs():
		if res.Err != nil {
			// suitedir errors carry neither the credentials nor the response body.
			log.Printf("suite: orgs: %v", res.Err)
		}
	case <-ctx.Done():
	}
}

// pollOrgs starts the shared poll, or joins the one running; its Val is when that poll began.
// It belongs to no caller, so a sign-in that stops waiting cancels nothing others joined;
// StopDetached ends it, and the store closes only after it returns.
func (s *Server) pollOrgs() <-chan singleflight.Result {
	return s.orgs.flight.DoChan("orgs", func() (any, error) {
		began := time.Now()
		s.detached.add()
		defer s.detached.done()
		ctx, cancel := context.WithTimeout(s.stopping, 30*time.Second)
		defer cancel()
		return began, s.syncOrgs(ctx)
	})
}

// syncOrgs is one poll: read the orgs after the cursor and write them with the next cursor.
func (s *Server) syncOrgs(ctx context.Context) error {
	ten, err := s.store.Tenancy(ctx)
	if err != nil || !ten.Multi {
		return err
	}
	p := s.signin.Load()
	if p == nil || p.Kind != sso.KindKyIdentity {
		return nil
	}
	dir, err := p.SuiteDirectory()
	if err != nil {
		return err
	}
	v, err := s.store.Settings().GetSettings(ctx, store.OrgsCursorSetting)
	if err != nil {
		return err
	}
	since, _ := strconv.ParseInt(v[store.OrgsCursorSetting], 10, 64) // none or unreadable: read all
	orgs, next, err := dir.Orgs(ctx, max(since, 0))
	if err != nil {
		return err
	}
	mirrored := make([]store.MirroredOrg, len(orgs))
	for i, o := range orgs {
		mirrored[i] = store.MirroredOrg{ID: store.OrgID(o.ID), Status: o.Status, Revision: o.Revision, Enabled: o.Enabled, Quotas: string(o.Quotas)}
	}
	return s.store.ApplyOrgs(ctx, ten.Staff, mirrored, next)
}

// awaitOrg polls for an org a verified token or signed webhook named that does not serve yet,
// so a new customer's first sign-in succeeds. Only KyIdentity can name an org, and each one
// polls at most every orgAskEvery.
func (s *Server) awaitOrg(ctx context.Context, org store.OrgID) {
	now := time.Now()
	s.orgs.mu.Lock()
	if now.Sub(s.orgs.asked[org]) < orgAskEvery {
		s.orgs.mu.Unlock()
		return
	}
	for id, at := range s.orgs.asked {
		if now.Sub(at) >= orgAskEvery {
			delete(s.orgs.asked, id)
		}
	}
	if s.orgs.asked == nil {
		s.orgs.asked = map[store.OrgID]time.Time{}
	}
	s.orgs.asked[org] = now
	s.orgs.mu.Unlock()
	t := time.NewTimer(orgAskWait)
	defer t.Stop()
	for {
		select {
		case res := <-s.pollOrgs():
			// A poll already running when the org was named may predate it: run one more.
			if began, _ := res.Val.(time.Time); !began.Before(now) {
				return
			}
		case <-t.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

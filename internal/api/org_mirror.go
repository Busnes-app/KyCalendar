package api

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

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
	mu      sync.Mutex
	running *orgRun
	asked   map[store.OrgID]time.Time // when each org last started a poll
}

// orgRun is one poll: err is set before done closes.
type orgRun struct {
	began time.Time
	done  chan struct{}
	err   error
}

// SyncOrgs mirrors /api/suite/orgs into the orgs table. Multi-tenant installs with a live
// KyIdentity provider only; a failed poll changes nothing. Logs carry no org data.
func (s *Server) SyncOrgs(ctx context.Context) {
	s.orgs.mu.Lock()
	r := s.pollOrgsLocked()
	s.orgs.mu.Unlock()
	select {
	case <-r.done:
		if r.err != nil {
			// suitedir errors carry neither the credentials nor the response body.
			log.Printf("suite: orgs: %v", r.err)
		}
	case <-ctx.Done():
	}
}

// pollOrgsLocked joins the poll running or starts one; s.orgs.mu is held. The poll belongs to
// no caller, so one that stops waiting cancels nothing others joined; StopDetached ends it, and
// the store closes only after it returns.
func (s *Server) pollOrgsLocked() *orgRun {
	if s.orgs.running != nil {
		return s.orgs.running
	}
	r := &orgRun{began: time.Now(), done: make(chan struct{})}
	s.orgs.running = r
	s.detached.add()
	go func() {
		defer s.detached.done()
		ctx, cancel := context.WithTimeout(s.stopping, 30*time.Second)
		r.err = s.syncOrgs(ctx)
		cancel()
		s.orgs.mu.Lock()
		s.orgs.running = nil
		s.orgs.mu.Unlock()
		close(r.done)
	}()
	return r
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

// awaitOrg waits for a poll that began after an org was named: one already running, or, if
// none is, a new one. The org came from a verified token or signed webhook that it does not
// serve yet, so a new customer's first sign-in succeeds. Only KyIdentity can name an org, and
// each one starts a poll at most every orgAskEvery; joining a running poll is always allowed.
func (s *Server) awaitOrg(ctx context.Context, org store.OrgID) {
	named := time.Now()
	t := time.NewTimer(orgAskWait)
	defer t.Stop()
	for {
		s.orgs.mu.Lock()
		r := s.orgs.running
		if r == nil {
			now := time.Now()
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
			r = s.pollOrgsLocked()
		}
		s.orgs.mu.Unlock()
		select {
		case <-r.done:
			if !r.began.Before(named) {
				return
			}
			// That poll may predate the org: wait for, or start, one more.
		case <-t.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// PurgeDeletedOrgs deletes the data of every org KyIdentity reported deleted (tenancy spec D12),
// in multi-tenant mode. A purge cut short resumes on the next run.
func (s *Server) PurgeDeletedOrgs(ctx context.Context) {
	ten, err := s.store.Tenancy(ctx)
	if err != nil || !ten.Multi {
		return
	}
	due, err := s.store.OrgsToPurge(ctx)
	if err != nil {
		log.Printf("orgs: listing deleted orgs: %v", err)
		return
	}
	for _, org := range due {
		if ctx.Err() != nil {
			return
		}
		if err := s.store.PurgeOrg(ctx, ten.Staff, org); err != nil {
			log.Printf("orgs: purge of %s stopped, resumes next run: %v", org, err)
			continue
		}
		log.Printf("orgs: purged %s", org)
	}
}

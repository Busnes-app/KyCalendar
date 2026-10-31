package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/feed"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/sso"
)

// Timing of the KyIdentity lookup. Variables so tests can shorten them.
var (
	kypostFirstWait = 5 * time.Second // startup waits this long for the first answer
	kypostRefresh   = time.Hour       // a working link is fetched again this often
	kypostRetry     = time.Minute     // first retry after a failure, doubling up to kypostRefresh
	kypostMinGap    = time.Minute     // a kick (a refused signature) waits this long after the last lookup
)

// Why there is no KyPost link (kypostView.Reason).
const (
	kypostPending       = "pending"        // not looked up yet
	kypostSchedOff      = "scheduling_off" // KYCALENDAR_SCHEDULING=false and no environment link: never looked up
	kypostNoKyIdentity  = "no_kyidentity"  // sign-in is not a live KyIdentity provider
	kypostNoPeer        = "no_peer"        // KyIdentity has no KyPost paired with KyCalendar
	kypostUnauthorized  = "unauthorized"   // KyIdentity refused KyCalendar's client credentials
	kypostUnreachable   = "unreachable"    // KyIdentity did not answer usefully
	kypostInvalidPeer   = "invalid_peer"   // KyIdentity's KyPost address breaks the URL rule
	kypostNoCredentials = "no_credentials" // the sign-in client secret could not be read
)

// kypostState is what the last lookup found, for the admin screen. The link itself is
// Server.kypost; only the lookup goroutine writes either.
type kypostState struct {
	mu      sync.Mutex
	reason  string    // "" while linked
	checked time.Time // the last lookup; zero for an environment link
	from    string    // issuer and client ID the KyIdentity link was issued to
}

func (k *kypostState) set(reason, from string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reason, k.checked, k.from = reason, time.Now(), from
}

func (k *kypostState) get() (reason string, checked time.Time, from string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reason, k.checked, k.from
}

// newKyPost builds the link, linked to the environment's KyPost when it names one, and turns
// scheduling on when that is already decided. Auto mode without an environment link waits for
// StartKyPost.
func (s *Server) newKyPost() {
	cal := s.config.Calendar
	// The private-address rule is the link's, so a KyIdentity address obeys it as an environment one does.
	policy := feed.RefusePrivate
	if cal.KyPostAllowPrivate {
		policy = sso.RefuseLocal
	}
	s.kypostKick = make(chan struct{}, 1)
	s.kypost = &kypost.Link{HTTP: sso.NewGuardedClient(policy, nil), Rotated: s.kickKyPost}
	s.kypostState.reason = kypostPending
	if cal.KyPostURL != "" {
		s.kypost.Set(&kypost.Peer{URL: cal.KyPostURL, Secret: cal.KyPostSecret, Source: kypost.SourceEnvironment})
		s.kypostState.reason = ""
	} else if cal.Scheduling == config.SchedulingOff {
		s.kypostState.reason = kypostSchedOff
	}
	if cal.Scheduling == config.SchedulingOn || cal.Scheduling == config.SchedulingAuto && cal.KyPostURL != "" {
		s.startScheduling()
	}
}

func (s *Server) startScheduling() {
	s.sched = &davbackend.Scheduler{Store: s.store, Limits: s.objectLimits(), KyPost: s.kypost}
	s.kypostReplay = syncauth.NewMemoryReplay(syncauth.DefaultWindow, 100000)
}

// StartKyPost links KyPost through KyIdentity when the environment names none and scheduling
// may use it: it waits up to kypostFirstWait for the first lookup, turns automatic scheduling
// on if that linked, then keeps the link fresh until ctx ends. Scheduling is decided here,
// once: a link that appears later is used for mail but turns scheduling on only at the next
// start. cmd/server calls it after LoadSignIn and before serving.
func (s *Server) StartKyPost(ctx context.Context) {
	cal := s.config.Calendar
	if cal.KyPostURL != "" || cal.Scheduling == config.SchedulingOff {
		return
	}
	first, cancel := context.WithTimeout(ctx, kypostFirstWait)
	ok := s.resolveKyPost(first)
	cancel()
	select {
	case <-s.kypostKick: // LoadSignIn's: that provider was just looked up
	default:
	}
	if cal.Scheduling == config.SchedulingAuto && s.kypost.Peer() != nil {
		s.startScheduling()
	}
	if s.sched != nil {
		log.Printf("[KYPOST] scheduling on (KYCALENDAR_SCHEDULING=%s)", cal.Scheduling)
	} else {
		log.Printf("[KYPOST] scheduling off (KYCALENDAR_SCHEDULING=%s)", cal.Scheduling)
	}
	go s.kypostLoop(ctx, ok, kypostRefresh, kypostRetry, kypostMinGap)
}

// kickKyPost asks for a lookup soon: the sign-in provider changed or KyPost refused the secret.
func (s *Server) kickKyPost() {
	select {
	case s.kypostKick <- struct{}{}:
	default:
	}
}

// setSignIn makes p the live sign-in provider; the KyPost link follows it.
func (s *Server) setSignIn(p *sso.Provider) {
	s.signin.Store(p)
	if s.kypostKick != nil {
		s.kickKyPost()
	}
}

func (s *Server) kypostLoop(ctx context.Context, ok bool, refresh, retry, minGap time.Duration) {
	from, last, failures := s.signin.Load(), time.Now(), 0
	if !ok {
		failures = 1
	}
	for {
		wait := refresh
		if failures > 0 {
			wait = min(retry<<min(failures-1, 16), refresh)
		}
		next := last.Add(wait)
	sleep:
		for {
			t := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
				break sleep
			case <-s.kypostKick:
				t.Stop()
				// A new provider is looked up at once; anything else no sooner than minGap after
				// the last lookup, so refused signatures cannot hammer KyIdentity.
				soon := last.Add(minGap)
				if s.signin.Load() != from {
					soon = time.Now()
				}
				if soon.Before(next) {
					next = soon
				}
			}
		}
		from, last = s.signin.Load(), time.Now()
		if s.resolveKyPost(ctx) {
			failures = 0
		} else {
			failures++
		}
	}
}

// resolveKyPost asks KyIdentity for KyPost and links it, reporting whether the lookup worked
// (false: retry with backoff). A refusal unlinks; an unreachable KyIdentity keeps a link it
// issued to the same client, so a KyIdentity restart does not drop invitations. The secret is
// never logged.
func (s *Server) resolveKyPost(ctx context.Context) bool {
	p := s.signin.Load()
	if !s.config.SSO.Enabled || p == nil || p.Kind != sso.KindKyIdentity {
		return s.unlinkKyPost(kypostNoKyIdentity, nil)
	}
	dir, err := p.SuiteDirectory()
	if err != nil {
		return s.unlinkKyPost(kypostNoCredentials, err)
	}
	from := dir.Issuer + " " + dir.ClientID
	peer, err := dir.Peer(ctx, "kypost")
	switch {
	case errors.Is(err, suitedir.ErrNoPeer):
		return s.unlinkKyPost(kypostNoPeer, nil)
	case errors.Is(err, suitedir.ErrUnauthorized):
		return s.unlinkKyPost(kypostUnauthorized, err)
	case err != nil:
		if cur := s.kypost.Peer(); cur != nil {
			if _, _, linked := s.kypostState.get(); linked == from {
				log.Printf("[KYPOST] KyIdentity lookup failed, keeping the current link: %v", err)
				return false
			}
		}
		return s.unlinkKyPost(kypostUnreachable, err)
	}
	u, err := config.KyPostURL(peer.URL)
	if err != nil {
		return s.unlinkKyPost(kypostInvalidPeer, err)
	}
	if cur := s.kypost.Peer(); cur == nil || cur.URL != u || string(cur.Secret) != string(peer.Secret) {
		log.Printf("[KYPOST] linked through KyIdentity: %s", u)
	}
	s.kypost.Set(&kypost.Peer{URL: u, Secret: peer.Secret, Source: kypost.SourceKyIdentity})
	s.kypostState.set("", from)
	return true
}

// unlinkKyPost drops the link for reason, logging once per change of reason.
func (s *Server) unlinkKyPost(reason string, err error) bool {
	if prev, _, _ := s.kypostState.get(); prev != reason {
		if err != nil {
			log.Printf("[KYPOST] not linked through KyIdentity (%s): %v", reason, err)
		} else {
			log.Printf("[KYPOST] not linked through KyIdentity (%s)", reason)
		}
	}
	s.kypost.Set(nil)
	s.kypostState.set(reason, "")
	return false
}

// kypostView is the admin screen's KyPost status. It never carries the secret.
type kypostView struct {
	Connected      bool       `json:"connected"`
	Source         string     `json:"source,omitempty"` // environment or kyidentity
	URL            string     `json:"url,omitempty"`
	Reason         string     `json:"reason,omitempty"` // why not connected
	CheckedAt      *time.Time `json:"checked_at,omitempty"`
	Scheduling     bool       `json:"scheduling"`
	SchedulingMode string     `json:"scheduling_mode"` // auto, on or off
}

func (s *Server) handleKyPostStatus(w http.ResponseWriter, r *http.Request) {
	v := kypostView{Scheduling: s.sched != nil, SchedulingMode: s.config.Calendar.Scheduling}
	reason, checked, _ := s.kypostState.get()
	if !checked.IsZero() {
		v.CheckedAt = &checked
	}
	if p := s.kypost.Peer(); p != nil {
		v.Connected, v.Source, v.URL = true, p.Source, p.URL
	} else {
		v.Reason = reason
	}
	s.writeJSON(w, http.StatusOK, v)
}

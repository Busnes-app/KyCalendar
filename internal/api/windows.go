package api

import (
	"context"
	"log"
	"net/http"
	"net/netip"
	"time"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// windowTimeout bounds a shared-window statement with no request deadline above it (detached
// refunds, the long routes). A window the database cannot answer denies: the caller's 429 (or
// DAV's 503), never a fallback to process memory.
const windowTimeout = 5 * time.Second

// windowContext bounds a shared-window statement by the request's own deadline where it has
// one (the 14 s request deadline), so a slow database is not an outage before the request is,
// else by windowTimeout.
func windowContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, windowTimeout)
}

// Rate windows live in process memory on SQLite (one server) and in the database on
// PostgreSQL (s.shared), where every replica counts against the same limit on database time.
// Keys and limits are the same on both.

func (s *Server) allowAttempt(ctx context.Context, key string, limit int, window time.Duration) bool {
	if s.shared != nil {
		_, ok := s.takeShared(ctx, "ip:"+key, limit, window)
		return ok
	}
	now := time.Now()
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	if _, known := s.attempts[key]; !known && len(s.attempts) >= attemptsCap {
		s.makeRoom(now)
	}
	entry := bumpWindow(s.attempts[key], now, window)
	s.attempts[key] = entry
	return entry.count <= limit
}

// allowAccountAttempt counts a per-account window.
func (s *Server) allowAccountAttempt(ctx context.Context, key string, limit int, window time.Duration) bool {
	_, ok := s.takeAccountAttempt(ctx, key, limit, window)
	return ok
}

// takeAccountAttempt counts a per-account window and returns the refund for a limit that
// counts failures only: counting before the check keeps concurrent guesses from all passing.
// On PostgreSQL the refund reaches only the window it was taken from. In memory, keys are a
// real user ID plus the client address, so a new key past attemptsCap first drops the expired
// windows; a live one is never dropped, or flooding the map would reset someone's lockout.
func (s *Server) takeAccountAttempt(ctx context.Context, key string, limit int, window time.Duration) (refund func(), ok bool) {
	if s.shared != nil {
		return s.takeShared(ctx, "acct:"+key, limit, window)
	}
	now := time.Now()
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	if _, known := s.accounts[key]; !known && len(s.accounts) >= attemptsCap {
		for k, w := range s.accounts {
			if now.After(w.reset) {
				delete(s.accounts, k)
			}
		}
	}
	entry := bumpWindow(s.accounts[key], now, window)
	s.accounts[key] = entry
	return func() {
		s.attemptsMu.Lock()
		defer s.attemptsMu.Unlock()
		if e, ok := s.accounts[key]; ok && e.count > 0 {
			e.count--
			s.accounts[key] = e
		}
	}, entry.count <= limit
}

func noRefund() {}

// takeShared counts key in the database; an error denies.
func (s *Server) takeShared(ctx context.Context, key string, limit int, window time.Duration) (func(), bool) {
	c, cancel := windowContext(ctx)
	defer cancel()
	gen, _, ok, err := s.shared.TakeAttempt(c, key, limit, window)
	if err != nil {
		log.Printf("[SECURITY] rate window unavailable, denying: %v", err)
		return noRefund, false
	}
	return func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), windowTimeout)
		defer cancel()
		if err := s.shared.ReleaseAttempt(c, key, gen); err != nil {
			log.Printf("[SECURITY] rate window refund not recorded: %v", err)
		}
	}, ok
}

// officeBudget is what all allowlisted office networks together may spend each minute on one
// per-IP limit before their requests fall back to that limit. It keeps one host inside an
// office from making unbounded attempts. Sustained for the longest window it guards (DAV's 15
// minutes), 300 a minute is 4 500 keys, under attemptsCap and davFailKeysCap, so office
// traffic alone cannot fill a limiter and evict live counters.
const officeBudget = 300

// officeExempt reports whether the request comes from an allowlisted office network and may
// skip the per-IP limit named bucket, spending one unit of that bucket's shared office budget,
// and returns that unit's refund. It counts the request as exempted only then. It matches the
// address requestIP resolved, so a forged X-Forwarded-For from an untrusted peer cannot claim
// an allowlisted address. The budget lives with the per-account windows, which never drop a
// live entry, so a flood of address keys cannot reset it.
func (s *Server) officeExempt(r *http.Request, bucket string) (refund func(), ok bool) {
	list := s.exemptList(r.Context())
	addr, err := netip.ParseAddr(s.requestIP(r))
	if err != nil || !list.Contains(addr) {
		return noRefund, false
	}
	refund, ok = s.takeAccountAttempt(r.Context(), officeKey(bucket), officeBudget, time.Minute)
	if !ok {
		return noRefund, false
	}
	list.Exempt(addr)
	return refund, true
}

func officeKey(bucket string) string { return "office:" + bucket }

// allowPerIP is allowAttempt for a limit keyed only on the client address: an office request
// draws on the shared office budget instead, and on the ordinary per-IP window once that is
// spent. Only per-IP limits come here; per-account windows, the CAPTCHA and every later check
// run regardless.
func (s *Server) allowPerIP(r *http.Request, bucket string, limit int, window time.Duration) bool {
	if _, ok := s.officeExempt(r, bucket); ok {
		return true
	}
	return s.allowAttempt(r.Context(), bucket+":"+s.limitIP(r), limit, window)
}

// davReserve counts one DAV failure on key before the credential is checked, unless key is
// full, and returns the hand-back and the wait. err is a window the database cannot answer.
func (s *Server) davReserve(ctx context.Context, key string, limit int, now time.Time) (release func(), ok bool, wait time.Duration, err error) {
	if s.shared == nil {
		if ok, wait := s.davFails.reserve(key, limit, now); !ok {
			return noRefund, false, wait, nil
		}
		return func() { s.davFails.release(key) }, true, 0, nil
	}
	c, cancel := windowContext(ctx)
	defer cancel()
	gen, left, ok, err := s.shared.TakeAttempt(c, "dav:"+key, limit, davFailWindow)
	if err != nil {
		return noRefund, false, 0, err
	}
	if !ok {
		return noRefund, false, left, nil
	}
	return func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), windowTimeout)
		defer cancel()
		if err := s.shared.ReleaseAttempt(c, "dav:"+key, gen); err != nil {
			log.Printf("[SECURITY] DAV failure hand-back not recorded: %v", err)
		}
	}, true, 0, nil
}

// davBlocked reports whether key already has limit failures in its window.
func (s *Server) davBlocked(ctx context.Context, key string, limit int, now time.Time) (bool, time.Duration, error) {
	if s.shared == nil {
		blocked, wait := s.davFails.blocked(key, limit, now)
		return blocked, wait, nil
	}
	c, cancel := windowContext(ctx)
	defer cancel()
	hits, left, err := s.shared.AttemptHits(c, "dav:"+key)
	return hits >= limit, left, err
}

// davFail counts one failure on key after the fact.
func (s *Server) davFail(ctx context.Context, key string, limit int, now time.Time) {
	if s.shared == nil {
		s.davFails.fail(key, now)
		return
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), windowTimeout)
	defer cancel()
	if _, _, _, err := s.shared.TakeAttempt(c, "dav:"+key, limit, davFailWindow); err != nil {
		log.Printf("[SECURITY] DAV failure not recorded: %v", err)
	}
}

// powSpender is where a CAPTCHA solution is spent: this process on SQLite, the database on
// PostgreSQL, so a solution admits one request across every replica.
func (s *Server) powSpender(ctx context.Context) auth.Spender {
	if s.shared == nil {
		return &s.powSpent
	}
	return sharedSpender{ctx, s.shared}
}

type sharedSpender struct {
	ctx    context.Context
	shared store.CoordinationStore
}

func (p sharedSpender) Spend(salt string, expiresAt int64) bool {
	c, cancel := windowContext(p.ctx)
	defer cancel()
	ok, err := p.shared.SpendChallenge(c, salt, expiresAt*1000)
	if err != nil {
		log.Printf("[SECURITY] CAPTCHA spend unavailable, refusing: %v", err)
	}
	return ok && err == nil
}

package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	davFailWindow   = 15 * time.Minute
	davFailsPerIP   = 10
	davFailsPerUser = 50
	davFailKeysCap  = 10000
)

// unknownAuthAudit bounds the audit rows anyone can write with no credential at all: a failed
// app-password sign-in naming no real token ID. Rows are kept a year, and the SQLite database
// must fit one capsule, so an unbounded flood of guesses from many addresses would stop every
// backup and fill the disk. 200 a day is at most a few MiB a year; the rest are logged.
type unknownAuthAudit struct {
	mu  sync.Mutex
	day int64
	n   int
}

const unknownAuthAuditsPerDay = 200

// admit reports whether one more such row may be written today.
func (a *unknownAuthAudit) admit(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if day := now.Unix() / 86400; day != a.day {
		a.day, a.n = day, 0
	}
	a.n++
	if a.n == unknownAuthAuditsPerDay+1 {
		log.Printf("[AUTH] more than %d failed app-password sign-ins without a known token today; no more are audited until 00:00 UTC", unknownAuthAuditsPerDay)
	}
	return a.n <= unknownAuthAuditsPerDay
}

// davFailures counts failed DAV logins per key; successes never count, so syncing phones are not throttled.
type davFailures struct {
	mu   sync.Mutex
	seen map[string]davFailure
}

type davFailure struct {
	count int
	since time.Time
}

func (f *davFailures) blocked(key string, limit int, now time.Time) (bool, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.seen[key]
	if !ok || now.Sub(e.since) > davFailWindow {
		return false, 0
	}
	return e.count >= limit, davFailWindow - now.Sub(e.since)
}

func (f *davFailures) fail(key string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string]davFailure{}
	}
	e, known := f.seen[key]
	if !known && len(f.seen) >= davFailKeysCap {
		f.makeRoom(now)
	}
	if now.Sub(e.since) > davFailWindow {
		e = davFailure{since: now}
	}
	e.count++
	f.seen[key] = e
}

// reserve counts one failure for key before the credential is checked, unless key already has
// limit failures in its window, and reports the wait if so. Counting first means concurrent
// guesses cannot all pass a check none of them has recorded yet; release hands it back when the
// credential turns out right, so successes still never count.
func (f *davFailures) reserve(key string, limit int, now time.Time) (bool, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string]davFailure{}
	}
	e, known := f.seen[key]
	if known && now.Sub(e.since) <= davFailWindow && e.count >= limit {
		return false, davFailWindow - now.Sub(e.since)
	}
	if !known && len(f.seen) >= davFailKeysCap {
		f.makeRoom(now)
	}
	if now.Sub(e.since) > davFailWindow {
		e = davFailure{since: now}
	}
	e.count++
	f.seen[key] = e
	return true, 0
}

// release takes back one reservation; a key left with no failures is dropped.
func (f *davFailures) release(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.seen[key]
	switch {
	case !ok:
	case e.count <= 1:
		delete(f.seen, key)
	default:
		e.count--
		f.seen[key] = e
	}
}

// davOfficeBudget names the office budget for the per-IP failure key DAV and /mcp share.
const davOfficeBudget = "dav-fail"

// reserveDAVFailure reserves one failure on the per-IP key prefix+<address>, before any lookup or
// secret check. An office request (allowlisted network, office budget left) uses
// prefix+<address>|<hash of name> instead, so one stale device locks out only its own account
// or token; name is caller-chosen, so the key holds a fixed-size hash, never the name, and the
// office budget bounds how many such keys one office can mint. It answers 429 itself when the
// key is full. The returned release hands the reservation (and the office budget) back.
func (s *Server) reserveDAVFailure(w http.ResponseWriter, r *http.Request, prefix, name string, now time.Time) (release func(), ok bool) {
	key := prefix + s.limitIP(r)
	refundOffice, office := s.officeExempt(r, davOfficeBudget)
	if office {
		key += "|" + crypto.SHA256Hex([]byte(strings.ToLower(name)))[:32]
	}
	releaseKey, ok, wait, err := s.davReserve(r.Context(), key, davFailsPerIP, now)
	if err != nil || !ok {
		refundOffice()
		if err != nil {
			davUnavailable(w, err)
			return nil, false
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		http.Error(w, "Too many failed attempts", http.StatusTooManyRequests)
		return nil, false
	}
	return func() { releaseKey(); refundOffice() }, true
}

// davUnavailable answers a credential check the database could not make: 503, never 401, so
// a client keeps its credentials and nothing counts as a failure.
func davUnavailable(w http.ResponseWriter, err error) {
	log.Printf("[AUTH] app-password check unavailable: %v", err)
	w.Header().Set("Retry-After", "5")
	http.Error(w, "Temporarily unavailable", http.StatusServiceUnavailable)
}

// makeRoom drops expired entries, then the single oldest one; live lockouts are never mass-reset.
func (f *davFailures) makeRoom(now time.Time) {
	oldest, oldestAt := "", now
	for k, e := range f.seen {
		if now.Sub(e.since) > davFailWindow {
			delete(f.seen, k)
		} else if e.since.Before(oldestAt) || oldest == "" {
			oldest, oldestAt = k, e.since
		}
	}
	if len(f.seen) >= davFailKeysCap {
		delete(f.seen, oldest)
	}
}

type davUserKey struct{}

func davUser(ctx context.Context) *store.User {
	u, _ := ctx.Value(davUserKey{}).(*store.User)
	return u
}

func davChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="KyCalendar", charset="UTF-8"`)
	http.Error(w, "Authentication required", http.StatusUnauthorized)
}

func (s *Server) withDAVAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, token, ok := r.BasicAuth()
		if !ok {
			davChallenge(w)
			return
		}
		if !validDAVUsername(username) {
			davChallenge(w)
			return
		}
		now := time.Now()
		release, reserved := s.reserveDAVFailure(w, r, "ip:", username, now)
		if !reserved {
			return
		}
		id, secret, parsed := apppass.Parse(token)
		var user *store.User
		var secretOK bool
		if parsed {
			var err error
			if user, secretOK, err = s.resolveAppPassword(r.Context(), username, id, secret); err != nil {
				release()
				davUnavailable(w, err)
				return
			}
		}
		// Only a holder of one of the user's token IDs can count against them (R28).
		resource := "user:unknown"
		if user != nil {
			resource = user.ID
			if limited, err := s.davLimited(w, r, "user:"+user.ID, davFailsPerUser, now); limited {
				if secretOK || err != nil { // the right secret is no guess; a wrong one stays counted
					release()
				}
				return
			}
		}
		if user == nil || !secretOK || user.Status != "active" {
			if user != nil && !secretOK {
				s.davFail(r.Context(), "user:"+user.ID, davFailsPerUser, now)
			}
			// Not shaped like an app password (an account password, a probe): no row to flood with.
			if parsed && (user != nil || s.unknownAudit.admit(now)) {
				_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{Action: "dav.auth_failed", Resource: resource, IPAddress: s.requestIP(r)})
			}
			davChallenge(w)
			return
		}
		release()
		if user.Role == "admin" {
			http.Error(w, "Administrator accounts cannot use calendars", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), davUserKey{}, user)))
	})
}

const davMaxUsername = 64

// validDAVUsername bounds attacker-controlled input before it keys the limiter or touches the store.
func validDAVUsername(name string) bool {
	if name == "" || len(name) > davMaxUsername {
		return false
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// davLimited answers 429 when key is full, or 503 when the window cannot be read (err set).
func (s *Server) davLimited(w http.ResponseWriter, r *http.Request, key string, limit int, now time.Time) (bool, error) {
	blocked, wait, err := s.davBlocked(r.Context(), key, limit, now)
	switch {
	case err != nil:
		davUnavailable(w, err)
		return true, err
	case blocked:
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		http.Error(w, "Too many failed attempts", http.StatusTooManyRequests)
	}
	return blocked, nil
}

// resolveAppPassword returns the owner of token ID id when it exists and belongs to the named
// user (case-insensitively), and whether the secret matches a dav token. An MCP token's right
// secret is refused like a wrong one. It touches last-used on a match. err is a store failure,
// never a missing token or user.
func (s *Server) resolveAppPassword(ctx context.Context, username, id, secret string) (*store.User, bool, error) {
	p, user, ok, err := s.appPasswordOwner(ctx, id, secret)
	if err != nil {
		return nil, false, err
	}
	if user == nil || !strings.EqualFold(user.Username, username) {
		return nil, false, nil
	}
	if !ok || p.Scope != store.ScopeDAV {
		return user, false, nil
	}
	_ = s.store.AppPasswords().TouchLastUsed(ctx, p.ID, time.Now())
	return user, true, nil
}

// appPasswordOwner loads token ID id and its owner, and reports whether secret matches and the
// owner may sign in. It writes nothing. A missing token or owner is no error; any other store
// failure is, and the caller answers 503 instead of a failed sign-in.
func (s *Server) appPasswordOwner(ctx context.Context, id, secret string) (*store.AppPassword, *store.User, bool, error) {
	p, err := s.store.AppPasswords().Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	user, err := s.store.Users().GetUserByID(ctx, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	allowed, err := s.store.InstallerUserAllowed(ctx, user)
	if err != nil {
		return nil, nil, false, err
	}
	if !allowed {
		return p, user, false, nil
	}
	return p, user, apppass.Matches(p.Hash, secret), nil
}

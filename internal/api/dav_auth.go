package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	davFailWindow   = 15 * time.Minute
	davFailsPerIP   = 10
	davFailsPerUser = 50
	davFailKeysCap  = 10000
)

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
		ipKey := "ip:" + s.requestIP(r)
		if s.davLimited(w, ipKey, davFailsPerIP, now) {
			return
		}
		user, secretOK := s.resolveAppPassword(r.Context(), username, token)
		// Only a holder of one of the user's token IDs can count against them (R28).
		resource := "user:unknown"
		if user != nil {
			resource = user.ID
			if s.davLimited(w, "user:"+user.ID, davFailsPerUser, now) {
				return
			}
		}
		if user == nil || !secretOK || user.Status != "active" {
			s.davFails.fail(ipKey, now)
			if user != nil && !secretOK {
				s.davFails.fail("user:"+user.ID, now)
			}
			_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{Action: "dav.auth_failed", Resource: resource, IPAddress: s.requestIP(r)})
			davChallenge(w)
			return
		}
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

func (s *Server) davLimited(w http.ResponseWriter, key string, limit int, now time.Time) bool {
	blocked, wait := s.davFails.blocked(key, limit, now)
	if blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		http.Error(w, "Too many failed attempts", http.StatusTooManyRequests)
	}
	return blocked
}

// resolveAppPassword returns the token's owner when its ID exists and belongs to the named
// user (case-insensitively), and whether the secret matches. It touches last-used on a match.
func (s *Server) resolveAppPassword(ctx context.Context, username, token string) (*store.User, bool) {
	id, secret, ok := apppass.Parse(token)
	if !ok {
		return nil, false
	}
	p, err := s.store.AppPasswords().Get(ctx, id)
	if err != nil {
		return nil, false
	}
	user, err := s.store.Users().GetUserByID(ctx, p.UserID)
	if err != nil || !strings.EqualFold(user.Username, username) {
		return nil, false
	}
	if !apppass.Matches(p.Hash, secret) {
		return user, false
	}
	_ = s.store.AppPasswords().TouchLastUsed(ctx, p.ID, time.Now())
	return user, true
}

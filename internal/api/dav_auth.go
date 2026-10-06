package api

import (
	"context"
	"net/http"
	"strconv"
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
	if f.seen == nil || len(f.seen) >= davFailKeysCap {
		f.seen = map[string]davFailure{}
	}
	e := f.seen[key]
	if now.Sub(e.since) > davFailWindow {
		e = davFailure{since: now}
	}
	e.count++
	f.seen[key] = e
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
		now := time.Now()
		ipKey, userKey := "ip:"+s.requestIP(r), "user:"+username
		for _, k := range []struct {
			key   string
			limit int
		}{{ipKey, davFailsPerIP}, {userKey, davFailsPerUser}} {
			if blocked, wait := s.davFails.blocked(k.key, k.limit, now); blocked {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				http.Error(w, "Too many failed attempts", http.StatusTooManyRequests)
				return
			}
		}

		user, ok := s.checkAppPassword(r.Context(), username, token)
		if !ok {
			s.davFails.fail(ipKey, now)
			s.davFails.fail(userKey, now)
			_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{Action: "dav.auth_failed", Resource: "user:" + username, IPAddress: s.requestIP(r)})
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

// checkAppPassword resolves the user only if the token is theirs and they are active.
func (s *Server) checkAppPassword(ctx context.Context, username, token string) (*store.User, bool) {
	id, secret, ok := apppass.Parse(token)
	if !ok {
		return nil, false
	}
	p, err := s.store.AppPasswords().Get(ctx, id)
	if err != nil || !apppass.Matches(p.Hash, secret) {
		return nil, false
	}
	user, err := s.store.Users().GetUserByUsername(ctx, username)
	if err != nil || user.ID != p.UserID || user.Status != "active" {
		return nil, false
	}
	_ = s.store.AppPasswords().TouchLastUsed(ctx, p.ID, time.Now())
	return user, true
}

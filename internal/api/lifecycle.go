package api

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"
)

// readinessTTL caps database load from probes: concurrent probes share one ping a second.
const readinessTTL = time.Second

// readinessPingTimeout is how long a ping may take before the server reports unready.
const readinessPingTimeout = time.Second

// readiness caches the last store ping. The mutex is held across the ping, so a burst of
// probes waits for one result instead of starting one ping each.
type readiness struct {
	mu      sync.Mutex
	checked time.Time
	ready   bool
}

// StartDraining turns /readyz to 503 and refuses new deposits, drills and exports, while
// every other route keeps serving, so a proxy stops routing here before the listener closes.
func (s *Server) StartDraining() { s.draining.Store(true) }

// Draining reports whether StartDraining was called.
func (s *Server) Draining() bool { return s.draining.Load() }

// handleLivez says the process serves. It never touches the database: a database outage is
// /readyz's business, and restarting every replica would not end it.
func (s *Server) handleLivez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz says whether to route requests here: not while draining, and not when the
// database does not answer a ping within readinessPingTimeout. Status word only.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.draining.Load() && s.storeReady(r.Context()) {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unready"})
}

func (s *Server) storeReady(ctx context.Context) bool {
	s.ready.mu.Lock()
	defer s.ready.mu.Unlock()
	if !s.ready.checked.IsZero() && time.Since(s.ready.checked) < readinessTTL {
		return s.ready.ready
	}
	// Detached from the probe so one probe hanging up does not mark the server unready. The
	// ping runs on its own goroutine: the timeout decides, not the driver's own give-up.
	ping, cancel := context.WithTimeout(context.WithoutCancel(ctx), readinessPingTimeout)
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- s.store.Ping(ping) }()
	select {
	case err := <-res:
		s.ready.ready = err == nil
	case <-ping.Done():
		s.ready.ready = false
	}
	s.ready.checked = time.Now()
	return s.ready.ready
}

// requestDeadline bounds every ordinary request, below the listener's 15 s WriteTimeout so
// the 503 still reaches the client. Without it a frozen database (a paused VM, a storage
// hang) holds requests open with no limit. A 503 after it does not prove the request had no
// effect: the handler may still finish once the database answers.
var requestDeadline = 14 * time.Second

// longRoutes are the patterns the deadline does not cover, each keeping the budget its slow
// work already has. Their database calls are not bounded by it either.
// TestLongRoutesAreExactlyTheNamedExemptions pins this list.
var longRoutes = map[string]string{
	"POST /api/backup/deposit":         "depositWriteBudget (16 min): snapshot, seal and upload a capsule",
	"POST /api/backup/drill":           "depositWriteBudget (16 min): snapshot, seal and open a capsule in a sandbox",
	"POST /api/backup/export-capsule":  "depositWriteBudget (16 min): snapshot, seal and download a capsule",
	"POST /api/backup/pair-remote":     "pairWriteBudget (1 min): the KyRecovery claim (30 s client) and the pin",
	"POST /api/calendars/{id}/import":  "importWriteBudget (5 min): a CSV written one event at a time",
	"POST /api/subscriptions":          "feedWriteBudget (2 min): the first fetch of a published calendar",
	"PATCH /api/subscriptions/{id}":    "feedWriteBudget (2 min): a fetch of the changed link",
	"POST /api/calendars/{id}/refresh": "feedWriteBudget (2 min): a fetch on demand",
	"/api/sso/kysignon/callback":       "sign-in provider token exchange and keys (20 s client)",
	"PUT /api/admin/signin":            "sign-in provider discovery (20 s client)",
	"POST /api/admin/signin/test":      "sign-in provider discovery (20 s client)",
	"POST /api/book/{slug}/hold":       "the hold's mail through KyPost (20 s client)",
	"POST /api/presence":               "KyMessages presence lookup (20 s client)",
	"GET /api/presence/me":             "KyMessages presence (20 s client)",
	"PUT /api/presence/me":             "KyMessages presence (20 s client)",
	"POST /api/suite/chat":             "KyMessages chat link (20 s client)",
}

// withDeadline answers 503 JSON when next has not answered within d. Like
// http.TimeoutHandler, the handler writes into a buffer sent when it returns in time and
// discarded otherwise; its context ends at d, so store and outbound calls stop.
func (s *Server) withDeadline(next http.Handler, d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := s.mux.Handler(r); longRoutes[pattern] != "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		tw := &deadlineWriter{h: http.Header{}}
		done, panicked := make(chan struct{}), make(chan any, 1)
		go func() {
			defer func() {
				if p := recover(); p != nil {
					panicked <- p
				}
			}()
			next.ServeHTTP(tw, r.WithContext(ctx))
			close(done)
		}()
		select {
		case p := <-panicked:
			panic(p)
		case <-done:
			tw.mu.Lock()
			defer tw.mu.Unlock()
			if !tw.wroteHeader {
				tw.code, tw.sent = http.StatusOK, tw.h.Clone()
			}
			for k, v := range tw.sent {
				w.Header()[k] = v
			}
			w.WriteHeader(tw.code)
			_, _ = w.Write(tw.buf.Bytes())
		case <-ctx.Done():
			tw.mu.Lock()
			defer tw.mu.Unlock()
			tw.timedOut = true
			s.securityHeaders(w.Header())
			w.Header().Set("Cache-Control", "no-store")
			s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "request_timeout",
				"error": "The server could not finish this request in time. It may still take effect; check before retrying."})
		}
	})
}

type deadlineWriter struct {
	mu sync.Mutex
	h  http.Header
	// sent is h as it stood at WriteHeader: as with net/http, later changes are not sent.
	sent                  http.Header
	buf                   bytes.Buffer
	code                  int
	wroteHeader, timedOut bool
}

func (tw *deadlineWriter) Header() http.Header { return tw.h }

func (tw *deadlineWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !tw.wroteHeader {
		tw.wroteHeader, tw.code, tw.sent = true, http.StatusOK, tw.h.Clone()
	}
	return tw.buf.Write(p)
}

func (tw *deadlineWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.timedOut && !tw.wroteHeader {
		tw.wroteHeader, tw.code, tw.sent = true, code, tw.h.Clone()
	}
}

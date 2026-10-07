package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/web"
)

// recoveryClient is the KyRecovery client as the handlers use it, narrowed so tests can stand
// in a fake without reaching the network.
type recoveryClient interface {
	ClaimPairing(ctx context.Context, serverURL, pairingCode, serviceName, appName string) (recoveryclient.PairingResult, error)
	recoveryclient.Depositor
}

type Server struct {
	config     *config.Config
	store      store.Store
	sessions   *auth.SessionManager
	kysignon   *sso.KySignOnClient
	saml       *sso.SAMLServiceProvider
	scim       *scim.Server
	recovery   recoveryClient
	mux        *http.ServeMux
	patterns   []string // every registered route, for the authorization matrix
	attemptsMu sync.Mutex
	attempts   map[string]attemptWindow
	accounts   map[string]attemptWindow // per-account windows, apart from the evictable per-IP map
	davFails   davFailures
	// detached counts the requests running on a context deliberately separated from their
	// connection. http.Server.Shutdown does not know about them, so runServer waits on this
	// before the store closes.
	detached detachedCounter
}

// detachedCounter is a WaitGroup that tolerates a registration arriving while the wait is
// already running. sync.WaitGroup panics on an Add from zero concurrent with Wait, and there is
// no barrier that rules that out here: Shutdown returns when its own timeout expires, with
// requests still in flight, so a second admin request can register just as the first finishes
// and drops the count to zero. A counter under a condition variable has no such rule.
type detachedCounter struct {
	once sync.Once
	mu   sync.Mutex
	cond *sync.Cond
	n    int
}

// signal builds the condition variable on first use, so the zero value of Server works.
func (d *detachedCounter) signal() *sync.Cond {
	d.once.Do(func() { d.cond = sync.NewCond(&d.mu) })
	return d.cond
}

func (d *detachedCounter) add() {
	c := d.signal()
	c.L.Lock()
	d.n++
	c.L.Unlock()
}

func (d *detachedCounter) done() {
	c := d.signal()
	c.L.Lock()
	d.n--
	c.L.Unlock()
	c.Broadcast()
}

// tracked counts a request as detached for as long as h runs. It wraps the auth middleware
// rather than the handler: requireAdmin authenticates against the store before the handler is
// reached, ReadTimeout (15s) outlasts cmd/server's shutdownTimeout (5s), and a SIGTERM landing
// during that lookup would otherwise leave the counter at zero, WaitDetached returning and the
// store closing under a request about to pin a key.
//
// The window before ServeHTTP is entered -- while net/http is still reading the request line
// and headers -- cannot be covered by any counter: there is no handler goroutine to register
// yet. Shutdown's own drain is all that covers it, which is why shutdownTimeout is spent first.
func (s *Server) tracked(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.detached.add()
		defer s.detached.done()
		h(w, r)
	}
}

// WaitDetached blocks until every request that detached from its connection has finished. It is
// called after http.Server.Shutdown and before the store is closed: pairing, the key pin and a
// deposit all keep writing after their connection is gone, and a closed store under them leaves
// a key pinned on disk with no row recording it.
func (s *Server) WaitDetached() {
	c := s.detached.signal()
	c.L.Lock()
	defer c.L.Unlock()
	for s.detached.n > 0 {
		c.Wait()
	}
}

type attemptWindow struct {
	count int
	reset time.Time
}

// attemptsCap bounds the limiter map. Unauthenticated callers influence the keys, so the map
// is itself attack surface. At the cap we evict, never refuse: refusing every unknown key
// would let one caller fill the map and lock every new client out of login.
//
// The trade-off: memory is bounded, but an attacker who fills the map shortens other clients'
// windows, since an evicted counter starts again from zero. That weakens throttling while the
// attack runs; it never locks anyone out, which is the failure mode worth avoiding.
//
// Eviction is deliberately blind to how much of a window is left. Picking the entry nearest to
// expiry would always sacrifice the shortest windows first, so a caller minting keys with a
// long window could keep the one-minute login counter from ever reaching its limit. Every key
// is therefore equally likely to go. The real defence is that no key carries caller-supplied
// bytes, so filling the map costs an attacker one slot per IPv4 address or IPv6 /64.
// Per-account windows live in their own map, so no flood of address keys can reset them.
const attemptsCap = 10000

func NewServer(cfg *config.Config, st store.Store) *Server {
	sessions := auth.NewSessionManager(st, cfg.Security)
	kysignon := sso.NewKySignOnClient(cfg.SSO, st)
	saml := sso.NewSAMLServiceProvider(cfg.SSO.SAMLEntityID, cfg.Server.AppURL+"/saml/acs")
	scimSrv := scim.NewServer(st, cfg.SCIM, cfg.Server.AppURL)
	recovery := recoveryclient.NewClient(recoveryclient.Options{AllowPrivate: cfg.Backup.AllowPrivateRecovery})

	s := &Server{
		config:   cfg,
		store:    st,
		sessions: sessions,
		kysignon: kysignon,
		saml:     saml,
		scim:     scimSrv,
		recovery: recovery,
		mux:      http.NewServeMux(),
		attempts: make(map[string]attemptWindow),
		accounts: make(map[string]attemptWindow),
	}

	s.routes()
	return s
}

func (s *Server) allowAttempt(key string, limit int, window time.Duration) bool {
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

// allowAccountAttempt counts a per-account window. Keys are built only from real user IDs, so
// the map is bounded by the user count and never evicts.
func (s *Server) allowAccountAttempt(key string, limit int, window time.Duration) bool {
	now := time.Now()
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	entry := bumpWindow(s.accounts[key], now, window)
	s.accounts[key] = entry
	return entry.count <= limit
}

// makeRoom frees a slot for a new key: it drops every expired window, and if the map is still
// full it drops one live entry chosen at random, never the one nearest expiry. Caller holds
// attemptsMu. The scan is O(attemptsCap) and only runs for a new key while the map is full;
// 10 000 entries is microseconds.
func (s *Server) makeRoom(now time.Time) {
	for candidate, w := range s.attempts {
		if now.After(w.reset) {
			delete(s.attempts, candidate)
		}
	}
	if len(s.attempts) >= attemptsCap {
		// Go randomises map iteration, so the first entry is an unbiased victim.
		for candidate := range s.attempts {
			delete(s.attempts, candidate)
			break
		}
	}
}

func bumpWindow(entry attemptWindow, now time.Time, window time.Duration) attemptWindow {
	if now.After(entry.reset) {
		entry = attemptWindow{reset: now.Add(window)}
	}
	entry.count++
	return entry
}

// requestIP is the limiter's key for unauthenticated routes. It resolves to the same address
// a session is bound to, and honours X-Forwarded-For only from a configured trusted proxy:
// keying on a caller-supplied header would make every limit here bypassable.
func (s *Server) requestIP(r *http.Request) string {
	return auth.ClientIP(r, s.config.Security.TrustedProxies)
}

// limitIP is requestIP with IPv6 cut to its /64: one host is routinely given a whole /64, so
// per-address keys would hand it endless fresh windows.
func (s *Server) limitIP(r *http.Request) string {
	ip := s.requestIP(r)
	if a, err := netip.ParseAddr(ip); err == nil && a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip
}

// handle registers a route and records its pattern, so the authorization matrix can prove it
// covers every route.
func (s *Server) handle(pattern string, h http.Handler) {
	s.patterns = append(s.patterns, pattern)
	s.mux.Handle(pattern, h)
}

func (s *Server) routes() {
	// Auth
	s.handle("/api/auth/pow-challenge", http.HandlerFunc(s.handlePoWChallenge))
	s.handle("/api/auth/login", http.HandlerFunc(s.handleLogin))
	s.handle("/api/auth/mfa/totp", http.HandlerFunc(s.handleMFATOTP))
	s.handle("/api/auth/mfa/recovery-code", http.HandlerFunc(s.handleMFARecovery))
	s.handle("/api/auth/logout", http.HandlerFunc(s.handleLogout))
	s.handle("/api/auth/me", http.HandlerFunc(s.handleMe))
	s.handle("/api/auth/change-password", http.HandlerFunc(s.handleChangePassword))

	// SSO
	s.handle("/api/sso/kysignon/login", s.requireSSO(s.handleKySignOnLogin))
	s.handle("/api/sso/kysignon/callback", s.requireSSO(s.handleKySignOnCallback))
	s.handle("/api/sso/kysignon/sync", s.requireSSO(s.handleKySignOnSyncWebhook))
	s.handle("/saml/metadata", http.HandlerFunc(s.handleSAMLMetadata))

	// Feature 0 KyBackup & Restore Drills. Capsules carry site data and keys: admins only.
	// Method patterns: only the declared method reaches a handler. Export is a POST so the
	// CSRF check covers a download that carries the whole instance.
	s.handle("POST /api/backup/drill", s.requireAdmin(s.handleBackupDrill))
	s.handle("POST /api/backup/export-capsule", s.requireAdmin(s.handleExportCapsule))
	s.handle("POST /api/backup/pair-remote", s.tracked(s.requireAdmin(s.handlePairRemoteRecovery)))
	s.handle("POST /api/backup/deposit", s.tracked(s.requireAdmin(s.handleRunBackup)))
	s.handle("DELETE /api/backup/pairing", s.requireAdmin(s.handleUnpair))
	s.handle("POST /api/backup/pin-key", s.tracked(s.requireAdmin(s.handlePinKey)))
	s.handle("PUT /api/backup/schedule", s.requireAdmin(s.handleSetSchedule))
	s.handle("GET /api/backup/status", s.requireAdmin(s.handleBackupStatus))

	// Settings & Theme. The read endpoint tiers its own payload by role.
	s.handle("/api/settings", http.HandlerFunc(s.handleGetSettings))
	s.handle("/api/settings/theme", s.requireAdmin(s.handleSetTheme))

	// SCIM 2.0 routes
	s.scim.RegisterRoutes(s.handle)

	// App passwords for native CalDAV clients. Everyday users only.
	s.handle("GET /api/app-passwords", s.requireEveryday(s.handleListAppPasswords))
	s.handle("POST /api/app-passwords", s.requireEveryday(s.handleCreateAppPassword))
	s.handle("DELETE /api/app-passwords/{id}", s.requireEveryday(s.handleDeleteAppPassword))

	// Group calendars: administrators create and delete them; administrators and managers
	// change grants. None of these routes reads or writes events.
	s.handle("GET /api/admin/calendars", s.requireAdmin(s.handleListGroupCalendars))
	s.handle("POST /api/admin/calendars", s.requireAdmin(s.handleCreateGroupCalendar))
	s.handle("DELETE /api/admin/calendars/{id}", s.tracked(s.requireAdmin(s.handleDeleteGroupCalendar)))
	s.handle("GET /api/admin/groups", s.requireAdmin(s.handleListGroups))
	s.handle("POST /api/admin/groups", s.requireAdmin(s.handleCreateGroup))
	s.handle("GET /api/admin/groups/{id}", s.requireAdmin(s.handleGetGroup))
	s.handle("PATCH /api/admin/groups/{id}", s.requireAdmin(s.handleRenameGroup))
	s.handle("GET /api/admin/users", s.requireAdmin(s.handleListUsers))
	s.handle("GET /api/admin/audit", s.requireAdmin(s.handleListAudit))
	s.handle("GET /api/calendars/{id}/grants", s.requireSession(s.handleListGrants))
	s.handle("PUT /api/calendars/{id}/grants/{group}", s.requireSession(s.handleSetGrant))
	s.handle("DELETE /api/calendars/{id}/grants/{group}", s.requireSession(s.handleDeleteGrant))

	// Calendars and events for everyday users. Roles come from access.Resolve; a calendar the
	// caller cannot read is 404.
	s.handle("GET /api/calendars", s.requireEveryday(s.handleListCalendars))
	s.handle("POST /api/calendars", s.requireEveryday(s.handleCreateCalendar))
	s.handle("PATCH /api/calendars/{id}", s.requireEveryday(s.handlePatchCalendar))
	s.handle("DELETE /api/calendars/{id}", s.tracked(s.requireEveryday(s.handleDeleteCalendar)))
	s.handle("GET /api/events", s.requireEveryday(s.handleListEvents))
	s.handle("POST /api/calendars/{id}/events", s.requireEveryday(s.handleCreateEvent))
	s.handle("PUT /api/events/{cal}/{uid}", s.requireEveryday(s.handleUpdateEvent))
	s.handle("DELETE /api/events/{cal}/{uid}", s.requireEveryday(s.handleDeleteEvent))

	// CalDAV for native clients; app-password Basic auth, never the session cookie.
	dav := s.withDAVAuth(http.HandlerFunc(s.handleDAV))
	s.handle("/dav/", dav)
	s.handle("/.well-known/caldav", dav)

	// Embedded React PWA Frontend
	s.handle("/", web.Handler())
}

type sessionUserKey struct{}

// authenticate resolves the session, or writes the 401 (or password-change 403) answer and
// returns nil.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) *store.User {
	user, _, err := s.sessions.AuthenticateRequest(r)
	if err == nil {
		return user
	}
	if errors.Is(err, auth.ErrPasswordChangeRequired) {
		s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Change your password before continuing", "code": "password_change_required"})
	} else {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
	}
	return nil
}

func withSessionUser(r *http.Request, u *store.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionUserKey{}, u))
}

// sessionUser is the user requireSession, requireAdmin or requireEveryday authenticated.
func sessionUser(ctx context.Context) *store.User {
	u, _ := ctx.Value(sessionUserKey{}).(*store.User)
	return u
}

// requireSession admits any signed-in user; the handler decides by role.
func (s *Server) requireSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if user := s.authenticate(w, r); user != nil {
			h(w, withSessionUser(r, user))
		}
	}
}

// requireAdmin rejects requests without a valid session, or with a non-admin one.
func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.authenticate(w, r)
		if user == nil {
			return
		}
		if user.Role != "admin" {
			s.writeError(w, http.StatusForbidden, "Administrator role required")
			return
		}
		h(w, withSessionUser(r, user))
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	if s.config.Security.CookieSecure {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}

	origin := r.Header.Get("Origin")
	if origin != "" && sameOrigin(origin, s.config.Server.AppURL) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-KySignOn-Signature")

	// Bare-host CalDAV discovery: 308 keeps the method, as the fork's own well-known hop does.
	if r.URL.Path == "/" && (r.Method == "PROPFIND" || r.Method == "REPORT" || r.Method == http.MethodOptions) {
		http.Redirect(w, r, "/.well-known/caldav", http.StatusPermanentRedirect)
		return
	}

	if r.Method == http.MethodOptions && !isDAVPath(r.URL.Path) {
		if origin != "" && !sameOrigin(origin, s.config.Server.AppURL) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if isUnsafeMethod(r.Method) && hasSessionCookie(r) && !csrfExempt(r.URL.Path) && !auth.ValidateCSRF(r) {
		s.writeError(w, http.StatusForbidden, "Invalid CSRF token")
		return
	}
	// Login and MFA run before a session (and its CSRF token) exists; a cross-site form posting
	// there would plant the attacker's session in the victim's browser.
	if isUnsafeMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/api/auth/") && crossSite(r) {
		s.writeError(w, http.StatusForbidden, "Cross-site request refused")
		return
	}
	if r.Body != nil {
		limit := int64(1 << 20)
		if isDAVPath(r.URL.Path) {
			limit = calendar.MaxObjectSize + 1<<16 // the fork answers max-resource-size itself
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}

	// SCIM middleware
	if strings.HasPrefix(r.URL.Path, "/scim/v2") {
		s.scim.AuthMiddleware(s.mux).ServeHTTP(w, r)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func hasSessionCookie(r *http.Request) bool {
	cookie, err := r.Cookie(auth.SessionCookieName)
	return err == nil && cookie.Value != ""
}

// crossSite reports a browser request from another site. Non-browser clients omit
// Sec-Fetch-Site and pass; so do browsers older than the header, which stay exposed.
func crossSite(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	return site != "" && site != "same-origin" && site != "none"
}

// requireSSO makes KY_SSO_ENABLED=false a kill switch for sign-in, provisioning and sync.
func (s *Server) requireSSO(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.config.SSO.Enabled {
			s.writeError(w, http.StatusNotFound, "Single sign-on is disabled")
			return
		}
		next(w, r)
	}
}

func csrfExempt(path string) bool {
	return path == "/api/auth/login" || strings.HasPrefix(path, "/api/auth/mfa/") || path == "/api/sso/kysignon/sync"
}

func sameOrigin(origin, appURL string) bool {
	a, err := url.Parse(appURL)
	if err != nil || a.Scheme == "" || a.Host == "" {
		return false
	}
	o, err := url.Parse(origin)
	return err == nil && o.Scheme == a.Scheme && o.Host == a.Host
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

package api

import (
	"net/http"
	"slices"
	"time"

	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// SetRecoveryClientForTest replaces the KyRecovery pairing client. Test-only: this file is not
// part of the package build.
func SetRecoveryClientForTest(s *Server, p recoveryClient) { s.recovery = p }

// SetStoreForTest replaces the handlers' store, so a test can break one write. Test-only.
func SetStoreForTest(s *Server, st store.Store) { s.store = st }

// AttemptsCapForTest is the limiter's hard bound on distinct keys.
const AttemptsCapForTest = attemptsCap

// AttemptKeysForTest returns the limiter's live keys. Test-only: this file is not part of the
// package build.
func AttemptKeysForTest(s *Server) []string {
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	keys := make([]string, 0, len(s.attempts))
	for k := range s.attempts {
		keys = append(keys, k)
	}
	return keys
}

// AllowAttemptForTest drives the limiter directly so a test can fill it without paying for
// 10 000 HTTP requests. Test-only.
func AllowAttemptForTest(s *Server, key string, limit int, window time.Duration) bool {
	return s.allowAttempt(key, limit, window)
}

// RegisterDetachedForTest registers one detached handler and returns its unregister func, so a
// test can drive the counter without an HTTP request. Test-only.
func RegisterDetachedForTest(s *Server) func() {
	s.detached.add()
	return s.detached.done
}

// SetStepUpWindowForTest changes how recent a sign-in must be for step-up actions and returns
// the restore func. Test-only.
func SetStepUpWindowForTest(d time.Duration) func() {
	old := stepUpWindow
	stepUpWindow = d
	return func() { stepUpWindow = old }
}

// RoutesForTest returns every registered route pattern. Test-only.
func RoutesForTest(s *Server) []string { return slices.Clone(s.patterns) }

// SetExpandTimeForTest changes the per-request event expansion budget and returns the restore
// func. Test-only.
func SetExpandTimeForTest(d time.Duration) func() {
	old := maxExpandTime
	maxExpandTime = d
	return func() { maxExpandTime = old }
}

// SetSignInHTTPClientForTest replaces the guarded client that reaches admin-entered providers,
// so a test can trust httptest's certificate and dial its loopback listener. Test-only.
func SetSignInHTTPClientForTest(s *Server, c *http.Client) { s.signinHTTP = c }

// SetFeedClientForTest replaces the client that fetches subscribed links. Test-only.
func SetFeedClientForTest(s *Server, c *http.Client) { s.feed.Client = c }

// SetSignInForTest makes p the live sign-in provider (nil closes sign-in). Test-only.
func SetSignInForTest(s *Server, p *sso.Provider) { s.signin.Store(p) }

// MCP tool errors the skill must name, and the app version its frontmatter must carry.
const (
	MCPReadOnlyForTest   = msgReadOnly
	MCPNotOwnForTest     = msgNotOwn
	MCPMeetingForTest    = msgMeeting
	MCPSubscribedForTest = msgSubscribed
	AppVersionForTest    = appVersion
)

// SetKyPostClientForTest points the scheduler's KyPost client at c (an httptest server's client:
// the real guarded client refuses loopback). Scheduling and KyPost must be configured.
func SetKyPostClientForTest(s *Server, c *kypost.Client) { s.sched.KyPost = c }

package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	SessionCookieName = "ky_session"
	CSRFCookieName    = "ky_csrf"
	HeaderCSRF        = "X-CSRF-Token"
	// PreSessionCookieName carries the random value a signed-out browser's CSRF token is bound
	// to, so sign-in and MFA are protected before a session exists.
	PreSessionCookieName = "ky_presession"
)

// CSRF token bindings. A token names what it is bound to, so a pre-session token never passes
// for a session one.
const (
	csrfSession    = "session:"
	csrfPreSession = "presession:"
)

var ErrPasswordChangeRequired = errors.New("password change required")

type SessionManager struct {
	store  store.Store
	config config.SecurityConfig
}

func NewSessionManager(st store.Store, cfg config.SecurityConfig) *SessionManager {
	return &SessionManager{
		store:  st,
		config: cfg,
	}
}

// IssueSession creates an active session for credentials verified just now and writes the
// HttpOnly cookie + CSRF token.
func (sm *SessionManager) IssueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, user *store.User) (*store.Session, string, error) {
	authenticatedAt := time.Now().UTC()
	rawToken := crypto.RandomHex(32)
	tokenHash := crypto.SHA256Hex([]byte(rawToken))

	ttl := sm.config.SessionTTL
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}

	sess := &store.Session{
		TokenHash: tokenHash,
		UserID:    user.ID,
		UserAgent: r.UserAgent(),
		IPAddress: ClientIP(r, sm.config.TrustedProxies),
		CreatedAt: authenticatedAt,
		ExpiresAt: time.Now().UTC().Add(ttl),
	}

	if err := sm.store.Sessions().CreateSession(ctx, sess, user.Credentials()); err != nil {
		return nil, "", err
	}

	// Set HttpOnly session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})

	sm.writeCSRFCookie(w, SessionCSRFToken(sm.config.SessionSecret, rawToken), sess.ExpiresAt)
	sm.clearCookie(w, PreSessionCookieName, true)

	return sess, rawToken, nil
}

// csrfToken is the CSRF token for binding: HMAC-SHA256 under DeriveKey(sessionSecret,
// "kycalendar:csrf"). Only the server, or a page that was handed the token, can know it.
func csrfToken(sessionSecret, binding string) string {
	return crypto.ComputeHMACSHA256([]byte(binding), csrfKey(sessionSecret))
}

func csrfKey(sessionSecret string) string {
	return string(crypto.DeriveKey([]byte(sessionSecret), "kycalendar:csrf"))
}

// SessionCSRFToken is the CSRF token of the session whose raw cookie value is rawSession.
func SessionCSRFToken(sessionSecret, rawSession string) string {
	return csrfToken(sessionSecret, csrfSession+rawSession)
}

// PreSessionCSRFToken is the CSRF token of the pre-session cookie value preSession.
func PreSessionCSRFToken(sessionSecret, preSession string) string {
	return csrfToken(sessionSecret, csrfPreSession+preSession)
}

// ValidateCSRF reports whether the X-CSRF-Token header is the token of the presented session
// cookie, compared in constant time. The ky_csrf cookie is never consulted: a token minted for
// another session, or a cookie/header pair planted by an attacker, does not match.
func (sm *SessionManager) ValidateCSRF(r *http.Request) bool {
	return sm.validCSRF(r, SessionCookieName, csrfSession)
}

// ValidatePreSessionCSRF is ValidateCSRF for sign-in and MFA: the header must be the token of
// the presented pre-session cookie or, for a browser that still holds a session, of that session.
func (sm *SessionManager) ValidatePreSessionCSRF(r *http.Request) bool {
	return sm.validCSRF(r, PreSessionCookieName, csrfPreSession) || sm.validCSRF(r, SessionCookieName, csrfSession)
}

func (sm *SessionManager) validCSRF(r *http.Request, cookieName, binding string) bool {
	cookie, err := r.Cookie(cookieName)
	header := r.Header.Get(HeaderCSRF)
	if err != nil || cookie.Value == "" || header == "" {
		return false
	}
	return crypto.VerifyHMACSHA256([]byte(binding+cookie.Value), csrfKey(sm.config.SessionSecret), header)
}

// RefreshCSRF rewrites the ky_csrf cookie for the presented session cookie, so a browser that
// lost the cookie, or holds one from before tokens were bound, gets the right token back.
func (sm *SessionManager) RefreshCSRF(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		sm.writeCSRFCookie(w, SessionCSRFToken(sm.config.SessionSecret, cookie.Value), sess.ExpiresAt)
	}
}

// IssuePreSession gives a signed-out browser a fresh random pre-session cookie (HttpOnly) and
// the CSRF token bound to it. It holds no server state and authorizes nothing but the sign-in
// steps. A fresh value each time means a value an attacker planted is replaced, not adopted.
func (sm *SessionManager) IssuePreSession(w http.ResponseWriter) {
	value := crypto.RandomHex(32)
	http.SetCookie(w, &http.Cookie{
		Name:     PreSessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})
	sm.writeCSRFCookie(w, PreSessionCSRFToken(sm.config.SessionSecret, value), time.Time{})
}

// writeCSRFCookie sets the ky_csrf cookie the SPA reads into the X-CSRF-Token header. A zero
// expires makes it a browser-session cookie.
func (sm *SessionManager) writeCSRFCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: false, // Read by JS to put into X-CSRF-Token header
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})
}

func (sm *SessionManager) clearCookie(w http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: httpOnly,
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})
}

// AuthenticateRequest extracts the session token from cookie or Authorization header and returns the user.
func (sm *SessionManager) AuthenticateRequest(r *http.Request) (*store.User, *store.Session, error) {
	var rawToken string

	// 1. Check Bearer token header
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		rawToken = strings.TrimPrefix(authHeader, "Bearer ")
	}

	// 2. Fallback to HttpOnly cookie
	if rawToken == "" {
		if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
			rawToken = cookie.Value
		}
	}

	if rawToken == "" {
		return nil, nil, store.ErrNotFound
	}

	tokenHash := crypto.SHA256Hex([]byte(rawToken))
	sess, err := sm.store.Sessions().GetSession(r.Context(), tokenHash)
	if err != nil {
		return nil, nil, err
	}

	user, err := sm.store.Users().GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	allowed, ownershipErr := sm.store.InstallerUserAllowed(r.Context(), user)
	if ownershipErr != nil || !allowed || user.Status != "active" {
		_ = sm.store.Sessions().DeleteSession(r.Context(), tokenHash)
		return nil, nil, store.ErrNotFound
	}

	// The password transaction may revoke the session between the two reads.
	// Recheck after loading the user so an old session cannot inherit a cleared flag.
	if _, err := sm.store.Sessions().GetSession(r.Context(), tokenHash); err != nil {
		return nil, nil, err
	}

	// A restricted session never authorizes product operations or private settings.
	if user.MustChangePassword && !((r.Method == http.MethodGet && r.URL.Path == "/api/auth/me") || (r.Method == http.MethodPost && (r.URL.Path == "/api/auth/change-password" || r.URL.Path == "/api/auth/logout"))) {
		return nil, nil, ErrPasswordChangeRequired
	}
	return user, sess, nil
}

// RevokeSession deletes the active session, clears the session cookie and hands the browser a
// pre-session, so the sign-in form that follows has a CSRF token.
func (sm *SessionManager) RevokeSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	rawToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		rawToken = ""
	}
	if rawToken == "" {
		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			rawToken = cookie.Value
		}
	}

	if rawToken != "" {
		tokenHash := crypto.SHA256Hex([]byte(rawToken))
		_ = sm.store.Sessions().DeleteSession(ctx, tokenHash)
	}

	sm.clearCookie(w, SessionCookieName, true)
	sm.IssuePreSession(w)

	return nil
}

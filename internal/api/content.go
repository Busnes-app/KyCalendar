package api

// Content encryption's request side (docs/superpowers/specs/2026-11-22-content-encryption-design.md,
// CE2 and CE4's setup and unlock): each request carries its unlock secret to the store, which
// opens the account key for that request only (E6).

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// stretchSlots bounds concurrent Argon2id derivations (64 MiB each) from setup and unlock, so
// signed-in users cannot exhaust memory shared with everyone; a full pool answers 503.
var stretchSlots = make(chan struct{}, 4)

func stretching(w http.ResponseWriter, s *Server) (func(), bool) {
	select {
	case stretchSlots <- struct{}{}:
		return func() { <-stretchSlots }, true
	default:
		w.Header().Set("Retry-After", "2")
		s.writeError(w, http.StatusServiceUnavailable, "Busy; try again in a moment")
		return nil, false
	}
}

// contentTransitionLabel derives the key the transition slot is wrapped under (E11).
const contentTransitionLabel = "kycalendar:content-transition"

func (s *Server) contentOn() bool { return s.config.Calendar.ContentEncryption }

func (s *Server) transitionKey() []byte {
	return crypto.DeriveKey(s.config.Security.EncryptionKey, contentTransitionLabel)
}

// unlockApp attaches an app password's or MCP token's secret to ctx. Only these may open the
// account through the transition slot (E11).
func (s *Server) unlockApp(ctx context.Context, userID string, kind contentkey.SlotKind, id, secret string) context.Context {
	if !s.contentOn() {
		return ctx
	}
	return s.store.Content().WithUnlock(ctx, store.Credential{UserID: userID, Kind: kind, SlotID: id, Secret: []byte(secret), Transition: s.transitionKey()})
}

// sessionCredential is the signed-in browser's slot credential: the raw session token, which
// the server stores only hashed (E6).
func sessionCredential(r *http.Request, sess *store.Session) (store.Credential, bool) {
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil || c.Value == "" || sess == nil {
		return store.Credential{}, false
	}
	return store.Credential{UserID: sess.UserID, Kind: contentkey.SlotSession, SlotID: sess.TokenHash, Secret: []byte(c.Value)}, true
}

// unlockSession attaches the session's secret to the request, when content encryption is on.
func (s *Server) unlockSession(r *http.Request) *http.Request {
	if !s.contentOn() {
		return r
	}
	cred, ok := sessionCredential(r, sessionOf(r.Context()))
	if !ok {
		return r
	}
	return r.WithContext(s.store.Content().WithUnlock(r.Context(), cred))
}

// slotForAppPassword gives a new app password or MCP token its own slot, opened through the
// creating session's slot. A session that has not unlocked cannot: the credential then reaches
// encrypted calendars only through the transition, and is refused after it (E10).
func (s *Server) slotForAppPassword(r *http.Request, user *store.User, p *store.AppPassword, token string) {
	if !s.contentOn() {
		return
	}
	cred, ok := sessionCredential(r, sessionOf(r.Context()))
	if !ok {
		return
	}
	account, err := store.UnlockOf(s.store.Content().WithUnlock(r.Context(), cred)).Account(r.Context())
	if err != nil {
		return
	}
	_, secret, _ := apppass.Parse(token)
	kind := contentkey.SlotAppPassword
	if store.IsMCPScope(p.Scope) {
		kind = contentkey.SlotMCP
	}
	if err := s.store.Content().AddSlot(r.Context(), account, store.Credential{UserID: user.ID, Kind: kind, SlotID: p.ID, Secret: []byte(secret)}); err != nil {
		log.Printf("[CONTENT] app password slot not created: %v", err)
	}
}

// handleContentStatus reports whether the account is set up and whether this session unlocked.
func (s *Server) handleContentStatus(w http.ResponseWriter, r *http.Request) {
	if !s.contentOn() {
		s.writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	user, sess := sessionUser(r.Context()), sessionOf(r.Context())
	st, err := s.store.Content().Status(r.Context(), user.ID, sess.TokenHash)
	if err != nil {
		s.writeUnavailable(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "set_up": st.SetUp, "unlocked": st.Unlocked, "transition_until": st.TransitionUntil})
}

// handleContentRecoveryCode is a fresh recovery code for the setup page to show and have typed
// back (E9). Nothing is stored until setup sends it.
func (s *Server) handleContentRecoveryCode(w http.ResponseWriter, r *http.Request) {
	if !s.contentOn() {
		s.writeError(w, http.StatusNotFound, "Not found")
		return
	}
	code, _, err := contentkey.NewRecoveryCode()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not make a recovery code")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]string{"recovery_code": code})
}

type contentSecrets struct {
	Passphrase   string `json:"passphrase"`
	RecoveryCode string `json:"recovery_code"`
}

func decodeContentSecrets(r *http.Request) (contentSecrets, bool) {
	var b contentSecrets
	err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<10)).Decode(&b)
	return b, err == nil
}

// handleContentSetup is the first unlock (E12): it creates the account key, the passphrase,
// recovery, transition and session slots, and keys the calendars every holder can open.
func (s *Server) handleContentSetup(w http.ResponseWriter, r *http.Request) {
	if !s.contentOn() {
		s.writeError(w, http.StatusNotFound, "Not found")
		return
	}
	// Setup makes credentials that outlive the session, so a stale or stolen one must not.
	if !s.requireStepUp(w, r, "set up encryption") {
		return
	}
	b, ok := decodeContentSecrets(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if len([]rune(b.Passphrase)) < contentkey.MinPassphrase {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "The passphrase needs at least 12 characters", "code": "weak_passphrase"})
		return
	}
	recovery, err := contentkey.ParseRecoveryCode(b.RecoveryCode)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That is not a recovery code", "code": "bad_recovery_code"})
		return
	}
	user := sessionUser(r.Context())
	cred, ok := sessionCredential(r, sessionOf(r.Context()))
	if !ok {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	// Refuse a repeat before any key stretching; the store's insert stays the real check.
	if st, err := s.store.Content().Status(r.Context(), user.ID, ""); err != nil {
		s.writeUnavailable(w, err)
		return
	} else if st.SetUp {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Encryption is already set up", "code": "already_set_up"})
		return
	}
	done, ok := stretching(w, s)
	if !ok {
		return
	}
	defer done()
	err = s.store.Content().Setup(r.Context(), user.ID, store.Setup{Passphrase: b.Passphrase, Recovery: recovery, Session: cred, Transition: s.transitionKey(), Now: time.Now()})
	switch {
	case errors.Is(err, store.ErrAlreadyExists):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Encryption is already set up", "code": "already_set_up"})
		return
	case err != nil:
		log.Printf("[CONTENT] setup failed for %s: %v", user.ID, err)
		s.writeError(w, http.StatusInternalServerError, "Could not set up encryption")
		return
	}
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "content.setup", Resource: "user:" + user.ID, IPAddress: s.requestIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

// handleContentUnlock opens the account with the passphrase or the recovery code and gives this
// session its own slot (Web flows, Unlock). A wrong secret counts like a failed sign-in.
func (s *Server) handleContentUnlock(w http.ResponseWriter, r *http.Request) {
	if !s.contentOn() {
		s.writeError(w, http.StatusNotFound, "Not found")
		return
	}
	b, ok := decodeContentSecrets(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	user := sessionUser(r.Context())
	session, ok := sessionCredential(r, sessionOf(r.Context()))
	if !ok {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	now := time.Now()
	// Counted before the secret is checked, so concurrent guesses cannot pass the limit together.
	release, reserved, wait, err := s.davReserve(r.Context(), "content-unlock:"+user.ID, davFailsPerUser, now)
	if err != nil {
		s.writeUnavailable(w, err)
		return
	}
	if !reserved {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		s.writeError(w, http.StatusTooManyRequests, "Too many failed attempts")
		return
	}
	done, ok := stretching(w, s)
	if !ok {
		release()
		return
	}
	defer done()
	with := store.Credential{UserID: user.ID, Kind: contentkey.SlotPassphrase, SlotID: "passphrase", Secret: []byte(b.Passphrase)}
	if b.RecoveryCode != "" {
		secret, err := contentkey.ParseRecoveryCode(b.RecoveryCode)
		if err != nil {
			s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That is not a recovery code", "code": "bad_recovery_code"})
			return
		}
		with = store.Credential{UserID: user.ID, Kind: contentkey.SlotRecovery, SlotID: "recovery", Secret: secret}
	}
	err = s.store.Content().Unlock(r.Context(), with, session)
	if !errors.Is(err, store.ErrLocked) {
		release() // only a wrong secret stays counted
	}
	switch {
	case errors.Is(err, store.ErrLocked):
		_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "content.unlock_failed", Resource: "user:" + user.ID, IPAddress: s.requestIP(r)})
		s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "That did not unlock your calendars", "code": "wrong_secret"})
		return
	case errors.Is(err, store.ErrNoAccountKey):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Set up encryption first", "code": "not_set_up"})
		return
	case err != nil:
		s.writeUnavailable(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleContentEndTransition ends the 14-day transition now (E11): older app passwords without
// their own slot stop working, and a copy of the deployment key opens nothing written after.
func (s *Server) handleContentEndTransition(w http.ResponseWriter, r *http.Request) {
	if !s.contentOn() {
		s.writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if !s.requireStepUp(w, r, "end the encryption transition") {
		return
	}
	user := sessionUser(r.Context())
	if err := s.store.Content().EndTransition(r.Context(), user.ID, s.transitionKey()); err != nil {
		if errors.Is(err, store.ErrNoAccountKey) {
			s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Set up encryption first", "code": "not_set_up"})
			return
		}
		s.writeUnavailable(w, err)
		return
	}
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "content.transition_ended", Resource: "user:" + user.ID, IPAddress: s.requestIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

// RotateContentTransitions is the elected job that ends every transition past its date (E11).
func (s *Server) RotateContentTransitions(ctx context.Context) {
	if !s.contentOn() {
		return
	}
	if n, err := s.store.Content().RotateDue(ctx, time.Now(), s.transitionKey()); err != nil {
		log.Printf("[CONTENT] transition rotation stopped after %d accounts: %v", n, err)
	}
}

// SweepContent is the elected job that seals legacy rows of keyed calendars (E12, CE6), a
// batch per transaction, for at most a minute per run; the next run starts over and finds
// only what is left.
func (s *Server) SweepContent(ctx context.Context) {
	if !s.contentOn() {
		return
	}
	deadline := time.Now().Add(time.Minute)
	var at store.SweepCursor
	total := 0
	for time.Now().Before(deadline) && ctx.Err() == nil {
		n, next, done, err := s.store.Content().SweepOnce(ctx, at, store.SweepBatch)
		if err != nil {
			log.Printf("[CONTENT] sweep stopped after %d rows: %v", total, err)
			return
		}
		total, at = total+n, next
		if done {
			break
		}
	}
	if total > 0 {
		log.Printf("[CONTENT] sweep sealed %d legacy rows", total)
	}
}

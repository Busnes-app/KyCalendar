package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"

	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// issuerURL is u's KyIdentity issuer URL: sso_issuer is the binding "kyidentity <issuer URL>".
func issuerURL(u *store.User) string {
	return strings.TrimPrefix(u.SSOIssuer, sso.KindKyIdentity+" ")
}

// elementOrigin is the https origin of Element (suite app kymessage-chat) in the user's app
// list, "" when absent: KyMessages' chat links must point there and nowhere else.
func elementOrigin(apps []suitedir.App) string {
	for _, a := range apps {
		if u, err := url.Parse(a.URL); a.ID == "kymessage-chat" && err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
			return "https://" + u.Host
		}
	}
	return ""
}

// handleSuiteChat is POST /api/suite/chat {"email"} (suite contract H): KyMessages' link for
// chatting with that organisation person. Nothing is created; the browser opens the link.
func (s *Server) handleSuiteChat(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r.Context())
	refuse := func(status int, code, msg string) {
		s.writeJSON(w, status, map[string]string{"error": msg, "code": code})
	}
	var req struct {
		Email string `json:"email"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req) != nil || req.Email == "" || validEmail(req.Email) != nil {
		refuse(http.StatusBadRequest, "bad_body", "Name one email address")
		return
	}
	if !s.allowAccountAttempt("chat:"+u.ID, 20, time.Minute) {
		refuse(http.StatusTooManyRequests, "rate_limited", "Too many chats opened; wait a minute")
		return
	}
	element := elementOrigin(s.suiteAppsFor(r.Context(), u))
	if !s.meetingsFor(u) || element == "" {
		refuse(http.StatusNotFound, "chat_off", "Chat needs KyMessages and a KyIdentity sign-in")
		return
	}
	people, err := s.resolveAs(r.Context(), u, []string{req.Email})
	if err != nil {
		log.Printf("suite: chat lookup for %s: %v", u.ID, err) // never the address
		refuse(http.StatusServiceUnavailable, "unavailable", "Try again in a moment")
		return
	}
	target := ""
	for _, p := range people {
		if strings.EqualFold(p.Email, req.Email) && p.Sub != u.SSOSubject {
			target = p.Sub
		}
	}
	if target == "" {
		s.auditChat(r, u, "no_target")
		refuse(http.StatusNotFound, "no_target", "This person isn't on KyMessages yet.")
		return
	}
	link, err := s.kymessages.Chat(r.Context(), "evt_"+crypto.RandomHex(16), kymessages.ChatRequest{
		User: kymessages.User{Sub: u.SSOSubject, Issuer: issuerURL(u)}, TargetSub: target,
	})
	if err == nil && !strings.HasPrefix(link, element+"/") && !strings.HasPrefix(link, element+"#") {
		err = kymessages.Refused{Code: "bad_reply"} // not on Element's own host
	}
	var refused kymessages.Refused
	switch {
	case errors.As(err, &refused) && (refused.Code == "no_target" || refused.Code == "no_user"):
		s.auditChat(r, u, refused.Code)
		msg := "This person isn't on KyMessages yet."
		if refused.Code == "no_user" {
			msg = "You have no KyMessages account yet."
		}
		refuse(http.StatusNotFound, refused.Code, msg)
	case errors.As(err, &refused):
		s.auditChat(r, u, refused.Code)
		refuse(http.StatusBadGateway, "refused", "KyMessages refused to open the chat")
	case err != nil:
		refuse(http.StatusServiceUnavailable, "unavailable", "Try again in a moment")
	default:
		s.auditChat(r, u, "")
		w.Header().Set("Cache-Control", "no-store")
		s.writeJSON(w, http.StatusOK, map[string]string{"url": link})
	}
}

// auditChat records an outcome with IDs only: never the address, the subject or the link.
func (s *Server) auditChat(r *http.Request, u *store.User, code string) {
	rec := &store.AuditRecord{UserID: u.ID, Action: "chat.open"}
	if code != "" {
		rec.Action, rec.Details = "chat.refused", "code="+code
	}
	_ = s.store.Audit().LogAudit(r.Context(), rec)
}

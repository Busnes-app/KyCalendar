package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/feed"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/sso"
)

// newKyMessages builds the KyMessages link, linked at once when the environment names one.
// The KyIdentity lookup follows the KyPost link's rules (kypost_link.go) with its own state.
func (s *Server) newKyMessages() {
	cal := s.config.Calendar
	policy := feed.RefusePrivate
	if cal.KyMessagesAllowPrivate {
		policy = sso.RefuseLocal
	}
	s.kymessagesKick = make(chan struct{}, 1)
	s.kymessages = &kymessages.Link{HTTP: sso.NewGuardedClient(policy, nil), Rotated: func() { kick(s.kymessagesKick) }}
	s.kymessagesState.reason = kypostPending
	if cal.KyMessagesURL != "" {
		s.kymessages.Set(&kymessages.Peer{URL: cal.KyMessagesURL, Secret: cal.KyMessagesSecret, Source: kymessages.SourceEnvironment})
		s.kymessagesState.reason = ""
	}
}

// StartKyMessages links KyMessages through KyIdentity when the environment names none, and
// keeps the link fresh until ctx ends. Nothing waits for it: meetings queue until it is up.
func (s *Server) StartKyMessages(ctx context.Context) {
	if s.config.Calendar.KyMessagesURL != "" {
		return
	}
	go func() {
		first, cancel := context.WithTimeout(ctx, kypostFirstWait)
		ok := s.resolveKyMessages(first)
		cancel()
		select {
		case <-s.kymessagesKick: // that provider was just looked up
		default:
		}
		s.peerLoop(ctx, s.kymessagesKick, s.resolveKyMessages, ok, kypostRefresh, kypostRetry, kypostMinGap)
	}()
}

// resolveKyMessages is resolveKyPost for the kymessage client ID. The secret is never logged.
func (s *Server) resolveKyMessages(ctx context.Context) bool {
	p := s.signin.Load()
	if !s.config.SSO.Enabled || p == nil || p.Kind != sso.KindKyIdentity {
		return s.unlinkKyMessages(kypostNoKyIdentity, nil)
	}
	dir, err := p.SuiteDirectory()
	if err != nil {
		return s.unlinkKyMessages(kypostNoCredentials, err)
	}
	from := dir.Issuer + " " + dir.ClientID
	peer, err := dir.Peer(ctx, "kymessage")
	switch {
	case errors.Is(err, suitedir.ErrNoPeer):
		return s.unlinkKyMessages(kypostNoPeer, nil)
	case errors.Is(err, suitedir.ErrUnauthorized):
		return s.unlinkKyMessages(kypostUnauthorized, err)
	case err != nil:
		if s.kymessages.Peer() != nil {
			if _, _, linked := s.kymessagesState.get(); linked == from {
				log.Printf("[KYMESSAGES] KyIdentity lookup failed, keeping the current link: %v", err)
				return false
			}
		}
		return s.unlinkKyMessages(kypostUnreachable, err)
	}
	u, err := config.KyPostURL(peer.URL)
	if err != nil {
		return s.unlinkKyMessages(kypostInvalidPeer, err)
	}
	if cur := s.kymessages.Peer(); cur == nil || cur.URL != u || string(cur.Secret) != string(peer.Secret) {
		log.Printf("[KYMESSAGES] linked through KyIdentity: %s", u)
	}
	s.kymessages.Set(&kymessages.Peer{URL: u, Secret: peer.Secret, Source: kymessages.SourceKyIdentity})
	s.kymessagesState.set("", from)
	return true
}

func (s *Server) unlinkKyMessages(reason string, err error) bool {
	if prev, _, _ := s.kymessagesState.get(); prev != reason {
		if err != nil {
			log.Printf("[KYMESSAGES] not linked through KyIdentity (%s): %v", reason, err)
		} else {
			log.Printf("[KYMESSAGES] not linked through KyIdentity (%s)", reason)
		}
	}
	s.kymessages.Set(nil)
	s.kymessagesState.set(reason, "")
	return false
}

// kymessagesView is the admin screen's KyMessages status. It never carries the secret.
type kymessagesView struct {
	Connected bool       `json:"connected"`
	Source    string     `json:"source,omitempty"`
	URL       string     `json:"url,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

func (s *Server) handleKyMessagesStatus(w http.ResponseWriter, r *http.Request) {
	var v kymessagesView
	reason, checked, _ := s.kymessagesState.get()
	if !checked.IsZero() {
		v.CheckedAt = &checked
	}
	if p := s.kymessages.Peer(); p != nil {
		v.Connected, v.Source, v.URL = true, p.Source, p.URL
	} else {
		v.Reason = reason
	}
	s.writeJSON(w, http.StatusOK, v)
}

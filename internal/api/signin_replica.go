package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/sso"
)

// signinPoll is how often a PostgreSQL replica looks for a sign-in change saved on another.
const signinPoll = 5 * time.Second

// signinDigest digests every sign-in row (signin_*), in key order: a save on any replica
// changes it.
func signinDigest(saved map[string]string) string {
	var keys []string
	for k := range saved {
		if strings.HasPrefix(k, "signin_") {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k + "\x00" + saved[k] + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// FollowSignIn reloads the live sign-in provider whenever the stored sign-in rows change, so
// a save on one PostgreSQL replica reaches every other within signinPoll. A no-op on SQLite,
// where the saving process is the only one. It returns when ctx ends.
func (s *Server) FollowSignIn(ctx context.Context) {
	if s.shared == nil {
		return
	}
	ticker := time.NewTicker(signinPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.refreshSignIn(ctx)
	}
}

func (s *Server) refreshSignIn(ctx context.Context) {
	saved, err := s.store.Settings().GetAllSettings(ctx)
	if err != nil {
		return // unreadable: keep the provider; callbacks recheck the database themselves
	}
	if cur := s.signinDigest.Load(); cur != nil && *cur == signinDigest(saved) {
		return
	}
	// The write lock keeps callbacks out while the provider is replaced.
	s.signinMu.Lock()
	defer s.signinMu.Unlock()
	if err := s.LoadSignIn(ctx); err != nil {
		log.Printf("[SSO] sign-in closed: settings changed on another server and could not be applied: %v", err)
	}
}

// signinCurrent reports whether p is still the provider the database holds: the stored
// binding is p's, and the sign-in rows are the ones p was built from. SQLite: always.
func (s *Server) signinCurrent(ctx context.Context, p *sso.Provider) (bool, error) {
	if s.shared == nil {
		return true, nil
	}
	saved, err := s.store.Settings().GetAllSettings(ctx)
	if err != nil {
		return false, err
	}
	if saved[sso.KeyBound] != p.Binding {
		return false, nil
	}
	cur := s.signinDigest.Load()
	return cur == nil || *cur == "" || *cur == signinDigest(saved), nil
}

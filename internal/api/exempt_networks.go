package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/netallow"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const exemptNetworksSetting = "rate_limit_exempt_networks"

// combineExemptNetworks parses the environment's networks and the admin's as one list.
func (s *Server) combineExemptNetworks(stored string) (*netallow.List, error) {
	return netallow.Parse(s.config.Security.RateLimitExemptNetworks + "," + stored)
}

// storedExemptNetworks is the admin's half of the allowlist; unset reads as empty.
func (s *Server) storedExemptNetworks(ctx context.Context) (string, error) {
	stored, err := s.store.Settings().GetSetting(ctx, exemptNetworksSetting)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return stored, err
}

// loadExemptNetworks builds the allowlist at startup. A stored value that cannot be read or no
// longer parses is dropped with a log line, leaving the environment's: fewer exemptions, never more.
func (s *Server) loadExemptNetworks() {
	s.exemptMu.Lock()
	defer s.exemptMu.Unlock()
	s.reloadExempt(context.Background())
}

// reloadExempt reads the admin's half again; caller holds exemptMu. The list is replaced only
// when the stored value changed, so the exempted count keeps counting.
func (s *Server) reloadExempt(ctx context.Context) {
	s.exemptLoaded = time.Now()
	stored, err := s.storedExemptNetworks(ctx)
	if err == nil && stored == s.exemptStored && s.exempt.Load() != nil {
		return
	}
	var list *netallow.List
	if err == nil {
		list, err = s.combineExemptNetworks(stored)
	}
	if err != nil {
		log.Printf("[SECURITY] stored rate-limit exempt networks ignored: %v", err)
		list, _ = s.combineExemptNetworks("")
		stored = "\x00unreadable" // read again next time
	}
	s.exemptStored = stored
	s.exempt.Store(list)
}

// exemptMaxAge is how stale a PostgreSQL replica's copy of the office list may be, so a
// network removed on one replica stops being exempt on every other within it.
const exemptMaxAge = 5 * time.Second

// exemptList is the office allowlist a request is judged by. SQLite: the one this process
// saved. PostgreSQL: the database's, read again once the copy is exemptMaxAge old.
func (s *Server) exemptList(ctx context.Context) *netallow.List {
	if s.shared == nil {
		return s.exempt.Load()
	}
	s.exemptMu.Lock()
	defer s.exemptMu.Unlock()
	if time.Since(s.exemptLoaded) >= exemptMaxAge {
		c, cancel := context.WithTimeout(ctx, windowTimeout)
		s.reloadExempt(c)
		cancel()
	}
	return s.exempt.Load()
}

func (s *Server) exemptNetworksStatus(stored string) map[string]any {
	return map[string]any{
		"networks":    stored,
		"environment": s.config.Security.RateLimitExemptNetworks,
		"exempted":    s.exempt.Load().Exempted(),
	}
}

func (s *Server) handleGetExemptNetworks(w http.ResponseWriter, r *http.Request) {
	stored, err := s.storedExemptNetworks(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load settings")
		return
	}
	s.writeJSON(w, http.StatusOK, s.exemptNetworksStatus(stored))
}

// handleSetExemptNetworks replaces the admin's half of the allowlist; the environment's half is
// not editable here. Step-up, like the other sign-in settings. The exempted count restarts.
func (s *Server) handleSetExemptNetworks(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "change office networks") {
		return
	}
	var req struct {
		Networks string `json:"networks"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON request body")
		return
	}
	own, err := netallow.Parse(req.Networks)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "netallow: "))
		return
	}
	stored := own.String()
	combined, err := s.combineExemptNetworks(stored)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "netallow: "))
		return
	}
	ctx := context.WithoutCancel(r.Context()) // the saved list and its audit row land together
	previous, _ := s.storedExemptNetworks(ctx)
	if err := s.store.Settings().SetSetting(ctx, exemptNetworksSetting, stored); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the networks")
		return
	}
	s.exemptMu.Lock()
	s.exempt.Store(combined)
	s.exemptStored, s.exemptLoaded = stored, time.Now()
	s.exemptMu.Unlock()
	if err := s.store.Audit().LogAudit(ctx, &store.AuditRecord{
		UserID:    sessionUser(r.Context()).ID,
		Action:    "admin.rate_limit_networks",
		Resource:  "settings",
		Details:   fmt.Sprintf("from=%q to=%q", previous, stored),
		IPAddress: s.requestIP(r),
	}); err != nil {
		log.Printf("[SECURITY] audit admin.rate_limit_networks not recorded: %v", err)
	}
	s.writeJSON(w, http.StatusOK, s.exemptNetworksStatus(stored))
}

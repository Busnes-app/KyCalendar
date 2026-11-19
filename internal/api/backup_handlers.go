package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/appversion"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// refuseWhileDraining answers 503 to new backup work once the server is stopping.
func (s *Server) refuseWhileDraining(w http.ResponseWriter) bool {
	if s.draining.Load() {
		w.Header().Set("Retry-After", "5")
		s.writeError(w, http.StatusServiceUnavailable, "This server is stopping; try again on another")
		return true
	}
	return false
}

// errRecoveryKeyMismatch answers a swapped recovery.pub: the pin in the database and the key
// on disk disagree, so refuse rather than seal a capsule nobody's custodians can open.
const errRecoveryKeyMismatch = "Recovery key file does not match the pinned key ID; refusing to seal"

// privateRecoveryHint names the opt-in, so a refused LAN destination is not a dead end. Both
// the pairing and the run refusals end with it.
const privateRecoveryHint = " (set KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY=true for a KyRecovery on your own network)"

// depositWriteBudget is how long the admin's connection may stay open for the receipt: the
// upload budget plus room for sealing. The listener's WriteTimeout is sized for JSON replies.
const depositWriteBudget = 16 * time.Minute

// pairWriteBudget covers a KyRecovery claim (its client allows 30 s) and the pin: the code is
// spent once the claim succeeds, so the admin must get the answer.
const pairWriteBudget = time.Minute

// AuditDetails flattens the lib's details map into the bounded audit column. Locally
// derived fields go first so a long remote error cannot displace the useful facts.
func AuditDetails(m map[string]any) string {
	seen := map[string]bool{}
	keys := []string{"outcome", "capsule_id", "local_path", "local_error", "deposited", "digest", "size_bytes"}
	var extra, remote []string
	for k := range m {
		switch k {
		case "outcome", "capsule_id", "local_path", "local_error", "deposited", "digest", "size_bytes":
		case "error", "receipt_unrecorded":
			remote = append(remote, k)
		default:
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	sort.Strings(remote)
	keys = append(keys, extra...)
	keys = append(keys, remote...)
	var b strings.Builder
	for _, k := range keys {
		v, ok := m[k]
		if !ok || seen[k] {
			continue
		}
		seen[k] = true
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s=%s", k, auditValue(v))
	}
	return recoveryclient.AuditSafe(b.String())
}

func auditValue(v any) string {
	return strings.ReplaceAll(fmt.Sprintf("%q", fmt.Sprint(v)), "=", `\x3d`)
}

// auditBackup records a backup event against the acting admin. Details never carry the
// token or capsule bytes, and both text fields are bounded and printable before they are
// stored: a resource may be an operator-typed URL and details may quote a remote body.
func (s *Server) auditBackup(ctx context.Context, userID string, r *http.Request, action, resource, details string) {
	resource, details = recoveryclient.AuditSafe(resource), recoveryclient.AuditSafe(details)
	if err := s.store.Audit().LogAudit(ctx, &store.AuditRecord{
		UserID:    userID,
		Action:    action,
		Resource:  resource,
		Details:   details,
		IPAddress: s.requestIP(r),
	}); err != nil {
		log.Printf("[BACKUP] audit %s for %s not recorded: %v", action, resource, err)
	}
}

// actorID is the session user behind an admin request, resolved while the request is live.
func (s *Server) actorID(r *http.Request) string {
	user, _, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		return ""
	}
	return user.ID
}

// handleBackupDrill seals the live payload to a throwaway key, opens it in a sandbox and
// runs the verification recipe. It reports, not proves, whether the suite key is pinned.
func (s *Server) handleBackupDrill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if s.refuseWhileDraining(w) {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(depositWriteBudget))
	ctx := r.Context()
	payload, err := backup.Collect(ctx, s.config, s.store, appversion.String())
	if errors.Is(err, backup.ErrNoDatabaseSnapshot) {
		// An honest failed drill, not a 500: the operator needs to read why no backup exists.
		s.writeJSON(w, http.StatusOK, &recoveryclient.DrillResult{Passed: false, ErrorMessage: err.Error(),
			Checks: []recoveryclient.Check{{Name: "Database", Passed: false, Message: err.Error()}}})
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to collect backup files")
		return
	}
	result, err := backup.RunDrill(ctx, s.config, payload)
	if errors.Is(err, backup.ErrDrillBusy) {
		s.writeError(w, http.StatusConflict, "A restore drill is already running")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to execute restore drill")
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

// handleExportCapsule hands the operator the sealed capsule itself. Only the custodians'
// shares open it, so the download is safe to store anywhere; kyrecovery is where it belongs.
// It is a POST so the CSRF check applies: a cross-site GET must not be able to pull it.
func (s *Server) handleExportCapsule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if s.refuseWhileDraining(w) {
		return
	}
	if !s.requireStepUp(w, r, "export a capsule") {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(depositWriteBudget))
	actor := s.actorID(r)
	won, err := backup.WithRunLock(r.Context(), s.store, func(ctx context.Context) error {
		s.exportCapsule(w, r, ctx, actor)
		return nil
	})
	if err != nil && !won {
		s.writeError(w, http.StatusServiceUnavailable, "The backup lock is unavailable; try again")
	} else if !won {
		s.writeBusy(w)
	}
}

// keyProblem reports a recovery key state the operator must repair, as opposed to a read
// failure.
func keyProblem(err error) bool {
	return errors.Is(err, recoveryclient.ErrKeyMismatch) || errors.Is(err, backup.ErrRecoveryKeyTampered) || errors.Is(err, backup.ErrRecoveryKeyUnavailable)
}

func keyProblemText(err error) string {
	switch {
	case errors.Is(err, recoveryclient.ErrKeyMismatch):
		return "recovery.pub on this server names another key than the pin; remove it to have it rewritten"
	case errors.Is(err, backup.ErrRecoveryKeyTampered):
		return "the stored recovery key record does not open under this instance's key or does not match the pin; backups are refused"
	}
	return recoveryclient.AuditSafe(err.Error())
}

// writeBusy answers a backup refused because another run holds the backup-run lock.
func (s *Server) writeBusy(w http.ResponseWriter) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Another backup is running; try again when it has finished", "code": "busy"})
}

// writeKeyProblem answers a recovery key this server cannot seal to, and reports whether it did.
func (s *Server) writeKeyProblem(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, recoveryclient.ErrKeyMismatch):
		s.writeError(w, http.StatusConflict, errRecoveryKeyMismatch)
	case errors.Is(err, backup.ErrRecoveryKeyTampered):
		s.writeError(w, http.StatusConflict, "The stored recovery key record does not open or does not match the pin; refusing to seal")
	case errors.Is(err, backup.ErrRecoveryKeyUnavailable):
		s.writeError(w, http.StatusConflict, "The pinned recovery key is not available on this server; pin it by hand on a server that has it")
	default:
		return false
	}
	return true
}

// exportCapsule is handleExportCapsule under the backup-run lock; ctx ends with the lock.
func (s *Server) exportCapsule(w http.ResponseWriter, r *http.Request, ctx context.Context, actor string) {
	if err := backup.SyncRecoveryKey(ctx, s.config, s.store); err != nil {
		if !s.writeKeyProblem(w, err) {
			s.writeError(w, http.StatusInternalServerError, "Failed to load recovery key")
		}
		return
	}
	settings := backup.Settings(ctx, s.store.Settings())
	key, err := recoveryclient.LoadRecoveryKey(s.config.Database.DataDir, settings)
	if errors.Is(err, recoveryclient.ErrNotPaired) {
		s.writeError(w, http.StatusPreconditionFailed, "No recovery key; pair or pin one")
		return
	}
	if s.writeKeyProblem(w, err) {
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load recovery key")
		return
	}
	payload, err := backup.Collect(ctx, s.config, s.store, appversion.String())
	if errors.Is(err, backup.ErrNoDatabaseSnapshot) {
		s.writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to collect backup files")
		return
	}
	raw, m, err := recoveryclient.Seal(payload, key)
	if err != nil {
		// Logged because writeError only reaches the browser: without this an export that has
		// outgrown the capsule limits is a bare 500 with nothing anywhere naming the cause.
		log.Printf("[BACKUP] export capsule: seal failed: %s", recoveryclient.AuditSafe(err.Error()))
		if errors.Is(err, capsule.ErrCapsuleTooLarge) {
			s.writeError(w, http.StatusRequestEntityTooLarge, recoveryclient.TooLargeMessage)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to seal capsule")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.kycap"`, recoveryclient.FilenameSafe(m.CapsuleID)))
	w.Header().Set("X-Recovery-Key-ID", m.RecoveryKeyID)
	// A capsule leaving the server is a copy of everything it holds; the trail says who took one.
	s.auditBackup(ctx, actor, r, "admin.backup_export", m.CapsuleID, AuditDetails(map[string]any{"size_bytes": len(raw)}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

type RemotePairRequest struct {
	RecoveryURL string `json:"recovery_url"`
	PairingCode string `json:"pairing_code"`
}

// handlePairRemoteRecovery claims a 6-digit PIN with KyRecovery, pins the suite recovery
// public key it hands back, and stores the URL and the sealed bearer token.
// The route wraps it in `tracked`, so it is counted as detached from the moment ServeHTTP
// dispatches, before requireAdmin authenticates.
func (s *Server) handlePairRemoteRecovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !s.requireStepUp(w, r, "pair with KyRecovery") {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(pairWriteBudget))
	var req RemotePairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.RecoveryURL == "" || req.PairingCode == "" {
		s.writeError(w, http.StatusBadRequest, "Both recovery_url and pairing_code are required")
		return
	}
	// Validated here as well as in the client so a bad URL is rejected before it is stored
	// or audited as a pairing attempt.
	if err := recoveryclient.ValidateURL(req.RecoveryURL, s.config.Backup.AllowPrivateRecovery); err != nil {
		msg := err.Error()
		if errors.Is(err, recoveryclient.ErrPrivateDestination) {
			msg += privateRecoveryHint
		}
		s.writeError(w, http.StatusBadRequest, msg)
		return
	}

	// Pairing is write-once: the pin, the stored pairing and the audit row must all land even
	// if the admin's connection drops mid-claim. The actor is resolved while it is still live.
	actor := s.actorID(r)
	ctx := context.WithoutCancel(r.Context())
	target := recoveryclient.AuditSafe(req.RecoveryURL)

	// The service name sent here is what kyrecovery pins for the token and what every
	// capsule's manifest is checked against, so it is the same ServiceName the collector seals under.
	// Only shutdown cancels the claim; nothing has been written this side while it is in flight.
	result, err := s.recovery.ClaimPairing(s.stopping, req.RecoveryURL, req.PairingCode, backup.ServiceName, s.config.Server.AppName)
	if err != nil {
		s.auditBackup(ctx, actor, r, "backup.pair_failed", target, "error="+err.Error())
		s.writeError(w, http.StatusBadRequest, "Recovery pairing failed")
		return
	}

	sealer, err := backup.NewSealer(s.config)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to prepare the pairing seal")
		return
	}
	// The pin and the pairing are written as one holder of the recovery-pin lock, so no other
	// server pins, pairs or unpairs in between.
	var pinned bool
	err = backup.WithPinLock(ctx, s.store, func(owned context.Context) error {
		if err := backup.PinLocked(owned, s.config, s.store, result.Key); err != nil {
			return err
		}
		pinned = true
		return recoveryclient.StorePairing(backup.Settings(owned, s.store.Settings()), sealer, req.RecoveryURL, result.APIToken)
	})
	switch {
	case err != nil && !pinned && errors.Is(err, backup.ErrTopologyPinned):
		s.auditBackup(ctx, actor, r, "backup.pair_failed", target, "error=the pinned recovery key has another custodian split")
		s.writeError(w, http.StatusConflict, "Already pinned to this recovery key with a different custodian split")
		return
	case err != nil && !pinned && errors.Is(err, fs.ErrExist):
		s.auditBackup(ctx, actor, r, "backup.pair_failed", target, "error=already paired to a different recovery key")
		s.writeError(w, http.StatusConflict, "Already paired to a different recovery key")
		return
	case err != nil && !pinned:
		s.auditBackup(ctx, actor, r, "backup.pair_failed", target, "error=key not pinned: "+err.Error())
		s.writeError(w, http.StatusInternalServerError, "Failed to save recovery key")
		return
	case err != nil:
		// The key is pinned whatever happens next, so it is recorded: a write-once pin that
		// exists nowhere in the audit trail is the gap this closes.
		s.auditBackup(ctx, actor, r, "backup.pair_failed", target,
			"error=key pinned but the pairing was not stored: "+err.Error())
		s.writeError(w, http.StatusInternalServerError, "Failed to save recovery pairing")
		return
	}
	s.auditBackup(ctx, actor, r, "backup.paired", target,
		fmt.Sprintf("recovery_key_id=%s allow_private=%v", result.Key.Public.ID(), s.config.Backup.AllowPrivateRecovery))

	s.writeJSON(w, http.StatusOK, map[string]any{
		"recovery_key_id": result.Key.Public.ID(),
		"threshold":       result.Key.Threshold,
		"total_shares":    result.Key.TotalShares,
	})
}

// handleRunBackup seals a capsule and deposits it now, outside the schedule: one capsule to
// the local directory and, when paired, to KyRecovery. The receipt it returns is what a
// restore is later checked against.
//
// The run outlives the request: once bytes are on their way, a closed tab must not leave
// KyRecovery holding a capsule this instance has no receipt for. Only shutdown interrupts it
// (StopDetached), and then the answer is 503 and the scheduler runs the backup again.
// The route wraps it in `tracked`, so it is counted as detached from the moment ServeHTTP
// dispatches, before requireAdmin authenticates.
func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if s.refuseWhileDraining(w) {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(depositWriteBudget))
	// The acting admin is resolved while the request is still live; the audit row is written
	// on the same detached context as the run, so a dropped connection cannot decide whether
	// the row exists.
	actor := s.actorID(r)
	ctx := context.WithoutCancel(r.Context())

	rc, err := backup.RunConfig(s.config, appversion.String())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to prepare backup configuration")
		return
	}
	var res recoveryclient.Result
	won, lockErr := backup.WithRunLock(s.stopping, s.store, func(owned context.Context) error {
		res, err = backup.Run(owned, s.config, rc, s.store, s.recovery)
		return nil
	})
	if !won {
		if lockErr != nil {
			s.writeError(w, http.StatusServiceUnavailable, "The backup lock is unavailable; try again")
		} else {
			s.writeBusy(w)
		}
		return
	}

	action, outcome, details := recoveryclient.Outcome(res, err)
	details["outcome"] = outcome
	s.auditBackup(ctx, actor, r, action, res.Manifest.CapsuleID, AuditDetails(details))
	if !errors.Is(err, recoveryclient.ErrInProgress) {
		RecordLastRun(ctx, s.store.Settings(), outcome, err)
	}

	if errors.Is(err, recoveryclient.ErrReceiptUnrecorded) {
		// The store has the capsule; only this side's record is missing. This is the one path
		// where the two sides disagree about what exists, so the cause goes on record here too.
		log.Printf("[BACKUP] run %s: receipt not recorded: %s", res.Manifest.CapsuleID, recoveryclient.AuditSafe(err.Error()))
		s.writeJSON(w, http.StatusOK, struct {
			recoveryclient.Result
			ReceiptUnrecorded bool `json:"receipt_unrecorded"`
		}{res, true})
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrInterrupted):
			s.writeError(w, http.StatusServiceUnavailable, "The backup was interrupted (this server is stopping, or lost the backup lock); it runs again")
		case errors.Is(err, recoveryclient.ErrKeyPinMissing):
			s.writeError(w, http.StatusPreconditionFailed, "Paired, but recovery.pub is missing or does not match the pin")
		case errors.Is(err, recoveryclient.ErrNotPaired):
			s.writeError(w, http.StatusPreconditionFailed, "No recovery key")
		case errors.Is(err, backup.ErrNoDatabaseSnapshot):
			// A DSN a restore would not open, or a snapshot that did not verify: the cause is
			// the operator's to read, not a server fault.
			s.writeError(w, http.StatusPreconditionFailed, err.Error())
		case errors.Is(err, recoveryclient.ErrNoDestination):
			s.writeError(w, http.StatusPreconditionFailed, "Nowhere to put a capsule: pair with KyRecovery or set KYCALENDAR_BACKUP_DIR")
		case errors.Is(err, recoveryclient.ErrInProgress):
			s.writeError(w, http.StatusConflict, "A backup is already in progress")
		case s.writeKeyProblem(w, err):
		case errors.Is(err, recoveryclient.ErrPrivateDestination):
			// The pairing was stored before the opt-in was turned off, or the host now resolves
			// private. Nothing left; the operator needs the switch named, not a 500.
			s.writeError(w, http.StatusPreconditionFailed, "The recovery host resolves to a private address"+privateRecoveryHint)
		case errors.Is(err, capsule.ErrCapsuleTooLarge):
			s.writeError(w, http.StatusRequestEntityTooLarge, recoveryclient.TooLargeMessage)
		case errors.Is(err, recoveryclient.ErrRemote):
			// The cause is audited; the browser gets only that the store did not take it.
			log.Printf("[BACKUP] run failed: %s", recoveryclient.AuditSafe(err.Error()))
			msg := "KyRecovery did not accept the deposit"
			if res.LocalPath != "" {
				msg += "; the local copy was written"
			}
			if res.LocalError != "" {
				msg += "; " + res.LocalError
			}
			s.writeError(w, http.StatusBadGateway, msg)
		default:
			// Anything raised before a byte left: a stored URL the client refuses, a store
			// read, a collector error. The cause is audited; the message does not guess.
			log.Printf("[BACKUP] run failed (local): %s", recoveryclient.AuditSafe(err.Error()))
			s.writeError(w, http.StatusInternalServerError, "Backup failed before reaching KyRecovery")
		}
		return
	}
	s.writeJSON(w, http.StatusOK, res)
}

// handleUnpair forgets the KyRecovery pairing. Deposits stop; the key pin and any local
// backup directory are untouched. The token on KyRecovery's side is revoked there, by its
// admin.
func (s *Server) handleUnpair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !s.requireStepUp(w, r, "unpair") {
		return
	}
	ctx := r.Context()
	actor := s.actorID(r)
	var target string
	err := backup.WithPinLock(ctx, s.store, func(owned context.Context) error {
		settings := backup.Settings(owned, s.store.Settings())
		url, err := settings.Get("kyrecovery_url")
		if err != nil && !errors.Is(err, recoveryclient.ErrNotFound) {
			return err
		}
		target = recoveryclient.AuditSafe(url)
		return recoveryclient.ClearPairing(settings)
	})
	if err != nil {
		if errors.Is(err, recoveryclient.ErrNotPaired) {
			s.writeError(w, http.StatusPreconditionFailed, "Not paired with KyRecovery")
			return
		}
		s.auditBackup(ctx, actor, r, "admin.backup_unpair", target, "error="+err.Error())
		s.writeError(w, http.StatusInternalServerError, "Failed to remove the pairing")
		return
	}
	s.auditBackup(ctx, actor, r, "admin.backup_unpair", target, "success")
	s.writeJSON(w, http.StatusOK, map[string]any{"paired": false})
}

type PinKeyRequest struct {
	PublicKey   string `json:"public_key"`
	Threshold   int    `json:"threshold"`
	TotalShares int    `json:"total_shares"`
}

// handlePinKey pins the suite recovery public key by hand, for an instance with no
// KyRecovery to pair with. The key is the one the ceremony page shows; the topology is the
// k-of-n it was split with. Write-once, like pairing.
// The route wraps it in `tracked`, so it is counted as detached from the moment ServeHTTP
// dispatches, before requireAdmin authenticates.
func (s *Server) handlePinKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !s.requireStepUp(w, r, "pin a recovery key") {
		return
	}
	var req PinKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON request body")
		return
	}
	key, err := recoveryclient.ParsePinRequest(req.PublicKey, req.Threshold, req.TotalShares)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Write-once like pairing, so the pin and its audit row outlive the request too.
	actor := s.actorID(r)
	ctx := context.WithoutCancel(r.Context())
	if err := backup.PinRecoveryKey(ctx, s.config, s.store, key); err != nil {
		if errors.Is(err, backup.ErrTopologyPinned) {
			s.auditBackup(ctx, actor, r, "admin.backup_key_pin", key.Public.ID(), "error=pinned with another custodian split")
			s.writeError(w, http.StatusConflict, "Already pinned to this recovery key with a different custodian split")
			return
		}
		if errors.Is(err, fs.ErrExist) {
			s.auditBackup(ctx, actor, r, "admin.backup_key_pin", key.Public.ID(), "error=already pinned to a different recovery key")
			s.writeError(w, http.StatusConflict, "Already pinned to a different recovery key")
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to save recovery key")
		return
	}
	s.auditBackup(ctx, actor, r, "admin.backup_key_pin", key.Public.ID(),
		fmt.Sprintf("threshold=%d total_shares=%d", key.Threshold, key.TotalShares))
	s.writeJSON(w, http.StatusOK, map[string]any{
		"recovery_key_id": key.Public.ID(), "threshold": key.Threshold, "total_shares": key.TotalShares,
	})
}

type ScheduleRequest struct {
	IntervalSec int64 `json:"interval_sec"`
}

// handleSetSchedule stores how often the scheduler backs up. Zero turns it off, so the
// admin-only, CSRF-protected and audited boundary matters.
func (s *Server) handleSetSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req ScheduleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON request body")
		return
	}
	ctx := r.Context()
	actor := s.actorID(r)
	settings := backup.Settings(ctx, s.store.Settings())
	if err := recoveryclient.SetInterval(settings, req.IntervalSec); err != nil {
		if errors.Is(err, recoveryclient.ErrBadInterval) {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to save the schedule")
		return
	}
	// Read back what the store holds, so the audit row and the reply never describe a
	// schedule the scheduler will not run.
	stored, err := recoveryclient.Interval(s.config.Backup.DepositInterval, settings)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to read the schedule back")
		return
	}
	sec := int64(stored / time.Second)
	s.auditBackup(ctx, actor, r, "admin.backup_schedule", "", fmt.Sprintf("interval_sec=%d", sec))
	s.writeJSON(w, http.StatusOK, map[string]any{"interval_sec": sec})
}

// handleBackupStatus reports pairing and the last receipt. It never decrypts or echoes the
// credential.
// lastRunSetting holds the latest run's outcome from any actor. The lib keeps only the attempt
// time and the last success, so without it a failing schedule never reaches the screen.
const lastRunSetting = "backup_last_run"

// RecordLastRun stores one run's outcome for the status route.
func RecordLastRun(ctx context.Context, settings store.SettingsStore, outcome string, err error) {
	run := struct {
		At      time.Time `json:"at"`
		Outcome string    `json:"outcome"`
		Error   string    `json:"error,omitempty"`
	}{At: time.Now().UTC(), Outcome: outcome}
	if err != nil {
		run.Error = recoveryclient.AuditSafe(err.Error())
	}
	raw, _ := json.Marshal(run)
	if err := settings.SetSetting(ctx, lastRunSetting, string(raw)); err != nil {
		log.Printf("[BACKUP] last run not recorded: %s", recoveryclient.AuditSafe(err.Error()))
	}
}

func (s *Server) handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	ctx := r.Context()
	out := map[string]any{
		"app_name":               s.config.Server.AppName,
		"app_version":            appversion.String(),
		"allow_private_recovery": s.config.Backup.AllowPrivateRecovery,
		"database_driver":        s.config.Database.Driver,
		"members":                backup.Members(s.config),
	}
	// A read failure answers 503: the screen must never show "not paired" or "no key" for a
	// pairing or pin it could not read. One adapter reads the pairing and pin rows once.
	settings := backup.Settings(ctx, s.store.Settings())
	paired, err := backup.Paired(settings)
	if err != nil {
		s.writeUnavailable(w, err)
		return
	}
	out["paired"], out["key_pinned"] = false, false
	if u, err := settings.Get("kyrecovery_url"); err == nil {
		out["recovery_url"] = u
	}
	syncErr := backup.SyncRecoveryKey(ctx, s.config, s.store)
	key, err := recoveryclient.LoadRecoveryKey(s.config.Database.DataDir, backup.Settings(ctx, s.store.Settings()))
	switch {
	case keyProblem(syncErr):
		out["recovery_key_error"] = keyProblemText(syncErr)
	case syncErr != nil:
		s.writeUnavailable(w, syncErr)
		return
	case err == nil:
		out["key_pinned"] = true
		out["paired"] = paired
		out["recovery_key_id"] = key.Public.ID()
		out["threshold"] = key.Threshold
		out["total_shares"] = key.TotalShares
	case errors.Is(err, recoveryclient.ErrKeyMismatch):
		out["recovery_key_error"] = "recovery.pub does not match the pinned key ID"
	case errors.Is(err, recoveryclient.ErrNotPaired):
		if paired {
			out["recovery_key_error"] = "paired, but recovery.pub is missing; restore it or re-pair"
		}
	default:
		s.writeUnavailable(w, err)
		return
	}
	last, ok, err := recoveryclient.LastDeposit(settings)
	if err != nil {
		s.writeUnavailable(w, err)
		return
	}
	if ok {
		out["last_deposit"] = last
	}
	if raw, err := s.store.Settings().GetSetting(ctx, lastRunSetting); err == nil && json.Valid([]byte(raw)) {
		out["last_run"] = json.RawMessage(raw)
	}
	if s.config.Backup.Dir != "" {
		out["local_dir"] = s.config.Backup.Dir
		out["local_keep"] = s.config.Backup.Keep
		if copies, err := recoveryclient.ListLocalCopies(s.config.Backup.Dir, backup.ServiceName); err == nil {
			out["local_copies"] = copies
		} else {
			out["local_error"] = recoveryclient.AuditSafe(err.Error())
		}
	}
	if interval, err := recoveryclient.Interval(s.config.Backup.DepositInterval, settings); err == nil {
		out["interval_sec"] = int64(interval / time.Second)
		out["min_interval_sec"] = int64(recoveryclient.MinInterval / time.Second)
		if next, ok, err := recoveryclient.NextRun(s.config.Backup.DepositInterval, settings); err == nil && ok {
			out["next_run_at"] = next.Format(time.RFC3339)
		}
	}
	s.writeJSON(w, http.StatusOK, out)
}

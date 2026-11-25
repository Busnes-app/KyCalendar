package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// lastAttemptKey is the row recoveryclient.Run stamps before it works and NextRun counts from.
// The lib does not export it; TestInterruptedRunIsDueAgain fails if the two drift apart.
const lastAttemptKey = "backup_last_attempt"

// ErrInterrupted marks a run cut short: the server is stopping, or this process lost the
// backup-run lock.
var ErrInterrupted = errors.New("backup: interrupted by shutdown or a lost backup lock")

// ErrBusy is a run refused because another holds the backup-run lock.
var ErrBusy = errors.New("backup: another backup is running")

// runLock serializes every run that seals and delivers a capsule: scheduled, Back up now on
// any server, CLI deposit and export-capsule. PostgreSQL: a session lock whose loss cancels
// the run; SQLite: a flock beside the database.
const runLock = "backup-run"

// WithRunLock runs work as the one holder of the backup-run lock and reports whether it won.
// work's context ends when the server stops or the lock is lost.
func WithRunLock(ctx context.Context, st store.Store, work func(context.Context) error) (bool, error) {
	return st.Coordination().WithLock(ctx, runLock, work)
}

// Run is recoveryclient.Run for a process that may be told to stop mid-run; the caller holds
// the backup-run lock. Cancelling ctx aborts the snapshot and the upload. The settings
// writes ignore the cancellation, so a receipt KyRecovery has already returned is still
// recorded. An interrupted run gives its attempt stamp back, so the scheduler runs it again
// on the next start, not an interval later. On PostgreSQL the recovery key is synchronized
// from the database first, and a key this server cannot prove refuses the run.
//
// The lib's Seal and local-copy write are not cancellable mid-call: a run whose lock is lost
// while sealing finishes that step before it sees the cancellation. Nothing is uploaded after.
func Run(ctx context.Context, cfg *config.Config, rc recoveryclient.RunConfig, st store.Store, client recoveryclient.Depositor) (recoveryclient.Result, error) {
	if err := SyncRecoveryKey(ctx, cfg, st); err != nil {
		return recoveryclient.Result{}, err
	}
	// Multi-tenant mode keeps no backup copy on a disk (tenancy spec D15): the lib writes one
	// into scratch, which goes to the bucket and is removed with the scratch when Run returns.
	ten, err := st.Tenancy(ctx)
	if err != nil {
		return recoveryclient.Result{}, err
	}
	if ten.Multi {
		if !cfg.ObjectStorage.Configured() {
			return recoveryclient.Result{}, errNoObjectStorage
		}
		if ten.Cell == "" {
			return recoveryclient.Result{}, errNoStorageCell
		}
		dir, release, err := NewScratch(cfg.Database.DataDir)
		if err != nil {
			return recoveryclient.Result{}, err
		}
		defer release()
		rc.BackupDir, rc.Keep = dir, 1
	}
	settings := Settings(context.WithoutCancel(ctx), st.Settings())
	prev, prevErr := settings.Get(lastAttemptKey)
	res, err := recoveryclient.Run(ctx, rc, settings, func() (recoveryclient.Payload, error) {
		return Collect(ctx, cfg, st, rc.AppVersion)
	}, client)
	if ten.Multi && res.LocalPath != "" {
		// As for a local copy: a failed copy fails the run only when nothing was deposited.
		key, copyErr := objectCopy(ctx, cfg.ObjectStorage, ObjectPrefix(ten.Cell), res.LocalPath, cfg.Backup.Keep, time.Now())
		res.LocalPath = key
		if copyErr != nil {
			copyErr = fmt.Errorf("object copy: %w", copyErr)
			res.LocalError = recoveryclient.AuditSafe(copyErr.Error())
			if key == "" && res.Receipt == nil {
				err = errors.Join(err, copyErr)
			}
		}
	}
	// ErrInProgress stamped nothing: the stamp belongs to the run that holds the lock.
	if err == nil || ctx.Err() == nil || res.Receipt != nil || errors.Is(err, recoveryclient.ErrInProgress) {
		return res, err
	}
	switch {
	case errors.Is(prevErr, recoveryclient.ErrNotFound):
		_ = settings.Delete(lastAttemptKey)
	case prevErr == nil:
		_ = settings.Set(lastAttemptKey, prev)
	}
	return res, fmt.Errorf("%w: %w", ErrInterrupted, err)
}

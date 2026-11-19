package backup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// RecoveryKeyRecordSetting is the PostgreSQL anchor of the pinned recovery key: the public key
// and its k-of-n, sealed under a key derived from the encryption key with the pinned key ID as
// associated data. Every replica writes its own recovery.pub from it. A database writer
// without the encryption key, which lives outside the database, cannot substitute a key.
const RecoveryKeyRecordSetting = "kyrecovery_public_key"

const recordLabel = "kycalendar:setting:recovery_public_key"

// recoveryPinLock serializes pin, pairing persistence and unpair across processes.
const recoveryPinLock = "recovery-pin"

var (
	// ErrRecoveryKeyUnavailable is a pin this server cannot load and must not guess at.
	ErrRecoveryKeyUnavailable = errors.New("backup: the pinned recovery key is not available on this server")
	// ErrRecoveryKeyTampered is a record that does not open under this instance's key, or
	// does not match the pin: refuse to seal anything to it.
	ErrRecoveryKeyTampered = errors.New("backup: the stored recovery key record does not open or does not match the pin; refusing to seal")
	// ErrPinBusy is another server holding the pin lock for longer than a pin takes.
	ErrPinBusy = errors.New("backup: another server is changing the recovery key; try again")
)

// pinWait bounds how long a pin waits for another holder of the lock (a variable for tests).
var pinWait = 10 * time.Second

type keyRecord struct {
	PublicKey   string `json:"public_key"`
	Threshold   int    `json:"threshold"`
	TotalShares int    `json:"total_shares"`
}

func recordKey(cfg *config.Config) []byte {
	return crypto.DeriveKey(cfg.Security.EncryptionKey, recordLabel)
}

func sealRecord(cfg *config.Config, k recoveryclient.RecoveryKey) (string, error) {
	b, err := json.Marshal(keyRecord{base64.StdEncoding.EncodeToString(k.Public.Bytes()), k.Threshold, k.TotalShares})
	if err != nil {
		return "", err
	}
	return crypto.SealAESGCM(b, recordKey(cfg), []byte(k.Public.ID()))
}

// openRecord opens the record sealed for key ID id and checks it names that key.
func openRecord(cfg *config.Config, sealed, id string) (recoveryclient.RecoveryKey, error) {
	plain, err := crypto.OpenAESGCM(sealed, recordKey(cfg), []byte(id))
	if err != nil {
		return recoveryclient.RecoveryKey{}, ErrRecoveryKeyTampered
	}
	var rec keyRecord
	if err := json.Unmarshal(plain, &rec); err != nil {
		return recoveryclient.RecoveryKey{}, ErrRecoveryKeyTampered
	}
	k, err := recoveryclient.ParsePinRequest(rec.PublicKey, rec.Threshold, rec.TotalShares)
	if err != nil || k.Public.ID() != id {
		return recoveryclient.RecoveryKey{}, ErrRecoveryKeyTampered
	}
	return k, nil
}

type pin struct {
	id               string
	threshold, total int
}

// pinned reads the lib's pin rows; a missing one reads as zero.
func pinned(settings recoveryclient.Settings) (pin, error) {
	var p pin
	var err error
	if p.id, err = optional(settings, pinKeyID); err != nil {
		return p, err
	}
	for key, n := range map[string]*int{pinThreshold: &p.threshold, pinTotalShares: &p.total} {
		v, err := optional(settings, key)
		if err != nil {
			return p, err
		}
		if v != "" {
			if *n, err = strconv.Atoi(v); err != nil {
				return p, fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	return p, nil
}

func optional(settings recoveryclient.Settings, key string) (string, error) {
	v, err := settings.Get(key)
	if errors.Is(err, recoveryclient.ErrNotFound) {
		return "", nil
	}
	return v, err
}

// WithPinLock runs work as the one holder of the recovery-pin lock, waiting up to pinWait:
// PostgreSQL session lock or SQLite flock, so CLI commands are serialized too.
func WithPinLock(ctx context.Context, st store.Store, work func(context.Context) error) error {
	deadline := time.Now().Add(pinWait)
	for {
		won, err := st.Coordination().WithLock(ctx, recoveryPinLock, work)
		if err != nil || won {
			return err
		}
		if time.Now().After(deadline) {
			return ErrPinBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// PinRecoveryKey is the lib's write-once StoreRecoveryKey made global: under the pin lock,
// pin rows written once by the database, the pin read back before success, and on PostgreSQL
// the sealed record written beside it. A recovery.pub this call created for a key that did
// not stick is removed again. The same key and split again is an idempotent success.
func PinRecoveryKey(ctx context.Context, cfg *config.Config, st store.Store, k recoveryclient.RecoveryKey) error {
	return WithPinLock(ctx, st, func(owned context.Context) error {
		return PinLocked(owned, cfg, st, k)
	})
}

// PinLocked is PinRecoveryKey's body; the caller holds the pin lock.
func PinLocked(ctx context.Context, cfg *config.Config, st store.Store, k recoveryclient.RecoveryKey) error {
	dir := cfg.Database.DataDir
	path := recoveryclient.RecoveryKeyPath(dir)
	_, statErr := os.Lstat(path)
	settings := Settings(ctx, st.Settings())
	err := ctx.Err()
	if err == nil {
		err = refuseOtherTopology(settings, k)
	}
	if err == nil {
		err = recoveryclient.StoreRecoveryKey(dir, settings, k)
	}
	if err == nil {
		err = checkPinned(Settings(ctx, st.Settings()), k)
	}
	if err != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			_ = os.Remove(path) // a file for a key that did not stick
		}
		return err
	}
	if st.Driver() == "postgres" {
		// The pin stands, so its file stays: a failed record write is healed by the next sync,
		// which records the pin from a matching recovery.pub.
		sealed, err := sealRecord(cfg, k)
		if err == nil {
			err = st.Settings().SetSetting(ctx, RecoveryKeyRecordSetting, sealed)
		}
		return err
	}
	return nil
}

// refuseOtherTopology refuses the pinned key with another split before the lib writes
// anything: the lib would otherwise re-set the topology of the same key.
func refuseOtherTopology(settings recoveryclient.Settings, k recoveryclient.RecoveryKey) error {
	p, err := pinned(settings)
	if err != nil || p.id != k.Public.ID() || p.threshold == 0 && p.total == 0 {
		return err
	}
	if p.threshold != k.Threshold || p.total != k.TotalShares {
		return ErrTopologyPinned
	}
	return nil
}

// checkPinned reads the pin back: key and split must be k's.
func checkPinned(settings recoveryclient.Settings, k recoveryclient.RecoveryKey) error {
	p, err := pinned(settings)
	if err != nil {
		return err
	}
	if p.id != k.Public.ID() {
		return &recoveryclient.PinnedError{PinnedKeyID: p.id, OfferedKeyID: k.Public.ID()}
	}
	if p.threshold != k.Threshold || p.total != k.TotalShares {
		return ErrTopologyPinned
	}
	return nil
}

// SyncRecoveryKey makes this PostgreSQL server's recovery.pub agree with the pin and its
// sealed record: a missing file is written from the record, and a pin with no record yet
// (a database migrated from SQLite) is recorded from a recovery.pub that matches it. A file
// naming another key is recoveryclient.ErrKeyMismatch; a tampered record is
// ErrRecoveryKeyTampered; a pin nobody can load is ErrRecoveryKeyUnavailable. Both refuse
// every run. SQLite: nothing to do. Cmd/server runs it at startup, Run before every run.
func SyncRecoveryKey(ctx context.Context, cfg *config.Config, st store.Store) error {
	if st.Driver() != "postgres" {
		return nil
	}
	settings := Settings(ctx, st.Settings())
	p, err := pinned(settings)
	if err != nil {
		return err
	}
	sealed, err := optional(settings, RecoveryKeyRecordSetting)
	if err != nil {
		return err
	}
	dir := cfg.Database.DataDir
	path := recoveryclient.RecoveryKeyPath(dir)
	switch {
	case p.id == "" && sealed != "":
		return ErrRecoveryKeyTampered // a record with no pin was not written here
	case p.id == "":
		return nil
	case sealed == "":
		// Pinned before records existed: record it from the file that matches the pin.
		k, err := recoveryclient.LoadRecoveryKey(dir, settings)
		if err != nil {
			return fmt.Errorf("%w: the database pins recovery key %s, but %s does not hold it (%v); start once the server that pinned it, or copy its recovery.pub here", ErrRecoveryKeyUnavailable, p.id, path, err)
		}
		return WithPinLock(ctx, st, func(owned context.Context) error {
			sealed, err := sealRecord(cfg, k)
			if err == nil {
				err = st.Settings().SetSetting(owned, RecoveryKeyRecordSetting, sealed)
			}
			return err
		})
	}
	k, err := openRecord(cfg, sealed, p.id)
	if err != nil {
		return err
	}
	if k.Threshold != p.threshold || k.TotalShares != p.total {
		return ErrRecoveryKeyTampered
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		// The lib's idempotent re-pin of the pinned key writes the missing file.
		return recoveryclient.StoreRecoveryKey(dir, settings, k)
	}
	_, err = recoveryclient.LoadRecoveryKey(dir, settings)
	return err
}

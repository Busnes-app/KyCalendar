package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const recoveryTokenLabel = "ky_server_base:setting:kyrecovery_token"

// The lib's pairing and pin rows. It does not export the names; TestPinRowNames fails if
// they drift.
const (
	pinKeyID       = "kyrecovery_key_id"
	pinThreshold   = "kyrecovery_threshold"
	pinTotalShares = "kyrecovery_total_shares"
	pairingURL     = "kyrecovery_url"
	pairingToken   = "kyrecovery_token_enc"
)

// coherentKeys are read together, in one statement, the first time an operation reads any of
// them: a pairing changed on another server between two reads can never pair one URL with
// another pairing's token, nor a key with another key's topology.
var coherentKeys = []string{pairingURL, pairingToken, pinKeyID, pinThreshold, pinTotalShares, RecoveryKeyRecordSetting}

// ErrTopologyPinned is a pin of the pinned key with a different k-of-n. It is fs.ErrExist,
// like the lib's PinnedError, so callers answer 409 for both.
var ErrTopologyPinned = fmt.Errorf("%w: the recovery key is already pinned with another custodian split", fs.ErrExist)

type settingsAdapter struct {
	ctx      context.Context
	s        store.SettingsStore
	coherent map[string]string // nil until read; dropped on any write to coherentKeys
}

// Settings binds one operation's context to the settings store for calls into the lib. Build
// one per operation: its coherent rows are read once.
func Settings(ctx context.Context, s store.SettingsStore) recoveryclient.Settings {
	return &settingsAdapter{ctx: ctx, s: s}
}

func (a *settingsAdapter) Get(key string) (string, error) {
	if slices.Contains(coherentKeys, key) {
		if a.coherent == nil {
			m, err := a.s.GetSettings(a.ctx, coherentKeys...)
			if err != nil {
				return "", err
			}
			a.coherent = m
		}
		v, ok := a.coherent[key]
		if !ok {
			return "", recoveryclient.ErrNotFound
		}
		return v, nil
	}
	v, err := a.s.GetSetting(a.ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return "", recoveryclient.ErrNotFound
	}
	return v, err
}

func (a *settingsAdapter) Set(key, val string) error {
	if slices.Contains(coherentKeys, key) {
		a.coherent = nil
	}
	switch key {
	case pinKeyID, pinThreshold, pinTotalShares:
		// The database keeps the first key and split whatever any process believes: a pin
		// racing another server, or outliving its lock, cannot overwrite either.
		pinned, err := a.s.SetSettingIfAbsent(a.ctx, key, val)
		if err != nil || pinned == val {
			return err
		}
		if key == pinKeyID {
			return &recoveryclient.PinnedError{PinnedKeyID: pinned, OfferedKeyID: val}
		}
		return ErrTopologyPinned
	}
	return a.s.SetSetting(a.ctx, key, val)
}

func (a *settingsAdapter) Delete(key string) error {
	if slices.Contains(coherentKeys, key) {
		a.coherent = nil
	}
	return a.s.DeleteSetting(a.ctx, key)
}

// Paired reports whether a KyRecovery pairing is stored. Unlike the lib's HasPairing, a read
// failure is an error, never "unpaired".
func Paired(settings recoveryclient.Settings) (bool, error) {
	for _, key := range []string{pairingURL, pairingToken} {
		if _, err := settings.Get(key); errors.Is(err, recoveryclient.ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
	}
	return true, nil
}

// NewSealer seals the KyRecovery token under the deployment key, domain-separated so a row
// copied from another setting will not open.
func NewSealer(cfg *config.Config) (recoveryclient.Sealer, error) {
	return recoveryclient.NewAESGCMSealer(cfg.Security.EncryptionKey, recoveryTokenLabel)
}

// RunConfig is what Run needs from the scaffold's configuration.
func RunConfig(cfg *config.Config, appVersion string) (recoveryclient.RunConfig, error) {
	sealer, err := NewSealer(cfg)
	if err != nil {
		return recoveryclient.RunConfig{}, err
	}
	return recoveryclient.RunConfig{
		DataDir: cfg.Database.DataDir, AppName: ServiceName, AppVersion: appVersion,
		BackupDir: cfg.Backup.Dir, Keep: cfg.Backup.Keep, Sealer: sealer,
	}, nil
}

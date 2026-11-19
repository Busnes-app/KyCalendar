package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// errReplicaConfig is a replica whose shared settings differ from the recorded ones.
var errReplicaConfig = errors.New("replica configuration differs from the other replicas")

// keyDiffers is never accepted: the sealed settings every replica shares open only under the
// recorded key, so a different one is a typo or the wrong file, not a rotation.
const keyDiffers = "KY_ENCRYPTION_KEY differs from the key the other replicas use. Supply the same key; it cannot be rotated with accept-replica-config"

// checkReplicaConfig records the shared-setting fingerprint if none is recorded and refuses a
// replica that differs, naming only the settings. Recording is insert-if-absent: of two
// first starts the loser reads the winner's record back and compares, as any later start
// does. A setting this build adds is recorded on its first start. The caller holds the
// bootstrap lock.
func checkReplicaConfig(ctx context.Context, cfg *config.Config, settings store.SettingsStore) error {
	current := config.ReplicaFingerprint(cfg)
	mine, err := json.Marshal(current)
	if err != nil {
		return err
	}
	raw, err := settings.SetSettingIfAbsent(ctx, config.ReplicaConfigSetting, string(mine))
	if err != nil || raw == string(mine) {
		return err
	}
	var stored map[string]string
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return fmt.Errorf("%w: the stored record is unreadable; run accept-replica-config", errReplicaConfig)
	}
	if diff := config.DiffFingerprint(stored, current); slices.Equal(diff, []string{config.EncryptionKeyField}) {
		return fmt.Errorf("%w: %s", errReplicaConfig, keyDiffers)
	} else if len(diff) > 0 {
		return fmt.Errorf("%w: %s. Use the same values on every replica; after a deliberate change on all of them, stop them all and run accept-replica-config once", errReplicaConfig, strings.Join(diff, ", "))
	}
	if config.MergeFingerprint(stored, current) {
		b, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		return settings.SetSetting(ctx, config.ReplicaConfigSetting, string(b))
	}
	return nil
}

// acceptReplicaConfig rewrites the record from this process's configuration after a
// deliberate change on every replica, with an audit row naming the changed settings (names
// only) in the same transaction. It prints names, never values or fingerprints.
func acceptReplicaConfig(ctx context.Context, cfg *config.Config, st store.Store, out io.Writer) error {
	if st.Driver() != "postgres" {
		return errors.New("refusing: accept-replica-config applies to PostgreSQL replicas; SQLite records no replica configuration")
	}
	return st.Coordination().Serialized(ctx, "bootstrap", func(ctx context.Context) error {
		current := config.ReplicaFingerprint(cfg)
		changed := slices.Sorted(maps.Keys(current))
		raw, err := st.Settings().GetSetting(ctx, config.ReplicaConfigSetting)
		switch {
		case err == nil:
			var stored map[string]string
			if json.Unmarshal([]byte(raw), &stored) == nil {
				if changed = config.DiffFingerprint(stored, current); slices.Equal(changed, []string{config.EncryptionKeyField}) {
					return errors.New("refusing: " + keyDiffers)
				}
			}
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
		b, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if err := st.Settings().SetSettingAudited(ctx, config.ReplicaConfigSetting, string(b), &store.AuditRecord{
			UserID: "system", Action: "admin.replica_config_accepted", Resource: config.ReplicaConfigSetting,
			Details: "fields=" + strings.Join(changed, ","),
		}); err != nil {
			return err
		}
		if len(changed) == 0 {
			fmt.Fprintln(out, "Replica configuration unchanged; record rewritten.")
		} else {
			fmt.Fprintf(out, "Replica configuration accepted. Changed: %s\n", strings.Join(changed, ", "))
		}
		return nil
	})
}

const acceptUsage = `Usage: kycalendar accept-replica-config

Records this replica's shared settings as the ones every PostgreSQL replica must use, after a
deliberate change made on all of them. Stop every replica first, run this once with the new
settings, then start them. It prints the names of the changed settings, never their values.
A different KY_ENCRYPTION_KEY is refused: the key cannot be rotated this way.
`

func runAcceptReplicaConfig(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("accept-replica-config", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(fs.Output(), acceptUsage) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errors.New("unexpected arguments")
	}
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if cfg.Database.Driver != "postgres" {
		return errors.New("refusing: accept-replica-config applies to PostgreSQL replicas; SQLite records no replica configuration")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer st.Close()
	return acceptReplicaConfig(ctx, cfg, st, out)
}

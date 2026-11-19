package backup

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	_ "modernc.org/sqlite"
)

// encryptionKeyPath is where a restore drops the key that opens the sealed secrets (sign-in,
// KyRecovery token), relative to the restore target: the <DataDir>/encryption.key LoadFromEnv reads.
const encryptionKeyPath = "data/encryption.key"

// recoveryPubPath is where a restore drops the suite recovery public key, matching the
// <DataDir>/recovery.pub that recoveryclient.RecoveryKeyPath reads.
const recoveryPubPath = "data/recovery.pub"

// ErrNoDatabaseSnapshot is returned when the payload cannot carry a consistent copy of the
// database, so a capsule without one is never sealed as if it were a backup.
var ErrNoDatabaseSnapshot = errors.New("backup: no consistent database snapshot for this driver")

// ServiceName is what KyRecovery pins for this product's token and every capsule's manifest.
// KY_APP_NAME is the display name only.
const ServiceName = "kycalendar"

// DatabaseMember is the capsule path of the SQLite database: the file the default DSN opens
// under <DataDir>, so a restored tree starts on the restored data.
const DatabaseMember = "data/kycalendar.db"

// Collect assembles the payload every sealing caller uses: the local application files
// (SQLite database, configuration) plus the members that may only ever travel inside a
// sealed capsule (the encryption key, the pinned recovery public key). Nothing that returns
// from here may leave the process except through a Sealer.
func Collect(ctx context.Context, cfg *config.Config, st store.Store, appVersion string) (recoveryclient.Payload, error) {
	var dbBytes []byte
	var counts calendarCounts
	var err error
	switch strings.ToLower(cfg.Database.Driver) {
	case "sqlite":
		// The capsule restores the database as <DataDir>/kycalendar.db; a DSN elsewhere would
		// seal a file a restored server never opens.
		dsnFile, _, _ := strings.Cut(cfg.Database.DSN, "?")
		want := filepath.Join(cfg.Database.DataDir, filepath.Base(DatabaseMember))
		if !samePath(dsnFile, want) {
			return recoveryclient.Payload{}, fmt.Errorf("%w: KY_DB_DSN opens %s, backups require the default %s; unset KY_DB_DSN or point it there", ErrNoDatabaseSnapshot, dsnFile, want)
		}
		dbBytes, counts, err = snapshot(ctx, cfg.Database.DataDir, func(path string) error {
			db, err := sql.Open("sqlite", cfg.Database.DSN)
			if err != nil {
				return err
			}
			defer db.Close()
			return recoveryclient.SQLiteSnapshot(ctx, db, path)
		})
	case "postgres":
		// The same member, parts and drill recipe as SQLite: a restore is the SQLite restore,
		// then migrate-postgres into an empty database.
		dbBytes, counts, err = snapshot(ctx, cfg.Database.DataDir, func(path string) error {
			return st.SnapshotToSQLite(ctx, path)
		})
	default:
		return recoveryclient.Payload{}, fmt.Errorf("%w: %s", ErrNoDatabaseSnapshot, cfg.Database.Driver)
	}
	if err != nil {
		return recoveryclient.Payload{}, err
	}
	// Cancelled once the snapshot was whole: stop here so nothing is sealed for a run nobody waits for.
	if err := ctx.Err(); err != nil {
		return recoveryclient.Payload{}, err
	}
	files, parts := databaseFiles(dbBytes)
	sqlitePaths := []string{DatabaseMember}

	cfgJSON, _ := json.MarshalIndent(map[string]any{
		"server":   cfg.Server,
		"database": map[string]any{"driver": cfg.Database.Driver},
	}, "", "  ")
	files = append(files, recoveryclient.File{Path: "config/settings.json", Data: cfgJSON, Mode: 0600})

	if len(cfg.Security.EncryptionKey) != 32 {
		return recoveryclient.Payload{}, fmt.Errorf("backup: encryption key is %d bytes, want 32; refusing to seal a capsule that cannot decrypt what it restores", len(cfg.Security.EncryptionKey))
	}
	files = append(files, recoveryclient.File{
		Path: encryptionKeyPath,
		Data: []byte(hex.EncodeToString(cfg.Security.EncryptionKey) + "\n"),
		Mode: 0600,
	})

	if pub, err := os.ReadFile(recoveryclient.RecoveryKeyPath(cfg.Database.DataDir)); err == nil {
		files = append(files, recoveryclient.File{Path: recoveryPubPath, Data: pub, Mode: 0600})
	}

	if managed := cfg.InstallerSSO; managed != nil {
		paths := map[string]string{}
		for name, path := range map[string]string{"installer/client.secret": managed.ClientSecretFile, "installer/directory.secret": managed.DirectorySecretFile} {
			secret, err := applysetup.Secret(path)
			if err != nil {
				return recoveryclient.Payload{}, errors.New("backup: required installer credential unavailable")
			}
			files = append(files, recoveryclient.File{Path: name, Data: []byte(secret), Mode: 0600})
			paths[name] = path
		}
		raw, _ := json.Marshal(paths)
		files = append(files, recoveryclient.File{Path: "installer/credential-paths.json", Data: raw, Mode: 0600})
	}
	warnNearLimit(files)
	required := requiredFiles(files)
	if parts != nil {
		required = append(required, DatabaseMember) // joined from the parts before any check
	}
	payload := recoveryclient.Payload{
		ServiceName: ServiceName,
		AppVersion:  appVersion,
		Files:       files,
		Dependencies: map[string]any{
			"ports": []int{cfg.Server.Port},
			"env":   []string{"KY_PORT", "KY_DB_DRIVER"},
		},
		VerificationRecipe: map[string]any{
			"check_sqlite_integrity": true,
			"sqlite_paths":           sqlitePaths,
			"calendar_count":         counts.Calendars,
			"object_count":           counts.Objects,
			"required_files":         required,
			"expected_env":           []string{"KY_PORT", "KY_DB_DRIVER"},
			"expected_ports":         []int{cfg.Server.Port},
		},
	}
	if parts != nil {
		payload.VerificationRecipe["database_parts"] = parts
	}
	return payload, nil
}

// snapshot writes a consistent single-file SQLite copy of the live database with write (SQLite:
// VACUUM INTO through a live connection, since the store runs in WAL mode and a plain read
// misses the -wal; PostgreSQL: SnapshotToSQLite), in locked 0700 scratch removed before it
// returns, and counts calendars and objects in that copy.
func snapshot(ctx context.Context, dataDir string, write func(path string) error) ([]byte, calendarCounts, error) {
	var counts calendarCounts
	dir, release, err := NewScratch(dataDir)
	if err != nil {
		return nil, counts, err
	}
	defer release()
	path := filepath.Join(dir, "kycalendar.db")
	if err := write(path); err != nil {
		return nil, counts, fmt.Errorf("%w: %v", ErrNoDatabaseSnapshot, err)
	}
	// Counted from the snapshot, not the live database, so a write after the snapshot
	// cannot make the drill fail against its own capsule.
	snap, err := openReadOnly(path)
	if err != nil {
		return nil, counts, err
	}
	defer snap.Close()
	if counts, err = countCalendars(ctx, snap); err != nil {
		return nil, counts, fmt.Errorf("backup: counting calendars: %w", err)
	}
	data, err := os.ReadFile(path)
	return data, counts, err
}

type calendarCounts struct{ Calendars, Objects int64 }

func countCalendars(ctx context.Context, db *sql.DB) (calendarCounts, error) {
	var c calendarCounts
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendars`).Scan(&c.Calendars); err != nil {
		return c, err
	}
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendar_objects`).Scan(&c.Objects)
	return c, err
}

// samePath compares two paths after making them absolute, so ./data and /abs/data agree.
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

// Members names what a capsule carries, for the screen; it is what Collect would seal now.
func Members(cfg *config.Config) []string {
	m := []string{DatabaseMember, "config/settings.json", encryptionKeyPath}
	if _, err := os.Stat(recoveryclient.RecoveryKeyPath(cfg.Database.DataDir)); err == nil {
		m = append(m, recoveryPubPath)
	}
	if cfg.InstallerSSO != nil {
		m = append(m, "installer/client.secret", "installer/directory.secret", "installer/credential-paths.json")
	}
	return m
}

func requiredFiles(files []recoveryclient.File) []string {
	req := make([]string, 0, len(files))
	for _, f := range files {
		req = append(req, f.Path)
	}
	return req
}

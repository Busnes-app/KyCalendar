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

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/config"
	_ "modernc.org/sqlite"
)

// encryptionKeyPath is where a restore drops the key that decrypts users.totp_secret_enc,
// relative to the restore target: the same <DataDir>/encryption.key config.LoadFromEnv reads.
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
func Collect(ctx context.Context, cfg *config.Config, appVersion string) (recoveryclient.Payload, error) {
	if strings.ToLower(cfg.Database.Driver) != "sqlite" {
		return recoveryclient.Payload{}, fmt.Errorf("%w: %s", ErrNoDatabaseSnapshot, cfg.Database.Driver)
	}
	// The capsule restores the database as <DataDir>/kycalendar.db; a DSN elsewhere would seal
	// a file a restored server never opens.
	dsnFile, _, _ := strings.Cut(cfg.Database.DSN, "?")
	if want := filepath.Join(cfg.Database.DataDir, filepath.Base(DatabaseMember)); filepath.Clean(dsnFile) != filepath.Clean(want) {
		return recoveryclient.Payload{}, fmt.Errorf("%w: KY_DB_DSN opens %s, backups require the default %s; unset KY_DB_DSN or point it there", ErrNoDatabaseSnapshot, dsnFile, want)
	}
	dbBytes, counts, err := snapshotSQLite(ctx, cfg.Database.DSN, cfg.Database.DataDir)
	if err != nil {
		return recoveryclient.Payload{}, err
	}
	dbPath := DatabaseMember
	files := []recoveryclient.File{{Path: dbPath, Data: dbBytes, Mode: 0600}}
	sqlitePaths := []string{dbPath}

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
			"required_files":         requiredFiles(files),
			"expected_env":           []string{"KY_PORT", "KY_DB_DRIVER"},
			"expected_ports":         []int{cfg.Server.Port},
		},
	}
	return payload, nil
}

// snapshotSQLite returns a consistent single-file copy of the live database. The store runs
// in WAL mode, so reading the main file misses every commit still in the -wal and can tear
// under a concurrent checkpoint; the lib's SQLiteSnapshot runs VACUUM INTO through a live
// connection. The scaffold opens its own handle from the DSN because store.Store exposes no
// *sql.DB.
func snapshotSQLite(ctx context.Context, dsn, dataDir string) ([]byte, calendarCounts, error) {
	var counts calendarCounts
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, counts, err
	}
	defer db.Close()
	dir, err := os.MkdirTemp(dataDir, "snapshot-*")
	if err != nil {
		return nil, counts, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "kycalendar.db")
	if err := recoveryclient.SQLiteSnapshot(ctx, db, path); err != nil {
		return nil, counts, fmt.Errorf("%w: %v", ErrNoDatabaseSnapshot, err)
	}
	// Counted from the snapshot, not the live database, so a write after VACUUM INTO
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

// Members names what a capsule carries, for the screen; it is what Collect would seal now.
func Members(cfg *config.Config) []string {
	m := []string{DatabaseMember, "config/settings.json", encryptionKeyPath}
	if _, err := os.Stat(recoveryclient.RecoveryKeyPath(cfg.Database.DataDir)); err == nil {
		m = append(m, recoveryPubPath)
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

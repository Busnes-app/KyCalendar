package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

const migrateUsage = `Usage: kycalendar migrate-postgres [--check] [--source <path>]

Copies a stopped SQLite KyCalendar into an empty PostgreSQL database, in one transaction
verified row for row before it commits. --check does everything and then rolls back.

  --source  the SQLite database (default <KY_DATA_DIR>/kycalendar.db); relative paths are
            from the current directory
  --check   verify only; the target is left as it was

The target comes from KY_MIGRATE_POSTGRES_DSN, or the file KY_MIGRATE_POSTGRES_DSN_FILE
names, never from the command line, where it would be visible to every user and in history.
The SQLite server must be stopped (its database lock is taken) and its -wal checkpointed; the
source file is never written. A private copy is made under KY_DATA_DIR and removed after.
`

// migratePostgres is the migrate-postgres command.
func migratePostgres(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	for _, a := range args {
		if strings.Contains(a, "://") {
			return errors.New("a database URL on the command line is refused; set KY_MIGRATE_POSTGRES_DSN or KY_MIGRATE_POSTGRES_DSN_FILE")
		}
	}
	flags := flag.NewFlagSet("migrate-postgres", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), migrateUsage) }
	check := flags.Bool("check", false, "verify only, then roll back")
	source := flags.String("source", "", "the SQLite database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	dsn, err := migrateTarget()
	if err != nil {
		return err
	}
	dataDir := os.Getenv("KY_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	src, err := migrateSource(*source, dataDir)
	if err != nil {
		return err
	}
	var counts map[string]int64
	won, err := store.WithSQLiteInstance(ctx, src, func(ctx context.Context) error {
		counts, err = migrateLocked(ctx, src, dataDir, dsn, *check)
		return err
	})
	if err != nil {
		return err
	}
	if !won {
		return fmt.Errorf("%s is in use by a running KyCalendar server; stop it first", src)
	}
	var rows int64
	for _, n := range counts {
		rows += n
	}
	if *check {
		fmt.Fprintf(stdout, "Check passed: %d rows in %d tables copy and verify; the target was rolled back and is unchanged.\n", rows, len(counts))
		return nil
	}
	fmt.Fprintf(stdout, "Migrated %d rows in %d tables from %s; every table verified. The source is unchanged.\n", rows, len(counts), src)
	return nil
}

// migrateTarget reads the target DSN from the environment or a file, never argv.
func migrateTarget() (string, error) {
	env, file := strings.TrimSpace(os.Getenv("KY_MIGRATE_POSTGRES_DSN")), os.Getenv("KY_MIGRATE_POSTGRES_DSN_FILE")
	switch {
	case env != "" && file != "":
		return "", errors.New("set KY_MIGRATE_POSTGRES_DSN or KY_MIGRATE_POSTGRES_DSN_FILE, not both")
	case env != "":
		return env, nil
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("KY_MIGRATE_POSTGRES_DSN_FILE: %w", err)
		}
		if dsn := strings.TrimSpace(string(b)); dsn != "" {
			return dsn, nil
		}
		return "", errors.New("KY_MIGRATE_POSTGRES_DSN_FILE: the file is empty")
	}
	return "", errors.New("set KY_MIGRATE_POSTGRES_DSN (or KY_MIGRATE_POSTGRES_DSN_FILE) to the empty PostgreSQL target")
}

// migrateSource resolves the source to a clean absolute path of a regular file.
func migrateSource(source, dataDir string) (string, error) {
	if source == "" {
		source = filepath.Join(dataDir, filepath.Base(backup.DatabaseMember))
	}
	if strings.HasPrefix(source, "file:") || strings.ContainsAny(source, "?#") {
		return "", fmt.Errorf("--source takes a file path, not a SQLite URI or DSN (got %q)", source)
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("source: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("source %s is not a file", abs)
	}
	return abs, nil
}

// migrateLocked copies src while holding its server-instance lock, and proves afterwards that
// the source bytes are unchanged and no -wal or -shm appeared beside it.
func migrateLocked(ctx context.Context, src, dataDir, dsn string, check bool) (map[string]int64, error) {
	if fi, err := os.Stat(src + "-wal"); err == nil && fi.Size() > 0 {
		return nil, fmt.Errorf("%s-wal holds commits not yet in the database; start the old server once and stop it (docker stop), so it checkpoints, then run this again", src)
	}
	_, walErr := os.Stat(src + "-wal")
	_, shmErr := os.Stat(src + "-shm")
	before, err := fileDigest(src)
	if err != nil {
		return nil, err
	}
	dir, release, err := backup.NewScratch(dataDir)
	if err != nil {
		return nil, fmt.Errorf("private copy: %w", err)
	}
	defer release()
	cp := filepath.Join(dir, "source.db")
	if err := privateCopy(ctx, src, cp); err != nil {
		return nil, err
	}
	counts, err := store.MigrateFromSQLite(ctx, cp, dsn, check)
	if err != nil {
		return nil, err
	}
	after, err := fileDigest(src)
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, errors.New("the source changed while it was copied; is a server running on it?")
	}
	for path, was := range map[string]error{src + "-wal": walErr, src + "-shm": shmErr} {
		if _, err := os.Stat(path); errors.Is(was, fs.ErrNotExist) && err == nil {
			return nil, fmt.Errorf("%s appeared during the copy", path)
		}
	}
	return counts, nil
}

// privateCopy opens src read-only and immutable (no -wal or -shm is made) and writes a
// consistent copy to cp with VACUUM INTO, refuses a newer schema, migrates an older one on
// the copy only, and checks the copy's integrity and foreign keys.
func privateCopy(ctx context.Context, src, cp string) error {
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	err = recoveryclient.SQLiteSnapshot(ctx, db, cp)
	db.Close()
	if err != nil {
		return fmt.Errorf("copy the source: %w", err)
	}
	raw, err := sql.Open("sqlite", cp)
	if err != nil {
		return err
	}
	v, err := store.SchemaVersion(ctx, raw)
	raw.Close()
	if err != nil {
		return fmt.Errorf("source: no KyCalendar schema: %w", err)
	}
	if v > migrations.LatestVersion() {
		return fmt.Errorf("the source is at schema version %d, newer than this build's %d; migrate with the version that wrote it", v, migrations.LatestVersion())
	}
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: cp})
	if err != nil {
		return fmt.Errorf("migrate the private copy: %w", err)
	}
	st.Close()
	raw, err = sql.Open("sqlite", cp)
	if err != nil {
		return err
	}
	defer raw.Close()
	if err := store.CheckSQLite(ctx, raw); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	return nil
}

func fileDigest(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

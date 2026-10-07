package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// errResetFailed marks a restore whose files are in place but whose credentials and sync epoch
// are still the backup's: the server must not start on it until restore-reset succeeds.
var errResetFailed = errors.New("restored, but the reset failed")

// checkRestorePath refuses a target the SQLite DSN cannot name: the driver cuts the path at
// '?' or '#', so the reset would open (and create) a different, empty database.
func checkRestorePath(dir string) error {
	if strings.ContainsAny(dir, "?#") {
		return fmt.Errorf("restore target %q contains '?' or '#'; choose a path without them", dir)
	}
	return nil
}

// resetRestored opens the restored database under dataDir and runs ResetAfterRestore. It is
// idempotent, so an interrupted restore is finished by running it again.
func resetRestored(ctx context.Context, dataDir string) error {
	if err := checkRestorePath(dataDir); err != nil {
		return err
	}
	path := filepath.Join(dataDir, filepath.Base(backup.DatabaseMember))
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no restored database at %s: %w", path, err)
	}
	// Not config.LoadFromEnv: it would mint an encryption key beside the restored one.
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DataDir: dataDir, DSN: config.SQLiteDSN(path)})
	if err != nil {
		return err
	}
	defer st.Close()
	return st.ResetAfterRestore(ctx)
}

const resetDone = "✓ Sessions, MFA challenges and app passwords revoked; new sync epoch written. Every user creates new app passwords; CalDAV clients resync."

func runRestoreReset(args []string) {
	fs := flag.NewFlagSet("restore-reset", flag.ExitOnError)
	target := fs.String("to", "", "directory a restore extracted into")
	_ = fs.Parse(args)
	if *target == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "Usage: kycalendar restore-reset -to <dir>")
		os.Exit(2)
	}
	if err := resetRestored(context.Background(), filepath.Join(*target, "data")); err != nil {
		log.Fatalf("Reset failed: %v\nDo not start the server on %s.", err, *target)
	}
	fmt.Println(resetDone)
}

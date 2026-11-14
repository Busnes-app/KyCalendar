package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/appversion"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "apply-setup":
			if err := runInstaller(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "init-admin":
			runInitAdmin(os.Args[2:])
			return
		case "create-user":
			runCreateUser(os.Args[2:])
			return
		case "reset-password":
			runResetPassword(os.Args[2:])
			return
		case "rename-user":
			runRenameUser(os.Args[2:])
			return
		case "backup-drill":
			if !runBackupDrill(os.Args[2:]) {
				os.Exit(1)
			}
			return
		case "export-capsule":
			runExportCapsule(os.Args[2:])
			return
		case "deposit":
			runDeposit()
			return
		case "restore":
			runRestore(os.Args[2:])
			return
		case "restore-reset":
			runRestoreReset(os.Args[2:])
			return
		case "version":
			fmt.Printf("kycalendar %s (Busnes.app calendar)\n", appversion.String())
			return
		}
	}

	runServer()
}

// shutdownTimeout drains in-flight HTTP requests. Backup work is cancelled before the drain
// starts, so an interrupted deposit request can still answer inside it.
const shutdownTimeout = 5 * time.Second

// backupWaitTimeout bounds the wait for cancelled backup work to unwind. The snapshot and the
// upload stop at once; what may still be running is the sealing and local copy of one capsule
// already collected, and the database writes that record the outcome. The two timeouts
// together must stay inside Kubernetes' default 30s termination grace and docker-compose.yml's
// stop_grace_period; TestShutdownBudgetFitsTheGracePeriods holds them in step.
const backupWaitTimeout = 10 * time.Second

func runServer() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	if cfg.Backup.AllowPrivateRecovery {
		log.Printf("[BACKUP] KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY is on: RFC1918 and CGNAT destinations admitted; loopback, link-local and other reserved addresses remain refused (HTTPS still required)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("Failed to initialize database (%s): %v", cfg.Database.Driver, err)
	}
	defer st.Close()
	if err := loadInstallerConfiguration(ctx, cfg, st); err != nil {
		log.Fatalf("Installer configuration unavailable: %v", err)
	}

	if err := bootstrapAdmin(ctx, st, cfg.Database.DataDir); err != nil {
		log.Fatalf("Failed to bootstrap the admin account: %v", err)
	}

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			if err := st.Calendars().PruneChanges(ctx, time.Now().Add(-90*24*time.Hour)); err != nil {
				log.Printf("calendar change prune failed: %v", err)
			}
			// A year of audit: covers an annual access review and a late-found incident.
			if err := st.Audit().PruneAudit(ctx, time.Now().Add(-365*24*time.Hour)); err != nil {
				log.Printf("audit prune failed: %v", err)
			}
			// Finished scheduling messages keep their dedupe keys for 30 days, never their bodies.
			if err := st.Calendars().PruneOutbox(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
				log.Printf("scheduling outbox prune failed: %v", err)
			}
			if err := api.ExpireIdleMCPTokens(ctx, st, time.Now()); err != nil {
				log.Printf("idle agent token sweep failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	srv := api.NewServer(cfg, st)
	if err := srv.LoadSignIn(ctx); err != nil {
		log.Printf("[SSO] sign-in closed: settings not applied: %v", err)
	}
	// Before serving: automatic scheduling is decided on the first KyPost lookup.
	srv.StartKyPost(ctx)
	srv.StartKyMessages(ctx)
	backupCtx, stopBackups := context.WithCancel(ctx)
	defer stopBackups()
	backupDone := make(chan struct{})
	go backupLoop(backupCtx, cfg, st, recoveryclient.NewClient(recoveryclient.Options{AllowPrivate: cfg.Backup.AllowPrivateRecovery}), time.Minute, backupDone)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			srv.RefreshDueSubscriptions(ctx)
		}
	}()
	// Scheduling deliveries. An interrupted delivery stays pending and runs again: each one is
	// idempotent (copies are written against their ETag; a reply sets the same answer again).
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			srv.DeliverScheduled(ctx)
		}
	}()
	// Video meetings, apart so a slow KyMessages never delays invitations. Each attempt is
	// idempotent: a PUT reconciles the room, and closing a closed room succeeds.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			srv.SyncMeetings(ctx)
		}
	}()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Graceful shutdown channel
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	socket, err := config.InstallerSocket(cfg.Database.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	closeInstaller, err := applysetup.StartLocal(ctx, socket, srv.ApplyInstaller, func() {
		select {
		case stop <- syscall.SIGTERM:
		default:
		}
	})
	if err != nil {
		log.Fatalf("Installer IPC: %v", err)
	}
	defer closeInstaller()

	go func() {
		log.Printf("[KY-BASE] %s listening on http://%s (DB: %s)", cfg.Server.AppName, addr, cfg.Database.Driver)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-stop
	log.Println("[KY-BASE] Shutting down gracefully...")

	// Backups first: a snapshot or upload in flight is aborted now and runs again after the
	// restart, so the waits below take seconds whatever the backup was doing.
	stopBackups()
	srv.StopDetached()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), backupWaitTimeout)
	defer waitCancel()
	waitForBackupWork(waitCtx, backupDone, srv.WaitDetached)
	log.Println("[KY-BASE] Server stopped")
}

// waitForBackupWork blocks until the scheduler loop and every detached handler have finished,
// or until ctx expires. Their slow steps are already cancelled; what is waited out here is the
// unwinding, so the receipt, key pin and audit rows are written before the store closes.
//
// Both waits start before either blocks, and both are bounded by the one context rather than a
// timer channel: a timer channel delivers its value once, so whichever wait consumed it would
// leave the other unbounded. Starting them together matters as much: waited one after the
// other, a stuck scheduled run spends the whole budget and a live detached handler gets no
// time at all. Past the deadline the work is abandoned and said so; a SIGKILL would have been
// silent.
func waitForBackupWork(ctx context.Context, backupDone <-chan struct{}, waitDetached func()) {
	handlersDone := make(chan struct{})
	go func() { defer close(handlersDone); waitDetached() }()

	select {
	case <-backupDone:
	default:
		log.Println("[KY-BASE] waiting for the interrupted scheduled backup to unwind...")
		select {
		case <-backupDone:
		case <-ctx.Done():
			log.Printf("[KY-BASE] abandoning a scheduled backup still unwinding after %s; its outcome may be unrecorded", backupWaitTimeout)
		}
	}
	select {
	case <-handlersDone:
	case <-ctx.Done():
		log.Printf("[KY-BASE] abandoning a detached backup handler still unwinding after %s; its writes may be unrecorded", backupWaitTimeout)
	}
}

// runBackup seals one capsule and delivers it to every configured destination, for the CLI:
// it builds its own RunConfig because a one-shot failure is reported to the operator and ends.
func runBackup(ctx context.Context, cfg *config.Config, st store.Store) (recoveryclient.Result, error) {
	rc, err := backup.RunConfig(cfg, appversion.String())
	if err != nil {
		return recoveryclient.Result{}, err
	}
	client := recoveryclient.NewClient(recoveryclient.Options{AllowPrivate: cfg.Backup.AllowPrivateRecovery})
	return backup.Run(ctx, cfg, rc, st.Settings(), client)
}

// backupLoop polls the admin's schedule every tick; a change in the UI needs no restart and a
// restart never loses its place, the last attempt is in the database. Cancelling ctx stops the
// wait and interrupts a run in flight (backup.Run); done is closed once the loop has returned,
// after the interrupted run's outcome is recorded.
func backupLoop(ctx context.Context, cfg *config.Config, st store.Store, client recoveryclient.Depositor, tick time.Duration, done chan<- struct{}) {
	defer close(done)
	// Built once: a deployment key that cannot seal is a configuration fault, not a run that
	// might succeed next minute. Run never gets far enough to stamp the attempt, so retrying
	// would log and audit a failure every tick forever.
	rc, err := backup.RunConfig(cfg, appversion.String())
	if err != nil {
		log.Printf("[BACKUP] scheduler disabled: %v", err)
		return
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		next, on, err := recoveryclient.NextRun(cfg.Backup.DepositInterval, backup.Settings(ctx, st.Settings()))
		if err != nil {
			log.Printf("[BACKUP] schedule unreadable: %s", recoveryclient.AuditSafe(err.Error()))
			continue
		}
		if !on || time.Now().Before(next) {
			continue
		}
		res, err := backup.Run(ctx, cfg, rc, st.Settings(), client)
		if errors.Is(err, recoveryclient.ErrNotPaired) || errors.Is(err, recoveryclient.ErrNoDestination) {
			continue // never configured; nothing to report
		}
		recordRun(context.WithoutCancel(ctx), st, "system", res, err)
	}
}

// recordRun audits one run the same way the admin route does, under the actor that started it.
func recordRun(ctx context.Context, st store.Store, actor string, res recoveryclient.Result, err error) {
	action, outcome, details := recoveryclient.Outcome(res, err)
	details["outcome"] = outcome
	_ = st.Audit().LogAudit(ctx, &store.AuditRecord{UserID: actor, Action: action,
		Resource: res.Manifest.CapsuleID, Details: api.AuditDetails(details)})
	api.RecordLastRun(ctx, st.Settings(), outcome, err)
	if err != nil {
		log.Printf("[BACKUP] %s: %s", actor, recoveryclient.AuditSafe(err.Error()))
		return
	}
	log.Printf("[BACKUP] %s: capsule %s (%d bytes) local=%q deposited=%t", actor, res.Manifest.CapsuleID, res.SizeBytes, res.LocalPath, res.Receipt != nil)
}

// runDeposit seals and delivers one capsule now, for cron or an operator at a shell.
func runDeposit() {
	ctx := context.Background()
	cfg, st := openForBackup(ctx)
	defer st.Close()

	res, err := runBackup(ctx, cfg, st)
	recordRun(ctx, st, "cli", res, err)
	if err != nil {
		log.Fatalf("Backup: %v", err)
	}
	if res.Receipt != nil {
		log.Printf("✓ Capsule %s deposited at %s; digest %s", res.Manifest.CapsuleID, res.Receipt.DepositedAt.Format(time.RFC3339), res.Receipt.Digest)
	}
}

// runInitAdmin creates or resets a break-glass local admin. The password is read from stdin.
func runInitAdmin(args []string) {
	fs := flag.NewFlagSet("init-admin", flag.ExitOnError)
	username := fs.String("username", "admin", "Admin username")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "Usage: kycalendar init-admin [-username <name>] (password on stdin)")
		os.Exit(2)
	}
	pw, err := readPasswordLine(os.Stdin)
	if err != nil {
		log.Fatal("Error: write the admin password to stdin")
	}
	if err := auth.ValidatePassword(pw); err != nil {
		log.Fatalf("Error: %v", err)
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("DB error: %v", err)
	}
	defer st.Close()

	hash, err := password.Hash(pw)
	if err != nil {
		log.Fatalf("Password hashing error: %v", err)
	}

	existing, err := st.Users().GetUserByUsername(ctx, *username)
	if err == nil && existing != nil {
		if err := st.Users().ResetAdminPassword(ctx, existing.ID, hash); err != nil {
			log.Fatalf("Failed to update admin: %v", err)
		}
		log.Printf("✓ Admin user %q password successfully reset", *username)
		return
	}

	user := &store.User{
		ID:                 fmt.Sprintf("usr_%s", crypto.RandomHex(12)),
		Username:           *username,
		DisplayName:        "Administrator",
		PasswordHash:       hash,
		Role:               "admin",
		Status:             "active",
		SSOProvider:        "local",
		MustChangePassword: true,
	}

	if err := st.Users().CreateUser(ctx, user); err != nil {
		log.Fatalf("Failed to create admin: %v", err)
	}
	log.Printf("✓ Admin user %q created successfully", *username)
}

// collectFiles is what every CLI seal uses; the sealed-only members are safe here and nowhere else.
func collectFiles(ctx context.Context, cfg *config.Config) recoveryclient.Payload {
	payload, err := backup.Collect(ctx, cfg, appversion.String())
	if err != nil {
		log.Fatalf("Failed to collect backup files: %v", err)
	}
	return payload
}

// runBackupDrill prints the drill summary and reports whether it passed.
func runBackupDrill(args []string) bool {
	ctx := context.Background()
	cfg, st := openForBackup(ctx)
	defer st.Close()

	payload := collectFiles(ctx, cfg)
	result, err := backup.RunDrill(ctx, cfg, payload)
	if err != nil {
		log.Fatalf("Drill execution error: %v", err)
	}

	fmt.Printf("\n=== KyBackup Restore Drill Summary ===\n")
	fmt.Printf("Status:   %s\n", map[bool]string{true: "PASSED (OK)", false: "FAILED"}[result.Passed])
	fmt.Printf("Duration: %d ms\n", result.DurationMs)
	for _, check := range result.Checks {
		status := "✓"
		if !check.Passed {
			status = "✗"
		}
		fmt.Printf("  [%s] %s: %s\n", status, check.Name, check.Message)
	}
	fmt.Println("==================================================")
	return result.Passed
}

// openForBackup opens a one-shot backup command's configuration and store and loads the stored
// installer setup, as runServer does, so a managed install's capsules and drills carry its
// installer credentials.
func openForBackup(ctx context.Context) (*config.Config, store.Store) {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("DB error: %v", err)
	}
	if err := loadInstallerConfiguration(ctx, cfg, st); err != nil {
		log.Fatalf("Installer configuration unavailable: %v", err)
	}
	return cfg, st
}

func runExportCapsule(args []string) {
	fs := flag.NewFlagSet("export-capsule", flag.ExitOnError)
	out := fs.String("out", "", "output path (default <capsule-id>.kycap in the current directory)")
	_ = fs.Parse(args)

	ctx := context.Background()
	cfg, st := openForBackup(ctx)
	defer st.Close()

	key, err := recoveryclient.LoadRecoveryKey(cfg.Database.DataDir, backup.Settings(ctx, st.Settings()))
	if err != nil {
		log.Fatalf("Recovery key: %v", err)
	}
	raw, m, err := recoveryclient.Seal(collectFiles(ctx, cfg), key)
	if err != nil {
		log.Fatalf("Seal: %v", err)
	}
	path := *out
	if path == "" {
		path = recoveryclient.FilenameSafe(m.CapsuleID) + ".kycap"
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		log.Fatalf("Write: %v", err)
	}
	log.Printf("✓ Capsule %s sealed to recovery key %s, written to %s (%d bytes)", m.CapsuleID, m.RecoveryKeyID, path, len(raw))
}

// restore is the product-side half of the ceremony, owned by the lib: k custodian shares
// combined, used once, dropped; a capsule from another service refused before the key is
// touched; the authenticated manifest printed for comparison with KyRecovery's record.
// The restored database then loses every credential and sync epoch issued before the backup.
func restore(capsulePath, targetDir, expectService string, shares []string, stdout io.Writer) error {
	if err := checkRestorePath(targetDir); err != nil {
		return err
	}
	if err := recoveryclient.Restore(capsulePath, targetDir, expectService, shares, stdout); err != nil {
		return err
	}
	if err := resetRestored(context.Background(), filepath.Join(targetDir, "data")); err != nil {
		return fmt.Errorf("%w: %w", errResetFailed, err)
	}
	return nil
}

// stdinIsTerminal reports whether a human is typing, so a pipeline gets no stray prompt.
func stdinIsTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func runRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	capsulePath := fs.String("capsule", "", "path to the .kycap file")
	target := fs.String("to", "", "empty directory to restore into")
	service := fs.String("service", "", "expected service name (default: kycalendar)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: kycalendar restore -capsule <file.kycap> -to <dir> [-service <name>]\n\n"+
			"Custodian shares are read from stdin, one ky2-... share per line, and never from\n"+
			"the command line: argv is world-readable and lands in shell history.\n\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if *capsulePath == "" || *target == "" {
		fs.Usage()
		os.Exit(2)
	}
	if *service == "" {
		*service = backup.ServiceName
	}

	if stdinIsTerminal() {
		fmt.Fprintln(os.Stderr, "Paste custodian shares, one per line, then Ctrl-D:")
	}
	shares, err := recoveryclient.ReadShares(os.Stdin)
	if err != nil {
		log.Fatalf("Reading shares: %v", err)
	}
	if len(shares) == 0 {
		log.Fatal("Error: no custodian shares on stdin")
	}
	if err := restore(*capsulePath, *target, *service, shares, os.Stdout); err != nil {
		if errors.Is(err, errResetFailed) {
			log.Fatalf("%v\nDo not start the server on %s. Finish with: kycalendar restore-reset -to %s", err, *target, *target)
		}
		log.Fatalf("Restore failed: %v", err)
	}
	fmt.Println(resetDone)
}

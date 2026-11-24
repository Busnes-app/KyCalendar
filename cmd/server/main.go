package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	"github.com/Busnes-app/kycalendar/internal/worker"
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
		case "purge-org":
			if err := runPurgeOrg(os.Args[2:]); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintln(os.Stderr, "purge-org:", err)
				os.Exit(1)
			}
			return
		case "advance-storage-sequence":
			if err := runAdvanceStorageSequence(os.Args[2:]); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintln(os.Stderr, "advance-storage-sequence:", err)
				os.Exit(1)
			}
			return
		case "export-org":
			if err := runExportOrg(os.Args[2:]); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintln(os.Stderr, "export-org:", err)
				os.Exit(1)
			}
			return
		case "restore-reset":
			runRestoreReset(os.Args[2:])
			return
		case "migrate-postgres":
			if err := migratePostgres(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintln(os.Stderr, "migrate-postgres:", err)
				os.Exit(1)
			}
			return
		case "accept-replica-config":
			if err := runAcceptReplicaConfig(os.Args[2:], os.Stdout); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintln(os.Stderr, "accept-replica-config:", err)
				os.Exit(1)
			}
			return
		case "version":
			fmt.Printf("kycalendar %s (Busnes.app calendar)\n", appversion.String())
			return
		}
	}

	runServer()
}

// readinessDrainDelay is how long a stopping PostgreSQL replica keeps serving with /readyz at
// 503: five one-second proxy probes, so a proxy routes elsewhere before the listener closes.
// SQLite is one server with nowhere else to route, so it does not wait.
const readinessDrainDelay = 5 * time.Second

// shutdownTimeout drains in-flight HTTP requests. Backup work is cancelled before the drain
// starts, so an interrupted deposit request can still answer inside it.
const shutdownTimeout = 5 * time.Second

// backupWaitTimeout bounds the wait for cancelled background work to unwind: the elected
// jobs, an interrupted scheduled backup's sealing and outcome, and detached handlers. With
// the drain and shutdown it must stay inside Kubernetes' default 30s termination grace and
// docker-compose.yml's stop_grace_period; TestShutdownBudgetFitsTheGracePeriods holds them.
const backupWaitTimeout = 10 * time.Second

// drainDelay is readinessDrainDelay on PostgreSQL and nothing on SQLite.
func drainDelay(driver string) time.Duration {
	if driver == "postgres" {
		return readinessDrainDelay
	}
	return 0
}

func runServer() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	if cfg.Database.Driver != "postgres" {
		// One server per SQLite file: a second server or migrate-postgres is refused. CLI
		// backups and user commands share the file without this lock.
		path, _, _ := strings.Cut(cfg.Database.DSN, "?")
		won, err := store.WithSQLiteInstance(context.Background(), path, func(context.Context) error {
			runConfiguredServer(cfg)
			return nil
		})
		if err != nil {
			log.Fatalf("SQLite instance lock: %v", err)
		}
		if !won {
			log.Fatal("Another KyCalendar server or migrate-postgres is using this SQLite database; stop it first. Run replicas on PostgreSQL.")
		}
		return
	}
	runConfiguredServer(cfg)
}

// prepareStart is everything that must pass before the server listens: the first admin and,
// on PostgreSQL, the replica configuration check, both under the bootstrap lock migrations
// hold too; removal of plaintext snapshots a killed process left; the recovery key.
func prepareStart(ctx context.Context, cfg *config.Config, st store.Store) error {
	if err := st.Coordination().Serialized(ctx, "bootstrap", func(ctx context.Context) error {
		if err := bootstrapAdmin(ctx, st, cfg.Database.DataDir); err != nil {
			return fmt.Errorf("bootstrap the admin account: %w", err)
		}
		if st.Driver() != "postgres" {
			return nil
		}
		return checkReplicaConfig(ctx, cfg, st.Settings())
	}); err != nil {
		return err
	}
	if n, err := backup.RemoveStaleSnapshots(cfg.Database.DataDir); err != nil {
		log.Printf("[BACKUP] leftover snapshot scratch under %s not removed: %v", cfg.Database.DataDir, err)
	} else if n > 0 {
		log.Printf("[BACKUP] removed %d snapshot scratch directories a stopped process left", n)
	}
	// A key this server cannot prove refuses every backup run; it is said at startup too.
	if err := backup.SyncRecoveryKey(ctx, cfg, st); err != nil {
		log.Printf("[BACKUP] recovery key: %s", recoveryclient.AuditSafe(err.Error()))
	}
	return nil
}

func runConfiguredServer(cfg *config.Config) {
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
	if err := prepareStart(ctx, cfg, st); err != nil {
		log.Fatal(err)
	}

	srv := api.NewServer(cfg, st)
	if err := srv.LoadSignIn(ctx); err != nil {
		log.Printf("[SSO] sign-in closed: settings not applied: %v", err)
	}
	// Before serving: automatic scheduling is decided on the first KyPost lookup. Links and
	// their secrets stay per process; every replica keeps its own.
	srv.StartKyPost(ctx)
	srv.StartKyMessages(ctx)
	go srv.FollowSignIn(ctx)

	// Elected background work: one server at a time runs it (internal/worker).
	workCtx, stopWork := context.WithCancel(ctx)
	defer stopWork()
	workDone := make(chan struct{})
	client := recoveryclient.NewClient(recoveryclient.Options{AllowPrivate: cfg.Backup.AllowPrivateRecovery})
	go func() {
		defer close(workDone)
		worker.Run(workCtx, st.Coordination(), workerOptions(st), scheduledJobs(cfg, st, srv, client, time.Minute))
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

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
	go func() {
		log.Printf("[KY-BASE] %s listening on http://%s (DB: %s)", cfg.Server.AppName, addr, cfg.Database.Driver)
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-stop
	gracefulStop(srv, httpServer, stopWork, cancel, workDone, drainDelay(cfg.Database.Driver))
}

// gracefulStop is the SIGTERM order: /readyz turns 503 and new backup requests are refused;
// elected work and detached backup legs are cancelled, so another replica can take over;
// on PostgreSQL the server keeps serving for drain, so a proxy probing every second routes
// elsewhere; then the listener drains in-flight requests and the cancelled work unwinds.
func gracefulStop(srv *api.Server, httpServer *http.Server, stopWork, cancel func(), workDone <-chan struct{}, drain time.Duration) {
	srv.StartDraining()
	log.Printf("[KY-BASE] draining: /readyz answers 503; serving %s more before closing the listener", drain)
	stopWork()
	srv.StopDetached()
	time.Sleep(drain)

	log.Println("[KY-BASE] Shutting down gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), backupWaitTimeout)
	defer waitCancel()
	waitForBackupWork(waitCtx, workDone, srv.WaitDetached)
	log.Println("[KY-BASE] Server stopped")
}

// waitForBackupWork blocks until the elected work and every detached handler have finished,
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
		log.Println("[KY-BASE] waiting for the interrupted scheduled work to unwind...")
		select {
		case <-backupDone:
		case <-ctx.Done():
			log.Printf("[KY-BASE] abandoning scheduled work still unwinding after %s; its outcome may be unrecorded", backupWaitTimeout)
		}
	}
	select {
	case <-handlersDone:
	case <-ctx.Done():
		log.Printf("[KY-BASE] abandoning a detached backup handler still unwinding after %s; its writes may be unrecorded", backupWaitTimeout)
	}
}

// workerOptions times the election: a new PostgreSQL owner waits out the lock's probe bound
// before its first job; SQLite's flock cannot be lost, so it starts at once.
func workerOptions(st store.Store) worker.Options {
	if st.Driver() == "postgres" {
		return worker.Options{Retry: 2 * time.Second, Grace: store.ProbeBound() + time.Second}
	}
	return worker.Options{Retry: 2 * time.Second}
}

// scheduledJobs is the work exactly one server runs, at today's intervals, each on its own
// goroutine so a slow KyMessages never delays invitations. Every job tolerates a repeat: an
// interrupted delivery stays pending (copies are written against their ETag, a reply sets
// the same answer again, KyPost dedupes on the outbox key as its event ID), a KyMessages PUT
// reconciles and closing a closed room succeeds, and suite groups write only differences.
func scheduledJobs(cfg *config.Config, st store.Store, srv *api.Server, client recoveryclient.Depositor, backupTick time.Duration) []worker.Job {
	jobs := []worker.Job{
		{Name: "prune", Every: 24 * time.Hour, AtStart: true, Run: func(ctx context.Context) { prune(ctx, st) }},
		{Name: "subscriptions", Every: time.Minute, Run: func(ctx context.Context) {
			srv.RefreshDueSubscriptions(ctx)
			srv.PruneBookings(ctx)
		}},
		{Name: "deliveries", Every: 5 * time.Second, Run: srv.DeliverScheduled},
		{Name: "meetings", Every: 5 * time.Second, Run: func(ctx context.Context) {
			srv.SyncMeetings(ctx)
			srv.SyncPresence(ctx)
		}},
		{Name: "orgs", Every: api.OrgsInterval, AtStart: true, Run: srv.SyncOrgs},
		{Name: "org-purge", Every: api.OrgsInterval, Run: srv.PurgeDeletedOrgs},
		{Name: "suite-groups", Every: api.SuiteGroupsInterval, AtStart: true, Run: srv.SyncSuiteGroups},
		// Every new client address adds a rate-window row on PostgreSQL; expired ones go often,
		// so a spread of addresses cannot grow the table for a day.
		{Name: "coordination-cleanup", Every: 10 * time.Minute, AtStart: true, Run: func(ctx context.Context) {
			if err := st.Coordination().CleanExpired(ctx); err != nil {
				log.Printf("expired rate windows, CAPTCHA salts and KyPost event IDs not removed: %v", err)
			}
		}},
	}
	if j, ok := backupJob(cfg, st, client, backupTick); ok {
		jobs = append(jobs, j)
	}
	return jobs
}

// prune is the daily housekeeping.
func prune(ctx context.Context, st store.Store) {
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
	// The completion log feeds streaks and insights (12 weeks); search rows of deleted lists go too.
	if err := st.Tasks().PruneTasks(ctx, time.Now().Add(-400*24*time.Hour)); err != nil {
		log.Printf("task log prune failed: %v", err)
	}
	if err := api.ExpireIdleMCPTokens(ctx, st, time.Now()); err != nil {
		log.Printf("idle agent token sweep failed: %v", err)
	}
}

// backupJob is the scheduled backup: each tick it takes the backup-run lock, then reads
// whether a run is due (after the lock, so a server that waited never runs a stale due run),
// then runs it. ok is false when no RunConfig can be built: a deployment key that cannot seal
// is a configuration fault, not a run that might succeed next minute, and retrying would log
// and audit a failure every tick forever.
func backupJob(cfg *config.Config, st store.Store, client recoveryclient.Depositor, tick time.Duration) (worker.Job, bool) {
	rc, err := backup.RunConfig(cfg, appversion.String())
	if err != nil {
		log.Printf("[BACKUP] scheduler disabled: %v", err)
		return worker.Job{}, false
	}
	return worker.Job{Name: "backup", Every: tick, Run: func(ctx context.Context) {
		won, err := backup.WithRunLock(ctx, st, func(ctx context.Context) error {
			next, on, err := recoveryclient.NextRun(cfg.Backup.DepositInterval, backup.Settings(ctx, st.Settings()))
			if err != nil {
				recordRun(context.WithoutCancel(ctx), st, "system", recoveryclient.Result{}, fmt.Errorf("schedule unreadable: %w", err))
				return nil
			}
			if !on || time.Now().Before(next) {
				return nil
			}
			res, err := backup.Run(ctx, cfg, rc, st, client)
			if errors.Is(err, recoveryclient.ErrNotPaired) || errors.Is(err, recoveryclient.ErrNoDestination) {
				return nil // never configured; nothing to report
			}
			recordRun(context.WithoutCancel(ctx), st, "system", res, err)
			return nil
		})
		if err != nil && !won {
			log.Printf("[BACKUP] scheduled run skipped: the backup lock is unavailable: %v", err)
		}
	}}, true
}

// backupLoop runs the scheduled backup alone under the election, for tests; done is closed
// once it has returned, after an interrupted run's outcome is recorded.
func backupLoop(ctx context.Context, cfg *config.Config, st store.Store, client recoveryclient.Depositor, tick time.Duration, done chan<- struct{}) {
	defer close(done)
	j, ok := backupJob(cfg, st, client, tick)
	if !ok || ctx.Err() != nil {
		return
	}
	worker.Run(ctx, st.Coordination(), workerOptions(st), []worker.Job{j})
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

// errBusy is a CLI backup refused because another holds the backup-run lock.
var errBusy = errors.New("another backup is running (scheduled, Back up now or another CLI run); try again when it has finished")

// runBackup seals one capsule and delivers it to every configured destination, for the CLI,
// under the backup-run lock: a one-shot failure is reported to the operator and ends.
func runBackup(ctx context.Context, cfg *config.Config, st store.Store) (recoveryclient.Result, error) {
	rc, err := backup.RunConfig(cfg, appversion.String())
	if err != nil {
		return recoveryclient.Result{}, err
	}
	client := recoveryclient.NewClient(recoveryclient.Options{AllowPrivate: cfg.Backup.AllowPrivateRecovery})
	var res recoveryclient.Result
	won, lockErr := backup.WithRunLock(ctx, st, func(ctx context.Context) error {
		res, err = backup.Run(ctx, cfg, rc, st, client)
		return nil
	})
	if !won {
		if lockErr == nil {
			lockErr = errBusy
		}
		return recoveryclient.Result{}, lockErr
	}
	return res, err
}

// runDeposit seals and delivers one capsule now, for cron or an operator at a shell.
func runDeposit() {
	ctx := context.Background()
	cfg, st := openForBackup(ctx)
	defer st.Close()

	res, err := runBackup(ctx, cfg, st)
	if errors.Is(err, errBusy) {
		st.Close()
		log.Fatalf("Backup: %v", err)
	}
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
func collectFiles(ctx context.Context, cfg *config.Config, st store.Store) recoveryclient.Payload {
	payload, err := backup.Collect(ctx, cfg, st, appversion.String())
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

	payload := collectFiles(ctx, cfg, st)
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

	won, err := backup.WithRunLock(ctx, st, func(ctx context.Context) error {
		if err := backup.SyncRecoveryKey(ctx, cfg, st); err != nil {
			return fmt.Errorf("recovery key: %w", err)
		}
		key, err := recoveryclient.LoadRecoveryKey(cfg.Database.DataDir, backup.Settings(ctx, st.Settings()))
		if err != nil {
			return fmt.Errorf("recovery key: %w", err)
		}
		raw, m, err := recoveryclient.Seal(collectFiles(ctx, cfg, st), key)
		if err != nil {
			return fmt.Errorf("seal: %w", err)
		}
		path := *out
		if path == "" {
			path = recoveryclient.FilenameSafe(m.CapsuleID) + ".kycap"
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		log.Printf("✓ Capsule %s sealed to recovery key %s, written to %s (%d bytes)", m.CapsuleID, m.RecoveryKeyID, path, len(raw))
		return nil
	})
	if err == nil && !won {
		err = errBusy
	}
	if err != nil {
		st.Close()
		log.Fatalf("Export: %v", err)
	}
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

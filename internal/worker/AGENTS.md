# Worker

## Purpose
Runs the scheduled background jobs on exactly one server at a time.

## Ownership
Owns the election loop (`worker.Run`) and the job runner. The jobs themselves, their intervals and the backup schedule are `cmd/server`'s (`scheduledJobs`, `backupJob`).

## Local Contracts
- `Run` keeps taking the `scheduled-worker` lock (`store.CoordinationStore.WithLock`) until its context ends. While it owns the lock it runs every `Job` on its own goroutine, so a slow job never delays another; losing the lock cancels every job's context, and `Run` waits for all of them to return before it tries again, so this server never runs a job beside a later owner.
- `Options.Grace` is how long a new owner waits before its first job: `store.ProbeBound()` plus a second on PostgreSQL (3 s), so a predecessor whose session the database dropped has already cancelled. `TestOwnershipGraceOutlastsTheProbe` and `TestNewOwnerStartsAfterThePredecessorStops` (a predecessor behind a frozen relay) pin it. SQLite's flock cannot be lost, so it uses no grace. `Options.Retry` is how often a non-owner tries again.
- Single ownership, not exactly-once: a job cancelled mid-call may have finished its remote half, so every job must tolerate a repeat (ETag-guarded copies, the outbox dedupe key as KyPost's event ID, reconciling KyMessages PUTs, difference-only suite-group writes).

## Verification
- `go test ./cmd/server -run 'TestOneOfThreeServersRunsTheJobs|TestSecondSQLiteProcessNeverRunsJobs|TestTakeoverNeverOverlapsDeposits'` (the PostgreSQL ones need `KY_TEST_POSTGRES_DSN`)

## Child DOX Index
None.

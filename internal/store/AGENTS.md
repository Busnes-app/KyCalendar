# Storage Layer

## Purpose
Provides the unified Database Abstraction Layer (DAL) supporting pluggable backends (SQLite zero-CGO default and PostgreSQL enterprise) with automated dialect-aware migrations.

## Ownership
Owns data models, store interfaces (`UserStore`, `SessionStore`, `DeviceStore`, `GroupStore`, `AuditStore`, `SettingsStore`, `CalendarStore`, `AppPasswordStore`), dialect translations, and schema migrations.

## Local Contracts
- `CompletePasswordChange` atomically compares the old password, updates a flagged local account, clears the flag, deletes sessions/MFA challenges/device pairings and records `auth.password_changed`. Session/MFA issuance locks the same user row against the verified hash; MFA challenges persist the creation-time password hash, and consumption returns that snapshot to reject stale completions. Migration 4 discards preexisting challenges because their credential snapshot is unknown.
- `ResetAdminPassword` reactivates a local administrator with the replacement flag set and shares the atomic grant purge and audit path with `CompletePasswordChange`; it also works for disabled accounts.
- Migration 5 rebuilds `device_pairings` without the six-digit code column and with `authenticated_at`; pending pairings (90 s) are dropped on upgrade.
- Migration 6 adds `calendars` (owner_kind user|group, per-owner unique slug, `seq`, `min_sync_seq`), `calendar_objects` (unique name and uid per calendar), `calendar_changes` and `calendar_meta` (random `sync_epoch`).
- Migration 7 adds `app_passwords` (id, user_id cascade, label, SHA-256 hex `hash`, `last_used_at`). `AppPasswordStore.Delete` is scoped to the owning user and returns `ErrNotFound` otherwise; `DeleteByUser` revokes all.
- `CalendarStore.PutObject`/`DeleteObject` run in one transaction: check preconditions (`ErrPreconditionFailed`, `ErrUIDConflict`), bump `calendars.seq`, log one change. Failed writes leave seq and data untouched. `ifMatch` `"*"` means any current version (missing object: `ErrPreconditionFailed` on PUT, `ErrNotFound` on DELETE).
- Owner quotas are enforced atomically in the store: `PutObject` takes `OwnerLimits{MaxObjects, MaxBytes}` (zero = no limit) for the target calendar's owner across all their calendars, and `CreateCalendar` takes `maxPerOwner`. Both take the owner lock (`pg_advisory_xact_lock(hashtextextended('calendar-owner:<kind>:<id>', 0))` on Postgres, which also covers an owner with no rows yet; nothing on SQLite, whose single connection serializes transactions) before the calendar row lock, then count usage excluding the object being replaced; over a cap is `ErrQuotaExceeded`. `TestConcurrentQuota` pins this on both backends.
- `CountObjectsByOwner` and `SumObjectBytesByOwner` (`COALESCE(SUM(LENGTH(data)), 0)`, bytes on SQLite BLOB and Postgres BYTEA) report usage; the quota checks do not depend on them.
- Object ETag is the SHA-256 hex of the stored bytes. `first_start` and `last_end` are unix seconds; NULL `last_end` means unbounded.
- `PruneChanges` sets `min_sync_seq` to the highest pruned seq; `ChangesSince` returns `ErrSyncTokenExpired` only when the token seq is below it.
- `store.Open(ctx, cfg)` initializes and auto-migrates the configured database backend.
- SQLite runs in WAL mode with foreign keys enabled.
- PostgreSQL queries are rebound dynamically from standard positional parameters.
- MFA challenges and device pairings are consumed with database state transitions that permit exactly one successful use.
- Recovery-code hash updates use optimistic concurrency so simultaneous redemption cannot reuse a code.

## Verification
- `go test -v ./internal/store/...`

## Child DOX Index
None.

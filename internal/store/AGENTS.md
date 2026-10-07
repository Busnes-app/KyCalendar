# Storage Layer

## Purpose
Provides the unified Database Abstraction Layer (DAL) supporting pluggable backends (SQLite zero-CGO default and PostgreSQL enterprise) with automated dialect-aware migrations.

## Ownership
Owns data models, store interfaces (`UserStore`, `SessionStore`, `DeviceStore`, `GroupStore`, `AuditStore`, `SettingsStore`, `CalendarStore`, `AppPasswordStore`), dialect translations, and schema migrations.

## Local Contracts
- `CompletePasswordChange` atomically compares the old password, updates a flagged local account, clears the flag, deletes sessions/MFA challenges/device pairings/app passwords and records `auth.password_changed`. Session/MFA issuance locks the same user row against the verified hash; MFA challenges persist the creation-time password hash, and consumption returns that snapshot to reject stale completions. Migration 4 discards preexisting challenges because their credential snapshot is unknown.
- `ResetAdminPassword` reactivates a local administrator with the replacement flag set and shares the atomic grant purge and audit path with `CompletePasswordChange`; it also works for disabled accounts.
- `ResetPassword` is the same operator reset (`operatorReset`: hash, replacement flag, grant purge, `auth.password_changed` audit, one transaction) for any local account, without touching role or status; a missing or non-local account is `ErrNotFound`.
- Migration 5 rebuilds `device_pairings` without the six-digit code column and with `authenticated_at`; pending pairings (90 s) are dropped on upgrade.
- Migration 6 adds `calendars` (owner_kind user|group, per-owner unique slug, `seq`, `min_sync_seq`), `calendar_objects` (unique name and uid per calendar), `calendar_changes` and `calendar_meta` (random `sync_epoch`).
- Migration 7 adds `app_passwords` (id, user_id cascade, label, SHA-256 hex `hash`, `last_used_at`). `AppPasswordStore.Delete` is scoped to the owning user and returns `ErrNotFound` otherwise; `DeleteByUser` revokes all.
- Migration 8 adds `calendar_grants` (calendar_id and group_id, both `ON DELETE CASCADE`; role `reader|editor|manager`; one row per calendar and group). Deleting a group removes its grants; deleting a calendar removes its objects, changes and grants.
- Migration 9 deletes the sessions of `admin` users whose `sso_provider` is `kysignon` or `scim` (admins the retired global-role webhook could have made); they sign in again and the `roles` claim decides. `TestMigration9RevokesSSOAdminSessions` reruns it over seeded sessions.
- A group calendar is `owner_kind = 'group'` with `owner_id` = its own calendar ID: no KyIdentity group owns it, so per-owner quotas apply per group calendar. `SetGrant` refuses (`ErrNotFound`) a personal calendar or a missing group; `DeleteGrant` is idempotent; `UserGrants` joins `group_members`, so access follows current membership.
- `CalendarStore.PutObject`/`DeleteObject` run in one transaction: check preconditions (`ErrPreconditionFailed`, `ErrUIDConflict`), bump `calendars.seq`, log one change. Failed writes leave seq and data untouched. `ifMatch` `"*"` means any current version (missing object: `ErrPreconditionFailed` on PUT, `ErrNotFound` on DELETE).
- Owner quotas are enforced atomically in the store: `PutObject` takes `OwnerLimits{MaxObjects, MaxBytes}` (zero = no limit) for the target calendar's owner across all their calendars, and `CreateCalendar` takes `maxPerOwner`. Both take the owner lock (`pg_advisory_xact_lock(hashtextextended('calendar-owner:<kind>:<id>', 0))` on Postgres, which also covers an owner with no rows yet; nothing on SQLite, whose single connection serializes transactions) before the calendar row lock, then count usage excluding the object being replaced (`LENGTH(data)` counts bytes on SQLite BLOB and Postgres BYTEA); over a cap is `ErrQuotaExceeded`. Those transactions begin through `quotaTx`, which pins `READ COMMITTED` on Postgres so a server default of `REPEATABLE READ` cannot freeze the snapshot before the lock (SQLite uses the default). `TestConcurrentQuota` pins this on both backends; `TestConcurrentQuotaRepeatableReadDefault` on Postgres.
- `OwnerLimits.MaxTotalBytes` caps every owner's bytes together (excluding the object being replaced). On Postgres it takes the `calendar-total` advisory lock before any owner lock; every writer that takes both uses that order.
- `DeleteUser` deletes the user's calendars in the same transaction (objects and changes cascade); `calendars.owner_id` has no foreign key, and orphaned bytes would hold the instance cap forever.
- `ListUsers` takes a `UserFilter`: an exact, case-insensitive match on username, email or display name (columns from `userFilterColumns` only), never a substring.
- `GetUserByUsername` matches case-insensitively and returns the local account first, then the exact spelling: uniqueness is case-sensitive, so an SSO `ADMIN` may exist beside local `admin`.
- `GetObjectByUID` finds an object by its UID within one calendar (UIDs are unique per calendar), for the JSON event API.
- Object ETag is the SHA-256 hex of the stored bytes. `first_start` and `last_end` are unix seconds; NULL `last_end` means unbounded.
- `PruneChanges` sets `min_sync_seq` to the highest pruned seq; `ChangesSince` returns `ErrSyncTokenExpired` only when the token seq is below it.
- `Store.ResetAfterRestore` deletes every session, MFA challenge, device pairing and app password (the grants `revokePasswordGrants` clears), writes a new random `sync_epoch` (16 hex) and audits `system.restore_reset` (user `system`, details carry the epoch), in one transaction. Accounts, password hashes, `must_change_password`, recovery codes and calendar data stay as restored: a blanket forced change is no defence against a leaked restored password, so the runbook resets rotated ones by hand. It is idempotent; `cmd/server` runs it after `restore` and from `restore-reset`.
- `store.Open(ctx, cfg)` initializes and auto-migrates the configured database backend.
- SQLite runs in WAL mode with foreign keys enabled.
- PostgreSQL queries are rebound dynamically from standard positional parameters.
- MFA challenges and device pairings are consumed with database state transitions that permit exactly one successful use.
- Recovery-code hash updates use optimistic concurrency so simultaneous redemption cannot reuse a code.

## Verification
- `go test -v ./internal/store/...`

## Child DOX Index
None.

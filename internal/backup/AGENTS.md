# Backup

## Purpose
Adapts the scaffold to `github.com/Busnes-app/ky-primitives/recoveryclient`, which owns the
KyRecovery pairing, sealing, deposit, restore and drill contract. This package supplies only
what differs per product: a `Settings` adapter over `store.SettingsStore`, a `Sealer` under the
deployment key, the payload the scaffold seals (`Collect`), and the drill's verification
checks (`Checks`).

## Ownership
Owns the settings adapter (`settings.go`), payload collection (`payload.go`), and restore-drill
checks (`drill.go`) and serialized drill entry point (`run_drill.go`). It holds no private key, no share, and no pairing state of its own — those
live in `recoveryclient` and in the settings rows it reads and writes through the adapter.

## Local Contracts
- `Settings` maps `store.ErrNotFound` to `recoveryclient.ErrNotFound`; every other error passes
  through unchanged.
- `NewSealer` seals the KyRecovery token under the deployment key with label
  `ky_server_base:setting:kyrecovery_token`, domain-separated so a row copied from another
  setting will not open.
- `Collect` snapshots SQLite with the lib's `SQLiteSnapshot` (`VACUUM INTO`; the store runs in
  WAL mode, so a plain file read misses uncheckpointed commits) and returns
  `ErrNoDatabaseSnapshot` for any other driver, so a capsule without a consistent database is
  never sealed. It also carries the encryption key (`data/encryption.key`, required — restores
  a database whose sealed secrets are unreadable otherwise) and the pinned recovery public key
  (`data/recovery.pub`, only when paired).
- `ServiceName` (`kycalendar`) is what is claimed, sealed and used for local copies; `KY_APP_NAME` is display only. `DatabaseMember` (`data/kycalendar.db`) must equal the file the default DSN opens, so a restored tree starts on restored data; `Collect` refuses (`ErrNoDatabaseSnapshot`) a DSN whose file is not `<DataDir>/kycalendar.db`.
- A snapshot larger than one capsule member (`MaxCapsuleFileBytes`, 64 MiB) travels as ordered members `data/kycalendar.db.part-0001`, `-0002`, ... (`databaseFiles`, `parts.go`), named in order in the recipe's `database_parts`; `required_files` lists the parts and `data/kycalendar.db`. A snapshot that fits stays the single member `data/kycalendar.db`, as in every older capsule. `Checks` joins the parts in the scratch directory before any other check (the parts must be the capsule's, in order, with no whole database beside them), so the drill checks the database a restore gets; `restore` and `restore-reset` join them (`JoinRestoredDatabase`: from 0001 without a gap, through a temporary file renamed into place) and remove them last part first before the reset; run again after an interruption, it keeps a joined database only if it begins with exactly the remaining parts' bytes, and refuses anything else. Past 80% of the capsule total (`MaxCapsuleTotalBytes`, 256 MiB) `Collect` logs a warning; past the total, sealing fails as before. The library limits are never raised (DECISIONS: large databases in backups).
- `Checks(dir, opened)` reads the opened capsule's manifest, normalizes JSON lists and
  fails malformed or incomplete recipes. Required files include all capsule members and
  the database, settings and encryption key; SQLite integrity and required environment
  checks cannot be disabled. File checks accept only clean relative manifest members;
  SQLite opens read-only and missing/empty databases fail.
- `Collect` counts `calendars` and `calendar_objects` in the snapshot file (not the live database) and records `calendar_count` and `object_count` in the recipe. `Checks` requires both as non-negative integers, then `Calendar Counts` compares them with the restored database and `Calendar Objects` decodes and `calendar.Inspect`s up to 50 objects; messages name the calendar ID and the object's position in the sample, never its client-chosen name or data.
- HTTP and CLI call `RunDrill`, which holds an OS advisory lock on `<data dir>/drill.lock`
  across scratch preparation and the library drill. Contention returns `ErrDrillBusy`;
  closing the descriptor or process exit releases ownership. Keep the lock file in place.
  The Unix lock matches the Linux container deployment.
- `DrillRoot` is under the data directory and forced to 0700; opened payloads stay in the
  library's private, disposable subdirectories. Drills use throwaway keys, not custodian shares.
- `Members` names what `Collect` would seal now, for the status route and the screen; keep
  the two in step.
- `GET /api/settings`'s `extra_settings` never carries `kyrecovery_token_enc` or a legacy
  plaintext `kyrecovery_token`; the filter drops every key with the `kyrecovery_token`
  prefix, so a rename in the lib cannot leak. An admin sees the paired `kyrecovery_url`,
  never the credential.
- `Run` (`run.go`) is the only caller of `recoveryclient.Run` in the server, scheduler and
  CLI. Cancelling its context aborts the snapshot and the upload; settings writes use
  `context.WithoutCancel`, so a receipt already returned is recorded. An interrupted run
  restores the previous `backup_last_attempt` row (or deletes it when there was none) and
  returns `ErrInterrupted`. A run that fails for another reason keeps its stamp.
  `backup_last_attempt` is the lib's unexported key; `run_test.go` fails if it changes.
- Pairing, the write-once key pin, the lib's `Run` (one seal, every destination), the schedule, local
  copies and their pruning, drill mechanics, restore and the decrypt guard are the lib's;
  their contracts are in the `recoveryclient` README. `client_test.go` pins only what this
  package's wiring buys: `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY` admits RFC1918 and CGNAT and
  nothing else.

- Managed payloads require private OIDC and directory credentials, sealed as `installer/client.secret` and `installer/directory.secret` with `installer/credential-paths.json`. `Members` includes all three. The drill reads retained setup from restored SQLite and requires these members independently of the recipe, preserving native calendar count and object checks.

## Verification
- `go test -v ./internal/backup/...` covers decoded seal/open checks, malformed recipes,
  subprocess lock contention/exit, scratch cleanup and the synthetic v0.5.0 pairing fixture
  in `testdata/pairing-v050.json`. The fixture uses a 32-byte 0x01 deployment key and retains
  no recovery private key.

## Child DOX Index
None.

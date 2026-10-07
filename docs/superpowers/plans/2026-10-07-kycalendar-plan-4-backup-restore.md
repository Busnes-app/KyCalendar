# KyCalendar Plan 4: backup and restore backend

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the inherited KyRecovery backup a KyCalendar backup: the right service name, database file, env names, calendar-aware drill, step-up unpair, and a restore that leaves no pre-backup credential or sync token usable.

**Architecture:** The lib (`ky-primitives/recoveryclient` v0.9.0) already seals, deposits, schedules, drills and restores. This plan changes only the product adapter (`internal/backup`), config, the `restore` command, one store method and the docs. No lib code is copied.

**Tech Stack:** Go 1.x, `modernc.org/sqlite`, `ky-primitives/recoveryclient`, `go-ical`.

**Spec:** `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md` §4 "Backup and restore" and "Testing" item 2. The binding contract is root `AGENTS.md` "KyRecovery integration".

## Global Constraints

- Service name sealed and claimed: `kycalendar` (matches `[A-Za-z0-9][A-Za-z0-9_.-]{0,63}`). `KY_APP_NAME` stays the display name only.
- Env names: `KYCALENDAR_BACKUP_DIR`, `KYCALENDAR_BACKUP_KEEP`, `KYCALENDAR_BACKUP_DEPOSIT_INTERVAL`, `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY` (default off), `KYCALENDAR_DNS`. Other `KY_*` runtime variables (`KY_PORT`, `KY_DB_DRIVER`, `KY_DB_DSN`, …) keep their names.
- Shares are read from stdin only, never argv.
- Logs and audit rows carry IDs only, never tokens, passwords or event content.
- Every Go change passes `go test ./...` on SQLite; anything touching `internal/store` also passes against PostgreSQL in CI.
- Never edit existing migrations.

## Review Focus

1. A capsule restored with the documented steps must start the server on the restored data, not an empty database that then bootstraps a fresh admin. Task 1 pins the member name to the DSN file.
2. A restore that dies after extraction but before the reset must not leave a startable database that still trusts old app passwords. Task 5 makes the reset a separate, idempotent command and has `restore` tell the operator to rerun it.
3. Old `KY_BACKUP_*` names left in an operator's `.env` must not silently switch backups off. Task 2 refuses to start and names the new variable.
4. A drill on an empty calendar database (zero calendars) must pass, not divide by zero or fail "no sample". Task 3 tests it.
5. A sync token issued after the backup but carrying a higher `seq` must be refused after restore, even once new writes pass that `seq`. Task 5 tests it.

---

### Task 1: Service name and database member

**Files:**
- Modify: `internal/backup/payload.go`, `internal/backup/drill.go`, `internal/api/backup_handlers.go` (claim at ~226, local copies at ~532), `internal/backup/settings.go` (`RunConfig.AppName` at ~47), `cmd/server/main.go` (`runRestore` default service)
- Test: `internal/backup/payload_test.go`, `internal/backup/drill_test.go`, `internal/backup/settings_test.go`, `internal/api/backup_test.go`, `cmd/server/restore_test.go`

**Interfaces:**
- Produces: `backup.ServiceName = "kycalendar"` and `backup.DatabaseMember = "data/kycalendar.db"`, used by Tasks 3 and 5.

The default DSN (`internal/config/config.go:130`) opens `<DataDir>/kycalendar.db`, but the capsule carries `data/ky_server.db`. A restored tree therefore starts an empty database. The member must be the file the server opens.

- [ ] **Step 1: Write the failing tests**

In `payload_test.go`:

```go
func TestCapsuleCarriesTheDatabaseTheServerOpens(t *testing.T) {
	cfg := testConfig(t) // existing helper; sqlite under t.TempDir()
	p, err := Collect(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.ServiceName != "kycalendar" {
		t.Fatalf("service = %q, want kycalendar", p.ServiceName)
	}
	want := "data/" + filepath.Base(strings.SplitN(cfg.Database.DSN, "?", 2)[0])
	if want != DatabaseMember {
		t.Fatalf("DSN file %q != member %q", want, DatabaseMember)
	}
	var found bool
	for _, f := range p.Files {
		found = found || f.Path == DatabaseMember
	}
	if !found {
		t.Fatalf("payload lacks %s", DatabaseMember)
	}
}
```

If `testConfig` builds its own DSN, build it with `config.LoadFromEnv` under `t.Setenv("KY_DATA_DIR", dir)` instead, so the test compares against the real default. Update every existing test literal `data/ky_server.db` to `DatabaseMember`, and every `Busnes.app`/`busnes_app` service expectation to `ServiceName`.

In `internal/api/backup_test.go`, assert that the pairing claim request body carries `"service_name":"kycalendar"` and that `app_name` is still the configured display name. Extend the existing fake KyRecovery server's claim handler to record the body.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/backup/... ./internal/api/... ./cmd/server/...`
Expected: FAIL on the service name and member.

- [ ] **Step 3: Implement**

In `payload.go`:

```go
// ServiceName is what KyRecovery pins for this product's token and every capsule's manifest.
// KY_APP_NAME is the display name only.
const ServiceName = "kycalendar"

// DatabaseMember is the capsule path of the SQLite database: the file the default DSN opens
// under <DataDir>, so a restored tree starts on the restored data.
const DatabaseMember = "data/kycalendar.db"
```

Use `DatabaseMember` wherever `"data/ky_server.db"` appears in `payload.go`, `drill.go` and `Members`. Name the snapshot temp file `kycalendar.db`. Set `ServiceName: ServiceName`.

- Pairing: `ClaimPairing(ctx, url, code, backup.ServiceName, s.config.Server.AppName)`. Check the lib's argument order in `recoveryclient` and keep `service_name` first as the lib defines it.
- `RunConfig.AppName` (settings.go) and `ListLocalCopies(dir, …)`: `backup.ServiceName`.
- `runRestore`: the `-service` default becomes `backup.ServiceName`. Drop the `KY_APP_NAME` fallback and update the usage text to say `(default: kycalendar)`.

- [ ] **Step 4: Run and see them pass**

Run: `go test ./internal/backup/... ./internal/api/... ./cmd/server/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal cmd
git commit -m "fix(backup): seal as kycalendar and carry the database the server opens"
```

---

### Task 2: Product-prefixed backup environment

**Files:**
- Modify: `internal/config/config.go:156-249`, `docker-compose.yml:44-50`, `docker-compose.lan-dns.yml`, `Dockerfile:27`, `internal/api/backup_handlers.go` (hint strings at ~30, ~324), `README.md` (backup sections)
- Test: `internal/config/config_test.go`, `internal/api/backup_test.go` (~136, ~392)

- [ ] **Step 1: Write the failing tests**

```go
func TestBackupEnvUsesProductPrefix(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KYCALENDAR_BACKUP_DIR", "/backups")
	t.Setenv("KYCALENDAR_BACKUP_KEEP", "3")
	t.Setenv("KYCALENDAR_BACKUP_DEPOSIT_INTERVAL", "1h")
	t.Setenv("KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY", "true")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backup
	if b.Dir != "/backups" || b.Keep != 3 || b.DepositInterval != time.Hour || !b.AllowPrivateRecovery {
		t.Fatalf("backup config = %+v", b)
	}
}

func TestRetiredBackupEnvIsRefused(t *testing.T) {
	for _, name := range []string{"KY_BACKUP_DIR", "KY_BACKUP_KEEP", "KY_BACKUP_DEPOSIT_INTERVAL", "KY_BACKUP_ALLOW_PRIVATE_RECOVERY"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv(name, "1")
			_, err := LoadFromEnv()
			want := "KYCALENDAR_" + strings.TrimPrefix(name, "KY_")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to name %s", err, want)
			}
		})
	}
}
```

Match the field names to the real `config.Backup` struct. Rename the existing `KY_BACKUP_*` tests to the new names.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/config/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

At the top of `LoadFromEnv`, before anything else is read:

```go
// The backup variables carry the product prefix (root AGENTS.md); a retired name left in
// .env would otherwise switch backups off without a word.
for _, old := range []string{"KY_BACKUP_DIR", "KY_BACKUP_KEEP", "KY_BACKUP_DEPOSIT_INTERVAL", "KY_BACKUP_ALLOW_PRIVATE_RECOVERY"} {
	if _, set := os.LookupEnv(old); set {
		return nil, fmt.Errorf("%s was renamed to KYCALENDAR_%s", old, strings.TrimPrefix(old, "KY_"))
	}
}
```

Rename the four reads and their error messages to `KYCALENDAR_BACKUP_*`. Do the same in:
- `docker-compose.yml`
- `Dockerfile`
- the handler hint strings
- `docker-compose.lan-dns.yml`: every `KY_DNS` becomes `KYCALENDAR_DNS`, and the snippet's `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` becomes `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY`
- `README.md`: every backup variable and the DNS blocks. Add one line telling operators that the old names are refused at startup and which names replace them.

Grep afterwards: `grep -rn 'KY_BACKUP_\|KY_DNS' --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=docs .` must print only the retired-name guard and its test. Docs under `docs/` are handled in Task 6.

- [ ] **Step 4: Run and see them pass**

Run: `go test ./internal/config/... ./internal/api/... && make smoke KY_SMOKE_PORT=28931` (the smoke script sets no backup variables; confirm it still starts).
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A internal Dockerfile docker-compose.yml docker-compose.lan-dns.yml README.md
git commit -m "feat(config): KYCALENDAR_ backup and DNS variables; refuse the retired names"
```

---

### Task 3: Calendar-aware drill

**Files:**
- Modify: `internal/backup/payload.go` (record counts), `internal/backup/drill.go` (check them, parse a sample)
- Test: `internal/backup/drill_test.go`, `internal/backup/payload_test.go`

**Interfaces:**
- Consumes: `DatabaseMember` (Task 1); `calendar.Inspect(*ical.Calendar) (calendar.Object, error)`.
- Produces: recipe keys `calendar_count` and `object_count` (JSON numbers).

The counts are taken from the snapshot file itself, not the live database, so a write between snapshot and count cannot cause a false failure. They travel in the authenticated recipe.

- [ ] **Step 1: Write the failing tests**

In `drill_test.go`, using the existing fixture style:
- A database with 2 calendars and 3 valid objects, whose recipe says 2 and 3, passes. The check names are `Calendar Counts` and `Calendar Objects`.
- The same database with a recipe saying 2 and 4 fails `Calendar Counts` with a message naming both numbers.
- A recipe without `calendar_count` fails `Verification Recipe` with "calendar_count must be a non-negative integer".
- An object row whose `data` is `BEGIN:VCALENDAR\r\nBROKEN` fails `Calendar Objects` and names the calendar ID and object name, never the data.
- An empty calendar database (0 and 0) passes both checks. The `Calendar Objects` message is "No objects to parse".

In `payload_test.go`, after inserting 1 calendar and 2 objects into the live test database, `Collect`'s recipe holds `calendar_count == 1` and `object_count == 2`.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/backup/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `snapshotSQLite`, after `SQLiteSnapshot` and before reading the bytes, open the snapshot read-only (`file:<path>?mode=ro`, the same DSN form `sqliteIntegrityCheck` uses) and count:

```go
type calendarCounts struct{ Calendars, Objects int64 }

func countCalendars(ctx context.Context, db *sql.DB) (calendarCounts, error) {
	var c calendarCounts
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendars`).Scan(&c.Calendars); err != nil {
		return c, err
	}
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendar_objects`).Scan(&c.Objects)
	return c, err
}
```

Return the counts with the bytes and record them in the recipe as `"calendar_count"` and `"object_count"`.

In `Checks`, read both keys strictly: JSON decodes numbers as `float64`, and in-memory fixtures may hold `int64`/`int`. Anything negative, fractional or missing is a recipe failure. After the integrity check, append `calendarChecks(full, wantCalendars, wantObjects)`. It opens the restored database read-only, compares the two counts, then parses at most 50 objects:

```go
rows, err := db.Query(`SELECT calendar_id, name, data FROM calendar_objects ORDER BY calendar_id, name LIMIT 50`)
// for each: ical.NewDecoder(bytes.NewReader(data)).Decode(), then calendar.Inspect(cal).
// The first failure becomes Check{Name: "Calendar Objects", Message: "Cannot parse <calendar_id>/<name>"}.
```

Success reads `Parsed N of M objects`.

- [ ] **Step 4: Run and see them pass**

Run: `go test ./internal/backup/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backup
git commit -m "feat(backup): drill compares calendar counts and parses stored objects"
```

---

### Task 4: Step-up unpair

**Files:**
- Modify: `internal/api/backup_handlers.go` (`handleUnpair` ~360), `web/src/pages/Backup.tsx` (the unpair action)
- Test: `internal/api/backup_test.go`, the Backup page's vitest file

Root `AGENTS.md` requires the Unpair action to be "step-up, audited". It is audited but not step-up. Use the existing rule from `handleDeleteGroupCalendar` (`internal/api/group_calendars.go:121-130`): the session must be younger than `stepUpWindow`, otherwise 403 `{"code":"reauth_required"}`.

- [ ] **Step 1: Write the failing tests**

- An admin whose session `CreatedAt` is older than `stepUpWindow` gets 403 with `code == "reauth_required"`, and the pairing still exists.
- A fresh session unpairs: 200, `paired:false`, and the key pin is kept (the existing `TestUnpairKeepsPin` with a fresh session).
- vitest: a 403 `reauth_required` on unpair shows "Sign in again to unpair" and does not show the page as unpaired.

Reuse whatever helper `group_calendars_test.go` uses to age a session.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/api/ -run Unpair && (cd web && npx vitest run src/pages)`
Expected: FAIL.

- [ ] **Step 3: Implement**

At the top of `handleUnpair`, after the method check:

```go
_, sess, err := s.sessions.AuthenticateRequest(r)
if err != nil {
	s.writeError(w, http.StatusUnauthorized, "Authentication required")
	return
}
if time.Since(sess.CreatedAt) > stepUpWindow {
	s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign in again to unpair", "code": "reauth_required"})
	return
}
```

In `Backup.tsx`, show the server's `error` text for a 403 `reauth_required`. The page's existing error display is enough; do not add a re-login flow. Rebuild `web/dist` and commit it with the source.

- [ ] **Step 4: Run and see them pass**

Run: `go test ./internal/api/... && (cd web && npm test && npm run build)`
Expected: PASS, and the authz matrix still passes.

- [ ] **Step 5: Commit**

```bash
git add internal/api web
git commit -m "feat(backup): unpairing requires a recent sign-in"
```

---

### Task 5: Restore revokes credentials and starts a new sync epoch

**Files:**
- Modify: `internal/store/store.go` (interface), `internal/store/sqlstore.go` or `calendars.go` (implementation), `cmd/server/main.go` (`runRestore`, new `restore-reset` subcommand)
- Create: `cmd/server/restorereset.go`
- Test: `internal/store/store_test.go` (SQLite, and Postgres via `testdb`), `cmd/server/restore_test.go`, `internal/api/dav_test.go`

**Interfaces:**
- Consumes: `backup.DatabaseMember`, `backup.ServiceName` (Task 1).
- Produces: `Store.ResetAfterRestore(ctx context.Context) error` and `resetRestored(ctx context.Context, dataDir string) error` in `cmd/server`.

A restored database still holds the app-password hashes and sessions that were live at backup time, and its old `sync_epoch`. Once new writes push a calendar's `seq` past an old token's value, a client holding a post-backup token would be believed. It would then keep events the restore removed.

- [ ] **Step 1: Write the failing tests**

Store (run for every driver `testdb` provides):

```go
func TestResetAfterRestore(t *testing.T) {
	st := testdb.Open(t) // existing helper
	ctx := context.Background()
	// seed: a user with a session, an MFA challenge and an app password
	before, _ := st.Calendars().SyncEpoch(ctx)
	if err := st.ResetAfterRestore(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := st.Calendars().SyncEpoch(ctx)
	if after == before || len(after) != 16 {
		t.Fatalf("epoch %q -> %q", before, after)
	}
	// assert zero sessions, zero app passwords, zero mfa_challenges, and an audit row "system.restore_reset"
	if err := st.ResetAfterRestore(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
}
```

DAV (`internal/api/dav_test.go`, next to the other-epoch test at ~142):
1. Issue a sync token and write until `seq` is past it.
2. Call `ResetAfterRestore` and recreate the app password.
3. The old token gets 403 `valid-sync-token`.

`cmd/server/restore_test.go`:
1. Seal a fixture whose database has a session, an app password and a known epoch, using the same fixture helper the existing test uses with a real SQLite database at `DatabaseMember`.
2. Run `restore`, then open `<to>/data/kycalendar.db`.
3. Assert there are no sessions or app passwords and the epoch has changed.
4. Separately, `resetRestored` on a directory with no database fails with an error naming the path.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/store/... ./internal/api/... ./cmd/server/...`
Expected: FAIL (method undefined).

- [ ] **Step 3: Implement**

`store.go`, on `Store`:

```go
// ResetAfterRestore ends every session, MFA challenge and app password and writes a new
// sync epoch, in one transaction, so nothing issued after the backup is believed.
ResetAfterRestore(ctx context.Context) error
```

Implementation (one `BeginTx`, dialect via the store's existing `q()` placeholder helper):

```sql
DELETE FROM sessions;
DELETE FROM mfa_challenges;
DELETE FROM app_passwords;
UPDATE calendar_meta SET value = ? WHERE key = 'sync_epoch';   -- crypto.RandomHex(8)
INSERT INTO audit_log (...) VALUES (... 'system.restore_reset' ...); -- match the audit table's columns
```

`cmd/server/restorereset.go`:

```go
// resetRestored opens the restored database under dataDir and runs ResetAfterRestore. It is
// idempotent, so an interrupted restore is finished by running it again.
func resetRestored(ctx context.Context, dataDir string) error {
	path := filepath.Join(dataDir, filepath.Base(backup.DatabaseMember))
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no restored database at %s: %w", path, err)
	}
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DataDir: dataDir, DSN: sqliteDSN(path)})
	if err != nil {
		return err
	}
	defer st.Close()
	return st.ResetAfterRestore(ctx)
}
```

`sqliteDSN` reuses the pragma string from `config.go:130`. Factor it into an exported `config.SQLiteDSN(path string) string` and use it in both places. Do not call `config.LoadFromEnv` here: it mints a key, as the existing comment in `runRestore` warns.

In `runRestore`, after `restore(...)` succeeds:

```go
if err := resetRestored(context.Background(), filepath.Join(*target, "data")); err != nil {
	log.Fatalf("Restored, but the reset failed: %v\nDo not start the server on %s. Finish with: kycalendar restore-reset -to %s", err, *target, *target)
}
fmt.Println("✓ Sessions, MFA challenges and app passwords revoked; new sync epoch written. Every user creates new app passwords; CalDAV clients resync.")
```

Add `case "restore-reset":` with `-to <dir>`, calling `resetRestored`.

- [ ] **Step 4: Run and see them pass**

Run: `go test ./internal/store/... ./internal/api/... ./cmd/server/...`, then `make test-postgres` if Postgres is available. CI runs it otherwise.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal cmd
git commit -m "feat(restore): revoke credentials and start a new sync epoch after restore"
```

---

### Task 6: Docs

**Files:**
- Modify: `docs/RESTORE.md`, `README.md` (disaster recovery), root `AGENTS.md`, `internal/backup/AGENTS.md`, `internal/config/AGENTS.md`, `internal/api/AGENTS.md`, `internal/store/AGENTS.md`

- [ ] **Step 1: RESTORE.md for KyCalendar**

- Title: "Restoring KyCalendar".
- Capsule table: `data/kycalendar.db` holds the users, groups, calendars and events, app-password hashes, sessions, audit log, settings and the sealed KyRecovery token. Keep the existing rows for `config/settings.json`, `data/encryption.key` and `data/recovery.pub`.
- Service name `kycalendar` everywhere `Busnes.app`/`KY_APP_NAME` appears, including the example output.
- Replace the manual `DELETE FROM sessions` step with what `restore` now does automatically. Add `kycalendar restore-reset -to <dir>` for an interrupted restore. Add the user-visible consequence: every user creates new app passwords, and CalDAV clients resync from scratch.
- `KYCALENDAR_*` variable names.

- [ ] **Step 2: AGENTS.md pass**

- Root `AGENTS.md`: add `#### Plan 4 backup and restore contracts` after the Plan 3b section:
  - service `kycalendar`
  - capsule database member equals the default DSN file
  - `KYCALENDAR_*` names, with retired names refused
  - the drill checks counts and parses up to 50 objects
  - unpair is step-up
  - `restore` runs `ResetAfterRestore`, and `restore-reset` finishes an interrupted one
  - the interop gate (spec Testing 6) remains open, for the operator with real devices
- Update the child docs named above where they name `KY_BACKUP_*`, `KY_DNS`, the service name or the database member.

- [ ] **Step 3: Verify and commit**

Run: `KY_SMOKE_PORT=28931 make ci` (one run; nothing else in parallel).
Expected: `==> Local CI checks passed`.

```bash
git add -A docs README.md AGENTS.md internal
git commit -m "docs: KyCalendar restore runbook and Plan 4 contracts"
```

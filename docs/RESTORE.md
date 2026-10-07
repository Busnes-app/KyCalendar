# Restoring KyCalendar from a capsule

This is the procedure for bringing KyCalendar back from a `.kycap` backup after the original
is gone. It needs three things, held by three different parties by
design:

| Thing | Who has it |
|---|---|
| The capsule (`.kycap`) | KyRecovery, or the local backup directory, or a downloaded copy |
| k custodian cards | The custodians from the suite ceremony (k is usually 2 of 3) |
| A machine to restore on | You |

Nobody can do this alone. KyRecovery cannot open a capsule. One custodian cannot. The server
that made the backup never could. That is the point, and it is also why you should run this
procedure once as a drill before you ever need it.

The `docker compose` commands below use the base file alone, which runs the published
image. Source install: confirm the `COMPOSE_FILE` line in `.env` contains `docker-compose.build.yml`
before the first command; extra overlays beside it, such as `docker-compose.lan-dns.yml`, are
fine (the quickstart in `README.md` adds it). Check: `grep '^COMPOSE_FILE=' .env | grep -q
docker-compose.build.yml && echo ok`. Otherwise a restore silently pulls a different binary than
the one you built and are running.
Published install: never restore onto a floating `:latest`; the step before the restore
command pins and verifies a digest.

## What a capsule holds

Everything a fresh server needs to be the old one:

| Path in the capsule | What it is |
|---|---|
| `data/kycalendar.db` | The whole database: users, groups, calendars and events, app-password hashes, sessions, MFA state, audit log, settings, the sealed KyRecovery token |
| `data/encryption.key` | 32 bytes. Every TOTP secret and the KyRecovery pairing token are encrypted under it |
| `data/recovery.pub` | The suite recovery public key, so the restored server comes back pinned (present when the backup had a key) |
| `config/settings.json` | App name, URL, port, database driver. For your reference when re-deploying; nothing reads it |

The restored directory is the live directory in the clear. Treat it like the running server's
`data/`.

**This procedure is for SQLite deployments.** A capsule carries `data/kycalendar.db` because the
collector snapshots SQLite with `VACUUM INTO`; on `KY_DB_DRIVER=postgres` no snapshot is
possible, so no capsule is made at all and there is nothing here to restore from. Back a
Postgres deployment up with `pg_dump` on its own schedule, guard that dump as the plaintext of
everything above, and copy `data/encryption.key` and `data/recovery.pub` separately — the
recovery key pin, the pairing and the schedule are rows in the database and come back with the
dump, but nothing in it can be decrypted without `encryption.key`.

Backups require the default database path, `<KY_DATA_DIR>/kycalendar.db`: the capsule restores
the database there, so a backup refuses while `KY_DB_DSN` opens another file. Unset
`KY_DB_DSN` on the restored server, or point it at `<KY_DATA_DIR>/kycalendar.db`.

## Before you start

- **Pick the capsule.** In the KyRecovery dashboard, open Capsules, find the newest one for
  this service (`kycalendar`) that is not flagged
  corrupt, and note its `capsule_id`, `created_at` and `digest`. You will compare these after
  the restore. From a local backup directory the file is `<service>.<capsule-id>.kycap`
  (`kycalendar.cap-kycalendar-<n>.kycap`); the newest is the one to use unless
  you have a reason.
- **Install the `sqlite3` CLI on the host.** The image does not carry it, and the password
  steps below query the restored and old databases with it.
- **Gather k custodians.** Each card carries one share, a single line beginning `ky2-`. They
  type or paste it themselves; do not collect the shares in a file, a chat, or an email. Two
  shares in one place is the suite key in one place.
- **Prepare an empty directory** on a machine you trust, ideally the one that will run the
  restored server. The restore refuses a directory that is not empty.

## Step 1: open the capsule

With the binary (from a release, or `go build ./cmd/server`):

```bash
kycalendar restore -capsule kycalendar.cap-kycalendar-XXXXXXXX.kycap -to ./restored
```

`-service` defaults to `kycalendar`, the name every KyCalendar capsule is sealed under; the
capsule's service name must match or the restore stops before reading a share.

For a published-image install, and always on a fresh recovery machine, pin the commit you
intend to run (normally the one that made the backup, or the current tip) to a digest you have
verified before it reads a single share (`gh` must be logged in). Name the commit yourself.
Tags are movable, `:<commit sha>` included, so the chain also checks that the attestation records
your commit as its source: the guarantee is the commit you named, not whatever the tag points at. The
chain stops at the first failure and renames a same-directory staging file over `.env` only
if the filtered copy was written in full, so your secrets are never truncated. The pin persists
in `.env` after the drill: see the README's upgrade note for moving off it. Images built before 2026-09-16 can no longer be verified by name: the owner they were attested under is not held by this project, so do not point `--repo` or `--cert-identity` at it. Pin a commit built after that date, or build that commit from source with `docker-compose.build.yml`.

```bash
sha=<full commit sha you intend to run, e.g. $(git rev-parse origin/main)>
d=$(docker buildx imagetools inspect ghcr.io/busnes-app/kycalendar:$sha --format '{{.Manifest.Digest}}') \
  && gh attestation verify "oci://ghcr.io/busnes-app/kycalendar@$d" --repo Busnes-app/KyCalendar \
       --cert-identity https://github.com/Busnes-app/KyCalendar/.github/workflows/ci.yml@refs/heads/main \
  && [ "$(gh attestation verify "oci://ghcr.io/busnes-app/kycalendar@$d" --repo Busnes-app/KyCalendar \
       --cert-identity https://github.com/Busnes-app/KyCalendar/.github/workflows/ci.yml@refs/heads/main \
       --format json --jq '.[0].verificationResult.statement.predicate.buildDefinition.resolvedDependencies[0].digest.gitCommit')" = "$sha" ] \
  && (umask 077; t=$(mktemp ./.env.XXXXXX) && touch .env && { grep -v '^KY_IMAGE=' .env || [ $? -eq 1 ]; } > "$t" \
      && echo "KY_IMAGE=ghcr.io/busnes-app/kycalendar@$d" >> "$t" && mv "$t" .env) \
  && grep -qxF "KY_IMAGE=ghcr.io/busnes-app/kycalendar@$d" .env
```

Then, in the same shell (the check compares against `$d`), refuse to go on unless the image in
effect is exactly that digest. A source install passes on its `kycalendar:local` build instead,
since `docker-compose.build.yml` wins over the pin, which is what a source install wants. The
two refusal messages are distinct on purpose: a broken invocation is not an unpinned image.

```bash
imgs=$(docker compose config --images) || { echo 'refusing: compose could not resolve the image'; false; }
printf '%s\n' "$imgs" | grep -qxF "ghcr.io/busnes-app/kycalendar@$d" || printf '%s\n' "$imgs" | grep -qxF 'kycalendar:local' \
  || { echo "refusing: image in effect is '$imgs', not the digest verified above"; false; }
```

With Docker Compose, from the repository directory, mount the capsule and an empty target
directory into a one-off container. Create the target yourself at mode 700 and run the
container as your own user, so what comes out is owned by you and not by root. The image's
entrypoint is the binary, so the subcommand goes straight after the service name; `--no-deps`
keeps the real server down:

```bash
mkdir -m 700 restored
docker compose run --rm --no-deps --user "$(id -u):$(id -g)" \
  -v "$PWD/kycalendar.cap-kycalendar-XXXXXXXX.kycap:/in.kycap:ro" \
  -v "$PWD/restored:/restored" \
  app restore -capsule /in.kycap -to /restored
```

The bare binary needs none of this: it creates a missing target itself at mode 700.

The command prompts:

```
Paste custodian shares, one per line, then Ctrl-D:
```

Each custodian enters their share on its own line. After the k-th, press Ctrl-D. Shares are
read from stdin only, never from the command line, because argv is world-readable and lands
in shell history.

Only for a rehearsal with synthetic test shares, never with real cards, stdin can be a file.
Delete it afterwards; a file holding k shares is the suite key in a file.

On success it prints the authenticated manifest:

```
Restored 4 files from capsule cap-kycalendar-1788605720094118543
  service:      kycalendar (v1.0.0)
  created:      2026-09-05T12:15:20Z
  recovery key: 886ff52c...
  payload hash: 8a053985...
```

Then it resets the restored database in one transaction, with no further input: sessions,
MFA challenges and app passwords are deleted, a new sync epoch is written and
`system.restore_reset` is audited. Password hashes are left as the backup had them; Step 3
deals with them. It ends with:

```
✓ Sessions, MFA challenges and app passwords revoked; new sync epoch written. Every user creates new app passwords; CalDAV clients resync.
```

If the reset fails, the files are in place but the credentials are still the backup's. The
command exits non-zero with `Do not start the server on <dir>. Finish with: kycalendar
restore-reset -to <dir>`. Run that command (in the same one-off container, with the same
mounts); it is idempotent, so it also finishes a restore that died halfway. It refuses a
directory with no restored database.

**Check it against KyRecovery's record.** The capsule ID and `created` must match the
deposit record you noted. Opening has already proved the bytes are intact and were sealed to
the suite key; matching the ID and time against the blind store's record is what proves this
is the capsule you meant, not an older one someone substituted.

Failures you may see, and what they mean:

| Message | Meaning |
|---|---|
| `capsule is for service "X", this instance is "kycalendar"` | The capsule belongs to another product, or `-service` names something else |
| `shamir: fewer shares than the threshold requires` | Fewer than k valid lines were read. Check for a missed line or a truncated paste |
| `restore target directory is not empty` | Use an empty directory. The restore never overwrites |
| `restore target ... contains '?' or '#'` | The database path cannot carry those characters. Choose a target without them |
| a decrypt or integrity error | Wrong shares (from a different ceremony), a share mistyped, or a damaged file. Re-download and retry with the custodians |

## Step 2: check what came out

```bash
find restored -type f -printf '%m %p\n'
```

Expect three or four files, all mode `600`, under `restored/data` and `restored/config`.
`cat restored/config/settings.json` shows the app URL and port the old server ran with.

## Step 3: put it in service

**Docker Compose (the normal deployment).** The data directory is the bind mount `./data`
in the compose project. It must be empty before the copy, for the same reason Step 1 demands
an empty directory: a capsule carries `kycalendar.db` but never its `-wal` and `-shm`
sidecars, and a write-ahead log left over from the old database would be replayed into the
restored one at first open, mixing two databases.

```bash
docker compose down
ls -A data | wc -l
```

That must print `0`. If it does not, the old directory still holds data, and you keep a copy
of it before anything else: it holds every change made after the capsule was sealed, and it
is the only record Steps 3 and 5 can walk. The container runs as root, so the files are
root-owned; copy as root into a directory you create at mode 700:

```bash
mkdir -m 700 old-data
sudo cp -a data/. old-data/ && sudo ls -A old-data | wc -l
```

The count must equal the count above and the command must exit 0. `old-data/` is now the
old live directory in the clear, with the same key the capsule holds; it is removed in
"Afterwards", not before Step 5 is done. If `data/` was already empty (a new machine, or the
old disk is gone), there is no old audit log; the password step below says what to do.

Only with the copy confirmed, empty the directory. This is irreversible:

```bash
sudo rm -rf data/* data/.[!.]*
ls -A data | wc -l
```

With `0` confirmed, copy the restored files in. Do not start the server yet:

```bash
sudo cp -a restored/data/. data/ && sudo chmod 600 data/*
```

**Reset rotated local passwords before the server starts.** A restore brings back every local
password hash as it was at the backup, so a password that leaked and was rotated after the
capsule works again the moment the service listens. Find the accounts first, with the stack
still down.

If you kept `old-data/`, its audit log names every local password changed after the capsule:
the `auth.password_changed` rows newer than `created_at`. Match them to the restored users by
ID, and add every restored local account the old server no longer had: it was deleted after
the capsule, and the restore brought it back with its old password. The log stores UTC times
as `2026-09-05 12:15:20...`, so write the capsule's `created` with a space, not a `T`, and
without the `Z`:

```bash
sudo sqlite3 data/kycalendar.db "ATTACH 'old-data/kycalendar.db' AS old; SELECT u.username, u.role FROM users u WHERE u.sso_provider = 'local' AND (u.id IN (SELECT user_id FROM old.audit_records WHERE action = 'auth.password_changed' AND created_at > '2026-09-05 12:15:20') OR u.id NOT IN (SELECT id FROM old.users)) ORDER BY u.role = 'admin' DESC, u.username;"
```

Reset every account in the list; an account deleted after the capsule should then be deleted
or disabled again (step 5 re-applies what happened since).

If the old audit log is unavailable, you cannot know which passwords changed: reset every
local account, administrators first. List them from the restored database:

```bash
sudo sqlite3 data/kycalendar.db "SELECT username, role FROM users WHERE sso_provider = 'local' ORDER BY role = 'admin' DESC, username;"
```

For every account in the list, set a temporary password. `reset-password` reads it from
stdin, keeps the account's role and status, revokes its sessions, MFA challenges and app
passwords, and forces a change at the next sign-in. `run --rm -T` starts a one-off container
on the same `./data` with no published port, so nothing is reachable while you do this; `-T`
lets the pipe reach stdin:

```bash
read -rs TEMP_PW   # typed, not echoed, not in history
printf '%s\n' "$TEMP_PW" | docker compose run --rm --no-deps -T app reset-password -username <name>
unset TEMP_PW
```

Hand each temporary password to the account owner out of band. Use `init-admin` only when no
administrator account exists at all: it makes the named account an administrator and takes
its password on the command line. SSO accounts sign in through KyIdentity and need nothing
here.

Then start:

```bash
docker compose up -d
```

Keep `KY_APP_URL` identical to the old deployment, from `config/settings.json`. The service
name is fixed at `kycalendar`; it is what every capsule is sealed under and what KyRecovery
pinned for the pairing token. `KY_APP_NAME` is display only.

The restored `encryption.key` is the key; the file form is the one to use. If the old
deployment supplied `KY_ENCRYPTION_KEY` by environment instead, the environment wins when
both are present, so either remove that variable so the file is read, or keep supplying the
same value from wherever the old deployment kept it. Never print a key to a terminal or type
one on a command line: it lands in scrollback, session recordings and shell history. If you
must produce the hex form, write it straight into the compose project's `.env` with
`umask 077` and nothing else on stdout.

**Bare binary.** Point `KY_DATA_DIR` at `restored/data`, set `KY_APP_URL` as before, run
the password resets above with the bare binary (`printf '%s\n' "$TEMP_PW" | kycalendar
reset-password -username <name>`), and only then start.

## Step 4: prove it

1. Open the app URL and sign in. Every session was revoked, so everyone signs in again. TOTP
   working proves `encryption.key` is right.
2. Open Backup & recovery. If the backup had a key, the recovery key shows as pinned with the
   same key ID as before; compare it with the ceremony card. If the backup was paired, the
   sealed token came across in the database, so the restored server can deposit again
   without re-pairing: click Back up now to prove it. If the screen says the key is missing,
   `data/recovery.pub` did not come across; re-pair, which is refused unless KyRecovery hands
   back the same key.
3. Check the audit log: the last events before the restore are there, followed by your
   sign-in.

## Step 5: decide what to trust

The restore proves the service works. It does not make the restored state current or safe.
Everything comes back as of the capsule's `created_at`: users, passwords, MFA enrolments,
SCIM state, calendars and events. Anything you revoked or changed after that moment is undone.
`restore` has already revoked every session, MFA challenge and app password and started a
new sync epoch. It does not touch passwords: **a restore brings back every local password
hash as it was at the backup**, so a password that leaked and was rotated after the capsule
would work again; Step 3 reset those before the service opened.

1. Tell users what changes:
   - Everyone signs in again; every user creates new app passwords under Phones & apps, and
     CalDAV clients resync from scratch (the epoch changes the calendar CTag).
   - Local passwords changed after the backup were reset in Step 3; their owners sign in with
     the temporary password and choose a new one. SSO accounts are unaffected.
   - Recovery codes come back as they were at backup time. A user who used or regenerated
     recovery codes after the backup should regenerate them.
   - Events created after the backup are gone unless a client still holds them; the old audit
     log in step 2 shows what happened since.
   - The interop and real-device client checks remain an operator step: reconnect one
     iPhone, Android and Thunderbird client and confirm the calendars sync.
2. Walk the old audit log (`old-data/kycalendar.db`, when you kept it) from `created_at` to
   the moment the old server was lost (the restored server's log stops at `created_at`), and
   re-apply what else happened after the capsule: disabled accounts, reset MFA, SCIM changes.
3. If the reason for the restore was a suspected compromise rather than hardware loss, treat
   the restored secrets as exposed and rotate the ones that can be rotated. A restore from
   before a compromise brings the attacker's access back with the service unless you do this.

   **Never rotate `encryption.key`.** Every TOTP secret and the KyRecovery pairing token are
   encrypted under it. Remove it and every user's second factor and the pairing are gone for
   good, on a server you just recovered.

   What can be rotated, and how:

   - `KY_SESSION_SECRET` signs the proof-of-work login challenge, nothing durable. Replace it
     with `openssl rand -hex 32` written straight into `.env`, not echoed, then
     `docker compose up -d`.
   - `KY_SCIM_TOKEN` is the SCIM bearer. Replace it the same way and give the new value to the
     identity provider. If it was never set, the server mints a fresh one at every start.
   - The KyRecovery pairing token: ask the KyRecovery admin to revoke this service and pair
     again from the screen; the same key comes back, so the pairing is accepted.

   Then have every admin re-enrol their second factor, and confirm with a Back up now so the
   recovered server has a capsule that reflects the rotation.

## Afterwards

- Delete the `restored/` directory once the server runs from its own copy, and `old-data/`
  once Step 5 is done. Both are the live directory in the clear, key included. Files in
  `old-data/` are root-owned after the copy, so `sudo rm -rf old-data`.
- The custodians' cards are unchanged; a restore does not consume them. If a card was
  exposed during the restore (read aloud, photographed, pasted anywhere shared), that is a
  key compromise for the whole suite, not for one server: run a new ceremony.
- Make a backup from the restored server so the newest capsule reflects the recovery.

## Drill it

Run Steps 1 and 2 against the latest capsule on a scratch machine once a quarter, with the
real custodians and their real cards, and then delete the output. The in-app drill proves the
capsule format restores; only this proves the cards do.

The in-app drill and `backup-drill` CLI validate the recipe from the capsule actually opened,
including required files, read-only SQLite integrity and environment-variable presence.
A malformed recipe fails the drill. The drill also compares the calendar and object counts
recorded at backup with the restored database and parses up to 50 stored objects (IDs only in
messages). Concurrent drills on one data directory are refused
(HTTP 409 or a CLI error); retry after the active drill finishes. The OS releases the lock
if the process exits. Keep `data/drill.lock` in place; it holds no secret and must not be
removed to bypass a running drill. Opened scratch data stays under `data/drill` with 0700
permissions and is removed when the drill returns.

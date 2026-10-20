# Recovery

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

## Disaster recovery

Every backup is one `.kycap` capsule: the database snapshot, the deployment's encryption key,
the settings that describe the deployment, and the pinned suite recovery public key. It is
sealed to the suite recovery key, which only the custodians' cards (k of n, split at the suite
ceremony) can reconstruct. Nothing on this server, and nothing on KyRecovery, can open one.
The mechanics are `github.com/Busnes-app/ky-primitives/recoveryclient`; this repository
supplies what it seals and how it checks a drill.

**Capsules are SQLite-only today.** The snapshot is `VACUUM INTO` against the local database
file; on `KY_DB_DRIVER=postgres` there is no snapshot and every backup refuses with "no
consistent database snapshot for this driver". A Postgres deployment must back its database up
itself, with `pg_dump` on its own schedule and its own retention, and must protect that dump:
it is the plaintext of everything a capsule would have sealed. Nothing travels in a capsule
there, because no capsule is made. The recovery key pin, the pairing and the schedule live in
the database and so ride in the `pg_dump`; `data/encryption.key` and `data/recovery.pub` do
not, and you must copy them separately. Without `encryption.key` no TOTP secret and no
KyRecovery token in that dump can be decrypted.

Backups also require the default database path: a capsule restores the database as
`<KY_DATA_DIR>/kycalendar.db`, so a backup refuses while `KY_DB_DSN` opens any other file.
Unset `KY_DB_DSN` or point it at `<KY_DATA_DIR>/kycalendar.db`.

The admin screen **Backup & recovery** shows four facts (recovery key, KyRecovery, local
copies, schedule) and the actions: Back up now, Download capsule, Run restore drill, the
schedule, pairing with Unpair, and pinning the key by hand.

### Two ways to get a key

- **Pair with KyRecovery.** Its dashboard issues a six-digit code; entering it here hands this
  server the suite public key and a deposit credential. The key is pinned once and never
  replaced: a later pairing that returns a different key is refused.
- **Pin the key by hand.** For a server with no KyRecovery: paste the base64 public key the
  ceremony page shows, with its k-of-n. Capsules then go only to the local directory.

### Why TLS matters here

The capsule is sealed, so a copy of it is worthless to an eavesdropper. What the wire does
carry is the suite public key at pairing (trust on first use), the deposit credential, and
each receipt. A man in the middle at pairing could substitute a key whose shares they hold,
so pairing over plain HTTP is refused outright, and a pairing across your own network should
be checked: compare the key ID on the screen with the ceremony card, or pin the key by hand
and skip the question.

### One run, every destination

Back up now, the schedule and the `deposit` command all do the same thing: seal one capsule
and deliver it to every configured destination. A pinned key with no destination is refused
with a message that says so. A local write that fails does not stop the deposit, and a
refused deposit does not remove the local copy.

### Environment

The server refuses a non-empty `KY_BACKUP_DIR`, `KY_BACKUP_KEEP`, `KY_BACKUP_DEPOSIT_INTERVAL` or `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` at startup, compose included; the replacements are the `KYCALENDAR_BACKUP_*` names below, and `KYCALENDAR_DNS` replaces `KY_DNS`. Re-running the LAN-DNS block below migrates its own old `.env` lines; rename any other `KY_BACKUP_*` lines in `.env` by hand.

| Variable | Default | Meaning |
|---|---|---|
| `KYCALENDAR_BACKUP_DIR` | empty (off) | Directory for sealed local copies, `<service>.<capsule-id>.kycap` at mode 0600 (`kycalendar.cap-kycalendar-<n>.kycap`). Pruning removes only this application's own prefix. |
| `KYCALENDAR_BACKUP_KEEP` | `7` | Local copies to retain; below 1 refuses startup. |
| `KYCALENDAR_BACKUP_DEPOSIT_INTERVAL` | `24h` | Default schedule only. The admin screen's setting wins; `0` is off; 15 minutes to 366 days otherwise. |
| `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY` | `false` | Admit a KyRecovery on an RFC1918 or CGNAT address behind your own TLS proxy. Loopback, link-local and other reserved ranges stay refused; HTTPS stays required. Logged at startup and on the pairing audit row. |
| `KYCALENDAR_DNS` | unset | Only in `docker-compose.lan-dns.yml`: the container's resolver, for names that exist only on your LAN. |

Reach a KyRecovery that only your LAN's DNS knows:

The snippet appends `docker-compose.lan-dns.yml` to whatever `COMPOSE_FILE` chain `.env` already
holds (build overlay, local override) and leaves the rest of the chain alone; the resolver and the private-recovery flag
sit next to it: the resolver comes from an exported
`KYCALENDAR_DNS` (`export KYCALENDAR_DNS=<addr>`; fish: `set -x KYCALENDAR_DNS <addr>`) or, when that is unset, from the `KYCALENDAR_DNS` line
already in `.env`; there is no default, the block refuses to guess. An exported value overrides
`.env`, so re-running is a no-op only while `KYCALENDAR_DNS` is unset in your shell; the flag is set to true. One block for every install type:

```bash
(umask 077; touch .env \
  && cf=$({ grep '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2-) && cf=${cf:-docker-compose.yml} \
  && dns=${KYCALENDAR_DNS:-$({ grep '^KYCALENDAR_DNS=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2-)} \
  && : "${dns:?no resolver chosen: export KYCALENDAR_DNS=<your LAN resolver> (fish: set -x KYCALENDAR_DNS <addr>), then re-run this block}" \
  && case ":$cf:" in *:docker-compose.lan-dns.yml:*) ;; *) cf="$cf:docker-compose.lan-dns.yml";; esac \
  && t=$(mktemp ./.env.XXXXXX) && { grep -v -e '^COMPOSE_FILE=' -e '^KYCALENDAR_DNS=' -e '^KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY=' -e '^KY_DNS=' -e '^KY_BACKUP_ALLOW_PRIVATE_RECOVERY=' .env || [ $? -eq 1 ]; } > "$t" \
  && printf 'COMPOSE_FILE=%s\nKYCALENDAR_DNS=%s\nKYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY=true\n' "$cf" "$dns" >> "$t" && mv "$t" .env)
docker compose up -d --force-recreate
docker inspect kycalendar --format '{{.HostConfig.Dns}}'   # must print the resolver you chose
```

Turning it off: remove the resolver and the flag, strip only `docker-compose.lan-dns.yml` from
`COMPOSE_FILE` (a build overlay or local override in the chain survives), and recreate:

```bash
(umask 077; t=$(mktemp ./.env.XXXXXX) && touch .env \
  && cf=$({ grep '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2- | tr ':' '\n' | grep -vx docker-compose.lan-dns.yml | paste -sd: -) \
  && { grep -v -e '^COMPOSE_FILE=' -e '^KYCALENDAR_DNS=' -e '^KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY=' -e '^KY_DNS=' -e '^KY_BACKUP_ALLOW_PRIVATE_RECOVERY=' .env || [ $? -eq 1 ]; } > "$t" \
  && { [ -z "$cf" ] || [ "$cf" = docker-compose.yml ] || printf 'COMPOSE_FILE=%s\n' "$cf" >> "$t"; } && mv "$t" .env)
docker compose up -d --force-recreate
```

`KYCALENDAR_DNS` takes effect only while `docker-compose.lan-dns.yml` is in `COMPOSE_FILE`, but
`KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY` persists in `.env` on its own and keeps relaxing destination checks until you
remove it.

### Upgrading from plaintext local backups

Earlier builds wrote unencrypted backups into the backup directory. The variable is now `KYCALENDAR_BACKUP_DIR` and
means sealed capsules. Retention deliberately never touches files it did not write, so old
plaintext backups stay where they are: move them out of the directory, keep them until a
restore from a capsule has been proven, then remove them securely. They are the live
directory in the clear.

### Upgrading to the `kycalendar` service name

Earlier builds claimed and sealed under `KY_APP_NAME`; the service name is now fixed at
`kycalendar`. An instance paired under another name must re-pair: the KyRecovery admin
revokes the old token, then pair again from the screen (the same key comes back, so it is
accepted). Local copies written under the old name are neither listed nor pruned; move them
out by hand. An explicitly empty `KY_BACKUP_DIR=` (local copies off) does not carry over:
compose now defaults to `/app/backups`, so set `KYCALENDAR_BACKUP_DIR=` in `.env` to keep
them off.

### Restoring

`docs/RESTORE.md` is the runbook: opening a capsule with the custodians' cards, putting the
result in service, and what to distrust afterwards. Drill it once a quarter with real cards.

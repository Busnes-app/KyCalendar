# Running

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

Published image (needs `docker-compose.yml` from this repository, no build):

```bash
mkdir -p -m 700 data backups && sudo chown 10001:10001 data backups   # the container's UID
docker compose up -d
```

For production, set `KY_ENV`, `KY_APP_URL` and `KY_SESSION_SECRET` in `.env` first; see
[Settings in `.env`](#settings-in-env).

Source install (never paste this into a published-image install: the build overlay wins over a
`KY_IMAGE` digest pin, and a source install must set this line before its first `up -d` on a
new checkout; an install from before the published image existed has no such line yet, so run
this block once and confirm with `docker compose config --images`, which must print
`kycalendar:local` rather than the `ghcr.io` name):

```bash
make ci        # gofmt, vet, race tests, smoke test
(umask 077; t=$(mktemp ./.env.XXXXXX) && touch .env \
  && cf=$({ grep '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2-) && cf=${cf:-docker-compose.yml} \
  && case ":$cf:" in *:docker-compose.build.yml:*) ;; *) cf="$cf:docker-compose.build.yml";; esac \
  && { grep -v -e '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } > "$t" \
  && printf 'COMPOSE_FILE=%s\n' "$cf" >> "$t" && mv "$t" .env)
mkdir -p -m 700 data backups && sudo chown 10001:10001 data backups   # the container's UID
docker compose up -d --build
```

A source build reports the release line in `internal/appversion/VERSION` (Settings,
`kycalendar version`, backups) unless `.env` sets `KYCALENDAR_VERSION`, for example to
`$(git describe --tags | sed 's/^v//')` on a release tag. Published images and `make build` stamp it themselves.

For a foreground development server instead, use `make run` in a separate terminal.
It binds `127.0.0.1:8080` and uses `.dev-data/`; stop it before using the same port for Compose.

Update a published-image install on the rolling tag:

```bash
docker compose pull && docker compose up -d
```

A digest-pinned install (`KY_IMAGE` in `.env`) gets nothing from `pull`: re-run the pin recipe in
`docker-compose.yml` with the commit sha you want first, or delete that line to follow `:latest` again.

`AGENTS.md` is the contract for working in this repository.

`make run` never touches `./data` or `./backups`, which hold the container's live database,
key and capsules, and `make clean` removes only build output.

## Monitoring

`GET /healthz` is public and answers the suite's `ky.health/1` JSON for KyPulse: 200 while
`ok` or `degraded`, 503 when `down`. It names two checks, `database` and `backup`, and never an
error message; `backup` is `degraded` (`last_run_failed`) while the schedule is on and the last
run failed. Results are cached for 5 seconds. Compose probes it every 30 seconds, so
`docker compose ps` shows the container unhealthy while it answers 503.

## Network and container defaults

- A bare binary listens on `127.0.0.1`; set `KY_HOST=0.0.0.0` to listen on every interface.
  The image sets `KY_HOST=0.0.0.0`, because a container must.
- Compose publishes port 8080 on `127.0.0.1` only. Put a TLS reverse proxy on the same host in
  front of it and set `KY_APP_URL=https://<your host>` in `.env`. Only when the proxy runs on
  another machine, publish on the LAN with `KY_BIND_ADDRESS=0.0.0.0` (or one LAN address) in
  `.env`. The static-IP overlay below gives a proxy on a Docker network a fixed target and
  publishes nothing itself; the LAN-DNS overlay changes only name resolution.
- With `KY_ENV=production` the server refuses to start unless `KY_APP_URL` is `https://`.
  `KY_ALLOW_INSECURE_HTTP=true` accepts plain HTTP anyway; sessions and app passwords then
  cross the network in the clear.
- The container runs as UID/GID 10001 on a read-only root filesystem with no capabilities;
  only `/app/data`, `/app/backups` and a `/tmp` tmpfs are writable. An install from before
  this change has `./data` and `./backups` owned by root, and the server cannot open its
  database. Fix the ownership once, with the stack down:

  ```bash
  docker compose down && sudo chown -R 10001:10001 data backups && docker compose up -d
  ```

  A new install creates both before its first `up -d` (the quick start above does); a
  directory Docker creates for a missing bind mount is owned by root.

## Settings in `.env`

Compose passes these from `.env` to the server: `KY_APP_URL`, `KY_ENV`, `KY_SESSION_SECRET`,
`KY_ADMIN_PASSWORD`, `KY_TRUSTED_PROXIES`, `KY_RATE_LIMIT_EXEMPT_NETWORKS`, `KY_SCIM_TOKEN`, `KY_KYSIGNON_ISSUER`,
`KY_KYSIGNON_CLIENT_ID`, `KY_KYSIGNON_SECRET`, `KY_KYSIGNON_HMAC_SECRET` and the
`KYCALENDAR_*` settings below. Unset, each keeps the server's default. A production `.env`
(mode 600) holds at least:

```dotenv
KY_ENV=production
KY_APP_URL=https://<your host>
KY_SESSION_SECRET=<random, e.g. openssl rand -hex 32>
KY_TRUSTED_PROXIES=<your proxy's address>/32
```

Switches that weaken security (`KY_ALLOW_INSECURE_HTTP`, `KY_COOKIE_SECURE`) are not passed;
for local testing, set them in a local override's `environment:`.

## Office networks and rate limits

Sign-in, password change, CalDAV and agent (`/mcp`) sign-in are limited per client address:
20 sign-ins a minute, 10 password changes a minute, 10 failed CalDAV or agent sign-ins per
15 minutes. Staff in one office usually share one public NAT address, so they share those
limits; one phone with an old app password can lock every calendar app in the office out for
15 minutes.

List the office's networks to lift the per-address limits for them only: in `.env` as
`KY_RATE_LIMIT_EXEMPT_NETWORKS=203.0.113.0/24,2001:db8:1::/48`, or as an administrator under
**Server settings → Office networks** (one per line; takes effect at once, audited as
`admin.rate_limit_networks`, needs a sign-in younger than 10 minutes). The server uses both
lists. It is empty by default. Ranges broader than /16 (IPv4) or /48 (IPv6), loopback and
anything unparsable are refused: at startup for the variable, with the reason on save for the
screen. A saved list that no longer parses at startup is ignored and logged, never widened.

What still applies to a listed network: the per-account sign-in window (10 failures per
account per 15 minutes), the per-account password-change limit, the CAPTCHA, and on CalDAV and
`/mcp` ten failures per account name (CalDAV) or per token (`/mcp`) per address per 15 minutes,
plus the per-user limit on wrong secrets. So one misconfigured device locks out only its own
account.

The exemption has a ceiling: all listed networks together share 300 sign-ins, 300 password
changes and 300 failed CalDAV or agent sign-ins a minute past the per-address limits. Beyond
that their requests meet the ordinary per-address limits again until the minute is over, so one
host inside the office cannot make unlimited attempts.

The match uses the client address the server already resolves through `KY_TRUSTED_PROXIES`, so
a forged `X-Forwarded-For` cannot claim a listed network. Set `KY_TRUSTED_PROXIES` first when a
proxy fronts the server.

**Risk.** Everyone who reaches the server from a listed address loses the per-address limit.
List only networks you control. Never list guest Wi-Fi, a shared or public network (a
co-working space, a hotel, a carrier's CGNAT range), a VPN provider's exit, or the reverse
proxy's own address: with the proxy listed, every client would be exempt whenever forwarding
breaks.

## Container IP address

To assign the app a static IP on the Docker network, append
`docker-compose.static-ip.yml` to the existing `COMPOSE_FILE` value in `.env`, preserving
any build or LAN-DNS overlays. For a published-image install without other overlays:

```dotenv
COMPOSE_FILE=docker-compose.yml:docker-compose.static-ip.yml
KY_CONTAINER_IP=172.30.0.10
KY_NETWORK_SUBNET=172.30.0.0/24
```

Choose an unused IP inside the subnet and a subnet that does not overlap your existing
networks. These settings are consumed by Compose; `KY_HOST` remains the listener address
inside the container. Without the overlay, Docker continues assigning addresses automatically.

For an existing deployment, run `docker compose down` before changing these settings,
then `docker compose up -d` to recreate the network (brief downtime; omit `-v` to retain
database volumes). For a new deployment, just run `docker compose up -d`.

## Upgrading: compose passes `.env` settings

`docker-compose.yml` now passes `KY_ENV`, `KY_SESSION_SECRET`, `KY_ADMIN_PASSWORD`,
`KY_TRUSTED_PROXIES` and the four `KY_KYSIGNON_*` variables from `.env`; before, those lines
did nothing. Read `.env` before the next `docker compose up -d`: each of them set there now
takes effect. `KY_ENV=production` refuses to start without an `https://` `KY_APP_URL` and a
`KY_SESSION_SECRET`, and `KY_KYSIGNON_ISSUER` binds sign-in to that provider (see
[Sign-in](ACCOUNTS-AND-GROUPS.md#sign-in)). To keep the old behaviour, delete those lines from `.env`. The published port is
unchanged (`127.0.0.1` unless `KY_BIND_ADDRESS` says otherwise), and the container now has a
healthcheck on `/healthz`.

## PostgreSQL and replicas

SQLite is the default: one server, one host. On PostgreSQL, KyCalendar can run as several
identical replicas behind a load balancer that health-checks them. This section is what that
needs. Application replicas are **not** database high availability: every replica depends on
the one PostgreSQL database, and nothing here makes PostgreSQL itself redundant. No capacity
or throughput claim is made. The design follows ky_server_base pull request #121, which is
still open and unapproved.

### Prerequisites

- Every replica uses the same database and schema, with a direct connection or session
  pooling. Transaction pooling (PgBouncer `pool_mode=transaction`) breaks the session locks
  that elect background work.
- A PostgreSQL start refuses, naming the setting, unless:
  - `KY_DB_DSN` is set, and not with the `postgres:postgres` credentials;
  - `KY_SESSION_SECRET` is set, in every `KY_ENV`;
  - the encryption key comes from `KY_ENCRYPTION_KEY` or an existing `<KY_DATA_DIR>/encryption.key`
    (a PostgreSQL start never mints one: replicas must share it);
  - `KYCALENDAR_SCHEDULING` is `true` or `false` (automatic mode decides per process);
  - `KY_DB_MAX_OPEN_CONNS` is at least 4 (the worker lock, the backup lock, a snapshot and a
    request each hold a connection).
- `KY_CALENDAR_MAX_BYTES_TOTAL` defaults to 40 MiB on PostgreSQL as on SQLite, so one tenant
  cannot outgrow every capsule. It caps calendar-object bytes only; it does not guarantee that
  the whole database fits in a capsule (the audit log, change history and indexes add to it). An
  existing PostgreSQL install already above 40 MiB must set a higher value, or new calendar
  writes are refused (507). The per-owner default follows it: a tenth, 4 MiB per user or group
  calendar (it was 16 MiB on PostgreSQL); set `KY_CALENDAR_MAX_BYTES_PER_USER` to keep more. See
  what the instance holds with `docker compose exec postgres psql -U postgres -d ky_server -c 'SELECT bytes FROM calendar_usage'`.
  A capsule still carries at most 256 MiB, and the log warns at 80%.
- Installer-managed deployments (`apply-setup`) are single-server SQLite; `apply-setup` is
  refused on PostgreSQL (`--check` still inspects).
- A mixed-version rolling upgrade is not supported: stop every old replica first.

### Shared and per-host settings

At its first start a replica records a fingerprint of the settings all replicas must share,
each value keyed under a key derived from the encryption key, in the `server_settings` row
`replica_config_fingerprint`. A later start that differs refuses, naming the settings and
never their values. A different encryption key is reported alone and is never accepted.

- **Shared:** `KY_APP_URL`, `KY_APP_NAME`, `KY_ENV`, `KY_SESSION_SECRET`, the encryption key,
  `KY_COOKIE_SECURE`, `KY_COOKIE_DOMAIN`, `KY_RATE_LIMIT_EXEMPT_NETWORKS`, the CAPTCHA settings,
  SCIM, `KY_SSO_ENABLED`, `KY_KYSIGNON_*`, the SAML settings, `KYCALENDAR_SCHEDULING`, the KyPost
  and KyMessages URL, secret-file contents and allow-private switches, `KYCALENDAR_BACKUP_DEPOSIT_INTERVAL`,
  `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY`, every `KY_CALENDAR_MAX_*` cap.
- **Per host:** `KY_HOST`, `KY_PORT`, `KY_DATA_DIR`, `KY_DB_DSN`, the pool sizes,
  `KY_TRUSTED_PROXIES`, `KYCALENDAR_BACKUP_DIR`, `KYCALENDAR_BACKUP_KEEP`, `KY_ADMIN_PASSWORD`
  (bootstrap only).

To change a shared setting: stop every replica, change it everywhere, record it once, start them:

```bash
docker compose run --rm --no-deps app accept-replica-config
```

It prints the names of the changed settings and writes an `admin.replica_config_accepted` audit
row. It refuses while a replica still runs, and it is never the way to change the encryption key.

### What replicas share, and what stays per process

Sign-in limits, DAV and MCP failure budgets, the office budget, CAPTCHA solutions and KyPost
event IDs live in the database on database time, so every replica counts against one limit. A
database that cannot answer a limit denies the request. Sign-in settings saved on one replica
reach the others within 10 seconds, and a sign-in that started under the old provider is
refused ("start again"); office networks follow the database within 5 seconds. Per process,
so N replicas allow N times as much: the invite-count window (the 64 MiB invitation byte budget
is shared), the audit budget for unknown credentials, the suite caches and the displayed count of
exempted requests. Each replica looks up its own KyPost and KyMessages links.

### Background work

One replica at a time runs the scheduled jobs (deliveries, meetings, suite groups,
subscriptions, pruning, the backup schedule), elected by a PostgreSQL session lock that is
probed every second; a replica that loses its database connection stops them within about two
seconds, and the next owner waits three seconds before it starts. This is single ownership,
not exactly-once: the jobs are already idempotent (copies are written against their ETag, a
KyPost send carries the outbox key as its event ID, a KyMessages PUT reconciles). Every backup
run, scheduled, Back up now or CLI `deposit`/`export-capsule`, holds one `backup-run` lock: a
second answers 409 `busy` (the CLI exits 1). The library's seal and local-copy write cannot be
interrupted mid-call; that window is an upstream follow-up, so a run whose lock is lost while
sealing finishes that step before it stops.

### Load balancer

- Probe `GET /readyz` every second. It answers 503 while the replica drains or when the database
  does not answer within a second, with only a status word. `GET /livez` answers 200 whenever the
  process serves and never touches the database; restart on it, never on `/readyz`.
  `/healthz` stays the KyPulse health check.
- Retry only idempotent requests whose connection failed, never after a timeout.
- Upstream timeout about 15 seconds for ordinary requests, which end at 14 seconds with 503
  `request_timeout`. That 503 does not prove the request had no effect: check before retrying.
  The routes below keep longer budgets of their own. Give each a little more than its budget, so
  the server's own answer at the end of the budget still reaches the client:

  | Route | Server budget | Upstream timeout |
  |---|---|---|
  | `POST /api/backup/deposit`, `POST /api/backup/drill`, `POST /api/backup/export-capsule` | 16 minutes | 17 minutes |
  | `POST /api/calendars/{id}/import` (CSV import) | 5 minutes | 6 minutes |
  | `POST /api/subscriptions`, `PATCH /api/subscriptions/{id}`, `POST /api/calendars/{id}/refresh` (published calendars) | 2 minutes | 3 minutes |
  | `POST /api/backup/pair-remote` | 1 minute | 2 minutes |
  | `/api/sso/kysignon/callback`, `PUT /api/admin/signin`, `POST /api/admin/signin/test`, `POST /api/book/{slug}/hold`, `POST /api/presence`, `GET /api/presence/me`, `PUT /api/presence/me`, `POST /api/suite/chat` | 20-second outbound client | 30 seconds |

  Simplest, and the only choice in Nginx Proxy Manager, which cannot set per-route timeouts on
  `{id}` paths: one 17-minute upstream timeout for the whole site (nginx `proxy_read_timeout 17m;`).
  The server still ends every ordinary request at 14 seconds itself.
- nginx and Nginx Proxy Manager passive health checks only notice failed connections; they cannot
  see `/readyz`, so they keep sending requests to a draining replica until it closes. That
  behaviour is not exercised here.

On SIGTERM a PostgreSQL replica turns `/readyz` to 503, refuses new deposits, drills and exports,
cancels its background work, keeps serving for 5 seconds, then drains requests for 5 seconds and
waits up to 10 seconds for cancelled work to record its outcome: 20 seconds, inside Compose's
30-second `stop_grace_period` and Kubernetes' default grace. SQLite skips the 5-second wait.

A Kubernetes sketch, **not executed** (KyCalendar's Kubernetes placement is optional):

```yaml
terminationGracePeriodSeconds: 30
readinessProbe: {httpGet: {path: /readyz, port: 8080}, periodSeconds: 1, timeoutSeconds: 2, failureThreshold: 1}
livenessProbe: {httpGet: {path: /livez, port: 8080}, periodSeconds: 10}
```

### Backups on replicas

On PostgreSQL a capsule carries a verified SQLite snapshot of the database as
`data/kycalendar.db`, the same member, parts and drill as on SQLite. `KYCALENDAR_BACKUP_DIR` is
per replica by default; a directory shared by every replica is also fine, because every write
and prune happens under the `backup-run` lock.

The recovery key pin lives in the database: a pin or a pairing also stores the key and its k-of-n,
sealed under a key derived from the encryption key. Each replica writes its own `recovery.pub`
from it at startup and before each run. A `recovery.pub` naming another key, or a record that does
not open, refuses every run and shows on **Backup & recovery**. A replica that reports the pinned
key unavailable usually just lacks the file: start once the replica that pinned it, or copy its
`data/recovery.pub` to this one.

To replace the pinned key on purpose, or to fix a mistyped k-of-n (pinning the same key with
another split is refused with 409 on both drivers), there is no button. Unpair first if paired,
stop every replica, delete the pin rows, remove `data/recovery.pub` on every replica host, start,
and pin again:

```bash
docker compose stop app
docker compose exec postgres psql -U postgres -d ky_server -c "DELETE FROM server_settings WHERE key IN ('kyrecovery_key_id','kyrecovery_threshold','kyrecovery_total_shares','kyrecovery_public_key')"
rm ./data/recovery.pub        # on every replica host
docker compose up -d app
```

On SQLite run the same `DELETE` with the server stopped, using the host's `sqlite3` package (the
image has none), then give anything it wrote back to the data directory's owner (whatever that is,
rootless Docker included):

```bash
docker compose stop app
sudo sqlite3 ./data/kycalendar.db "DELETE FROM server_settings WHERE key IN ('kyrecovery_key_id','kyrecovery_threshold','kyrecovery_total_shares','kyrecovery_public_key')"
sudo chown -R --reference=./data ./data
```

### Moving a SQLite install to PostgreSQL

Needs Docker Compose 2.34 or later (`docker compose version`): older releases refuse
`run --env-from-file` with `unknown flag`.

With the bundled `postgres` service (database `ky_server`, user `postgres`, its password
`KY_POSTGRES_PASSWORD` in `.env`):

```bash
docker compose stop app                                  # it checkpoints its -wal
docker compose --profile postgres up -d postgres         # --no-deps below needs it running
(umask 077; cat > migration.env)                         # paste the line below without its #, then Ctrl-D
# KY_MIGRATE_POSTGRES_DSN=postgres://postgres:<KY_POSTGRES_PASSWORD>@postgres:5432/ky_server?sslmode=disable
docker compose run --rm --no-deps --env-from-file migration.env app migrate-postgres --check --source /app/data/kycalendar.db
docker compose run --rm --no-deps --env-from-file migration.env app migrate-postgres --source /app/data/kycalendar.db
```

The address stays in a mode-600 file, never in argv or shell history. It is a URL: a password
holding `@`, `/`, `:` or another reserved character must be percent-encoded there (or use a hex
password, `openssl rand -hex 24`). `--check` verifies and
rolls back. The copy is one transaction under the bootstrap lock: it refuses a database that
already holds data, a newer schema, a running server and an unflushed `-wal`, never writes the
source, checks it, compares every table row for row before it commits and moves the audit
sequence past the copied rows.

Then add to `.env` and start the app with `docker compose up -d`; it keeps
`data/encryption.key` and `data/recovery.pub`:

```bash
COMPOSE_PROFILES=postgres          # every up and down now includes the postgres service
KY_DB_DRIVER=postgres
KY_DB_DSN=postgres://postgres:<KY_POSTGRES_PASSWORD>@postgres:5432/ky_server?sslmode=disable
KYCALENDAR_SCHEDULING=false        # or true if KyPost is linked
KY_SESSION_SECRET=<keep yours; if unset: openssl rand -hex 32>
```

Without `COMPOSE_PROFILES`, a plain `docker compose down` leaves PostgreSQL running and the next
`docker compose up -d` starts the app alone, which restarts in a loop on `lookup postgres: no
such host`. With it set, one such restart after a reboot, while PostgreSQL comes up, is normal.
PostgreSQL requires `KY_SESSION_SECRET` in every `KY_ENV`; setting it for the first
time signs everyone out once.

`docker-compose.yml` passes `KY_DB_DRIVER`, `KY_DB_DSN`, `KY_ENCRYPTION_KEY`,
`KY_DB_MAX_OPEN_CONNS` and the calendar caps from `.env`. `scripts/check-postgres-cutover.sh` runs
these steps through `docker-compose.yml` with the built image in CI, including
`accept-replica-config`.

- **Retry:** a target that already holds data is refused, including one a PostgreSQL start
  already wrote an `admin` into. Empty it and run again:
  `docker compose stop app && docker compose exec postgres psql -U postgres -c 'DROP DATABASE ky_server' -c 'CREATE DATABASE ky_server'`
  (the app holds connections, so the drop fails while it runs).
- **Roll back:** stop the app, remove `KY_DB_DRIVER` and `KY_DB_DSN` from `.env`, start. The SQLite
  file was never written, so it is as you stopped it; anything written on PostgreSQL since stays
  there.

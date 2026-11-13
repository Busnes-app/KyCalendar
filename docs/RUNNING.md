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

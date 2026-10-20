# KyCalendar standalone installation

## Before you start

These steps are for a **new, independent installation** from this repository. Use the
README’s upgrade/restore procedure for an existing instance. Keep its volumes, keys,
configuration and Compose overlays; do not replace its environment files with examples.

Use Linux with Docker Engine and Docker Compose 2.24 or newer, Git, and permission to use Docker.
Choose a release/revision and use its matching Compose files and documentation. Image tags
can move: follow the README/Compose attestation and digest-pinning procedure before relying
on a published image. Source builds use the separate build overlay where provided.

Clone this repository, enter its directory, then run the product steps below:

```sh
git clone https://github.com/Busnes-app/KyCalendar.git
cd KyCalendar
```

Each service needs its own directory and persistent storage. Several products use host
port 8080 by default; install them on separate hosts or configure distinct host ports and
matching public origins. For remote users, provide a TLS reverse proxy and a hostname you
control. Trust only the proxy’s actual peer address, and forward the original scheme/client
address according to the product’s proxy guide.

## Install the calendar service

Local administrator, everyday users and groups work without KyIdentity. A browser is
sufficient for calendar use; CalDAV clients use separate device app passwords. Invitations
through KyPost need an additional integration and working mail delivery.

```sh
[ -e .env ] || (umask 077; touch .env)
chmod 600 .env
mkdir -p -m 700 data backups
sudo chown 10001:10001 data backups
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose exec app cat /app/data/initial-admin-password
```

For a same-host trial open `http://localhost:8080`, sign in as `admin`, replace the password
and store recovery codes. The bootstrap file contains a secret; remove it after password
replacement. Administrators manage the service; create an everyday account in **People**
to use calendars. Create local groups for shared calendars if needed.

For remote access, configure a TLS proxy first. In `.env` set `KY_ENV=production`,
`KY_APP_URL=https://calendar.example.com`, a generated durable `KY_SESSION_SECRET`, and
`KY_TRUSTED_PROXIES` to the exact proxy peer address. Keep host port 8080 local to that
proxy. `curl -fsS http://127.0.0.1:8080/healthz` checks service health.

As the everyday user, create a calendar/event, make a per-device app password with `dav`
scope, and configure the client at `https://calendar.example.com/dav/`. Verify read, edit
and delete in both directions with each real client you intend to support. Server tests
are not qualification of iOS/macOS Calendar, DAVx5 or Thunderbird on your devices.

Keep `data` (including secrets) and `backups`. Recovery settings use
`KYCALENDAR_BACKUP_*`; retired `KY_BACKUP_*` names are refused. Read the
[restore runbook](docs/RESTORE.md). Optional OIDC/SCIM and invitation setup are described
in the [README](README.md).

A source Compose installation retains `docker-compose.build.yml` in `COMPOSE_FILE` and
uses `docker compose up -d --build`; a foreground `make run` is a separate development
path using `.dev-data/`, not a deployment step.

## Keep it recoverable

Preserve the database **and deployment keys**, not just the image. Configure recovery using
the linked restore runbook, retain an independent copy, and complete a scratch restore with
custodian cards before treating recovery as verified. A synthetic drill uses a throwaway
key and does not prove that the real cards are available or correct.

For upgrades, back up first, read the chosen release’s migration notes, and reuse the same
Compose project, volume mounts and overlay selection for every command. `docker compose
pull` followed by `docker compose up -d` updates a published-image installation; a digest pin
must be deliberately changed after verification. A source install must keep its build
overlay and rebuild. `docker compose down -v` deletes managed volumes: it is not an upgrade.

## What verification means

The startup checks above prove only the checks they perform. Use the README’s development
section and the repository CI workflow for source/build tests; a passing health response or
screenshot is not a substitute. Record the revision, commands, environment and skips when
reporting results. Design plans and dated evidence remain records of their stated scope.

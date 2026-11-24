# Changelog

## Unreleased

- Groundwork for a future multi-tenant (hosted) mode, with no change to how a self-hosted install
  behaves ([design](docs/superpowers/specs/2026-11-20-tenancy-design.md)).
  - Migration 37 places every existing account, group, calendar, event and audit row in one
    default organisation.
  - It adds database guards so a row can never name a missing organisation or change
    organisation.
  - `KYCALENDAR_TENANCY` accepts only `single` (or unset): any other value refuses to start,
    because multi-tenant mode is not built yet.
  - Erasure groundwork hands restored shared personal calendars to an eligible member of the
    same organisation, preserving content and sync state. Audit or slug conflicts keep the data
    for retry with the erased account deactivated; hosted startup remains gated.
  - Migration 40 adds shared storage-pool report/lease state. In gated multi-tenant code,
    expired grants refuse growth while shrinking remains allowed; renewal snapshots cannot
    spend capacity released remotely. Single-tenant storage limits remain unchanged.
  - Migration 41 records early pool-refresh signals with half-grant/shrinking writes and member
    erasure. The elected worker reports marked usage every five seconds and sizes demand from
    recent growth; storage admission and hosted startup remain gated as before.
  - **Upgrade impact:** the migration adds a column and its checks to the calendar object
    table, which on PostgreSQL validates every row once. Allow for it on a large install.

- PostgreSQL deployments can run as several identical replicas behind a load balancer that
  probes the new `GET /readyz` (`GET /livez` answers whenever the process serves; `/healthz`
  is unchanged). Background work is elected to one replica at a time; rate limits, CAPTCHA
  solutions and KyPost replay records are shared through the database; sign-in and office
  network changes reach every replica within 10 seconds. Replicas are not database high
  availability, and no capacity claim is made ([RUNNING.md](docs/RUNNING.md#postgresql-and-replicas)).
- PostgreSQL makes capsules: a verified SQLite snapshot sealed as before. Restore is the SQLite
  restore then `migrate-postgres` into an empty database. `kycalendar migrate-postgres` moves a
  stopped SQLite install to PostgreSQL, verified row for row.
- Stopping turns `/readyz` to 503 first; PostgreSQL keeps serving 5 more seconds (stop budget
  20 s, inside the 30 s grace). Requests end at 14 seconds with 503 `request_timeout`, except
  the named long routes. A database that cannot answer now gives 503, never a sign-out: the web
  app shows "temporarily unavailable" with Try again, and DAV and MCP clients get 503 with
  `Retry-After`.
- **Upgrade impact (PostgreSQL):**
  - A PostgreSQL start now refuses without an explicit `KY_DB_DSN` (the built-in
    `postgres:postgres` default is gone), `KY_SESSION_SECRET` in every `KY_ENV`,
    `KY_ENCRYPTION_KEY` or an existing `encryption.key` (none is minted), `KYCALENDAR_SCHEDULING`
    set to `true` or `false`, and `KY_DB_MAX_OPEN_CONNS` of at least 4. Each refusal names the
    setting.
  - `KY_CALENDAR_MAX_BYTES_TOTAL` now defaults to 40 MiB on PostgreSQL, as on SQLite (owner
    choice). An install already above 40 MiB must set a higher value before upgrading, or new
    calendar writes answer 507. The cap bounds calendar-object bytes only; it does not guarantee
    that the whole database fits in one capsule (256 MiB). Check with
    `SELECT bytes FROM calendar_usage`.
  - The per-owner default follows it: a tenth, so 4 MiB per user or group calendar instead of
    16 MiB on PostgreSQL. Set `KY_CALENDAR_MAX_BYTES_PER_USER` (or a higher total) to keep more.
  - The first PostgreSQL start records the shared settings; a later start with a different
    shared setting refuses until `accept-replica-config` is run with every replica stopped.
  - Stop every old replica before starting the new version; mixed versions are not supported.
  - `apply-setup` (installer activation) is refused on PostgreSQL; installer-managed deployments
    stay single-server SQLite.
- `docker-compose.yml` passes `KY_DB_DRIVER`, `KY_DB_DSN`, `KY_ENCRYPTION_KEY`,
  `KY_DB_MAX_OPEN_CONNS`, `KY_CALENDAR_MAX_BYTES_TOTAL` and `KY_CALENDAR_MAX_BYTES_PER_USER` from
  `.env`; unset, nothing changes.
- **Upgrade impact (both drivers):** pinning the pinned recovery key again with another k-of-n
  is now refused (409) instead of replacing the split; fixing a mistyped split is the manual
  procedure in RUNNING.md.

## 0.6.1 — pending

- Keep SQLite `v1.62.0` with its upstream-required libc `v1.80.1` and memory `v1.12.1`; defer independent updates of those runtime dependencies.

- Prepare application version `0.6.1` (from `0.6.0`) for the compatible dependency refresh. Rebuild or install the updated application; this version bump requires no additional data, account, key or configuration migration.

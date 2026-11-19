# Changelog

## Unreleased

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
    that the whole database fits in one capsule (256 MiB).
  - The first PostgreSQL start records the shared settings; a later start with a different
    shared setting refuses until `accept-replica-config` is run with every replica stopped.
  - Stop every old replica before starting the new version; mixed versions are not supported.
  - `apply-setup` (installer activation) is refused on PostgreSQL; installer-managed deployments
    stay single-server SQLite.

## 0.6.1 — pending

- Keep SQLite `v1.62.0` with its upstream-required libc `v1.80.1` and memory `v1.12.1`; defer independent updates of those runtime dependencies.

- Prepare application version `0.6.1` (from `0.6.0`) for the compatible dependency refresh. Rebuild or install the updated application; this version bump requires no additional data, account, key or configuration migration.

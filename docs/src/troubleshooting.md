# Troubleshooting

- **CalDAV login fails:** use an everyday account and its per-device app password, not a browser cookie or an administrator account.
- **A shared calendar is absent:** ask the administrator to check group membership and its reader/editor/manager grants.
- **A change conflicts:** reload the current event before applying your changes again.
- **A recurrence is marked partial:** the server bounded its expansion; inspect the series instead of assuming all occurrences are shown.
- **New administrator has no access:** KyIdentity administration requires the explicit `kycalendar.admin` app role; a global role does not grant it.
- **"KyCalendar is temporarily unavailable":** the server could not reach its database. Your session is kept; press Try again. An administrator checks the database and `/readyz` on each replica.
- **A replica refuses to start:** its log names the setting. PostgreSQL needs `KY_DB_DSN`, `KY_SESSION_SECRET`, a shared encryption key, `KYCALENDAR_SCHEDULING` set to `true` or `false` and `KY_DB_MAX_OPEN_CONNS` of at least 4; "replica configuration differs" names shared settings that differ from the other replicas: use the same values everywhere, or after a deliberate change on all of them run `accept-replica-config` once ([PostgreSQL and replicas](https://github.com/Busnes-app/KyCalendar/blob/main/docs/RUNNING.md#postgresql-and-replicas)).
- **Calendar writes answer 507 after an upgrade on PostgreSQL:** `KY_CALENDAR_MAX_BYTES_TOTAL` now defaults to 40 MiB there, and each person or group calendar to a tenth of it (4 MiB); set `KY_CALENDAR_MAX_BYTES_TOTAL` or `KY_CALENDAR_MAX_BYTES_PER_USER` higher if the instance holds more.
- **Backup fails:** check the pinned key, configured destination and last attempt; a local directory on the same host does not provide an independent failure domain.

When reporting a problem, include the product version, platform, failed task and a redacted error. Exclude pairing links, device secrets, recovery codes, custodian shares and private content.

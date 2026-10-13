# KyCalendar installer boundary

Run `/app/kycalendar apply-setup --file /data/installer/bundle.json` in the selected running
container. `--check` performs read-only comparison through private target IPC; it does not
open or migrate a database, generate keys, modify accounts or audit. `--capabilities` needs
no configuration. `KY_DATA_DIR` must match the running server (default `./data`).

The common `ky.product-setup/1` envelope requires service `kycalendar`, a stable setup UUID,
a canonical HTTPS KyIdentity issuer, dedicated client ID and private absolute client and
directory credential references, callback `<KY_APP_URL>/api/sso/kysignon/callback`, at least
one dedicated administrator and optional everyday identities. Credentials and the bundle
must be process-owned regular 0600 files. Identities name exact issuer, subject and unique
username. Use assigned-users-only OIDC and the application role `kycalendar.admin`.

First setup atomically creates new subjects, immutable ownership, sign-in binding and native
SQL audit. It refuses existing identities, sign-in settings, conflicting environment settings,
independent SCIM, automatic provisioning and the disabled SSO kill switch. Reruns inspect
rather than repair drift or reactivate offboarded accounts. Configuration activation requires
the existing graceful restart; KyQuickStart rechecks the running snapshot before success.

Managed sign-in reads protected credentials on each flow and accepts only verified assigned
issuer/subject identities with the expected application role. Global administrator claims,
email and username do not grant access. Dedicated administrators cannot use personal
calendars, app passwords or CalDAV; everyday accounts cannot reach administration. Independent
local recovery identities retain their native credential-replacement rules. Standalone sign-in
settings and reattachment cannot overwrite installer-owned authority; the Sign-in screen is
read-only for managed installations.

Signed `/api/installer/directory` User resources require positive canonical `meta.version`
revisions. Exact retries are no-ops; stale or conflicting revisions are refused. Offboarding
atomically revokes sessions, MFA challenges, pairings and app passwords with its audit while
retaining accounts and calendar content. A newer activation preserves the assigned role.
Managed `/api/sso/kysignon/sync` delegates to the same transport.

`GET /api/installer/network` returns only client address, scheme and trusted-peer decision,
with `no-store`, using native `KY_TRUSTED_PROXIES` resolution.

Managed sealed backups include client/directory credentials and their destination map.
Restore these files to their recorded target paths with the runtime owner and 0600 permissions
before startup. Drills inspect the retained database and require credential members as well
as native calendar counts, objects and integrity checks.

Installer recovery enrollment, central client/app-role/group composition, existing mixed-use
migration, real-device interop and image/library provenance remain deployment gates. The
shared setup package is `ky-primitives/applysetup`, published in v0.10.0 and pinned in
`go.mod`. This
adapter changes no live installation and does not claim a complete deployed suite.

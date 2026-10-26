# kycalendar

[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/15923/badge)](https://www.bestpractices.dev/projects/15923)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/Busnes-app/KyCalendar/badge)](https://scorecard.dev/viewer/?uri=github.com/Busnes-app/KyCalendar)

**Start here:** [Standalone installation](INSTALL.md). Create personal calendars, events and local groups, share group calendars, and connect CalDAV clients using per-device app passwords. Local administrator and everyday accounts work without KyIdentity. KyPost invitation delivery and other suite integrations require separate setup.

KyCalendar is the Busnes.app suite calendar: personal calendars served to the web app and to
native CalDAV clients (iOS and macOS Calendar, DAVx5, Thunderbird) with per-device app
passwords. Built on the suite server base: Go backend, embedded React PWA, SQLite or
PostgreSQL, local and federated sign-in (KyIdentity or any OpenID Connect provider), SCIM provisioning, and
disaster recovery through the suite's KyRecovery.

Installer integration: [product-owned setup contract](docs/INSTALLER_SETUP.md).

## What it can do

- Personal and group calendars, events and shared schedules.
- Local accounts/groups or optional KyIdentity sign-in and provisioning.
- CalDAV clients with per-device app passwords and scoped agent access.
- Sealed backups and a documented recovery procedure.

## Documentation

- [Running](docs/RUNNING.md) — Startup, Monitoring, Network and container defaults, Settings in `.env`, and related procedures. <a id="monitoring"></a><a id="network-and-container-defaults"></a><a id="settings-in-env"></a><a id="container-ip-address"></a><a id="upgrading-compose-passes-env-settings"></a>
- [Accounts and groups](docs/ACCOUNTS-AND-GROUPS.md) — First sign-in, Local everyday accounts, Two-step verification, Administrators and group calendars, People, and related procedures. <a id="first-sign-in"></a><a id="local-everyday-accounts"></a><a id="administrators-and-group-calendars"></a><a id="people"></a><a id="sign-in"></a><a id="groups"></a><a id="calendar-limits"></a>
- [Clients](docs/CLIENTS.md) — Connect a phone, Connect an AI agent. <a id="connect-a-phone"></a><a id="connect-an-ai-agent"></a>
- [Recovery](docs/RECOVERY.md) — Disaster recovery. <a id="disaster-recovery"></a><a id="two-ways-to-get-a-key"></a><a id="why-tls-matters-here"></a><a id="one-run-every-destination"></a><a id="environment"></a><a id="upgrading-from-plaintext-local-backups"></a><a id="upgrading-to-the-kycalendar-service-name"></a><a id="restoring"></a>
- [Verifying releases](docs/VERIFYING-RELEASES.md) — Check a release image's signature and source commit, and rebuild its binary.
- [Assurance case](docs/ASSURANCE-CASE.md) — Threat model, trust boundaries, and why the security requirements are met.
- [Contributing](CONTRIBUTING.md) — How to report issues and send changes, test policy and coding standard.

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

## License

kycalendar is released under the [MIT License](LICENSE.txt).

[![OctoCounts](https://api.octocounts.com/badge/Busnes-app/KyCalendar/branch/main)](https://octocounts.com/github/Busnes-app/KyCalendar/tree/main)

## Documentation

- [Key strength and TLS](docs/STRONG-CRYPTO.md) — supported process/edge configuration and migration while retaining existing data and recovery keys.
- [Running](docs/RUNNING.md) — Startup, Monitoring, Network and container defaults, Settings in `.env`, and related procedures. <a id="monitoring"></a><a id="network-and-container-defaults"></a><a id="settings-in-env"></a><a id="container-ip-address"></a><a id="upgrading-compose-passes-env-settings"></a>
- [Accounts and groups](docs/ACCOUNTS-AND-GROUPS.md) — First sign-in, Local everyday accounts, Two-step verification (KyIdentity's), Administrators and group calendars, People, and related procedures. <a id="first-sign-in"></a><a id="local-everyday-accounts"></a><a id="administrators-and-group-calendars"></a><a id="people"></a><a id="sign-in"></a><a id="groups"></a><a id="calendar-limits"></a>
- [Clients](docs/CLIENTS.md) — Connect a phone, Connect an AI agent. <a id="connect-a-phone"></a><a id="connect-an-ai-agent"></a>
- [Recovery](docs/RECOVERY.md) — Disaster recovery. <a id="disaster-recovery"></a><a id="two-ways-to-get-a-key"></a><a id="why-tls-matters-here"></a><a id="one-run-every-destination"></a><a id="environment"></a><a id="upgrading-from-plaintext-local-backups"></a><a id="upgrading-to-the-kycalendar-service-name"></a><a id="restoring"></a>
- [Verifying releases](docs/VERIFYING-RELEASES.md) — Check a release image's signature and source commit, and rebuild its binary.
- [Assurance case](docs/ASSURANCE-CASE.md) — Threat model, trust boundaries, and why the security requirements are met.
- [Contributing](CONTRIBUTING.md) — How to report issues and send changes, test policy and coding standard.

### Invitation storage and reactivation

An administrator can Enable a disabled KyIdentity person after a fresh sign-in when the account is bound to the live KyIdentity provider. SCIM accounts remain managed by SCIM; changing an issuer still uses Reattach. Enable keeps the person's identity, role and calendars, revokes old credentials and records an audit entry.

Internal invitation copies are charged to the sender until the attendee answers, then to the authenticated responding attendee (including a copy in a group calendar). Quota refusal leaves the copy and charge unchanged. Sender storage includes pending messages and delivered unanswered copies together. Existing copies without delivery metadata acquire it on their next organizer REQUEST; migration does not guess trusted delivery from editable ICS. In addition to 1000 new invitees per sender per hour, internal REQUEST recipient bodies, including updates, have a 64 MiB hourly byte budget per sender. The new-invitee count resets on restart; byte windows persist and commit only with a successful enqueue. Failed saves and deduplicated messages spend no byte allowance. Refusals say when sending can resume in UTC.

Meetings also reserve room for their full cancellation notices. This reduces the room available for ordinary calendar data, so a successfully admitted meeting can still be cancelled when that ordinary space is full. Updates replace the reserved amount; cancellation uses it for the existing queued notices, and ended liability is reclaimed. This adds no setting and keeps the existing wire content and asynchronous delivery. Historical untracked sources remain subject to the legacy-attribution migration question.

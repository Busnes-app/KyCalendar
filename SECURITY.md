# Security Policy

KyCalendar serves personal and shared calendars to the suite web app and to CalDAV clients. KyIdentity owns people and groups; KyCalendar owns calendar data and its access rules.

## Reporting vulnerabilities

Report security vulnerabilities privately through GitHub Security Advisories:
<https://github.com/Busnes-app/KyCalendar/security/advisories>. Do not open a public issue.
Include the affected area, reproduction steps, impact, and affected versions.
We aim to acknowledge reports within two business days and will coordinate a fix
and disclosure.

## Trust boundaries and limitations

- The server stores calendar data to serve CalDAV and the web app; keep the data directory and its backups private.
- Access control is per-identity and per-group. A CalDAV client is scoped to what its authenticated identity may see; sharing never widens that silently.
- KyIdentity owns people and groups; KyCalendar must not grant access to an identity KyIdentity has deactivated.

## Deployment requirements

- Follow [Key strength and TLS](docs/STRONG-CRYPTO.md) for imported signing/TOTP
  key floors, the optional Go TLS profile, the independent HTTPS edge and a
  migration that retains stored data, keys and recovery custody.
- Run behind HTTPS; never add a default that downgrades to cleartext.
- Keep the database and backups private; stop the server before copying the data directory.
- Configure rate limits for login and CalDAV endpoints.

## Security-sensitive changes

Changes to authentication, CalDAV authorization, calendar sharing, or logging require tests for both the expected path and the relevant
failure or attack path, plus a description of the trust boundary in the pull
request.

## Related documents

- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution and review requirements
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — community expectations
- [README.md](README.md) — what this project is and how to run it


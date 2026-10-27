# Assurance case

[Product overview](../README.md) · [Running](RUNNING.md) · [Verifying releases](VERIFYING-RELEASES.md)

This is the argument that KyCalendar meets its security requirements: what it protects, from
whom, where the trust boundaries are, and why the design and the code hold at each one. The
contracts it summarises are in [AGENTS.md](../AGENTS.md) and `internal/api/AGENTS.md`; this page
ties them together and points at the evidence. Where a claim names code, the path is relative to
the repository root. Vulnerabilities are reported under the
[Busnes-app security policy](https://github.com/Busnes-app/.github/blob/main/SECURITY.md).

## What KyCalendar must protect

The supported [key-strength/TLS configuration](STRONG-CRYPTO.md) records each
enabled boundary and its migration requirements. Configured HMAC inputs and
TOTP authentication have key floors; OIDC's bounded JWKS guard checks the same
case-sensitive members as the maintained verifier. The optional process policy
filters verified TLS chains, including trusted roots, while preserving address
guards and credentials. The external HTTPS proxy and live endpoint/custody
readiness require separate operational verification.

KyCalendar serves personal and group calendars to its web app, to CalDAV clients and to AI agents.
Its security requirements:

1. **Only the right person signs in as an account**, and passwords, sessions and app passwords
   cannot be guessed, replayed or recovered from the database.
2. **A user reaches only the calendars they own or were granted**, with only that grant's rights,
   on every surface: web API, CalDAV and MCP.
3. **Access ends when it should.** A password reset, disable, role change or sign-out revokes the
   credentials that depend on it.
4. **The server cannot be turned against the network it sits in** through calendar subscription
   links, identity-provider settings or the KyPost connection.
5. **Secrets at rest stay secret**: identity-provider client secrets and subscription links are
   sealed, and credentials are stored only as hashes.

## Threat model

| Attacker | Can | Wants |
|---|---|---|
| Anonymous internet client | Send any request through the proxy | Take over an account, guess passwords, plant a session |
| Signed-in user | Use their own session, app passwords and agent tokens | Read or change other users' calendars, reach admin functions |
| Malicious web page in the user's browser | Make cross-site requests with the user's cookies | Change calendars or settings (CSRF) |
| Owner of a subscribed calendar feed | Serve any bytes at a link a user subscribes to | Reach internal services, exhaust memory, inject content |
| AI agent holding a token | Call `/mcp` | Exceed the token's scope |
| Someone with a copy of the database | Read every table | Recover passwords, app passwords or sealed secrets |

Out of scope: an attacker who runs code on the host or holds the database **and** the encryption
key (they are the server), and a malicious administrator.

## Trust boundaries

1. **Browser and clients → proxy → KyCalendar.** `X-Forwarded-For` counts only when the peer is in
   `KY_TRUSTED_PROXIES`, walked from the right; `X-Real-IP` is ignored (`internal/auth/clientip.go`).
   Compose publishes on `127.0.0.1` by default and runs the container read-only with all
   capabilities dropped (`TestComposeAndImageAreHardenedByDefault`).
2. **KyCalendar → outbound hosts.** Every outbound fetch uses one guarded client
   (`NewGuardedClientLimit` in `internal/sso/discovery.go`): HTTPS only, the policy applied to the
   address actually dialled after DNS. Identity providers an admin enters are refused loopback,
   link-local (including 169.254.169.254) and other cloud-metadata addresses (`RefuseLocal`).
   Subscription links come from everyday users, so the LAN and CGNAT ranges are refused as well
   (`RefusePrivate` in `internal/feed/feed.go`); the KyPost connection uses the same policy unless
   the operator allows private addresses.
3. **CalDAV clients → KyCalendar.** App passwords, never the account password. A path that is not
   canonical, or whose first segment is not the caller's user ID, is refused before the backend
   runs (`TestCalDAVEncodedTraversal`).
4. **AI agents → KyCalendar.** `/mcp` accepts only an agent bearer token, sends no CORS grant and
   refuses any request carrying an `Origin` header (`internal/api/mcp.go`).
5. **Identity provider → KyCalendar.** The back-channel logout route is anonymous; only a token the
   live provider signed for this client, with the logout event and no nonce, ends sessions
   (`internal/sso/backchannel.go`). A forged or replayed ID token is refused. The route reuses the
   provider's discovery and key cache, so a request does not trigger discovery.
6. **KyCalendar → its database.** Credentials are hashed and secrets sealed before they are stored.

## Secure design principles applied

- **Least privilege.** Administrator accounts cannot use calendars or app passwords
  (`requireEveryday` in `internal/api/app_passwords.go`). App passwords carry a scope. The image runs as UID 10001.
- **Fail-safe defaults.** Forwarded headers are ignored until a proxy is named. Insecure switches
  (`KY_ALLOW_INSECURE_HTTP`, `KY_COOKIE_SECURE`) are not forwarded by Compose. Outbound fetches
  refuse local addresses by default.
- **Complete mediation.** Every route has a row in the authorization matrix, and a route without
  one fails the build (`TestEveryRouteIsInTheMatrix` in `internal/api/authz_matrix_test.go`).
  Access writes recheck the acting administrator inside the write's transaction.
- **Economy of mechanism.** Thirteen direct Go modules (`go.mod`), one guarded HTTP client for
  every outbound call.
- **Separation of privilege.** Irreversible backup actions and app-password creation need a
  sign-in younger than 10 minutes (`requireStepUp`).
- **Open design.** Only published algorithms: Argon2id, HMAC-SHA256, AES-256-GCM, SHA-256. The
  code and this argument are public.

## Common weaknesses and how they are countered

| Weakness | Counter | Where |
|---|---|---|
| Password theft from the database | Argon2id through `ky-primitives/password`; a 12-character minimum; unknown usernames run a dummy verification so timing does not reveal them | `internal/api/auth_handlers.go`, `internal/api/password_handlers.go`, `internal/auth/policy.go` |
| Credential guessing | 20 login attempts per address per minute and 10 failures per account per 15 minutes; MFA 20 per address per minute and 5 per account per 5 minutes; TOTP enrolment, confirmation and disabling 5 per account per 5 minutes each, wrong passwords on disabling counted per account and address; the limiter map is capped | `internal/api/auth_handlers.go`, `internal/api/server.go` |
| Session theft or fixation | A fresh 32-byte random token per sign-in, only its SHA-256 stored; `HttpOnly`, `SameSite=Lax` cookie; 7-day default lifetime; cross-site POSTs to `/api/auth/` refused by `Sec-Fetch-Site` | `internal/auth/session.go`, `crossSite` in `internal/api/server.go` |
| CSRF | Central middleware: every unsafe request with a session cookie must send `X-CSRF-Token` equal to HMAC-SHA256 of that session token under a key derived from the session secret (constant-time compare); the `ky_csrf` cookie only carries the value to the page and is never trusted. Browser requests (`Origin` or `Sec-Fetch-Site` present) to login and MFA must send the HMAC of a random pre-session cookie that `GET /api/auth/me` and sign-out hand out. Only the signed SCIM-sync endpoint is exempt | `ValidateCSRF`, `ValidatePreSessionCSRF` in `internal/auth/session.go`, `csrfValid` in `internal/api/server.go` |
| Session outliving its sign-in-provider login | The provider's back-channel logout token is verified (signature, issuer, audience, expiry, `iat`, logout event, no nonce) before every session of its `sid`, or of its subject issued before it, ends; the `sid` or the subject's cutoff is fenced so an in-flight callback cannot revive it; SSO sessions are dated by the ID token's `iat` | `internal/sso/backchannel.go`, `internal/store/sqlstore.go`, `TestVerifyLogoutToken`, `TestBackchannelLogoutEndsTheSSOLogin` |
| App-password theft | 256 random bits, stored as SHA-256, compared in constant time; shown once | `internal/apppass/apppass.go` |
| Broken access control | Per-route authorization matrix; CalDAV path ownership check; per-calendar grants checked in the store | `internal/api/authz_matrix_test.go`, `internal/api/dav.go`, `internal/access` |
| Server-side request forgery | One guarded client, address policy applied at dial time, HTTPS only, bodies capped, redirects refused (feeds: a bounded number) | `internal/sso/discovery.go`, `internal/feed/feed.go` |
| Cross-site scripting | React escaping (no `dangerouslySetInnerHTML` in `web/src`); CSP with `script-src 'self'`, `frame-ancestors 'none'` | `internal/api/server.go` |
| SQL injection | `database/sql` with `?` parameters, rebound per database driver | `internal/store` |
| Secrets at rest | AES-256-GCM with a random nonce and per-purpose keys derived by HMAC-SHA256 from the deployment key; used for TOTP secrets, OIDC client secrets and subscription links | `internal/crypto/crypto.go`, `internal/auth/totp.go`, `internal/sso/settings.go`, `internal/feed/feed.go` |
| Hostile iCalendar input | Object size limits, and a Go fuzz target for inbound iTIP | `internal/itip/fuzz_test.go`, `internal/calendar` |
| Vulnerable dependencies | Dependabot for Go, npm, Actions and base images; `govulncheck` and `npm audit --audit-level=high` in CI | `.github/dependabot.yml`, `.github/workflows/ci.yml` |
| Supply-chain tampering | Actions pinned by commit; base images pinned by digest; release images attested and built only from a commit that passed CI; the binary rebuilds to the same bytes | [VERIFYING-RELEASES.md](VERIFYING-RELEASES.md) |

## Residual risk

- A sibling host that can set cookies for this domain can still plant a whole session of its own
  (cookie and its token), signing the victim's browser in as the attacker; it cannot forge
  requests on the victim's session.
- Sign-in from a browser too old to send `Origin` or `Sec-Fetch-Site` skips the pre-session
  token, as a native client does.
- Rate limits are process-local and reset on restart.
- TOTP guards the web sign-in of local accounts only. App passwords (CalDAV, MCP) are separate
  credentials; turning TOTP on or off revokes the existing ones, but ones created later work
  without a code.
- An operator password reset turns TOTP off, so whoever can reset a password can also remove
  the second factor.
- No product in the suite has had an independent security audit.

## Evidence

- **Tests:** the authorization matrix, CalDAV traversal, subscription lifecycle
  (`TestSubscriptionLifecycle`), step-up and actor-revocation tests in `internal/api`, run under the
  race detector against SQLite and PostgreSQL 17 on every push and pull request; the vendored
  `go-webdav` tests; Chromium regressions against the built server; an end-to-end smoke test of
  the binary.
- **Static analysis:** CodeQL default setup (Go, JavaScript/TypeScript, Actions), `go vet`, `tsc`
  in strict mode, `shellcheck`, and `govulncheck`.
- **Dynamic analysis:** the race detector, the iTIP fuzz target's seed corpus on every test run,
  and the container and browser checks against the production build.
- **Releases:** attested images and a repeatable binary ([VERIFYING-RELEASES.md](VERIFYING-RELEASES.md)).

This case is updated when a trust boundary or a security requirement changes. A pull request that
moves one must update this page in the same change.

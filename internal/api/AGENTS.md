# API

## Purpose
Exposes HTTP REST routes, authentication endpoints, Single Sign-On callbacks, SCIM endpoints, backup restore drill handlers, and static React PWA hosting.

## Ownership
Owns HTTP routing, request parsing, session cookie validation, CORS headers, and error response formatting.

## Local Contracts
- POST `/api/auth/change-password` accepts a restricted local session, current password and a different policy-valid new password. Browser CSRF and per-IP/account limits apply. Success revokes all sessions and requires sign-in again; flagged sessions get `password_change_required` on protected routes and public-only settings.
- All JSON API endpoints return structured errors `{"error": "message"}` upon failure.
- Non-API routes fall back to serving `web.Handler()` for client-side SPA routing.
- New routes are unauthenticated only by deliberate choice; privileged ones are registered wrapped in `s.requireAdmin` in `routes()`, so the trust level of every route is readable in one place.
- Backup routes and theme writes are admin-only: capsules and settings carry site data and secrets. Group calendar deletion is the one step-up action: the session's credentials must be younger than 10 minutes (`stepUpWindow`, from `Session.CreatedAt`), else 403 `reauth_required`. Other destructive backup routes rely on admin-only plus `TestPrivilegedEndpointsRequireAdmin`. Routes are registered with method patterns, and because the SPA catch-all answers any method, tests pin that a wrong method never reaches a backup handler rather than expecting 405.

| Method | Path | Handler | Response |
|---|---|---|---|
| POST | `/api/backup/drill` | `handleBackupDrill` | `recoveryclient.DrillResult`; 409 when another HTTP/CLI drill holds the data-directory lock |
| POST | `/api/backup/export-capsule` | `handleExportCapsule` | `.kycap` attachment; POST so the CSRF check covers it |
| POST | `/api/backup/pair-remote` | `handlePairRemoteRecovery` | `{recovery_key_id, threshold, total_shares}` |
| POST | `/api/backup/deposit` | `handleRunBackup` | `recoveryclient.Result` (+`receipt_unrecorded`) |
| DELETE | `/api/backup/pairing` | `handleUnpair` | `{paired:false}`; URL and token rows only, key pin stays |
| POST | `/api/backup/pin-key` | `handlePinKey` | write-once; 409 on a different key |
| PUT | `/api/backup/schedule` | `handleSetSchedule` | `{interval_sec}` read back from the store |
| GET | `/api/backup/status` | `handleBackupStatus` | pairing, key, local copies, schedule, members, `database_driver`, `last_run`; never the token |

| Method | Path | Guard | Response |
|---|---|---|---|
| GET | `/api/admin/calendars` | admin | `[{id,name,color,description,created_at,grants:[{group_id,group_name,role}]}]` |
| POST | `/api/admin/calendars` | admin | `{name,color?,description?}` -> 201 the calendar |
| DELETE | `/api/admin/calendars/{id}` | admin + step-up | 204; 403 `reauth_required`; 404 not a group calendar |
| GET | `/api/admin/groups` | admin | `{groups:[{id,display_name}],total}`, `offset`/`limit` <= 200 |
| GET | `/api/admin/audit` | admin | `{records:[...],total}`, `offset`/`limit` <= 200 |
| GET | `/api/calendars/{id}/grants` | session: admin or manager | `[grant]` |
| PUT | `/api/calendars/{id}/grants/{group}` | session: admin or manager | `{role}` -> 200 `[grant]`; 400 bad role; 404 no such group |
| DELETE | `/api/calendars/{id}/grants/{group}` | session: admin or manager | 200 `[grant]` (idempotent) |

- `requireSession`, `requireAdmin` and `requireEveryday` share `authenticate` and put the user in context (`sessionUser`). Grant routes are `requireSession`. `grantableCalendar` admits admins and managers (via `access.Resolve`), answers 404 to users who cannot read the calendar and 403 to readers and editors. Audit actions `admin.calendar_create`, `admin.calendar_delete`, `calendar.grant_set`, `calendar.grant_remove` carry the session user ID, the calendar ID and `group=`/`role=` details.

- `POST /api/backup/deposit` is one `recoveryclient.Run`: seal once, deliver to the local directory and to KyRecovery when paired. 412 no key, key pin missing, no destination, no database snapshot, or a private destination with `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` off; 409 key mismatch or a run in flight; 413 over the capsule caps; 502 when KyRecovery refused (`recoveryclient.ErrRemote`, naming a local copy that was written, so the `ErrPrivateDestination` arm must stay above it: the lib wraps both on the dial path); 500 for a failure before a byte left; 200 with `receipt_unrecorded` when the store holds the capsule but the receipt was not written. It runs on a context detached from the request with a 16-minute write deadline; the acting admin is resolved before the upload and the audit row is written on that same detached context.
- `RecordLastRun` stores every run's outcome (`{at, outcome, error}` in setting `backup_last_run`) from the deposit route (except `ErrInProgress`), the scheduler and the CLI; the lib keeps only the attempt time and the last success, so without it a failing schedule never reaches the screen.
- The write-once, irreversible backup handlers (`handlePairRemoteRecovery`, `handlePinKey`, `handleRunBackup`, `handleDeleteGroupCalendar`) run on `context.WithoutCancel(r.Context())` so a dropped connection cannot leave a pin, a pairing or a deposit half-written with no audit row; the idempotent ones (`handleSetSchedule`, `handleUnpair`) stay on the request context. Their routes are registered as `s.tracked(s.requireAdmin(s.handleX))`, so the `detached` counter is incremented the moment `ServeHTTP` dispatches -- before `requireAdmin`'s session lookup, which is itself a store round-trip that `ReadTimeout` (15s) lets outlast `shutdownTimeout` (5s). Registering inside the handler was too late: `Shutdown` returns after its timeout with requests still active, and one still in the auth lookup would leave `WaitDetached()` reading zero and the store closing under a request about to pin a key. The header-read window before `ServeHTTP` is entered cannot be covered by any counter, because no handler goroutine exists yet; `Shutdown`'s own drain is what covers it. The counter is a mutex and a `sync.Cond`, not a `sync.WaitGroup`, which panics when an `Add` from zero races an in-progress `Wait` -- two admin requests at SIGTERM do exactly that. `WaitDetached()` is what `cmd/server` blocks on before closing the store, because `http.Server.Shutdown` returns without knowing these goroutines exist.
- Audit actions: `backup.paired`, `backup.pair_failed`, `admin.backup_run` (details start with `outcome="success|failure"`), `admin.backup_unpair`, `admin.backup_key_pin`, `admin.backup_schedule`, `admin.backup_export` (a downloaded capsule, resource the capsule ID). `AuditDetails` flattens the lib's details map into the bounded audit field with locally derived fields first and remote error text last; values are quoted and `=` is escaped before the final `AuditSafe` cut. `cmd/server` uses it for the scheduler and CLI rows. Details carry key or capsule IDs, digests and paths, never the token.
- Rate-limit keys for `login:`, `mfa:` and `password-change:` come from `limitIP` (`auth.ClientIP`, IPv6 cut to its /64), never from `RemoteAddr` or a raw header, so a limit is neither shared by everyone behind a proxy nor bypassable by forging `X-Forwarded-For` or rotating addresses in one /64. Per-account windows (`allowAccountAttempt`, MFA and password change) live in a separate map keyed only by real user IDs, so a flood of address keys can never evict them.
- Unsafe requests to `/api/auth/*` with `Sec-Fetch-Site` other than `same-origin` or `none` get 403: login and MFA run before a CSRF token exists, and a cross-site form would plant the attacker's session.
- KySignOn login, callback and sync are wrapped in `requireSSO`; `KY_SSO_ENABLED=false` answers 404 on all three.
- `upsertSSOUser` decides the admin grant from the `roles` claim at every KySignOn login (`kycalendar.admin` only). A change updates the user, revokes sessions and app passwords and audits `sso.role_changed`. Inactive users get 403 and no session.
- App passwords: `GET|POST /api/app-passwords` and `DELETE /api/app-passwords/{id}` are wrapped in `s.requireEveryday` (signed-in, non-admin; admins get 403). Create takes `{label}` (1-64 chars), returns the token once, caps at 20 per user (409) and audits `app_password.create`/`app_password.revoke` with IDs only. Delete is scoped to the caller; another user's ID is 404. List never returns the secret.
- DAV auth: `s.withDAVAuth` (dav_auth.go) wraps the `/dav/` mount with HTTP Basic, username plus app-password token. `resolveAppPassword` loads the token's row by ID, then its owner by `UserID`, and accepts only when the Basic-auth name equals the owner's username under `strings.EqualFold` (never a `LOWER()` username lookup: `alice` and `Alice` can both exist), the secret matches and `Status == "active"` (deactivation backstop). Admins get 403. Only failures are counted, 15-minute windows, 429 with `Retry-After`: every failure counts 10 per IP; a failure counts against the user (50, keyed by user ID) only when the token's ID exists and belongs to the named user and the secret is wrong, so a stranger cannot lock out a user's phones (R28). Usernames over 64 bytes or with control characters get the 401 challenge before the limiter, the store or the audit log; failures audit `dav.auth_failed` with the token owner's user ID when the token ID belongs to the named user, else `user:unknown`, never raw input or the token. The user reaches handlers via `davUser(ctx)`.
- Bare-host discovery: `ServeHTTP` answers PROPFIND, REPORT and OPTIONS on `/` with 308 to `/.well-known/caldav` before the CORS OPTIONS short-circuit and the SPA; GET `/` still serves the SPA.
- CalDAV: `/dav/` and `/.well-known/caldav` are mounted behind `withDAVAuth` and served by `handleDAV` (dav.go) with a per-request `davbackend.Backend`. A decoded path that is not canonical (empty or dot segments, including ones decoded from `%2F` or `%2E`) and a path whose first segment under `/dav/` is not the caller's user ID are 403 before the backend runs; the fork classifies by the cleaned path but passes the raw one, so the two must agree. `TestCalDAVEncodedTraversal` pins this. DAV paths skip the CORS OPTIONS short-circuit and get a body cap of `calendar.MaxObjectSize` + 64 KiB so the fork answers `max-resource-size` itself.
- CORS permits only the exact configured `KY_APP_URL` origin and credentialed browser writes require matching CSRF cookie/header tokens.
- API request bodies are capped at 1 MiB and all responses receive baseline CSP, anti-framing, MIME-sniffing, and referrer-policy headers.
- `GET /api/settings` tiers its payload: public fields for the login screen, `db_driver`/`scim_enabled` for any session, and `extra_settings` for admins only; KyRecovery tokens are omitted in both sealed and legacy plaintext forms, dropped by the `kyrecovery_token` key prefix rather than by literal key name.

## Verification
- `go test -v ./internal/api/...` (`authz_test.go` pins the per-role exposure of every privileged route; `backup_test.go` the backup routes, on SQLite only because a run snapshots the database)
- `scripts/smoke-test.sh` asserts the same boundaries against a running binary

## Child DOX Index
None.

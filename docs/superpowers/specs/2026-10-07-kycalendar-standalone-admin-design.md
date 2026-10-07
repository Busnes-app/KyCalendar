# KyCalendar standalone administration — design

Status: approved in brainstorm 2026-10-07 (approach A, sections 1-5). Implementation plan follows.

## Goal

An operator who installs only KyCalendar runs it entirely from its own admin screens: add
people, put them in groups for group calendars, reset or disable them, and connect a sign-in
provider (KyIdentity or any OIDC provider). No CLI and no KyIdentity for day-to-day use. When
KyIdentity is connected, it stays the owner of the people and groups it synchronises.

Invariants carried over unchanged:

- Administrators and everyday users stay separate (root `AGENTS.md`, Plan 2). Admin identities
  never get calendars, CalDAV or app passwords.
- KyIdentity admin = the exact `kycalendar.admin` app role from `roles`. Nothing else from an
  identity provider grants admin.
- Accounts are never linked by username (PR #13).

## Decisions

| Question | Decision |
|---|---|
| Scope | People, Groups and Sign-in screens |
| Admin for non-KyIdentity providers | Local admin accounts only; every `oidc` SSO user is everyday |
| Synced users/groups | Read-only here, badged "Managed by …"; local ones editable beside them |
| Sign-in storage | Settings store in the database, secret sealed with the data-volume key; env vars lock fields |

Out of scope: password reset by email, SAML, more than one provider at a time, a SCIM-token or
webhook-secret screen (env vars for now), user deletion, auditing SCIM group writes.

## 1. Data model

- **Users:** no schema change. `sso_provider` is the source: `local` is editable; `kysignon`,
  `scim`, `oidc` are read-only.
- **Groups:** migration 10 adds `groups.source TEXT NOT NULL DEFAULT 'local'` (both dialects).
  Every existing row is backfilled to `scim` (SCIM is today's only group writer). The SCIM
  create path writes `scim` explicitly; every other insert is `local`.
- **Sign-in settings** (settings store keys):
  - `signin_provider`: `none` | `kyidentity` | `oidc`
  - `signin_display_name`: login-button label
  - `signin_issuer`: HTTPS issuer URL
  - `signin_client_id`
  - `signin_client_secret_sealed`: AES-GCM under `crypto.DeriveKey(encryptionKey,
    "kycalendar:setting:signin_client_secret")`. Never returned by any API; the admin
    `extra_settings` dump filters every `signin_client_secret*` key.
- **Precedence:** a field set by environment (`KY_KYSIGNON_ISSUER`, `KY_KYSIGNON_CLIENT_ID`,
  `KY_KYSIGNON_SECRET`) wins and is locked. Any `KY_KYSIGNON_*` set means provider
  `kyidentity`. `KY_SSO_ENABLED=false` remains the kill switch.
- Capsule backups carry the settings and the key, so a restore restores sign-in.

## 2. People

Admin-only routes. Writes apply to `local` accounts only; others get 409 `managed_externally`.

| Route | Behaviour |
|---|---|
| `GET /api/admin/users?q=&offset=&limit=` | username, display name, email, role, status, source, MFA, last sign-in; never hashes |
| `POST /api/admin/users` | `{username, display_name, email, role: user\|admin}`; server generates the temporary password, returns it once; forced change at first sign-in |
| `PATCH /api/admin/users/{id}` | display name, email, username (rename via `RenameUser`) |
| `POST /api/admin/users/{id}/reset-password` | new one-time temporary password; existing operator reset (revokes sessions, MFA challenges, app passwords) |
| `POST /api/admin/users/{id}/role` | `user` ↔ `admin`; revoke sessions and app passwords before storing |
| `POST /api/admin/users/{id}/disable`, `/enable` | `inactive` / `active`; disable revokes sessions and app passwords |

Rules:

- Step-up (session younger than 10 minutes, `reauth_required`) for creating an admin, reset,
  role change and disable. One `requireStepUp` helper replaces the copies in
  `group_calendars.go` and `backup_handlers.go`.
- An admin cannot disable or demote themselves. The last active local admin cannot be disabled
  or demoted.
- No delete (it would delete the user's calendars). No admin-typed passwords.
- Audit with the acting admin as actor: `admin.user_create`, `admin.user_update`,
  `admin.user_reset_password`, `admin.user_role`, `admin.user_disable`, `admin.user_enable`.
  `RenameUser` gains an actor parameter.

Screen: searchable table with source badges; "Add person" dialog that shows the temporary
password once with copy and an out-of-band hand-over note; row actions Edit, Reset password,
Make admin/Make user, Disable/Enable; synced rows show the badge and no actions.

## 3. Groups

Admin-only routes. Writes apply to `local` groups only; SCIM groups get 409 `managed_externally`.

| Route | Behaviour |
|---|---|
| `GET /api/admin/groups` | existing; adds `source`, `member_count`, `calendar_count` |
| `GET /api/admin/groups/{id}` | detail with members (id, username, display name, source) |
| `POST /api/admin/groups` | `{display_name}`, created `local` |
| `PATCH /api/admin/groups/{id}` | rename |
| `PUT`/`DELETE /api/admin/groups/{id}/members/{userId}` | add/remove, idempotent |
| `DELETE /api/admin/groups/{id}` | step-up; cascades members and calendar grants; calendars survive |

Rules:

- Members are active everyday users, local or synced. Administrators are refused (409).
- Names are unique across both kinds, case-insensitively. A SCIM create that collides with a
  local name fails with a conflict; the screen flags it; the fix is renaming the local group.
  SCIM never overwrites a local group.
- Access is resolved live by `access.Resolve`; membership changes reach CalDAV clients at the
  next sync and revoke nothing.
- The delete confirm names how many group calendars lose this group's access.
- Audit: `admin.group_create`, `admin.group_rename`, `admin.group_delete`,
  `admin.group_member_add`, `admin.group_member_remove`.

Screen: list with source badge, member and calendar counts; members panel with search-to-add
and remove; rename; delete with confirm. SCIM groups show members read-only.

## 4. Sign-in

One OIDC flow (PKCE S256, nonce, issuer and audience checks) serves both providers. The unused
generic-OIDC client in `internal/sso/oidc.go` and its construction in `api.NewServer` are deleted.

| | `kyidentity` | `oidc` |
|---|---|---|
| Admin | exact `kycalendar.admin` in `roles` | never |
| SCIM adoption by `sub` | yes | no |
| `users.sso_provider` | `kysignon` | `oidc` |

- The callback stays `/api/sso/kysignon/callback` for every provider; the screen shows the full
  URL to copy.
- `GET /api/admin/signin`: each field with its source (`environment` / `saved` / `unset`); the
  secret only as set/unset.
- `POST /api/admin/signin/test`: discovery for the submitted values, not saved. Requires HTTPS,
  an exact issuer match and S256 support; refuses redirects, loopback and link-local targets
  (cloud metadata); private LAN addresses are allowed. Errors are plain text.
- `PUT /api/admin/signin`: step-up, test, save. A blank secret keeps the stored one. On success
  the live provider is swapped behind an `atomic.Pointer` with no restart; a login in flight
  across the swap fails once.
- **Provider or issuer change:** every account of the previous provider is disabled (sessions
  and app passwords revoked) in the same operation, and audited with the count. A colliding
  `sub` from the new provider then reaches a disabled account and gets 403, never a takeover.
  Calendars stay. The confirm dialog states the count.
- Login page: "Continue with <display name>" only when a provider is configured and SSO is
  enabled (today the button always shows).
- Audit: `admin.signin_test`, `admin.signin_save`, `admin.signin_provider_change`. Never the
  secret or fetched URLs' responses.

## 5. Navigation, testing, rollout

- Header for admins: People, Groups, Sign-in beside Group calendars; Dashboard cards for each.
  `App.tsx` gates `dashboard`, `scim`, `backup` and `settings` to admins on the client too.
- Tests:
  - Go: local-only 409s; self and last-admin rules; one-time password returned once and never
    stored in plain; `requireStepUp` and every gated route; group source rules, admin members
    refused, delete cascade; sealed-secret round trip and wrong-label failure; env precedence;
    hot swap; provider-change disable proven by an old `sub` refused through the new provider;
    discovery refusals (HTTP, redirect, loopback, link-local, issuer mismatch); an `oidc` user
    with `roles: [kycalendar.admin]` stays everyday.
  - Migration 10 test in the style of `migration9_test.go`.
  - Authz matrix rows for every new route.
  - Vitest per page: one-time password dialog, reauth message, read-only synced rows, locked
    env fields.
  - Chromium: create a person, sign in as them, land on the calendar; create a group, add them,
    see the group calendar.
  - Smoke: create and list a user through the API on the built binary.
- Rollout: three PRs in order — Groups (migration 10, dead-code removal), People, Sign-in. The
  K8s deployment needs no change: env vars keep its KyIdentity fields locked.

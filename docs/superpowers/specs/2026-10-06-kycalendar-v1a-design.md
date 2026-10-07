# KyCalendar v1a design

Status: approved in conversation 2026-10-06; written spec awaiting review.

## Outcome

KyCalendar is the suite calendar: a separate product that serves personal and group calendars to
the web and to native CalDAV clients (Apple Calendar, Thunderbird, DAVx5). v1a is the core calendar
without invitations. Success is the interop gate in "Testing": real iOS, DAVx5 and Thunderbird
discover, sync, edit and are refused correctly.

## Decisions

| Decision | Chosen | Rejected and why |
|---|---|---|
| Server engine | Go on `ky_server_base`; CalDAV from a fork of `emersion/go-webdav` | Sabre/dav (PHP): first PHP in the suite, cannot use `ky-primitives`, so auth, admin separation and KyRecovery would be rebuilt. Hybrid Go shell + Sabre: two runtimes writing one store. |
| Web calendar widget | FullCalendar standard (MIT): React wrapper, daygrid, timegrid, list, multimonth, interaction, rrule | Schedule-X: v4 moved drag-and-drop and resize to paid Premium; drag-to-create is Premium. The suite excludes required proprietary extensions. Pinning the last free drag-and-drop release means no updates. |
| Product boundary | Separate product | Inside KyPost: KyPost's turnkey plan excludes calendars from its first release, KyPost is mid-qualification at ~66k lines, and the installer must allow a calendar without KyPost. |
| CalDAV library gaps | Fork as `Busnes-app/go-webdav`, upstream each patch | Own handler on go-ical: ~2k lines re-proving what go-webdav already does. Waiting for upstream: no date. |
| Recurring-event edits in the web UI | "This occurrence" and "all occurrences" | "This and following" splits a series; deferred. Native clients may still do it and their data is stored as sent. |
| Components | VEVENT only | VTODO (iOS Reminders) deferred; advertised via `supported-calendar-component-set`. |

go-webdav v0.7.0 facts that forced the fork (read from source): calendar PROPFIND has a fixed
property set (no colour, `getctag` or `sync-token`); `current-user-privilege-set` always grants
everything, so read-only calendars cannot be expressed; no `sync-collection` REPORT; PROPPATCH
returns 501; `free-busy-query` is a TODO.

## Scope

In v1a: identity and access, storage, CalDAV with sync tokens, app passwords, JSON API, recurrence
expansion, web UI, admin screens, KyRecovery backup and restore.

Out of v1a, each with its own spec:

- **v1b invitations (KyCalendar):** iTIP/iMIP REQUEST, REPLY, CANCEL, COUNTER; implicit scheduling
  on save so web and native clients share it; internal delivery; outbound mail through the operator
  relay or KyPost's outbox (undecided); an inbound endpoint KyPost calls as the user. COUNTER
  support in Google Calendar is unverified and must be tested before it is promised.
- **KyPost:** RSVP card on `text/calendar` mail that calls KyCalendar's API as the signed-in user.
- **KyMessages:** an API to create a call link; none exists today.
- **kypost-android:** calendar sync.

## 1. Identity, ownership and access

- A user is a KyIdentity subject keyed by stable `sub`. Users and groups arrive through OIDC login
  and the SCIM connector KyDrive uses. Email is an attribute, never a key.
- Every calendar has one owner: a user or a group.
- Personal calendars: one default is created at first login; the owner may add, rename and recolour.
  Owner-only; no per-user sharing in v1a.
- Group calendars: the KyCalendar admin creates them and grants KyIdentity groups a role:
  `reader` (see events), `editor` (create, edit, delete events), `manager` (rename, recolour, change
  grants). A user's effective role is the highest across their groups. Group calendars belong to the
  organisation and survive offboarding.
- Admin separation: the KyCalendar admin role comes from the `roles` claim (KyPost `AppAdmin`
  pattern; the global `role` claim is ignored). An admin identity can create group calendars, manage
  grants, run backups and read the audit log. It cannot hold a personal calendar, read events or
  create app passwords. Enforced in the authorization layer for API and CalDAV, not by hiding UI.
- Deactivation revokes sessions and app passwords immediately and keeps personal calendars.
  Reactivation restores them; group access follows current membership only.
- Group deletion removes its grants; its calendars remain unassigned until an admin re-grants or
  deletes them. Deleting a group calendar is a separate step-up admin action.
- Native clients authenticate with per-device app passwords (ported from KyPost
  `backend/internal/api/dav_auth.go`). One app password covers the user's personal calendars and
  every group calendar they can read, each exposed read-only or writable by role.

## 2. Storage and CalDAV

Storage is SQLite through the base's DB layer.

| Table | Contents |
|---|---|
| `calendars` | owner kind and ID, name, colour, description, `seq`, `deleted_at` |
| `calendar_grants` | calendar, group, role |
| `objects` | calendar, resource name, UID, ETag, raw iCalendar bytes, `first_start`, `last_end` (NULL for unbounded recurrence) |
| `changes` | calendar, `seq`, resource name, `put` or `delete` |
| `app_passwords` | KyPost schema |
| `audit` | actor, action, target IDs, result |
| `sync_epoch` | one value, replaced on restore |

- Raw bytes are stored and served unchanged; parsing exists only to validate and index. Clients
  keep their `X-` properties.
- PUT validation at the boundary: parses as iCalendar; all components share one UID; UID unique in
  the calendar; VEVENT only; object ≤ 1 MiB and per-user object quota (both configurable);
  `If-Match` and `If-None-Match` honoured.
- Every write or delete increments the calendar's `seq` and appends to `changes` in the same
  transaction. `getctag` is `seq`; the sync token is an opaque URL encoding `sync_epoch` and `seq`.
  `sync-collection` returns changes since the token. Change rows older than 90 days are pruned; an
  older or foreign-epoch token gets `valid-sync-token` and the client resyncs.
- Time-range queries prefilter on `first_start`/`last_end` in SQL, then go-webdav `match.go` decides
  exactly.
- The calendar home lists the user's personal calendars plus reachable group calendars, with
  privileges from the role. Writes from a reader get 403. Admin identities get 403 on every CalDAV
  route, discovery included.
- Fork patches: an extra-properties hook (colour, `getctag`, `sync-token`), per-calendar
  `current-user-privilege-set`, `sync-collection` REPORT, PROPPATCH for display name, colour and
  description, MKCALENDAR.

## 3. Web API, recurrence and UI

- One write path: a web edit is a pure function over the stored iCalendar object that preserves
  unknown properties, and its output goes through the CalDAV PUT path (same validation, ETag check
  and change log).
- JSON API, session cookie and CSRF from the base:
  - `GET /api/calendars`: visible calendars with role, colour, visibility.
  - `POST /api/calendars`, `PATCH`/`DELETE /api/calendars/{id}`: personal calendars; managers may
    rename and recolour group calendars.
  - `GET /api/events?start&end&calendar=…`: expanded instances. Range ≤ 400 days, at most 5000 instances per
    response.
  - `POST /api/calendars/{id}/events`; `PUT` (with `If-Match`) and `DELETE
    /api/events/{cal}/{uid}`, with scope `this` or `all` for recurring events. 412 on conflict.
  - Admin routes for group calendars, grants, audit and backup. None touch events.
- Recurrence: server-side expansion of RRULE, RDATE, EXDATE and RECURRENCE-ID overrides.
  Spiked 2026-10-07: go-ical's recurrence helper is unusable (drops RDATE, fails on EXDATE lists and
  non-IANA TZIDs); expansion is KyCalendar's own over `rrule-go`, which handles DST, UNTIL and EXDATE
  correctly. Recorded Apple, Google and Outlook exports remain part of the interop gate.
- Time zones: TZIDs resolve against Go's embedded IANA data plus the CLDR Windows→IANA map (Outlook
  sends names such as `Eastern Standard Time`). An unknown TZID is stored, shown in UTC and flagged.
  Floating times render in the viewer's local zone.
- UI (FullCalendar standard):
  - month, week, day and list views; sidebar of personal and group calendars with visibility toggles
    and colours;
  - drag to create, move and resize where the role allows;
  - event form: title, time, all-day, location, description, calendar; repeat presets none, daily,
    weekly with weekdays, monthly, yearly. Rules outside the presets show "Custom (edit on your
    device)" and are preserved;
  - settings: app password created and shown once, list, revoke; CalDAV URL and setup steps for iOS,
    DAVx5 and Thunderbird;
  - ky-ui tokens vendored with `check-vendor.mjs`; FullCalendar CSS variables mapped to `--ky-*`;
    Busnes light/dark following the OS;
  - list view and event form are the keyboard path for every action.
- Event text is untrusted: descriptions and locations render as text, only `http`/`https` links are
  linkified, and a strict CSP forbids inline script.

## 4. Errors, backup and testing

### Errors

- CalDAV errors use RFC preconditions: `valid-calendar-data`, `no-uid-conflict`,
  `max-resource-size`, `valid-sync-token`; 412 on ETag mismatch.
- Every write, with its change row, is one transaction.
- App-password authentication is rate-limited per user and per IP (KyPost pattern).
- Logs carry IDs only: never passwords, tokens or event content.
- Audit: admin actions, app password create and revoke, auth failures, pairing and deposits.

### Backup and restore

Follows the root `AGENTS.md` KyRecovery contract via `ky-primitives/recoveryclient`, with
`kysignon-server/internal/backup` as the reference adapter.

- Sealed payload: SQLite snapshot by `VACUUM INTO` (includes WAL) plus every secret a restore needs.
  Service name `kycalendar`.
- Config: `KYCALENDAR_BACKUP_DIR` (keep newest N), `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY`
  (default off), `KYCALENDAR_DNS`; interval set in the admin UI.
- Drill: seal to a throwaway key, open, `PRAGMA integrity_check`, compare calendar and object counts,
  parse a sample of objects.
- `restore`: k shares on stdin, never argv. After restore, revoke all app passwords and sessions and
  write a new `sync_epoch`, so a client holding a post-backup token cannot believe it is current and
  keep events the restore removed.

### Testing

1. Pure functions, table-driven: iCalendar edits preserve unknown properties; recurrence expansion
   and Windows TZ mapping over a fixture corpus of real Apple, Google, Outlook and Thunderbird
   exports; permission resolver.
2. Store: `seq` advances exactly once per write and per delete; stale ETag rejected; pruned token
   gets `valid-sync-token`; tokens from before a restore are rejected.
3. CalDAV: request sequences recorded once from real iOS, DAVx5 and Thunderbird and replayed as
   golden tests; round trips through go-webdav's CalDAV client.
4. Authorization matrix: every API and CalDAV route × owner, reader, editor, manager, non-member,
   admin identity, deactivated user. The test fails when a registered route is missing from the
   matrix.
5. Web: vitest and Playwright from the base; ky-ui vendor and theme checks. `make ci` in GitHub
   Actions.
6. Interop gate, recorded as evidence under `docs/`: iOS, DAVx5 and Thunderbird each discover from
   the bare host, create, edit and delete, change one occurrence of a recurring event, are refused on
   a read-only group calendar, and stop syncing after app-password revocation. v1a is not done until
   all three pass.

## Open items for the plan

- Spike: go-ical recurrence over the fixture corpus (section 3).
- Fork setup: repository under `Busnes-app`, pinned in `go.mod` via `replace`, patch list above.
- KyIdentity: register the `kycalendar` OIDC client, create the app role `kycalendar.admin` (Plan 2 grants admin on that exact value), and confirm the SCIM connector delivers groups as it does for KyDrive. This is an operator step; Plan 2 does not automate it.

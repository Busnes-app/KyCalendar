# Tenancy: single-tenant and multi-tenant (SaaS), KyCalendar design

Date: 2026-11-20. Status: proposed design. Nothing is built, and this change is docs only. Product code
waits for KyIdentity's tenancy step 3 (`org_id` in tokens, userinfo, logout tokens, signed webhooks
and `/api/suite/orgs`).

Owner decisions (fixed, not reopened here):

1. One binary, both modes. Single-tenant (today's self-hosted) is unchanged on upgrade.
   Multi-tenant is a mode.
2. Organisations under one sign-in: one KyIdentity issuer for every customer. Each customer (a
   person or a household) is an org, and tokens carry an opaque `org_id`. KyCalendar is one shared
   install that separates data by org. Per-customer realms were rejected.
3. One org per account. There is no org switcher.
4. No administrators inside customer orgs. Only our staff are administrators. Customers are
   members. Single-tenant installs keep their own administrators.
5. Businesses do not use the shared service. Each gets its own isolated single-tenant install, set
   up by hand.
6. Stripe lives only in a separate billing service, built later.

Line references are to KyCalendar `origin/main` at `8f59677`, and to KyIdentity `origin/master` at
`7aa1cca` for its tenancy spec (`docs/superpowers/specs/2026-11-19-tenancy-design.md`, cited below
as **T**). "Unverified" marks a claim this spec did not check in code.

## Summary

| | Single-tenant (self-hosted, business platforms) | Multi-tenant (hosted SaaS) |
|---|---|---|
| Mode | `KYCALENDAR_TENANCY=single` (default) | `KYCALENDAR_TENANCY=multi` |
| Database | SQLite or PostgreSQL, as today | PostgreSQL only (D2) |
| Sign-in | any provider an admin configures (`AGENTS.md:380`) | KyIdentity only, fixed by `KY_KYSIGNON_ISSUER` |
| Org of a row | the default org, one per install | the account's `org_id`, from KyIdentity |
| Administrators | local admins and `kycalendar.admin` holders | staff only: `kycalendar.admin` in the staff org (D7) |
| Groups and group calendars | local, SCIM and suite groups | suite groups only (D9) |
| Invitations to people in another org | n/a | email (iTIP/iMIP through KyPost) only (D5) |
| Code paths | one: handlers get an org-bound store bound to the default org | the same code, bound to the caller's org |

## Decisions this spec makes

| # | Decision | Why |
|---|---|---|
| D1 | `KYCALENDAR_TENANCY=single` (default) or `multi`, recorded in `server_settings` at first start. A later start with the other value refuses to start. In multi-tenant mode `KYCALENDAR_STAFF_ORG_ID` (D7) gets the same treatment: recorded at first start, a later start with another value refuses to start, and setting it in single-tenant mode is a startup error. Both join the replica configuration fingerprint (`internal/config/replica.go:17`). | Install-time, as decided. A replica started in the wrong mode must not serve. Mirrors T D1. The staff org decides every admin grant and owns the break-glass accounts. If it changed on a restart, existing staff would silently lose admin, and the new org would have no seeded row. If it differed between replicas, they would make different admin decisions on one database. |
| D2 | **Multi-tenant requires PostgreSQL**, and a KyIdentity provider fixed by environment (`KY_KYSIGNON_ISSUER`, `internal/sso/settings.go:85-114`). The admin Sign-in save is refused in multi-tenant mode. `KY_SSO_AUTO_PROVISION=false` is a startup error in multi-tenant mode. | Auto-provisioning is what makes a skipped webhook create harmless (Directory webhook): the first sign-in creates the account. The hosted service runs PostgreSQL replicas anyway (`AGENTS.md:356`), as T D2 requires for KyIdentity. One issuer (owner decision 2) means the binding never changes, so the binding-change paths (`internal/api/signin.go:69-157`, reattach) are out of scope there. |
| D3 | Isolation is enforced in the application, through an org-bound store handle (`OrgStore`), as T D3 does. | Each migration names a SQL text per backend (`internal/store/migrations/migrations.go:13-18`). From version 20 on, the registry entries (`migrations.go:571-607`, versions 20–36) give both backends one text, with only column types swapped. The exception is version 32's task search index (`taskSearchSQLite` and `taskSearchPostgres`, `:603`). The table SQL itself lives in constants later in the file, which the Inventory cites. Single-tenant installs on SQLite run the same handlers. App-level scoping covers both. |
| D4 | Single-tenant installs get one **default org** (`00000000-0000-0000-0000-000000000001`, T's default-org ID). Every org-owned row carries it after the upgrade. Tokens there carry no `org_id` (T D4), and KyCalendar reads none. | One code path. With one org, every query returns what it returns today, which is the zero-change argument the upgrade test proves. |
| D5 | **In multi-tenant mode every address is external, inside one org too.** `CalendarUser(email)` (`internal/store/scheduling.go`) answers `ErrNotFound` for everyone there. The caller's own address (`EmailOwner`, L2) is unchanged. | That one lookup decides internal delivery, free/busy, the out-of-office comment and meeting membership (Isolation, below). With no peer found, nothing is written into another account's calendar and no busy time is shown, so nothing can cross orgs. Owner decision, 2026-11-21: an org that wants internal scheduling between colleagues gets its own single-tenant install (owner decision 5). This replaces a per-org lookup, which would have threaded the org through every scheduling path. |
| D6 | KyCalendar keeps a local mirror of `/api/suite/orgs` (`orgs` table), polled about every 60 s by the elected worker. Every access check reads the user's org status from that mirror in the same query as the user. | T D6 and Downstream contract 5 and 6. Reading status with the user, as T's `active_principals` does, means a suspension takes effect at the next request on every replica. |
| D7 | In multi-tenant mode an account is an administrator only when the ID token carries `kycalendar.admin` (`internal/access/admin.go:9-12`) **and** its `org_id` equals the configured staff org (`KYCALENDAR_STAFF_ORG_ID`, required in multi). Every other account is an everyday member, whatever its roles. | Owner decision 4. T D8 already keeps admin roles and delegations in the staff org. This check is defence in depth: a staff mistake that assigns the app role in a customer org grants nothing here. |
| D8 | Local accounts in multi-tenant mode are staff break-glass administrators only: the bootstrap `admin` and `init-admin` accounts, in the staff org. `create-user`, admin People writes and local everyday sign-in are refused. | A customer account's org must come from a verified token, a signed webhook or a suite answer (Downstream contract 4). A local account has none of those, so it cannot be a customer. |
| D9 | In multi-tenant mode, local groups, SCIM, rooms and equipment, admin-made group calendars and reattach are off (404). Group calendars come only from KyIdentity suite groups. | Business features. They stay in single-tenant installs (owner decision 5), as T "Per-org features" does for KyIdentity's business features. It also leaves the only group names in use as `suite-group:<KyIdentity id>` (`internal/store/suite_groups.go:33`), which are already unique across orgs. So `groups.display_name UNIQUE` (`migrations.go:76`) and `resources_name` (`migrations.go:663`) need no per-org rewrite. |
| D10 | Every KyCalendar↔KyPost and KyCalendar↔KyMessages body gains `org_id` in multi-tenant mode. KyCalendar fills it from the account row. On receipt, KyCalendar resolves the account by `(sub, issuer)` as today and requires the body's `org_id` to equal the account's. A mismatch or an absent `org_id` answers exactly like an unknown user. | T "Suite directory", `/api/suite/peer`: the pair secret is per pair, not per org, so the org must travel in the signed body and be checked. A peer's `org_id` is a check, never a source: KyCalendar never assigns an org from a peer. |
| D11 | Org quotas come from `/api/suite/orgs` `quotas` (T "Entitlements"). Keys: `max_bytes` (the org's total calendar storage), `max_bytes_per_user`, `booking_pages` (bool). An absent key falls back to today's install setting. | Downstream contract 6. Today's caps are per owner and per install (`internal/config/config.go:296-307`). A shared install needs an org total between the two. |
| D12 | A `deleted` org is purged by the elected worker in short, resumable batches behind a fail-closed audit row (Lifecycle, Deleted). It is never one transaction, which would hold `calendar-total` and block every other org's writes. Its `orgs` mirror row stays as a tombstone, so a late token or peer message naming it is refused. | Downstream contract 5. T keeps the same tombstone rule for its own `orgs` rows. |

## Inventory

Classes, as in T's appendix:

- **O** (org-owned): gets `org_id TEXT NOT NULL`.
- **D** (derived): scoped through the parent named, with no column of its own.
- **P** (platform): global.
- **Off**: a business feature, off in multi-tenant mode (D9). The table stays and holds no rows
  there.

All tables are created in `internal/store/migrations/migrations.go` (cited as `mig.go`). The
highest version is 36 (`mig.go:607`). Where two lines are given, they are SQLite then Postgres.
From version 20 on, one SQL text serves both, apart from column types and the task search index.

| Table | Created | Class | Reasoning |
|---|---|---|---|
| `schema_migrations` | `mig.go:968` / `:961` | P | Schema version |
| `users` | `mig.go:25` / `:106` | O | Root of every org's data. `username UNIQUE` (`mig.go:27`) stays global: in multi-tenant mode KyIdentity's username is the email and globally unique (T D5). Email is not unique here (`mig.go:46`), and KyIdentity keeps it globally unique (T D5). |
| `sessions` | `mig.go:49` / `:130` | D | `user_id` FK, cascade |
| `mfa_challenges` | `mig.go:191` / `:199` | D | `user_id` FK |
| `device_pairings` | `mig.go:227` / `:240` | O | `user_id` has **no FK** (recreated in v5). The feature is removed (`AGENTS.md:300`). The org column lets the purge find any leftover rows without proving a join. |
| `groups` | `mig.go:74` / `:155` | O | `display_name UNIQUE` stays global (D9) |
| `group_members` | `mig.go:82` / `:163` | O | Joins two org-owned rows. A composite FK keeps both in one org. |
| `audit_records` | `mig.go:88` / `:169` | O | `user_id` has no FK. **Nullable** `org_id`: NULL is a platform row (restore, backup, staff platform actions), as T does for `audit_events`. |
| `server_settings` | `mig.go:99` / `:180` | P | Install-wide keys only. Every key in use is install-wide (`internal/store/sqlstore.go:1807-1949`). Future per-org settings go in a new table, never in prefixed keys. |
| `calendars` | `mig.go:257` / `:298` | O | `owner_id` has no FK (`mig.go:259-260`). For a user it names the user. For a group calendar it is the calendar's own ID (`AGENTS.md:334`). A trigger ties a personal calendar's `org_id` to its owner's. A group calendar takes the suite group's org at creation (Isolation). |
| `calendar_objects` | `mig.go:270` / `:311` | O | It has a `calendar_id` FK, but carries its own `org_id`, for the usage triggers alone. A calendar delete removes its objects by cascade (`internal/store/calendars.go:138`, `internal/store/sqlstore.go:449`). A delete trigger that looked up the parent calendar for the org could then find it already gone, and `org_usage` would drift upward. With the column, the trigger reads `OLD.org_id`. An insert trigger requires it to equal the calendar's `org_id`, and the immutability trigger keeps it. `invitation_sender`, `_attendee` and `_recipient` are user IDs with no FK (`mig.go:861-863`). In multi-tenant mode nothing is delivered internally (D5), so they never name another org's user. |
| `calendar_changes` | `mig.go:283` / `:324` | D | `calendar_id` FK |
| `calendar_meta` | `mig.go:292` / `:333` | P | One row, `sync_epoch` (`mig.go:296`) |
| `app_passwords` | `mig.go:343` / `:353` | D | `user_id` FK. Covers DAV and MCP tokens. |
| `calendar_grants` | `mig.go:368` / `:376` | O | Joins a calendar and a group, both org-owned. Composite FKs `(org_id, calendar_id)` and `(org_id, group_id)` make a cross-org grant impossible to write. |
| `calendar_usage` | `mig.go:877` | P | The single install-wide byte total (`CHECK (id = 1)`), kept by triggers (`mig.go:410-428`). It stays as the instance cap. |
| `mcp_events` | `mig.go:442` / `:449` | D | `token_id` and `calendar_id` FKs |
| `calendar_subscriptions` | `mig.go:460` / `:473` | D | `calendar_id` FK. The feed URL is sealed (`AGENTS.md:400`). |
| `scheduling_outbox` | `mig.go:500` / `:514` | O | `sender_id` has no FK, and a queued message must be found by org on suspension and purge. `dedupe_key UNIQUE` stays global: it is a hash over the sender ID (`internal/davbackend/scheduling.go:242-243`). |
| `sso_logouts` | `mig.go:536` / `:542` | P | A fence keyed by `binding + sid`. Rows expire. |
| `sso_subject_logouts` | `mig.go:552` / `:559` | P | A fence keyed by `(issuer, subject)`. KyIdentity subjects are opaque and global. The purge deletes the org's subjects' rows. |
| `invitation_byte_budget` | `mig.go:864` | O | `sender_id` has no FK |
| `cancellation_reservations` | `mig.go:851` | D | `sender_id` FK and `calendar_objects` FK |
| `video_meetings` | `mig.go:759` | D | `user_id` FK, cascade |
| `meeting_guests` | `mig.go:744` | D | `meeting_id` FK |
| `booking_types` | `mig.go:697` | D | `user_id` FK. `slug UNIQUE` (`mig.go:700`) stays global: 32 random hex characters (`internal/api/booking.go:390-392`) on one public route. |
| `bookings` | `mig.go:717` | D | `type_id` and `user_id` FKs |
| `resources`, `resource_groups`, `resource_approvers`, `resource_bookings`, `resource_busy` | `mig.go:648-1061` | Off | Rooms and equipment are install-wide (`internal/store/resource_bookings.go:59-63`: audience `everyone` means every active member) |
| `kypost_out_of_office` | `mig.go:680` | D | `user_id` FK |
| `presence_pushes` | `mig.go:1027` | D | `user_id` FK |
| `suite_group_calendars` | `mig.go:637` | O | Joins a calendar and a group. A composite FK keeps both in one org, and the org must equal the suite group's org from `/api/suite/groups`. |
| `task_areas`, `task_views`, `task_goals`, `task_prefs`, `task_completions` | `mig.go:781-834` | D | `user_id` FK |
| `task_search` | `mig.go:841` (FTS5) / `:843` | D | Postgres has the `calendar_id` FK (`mig.go:844`). The SQLite FTS5 table has none (pruned at `internal/store/tasks.go:298`), but multi-tenant is Postgres only (D2). |
| `security_windows`, `spent_challenges`, `kypost_replay` | `mig.go:615-626` | P | Rate windows, CAPTCHA salts and the KyPost replay ledger. Keys hold user IDs or event IDs, never content, and expire. |

Tables this spec adds:

| Table | Class | Reasoning |
|---|---|---|
| `orgs` (`id`, `status`, `revision`, `enabled`, `quotas_json`, `seen_at`, `purged_at`) | P | The local mirror of `/api/suite/orgs` (D6), and the target of every `org_id`. Holds the default org in single-tenant mode. A deleted org stays as a tombstone (D12). |
| `org_usage` (`org_id`, `bytes`) | O | The org's stored object bytes for `max_bytes` (D11). It is kept by triggers on `calendar_objects`, beside `calendar_usage`'s, keyed by the object's own `org_id` (`NEW.org_id` / `OLD.org_id`) with no parent lookup. Queued outbox bytes and reservations are added at check time (Quotas). |

**Files and process state.** None of these are per user, and none change class in multi-tenant mode.

- On disk: the SQLite database, `encryption.key`, `initial-admin-password`, `recovery.pub`, the
  installer socket and the backup and snapshot directories (`internal/config/config.go:202-243`,
  `cmd/server/bootstrap.go:19`, `internal/backup/scratch.go:14-29`). All are P. Multi-tenant
  is Postgres, and `apply-setup` is already refused there (`AGENTS.md:359`).
- In memory: per-IP and per-account rate windows (`internal/api/windows.go:33-83`, shared through
  `security_windows` on Postgres), the per-sender invite count (`davbackend.InviteLimit`,
  `internal/davbackend/scheduling.go:36-43`, per process), and caches keyed by user ID:
  suite apps (`internal/api/suite_apps.go:23`), suite people (`internal/api/suite_people.go:39-42`)
  and presence (`internal/api/presence.go:88-91`). Keys are user IDs, which are random and global
  (`usr_<24 hex>`, `internal/api/sso_handlers.go:79`), so no key can name two orgs.
- One KyPost link and one KyMessages link per install (`internal/kypost/link.go:29-41`,
  `internal/kymessages/kymessages.go:118-129`). They are P, and D10 carries the org.

## Isolation

### `org_id` on every row

One migration (37, the next free version) on both backends:

1. Create `orgs` and `org_usage`. The migration text is fixed and reads no environment. It inserts
   the default org (`active`, `enabled`) only when any org-owned table already holds rows, so
   PostgreSQL's foreign key finds the org for every existing row. That is an existing
   install, which is always single-tenant, since converting one to multi-tenant is out of scope
   (as in T). A fresh database gets no `orgs` row from the migration.

   `store.Open` then runs `ensureOrgs` (`internal/store/orgs.go`), right after the migrations and
   so before `cmd/server` creates the first admin (`AGENTS.md:475`). Its inserts are
   `ON CONFLICT DO NOTHING`, so it is idempotent and replicas starting together need no lock: it inserts the default org
   (single-tenant) or the staff org from `KYCALENDAR_STAFF_ORG_ID` (multi-tenant, `active`,
   `enabled`) if it is missing. So the staff org exists before bootstrap and `init-admin` insert the
   `admin` account into it (D8), and no poll is needed first. The staff org row comes from
   configuration: the mirror poll never changes it and the purge never touches it.

   The copy paths stay correct, because they run only the migrations on an empty target, which
   then holds no `orgs` row:
   - `SnapshotToSQLite` (the PostgreSQL backup member);
   - `migrate-postgres`, whose emptiness check allows only the rows migrations write themselves
     (`seeded`, `internal/store/migrate_postgres.go:16-17`).

   `orgs` and `org_usage` join `migrationTables` (`internal/store/snapshot.go:23-37`). The list is
   the copy order, and two places in it matter:
   - `orgs` goes **first**, before every O table, because the existence trigger refuses any row
     whose org is not yet in `orgs`. Today's list starts with `users` (`:24`), so `orgs` must be
     added ahead of it.
   - `org_usage` goes after `calendar_objects`, as `calendar_usage` does (`:20-22`): the target's
     usage rows are deleted before the copy, and the copied rows land last.

   **Who creates an `org_usage` row.** The usage triggers only `UPDATE`, as `calendar_usage`'s do
   (`mig.go:410-428`). They key on the object's own `org_id`, so a cascaded calendar delete
   decrements correctly. A test deletes a calendar holding objects, and a user with calendars, then
   proves `org_usage` equals the sum of the org's remaining objects. An org's row is inserted, with `bytes = 0`, by the application code in the
   same transaction that inserts its `orgs` row: `ensureOrgs`, and the mirror poll when it first sees
   an org. The migration backfill inserts one for the default org. A copy inserts `orgs` rows raw, so
   no `org_usage` row exists in the target while objects are copied. The triggers then match
   nothing, and the copied `org_usage` rows land last with exact totals, as for `calendar_usage`.
   The `max_bytes` check treats a missing `org_usage` row as an error and refuses the write (fail
   closed), so a bug can never turn into an unmetered org. A test inserts a new org through the poll,
   writes an object, and proves its `org_usage` grew by the object's size.

   PR 1 extends `TestMigrationTablesCoverTheSchema` (`internal/store/roundtrip_test.go:128`) to
   fail when an O table precedes `orgs` in the list.
2. Add `org_id`, defaulting to the default org, to every O table. Backfill `org_usage` from
   `calendar_objects`.
3. Add triggers on both backends, with the same names. One requires the org to exist in `orgs`.
   One refuses any change to `org_id`. For a personal calendar (`owner_kind = 'user'`), one requires
   the calendar's `org_id` to equal the owning user's.

   A group calendar has no owner row to compare. Its `owner_id` is its own ID, so it outlives the
   group (`AGENTS.md:334`). Its org is set once, at creation, from the suite group's org in the
   `/api/suite/groups?org_id=` answer that creates it (Suite answers). The composite FKs on
   `suite_group_calendars` and `calendar_grants` then tie it to groups of that same org.
4. Add the composite uniques `users(org_id, id)`, `groups(org_id, id)` and
   `calendars(org_id, id)`. Then add the composite FKs on `group_members`, `calendar_grants` and
   `suite_group_calendars`. On SQLite these are trigger checks, since `ALTER TABLE` cannot add
   constraints there.
5. `tenancy_mode` is recorded by `ensureOrgs`, not the migration. No migration writes a setting,
   and `server_settings.updated_at` has a different type on each backend. When no mode row exists,
   `ensureOrgs` records `single` if the default org exists: an upgraded install, whose migration
   made it. Otherwise it records the configured mode, with `KYCALENDAR_STAFF_ORG_ID` in
   multi-tenant mode. D1's mismatch check runs right after. Until PR 6 only `single` exists, and
   `KYCALENDAR_TENANCY` refuses any other value at startup. A multi-tenant install therefore starts
   on an empty database (T "Migration"), and its first start records `multi`. The copy paths stay as
   step 1 describes: a snapshot target's own rows are cleared before the copy, and the row is copied
   with `server_settings`.

The `org_id` default (the default org) stays on both backends. A multi-tenant install has no
default org row, so an insert that forgets `org_id` names a missing org and fails the existence
check instead of landing in an org. That is the protection T gets by dropping its default, and
keeping the default leaves single-tenant inserts that name no org unchanged.

`orgs.status` takes KyIdentity's values (`active`, `suspended`, `deletion_scheduled`, `deleted`)
plus one local value, `stale`: KyCalendar has no current answer for this org. Only `active`
serves. A customer org's row changes in exactly two ways:

- **A successful poll** writes KyIdentity's status, including back to `active` from `stale`.
- **`ResetAfterRestore`** sets every non-tombstone customer org to `stale` (Lifecycle,
  Restore). This is the only source of `stale`.

A failed poll changes nothing: every row keeps its last answer. A short KyIdentity outage therefore
leaves serving orgs serving, and KyIdentity's own token lifetimes and back-channel logouts still
apply.

### `OrgStore`

`store.Org(id OrgID)` returns a handle that binds `org_id` in its queries. It holds the methods that
take a request-chosen ID of an O table (users, groups, memberships, grants, suite groups). Their
unscoped versions are removed, not kept beside them. Methods on D tables keyed by the caller's own
user ID stay on the store: another org's row cannot match the caller's user ID, so it answers as a
missing one, which `TestCrossOrgMatrix` proves route by route (as built in PRs 2a–2d). Requests get
their handle from middleware:

- `OrgID` is built only from an authenticated principal: a session's user, an app password's owner
  (DAV and MCP, `internal/api/dav_auth.go:302-325`), a KyPost-signed request's resolved account
  (`internal/api/itip_handlers.go:180-194`), a booking slug's host, or a staff selection on a
  staff route (Admin surface).
- Single-tenant binds the default org, so handlers have one path.
- **Pre-org lookups** stay on the unscoped store, because they find the principal that decides the
  org. They are an allowlist in code: session by token hash, app password by ID, user by SSO
  subject (`sqlstore.go:377-386`), user by username at local sign-in, booking type by slug, booking
  by confirm-token hash.
- **Platform workers** (outbox delivery, subscription refresh, meetings and presence outboxes, suite
  groups, pruning, backup) read across orgs through a `PlatformStore`, built in PR 4, where
  suspension first needs a worker to know a row's org. Every row it returns carries `org_id`, and
  each unit of work runs under that row's `OrgStore`. Until then a worker acts only on its row's own
  account: delivery is external in multi-tenant mode (D5), and a feed refresh writes into its
  subscriber's calendar, whose objects take that calendar's org.

### The install-wide lookups this changes

Each is a lookup today that is scoped by the install. In multi-tenant mode each is scoped by the
caller's org.

| # | Where | Today | Multi-tenant |
|---|---|---|---|
| L1 | `CalendarUser`, `internal/store/scheduling.go` | One active everyday account with that email anywhere | Nobody (D5) |
| L2 | `Address`, `internal/davbackend/scheduling.go:155-169` | The shared-email rule across the install | Unchanged, through `EmailOwner`. KyIdentity's global emails (T D5) make a cross-org twin impossible. `organizerEdits` uses it too: it only recognises an organizer's own object in a calendar both can edit, and another org's account holds no grant there (L10). |
| L3 | Internal delivery, `scheduling.go:339-341` | A local recipient gets the message written into their calendar (`deliverToAttendee`, `:477`) | Never. Every recipient goes out through KyPost (`sendExternal`, `internal/davbackend/external.go:36-79`). |
| L4 | `Plan`, `scheduling.go:204` | Marks attendees internal for the invite budget | None internal |
| L5 | CalDAV outbox free/busy, `internal/davbackend/surface.go:96-114` | Busy times of any account on the install (`2.0`), else `3.7` | `3.7` for every address, as for a stranger |
| L6 | `GET /api/freebusy`, `internal/api/freebusy.go:56-75` | Busy times of any account, else `unknown: true` | `unknown` for every address, as for a stranger |
| L7 | Out-of-office comment, `internal/davbackend/out_of_office.go:104-107` | The local comment for any organizer on the install | The remote comment for everyone |
| L8 | Meeting membership, `internal/api/events.go:264-270` | The video link stays clickable for any organizer on the install | Plain text for every other organizer |
| L9 | Group-calendar cancellation, `internal/api/calendars.go:320-345` | Cancels as any organizer on the install | Sends nothing |
| L10 | `SetGrant`, `internal/store/calendars.go:178-198` | Checks only that the group exists | `(org_id, id)`, plus the composite FK. A group ID from another org is `404`. |
| L11 | Suite group members, `internal/store/suite_groups.go:161-167` | Subjects in the issuer scope | Subjects in the scope **and** the suite group's org |
| L12 | Admin lists: `ListUsers`, `ListAuditRecords` (`sqlstore.go:1766`) | Whole install | Staff routes only, bound to the selected org (Admin surface) |

Paths that are already safe across orgs, because they key on the caller and never on a name:

- A CalDAV home other than the caller's is `403` before any lookup (`internal/api/dav.go:37-42`,
  `AGENTS.md:297`). A path naming a missing user and one naming another org's user give the same
  answer.
- A calendar the caller cannot read is `404` on every API and CalDAV route (`AGENTS.md:335`,
  `internal/access/role.go:43-62`). Personal calendars are owner-only, and group calendars need a
  grant through the caller's own groups.
- MCP tools see personal calendars only (`internal/api/mcp.go:203-230`).
- Booking types, task views, goals, areas and preferences are read by the caller's user ID.

### Tests

- `TestEveryTableIsClassified`: extends `TestMigrationTablesCoverTheSchema`
  (`internal/store/roundtrip_test.go:128`), which already fails on a table missing from
  `migrationTables` (`internal/store/snapshot.go:23-37`). Every table must be in exactly one of
  `orgOwned`, `derived` (with its parent FK), `platform` or `off`.
- `TestOrgOwnedTablesHaveOrgID`: a non-null `org_id`, the existence and immutability triggers and,
  on Postgres, the FK to `orgs`. The only nullable exception is `audit_records`.
- `TestEveryRouteIsInTheCrossOrgMatrix`: every registered route has a `TestCrossOrgMatrix` row or
  a stated reason no other org's ID reaches it (own rows only, addresses under D5, KyIdentity as the
  caller, a signed peer, public by slug or token, pre-auth, DAV, platform, staff admin, or off under
  D9). A new route fails until it has one of the two. It replaces a planned go/ast scan of store
  SQL for `org_id`: 239 store queries name no `org_id`, almost all keyed by the caller's own user
  ID, so that scan would have been a 239-entry allowlist (owner decision, 2026-11-21).
- **`TestCrossOrgMatrix`**, built on `internal/api/authz_matrix_test.go`. That file already fails
  when a registered route has no row (`TestEveryRouteIsInTheMatrix`, `:382-408`), and its world has
  one persona per role (`:28-39`, `newWorld` at `:90-207`). The cross-org version:
  - Builds orgs A and B, each with an owner, a group calendar with reader, editor and manager
    grants, a non-member, a deactivated member, DAV and MCP tokens and a booking type. It also
    builds the staff org with a staff admin.
  - Calls every row as each A persona with B's object IDs (calendar, event, task, group, booking
    type, view, goal), and every DAV operation on B's paths. Each answer must equal the answer
    for a random ID that exists nowhere: same status, same body code, same headers apart from
    `Date`. B's rows must be unchanged.
  - Writes an A event inviting B's member, then runs delivery. Expects nothing written in B, and
    one KyPost send.
  - Asks free/busy (API and outbox) as A for B's member and for a never-used address. Expects
    byte-identical answers.
  - Sends each KyPost and KyMessages inbound route with B's member and A's `org_id`, and with no
    `org_id`. Expects `404 no_user`, as for an unknown subject.
  - Runs on Postgres in the `postgres-tests` leg.

## Identity binding

### Sign-in (ID token)

Today a login looks up the account by `(sso_provider, sso_subject)`, then refuses a row bound to
another issuer (`internal/api/sso_handlers.go:69-110`). New accounts are created on first login
when `KY_SSO_AUTO_PROVISION` is on (default on, `internal/config/config.go:377`). Claims come from
`claimsFromIDToken` (`internal/sso/oauth.go:179-202`), which reads no `org_id` today.

In multi-tenant mode:

1. `claimsFromIDToken` also reads `org_id`. A token without it is refused: `403`, audited
   `auth.sso_refused reason=no_org`, and no row is created or changed.
2. The org must be in the `orgs` mirror, `active` and `enabled`. An org not yet mirrored triggers an
   immediate poll, and the callback waits up to 5 s for it. Polls are single-flight: a sign-in joins
   one already running instead of being refused for it. Each unknown org ID can trigger a poll at
   most once every 10 s. The ID comes from a verified token or signed webhook, so only KyIdentity
   can name one. Only if the org is still unknown or not active
   after that poll is the login refused `403 org_not_active`. A new customer's first sign-in right
   after signup therefore succeeds. KyIdentity would already have refused a
   suspended org (T "Suspension"). This check covers the poll window and a disabled app.
3. **New account:** created with `org_id` from the token, in the same transaction that inserts the
   user. `users.org_id` is immutable (trigger).
4. **Known `sub`, different `org_id`:** refused `403`, audited `auth.sso_refused reason=org_mismatch`
   with both org IDs, and nothing changes. T makes `users.org_id` immutable in KyIdentity, so this
   means a KyIdentity bug or a forged token. It never moves an account.
5. `kycalendar.admin` grants the admin role only under D7.

The org comes only from the verified ID token here. The session then carries the user, and the
user carries the org. No request header, path or form field ever names an org for a member.

### Directory webhook

> **Gap found while building PR 3 (2026-11-21).** KyIdentity no longer sends the payload below.
> Its lifecycle transport is the signed suite connector: `syncauth`-signed SCIM User bodies, which
> carry `org_id` in multi-tenant mode (KyIdentity `internal/sync/sync.go`, `SCIMUserResource`).
> KyCalendar receives them at `POST /api/installer/directory`, and a managed
> `/api/sso/kysignon/sync` delegates there. That receiver only updates accounts the installer
> assigned (`internal/store/installer_directory.go`). A self-signed-up customer's account is not
> assigned, so it would be refused and KyIdentity would retry it. Multi-tenant mode needs a
> receiver for any account of a mirrored org, with the rules below applied to the SCIM body.
> That is open question 10 and its own PR (3c). The rules below stay the target.

`/api/sso/kysignon/sync` (`internal/api/server.go:312`) verifies an HMAC-SHA256 of the raw body
(`internal/sso/kysignon.go:39-56`, `internal/crypto/crypto.go:87-97`), so a field T adds to the
body is authenticated. Today's payload has no org (`kysignon.go:28-36`).

In multi-tenant mode the payload must carry `org_id`. The rule for a missing or mismatched one depends on what the event would do (below).

`user.created` for an unknown `sub` follows the same org rule as sign-in:

- If the org is not mirrored yet, KyCalendar runs the same single-flight poll as sign-in, limited
  per org ID, and waits for it. A new customer's first members usually arrive inside the 60 s gap.
- If the org is mirrored, `active` and `enabled`, the account is created in that org.
- If it is still unknown, or not `active` or `enabled`, nothing is created, and the event is
  audited `sso.webhook_refused reason=org_unknown` or `reason=org_not_active`.

A skipped create loses nothing. Auto-provisioning is required in multi-tenant mode (D2), so the
person's first sign-in creates the account once the org serves (Sign-in, step 2). So the insert never reaches the existence trigger
with an unmirrored org.

For a known `sub` whose event has a missing or mismatched `org_id`, the rule follows back-channel
logout (below): **removal is always applied, additions never are.**

- `user.deactivated`, and a `user.updated` whose status is not active, end access exactly as today
  (`RevokeSSOUser`, `internal/store/sqlstore.go:1109-1142`). That covers sessions, app passwords
  (DAV and MCP), MFA challenges and pairings.
- `user.deleted` ends access in the same way but **does not delete the account**. Today it deletes
  the row and its calendars (`internal/sso/kysignon.go:117-130`), and a deletion on an
  unconfirmed org would be a destructive guess. The account stays deactivated with its data, and
  the org's purge or a correct `user.deleted` removes it later.
- Profile changes (name, email) are not applied.
- Each is audited `sso.webhook_org_mismatch` with both org IDs, or `reason=no_org`.

So a KyIdentity bug that drops the field can never leave a removed person signed in. The webhook
still only takes access away from an existing account (`AGENTS.md:331`). Every event answers `200`,
as an event for another binding's account does today (`internal/sso/kysignon.go:75-79`), so
KyIdentity does not retry it.

### Back-channel logout

`POST /api/sso/kysignon/backchannel-logout` (`server.go:313`, `internal/sso/backchannel.go:24-50`)
ends sessions by `sid` or by `sub` (`sso_handlers.go:387-392`). A logout only takes access away.
So a logout token with a missing or mismatched `org_id` **still ends** the matching sessions, and
is audited `auth.sso_org_mismatch`. Refusing it would leave a session alive that KyIdentity meant
to end.

### Suite answers

- `/api/suite/orgs` (D6) is the only source of org status, enablement and quotas.
- `/api/suite/groups` requires `org_id` in multi-tenant mode (T "Suite directory").
  `SyncSuiteGroups` (`internal/api/suite_groups.go:23-64`) polls once per active, enabled org.
  Each group's calendar and local group take that org, and members must be accounts of that org
  (L11).
- `/api/suite/people?sub=` lists only the `sub` user's org (T). KyCalendar sends the caller's own
  subject (`internal/api/suite_people.go:27-34`, `internal/api/video_meetings.go:386-398`), so the
  people picker, meeting members, presence lookup and chat targets stay in the caller's org. A
  returned subject that maps to a local account of another org is dropped.

### SCIM and local accounts

- SCIM (`/scim/v2`, one static install token, `internal/scim/handler.go:56-84`) is off in
  multi-tenant mode (D9). The signed webhook carries lifecycle.
- The password login (`/api/auth/login`, `internal/api/auth_handlers.go:39-118`) admits only staff
  break-glass accounts in multi-tenant mode (D8). `create-user` refuses. `init-admin` and bootstrap
  create the account in the staff org.

## Calendar specifics

### CalDAV and other protocol endpoints

| Endpoint | Authenticates by | Org binding |
|---|---|---|
| `/dav/`, `/.well-known/caldav` (`server.go:459-460`) | HTTP Basic: username plus a `dav` app password `kc_<id>_<secret>`. Token ID first, then owner, then a username match (`dav_auth.go:193-249`, `:284-325`). | The token's owner, so the owner's org. A token never names an org. The home rule (`dav.go:37-42`) keeps the path to the caller's own tree. |
| `/mcp` (`server.go:345`) | Bearer MCP app password (`mcp.go:73-140`) | The token's owner's org |
| Web API | `ky_session` cookie, or the raw session token as a Bearer (`internal/auth/session.go:204-254`) | The session user's org |
| KyPost inbound (`/api/itip/*`, `/api/kypost/out-of-office`, `/api/tasks/flag`) | syncauth v1 under the pair secret (`itip_handlers.go:48-89`) | The resolved account's org, which must equal the body's `org_id` (D10) |
| Public booking (`/api/book/*`, `server.go:439-445`) | Slug, or a mailed confirm or manage token | The host's org (Public links) |

Every authenticated path also requires the account's org to be `active` and `enabled` in the
mirror, read in the same query as the account. That query is in `AuthenticateRequest`
(`session.go:230-241`) for sessions and in `appPasswordOwner` (`dav_auth.go:302-325`) for DAV and
MCP. A session in a non-active org is deleted, as an inactive account's is today (`session.go:238`).
DAV and MCP answer `403 org_not_active`. That answer needs a valid app password, so it reveals the
state only to the account's own holder, as T owner answer 4 does.

### Sharing, invitations and free/busy

- **Inside an org**: group calendars through suite groups (D9) and grants (L10). Invitations and
  free/busy between colleagues work as across orgs (D5), by email.
- **Across orgs**: only by email, deliberately. No attendee is a calendar user in multi-tenant
  mode (D5), so `deliver` hands the message to KyPost (`scheduling.go:339-341`).
  KyPost mails it. If the address is a hosted KyPost mailbox, KyPost forwards it to
  `/api/itip/inbound` for the recipient's own account. A new invitation there is never written: it
  waits as `pending` until the recipient answers through `/api/itip/respond`
  (`internal/davbackend/external.go:103-163`, `AGENTS.md:310-311`). The answer goes back the same
  way. Nothing crosses the boundary without the recipient's own action.
  - Inbound needs `dkim_aligned` (`itip_handlers.go:203-207`). Whether KyPost marks mail between two
    hosted mailboxes as aligned is unverified (open question 6).
- **Free/busy** answers every address like a stranger's (L5, L6), so it reveals nothing about
  any account.
- **Scheduling switch**: until the device gate passes, launch deployments pin
  `KYCALENDAR_SCHEDULING=false` (`AGENTS.md:323`). With scheduling off, nothing is delivered
  internally or by mail, so nothing crosses orgs at all.

### Public and subscription links

- **Booking pages** (`/book/<32 hex>`). The slug is a 128-bit random value, globally unique
  (`mig.go:700`), so one public namespace is safe. In multi-tenant mode a page answers only while:
  - the platform switch `booking_pages_allowed` (`internal/api/booking.go:26`) is on;
  - the host's org is `active` and `enabled`, as on every other path, and its `booking_pages`
    quota is true (D11);
  - the existing conditions hold: scheduling on, KyPost linked, and an address of the host's own
    (`booking.go:43-74`).

  Otherwise the page is `404`, like an unknown slug. A confirmed booking is written as the host
  (`booking.go:718-739`). No booker is a calendar user (D5), so a booker gets mail, never an
  internal write.
- **ICS subscriptions** are fetches out to the internet, not published links
  (`internal/feed/feed.go`, SSRF guard `feed.RefusePrivate`, `internal/api/server.go:228`). The
  calendar is the subscriber's own. Nothing changes.
- **Outbound publishing**: KyCalendar serves no ICS feed of a calendar today. No route serves one
  (`server.go:294-464`). This spec adds none.
- `/agent/skills/kycalendar/SKILL.md` and `/healthz` carry no user or org data.

### Peer integrations

Wire formats: `internal/kypost/kypost.go:21-107` and `internal/kymessages/kymessages.go:63-107`,
`internal/kymessages/presence.go`. Contract: `docs/SUITE-INTEGRATIONS.md`. Every body names the user
by `sub` and `issuer` (`docs/SUITE-INTEGRATIONS.md:25-27`). None carries an org today.

| Message | Direction | Change in multi-tenant mode |
|---|---|---|
| `kycalendar.itip.send` (`SendRequest`, `kypost.go:45-51`): invitations and booking holds | out to KyPost | Adds `org_id`. KyPost must check it against its account for `(sub, issuer)`. |
| `kycalendar.task.state` (`kypost.go:103-107`) | out to KyPost | Adds `org_id` |
| `kypost.itip.inbound`, `kypost.itip.respond`, `kypost.ooo.set`, `kypost.task.flag` (`kypost.go:55-100`) | in from KyPost | Requires `org_id` equal to the resolved account's (D10). Otherwise `404 no_user`, the unknown-user answer (`itip_handlers.go:48-89`). |
| `kycalendar.meeting.put`, `.close` (`kymessages.go:66-73`, `:186-189`) | out to KyMessages | Adds `org_id`. `member_subs` come from `/api/suite/people` for the organizer's subject, so they are the organizer's org. KyMessages must refuse a member sub outside the body's org. |
| `kycalendar.chat.open` (`kymessages.go:104-107`) | out to KyMessages | Adds `org_id`. The target is resolved in the caller's org. |
| `kycalendar.presence.busy`, `.me`, `.lookup` (`internal/kymessages/presence.go:12-14`) | out to KyMessages | Adds `org_id`. KyMessages must answer a lookup only for subjects in that org. |
| KyDrive picker (`docs/SUITE-INTEGRATIONS.md:270-300`) | browser only | No server payload. KyDrive's org link decides who can open the file. |

The pair secret stays per pair (T `/api/suite/peer`). A peer that holds it can speak for any
account of any org. That is accepted because every peer is platform-run (Residual risks). The
replay ledger (`kypost_replay`, keyed by event ID) stays global: event IDs are unique per sender,
not per org.

## Admin surface (multi-tenant)

Administrators are staff (D7, D8). `access.Resolve` already gives administrators `None` on every
calendar (`internal/access/role.go:44-46`), and DAV, MCP and app passwords refuse them
(`AGENTS.md:301`). **Staff never read calendar content**, which matches T ("staff … cannot read app
content").

| Route (`internal/api/server.go`) | Multi-tenant |
|---|---|
| `GET /api/admin/users` (`:360`) | Read-only, and needs `?org=<id>` (`400 org_required` without it). It lists that org's accounts: status, role, last sign-in and usage. The org is the one request-chosen org, reachable only after `requireAdmin` (`:520-532`) and D7 have passed. It writes a `staff.read` audit row (see below). |
| `POST`/`PATCH /api/admin/users…`, `role`, `disable`, `enable`, `reset-password`, `reattach` (`:361-367`) | `404` for every account (D8). Customer lifecycle is KyIdentity's (T member endpoints). Staff break-glass accounts are managed only on the host, with `init-admin` and `reset-password` (`AGENTS.md:254-257`). |
| `/api/admin/groups*`, `/api/admin/calendars*` (`:350-359`) | `404` (D9) |
| `/api/admin/resources*` (`:425-427`) | `404` (D9) |
| `GET /api/admin/scim` (`:369`) | Shows SCIM off |
| `GET /api/admin/signin` (`:370`) | Read-only. `PUT` and `POST /test` (`:371-372`) are refused (D2). |
| `GET /api/admin/audit` (`:368`) | Platform rows by default. `?org=<id>` reads one org's rows and writes a `staff.read` row. |
| `GET /api/admin/kypost`, `/kymessages` (`:373-374`), `/api/admin/booking` (`:432-433`), backups (`:319-326`), theme and rate-limit networks (`:330-332`) | Platform settings, unchanged, admin and step-up as today |
| New `GET /api/admin/orgs` | Read-only: the mirror (status, revision, enabled, quotas, usage, last poll). Staff need it to answer a customer about a quota. |

`staff.read` rows are **platform** rows (`org_id` NULL), with the target org's ID and the route in
`details`. So they survive the org's purge, which deletes only the org's own audit rows. The record
of which staff read a deleted customer's directory then outlives the customer, and ages out with the
rest of the audit log (365 days, `AGENTS.md:303`). The org ID is opaque, so the row holds no
customer data.

Single-tenant keeps every route as it is today.

## Lifecycle

The mirror poll (D6) drives every transition. It runs on the elected worker beside the other
60-second jobs (`AGENTS.md:476`). Status is read with the account on every request, so a change
reaches every replica at the next request.

**Suspended or `deletion_scheduled`** (stop serving, keep data):

- In one transaction with its audit row: delete the org's sessions.
- Queued outbox messages are not changed. They stay `pending`, and delivery skips any row whose
  org is not `active`. So their bytes keep counting toward `max_bytes` (Quotas), and a resume adds
  nothing that was not already admitted.
- Sign-in, web, DAV and MCP refuse (Calendar specifics). App passwords are kept, so a resume brings
  back DAV clients without new setup.
- KyPost inbound for the org's accounts answers `404 no_user`. KyPost keeps the mail.
- Booking pages of the org's hosts are `404`.
- Workers skip the org: subscription refresh, meeting and presence outboxes, suite groups and
  outbox delivery. A skipped message is still abandoned 24 hours after it was built
  (`internal/davbackend/scheduling.go:131,266`), so a long suspension drops pending invitations.
  This is accepted: a sender in a suspended org cannot follow up anyway.
- Nothing is deleted.

**Resumed:** delivery picks the org's pending messages up again. Sessions do not come back, as in T.

**Deleted** (purge). Every object write in every org takes the `calendar-total` lock
(`internal/store/calendars.go:322,483`), so one transaction for a large org would block writes in
all the others, past the 14 s request deadline (`AGENTS.md:356`). The purge therefore runs in short
batches, on the elected worker:

1. Write the platform audit row `platform.org_purge_started` (org ID, no content). If it cannot be
   written, nothing is deleted (fail closed), as in T. The org is already refused everywhere, since
   its status is `deleted`, so nothing new is written into it while the purge runs.
2. Delete objects in batches under `calendar-total`: at most 500 objects or 4 MiB per transaction,
   whichever comes first. Each object's cascades go with it (changes, reservations, MCP and
   meeting rows), and the usage triggers adjust `calendar_usage` and `org_usage` in the same
   transaction. The batch bounds rows and bytes, not owners. A group calendar's objects are
   charged to many writers (`internal/store/invitation_charges.go:20-26`), so one calendar can hold
   far more than one user's quota. When a calendar is empty, a small transaction deletes it with
   its grants and subscription. Other orgs' writes wait for at most one batch. The 500 and 4 MiB
   are starting values, measured in PR 4.
3. One final transaction:
   - delete `sso_subject_logouts` rows whose `subject` is one of the org's users'
     `sso_subject`, before the users (the table is P with no `org_id`, so the users rows are its only
     map to the org);
   - delete the org's groups and users (sessions, app passwords, bookings, tasks, meetings,
     out-of-office and presence cascade);
   - delete its rows in the O tables without FKs: `scheduling_outbox`, `invitation_byte_budget`,
     `device_pairings` and the org's own `audit_records`;
   - delete `org_usage`;
   - set `orgs.purged_at`, and write `platform.org_deleted` with the row count per table and no
     content.

The purge is idempotent. A crash between batches leaves `purged_at` unset, and the next worker run
continues from the calendars that are left. The batch size and the purge's total time are measured
in PR 4 (open question 4).

A test proves the purge leaves no row with the org's ID, or naming one of its users, in any
O or D table, checked against the table classification. A second test kills the purge between
batches and proves the next run finishes it.

**Restore:** a capsule is the whole install (`internal/backup/payload.go:45-143`), so a restore
can bring back an org deleted or suspended since the capsule was made. In multi-tenant mode
`ResetAfterRestore` (`internal/store/sqlstore.go:81-131`) also sets every customer org's mirror row
to `stale`, apart from tombstones, and its revision to 0, in the same transaction as the rest of
the reset. A restored row therefore never serves on its capsule-time `active`.

Every access check (sessions, DAV, MCP, booking pages, KyPost inbound) refuses a `stale` org as it
refuses a suspended one. Only the next successful poll writes KyIdentity's current status back, and
that poll re-reads every org from revision 0. The purge then runs again for any org now `deleted`.

This relies on a KyIdentity contract that T implies but does not state: a full listing from
revision 0 includes every org ever created for the calling app, with tombstones as `deleted`. T
keeps every `orgs` row as a tombstone and never reuses an ID ("Data model"), so the data exists.
Open question 9 asks KyIdentity to state the listing rule.

Until it does, KyCalendar never purges on absence. A customer org missing from a complete full
listing stays `stale`. It is unserved, its data is kept, and it is flagged on `GET /api/admin/orgs`
for staff. Staff confirm the org's state in KyIdentity, then run `kycalendar purge-org -org <id>`,
which runs the same purge. Absence can also mean "KyCalendar not enabled" or a KyIdentity bug, so a
deletion on absence alone would be a destructive guess.
If the poll keeps failing after a restore, the restored orgs stay `stale` and unserved. That is the fail-closed choice: KyIdentity
is down then anyway, so nobody can sign in. The staff org is not touched, so staff can still reach
the admin screens. A test restores a capsule taken before a deletion and a suspension, then proves
every credential type is refused until the poll, and that the poll purges the deleted org.

**Export:** KyIdentity's org export holds directory data only (T "Replicas, Postgres and backups").
Calendar content is KyCalendar's. While an org is active, every member can already copy their own
calendars with any CalDAV client. During `deletion_scheduled` nobody in the org can reach
KyCalendar (T's export-only session opens no suite session). v1 therefore adds an operator command,
`kycalendar export-org -org <id> -out <dir>`: one `.ics` per calendar, grouped by member, and a
manifest of IDs. Staff run it on the account holder's request, like T open question 9 (c). See open
question 1.

**Quotas** (D11):

- `max_bytes` is checked beside the per-owner and instance checks
  (`internal/store/calendars.go:396-445`), under the same `calendar-total` lock. It counts the same
  bytes as the owner check, `ownerUsage` (`internal/store/invitation_charges.go:20-45`):
  - stored objects: `org_usage`;
  - queued outbox bytes: `SUM(LENGTH(body))` of `scheduling_outbox` rows with `status = 'pending'`
    and the org's `org_id`, which is O for this reason;
  - cancellation reservations: `cancellation_reservations` joined to `calendars` on
    `calendar_id`, for the org's calendars (`mig.go:851-858`), plus the pending cancellation growth
    `ownerUsage` already adds.

  The last two are summed in the check, as `ownerUsage` does for one sender, rather than kept by
  triggers. So `org_usage` holds object bytes only, and the backfill from `calendar_objects` is
  complete. Every row counted belongs to the org: delivery never crosses orgs (D5), so a sender's
  outbox rows and reservations are on the sender's own org's calendars.
- `max_bytes_per_user` replaces `KY_CALENDAR_MAX_BYTES_PER_USER` for the org's owners.
- A lower quota never deletes data. Writes that grow the org are refused (`507` on CalDAV, the
  existing quota answer on the web) until usage is under the limit.
- The other caps stay install constants: calendars, objects, subscriptions, booking types, app
  passwords and the invite limits (`internal/config/config.go:263-264`, `internal/feed/feed.go:32`,
  `internal/booking/booking.go:23`).

## Downstream contract (T "Tokens and suite contracts"), KyCalendar's half

| # | T requires | KyCalendar today | KyCalendar in multi-tenant mode |
|---|---|---|---|
| 1 | Refuse a token without `org_id` | Reads no org claim (`internal/sso/oauth.go:179-202`) | ID token without `org_id`: `403`, nothing written (Identity binding). Webhook without it, or with another org: removals applied (deactivation, and a delete downgraded to deactivation), additions and profile changes ignored (Directory webhook). A logout token without it still ends sessions. Removal is always safe on both paths. |
| 2 | Key every row by `org_id` | No org anywhere | `org_id` on every O table, D tables through their parents, composite FKs where a table joins two org rows, and triggers for existence, immutability and owner agreement (Isolation) |
| 3 | Bind a local account to `(sub, org_id)`, and refuse a known `sub` with a different `org_id` | Bound to `(sso_provider, sso_subject)` plus `sso_issuer` (`sso_handlers.go:69-110`) | `users.org_id` set at creation from the token or webhook and immutable. A mismatch is refused and audited, and never moves the account. |
| 4 | Take the org only from a verified token, a signed webhook or a suite answer | n/a | Org sources: ID token, the signed webhook body, `/api/suite/orgs` and `/api/suite/groups` answers. Peer `org_id` is a check only (D10). The only request-chosen org is a staff selection on a staff read route, after the admin check (Admin surface). |
| 5 | Stop serving an org that is not `active`, and keep its data unless `deleted` | n/a | The mirror poll, the status read with every account, and the suspension and purge rules (Lifecycle) |
| 6 | Enforce its own quotas from `/api/suite/orgs` | Install and per-owner caps (`config.go:296-307`) | `max_bytes`, `max_bytes_per_user` and `booking_pages` (D11) |

## Threat model

| Attacker | Goal | Defence |
|---|---|---|
| Member of org A | Read or change org B's calendars, events, tasks or bookings by putting B's IDs in API, CalDAV or MCP calls | The DAV home rule (`dav.go:37-42`) and `access.Resolve` (`role.go:43-62`) key on the caller. Every O and D query goes through an `OrgStore` bound to the caller's org. A cross-org ID answers exactly like a missing one, as `TestCrossOrgMatrix` proves. |
| Member of org A | Write into org B's calendar by inviting a B member | No calendar user in multi-tenant mode (D5, L3). The invitation goes out as mail and waits `pending` until the B member answers it themselves. |
| Member of org A | Read a B member's busy times, or learn whether an address is a customer | Free/busy is per org (L5, L6). A B member answers like a stranger, byte for byte (matrix test). |
| Calendar manager in org A | Grant an A calendar to an org B group by its ID | `SetGrant` resolves `(org_id, id)` (L10), and the composite FK refuses the write at the database |
| Customer given `kycalendar.admin` by a staff mistake | Gain administrator access | D7: admin only in the staff org. A customer account is always an everyday member. |
| Forged or replayed directory webhook | Create an account in another org, or move one | HMAC over the body, which carries `org_id`. The timestamp window is ±5 minutes (`kysignon.go:52`). A known `sub` with another or no org gets removals only, and never a deletion, profile change or create. `users.org_id` is immutable. |
| Buggy or compromised KyPost or KyMessages | Act on an account in another org | D10: the body's `org_id` must equal the account's, else `404 no_user`. Residual: a peer holding the pair secret can still send a correct `org_id` for any account (Residual risks). |
| User of a suspended org | Keep working through DAV, MCP, a cookie or a booking page | Org status is read with the account on every request (D6). Sessions are deleted at the transition, and DAV and MCP refuse. Back-channel logout and T's token revocation end KyIdentity's side. Window: one poll, at most about 60 s. |
| Former customer (deleted org) | Get data back, or have it linger | Batched, resumable purge behind a fail-closed audit row. The tombstone refuses later tokens and peer messages. A restore re-purges at the first poll. Residual: backups keep data until they age out. |
| Anonymous visitor to a booking page | Reach a host in another org, or a suspended host | The slug is a 128-bit random value. A non-active host org is `404`. The booker is looked up only in the host's org. |
| Staff, or a stolen staff account | Read customers' calendars | Administrators get `None` on every calendar and are refused on DAV, MCP and app passwords. Staff see the directory and usage only, and every org-bound read writes a platform audit row naming the org, which outlives a purge. T's staff controls (passkeys, audit) cover the KyIdentity side. |
| One org | Exhaust storage or the outbox for everyone | `max_bytes` and `max_bytes_per_user` (D11), the instance cap (`calendar_usage`) and the per-sender invite limits (`scheduling.go:43`, `invitation_budget.go:11`) |

## Phased build plan

Each PR ships on its own with single-tenant behaviour unchanged. `KYCALENDAR_TENANCY=multi` is
refused at startup until PR 6. Until then multi-tenant code runs only in tests.

0. **Prerequisites, elsewhere.** KyIdentity tenancy PR 3 (T "Phased build plan": the org claim,
   suite APIs and webhooks) and the `ky-primitives/suitedir` `Orgs` method. KyPost and KyMessages
   must check the `org_id` D10 adds before multi-tenant goes live (their own specs; open
   question 7).
1. **Org model and default-org migration.** Migration 37: `orgs`, `org_usage`, the `org_id` columns,
   the triggers, the composite keys, `tenancy_mode` for an existing install, and `ensureOrgs`.
   - Tests: an upgrade test from a version-36 database on both backends, with every row in the
     default org and every list and DAV answer unchanged. Also `TestEveryTableIsClassified`,
     `TestOrgOwnedTablesHaveOrgID`, and the full `make ci` and `make test-postgres`.
2. **Store scoping.** `OrgStore`, the pre-org allowlist, and L1–L11, split so each
   PR stays reviewable: (a) users, groups, grants and suite groups; (c) L1–L9 (every address
   external, D5), `org_id` written from the parent row on calendar, object, outbox and
   invite-budget inserts, and cross-org rows for calendars, booking types, task views and goals;
   (d) `org_id` on audit and device-pairing inserts (an audit row joins its user's org, or is a
   platform row when it names none), and the route-coverage guard. There is no (b): D5 replaced the per-org scheduler lookup. Bookings,
   tasks, meetings and presence are D tables read by the caller's user ID, so they need no store
   change, only the matrix rows.
   - Tests: `TestCrossOrgMatrix` and `TestEveryRouteIsInTheCrossOrgMatrix`, run on SQLite and
     Postgres. Existing tests pass unchanged on the
     default org.
3. **Identity binding and peer payloads.** `org_id` from the ID token, webhook and logout token,
   D7, D8, and `org_id` in every KyPost and KyMessages body, all gated on the mode. Split:
   (a) sign-in, back-channel logout, D7, and D8 for password sign-in and `create-user`. A sign-in
   is accepted only when its org is already mirrored and `active`. The poll that fills the
   mirror, and its wait, come with PR 4. The staff org is read from the `tenancy_staff_org`
   setting, which PR 6 records from `KYCALENDAR_STAFF_ORG_ID`. With none recorded, nobody is an
   administrator. `init-admin` and bootstrap in the staff org wait for PR 6. Refusing admin
   People writes waits for PR 5. (b) `org_id` in KyPost and KyMessages bodies (D10), filled by
   `store.PeerOrg` and checked in `kypostPeerRequest`, the one gate of all four KyPost routes.
   A mode read that fails answers 503 so KyPost retries.
   (c) the multi-tenant directory receiver (open question 10).
   - Tests: refusal without `org_id`, refusal of a known `sub` in another org, staff-org admin
     only, and peer bodies with a wrong or missing org answering `no_user`. Single-tenant golden
     peer bodies are byte-identical.
4. **Org lifecycle and quotas.** `PlatformStore`, the `/api/suite/orgs` poller and mirror, the status checks on
   every auth path, suspension and its holds, the purge, restore reconciliation, `export-org`, and
   D11.
   - Tests: a suspended org is refused on every credential type (session, DAV, MCP, booking page,
     KyPost inbound). A resume restores DAV with the same app password. The purge leaves no org
     row, checked against the classification. A restore of a capsule taken before a deletion
     purges again. `max_bytes` with two parallel writers on Postgres and one slot free (one
     succeeds).
5. **Admin surface.** The route changes in Admin surface, `GET /api/admin/orgs`, and staff audit
   rows.
   - Tests: the authorization matrix with staff personas in multi-tenant mode, every off route
     `404`, and the existing browser admin flows in single-tenant (`AGENTS.md:281`).
6. **Enable multi-tenant mode.** Startup accepts `multi` with its requirements (D1, D2,
   `KYCALENDAR_STAFF_ORG_ID`). An end-to-end run with KyIdentity in multi-tenant mode, KyPost and
   KyMessages: two orgs, an invitation across them by mail, and suspension and deletion of one.

## Residual risks

- **One writer for every org's calendar writes.** On Postgres every object write takes the
  `calendar-total` advisory lock (`internal/store/calendars.go:322,483`,
  `internal/store/sqlstore.go:170-176`). A busy org delays writes in every other org. Capacity is
  unmeasured (open question 4).
- **Shared pair secrets.** A compromised KyPost or KyMessages can speak for any account in any org,
  since D10's `org_id` is only as honest as the peer. All peers are platform-run.
- **Backups hold deleted orgs** until the capsules and local copies age out (`KYCALENDAR_BACKUP_KEEP`,
  `internal/config/config.go:257`, plus KyRecovery's own retention, unverified). A restore rolls
  back every org, then re-purges deleted ones.
- **Mirror lag.** A suspension reaches KyCalendar's DAV and MCP tokens within one poll, about 60 s.
  T's back-channel logout ends cookie sessions sooner.
- **Feed egress is shared.** Every org's ICS subscriptions fetch from the platform's addresses, so
  one org's subscriptions can get the platform rate-limited by a feed host.
- **Operator access.** `export-org` and the database give the operator calendar content. The
  operator is trusted with the database and keys, as in T.
- **Pending invitations drop** when a suspension outlasts the 24-hour outbox lifetime.

## Open questions, with recommendations

1. **Export during `deletion_scheduled`.** T's export-only session reaches no app.
   - (a) Staff run `export-org` on the account holder's request.
   - (b) KyIdentity's export page links an app-held export, which needs a new suite contract.
   - (c) KyCalendar serves read-only CalDAV to the org's existing app passwords during the grace
     period.
   - Recommend (a) for v1, the same answer as T open question 9 (c). (c) contradicts Downstream
     contract 5.
2. **Suite group polling cost.** `/api/suite/groups` needs one call per org each minute in
   multi-tenant mode. Recommend asking KyIdentity for `since=<revision>` on groups, or a
   platform-scoped listing that carries `org_id` per group, before org count passes a few hundred.
   Until then, poll only orgs that are `active` and `enabled`.
3. **How KyCalendar knows the staff org.** Recommend `KYCALENDAR_STAFF_ORG_ID` (D7) for v1. Revisit
   if `/api/suite/orgs` gains a `kind` field.
4. **Per-org write lock.** Recommend accepting the single `calendar-total` writer for v1 and
   measuring before launch (orgs, writes a minute), as T open question 8 does. If it fails, use a
   per-org lock and check the instance cap without the lock.
5. **Booking pages in v1.** Recommend on, behind the platform switch and the per-org
   `booking_pages` quota (D11), since they need nothing but KyPost.
6. **DKIM alignment between hosted mailboxes.** A cross-org invitation reaches KyCalendar only if
   KyPost marks hosted-to-hosted mail `dkim_aligned`. Unverified. Recommend that KyPost's tenancy
   spec state that its own hosted mail is aligned.
7. **Peer `org_id` checks on the other side.** KyPost has no tenancy spec yet (its masked-addresses
   note, `docs/superpowers/specs/2026-11-19-masked-addresses-hosted-design.md` D6). Unverified for
   KyMessages. Recommend that each peer's tenancy PR lists D10's messages as its own contract rows.
   KyCalendar's PR 3 ships behind the mode, so it can merge first.
8. **App passwords during suspension.** Recommend keeping them, refused while not active, so a
   resume needs no device setup. The alternative is to revoke them at suspension, which is simpler
   but makes every customer re-pair every device after a late payment.
9. **Tombstones in `/api/suite/orgs`.** Recommend that KyIdentity's step 3 states that a full
   listing (`since=0`) returns every org ever created for the app, tombstones included as
   `deleted`, and that `since=<revision>` returns each deletion once. KyCalendar's restore
   re-purge (Lifecycle) depends on it. Until then, an org absent from a full listing stays `stale`
   and is purged only by staff with `purge-org`.
10. **The multi-tenant directory receiver.** KyIdentity delivers account lifecycle as signed SCIM
    User bodies (Directory webhook). Recommend a receiver beside `/api/installer/directory` for
    multi-tenant mode. It would verify the suite connector signature, resolve the account by
    `(issuer, id)`, and apply the Directory webhook rules to the body's `active` and `org_id`.
    Removals are always applied, additions only within the mirrored org, and an unknown account
    is created only when its org serves. It answers `204` once applied, so KyIdentity stops
    retrying. Until it ships, a multi-tenant install learns of a deactivation only through
    back-channel logout and the next sign-in.

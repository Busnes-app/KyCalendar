# Content encryption at rest, KyCalendar design

Date: 2026-11-22. Status: proposed design. Nothing is built, and this change is docs only.

Owner decisions (fixed, not reopened here):

1. Encrypt event content per account at rest, with no new client apps. Native calendar apps keep
   working unchanged ("option 3").
2. Times, recurrence and attendees stay readable to the server, so availability, booking pages,
   rooms and invitations keep working ("option 2's readable times").
3. Leave the door open for real end-to-end encryption later ("option 1"): devices hold the keys
   and the server never decrypts.
4. Browser unlock is a passkey, with a calendar passphrase where the passkey cannot do it.
5. Recovery is a recovery code only. There is no escrow. Lose every unlock method and the content
   is gone; the times stay.
6. An account is encrypted at its first unlock. Until then it stays as it is today.
7. Both single-tenant and multi-tenant installs, on by default.

Line references are to KyCalendar `origin/main` at `c2e5bf0`. KyPost references are to
KyPost-Server `origin/main` at `fb0d6aab`. "Unverified" marks a claim this spec did not check in
code.

## Summary

The server splits every calendar object into a **skeleton** and **content**.

- **The skeleton** is an iCalendar object without the content properties. It is stored in
  `calendar_objects.data` as today, so every reader that needs only times or attendees keeps
  working unchanged: free/busy, presence, booking availability, time-range queries, resource
  conflicts and the restore drill.
- **The content** is the object exactly as the client sent it, byte for byte. It is sealed to
  the calendar's key in a new column and served back as it was when an unlocked request reads the
  object. The skeleton is a readable copy of its times, recurrence and attendees (title,
  description, location and notes are left out).

Each calendar has a key pair. Each account has a key pair whose private half is wrapped in
**slots**, one per unlock secret: each app password, each MCP token, each passkey, the
passphrase, the recovery code and each signed-in browser session. This is KyPost's design
(`docs/E2E_PGP.md:14-24`), with the same room for device slots later.

- **Requests from calendar apps** carry an app password on every request. The server unwraps the
  account key with it, for that one request only. This is why native apps keep working.
- **Background workers never unlock.** When they write content (invitations, bookings, feeds),
  they seal it to the calendar's public key, which needs no secret. They only ever read skeletons.

This is encryption at rest, not end-to-end encryption. A server that is compromised while it runs
sees content as requests pass through it. A stolen database, backup or capsule gives the times and
attendees, but no content.

| | Today | After |
|---|---|---|
| Stolen database, backup or capsule (they include `encryption.key`) | everything | times, recurrence, attendees, sizes for every calendar that has a key; full content for the exceptions in the Threat model |
| Operator reading the database or running `export-org` | everything | times, attendees; content only with the user's secret (reading only: see Threat model for writing) |
| Compromised running server | everything | everything that passes through while users are active |
| Native calendar apps | work | work, with no change on the device |
| Availability, booking pages, rooms, invitations | work | work |

## Decisions this spec makes

| # | Decision | Why |
|---|---|---|
| E1 | **Content properties are an allowlist of readable properties; everything else is content.** Readable: `UID`, `DTSTART`, `DTEND`, `DUE`, `DURATION`, `RRULE`, `RDATE`, `EXDATE`, `RECURRENCE-ID`, `SEQUENCE`, `DTSTAMP`, `CREATED`, `LAST-MODIFIED`, `STATUS`, `TRANSP`, `CLASS`, `PRIORITY`, `COMPLETED`, `PERCENT-COMPLETE`, `ORGANIZER`, `ATTENDEE` (with parameters, including `CN`), `VALARM`'s `ACTION` and `TRIGGER`, every `VTIMEZONE` component, and KyCalendar's own task markers `X-KY-REPEAT-FROM`, `X-KY-HABIT`, `X-KY-ESTIMATE`, `X-KY-SOURCE` and `X-KY-REF` (`internal/tasks/repeat.go:8-38`, `task.go:22-25`). Every other property, including every other `X-` property, is content. | Default to secret. An unknown vendor property, which can hold anything, is sealed. The readable set is what scheduling, availability, recurrence, alarms and task repeats need (Readers, below). The `X-KY-` markers are flags, a duration and KyPost's opaque message reference: a worker completing a repeating task from KyPost needs `X-KY-REPEAT-FROM` and `X-KY-HABIT` to move it to the right next occurrence, and finds the task by `X-KY-REF`. `CN` stays readable because invitations and the attendee list show it, and the attendee's address already identifies them. |
| E2 | **The skeleton stays in `calendar_objects.data`; the sealed column holds the whole original body.** An unlocked write seals the body exactly as received and stores its skeleton beside it. **The skeleton is the truth for readable properties; the base and patches are the truth for content.** A read takes each readable property (E1) and the set of components from the skeleton, and the content properties from the base with the patches (E5) applied. **A component in the skeleton that has no content in the base or patches** (an override a worker made for one occurrence, from a `REPLY` or a booking reschedule) takes the content of its master component, the one with no `RECURRENCE-ID`, as the stored copy would carry it today when a worker copies the master to make an override. When no patch applies and the skeleton still equals the base's own skeleton, the read serves the sealed bytes unchanged. Otherwise it serves the merged object re-encoded. | Every time-only reader keeps parsing `data` unchanged (Readers). The contract that PUT bodies are "stored and served as the raw bytes received" (`AGENTS.md:302`) keeps holding, so property order, folding and vendor properties survive, and the interop fixtures (`TestInteropFixturesSurviveWebEdits`) keep passing. Workers change readable properties (a rescheduled booking's times, an attendee's `PARTSTAT` from a reply or a room's answer) by rewriting the skeleton alone, as they rewrite `data` today, and every later read shows the change, so a client never gets stale times back to write over it. Bytes stay exactly as received until a worker changes the object, which is already when a worker rewrite re-encodes today. The readable properties are stored twice; quotas count only the sealed copy (E4). A legacy row has `sealed IS NULL` and holds the full object in `data`, which is how unencrypted accounts and the migration work. |
| E3 | **Key hierarchy: account key → calendar key → content.** Each account has an X-Wing key pair. Each calendar has a 32-byte seed. The seed gives an X-Wing key pair (for sealing without a secret) and an AES-256-GCM key (for unlocked writes). The seed is wrapped to each holder's account public key. | Grants are to groups (`calendar_grants`, `migrations.go:368-374`), so several accounts read one calendar; a per-calendar key lets each holder's own secret open it. X-Wing is the KEM already used for capsules and the recovery key (`ky-primitives/capsule/hpke.go`, `recoverykey`), so stolen content sealed today stays safe against a future quantum attacker. |
| E4 | **The sealed column is a list of parts: a base and then patches.** The base is a whole body. An unlocked write replaces every part with one base sealed under the calendar's AES key (28 bytes of overhead). A worker that writes a whole new object (a delivered invitation, a booking, a feed object, the sweep, E12) writes a base, and a worker that changes named properties of an existing object appends a patch (E5). **An organizer's update delivered to an attendee's existing copy appends a new base marked `keep-alarms`**: today that update replaces the copy with the organizer's object and keeps only the attendee's own `VALARM`s and `PARTSTAT` (`itip.AttendeeCopy`, `internal/itip/itip.go:611-615`). So the newest base gives every content property, which removes anything the organizer cleared, and the alarms come from the newest base before it that is not marked. **A new base drops what it supersedes, with no key needed**: an unmarked base replaces every earlier part, and a `keep-alarms` base replaces every earlier part except the newest unmarked base. An object therefore holds at most two bases (the alarms source and the newest update) plus at most 16 patches written after the newest base. Bases do not count toward the cap. `PARTSTAT` is kept or reset in the skeleton, as today. A worker run (one delivery, one feed refresh, one sweep batch) makes a fresh AES key, wraps it once with HPKE to each calendar it writes (`worker_keys`, about 1.1 KB), seals its parts under it (28 bytes each plus the key's ID), and forgets it when the run ends. It is never stored unwrapped. **Folding:** the next unlocked write or read of the object replaces every part with one base under the calendar's AES key and deletes worker keys that nothing references and whose run has ended. A run sets `ended_at` on its keys when it finishes; a key whose run died counts as ended one hour after `created_at`, and no run may last longer than that. A key in use by a live run is never deleted, even if no part references it yet. Every append increments `parts_version`. A fold updates the row only `WHERE parts_version` equals the value it read. If a worker appended in between, the fold changes nothing and the next read folds again, so no patch is ever lost. An unlocked write already holds the calendar lock that worker writes take. A read that folds keeps the ETag (below), so clients refetch nothing. **Cap:** an object holds at most 16 worker parts with distinct labels (E5). **Size:** `calendar.MaxObjectSize` always measures plaintext. The row keeps `plain_size`, the plaintext size of the merged object: an unlocked write sets it from the body, and a worker adds or replaces its part's plaintext size, which it knows. The owner's quota counts `LENGTH(sealed)`. Only a patch can be dropped: a worker patch past the cap, the size limit or the quota still makes its readable change in the skeleton but drops its content part, and audits `content_part_dropped` with the object, never the content. **A write that makes a base is never stored without it.** A first delivery, an organizer's update, an inbound invitation, a booking or a feed object over the limit is refused exactly as the same plaintext write is refused today (`len(raw) > calendar.MaxObjectSize`, `internal/davbackend/write.go:87`), and fails or retries as it does today. Sealing overhead never counts toward `MaxObjectSize`, because the limit measures plaintext. | A worker can write content without being able to read the existing content, and never holds a key that opens anything it did not write itself. Folding on reads as well as writes means parts pile up only on objects nobody with access opens, and the cap bounds those: a recurring meeting whose organizer never edits it again folds at the organizer's next sync. One HPKE wrap per run instead of per object keeps a 10,000-object sweep or feed near today's size. **Quotas count `LENGTH(sealed)` for an encrypted row and `LENGTH(data)` for a legacy row,** so the skeleton copy is free and sealing an existing object adds 28 bytes plus a key ID. The sweep is never refused by a quota or by `MaxObjectSize`: it adds no content, its body already passed both checks when it was written, and refusing it would leave content unencrypted. |
| E5 | **A patch is a list of operations on named properties**, never a whole component. Each operation names a component (`VEVENT`, `VTODO` and so on, plus its `RECURRENCE-ID`) and is one of: `set` (replace every instance of one property, for example `LOCATION`), `remove` (one property), or `conference` (the KyMessages room: set `CONFERENCE` and the final `Join:` description line, or remove both, with the rules of `calendar.SetConference` and `StripJoin`, `internal/calendar/conference.go:25-86`). Patches touch content properties only; readable ones change in the skeleton (E2). Patches apply in order on top of the base. **Each patch carries a readable label**: its operation, property and component key, never a value. A new patch with the same label as an older one replaces it in the same transaction, so a later room link or room location supersedes the earlier one and never counts toward the cap (E4). | A worker never sees the user's other content, so it must not be able to replace it. Setting one property leaves the title, description and vendor properties exactly as they were. `conference` is its own operation because the `Join:` line lives inside `DESCRIPTION`, which the worker cannot read. Applying it at read time keeps the existing rule that a later `Join:` replaces the earlier one. |
| E6 | **Unlock secrets.** Each slot wraps the account's private key under a key derived from one secret: an app password or MCP token (HKDF of its 256-bit secret, `internal/apppass/apppass.go:18-30`), a passkey (its WebAuthn PRF output), the passphrase (Argon2id), the recovery code (HKDF of 128 random bits), a local password where one exists (Argon2id), or a browser session (HKDF of the session token, which the server stores only as SHA-256, `internal/auth/session.go:65-66`). | Every request already carries one of these secrets. Unwrapping per request keeps replicas stateless: no unlocked key lives in any process between requests. A session slot dies with its session, so a stolen cookie database (hashes only) opens nothing. |
| E7 | **Passkeys for unlock only; KyCalendar verifies no assertion.** The browser calls WebAuthn with the PRF extension and a per-slot salt, and sends the PRF output over the signed-in session. A wrong output fails to unwrap. | Sign-in is already KyIdentity's job. The PRF output is the secret, so there is no server-side WebAuthn code to get wrong. A passkey registered with KyCalendar's relying-party ID belongs to KyCalendar, so another app's passkey cannot unlock it. |
| E8 | **The passphrase is the fallback**, for browsers or authenticators without PRF support. It is at least 12 characters and is stretched with Argon2id (`ky-primitives/password`). | Owner decision 4. A stolen database can be searched offline against a weak passphrase, so the setup page recommends the passkey and states the trade-off. |
| E9 | **The recovery code is 128 random bits**, shown once at setup as 26 base32 symbols in groups. It can be replaced while unlocked. It does not use `ky-primitives/recoverycode`, whose 60 bits (`recoverycode.go:29`) are too weak for a key someone can attack offline. | Owner decision 5. KyPost's recovery secret is also 128 bits (`frontend/src/lib/keyVault.ts:88`). |
| E10 | **A credential that cannot unlock an encrypted calendar is refused**, never served the skeleton. CalDAV gets `403` with a body that says to create a new app password; the web shows the unlock screen. | A calendar app that read a skeleton would show untitled events, and writing one back would erase the content. |
| E11 | **Existing app passwords get a 14-day transition.** At setup, the account key is also wrapped under the deployment key (`KY_ENCRYPTION_KEY`, purpose `content-transition`). Each existing app password or MCP token gets its own slot at its first use in those 14 days. The transition slot stays until `transition_until` passes, however many credentials have moved over, and is deleted then. After that, older credentials that were never used are refused (E10). | Without it, every device would have to be set up again at the moment of encryption. The window is the only time an operator with the deployment key can read the account, and the account's admin page shows when it ends. |
| E12 | **New calendars and accounts are encrypted from the first unlock.** An account with no key keeps writing legacy rows, as today. Its first web unlock runs setup: create keys, show the recovery code, register a passkey or set a passphrase. A background sweep then seals the legacy rows of every calendar that gets a key. **A calendar gets a key only when every account with access to it has an account key**: a personal calendar at its owner's setup, a group calendar once its last current member has run setup. The key is made at the next unlocked request of any member, wrapping the seed to every member's public key. Until then the group calendar stays legacy, readable to every member's existing credentials as today, and the admin page shows which members it waits for. Someone who joins a group whose calendar already has a key must run setup to read it, and is told so (E10). | Owner decision 6, applied per calendar: no member is locked out of a calendar they can read today because someone else set up encryption. Encrypting a shared calendar before every member can open it would break their native apps. Decrypting a keyed calendar for a new member would undo everyone else's encryption. Sealing needs only public keys, so the sweep needs no user present. Setup needs the web because the recovery code must be shown to the user. |
| E13 | **Sharing a calendar wraps its seed to each holder.** When a grant gives a new account access (a grant change, or a group membership arriving from the suite mirror), a `pending_key_shares` row is queued. Any current holder's next unlocked request completes it. Until then, the new member sees the calendar as locked. **Checks before sharing** (CE5): (1) a request that completes a pending share first re-checks that the target account has access now (`calendar_grants` and the membership), not only that a `pending_key_shares` row exists; (2) every unlocked request that opens a seed derives the calendar's public key from it and compares it with `calendar_keys.public_key`, and on a mismatch refuses the key for writing and sealing and audits `calendar_key_mismatch`; (3) every completed share is audited (`calendar_key_shared`, naming the calendar and the account) and listed under "Shared with" in each holder's security settings. Removing access deletes the holder's wrap, **except the last ones.** If a removal would leave no current holder (the group is deleted, or every member leaves or is deactivated), the wraps stay as *former* holders. Any unlocked request of a former holder's account then completes the calendar's pending shares, even though that account can no longer open the calendar through the server. Once a current holder has the seed, the former wraps are deleted. Deleting a group or its last member warns the admin when the calendar would become unreadable, which happens once no former holder's account remains. | The seed can only be wrapped by someone who can open it. Group calendars have members who are active, so pending shares clear quickly. Group calendars outlive their group (deleting a group keeps the calendar), so the content must survive the gap until the next grant. A former wrap gives no access the server would allow, and its holder could already read the seed. The seed is not rotated on removal: the removed member could already read it, and the times were never secret. |
| E14 | **Outgoing scheduling messages** are still created inside the sender's request. Every queued body, for internal and external recipients alike, is sealed under the deployment key (purpose `outbox`) until delivered or sent, and blanked afterwards as today (`internal/store/scheduling.go:154-158`). **Delivery to an internal recipient decides the format at delivery time,** inside the delivery's transaction under the calendar lock: if the target calendar has a key then, the worker opens the body and writes it as a base sealed to that calendar's current public key (E4). If it has none (its owner has not run setup, or a group calendar still waits for a member, E12), it writes a legacy row, as today; a later sweep seals it. The dedupe key becomes an HMAC under a deployment-derived key instead of SHA-256 over the body (`davbackend/scheduling.go:188`). | The worker that delivers or sends never needs the sender's key. Deciding at delivery means a recipient who runs setup, or resets and gets a new calendar key, between queue and delivery always gets the right format. Sealing at queue time could seal to a key that no longer exists, or to none. An external message is mailed in plaintext by design, and every message waits at most 24 hours (`outboxGiveUp`); deliveries to internal recipients usually take seconds. A bare hash of content would let anyone with the database confirm a guess. |
| E15 | **Formats are versioned and documented** in `docs/CONTENT-ENCRYPTION-FORMAT.md` with test vectors: the skeleton split, parts, slots and HPKE parameters (X-Wing, HKDF-SHA256, AES-256-GCM, `info` naming the format and version, AAD binding the calendar ID, object UID and part index). | Option 1 means a device must read and write these formats without the server. Binding the AAD means a part moved to another object or calendar fails to open. |

## Readers

Every place that reads `calendar_objects.data` or another content column, from the code survey at
`c2e5bf0`.

**Request-time, unlocked: decrypt and merge, then work as today.**

- CalDAV `GET`, `calendar-multiget` and `calendar-query` (`internal/davbackend/backend.go:342-383`).
  The column filter still narrows candidates by time; `text-match` runs on merged objects.
- The web events API (`internal/api/events.go:293`).
- MCP tools (`internal/api/mcp.go:260-460`), unlocked by the MCP token's slot.
- Writes: `davbackend.Write` (`internal/davbackend/write.go:32`) receives the full object, splits
  it, seals the whole body under the calendar's AES key as a new base, and drops older parts (E2, E4).
- Task search (`internal/api/tasks.go:619-632`). The plaintext `task_search` table
  (`migrations.go:966-974`) keeps serving unencrypted accounts. An encrypted account's rows are
  deleted at setup and never written again; its search decrypts the account's tasks per request. Fine for
  thousands of tasks; measured in CE2.

**Background or anonymous: skeleton only, or seal without reading.**

- **Free/busy and presence** (`internal/davbackend/freebusy.go:62`, `davbackend/presence.go:43`):
  skeleton. No change.
- **Booking pages.** Availability parses skeletons (`internal/booking/booking.go:187-214`).
  Neither the hold nor the confirmation carries a host secret: both are the visitor's anonymous
  requests. The hold seals the visitor's `name` and `note` (`bookings`, `migrations.go:842-862`)
  under the deployment key (purpose `booking-hold`), for at most the 15-minute hold
  (`internal/booking/booking.go:30`). Confirming opens them and builds the event and the booker's
  invitation as today (`internal/api/booking.go:734`). It writes the event as a base sealed to the
  host calendar's public key, and queues the invitation under E14. Then it clears the row's name,
  note and email, as confirming already does (booking page spec, Retention). An expired hold is
  deleted unopened. The visitor's `email` stays readable while the hold lasts, because the
  confirmation link is mailed to it.
- **The booker's manage link** (reschedule and cancel, `booking.go:860`) is also anonymous, and
  it mails the booker an updated `REQUEST` or `CANCEL` built from the stored event. So each
  confirmed booking gets its own key pair, like a calendar key with one holder: the seed is wrapped
  under a key derived from the manage token (stored only as SHA-256, `booking.go:751`), and the
  public key is stored with the booking.
  - **Confirm** seals the booker copy (the event as the booker's invitation carries it) to that
    public key.
  - **Every later host write** to the event is unlocked. It re-seals the booker copy as the booker
    now sees it, in the same transaction that queues the booker's update today.
  - **A manage-link action** derives the key from the presented token, opens the booker copy,
    changes its times, mails the update under E14, and re-seals it. The host's event gets the same
    times in its skeleton.

  Only the token's holder (the booker, who already has this content by mail) and the host can open
  the booker copy. The booking manage link is the only anonymous path that sends an organizer's
  `REQUEST` or `CANCEL`. Confirm is covered above, worker deliveries write attendee copies or
  replies, and every other organizer write is a user's own request. CE3 re-checks this against
  every caller of the scheduling queue.
- **Resource auto-answer** (`internal/store/resource_bookings.go:107-149`,
  `internal/resource/settle.go:643-644`). Conflicts use skeletons. When it sets `LOCATION` on a
  worker path, it appends a `set LOCATION` patch (E5). A room's own calendar holds the skeleton only: a room has
  no user to unlock it, and its calendar shows the organizer and times.
- **Outbox delivery** (`davbackend/scheduling.go:199-305`, `deliverToAttendee` `:422`,
  `deliverReply` `:556`): writes the skeleton. A first delivery writes the content as the
  base, in the format the target calendar has at delivery (E14); an update to an existing copy
  appends it as a `keep-alarms` base (E4). A
  `REPLY` follows the reply rule below.
- **Inbound iTIP from KyPost** (`internal/api/itip_handlers.go:211,241`,
  `davbackend/external.go:107-167`): the message arrives in plaintext from mail, is split, and its
  content is sealed to the recipient's calendar public key. An organizer's new version is a new
  base, as the stored copy is replaced today (`external.go:107-167`). A `REPLY` follows the reply
  rule below.
- **The reply rule**, the same for an internal `REPLY` from the outbox and an external one from
  KyPost (RFC 5546 lets either carry `COMMENT`): the replying attendee's `ATTENDEE` parameters
  (`PARTSTAT`) go to the organizer's skeleton. Nothing else from the reply is stored, including a
  `COMMENT`: today `itip.ApplyReply` copies only `PARTSTAT` onto the organizer's object, and the
  organizer reads the comment in the reply mail or its iTIP view. So a reply writes no content part
  at all.
- **Out-of-office and KyPost task flags** (`davbackend/out_of_office.go:26,98`,
  `internal/api/kypost_tasks.go:87`): seal what they write to the public key.
- **Feed refresh** (`internal/feed/refresh.go:83-96`, `store/subscriptions.go:138-248`): fetched
  objects are split and sealed to the subscribed calendar's public key. Change detection compares
  an HMAC of the fetched object under a deployment-derived key instead of the plaintext ETag
  (`subscriptions.go:142-143,222`).
- **KyMessages meeting sync** (`internal/api/video_meetings.go:106-124`) sends the event title as
  the room title. It moves into the write request that creates or renames the meeting, which is
  unlocked. The background sync stops sending titles. When the sync writes the room link back to
  the event, it appends a `conference` patch (E5), which sets `CONFERENCE` and the `Join:` line
  without reading the description.
- **Backup** (`internal/backup/payload.go:64-70`): unchanged. The capsule holds sealed content and
  wrapped keys, which `encryption.key` does not open. The restore drill (`internal/backup/drill.go:257`)
  decodes skeletons.
- **Org purge and booking pruning** (`internal/store/org_purge.go:115`, `booking.go:888`): delete
  rows; the new tables join the purge classification.
- **`export-org`** (tenancy spec, step 4e): writes skeletons, sealed content and wrapped slots.
  `kycalendar open-export -in <dir> -recovery-code -` opens one member's calendars offline, as a
  client would in option 1.

**ETags** become the SHA-256 of `data` followed by `sealed`, computed at each write (today the
body's, `calendars.go:369-370`) and stored in the existing `etag` column. They change whenever a
write changes either, and the server computes them without a key. Folding (E4) does not count as
a write, so it leaves the stored ETag as it was.

## Schema

All in one migration with both backends' SQL, as every migration since version 20 is.

- `account_keys (user_id PK, org_id, public_key, created_at, transition_until NULL)`.
- `key_slots (user_id, slot_id, kind, wrapped, params, created_at, last_used_at, PK (user_id, slot_id))`.
  `kind` is `apppass`, `mcp`, `passkey`, `passphrase`, `password`, `recovery`, `session`,
  `transition`, and later `device`. `params` holds the Argon2id parameters or the PRF salt. An
  app-password, MCP or session slot is deleted with its credential.
- `calendar_keys (calendar_id PK, public_key)` and
  `calendar_key_holders (calendar_id, user_id, wrapped_seed, PK (calendar_id, user_id))`.
- `pending_key_shares (calendar_id, user_id, queued_at, PK (calendar_id, user_id))`.
- `calendar_objects` gains `plain_size INTEGER NULL` (E4, size).
- `bookings` gains `public_key`, `wrapped_seed` and `sealed_copy` (the booker copy, Readers).
- `worker_keys (id PK, calendar_id, wrapped, created_at, ended_at NULL)`, deleted when no part references it
  (E4).
- `calendar_objects.sealed BLOB/BYTEA NULL`. The usage triggers (`migrations.go:410-428`,
  `:712-730`) count `LENGTH(sealed)` when it is set and `LENGTH(data)` otherwise (E4).
- `scheduling_outbox` gains `sealed`, and `bookings` gains `sealed_hold` (the name and note
  under the `booking-hold` key).
- `calendar_objects` gains `parts_version INTEGER NOT NULL DEFAULT 0` (E4, folding).
- **Additive only.** No migration in CE1 to CE5 drops or rewrites a column a running reader uses:
  `bookings.name` and `bookings.note` keep serving holds made with the setting off, and
  `task_search` keeps serving unencrypted accounts. CE3 writes `sealed_hold` instead of the plain
  columns only for hosts with an account key. Migrations are never edited once released, so the
  schema grows in the phase that adds each reader.

Every new table is org-owned (class O in the tenancy spec's Inventory) and joins `OrgStore`.

## Web flows

- **Setup** (the first unlock of an unencrypted account): explain what is encrypted and what is not,
  register a passkey or set a passphrase, show the recovery code and require it to be typed back.
  Then start the sweep and the 14-day transition (E11).
- **Unlock**: the browser asks for the passkey, or the passphrase, or the recovery code ("Use
  recovery code"). It creates the session slot. Local-password accounts in single-tenant installs
  unlock at sign-in, from the password slot.
- **After recovery-code unlock**: require a new passkey or passphrase, and offer a new code.
- **Security settings**: list slots (passkeys, passphrase, recovery code, app passwords and MCP
  tokens with their last use, the transition's end date), and replace or remove them. Removing the
  last web unlock method is refused unless the recovery code is set.
- **Reset encryption** (every secret lost): after a typed confirmation, in one transaction, the
  account gets a new key pair and loses every old slot. Then, for each calendar the account holds:
  - **if another current or former holder has a wrap** (E13), the account's own wrap, which only
    the old key opens, is deleted and a `pending_key_shares` row is queued for it. That holder's
    next unlocked request re-shares the seed to the new key, and the content survives;
  - **otherwise** the calendar gets a new key, keeps its times, and loses its content: its sealed
    parts are deleted, and its objects are served as skeletons from then on.
- **Admin pages**: per account, whether it is encrypted, its transition end date, and how many
  legacy rows remain. Never content.

A local password reset by an admin cannot rewrap the password slot. The user unlocks with the
passkey, passphrase or recovery code, and the new password gets a slot at that unlock.

## Leaving the door open for option 1

Option 1 means devices hold the account key and the server never unwraps it. This design keeps
that possible:

- **Device slots.** A device publishes a public key and gets the account key sealed to it, as
  KyPost enrolls devices (`docs/DEVICE_ENROLLMENT.md:49-67`). The `device` slot kind is reserved.
- **Shared formats.** Clients read and write skeletons, parts and slots by the format document and
  its test vectors (E15). The Android app would sync through the system calendar provider, the
  Apple apps through EventKit, and desktops through a local CalDAV bridge.
- **Workers already never decrypt.** Nothing in the background depends on the server holding a
  key, so taking the server's keys away breaks only request-time features: `text-match`,
  task search and merging on write. Clients do those themselves.
- **What option 1 removes:** the app-password, MCP, session, passphrase and password slots on the
  server, so a native app reaches the server only through the device's bridge.
- **The browser** would need X-Wing in JavaScript or WebAssembly (WebCrypto has no ML-KEM) to open
  slots itself. The passkey PRF unlock already runs in the browser.

## Threat model

- **Stolen database, replica snapshot, backup or capsule, including `encryption.key`**: gets
  skeletons, sizes, wrapped keys and sealed content. To read the content of a calendar that has a
  key, the attacker must break a user slot: a 256-bit app-password secret, a 128-bit recovery
  code, a passkey's PRF output, a session token, or a passphrase or password stretched by
  Argon2id. Passphrases and passwords are the weak point (E8).

  **Exceptions:** the stolen `encryption.key` opens these directly, or they were never encrypted:
  - accounts that have never run setup, and group calendars waiting on a member (E12), as legacy
    plaintext;
  - an account in its 14-day transition, whose `transition` slot opens with `encryption.key` (E11),
    so a capsule taken in those 14 days exposes that account's full content;
  - queued scheduling messages, for up to 24 hours (E14);
  - booking holds' names and notes, for up to 15 minutes.

  CE6's updates to `docs/ASSURANCE-CASE.md` and `docs/STRONG-CRYPTO.md` carry this list as it
  stands here.
- **Operator or staff** reading the database or running `export-org`: the same as a stolen
  database for every calendar that has a key. Calendars without one are readable in full, as
  today: those of accounts that never ran setup, and group calendars waiting on a member (E12).
  The deployment key also opens content during an account's 14-day transition (E11), for outgoing scheduling
  messages waiting up to 24 hours (E14), and for a booking hold's name and note, which wait up to 15 minutes.
- **Compromised running server**: sees every secret and every object in the requests it handles. A
  fully compromised host can also serve modified web code that leaks the passkey PRF output. Not
  covered; option 1 narrows this only for native apps.
- **Malicious member of a shared calendar**: could read the content while a member. Removal stops
  new reads through the server, not what they already saw (E13).
- **Someone who can write the database, including the operator**:
  - A part moved to another object or calendar fails to open (E15).
  - A replaced skeleton is not detected: skeletons are not authenticated, because workers rewrite
    them without a key.
  - **They can get a shared calendar's seed.** They add a grant or group membership for an account
    they control (with an account key of their own), or replace a member's `account_keys` public
    key. The next holder's unlocked request then wraps the seed to that key, with no user secret
    involved. The CE5 checks below stop a pending share with no matching grant and a swapped
    calendar key. They cannot stop a forged grant or a swapped member key, because both look like a
    real grant from the suite mirror. What stops it from going unseen is the share audit and the
    "shared with" list each holder sees (E13).
  - **They can get future worker-written content** by replacing `calendar_keys.public_key`, until the
    next unlocked request of a holder detects the mismatch (E13) and refuses it.
  - Reading the database alone gives none of this. Option 1, with device-held keys and holders
    confirming new members, would close it.

## Phased build plan

Every PR ships behind the hidden setting `KYCALENDAR_CONTENT_ENCRYPTION` (default off) until CE6
turns it on.

1. **CE1, formats and keys.** `internal/contentkey`: the skeleton split (E1), parts (E4, E5),
   seed derivation, slots and the HPKE suite. `docs/CONTENT-ENCRYPTION-FORMAT.md` with test
   vectors. The new tables and columns (Schema), none of them read yet. No behaviour change.
   - Tests: sealing then opening a base returns the original bytes exactly, for a corpus of real
     client objects (Apple, Google, Outlook, Thunderbird, DAVx5 exports, the existing interop
     fixtures). Each patch operation changes only its property: a `set LOCATION` or `conference`
     patch on top of a base leaves `SUMMARY`, `DESCRIPTION` (apart from the `Join:` line) and every
     `X-` property unchanged. A part opens only
     with its calendar, object and index. Every slot kind opens with its secret and fails with any
     other.
2. **CE2, store and request paths.** Seal on write, merge on read, folding, app-password and MCP
   slots, session slots, E10 refusal, task search over decrypted content.
   - Tests: a CalDAV client round trip on an encrypted account returns the bytes it sent.
     `TestInteropFixturesSurviveWebEdits` passes on an encrypted account. A credential
     without a slot gets `403`. The database holds no content property in `data`. A multiget of
     2,000 objects stays under 1 s on Major.
3. **CE3, workers.** Outbox sealing and HMAC dedupe (E14), inbound iTIP, booking confirm and the
   `booking-hold` sealing of a hold's name and note, resource `LOCATION` parts, feed refresh with an HMAC change check,
   KyMessages titles at write time, out-of-office and task flags.
   - Tests: an organizer's update that clears `LOCATION` reaches an attendee's encrypted copy with
     no location and the attendee's own alarms intact. 40 worker patches with distinct labels (one
     `set LOCATION` on each of 40 occurrences) to a meeting nobody opens stop adding parts at 16,
     audit `content_part_dropped` for the rest and keep every readable change; the organizer's next read folds them
     to one base with the same ETag.
   - Tests: completing a repeating task from KyPost on an encrypted account moves it to the same
     next occurrence as today, for both repeat-from-due and repeat-from-completion.
   - Tests: a `REPLY` for one occurrence of a recurring event creates an override that an unlocked
     read serves with the master's title, description and location.
   - Tests: a `REPLY` with a `COMMENT`, internal and from KyPost, updates `PARTSTAT` in the
     skeleton and stores the comment nowhere, as today.
   - Tests: a message queued for a recipient without a key, who runs setup before delivery,
     arrives sealed to the new calendar key; one queued for a keyed recipient who resets before
     delivery arrives sealed to the replacement key; one for a recipient still without a key
     arrives as a legacy row. A delivery over `MaxObjectSize` is refused as today and never stored as
     a bare skeleton; the sweep seals an object of exactly `MaxObjectSize`.
   - Tests: a booker who reschedules through the manage link after the host renamed the event
     gets an updated invitation with the new title and time; a wrong token opens nothing. Twenty
     room-link changes to an unopened meeting leave one `conference` patch with the latest link.
     A delivery whose plaintext is exactly `MaxObjectSize` is accepted.
   - Tests: a sweep run whose first object is folded by a user's read keeps its key and can open
     every object it writes afterwards. Fifty organizer updates to an attendee copy nobody opens
     leave two bases.
   - Tests: a fold racing a worker's patch on PostgreSQL never loses the patch: either the fold
     includes it, or the fold is skipped and the next read folds both.
   - Tests: a booking reschedule, an attendee's reply and a room's answer on an encrypted account
     show up in the next CalDAV read, with the title and description unchanged.
   - Tests: each worker writes content an unlocked read returns, and none of them can open a slot (the
     worker gets no slot code path at all).
4. **CE4, web setup and unlock.** Setup, passkey PRF, passphrase, recovery code, password slot,
   unlock screen, security settings, reset.
   - Tests: browser tests for setup, unlock, recovery-code unlock and replacing a passkey.
5. **CE5, sharing.** Calendar key holders, pending shares from grants and the suite group mirror,
   removal.
   - Tests, with rows inserted directly by SQL, bypassing the grant path: a `pending_key_shares`
     row and an `account_keys` row for an account with no grant make no `calendar_key_holders`
     row at a holder's unlocked request, and write an audit event. A replaced
     `calendar_keys.public_key` is detected and refused at the next unlocked request. A real share
     appears under "Shared with" for every holder.
   - Tests: a new group member reads the calendar after another member's next request, and not
     before. After a member resets encryption, they read the group calendar again after another
     member's next request, and their personal calendars keep only times. A removed member is refused. A group calendar stays legacy and readable to a member
     who has not run setup until that member runs it. After the group is deleted and the calendar
     is granted to a new group, the new members can read its content after a former member's
     next request.
6. **CE6, migration and default on.** Setup's sweep, the 14-day transition, admin status,
   `export-org` and `open-export`, the restore drill. Docs: `docs/ASSURANCE-CASE.md`,
   `docs/STRONG-CRYPTO.md`, `docs/RUNNING.md`, `AGENTS.md`. The setting defaults to on.
   - Tests: an upgraded install with two existing app passwords and an MCP token keeps all three
     syncing through the transition: the second and third still unlock after the first has its own
     slot. An app password unused past the window is refused. The sweep leaves no legacy rows, is resumable, changes the account's counted usage by no more
     than 28 bytes plus a key ID per object, and finishes for an account already at its quota. A
     capsule restore reads nothing without a slot secret.

## Residual risks

- **Readable metadata.** Times, recurrence, attendees and their names, organizers, status and sizes
  stay readable to the operator. That is the price of owner decision 2.
- **Lost secrets lose content.** No escrow (owner decision 5). The setup flow makes the recovery
  code hard to skip, but users still lose it.
- **Weak passphrases** can be attacked offline from a stolen database (E8).
- **The transition, the outbox and booking holds** are readable with the deployment key for
  their windows: 14 days, 24 hours and 15 minutes (E11, E14, Readers).
- **Keyed fingerprints.** The outbox dedupe key and the feed change check are HMACs under
  deployment-derived keys, so someone with `encryption.key` can confirm a guessed message or feed
  object.
- **Accounts that never open the web stay unencrypted,** and so does every group calendar they
  belong to. The admin page shows them.
- **A group calendar with no current or former holder left** (every account deleted) loses its
  content. The admin is warned first (E13).
- **Rooms and equipment** show only times and organizers.
- **CPU per request.** Each request unwraps the account key, and each object it reads is opened.
  CE2 measures it.

## Open questions, with recommendations

1. **Does this land before tenancy 4e (`export-org`)?** Recommend building 4e now as specified and
   changing it in CE6. It is small, and CE6 already has to touch the restore drill and exports.
2. **Transition length.** Recommend 14 days. Shorter leaves less time readable; longer suits
   devices that sync rarely.
3. **Should a PRF-capable passkey be required in multi-tenant mode, with no passphrase?** Recommend
   no for v1: browser support for PRF still varies, and the passphrase states its trade-off.
4. **Should `CN` move to content?** Recommend no: invitations and attendee lists need it, and the
   address already identifies the person.

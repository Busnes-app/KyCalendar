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
| Stolen database, backup or capsule (they include `encryption.key`) | everything | times, recurrence, attendees, sizes |
| Operator reading the database or running `export-org` | everything | times, attendees; content only with the user's secret |
| Compromised running server | everything | everything that passes through while users are active |
| Native calendar apps | work | work, with no change on the device |
| Availability, booking pages, rooms, invitations | work | work |

## Decisions this spec makes

| # | Decision | Why |
|---|---|---|
| E1 | **Content properties are an allowlist of readable properties; everything else is content.** Readable: `UID`, `DTSTART`, `DTEND`, `DUE`, `DURATION`, `RRULE`, `RDATE`, `EXDATE`, `RECURRENCE-ID`, `SEQUENCE`, `DTSTAMP`, `CREATED`, `LAST-MODIFIED`, `STATUS`, `TRANSP`, `CLASS`, `PRIORITY`, `COMPLETED`, `PERCENT-COMPLETE`, `ORGANIZER`, `ATTENDEE` (with parameters, including `CN`), `VALARM`'s `ACTION` and `TRIGGER`, and every `VTIMEZONE` component. Every other property, including every `X-` property, is content. | Default to secret. An unknown vendor property, which can hold anything, is sealed. The readable set is what scheduling, availability, recurrence and alarms need (Readers, below). `CN` stays readable because invitations and the attendee list show it, and the attendee's address already identifies them. |
| E2 | **The skeleton stays in `calendar_objects.data`; the sealed column holds the whole original body.** An unlocked write seals the body exactly as received and stores its skeleton beside it. A read with no worker parts on top (E5) serves those sealed bytes unchanged. | Every time-only reader keeps parsing `data` unchanged (Readers). The contract that PUT bodies are "stored and served as the raw bytes received" (`AGENTS.md:302`) keeps holding, so property order, folding and vendor properties survive, and the interop fixtures (`TestInteropFixturesSurviveWebEdits`) keep passing. The readable properties are stored twice, which costs some space; quotas count both. A legacy row has `sealed IS NULL` and holds the full object in `data`, which is how unencrypted accounts and the migration work. |
| E3 | **Key hierarchy: account key → calendar key → content.** Each account has an X-Wing key pair. Each calendar has a 32-byte seed. The seed gives an X-Wing key pair (for sealing without a secret) and an AES-256-GCM key (for unlocked writes). The seed is wrapped to each holder's account public key. | Grants are to groups (`calendar_grants`, `migrations.go:368-374`), so several accounts read one calendar; a per-calendar key lets each holder's own secret open it. X-Wing is the KEM already used for capsules and the recovery key (`ky-primitives/capsule/hpke.go`, `recoverykey`), so stolen content sealed today stays safe against a future quantum attacker. |
| E4 | **The sealed column is a list of parts: a base and then patches.** The base is a whole body. An unlocked write replaces every part with one base sealed under the calendar's AES key (28 bytes of overhead). A worker that writes a whole new object (a delivered invitation, a booking, a feed object) writes a base sealed with HPKE to the calendar's public key (about 1.1 KB of overhead). A worker that changes an existing object appends a patch sealed with HPKE (E5). The next unlocked write replaces them all with one base again. | A worker can then write content without being able to read the existing content. Most writes come from users and stay small; worker parts are rare and get folded. Quotas count `data` plus `sealed`. |
| E5 | **A patch is a list of operations on named properties**, never a whole component. Each operation names a component (`VEVENT`, `VTODO` and so on, plus its `RECURRENCE-ID`) and is one of: `set` (replace every instance of one property, for example `LOCATION` or `COMMENT`), `remove` (one property), or `conference` (the KyMessages room: set `CONFERENCE` and the final `Join:` description line, or remove both, with the rules of `calendar.SetConference` and `StripJoin`, `internal/calendar/conference.go:25-86`). Patches apply in order on top of the base. A read that applies any patch serves the merged object re-encoded, as a worker rewrite serves today. | A worker never sees the user's other content, so it must not be able to replace it. Setting one property leaves the title, description and vendor properties exactly as they were. `conference` is its own operation because the `Join:` line lives inside `DESCRIPTION`, which the worker cannot read. Applying it at read time keeps the existing rule that a later `Join:` replaces the earlier one. |
| E6 | **Unlock secrets.** Each slot wraps the account's private key under a key derived from one secret: an app password or MCP token (HKDF of its 256-bit secret, `internal/apppass/apppass.go:18-30`), a passkey (its WebAuthn PRF output), the passphrase (Argon2id), the recovery code (HKDF of 128 random bits), a local password where one exists (Argon2id), or a browser session (HKDF of the session token, which the server stores only as SHA-256, `internal/auth/session.go:65-66`). | Every request already carries one of these secrets. Unwrapping per request keeps replicas stateless: no unlocked key lives in any process between requests. A session slot dies with its session, so a stolen cookie database (hashes only) opens nothing. |
| E7 | **Passkeys for unlock only; KyCalendar verifies no assertion.** The browser calls WebAuthn with the PRF extension and a per-slot salt, and sends the PRF output over the signed-in session. A wrong output fails to unwrap. | Sign-in is already KyIdentity's job. The PRF output is the secret, so there is no server-side WebAuthn code to get wrong. A passkey registered with KyCalendar's relying-party ID belongs to KyCalendar, so another app's passkey cannot unlock it. |
| E8 | **The passphrase is the fallback**, for browsers or authenticators without PRF support. It is at least 12 characters and is stretched with Argon2id (`ky-primitives/password`). | Owner decision 4. A stolen database can be searched offline against a weak passphrase, so the setup page recommends the passkey and states the trade-off. |
| E9 | **The recovery code is 128 random bits**, shown once at setup as 26 base32 symbols in groups. It can be replaced while unlocked. It does not use `ky-primitives/recoverycode`, whose 60 bits (`recoverycode.go:29`) are too weak for a key someone can attack offline. | Owner decision 5. KyPost's recovery secret is also 128 bits (`frontend/src/lib/keyVault.ts:88`). |
| E10 | **A credential that cannot unlock an encrypted calendar is refused**, never served the skeleton. CalDAV gets `403` with a body that says to create a new app password; the web shows the unlock screen. | A calendar app that read a skeleton would show untitled events, and writing one back would erase the content. |
| E11 | **Existing app passwords get a 14-day transition.** At setup, the account key is also wrapped under the deployment key (`KY_ENCRYPTION_KEY`, purpose `content-transition`). Each existing app password or MCP token gets its own slot at its first use in those 14 days. The transition slot stays until `transition_until` passes, however many credentials have moved over, and is deleted then. After that, older credentials that were never used are refused (E10). | Without it, every device would have to be set up again at the moment of encryption. The window is the only time an operator with the deployment key can read the account, and the account's admin page shows when it ends. |
| E12 | **New calendars and accounts are encrypted from the first unlock.** An account with no key keeps writing legacy rows, as today. Its first web unlock runs setup: create keys, show the recovery code, register a passkey or set a passphrase. A background sweep then seals every legacy row to the calendar's public key. | Owner decision 6. Sealing needs only public keys, so the sweep needs no user present. Setup needs the web because the recovery code must be shown to the user. |
| E13 | **Sharing a calendar wraps its seed to each holder.** When a grant gives a new account access (a grant change, or a group membership arriving from the suite mirror), a `pending_key_shares` row is queued. Any current holder's next unlocked request completes it. Until then, the new member sees the calendar as locked. Removing access deletes the holder's wrap. | The seed can only be wrapped by someone who can open it. Group calendars have members who are active, so pending shares clear quickly. The seed is not rotated on removal: the removed member could already read it, and the times were never secret. |
| E14 | **Outgoing scheduling messages** are still created inside the sender's request. For an internal recipient, the content is sealed to the recipient's scheduling calendar at queue time. For an external recipient it is sealed under the deployment key (purpose `outbox`) until sent, and blanked afterwards as today (`internal/store/scheduling.go:154-158`). The dedupe key becomes an HMAC under a deployment-derived key instead of SHA-256 over the body (`davbackend/scheduling.go:188`). | The worker that delivers or sends never needs the sender's key. An external message is mailed in plaintext by design, and it waits for at most 24 hours (`outboxGiveUp`). A bare hash of content would let anyone with the database confirm a guess. |
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
  (`migrations.go:966-974`) is dropped; search decrypts the account's tasks per request. Fine for
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
  confirmation link is mailed to it. Rescheduling changes only skeleton
  times (`booking.go:860`).
- **Resource auto-answer** (`internal/store/resource_bookings.go:107-149`,
  `internal/resource/settle.go:643-644`). Conflicts use skeletons. When it sets `LOCATION` on a
  worker path, it appends a `set LOCATION` patch (E5). A room's own calendar holds the skeleton only: a room has
  no user to unlock it, and its calendar shows the organizer and times.
- **Outbox delivery** (`davbackend/scheduling.go:199-305`, `deliverToAttendee` `:422`,
  `deliverReply` `:556`): writes the skeleton and appends the part sealed at queue time (E14). A
  `REPLY` changes `ATTENDEE` parameters, which are skeleton; a `COMMENT` it carries becomes a `set COMMENT` patch.
- **Inbound iTIP from KyPost** (`internal/api/itip_handlers.go:211,241`,
  `davbackend/external.go:107-167`): the message arrives in plaintext from mail, is split, and its
  content is sealed to the recipient's calendar public key. An organizer's new version is a new
  base, as the stored copy is replaced today (`external.go:107-167`). A reply touches skeleton
  properties only.
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

**ETags** become the SHA-256 of `data` followed by `sealed` (today the body's,
`calendars.go:369-370`), so they change when either changes and the server computes them without
a key.

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
- `calendar_objects.sealed BLOB/BYTEA NULL`. The usage triggers (`migrations.go:410-428`,
  `:712-730`) add `LENGTH(sealed)`.
- `scheduling_outbox` gains `sealed`, and `bookings.name` and `bookings.note` become sealed blobs
  under the `booking-hold` key.
- `task_search` is dropped.

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
- **Reset encryption** (every secret lost): a new account key. The account's own calendars keep
  their times and lose their content, after a typed confirmation. A shared calendar keeps its content
  when another holder can still open it: that holder's next unlocked request re-shares it (E13).
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
  skeletons, sizes, wrapped keys and sealed content. To read content, the attacker must break a
  slot: a 256-bit app-password secret, a 128-bit recovery code, a passkey's PRF output, a session
  token, or a passphrase or password stretched by Argon2id. Passphrases and passwords are the weak
  point (E8).
- **Operator or staff** reading the database or running `export-org`: the same as a stolen
  database, except during an account's 14-day transition (E11), for external messages waiting
  up to 24 hours (E14), and for a booking hold's name and note, which wait up to 15 minutes.
- **Compromised running server**: sees every secret and every object in the requests it handles. A
  fully compromised host can also serve modified web code that leaks the passkey PRF output. Not
  covered; option 1 narrows this only for native apps.
- **Malicious member of a shared calendar**: could read the content while a member. Removal stops
  new reads through the server, not what they already saw (E13).
- **Row swapping by someone with write access to the database**: a part moved to another object or
  calendar fails to open (E15). Replacing a skeleton is not detected: skeletons are not
  authenticated, because workers rewrite them without a key.

## Phased build plan

Every PR ships behind the hidden setting `KYCALENDAR_CONTENT_ENCRYPTION` (default off) until CE6
turns it on.

1. **CE1, formats and keys.** `internal/contentkey`: the skeleton split (E1), parts (E4, E5),
   seed derivation, slots and the HPKE suite. `docs/CONTENT-ENCRYPTION-FORMAT.md` with test
   vectors. Schema (above). No behaviour change.
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
   the `booking-hold` sealing of a hold's name and note, resource `LOCATION` parts, feed refresh with an HMAC change check,
   KyMessages titles at write time, out-of-office and task flags.
   - Tests: each worker writes content an unlocked read returns, and none of them can open a slot (the
     worker gets no slot code path at all).
4. **CE4, web setup and unlock.** Setup, passkey PRF, passphrase, recovery code, password slot,
   unlock screen, security settings, reset.
   - Tests: browser tests for setup, unlock, recovery-code unlock and replacing a passkey.
5. **CE5, sharing.** Calendar key holders, pending shares from grants and the suite group mirror,
   removal.
   - Tests: a new group member reads the calendar after another member's next request, and not
     before. A removed member is refused.
6. **CE6, migration and default on.** Setup's sweep, the 14-day transition, admin status,
   `export-org` and `open-export`, the restore drill. Docs: `docs/ASSURANCE-CASE.md`,
   `docs/STRONG-CRYPTO.md`, `docs/RUNNING.md`, `AGENTS.md`. The setting defaults to on.
   - Tests: an upgraded install with two existing app passwords and an MCP token keeps all three
     syncing through the transition: the second and third still unlock after the first has its own
     slot. An app password unused past the window is refused. The sweep leaves no legacy rows and is resumable. A
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
- **Accounts that never open the web stay unencrypted.** The admin page shows them.
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

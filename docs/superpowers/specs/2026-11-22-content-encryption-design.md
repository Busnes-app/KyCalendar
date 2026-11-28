# Content encryption at rest, KyCalendar design

Date: 2026-11-22. Status: proposed design, docs only. Build step CE1 follows this spec: CE1a
(`internal/contentkey`, keys and sealed formats, #194) and CE1c (migration 38, #196) are merged,
and CE1b (`internal/skeleton`, the split and merge) is in #195. They change when this spec does.
Nothing reads or writes the new formats yet.

PQC Wave 1 row 21's shared-helper refactor uses `ky-primitives/xwing` v0.11.0 for CE1a's
keys and HPKE, preserving content format v1 and its stored vectors. It does not complete
CE2 or enable content encryption. V1 binds global user/calendar IDs, not `org_id`; the
[format document](../../CONTENT-ENCRYPTION-FORMAT.md#tenant-and-organization-binding) states
the required caller-side org checks and the limits of the binding tests.

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
| E1 | **Content properties are an allowlist of readable properties; everything else is content.** Readable: `UID`, `DTSTART`, `DTEND`, `DUE`, `DURATION`, `RRULE`, `RDATE`, `EXDATE`, `RECURRENCE-ID`, `SEQUENCE`, `DTSTAMP`, `CREATED`, `LAST-MODIFIED`, `STATUS`, `TRANSP`, `CLASS`, `PRIORITY`, `COMPLETED`, `PERCENT-COMPLETE`, `ORGANIZER`, `ATTENDEE` (with parameters, including `CN`), `VALARM`'s `ACTION`, `TRIGGER`, `REPEAT`, `DURATION`, `UID` and `ACKNOWLEDGED`, the time-zone rules (`VTIMEZONE`'s `TZID` and `LAST-MODIFIED`, and its `STANDARD` and `DAYLIGHT` components' `DTSTART`, `TZOFFSETFROM`, `TZOFFSETTO`, `RRULE` and `RDATE`), `VCALENDAR`'s `VERSION`, `PRODID`, `CALSCALE` and `METHOD`, KyCalendar's own task markers `X-KY-REPEAT-FROM`, `X-KY-HABIT`, `X-KY-ESTIMATE`, `X-KY-SOURCE` and `X-KY-REF` (`internal/tasks/repeat.go:8-38`, `task.go:22-25`), and its scheduling markers `X-KYCALENDAR-ORGANIZER-SEQUENCE`, `X-KYCALENDAR-OUT-OF-OFFICE` and `X-KYCALENDAR-BOOKING-TYPE`, which workers read (found while building CE1b, `internal/skeleton/skeleton.go`). Every other property, including every other `X-` property, is content. | Default to secret. An unknown vendor property, which can hold anything, is sealed. The readable set is what scheduling, availability, recurrence, alarms and task repeats need (Readers, below). The `X-KY-` markers are flags, a duration and KyPost's opaque message reference: a worker completing a repeating task from KyPost needs `X-KY-REPEAT-FROM` and `X-KY-HABIT` to move it to the right next occurrence, and finds the task by `X-KY-REF`. `CN` stays readable (owner decision 2026-11-23, `Busnes-app/ky-launch#4`) because invitations and the attendee list show it, and the attendee's address already identifies them. A time zone keeps only what its rules need: its `COMMENT`, `TZNAME`, `TZURL` and vendor properties (`X-LIC-LOCATION` and the like) are free text a client can fill with anything, nothing in KyCalendar reads them (it resolves zones by `TZID`, `internal/calendar/object.go:254`), and they are sealed like any other content. |
| E2 | **The skeleton stays in `calendar_objects.data`; the sealed column holds the whole original body.** An unlocked write seals the body exactly as received and stores its skeleton beside it. **The skeleton is the truth for readable properties; the base and patches are the truth for content.** A read takes each readable property (E1) and the set of components from the skeleton, and the content properties from the base with the patches (E5) applied. **An override's content is layered:** its master component's content (the one with no `RECURRENCE-ID`), then the override's own content in the newest base if it has any, then the patches naming that override, in order. An override a worker made for one occurrence (from a `REPLY` or a booking reschedule) has no content of its own, so it shows the master's title and description, plus a `LOCATION` or room link a later patch sets for that occurrence alone. This is what the stored copy would carry today when a worker copies the master to make an override. A fold writes the layered result into the new base, so the override keeps its title afterwards. When no patch applies and the skeleton still equals the base's own skeleton, the read serves the sealed bytes unchanged. Otherwise it serves the merged object re-encoded. | Every time-only reader keeps parsing `data` unchanged (Readers). The contract that PUT bodies are "stored and served as the raw bytes received" (`AGENTS.md:302`) keeps holding, so property order, folding and vendor properties survive, and the interop fixtures (`TestInteropFixturesSurviveWebEdits`) keep passing. Workers change readable properties (a rescheduled booking's times, an attendee's `PARTSTAT` from a reply or a room's answer) by rewriting the skeleton alone, as they rewrite `data` today, and every later read shows the change, so a client never gets stale times back to write over it. Bytes stay exactly as received until a worker changes the object, which is already when a worker rewrite re-encodes today. The readable properties are stored twice; quotas count the object's plaintext size, as today (E4). A legacy row has `sealed IS NULL` and holds the full object in `data`, which is how unencrypted accounts and the migration work. |
| E3 | **Key hierarchy: account key → calendar key → content.** Each account has an X-Wing key pair. Each calendar has a 32-byte seed. The seed gives an X-Wing key pair (for sealing without a secret) and an AES-256-GCM key (for unlocked writes). The seed is wrapped to each holder's account public key. **Both keys are versioned:** a rotation (E11) adds a new account key version and a new calendar key version, and keeps the old ones so content sealed under them still opens. A part, a worker key and a holder wrap each name the calendar key version they use. | Grants are to groups (`calendar_grants`, `migrations.go:368-374`), so several accounts read one calendar; a per-calendar key lets each holder's own secret open it. X-Wing is the KEM already used for capsules and the recovery key (`ky-primitives/capsule/hpke.go`, `recoverykey`), so stolen content sealed today stays safe against a future quantum attacker. |
| E4 | **The sealed column is a list of parts: a base and then patches.** The base is a whole body. An unlocked write replaces every part with one base sealed under the calendar's AES key (28 bytes of overhead). A worker that writes a whole new object (a delivered invitation, a booking, a feed object, the sweep, E12) writes a base, and a worker that changes named properties of an existing object appends a patch (E5). **An organizer's update delivered to an attendee's existing copy appends a new base marked `keep-alarms`**: today that update replaces the copy with the organizer's object and keeps only the attendee's own `VALARM`s and `PARTSTAT` (`itip.AttendeeCopy`, `internal/itip/itip.go:611-615`). So the newest base gives every content property, which removes anything the organizer cleared, and the alarms come from the newest base before it that is not marked. **A new base drops what it supersedes, with no key needed**: an unmarked base replaces every earlier part, and a `keep-alarms` base replaces every earlier part except the newest unmarked base. An object therefore holds at most two bases (the alarms source and the newest update) plus at most 16 patches written after the newest base. Bases do not count toward the cap. `PARTSTAT` is kept or reset in the skeleton, as today. A worker run (one delivery, one feed refresh, one sweep batch) makes a fresh AES key, wraps it once with HPKE to each calendar it writes (`worker_keys`, about 1.1 KB), seals its parts under it (28 bytes each plus the key's ID), and forgets it when the run ends. It is never stored unwrapped. **Folding:** the next unlocked write or read of the object replaces every part with one base under the calendar's AES key and deletes worker keys that nothing references and whose run has ended. A run sets `ended_at` on its keys when it finishes; a key whose run died counts as ended one hour after `created_at`, and no run may last longer than that. A key in use by a live run is never deleted, even if no part references it yet. **Every write of `sealed` other than a fold increments `parts_version` under the calendar lock**: an unlocked write's new base, a worker's base or patch, and a superseding base. A fold reads the parts and their `parts_version` and writes its new base only `WHERE parts_version` still equals the value it read, without changing it. So if anything wrote in between, whether a worker's patch or a client's new body, the fold changes nothing and the next read folds again. A fold can never put stale content over a newer write. A read that folds keeps the ETag (below), so clients refetch nothing. **Cap:** an object holds at most 16 worker parts with distinct labels (E5). **Size:** `calendar.MaxObjectSize` always measures plaintext. The row keeps `plain_size`, the plaintext size of the merged object, measured exactly whenever the full plaintext is in hand: an unlocked write sets it from the body, a worker's new base from that base, and a fold from the merged result. **A `keep-alarms` base cannot see the alarms it keeps**, so every unmarked base also records `alarm_bytes`, the serialized size of its `VALARM` components, on the row. A `keep-alarms` base sets `plain_size` to its own size, minus its own alarms, plus that `alarm_bytes`: the size of the merged object, with the attendee's own alarms counted exactly as in today's plaintext copy. **A worker never measures content it cannot read, so every base records the sizes a worker's change needs** (`content_sizes`, on the row, written whenever the plaintext is in hand: an unlocked write, a worker's base, a fold). For each component it holds the serialized size of its `LOCATION`, its `CONFERENCE` and its description's final `Join:` line, keyed by the value-free label of the patch that would replace them (E5), and for the master component the size of the content properties an override inherits from it (E2). Workers patch only those properties (E5), so each change is exact: a patch sets `plain_size` to `plain_size − old + its own size`, where old is the size recorded under its label (for an override with no value of its own, its master's), then records its own size there; a worker-made override adds its master's inherited size, as the plaintext copy grows today. A change that would take `plain_size` past `MaxObjectSize` is refused, so a fold never produces a base over the limit and the user's next save is never refused for size. **`plain_size` is therefore always the size of the merged object**, and a fold re-measures it. The owner's quota, the org total (tenancy D11) and the pool (tenancy D14) count `plain_size`: the same figure `LENGTH(data)` gives for a plaintext row today. The recorded sizes are readable, as the object's size already is (Threat model). Only a patch can be dropped: a worker patch past the cap, the size limit or the quota still makes its readable change in the skeleton but drops its content part, and audits `content_part_dropped` with the object, never the content. **A write that makes a base is never stored without it.** A first delivery, an organizer's update (measured with the alarms it keeps, above), an inbound invitation, a booking or a feed object over the limit is refused exactly as the same plaintext write is refused today (`len(raw) > calendar.MaxObjectSize`, `internal/davbackend/write.go:87`), and fails or retries as it does today. Sealing overhead never counts toward `MaxObjectSize`, because the limit measures plaintext. | A worker can write content without being able to read the existing content, and never holds a key that opens anything it did not write itself. Folding on reads as well as writes means parts pile up only on objects nobody with access opens, and the cap bounds those: a recurring meeting whose organizer never edits it again folds at the organizer's next sync. One HPKE wrap per run instead of per object keeps a 10,000-object sweep or feed near today's size. **Quotas count `plain_size` for an encrypted row and `LENGTH(data)` for a legacy row,** so encryption changes no one's counted usage. The skeleton copy, the sealing overhead, worker keys, and a superseded alarms base kept by a `keep-alarms` update, apart from the alarms it supplies, cost nothing against a quota. The extra stored bytes are bounded by the cap (at most two bases and 16 patches per object) and are the platform's to carry. The sweep is never refused by a quota or by `MaxObjectSize`: it adds no content, its body already passed both checks when it was written, and refusing it would leave content unencrypted. |
| E5 | **A patch is a list of operations on named properties**, never a whole component. Each operation names a component (`VEVENT`, `VTODO` and so on, plus its `RECURRENCE-ID`) and is one of: `set` (replace every instance of one property, for example `LOCATION`), `remove` (one property), or `conference` (the KyMessages room: set `CONFERENCE` and the final `Join:` description line, or remove both, with the rules of `calendar.SetConference` and `StripJoin`, `internal/calendar/conference.go:25-86`). Patches touch content properties only; readable ones change in the skeleton (E2). **Workers write only `set` or `remove` of `LOCATION` and `conference`**, the only content properties a worker changes today (a room's location, a KyMessages room); CE3 refuses any other before sealing, so every patch replaces a value whose size the base recorded (E4). Each operation's values, or its room link, are at most 2 KiB. Patches apply in order on top of the base. **Each patch carries a readable label**: its operation, property and component key, never a value. A new patch with the same label as an older one replaces it in the same transaction, so a later room link or room location supersedes the earlier one and never counts toward the cap (E4). | A worker never sees the user's other content, so it must not be able to replace it. Setting one property leaves the title, description and vendor properties exactly as they were. `conference` is its own operation because the `Join:` line lives inside `DESCRIPTION`, which the worker cannot read. Applying it at read time keeps the existing rule that a later `Join:` replaces the earlier one. |
| E6 | **Unlock secrets.** Each slot is its own X-Wing key pair. Its private seed is wrapped under a key derived from one secret, and the account's key is sealed to the slot's public key (`account_wrap`), so a new account key reaches every slot without its secret (E11). The secrets: an app password or MCP token (HKDF of its 256-bit secret, `internal/apppass/apppass.go:18-30`), a passkey (its WebAuthn PRF output), the passphrase (Argon2id), the recovery code (HKDF of 128 random bits), a local password where one exists (Argon2id), or a browser session (HKDF of the session token, which the server stores only as SHA-256, `internal/auth/session.go:65-66`). | Every request already carries one of these secrets. Unwrapping per request keeps replicas stateless: no unlocked key lives in any process between requests. A session slot dies with its session, so a stolen cookie database (hashes only) opens nothing. |
| E7 | **Passkeys for unlock only; KyCalendar verifies no assertion.** The browser calls WebAuthn with the PRF extension and a per-slot salt, and sends the PRF output over the signed-in session. A wrong output fails to unwrap. | Sign-in is already KyIdentity's job. The PRF output is the secret, so there is no server-side WebAuthn code to get wrong. A passkey registered with KyCalendar's relying-party ID belongs to KyCalendar, so another app's passkey cannot unlock it. |
| E8 | **The passphrase is the fallback**, for browsers or authenticators without PRF support. It is at least 12 characters and is stretched with Argon2id (`ky-primitives/password`). | Owner decision 4. A stolen database can be searched offline against a weak passphrase, so the setup page recommends the passkey and states the trade-off. |
| E9 | **The recovery code is 128 random bits**, shown once at setup as 26 base32 symbols in groups. It can be replaced while unlocked. It does not use `ky-primitives/recoverycode`, whose 60 bits (`recoverycode.go:29`) are too weak for a key someone can attack offline. | Owner decision 5. KyPost's recovery secret is also 128 bits (`frontend/src/lib/keyVault.ts:88`). |
| E10 | **A credential that cannot unlock an encrypted calendar is refused**, never served the skeleton. CalDAV gets `403` with a body that says to create a new app password; the web shows the unlock screen. | A calendar app that read a skeleton would show untitled events, and writing one back would erase the content. |
| E11 | **Existing app passwords get a 14-day transition, which ends in a key rotation.** At setup, a `transition` slot's private seed is wrapped under the deployment key (`KY_ENCRYPTION_KEY`, purpose `content-transition`). Each existing app password or MCP token gets its own slot at its first use in those 14 days. The transition slot stays until `transition_until` passes, however many credentials have moved over. **When it passes, the elected worker rotates the account, with no user present:** (1) it opens the transition slot for the account key K0, as every first request of a not-yet-moved credential has during the window (that request opens it to seal K0 to the credential's new slot), and the last time it is ever opened; (2) it makes a new account key K1 and seals it to every other slot's public key (E6); (3) it seals K0 to K1, so the user's own older content still opens; (4) for every calendar the account holds, it makes a new calendar key version and wraps it to every holder's current account key; (5) it deletes the transition slot and forgets K0, K1 and the new seeds. All of that is one transaction under the calendar lock, and a run that dies is repeated whole at the next pass. From then on every write seals under the new versions. Older credentials that were never used are refused (E10). The user can end the transition early from security settings, which runs the rotation at once. **A shared calendar is protected only after its last holder's transition ends.** Step (4) wraps the new version to every holder's current account key, and a holder still in its own transition has an account key its transition slot opens. So each calendar key version records `exposed`, set when any holder wrap of that version reaches an account key with a transition slot. Each holder's own rotation makes a fresh version, and the first version that no transition slot reaches is the calendar's protection point. **A protected calendar is never wrapped to a transition-reachable key:** a share to an account still in its transition (E13) waits until that account's rotation, and the member is told the date and that ending the transition early opens the calendar now. | Without the transition, every device would have to be set up again at the moment of encryption. Without the rotation, a backup taken in the window (capsules include `encryption.key`) would open the account key for good, and with it every calendar key and all content written later, in this account and in every calendar it shares. After the rotation, that backup opens only content stored before the transition ended: K1 is sealed only to slots whose private halves are under the users' own secrets, never under the deployment key. For a shared calendar, "the transition" is the last of its holders' transitions to end: until then every new version is `exposed`, and the admin page shows, per calendar, whether it is protected and, if not, the date it will be. A member joining later never re-exposes it, because their share waits for their own rotation. |
| E12 | **New calendars and accounts are encrypted from the first unlock.** An account with no key keeps writing legacy rows, as today. Its first web unlock runs setup: create keys, show the recovery code, register a passkey or set a passphrase. A background sweep then seals the legacy rows of every calendar that gets a key. **A calendar gets a key only when every account with access to it has an account key**: a personal calendar at its owner's setup, a group calendar once its last current member has run setup. The key is made at the next unlocked request of any member, wrapping the seed to every member's public key. Until then the group calendar stays legacy, readable to every member's existing credentials as today, and the admin page shows which members it waits for. Someone who joins a group whose calendar already has a key must run setup to read it, and is told so (E10). | Owner decision 6, applied per calendar: no member is locked out of a calendar they can read today because someone else set up encryption. Encrypting a shared calendar before every member can open it would break their native apps. Decrypting a keyed calendar for a new member would undo everyone else's encryption. Sealing needs only public keys, so the sweep needs no user present. Setup needs the web because the recovery code must be shown to the user. |
| E13 | **Sharing a calendar wraps its seed to each holder.** When a grant gives a new account access (a grant change, or a group membership arriving from the suite mirror), a `pending_key_shares` row is queued. Any current holder's next unlocked request completes it, wrapping **every** kept version of the calendar key (E3, E11) to the new holder, so content sealed before a rotation, and worker parts tied to an older version, open for them too. Until then, the new member sees the calendar as locked. A share to an account still in its transition waits for its rotation (E11). **A share back to an account that reset encryption needs a holder's approval:** a reset needs only a sign-in, so an ordinary request never completes it (Web flows, Reset encryption). **Checks before sharing** (CE5): (1) a request that completes a pending share first re-checks that the target account has access now (`calendar_grants` and the membership), not only that a `pending_key_shares` row exists; (2) every unlocked request that opens a seed derives the calendar's public key from it and compares it with `calendar_keys.public_key`, and on a mismatch refuses the key for writing and sealing and audits `calendar_key_mismatch`; (3) every completed share is audited (`calendar_key_shared`, naming the calendar and the account) and listed under "Shared with" in each holder's security settings. Removing access deletes the holder's wrap, **except the last ones.** If a removal would leave no current holder (the group is deleted, or every member leaves or is deactivated), the wraps stay as *former* holders. Any unlocked request of a former holder's account then completes the calendar's pending shares, even though that account can no longer open the calendar through the server. Once a current holder has the seed, the former wraps are deleted. Deleting a group or its last member warns the admin when the calendar would become unreadable, which happens once no former holder's account remains. Delete everything deletes the account's wraps with the account, and its shared calendars pass to another member (tenancy D16, #198), who reads them through their own wrap. | The seed can only be wrapped by someone who can open it. Group calendars have members who are active, so pending shares clear quickly. Group calendars outlive their group (deleting a group keeps the calendar), so the content must survive the gap until the next grant. A former wrap gives no access the server would allow, and its holder could already read the seed. The seed is not rotated on removal: the removed member could already read it, and the times were never secret. |
| E14 | **Outgoing scheduling messages** are still created inside the sender's request. Every queued body, for internal and external recipients alike, is sealed under the deployment key (purpose `outbox`) until delivered or sent, and blanked afterwards as today (`internal/store/scheduling.go:154-158`). **Delivery to an internal recipient decides the format at delivery time,** inside the delivery's transaction under the calendar lock: if the target calendar has a key then, the worker opens the body and writes it as a base sealed to that calendar's current public key (E4). If it has none (its owner has not run setup, or a group calendar still waits for a member, E12), it writes a legacy row, as today; a later sweep seals it. The dedupe key becomes an HMAC under a deployment-derived key instead of SHA-256 over the body (`davbackend/scheduling.go:188`). | The worker that delivers or sends never needs the sender's key. Deciding at delivery means a recipient who runs setup, or resets and gets a new calendar key, between queue and delivery always gets the right format. Sealing at queue time could seal to a key that no longer exists, or to none. An external message is mailed in plaintext by design, and every message waits at most 24 hours (`outboxGiveUp`); deliveries to internal recipients usually take seconds. A bare hash of content would let anyone with the database confirm a guess. |
| E15 | **Formats are versioned and documented** in `docs/CONTENT-ENCRYPTION-FORMAT.md` with test vectors: the skeleton split, parts, slots and HPKE parameters (X-Wing, HKDF-SHA256, AES-256-GCM, `info` naming the format and version, AAD binding the calendar ID, object UID and each part's readable fields: kind, `keep-alarms`, sequence number, label, and the key it is sealed under, either a calendar key version or a worker key ID). | Option 1 means a device must read and write these formats without the server. Binding the AAD means a part moved to another object or calendar, relabelled or renumbered fails to open. The sequence number is the object's `parts_version` when the part was written, not its position: a newer same-label patch replaces an older one and a fold rebuilds the list, so a position would change and later parts would stop opening (found while building CE1a). |

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
  deleted at setup and never written again; its search decrypts the account's tasks per request,
  matching words as the index does (`MatchTask`: word prefixes on SQLite, substrings on
  PostgreSQL), so results do not change with encryption. Fine for thousands of tasks; measured in CE2.

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
  - **The booker copy holds content only.** It has no skeleton of its own: like every other
    object, its readable properties come from the host event's skeleton (E2), which every
    readable change already updates. Its parts are the content the booker's invitation carries.
  - **Confirm** seals the booker copy's base (the event as the booker's invitation carries it) to
    that public key.
  - **Every later host write** to the event is unlocked. It re-seals the booker copy's base as the
    booker now sees it, in the same transaction that queues the booker's update today.
  - **A worker's content patch** to the host event (`conference`, `set LOCATION`) appends the
    same patch to the booker copy, sealed to the booking's public key (E4, E5). A worker's
    readable change (the times of a reschedule, a `PARTSTAT` from a reply, a room's answer)
    changes the host skeleton alone, as for every object.
  - **A manage-link action** derives the key from the presented token, opens the booker copy,
    merges it with the host event's current skeleton (E2), changes the times in that skeleton,
    and mails the merged update under E14. It never writes readable properties into the booker
    copy.

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
- **`export-org`** (tenancy spec, step 4e), for each member: skeletons and sealed parts; the
  member's recovery slot and the account key wraps it opens (current and previous versions, E11);
  the holder wraps of every calendar key version of every calendar the member holds; and the
  `worker_keys` rows those calendars' parts name. `kycalendar open-export -in <dir> -recovery-code -`
  opens one member's calendars offline with only the recovery code, as a client would in option 1:
  recovery slot, then account keys, then calendar key versions, then each part under its
  calendar key version or worker key. CE6 tests that an export with an unfolded worker patch and a
  `keep-alarms` base opens completely.

**ETags** become the SHA-256 of `data` followed by `sealed`, computed at each write (today the
body's, `calendars.go:369-370`) and stored in the existing `etag` column. They change whenever a
write changes either, and the server computes them without a key. Folding (E4) does not count as
a write, so it leaves the stored ETag as it was.

## Schema

Two migrations, each with both backends' SQL, as every migration since version 20 is:
- **migration 38 (CE1c, #196):** the tables `account_keys`, `key_slots`, `calendar_keys`,
  `calendar_key_holders`, `pending_key_shares` and `worker_keys`, and the columns `sealed`,
  `parts_version` and `plain_size` on `calendar_objects`, `sealed` on `scheduling_outbox`, and
  `sealed_hold`, `public_key`, `wrapped_seed` and `sealed_copy` on `bookings`;
- **CE2's migration:** everything the rotation and the size limit need. That is the table
  `account_previous_keys`, the columns marked "CE2's migration" below, `content_sizes` and
  `alarm_bytes` on `calendar_objects`, and the usage triggers' change.

Every table either migration adds is class D (below).

- `account_keys (user_id PK, public_key, created_at, transition_until NULL)`. CE2's migration adds
  `key_version`, and `account_previous_keys (user_id, key_version, sealed_seed)` for each older
  version sealed to the current one (E11).
- `key_slots (user_id, slot_id, kind, wrapped, params, created_at, last_used_at, PK (user_id, slot_id))`.
  `wrapped` is the slot's private seed under its secret. CE2's migration adds `public_key`,
  `account_wrap` (the account key sealed to the slot) and `account_key_version` (E6).
  `kind` is `apppass`, `mcp`, `passkey`, `passphrase`, `password`, `recovery`, `session`,
  `transition`, and later `device`. `params` holds the Argon2id parameters or the PRF salt. An
  app-password, MCP or session slot is deleted with its credential.
- `calendar_keys (calendar_id PK, public_key)` and
  `calendar_key_holders (calendar_id, user_id, wrapped_seed, PK (calendar_id, user_id))`. CE2's
  migration adds a `version` to both, in their keys, so every version a rotation made stays
  (E3, E11), and `exposed` to `calendar_keys` (E11, set when a holder wrap of that version reaches
  a transition slot).
- `pending_key_shares (calendar_id, user_id, queued_at, PK (calendar_id, user_id))`.
- `calendar_objects` gains `plain_size INTEGER NULL` (E4, size), and in CE2's migration
  `content_sizes TEXT NOT NULL DEFAULT '{}'` (E4: per label, the sizes a worker's change replaces) and
  `alarm_bytes INTEGER NOT NULL DEFAULT 0` (E4, the alarms a `keep-alarms` base keeps).
- `bookings` gains `public_key`, `wrapped_seed` and `sealed_copy` (the booker copy, Readers).
- `worker_keys (id PK, calendar_id, wrapped, created_at, ended_at NULL)`, plus the calendar key
  version it is wrapped to (CE2's migration), deleted when no part references it
  (E4).
- `calendar_objects.sealed BLOB/BYTEA NULL`. The usage triggers (`migrations.go:410-428`,
  `:712-730`) count `plain_size` when `sealed` is set and `LENGTH(data)` otherwise (E4); CE2 changes
  them, as migration 38 (CE1c) leaves them counting `LENGTH(data)`.
- `scheduling_outbox` gains `sealed`, and `bookings` gains `sealed_hold` (the name and note
  under the `booking-hold` key).
- `calendar_objects` gains `parts_version INTEGER NOT NULL DEFAULT 0` (E4, folding).
- **Additive only.** No migration in CE1 to CE5 drops or rewrites a column a running reader uses:
  `bookings.name` and `bookings.note` keep serving holds made with the setting off, and
  `task_search` keeps serving unencrypted accounts. CE3 writes `sealed_hold` instead of the plain
  columns only for hosts with an account key. Migrations are never edited once released, so the
  schema grows in the phase that adds each reader.

Every new table is derived (class D in the tenancy spec's Inventory): it cascades from `users` or `calendars`, as `sessions` and `app_passwords` do, so an org purge removes it with them and no new org triggers are needed. Migration 38 built this for its tables; CE2's `account_previous_keys` follows the same rule (`ON DELETE CASCADE` from `users`), and joins the classification, the purge test's fixture and `migrationTables` in the same PR. `calendar_key_holders` also carries `former` (E13).

## Web flows

- **Setup** (the first unlock of an unencrypted account): explain what is encrypted and what is not,
  register a passkey or set a passphrase, show the recovery code and require it to be typed back.
  Then start the sweep and the 14-day transition (E11).
- **Unlock**: the browser asks for the passkey, or the passphrase, or the recovery code ("Use
  recovery code"). It creates the session slot. Local-password accounts in single-tenant installs
  unlock at sign-in, from the password slot.
- **After recovery-code unlock**: require a new passkey or passphrase, and offer a new code.
- **Security settings**: list slots (passkeys, passphrase, recovery code, app passwords and MCP
  tokens with their last use, the transition's end date, with a button to end it now, E11), and replace or remove them. Removing the
  last web unlock method is refused unless the recovery code is set.
- **Reset encryption** (every secret lost): after a typed confirmation, in one transaction, the
  account gets a new key pair and loses every old slot. Then, for each calendar the account holds:
  - **if another current holder has a wrap** (E13), the account's own wrap, which only the old key
    opens, is deleted and a `pending_key_shares` row is queued for it, marked `reset`. A reset
    needs only a KyIdentity sign-in, and the old key that would prove the user is the same person
    is gone, so a `reset` share is completed only when a different current holder, unlocked and
    still granted the calendar, approves it in their security settings ("Ana reset encryption.
    Give them back this calendar's history?"). Approving re-shares every kept version to the new
    account key and is audited (`calendar_key_reshare_approved`, naming both accounts). Until
    then the account sees the calendar as locked, and every holder's ordinary request leaves it
    so. The request stays listed for every holder until one approves it, and is deleted if the
    account loses the grant;
  - **if only former holders have a wrap**, the account's own wrap is deleted and nothing is
    queued: it no longer has access, and the former wraps stay for the next grant (E13);
  - **otherwise** the calendar gets a new key, keeps its times, and loses its content: its sealed
    parts are deleted, and its objects are served as skeletons from then on.
- **Admin pages**: per account, whether it is encrypted, its transition end date, and how many
  legacy rows remain; per shared calendar, whether it is protected and, if not, the date it will
  be (E11). Never content.

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
  - an account in its 14-day transition, whose `transition` slot opens with `encryption.key` (E11).
    A capsule taken in those 14 days exposes the content that account held before its transition
    ended. For a calendar it shares, it exposes the content stored before the calendar's
    protection point: the rotation that ends the last holder's transition, which can be later
    than this account's own (E11). It exposes nothing stored after that point, even with a newer
    database: the newer versions are wrapped only to account keys sealed to slots under users' own
    secrets, and a member who joins during their own transition gets no wrap until it ends;
  - queued scheduling messages, for up to 24 hours (E14);
  - booking holds' names and notes, for up to 15 minutes.

  CE6's updates to `docs/ASSURANCE-CASE.md` and `docs/STRONG-CRYPTO.md` carry this list as it
  stands here.
- **Operator or staff** reading the database or running `export-org`: the same as a stolen
  database for every calendar that has a key. Calendars without one are readable in full, as
  today: those of accounts that never ran setup, and group calendars waiting on a member (E12).
  The deployment key also opens, from a copy made during an account's 14-day transition (E11),
  the content stored before that transition's rotation, and in a shared calendar the content
  stored before the calendar's protection point, for outgoing scheduling
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

## As built (CE2, first PR)

Behind `KYCALENDAR_CONTENT_ENCRYPTION`, default off; startup refuses to run with it off once an
account has set up. Migration 43 and `internal/store/content.go`, `internal/api/content.go`,
`web/src/pages/Encryption.tsx`. Built: seal on write and merge on read for every request path
that reaches the store (CalDAV, web, MCP, imports); E10 refusal with `403` on CalDAV and
`content_locked` on the web; app-password, MCP, session, passphrase, recovery and transition
slots; web setup and unlock with the passphrase or the recovery code; the 14-day transition and
its rotation (elected job every 10 minutes, and "end now"); calendar keys only once every holder
has an account key (E12); usage counting `plain_size`; task search of encrypted lists by
decrypting per request.

Choices made where the spec left them open, each the narrowest that keeps its promises:

- **Workers before CE3.** A worker's new object in a keyed calendar, or its rewrite of a legacy
  row there, is sealed as a base under a fresh worker key used for that one object (not one key
  per run). A worker writing over a sealed row changes readable properties only: its object's
  skeleton replaces `data` and the parts stay, so content never reaches `data` and nothing the
  worker could not read is lost. Until CE3's patches and `keep-alarms` bases, a worker's
  *content* change to an existing sealed object (an organizer's new title reaching an
  attendee's copy, a room's `LOCATION`, a KyMessages link) is dropped; readable changes apply.
- **Rotation** rewraps the account's older calendar seeds to K1 rather than reaching them through
  `account_previous_keys` (which it still writes). Versions made before the rotation stay
  exposed to a copy taken during the transition, as E11 already says; versions made at and after
  it are wrapped only to keys sealed to user slots.
- **No passkey PRF yet**: the passphrase (E8) is the web unlock; the recovery code unlocks too.

**The sweep (second PR).** Every 2 minutes an elected job seals the legacy rows of every calendar that has a key. It works in batches of 200, each batch in one transaction, with one worker key per object so that a later write dropping that key affects only its own object. A row is replaced only after its sealed base opens again and merges back to the exact original bytes, and only if it is still the row the batch read. ETags and counted usage do not change, so clients refetch nothing. Rows of accounts that never ran setup are not touched (option A, the spec default). Downgrading to a release before CE2 once anything is sealed is unsupported: an older server would serve skeletons. The content is not lost; upgrading again serves it.

Left for later PRs: folding and patches (CE3), outbox, booking-hold
and feed sealing (CE3), pending shares (CE5), passkeys, password slots, reset, admin pages,
`export-org`/`open-export`, and the 2,000-object multiget timing.

## Phased build plan

Every PR ships behind the hidden setting `KYCALENDAR_CONTENT_ENCRYPTION` (default off) until CE6
turns it on.

1. **CE1, formats and keys**, in three PRs: `internal/contentkey` (CE1a: keys, slots, the HPKE
   suite and the part format, E3–E6, E15), `internal/skeleton` (CE1b: the split and merge, E1, E2,
   E5) and migration 38 (CE1c). `docs/CONTENT-ENCRYPTION-FORMAT.md` with test
   vectors. The new tables and columns (Schema), none of them read yet. No behaviour change.
   - Tests: sealing then opening a base returns the original bytes exactly, for a corpus of real
     client objects (Apple, Google, Outlook, Thunderbird, DAVx5 exports, the existing interop
     fixtures). Each patch operation changes only its property: a `set LOCATION` or `conference`
     patch on top of a base leaves `SUMMARY`, `DESCRIPTION` (apart from the `Join:` line) and every
     `X-` property unchanged. A part opens only
     with its calendar, object and sequence number. Every slot kind opens with its secret and fails with any
     other.
   - Tests: splitting an object whose `VTIMEZONE` carries `COMMENT`, `TZNAME`, `TZURL` and
     `X-LIC-LOCATION` leaves only the rule properties (E1) in the skeleton, and merging returns
     every sealed one in place.
2. **CE2, store and request paths.** Seal on write, merge on read, folding, app-password and MCP
   slots, session slots, E10 refusal, task search over decrypted content.
   - This is a separate multi-layer milestone, not a small call-site change: migration 38's
     columns are unused by `internal/store`'s object model and reads/writes, and the schema
     additions above, credential unlock paths and request-time split/merge still need building.
     The shared-helper refactor leaves these, org-scoped key/holder lookups and CE2's acceptance
     tests pending. CE3–CE6 remain separate phases as listed below.
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
     read serves with the master's title, description and location. A room's later `set LOCATION` on that
     override keeps the master's title and description, before and after a fold.
   - Tests: a `REPLY` with a `COMMENT`, internal and from KyPost, updates `PARTSTAT` in the
     skeleton and stores the comment nowhere, as today.
   - Tests: a message queued for a recipient without a key, who runs setup before delivery,
     arrives sealed to the new calendar key; one queued for a keyed recipient who resets before
     delivery arrives sealed to the replacement key; one for a recipient still without a key
     arrives as a legacy row. A delivery over `MaxObjectSize` is refused as today and never stored as
     a bare skeleton; the sweep seals an object of exactly `MaxObjectSize`.
   - Tests: a booker who reschedules through the manage link after the host renamed the event
     gets an updated invitation with the new title and time; after KyMessages added a room link, the
     reschedule mail carries it; after the host's room declined and an attendee replied, the
     reschedule mail carries the room's and the attendee's `PARTSTAT` from the host skeleton; a wrong token opens nothing. Twenty
     room-link changes to an unopened meeting leave one `conference` patch with the latest link.
     A delivery whose plaintext is exactly `MaxObjectSize` is accepted. Worker patches on an object 2 KiB under `MaxObjectSize` are
     refused once the size they leave would pass it, so the fold after them stays under the limit
     and the user's next save of the object succeeds.
   - Tests: a room's `set LOCATION` that replaces a 40 KiB location with a short one, a
     `conference` removal, and a `REPLY` that makes an override of an event with a long
     description each leave `plain_size` equal to the merged object's size, before and after a
     fold, and equal to the size today's plaintext row would count. A worker `set` of any
     property other than `LOCATION` is refused before sealing.
   - Tests: a sweep run whose first object is folded by a user's read keeps its key and can open
     every object it writes afterwards. Fifty organizer updates to an attendee copy nobody opens
     leave two bases.
   - Tests: a fold racing a worker's patch, or a client's new body, on PostgreSQL never loses
     either: the fold includes the change or is skipped, and the next read folds what is there.
     An attendee copy holding two bases counts its merged plaintext size against the sender, the
     same as today's plaintext copy.
   - Tests: an organizer's update delivered to an attendee copy whose own alarms are large is
     refused when the update plus those alarms passes `MaxObjectSize`, and accepted one byte
     under; the copy's `plain_size` and the owner's counted usage include the kept alarms, before
     and after a fold, and match today's plaintext copy.
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
   - Tests: a new group member, and a member who reset encryption once another member approved,
     read content sealed under every kept calendar key version, including content from before a rotation, after another member's
     next request. A new group member reads the calendar after another member's next request, and not
     before. After a member resets encryption with only a sign-in, the group calendar stays locked
     to them through other members' ordinary unlocked requests, opens after a different member
     approves, and the approval is audited; the member who reset cannot approve their own share, and their personal calendars keep only times. A removed member is refused. A group calendar stays legacy and readable to a member
     who has not run setup until that member runs it. After the group is deleted and the calendar
     is granted to a new group, the new members can read its content after a former member's
     next request.
6. **CE6, migration and default on.** Setup's sweep, the 14-day transition and the rotation that
   ends it (E11), admin status,
   `export-org` and `open-export`, the restore drill. Docs: `docs/ASSURANCE-CASE.md`,
   `docs/STRONG-CRYPTO.md`, `docs/RUNNING.md`, `AGENTS.md`. The setting defaults to on.
   - Tests: a capsule taken during an account's transition, with its `encryption.key`, and the
     newer database after `transition_until`, open none of the content written after the rotation,
     neither in the account's personal calendar nor in a calendar it shares. Content written before
     the rotation still opens for the user through K0 sealed to K1. An interrupted rotation runs
     again whole.
   - Tests: a calendar shared by A (transition ends day 14) and B (setup day 7, ends day 21). A
     capsule from day 10 with the database from day 22 opens content stored before B's rotation,
     and nothing stored after it, though A rotated first. Between the two rotations the admin page
     shows the calendar unprotected until day 21, and after B's it shows it protected. A member C
     who joins on day 25 during their own transition gets no wrap and sees the calendar locked
     until C's rotation, or at once after C ends the transition early; a capsule from C's window
     opens nothing stored after day 21.
   - Tests: an upgraded install with two existing app passwords and an MCP token keeps all three
     syncing through the transition: the second and third still unlock after the first has its own
     slot. An app password unused past the window is refused. The sweep leaves no legacy rows, is resumable, leaves the account's counted usage unchanged, and finishes for an account already at its quota. A
     capsule restore reads nothing without a slot secret.

## Residual risks

- **Readable metadata.** Times, recurrence, attendees and their names, organizers, status and sizes
  stay readable to the operator, including the sizes of each event's location and room link and
  of the content an override inherits (E4). That is the price of owner decision 2.
- **Lost secrets lose content.** No escrow (owner decision 5). The setup flow makes the recovery
  code hard to skip, but users still lose it.
- **Weak passphrases** can be attacked offline from a stolen database (E8).
- **Plaintext left in the database's own storage.** SQLite: freed pages are zeroed
  (`secure_delete`), FTS5 deletes take terms out of the index (`secure-delete`, migration 44,
  which also rebuilt the index once to drop earlier deletes' remnants), and the sweep truncates
  the WAL, so after setup and the sweep neither the file nor a `VACUUM INTO` snapshot holds the
  account's task text. PostgreSQL: the rows keying deletes and the sweep replaces stay as dead
  tuples until VACUUM reuses their space, and in the WAL and its archive, so a physical copy
  (a disk image, a base backup, a replica's files) can hold them. A `pg_dump` and the backup
  capsule's snapshot copy live rows only. No request can force VACUUM to clear them, and
  VACUUM does not zero the space, so this is not fixed: an operator who needs it runs
  `VACUUM FULL task_search, calendar_objects` after the sweep and expires older base backups
  and WAL archives. Backups taken before setup hold the plaintext in every case.
- **The transition, the outbox and booking holds** are readable with the deployment key for
  their windows. For the transition, a copy from the 14 days keeps opening what was stored before
  its rotation, and in a shared calendar what was stored before its last holder's rotation, for
  as long as that copy exists; everything later stays out of reach (E11). A shared calendar whose
  members set up over several weeks stays exposed until the last of them is through, and the
  admin page shows which ones are. The outbox's window is 24 hours (E14), and booking holds' is 15 minutes (Readers).
  Expiring backups taken during a transition closes the first.
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

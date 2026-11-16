# Suite integrations (KyCalendar's half)

KyCalendar's copy of the wave 1 and wave 2 wire contracts (owner decisions 2026-11-14; source:
`ky-redesign/integrations/suite-integrations-contracts.md` and `suite-integrations-wave2.md`).
This copy is the durable one for this repository: change it together with the peer's copy.
Sections implemented here: shared rules, B (KyCalendar → KyMessages meetings), D (people picker,
KyCalendar's part), E (the `/new` deep link, receiver), F (the free/busy strip) and, from wave 2,
H (chat from the event view, KyCalendar's part), I (the KyDrive picker, as a caller) and L (meeting guests, KyCalendar's part).

Wave 2 compatibility rule: each side must keep working against the other side's wave 1 `main`
until both have merged. A new request field the old receiver refuses means: retry without it
and show the feature as unavailable.

## Shared rules

- Pairing follows KyPost↔KyCalendar: explicit env (`KYCALENDAR_<PEER>_URL` +
  `KYCALENDAR_<PEER>_SECRET_FILE`, optional `_ALLOW_PRIVATE`) wins; otherwise
  `suitedir.Peer(ctx, "<peer client id>")` with KyCalendar's own SSO client credentials. Same
  refresh, backoff, outage and unpair rules. Secret in memory only.
- Signed with `ky-primitives/syncauth` v1 under the pair secret. Receiver checks the event type
  equals the route's, then verifies with a replay guard. Verified replay: 200
  `{"outcome":"duplicate"}`. Any other failure: 401 `{"error","code":"bad_signature"}`.
- Unpaired receiver: 404 `<peer>_off` (e.g. `kycalendar_off`).
- Bodies name the user by `"sub"` and `"issuer"`. The receiver resolves an active everyday
  account linked to that subject and issuer, else 404 `no_user`. Dedicated administrator
  identities cannot use these workflows.
- Refusals are `{"error","code"}`, code `[a-z0-9_]{1,64}`.
- Outbound: HTTPS only, no redirects, no loopback/link-local/multicast, host resolved once.
- Audit success and refusal with IDs only. Never log secrets, tokens, titles, names.
- Per-user rate limit on every route.
- Client IDs: `kypost`, `kycalendar`, `kymessage` (singular), `kydrive`.

## B. Meetings: KyCalendar → KyMessages

Members only; guests, windows, `/meet/<token>` and the Synapse confinement module stay unbuilt.
Pairing is the suite pair `kycalendar`↔`kymessage`.

| Event type | Route on KyMessages console |
|---|---|
| `kycalendar.meeting.put` | `PUT /api/meetings/{id}` |
| `kycalendar.meeting.close` | `DELETE /api/meetings/{id}` |

`{id}`: KyCalendar's opaque meeting ID, `[A-Za-z0-9_-]{16,64}`, random, never the event UID.

### PUT (idempotent)

```json
{"sub":"<organizer>","issuer":"https://id.example","title":"Weekly sync",
 "member_subs":["…","…"]}
```

- Creates the room on first call, reconciles after: room name = title, listed members invited,
  members no longer listed removed. The organizer is always a member.
- `member_subs` holds KyIdentity subjects (KyCalendar resolves attendee addresses with
  `suitedir.Resolve`). A subject with no KyMessages account yet is skipped, not refused, and
  reported back; a later PUT picks it up. KyCalendar repeats the PUT on its daily refresh
  (`refresh_at`) while the last one skipped someone and the event has an occurrence still to
  come; one that skipped nobody, an ended event and a cancelled (closed) one get no refresh.
- A `guests` field that is present and non-empty: 422 `guests_unsupported` (wave 1 KyMessages;
  section L replaces this).
- Room state: join rule `invite`, history visibility `joined`, encryption on, power levels
  console 100, organizer 50, members 0, invite 50, kick 50.

```json
{"member_url":"https://chat.example/#/room/!abc:example","skipped_subs":["…"]}
```

| Status | Code | When |
|---|---|---|
| 404 | `kycalendar_off` | Not paired. |
| 401 | `bad_signature` | |
| 400 | `bad_body` | Malformed, bad `{id}`, title empty, over 255 bytes or with control characters, more than 100 members. |
| 404 | `no_user` | Organizer has no active KyMessages account linked to that subject. |
| 409 | `closed` | The meeting was closed; a closed ID is never reopened. |
| 429 | `rate_limited` | `Retry-After` set. |
| 503 | `unavailable` | Matrix stack unreachable; retry. |

### DELETE

Closes the room as the console's Close does (blocked, members out, history kept). 200
`{"outcome":"closed"}`; an unknown or already closed ID is also 200.

### KyCalendar side

- Event editor: a "Video meeting" switch, shown only when KyMessages is paired and the user can
  open it. Default on for a new event with at least one attendee, off otherwise; the user's
  choice is kept per event.
- The link is stored in the event as `CONFERENCE;VALUE=URI;FEATURE=VIDEO;LABEL=KyMessages:<url>`
  (RFC 7986) and as a final description line `Join: <url>` so other calendar clients show it.
  Web edits must preserve both.
- Event view: a Join button. Beside the switch, one line saying what people outside the
  organisation get (section L).
- PUT and DELETE go through a durable outbox (like `scheduling_outbox`): on create, on title or
  attendee change, on switch off, on event delete or cancel. Recurring events use one room for
  the series.
- Only the organizer's copy drives the room. An invitee's copy shows Join and no switch.

### How KyCalendar implements it, and where it is more specific than the contract

- **Issuer.** `issuer` is the KyIdentity issuer URL the organizer signed in with: the account's
  `sso_issuer` binding (`kyidentity <issuer URL>`) without its kind, so KyMessages can match it
  against its own issuer.
- **DELETE body.** The contract names none; KyCalendar signs `{"id","sub","issuer"}` of the
  organizer, like every other body.
- **`id` in both bodies.** The signature does not cover the URL, so both bodies also carry the
  path's meeting ID as `"id"`; KyMessages answers 400 `bad_body` when they differ (its
  `docs/KYCALENDAR.md`). A KyMessages that predates the check ignores the field.
- **Event IDs.** Every attempt carries a fresh `X-KySignOn-Event-ID`, so a retry is a new event,
  not a replay: a `duplicate` answer would carry no `member_url`. PUT and DELETE are idempotent
  on the receiver, so this is safe.
- **`kycalendar_off`** is retried with backoff (a pairing settling on KyMessages' side), as are
  429, 5xx, the network and, on a KyIdentity peer, 401 (after a fresh lookup of the secret).
  Any other refusal is kept as the meeting's error and not retried until the event changes;
  `closed` ends the meeting.
- **`member_url`** must be absolute `https`, without credentials, at most 2048 bytes; anything
  else is treated as a refusal (`bad_reply`) and never written into an event.
- **Title.** Control characters become spaces, at most 255 bytes, `Meeting` when empty.
- **Members.** Attendees of every VEVENT but the organizer, at most 100, resolved with
  `suitedir.Resolve`. A failed lookup is retried with backoff; meanwhile a new room is created
  with the organizer only and an existing room is not PUT (a PUT removes unlisted members).
- **Mail.** The first invitation leaves before KyMessages answers; once the link is written into
  the organizer's event, the scheduler sends the update, which carries `CONFERENCE` and the
  `Join:` line.

## D. People picker (KyCalendar's part)

KyCalendar adds `GET /api/suite/people?q=` for its own signed-in users (session auth, CSRF
rules as its other GET routes), backed by `suitedir.People` with the session's subject, cached
per user and query for 60 seconds, empty list on any failure. The attendee field autocompletes
from it; free text still works.

How KyCalendar implements it: everyday users only (administrators get 403 like every
everyday route); `q` of 2 to 64 characters, else an empty list without asking KyIdentity;
`limit` 10; 60 searches per user per minute (429 `rate_limited`); only accounts the live
KyIdentity binding signed in reach KyIdentity. The query is never logged.

## E. "New meeting" deep link (KyCalendar receives)

`https://<kycalendar-origin>/new?title=<≤255 bytes>&attendees=<comma-separated addresses,
≤100>&body=<≤2000 bytes>`

- Opens the event editor prefilled. Nothing is stored until the user saves. All values are
  untrusted text: rendered as text, attendees validated as addresses, anything invalid dropped
  silently.
- Survives the sign-in redirect when the app has a return-to mechanism.

How KyCalendar implements it: KyCalendar has no return-to (sign-in, SSO included, lands on
`/`), so the page keeps the query in `sessionStorage` (this tab only) and moves to
`/#/calendar`; once a calendar to save to has loaded, the editor opens prefilled and the copy
is removed. A title or body over its limit or holding control characters (a title also
newlines or tabs) is dropped; attendees past the 100th, duplicates and anything that is not
`local@domain` are dropped. Guests show only where invitations are on (`scheduling`).

## F. Free/busy strip (KyCalendar's part)

KyCalendar event editor: a free/busy strip for attendees in the organisation, from the existing
`GET /api/freebusy`.

How KyCalendar implements it: one request for all guests over the chosen local day (the route
takes up to 50 addresses, so no new backend); rows only for people the server knows (not
`unknown`); busy and tentative blocks across the day, the chosen time outlined, and "Busy at
this time" when a block overlaps it (an all-day event is the whole day). Any failure shows no
strip. It appears with the People section, so only where invitations are on.

## H. Chat from the event view (KyCalendar → KyMessages)

| Event type | Route on KyMessages console |
|---|---|
| `kycalendar.chat.open` | `POST /api/suite/chat` |

One route serves KyPost (`kypost.chat.open`) and KyCalendar; the receiver accepts each type only
from its own pair secret. Pair: `kycalendar`↔`kymessage`, the meetings pair.

```json
{"sub":"<caller>","issuer":"https://id.example","target_sub":"<person to chat with>"}
```

200 `{"url":"https://chat.example/#/user/@bob:example"}`: Element's user view, which offers
"Message" and reuses an existing direct chat. The console creates nothing.

| Status | Code | When |
|---|---|---|
| 404 | `kycalendar_off` | Not paired. |
| 404 | `no_user` | Caller has no active KyMessages account. |
| 404 | `no_target` | Target has no active KyMessages account, is the caller, or is an administrator identity. |
| 400 | `bad_body` | |
| 429 | `rate_limited` | |

Caller: a "Chat" action on each organisation attendee in the event view, shown only when
KyMessages is in the user's suite app list and `suitedir.Resolve` places the address in the
organisation. Clicking asks KyCalendar's own session route, which calls the console and opens
the answer in a new tab. `no_target` shows "This person isn't on KyMessages yet." The answer's
URL must be `https://` on the paired KyMessages origin's Element host, else it is dropped.

### Where Element is (shared with KyPost)

The pairing names the KyMessages console's URL, not Element's host, so KyCalendar reads Element's
origin from the user's suite app list, entry `kymessage-chat` (as KyPost does, KyPost #613). Chat
shows only when that entry is there with an `https` URL, and the answer must be on exactly that
origin and also pass `kymessages.ValidChatURL`: no credentials, path empty or `/`, no query,
fragment exactly `/user/@<localpart>:<server>`. Anything else is `bad_reply` and never reaches the
browser. KyMessages answers `ChatHost + "/#/user/@bob:example"`; `KY_MATRIX_CHAT_HOST` must equal
the `kymessage-chat` launch URL's origin.

### How KyCalendar implements it

- **Session routes.** `POST /api/suite/chat {"email"}` (everyday, CSRF) → 200 `{"url"}`,
  `Cache-Control: no-store`. It resolves the address with `suitedir.Resolve` as the caller; no
  match, or the caller themself, is 404 `no_target` without asking KyMessages. Refusals: 400
  `bad_body` (not one address, body over 1 KiB), 429 `rate_limited` (20 per user per minute),
  404 `chat_off` (KyMessages not linked, the account did not sign in through the live
  KyIdentity binding, or no `kymessage-chat` in the user's suite apps), 404 `no_target`/`no_user` (passed on from
  KyMessages), 502 `refused` (any other refusal or a bad link), 503 `unavailable` (the people
  lookup, the network, 429/5xx or `kycalendar_off` from KyMessages; never retried for the user).
- **Who gets the action.** `GET /api/suite/people?email=…` (repeated, 1 to 100 addresses) answers
  which addresses are organisation people, with the search's rate limit, minute cache and
  empty-list-on-failure rules. The event view asks it once for the organizer and attendees but
  the user, and shows Chat beside each match.
- **Body.** `issuer` is the issuer URL: the account's `sso_issuer` binding without its
  `kyidentity ` prefix. Every attempt carries a fresh event ID.
- **Audit.** `chat.open`, or `chat.refused` with `code=`, by user ID only: never the address,
  the subject or the link.

## P. Status from the calendar (KyCalendar → KyMessages)

Owner request 2026-11-15. KyMessages keeps a member's coarse status and is the contract's owner
(KyMessages `docs/PRESENCE.md`, the durable copy of the wire format; design
`docs/superpowers/specs/2026-11-15-presence-design.md` there). Pair: `kycalendar`↔`kymessage`.

| Event type | Route on KyMessages console | KyCalendar sends |
|---|---|---|
| `kycalendar.presence.busy` | `POST /api/suite/presence/calendar` | `{"sub","issuer","revision","busy":[{"start","end"}]}` |
| `kycalendar.presence.me` | `POST /api/suite/presence/me` | `{"sub","issuer","share"?,"manual"?}` |
| `kycalendar.presence.lookup` | `POST /api/suite/presence/lookup` | `{"sub","issuer","subs":[≤50]}` |

KyMessages decodes strictly: any other field is refused, so a title, location, attendee or
event ID can never ride along. States: `available`, `busy`, `in_a_meeting` (with `until`),
`away`, `out_of_office` (with `until`), `unknown`.

**Busy blocks.** `davbackend.MeetingBlocks`: instances in the member's personal event
calendars, from now to 48 hours ahead, that are opaque, not cancelled or tentative, and that the
member organised (or that have no organizer) or accepted (`PARTSTAT=ACCEPTED` on the member's
own `ATTENDEE`, per instance). Out-of-office events (`X-KYCALENDAR-OUT-OF-OFFICE:TRUE` or
`X-MICROSOFT-CDO-BUSYSTATUS:OOF`) are left out: out-of-office reaches KyMessages from KyPost.
Start and end only, RFC 3339 UTC; `revision` is the compute time in milliseconds.

**When.** `presence_pushes` (migration 30) is one outbox row per everyday member with a KyIdentity
subject. Every object write and calendar delete bumps the owner's row in the write's
transaction (`touchPresence`); a sent row is due again after `store.PresenceRefresh` (6 h).
`SyncPresence` runs on the meetings ticker (5 s), 20 rows a pass, only while KyMessages is
linked. 429, 5xx and network errors back off as meetings do; a refusal (`no_user`, ...) waits
for the next change or refresh. Nothing is kept in memory and nothing is scheduled per event.

**Session routes** (everyday members, `meetingsFor`):
- `GET`/`PUT /api/presence/me`: Settings → Status. `share` is "Show my status to colleagues"
  (on by default, KyMessages' default); `manual` is `{"state":"available|busy|away","until"}`
  within 7 days, or `null` for automatic. Unknown fields are 400. 30 a minute. 409
  `status_off` without KyMessages or a KyIdentity sign-in; 409 `sharing_off` for a manual
  status while sharing is off.
- `POST /api/presence {"emails":[≤50]}`: `resolveAs` keeps organisation people only, then
  one lookup for the subjects not cached; answers are cached by subject for 60 seconds.
  Answers `{"people":[{"email","state","until"?}]}`, organisation people only. 60 a minute;
  400 `too_many` over 50.

**UI.** `StatusLabel`: a dot and a text label ("In a meeting until 15:00", "Out of office until
5 Dec", "Status unknown"); the dot is `aria-hidden` and colour never carries the meaning alone.
Shown on attendee rows, the organizer line and, as text, in the Invite field's suggestions.

## I. KyDrive picker (KyCalendar is a caller, in the browser)

`https://<kydrive-origin>/pick?origin=<caller origin>&mode=link|copy`, opened as a popup on a
user click.

- KyDrive serves it only to a signed-in everyday user, and only when `origin` is exactly the
  origin of an app in that user's suite app list (from KyIdentity) and is `https://`. Otherwise
  it shows a plain refusal and sends nothing.
- The user picks one or more files they may share under KyDrive's own rules and confirms. Only
  that confirming click sends anything.
- `mode=link` (default): KyDrive creates an organisation link per file in the user's own session
  (role reader, the same expiry choice as contract C), then
  `window.opener.postMessage({type:"kydrive.picked", files:[{name,size,url,expires,mime?}]}, origin)`
  and closes. The target origin is the validated `origin`, never `*`. `mime` (added
  2026-11-15) is the file's media type as KyDrive serves it, set by KyDrive's server, present
  only when it matches `^[a-z0-9]+/[a-z0-9.+-]+$` and is at most 127 bytes.
- `mode=copy` is for the paired KyPost only.
- The picker page keeps `frame-ancestors 'none'`.
- Callers listen for exactly `kydrive.picked` from exactly the KyDrive origin, and treat every
  field as untrusted text.

KyCalendar event editor: "Add file from KyDrive" (`mode=link`). The event stores each link as
`ATTACH;FMTTYPE=<type if known>;FILENAME=<name>:<url>` (RFC 7986 / Outlook style) and as a
description line `File: <name> <url>`; the event view lists them. Attendees outside the
organisation see the line but cannot open it; say so beside the button.

### How KyCalendar implements it

- **Where KyDrive is.** The origin of the `kydrive` app in `GET /api/suite/apps`, only when it is
  `https`; without one there is no button (existing links still show and can be removed).
- **The listener** (`web/src/drivePicker.ts`) reads only a `message` whose `origin` is exactly
  that origin, whose `source` is the popup it opened, and whose `type` is `kydrive.picked`. Each
  file needs a name (1 to 255 bytes, no control characters or `"`) and a `url` of at most 2048
  characters on that same origin, without credentials or spaces; anything else is dropped.
  A `mime` that passes the same rule becomes the file's `type`; any other is dropped and the
  file kept. `size` and `expires` are not stored (the link's own page says when it expires). A closed popup
  ends the pick with nothing. KyCalendar sends no `Cross-Origin-Opener-Policy`, so `opener` survives.
- **Events API.** Create and update take `files: [{name, url, type?}]` (absent keeps, `[]`
  removes; series only, else 400 `attendees_series_only`); at most 20, each a trimmed name of 1
  to 255 bytes without control characters or `"`, an `https` link without credentials, spaces
  or controls (at most 2048 bytes); else 400 `bad_files`. An optional `type` is written as
  `FMTTYPE` only when it matches `^[a-z0-9]+/[a-z0-9.+-]+$` and is at most 127 bytes; any other
  is dropped, never refused, so no quote, semicolon, colon or line break reaches the line. The KyDrive origin is not checked server-side: the browser does that, and the
  server's rule is only that nothing but a safe `https` link is ever written.
- **Stored form.** Every VEVENT gets the `ATTACH` properties and one `File:` line per file, in a
  block after the user's text and before the `Join:` line (`calendar.SetFiles`). Only URI
  `ATTACH`es with `https` and `FILENAME` are KyCalendar's; other `ATTACH`es (inline files from
  other clients) are kept untouched. Every web, agent and import save puts the block back
  (`calendar.KeepJoin`).
- **View.** `files: [{name, url, type?}]` (https links only); the description omits the block.
  The web lists them as links opened with `noopener noreferrer`.

## L. Meeting guests (KyCalendar → KyMessages)

Builds the guest half of KyMessages `docs/superpowers/specs/2026-10-18-kymessages-meeting-rooms-design.md`
(owner-approved 2026-10-18). Where that spec and contract B differ, contract B's wire shape wins
(`sub`/`issuer`, not `organizer_sub`).

- KyMessages console gains an administrator switch "Let people outside the organisation join
  meetings", default **off**. Off: a non-empty `guests` answers 422 `guests_off` (replacing
  `guests_unsupported`, which old KyMessages still sends).
- PUT body gains `guests:[{key}]` (opaque per-attendee key, ≤100, `[A-Za-z0-9_-]{16,64}`, never
  a name or address) and `windows:[{start,end}]` (RFC 3339 UTC, ≤400, at most 90 days ahead).
  The answer gains `guest_urls:{key:url}`, returned only for keys new in this call or re-issued;
  a key dropped from the list revokes its link.
- KyCalendar: an attendee outside the organisation (by `suitedir.Resolve`) on an event with
  "Video meeting" on becomes a guest key. Each guest's link goes only into that attendee's own
  invitation; never into the stored event, its CONFERENCE property, the shared description or
  any other attendee's copy. If invitations are not per-recipient today, make them so for
  guests. Windows refresh daily through the outbox for recurring events. On `guests_off` or
  `guests_unsupported`, KyCalendar retries without guests and the editor says which attendees
  cannot join and why. The wave 1 line "people outside the organisation cannot join yet" becomes
  accurate to the state.

### How KyCalendar implements it

- **Who is a guest.** On each sync of a meeting, the attendees of every VEVENT but the organizer
  are resolved with `suitedir.Resolve` as the organizer; organisation people become
  `member_subs`, every other address (lower-cased, at most 100) a guest. A failed lookup keeps
  the stored guests and, as for members, never PUTs to an existing room.
- **Keys** are 32 random base64url characters (`newMeetingID`), one per meeting and address,
  stored in `meeting_guests` with the guest link sealed (AES-GCM under a key derived from the
  server's encryption key, label `kycalendar:guest-url`). A removed attendee's row and key go,
  so the next PUT revokes the link. A key that gets no link and has none stored (an answer lost
  in transit) is replaced by a fresh key and the PUT is retried with backoff.
- **Windows** are sent only with guests: each occurrence from now to 90 days ahead (cancelled
  ones skipped) as `[start − 10 min, end]` in UTC, at most 400, the last cut at 90 days.
- **Refusals.** `guests_off` and `guests_unsupported` make KyCalendar send the same PUT at once
  without `guests` and `windows`; the room works for members, the code is kept on the meeting
  (`guests_error`) and the guests get fresh keys without links. A meeting with guests (recurring,
  or refused) is due again daily (`refresh_at`), so new windows reach KyMessages and a switch
  turned on is picked up within a day. A body without guests carries no `guests` or `windows`
  field, so wave 1 KyMessages sees exactly the body it knows.
- **Per-recipient invitations.** Every outbox row already has one recipient, and KyPost mails
  each to that one address. The link is added only there, at send time: for a REQUEST to an
  address outside the instance, the scheduler asks for that address's link on the sender's
  open meeting for that UID and, if one exists, appends `Join as a guest: <url>` as a final
  description line to that recipient's copy only. The stored event, its `CONFERENCE`, the
  queued body and every other copy are untouched. Recipients on this instance (internal
  delivery) never get a guest link.
- **Waiting for the link.** While the meeting has a change KyMessages has not answered and
  KyMessages is linked, such an invitation is held (`guest_pending`, the outbox's usual backoff)
  for at most 10 minutes after it was queued; then it leaves without a link, and the guest gets
  theirs with the next update of the event.
- **Editor.** The organizer's view carries `guests` (their addresses) and `guests_error`. Beside
  the switch: "<addresses> can join as guests, each with their own link sent only in their
  invitation.", "<addresses> cannot join: KyMessages does not let people outside the
  organisation join meetings." (`guests_off`), "<addresses> cannot join: this KyMessages cannot
  take guests yet." (`guests_unsupported`), or, before any sync, "People outside your
  organisation get their own guest link in their invitation when KyMessages lets guests join."
- **Never logged or audited:** keys, links, addresses. `meeting.refused op=put code=guests_off`
  records the refusal by meeting ID.

## O. Out of office (KyPost → KyCalendar)

Added 2026-11-15 under the existing KyPost↔KyCalendar pair (no new pair). KyPost's
out-of-office setting is the single source of truth (KyPost `docs/OUT_OF_OFFICE.md`).
KyCalendar stores what KyPost last sent and never turns out-of-office on itself, so the two
cannot loop.

`POST {KyCalendar}/api/kypost/out-of-office`, event type `kypost.ooo.set`, signed with
`ky-primitives/syncauth` v1 under the pair secret. The body is the user's whole state, every
time:

```json
{"sub":"…","issuer":"https://id.example","revision":1794760000123,
 "away":true,"start":"2026-12-01T05:00:00Z","end":"2026-12-05T05:00:00Z",
 "all_day":true,"time_zone":"America/New_York",
 "tentative":true,"comment_local":"…","comment_remote":"…"}
```

- `revision`: KyPost's save time in milliseconds, which only grows. KyCalendar keeps the
  newest revision it has seen. An older one is answered `{"outcome":"stale"}` and changes
  nothing. The same revision again is applied again, so a retry after a failure works.
- `away` false, sent when out-of-office is off or the user unticks "Show me as away", removes
  the event and stops automatic answers. The other fields are then ignored.
- With `away` true:
  - `start` and `end` (RFC 3339) are required. `end` is after `start`, and the period is at
    most 366 days.
  - `all_day` needs an IANA `time_zone` in which both instants are midnight; the event then
    uses DATE values.
  - The comments are plain text, at most 4000 characters each, with no control characters
    except newline and tab.

**The event.** One per user, UID `kypost-ooo-<hash of the user>`, in the default calendar
(or wherever the user moved it among their own calendars; a calendar shared to them is never searched or written):

- `SUMMARY:Out of office`, `TRANSP:OPAQUE`, `X-MICROSOFT-CDO-BUSYSTATUS:OOF` and
  `X-KYCALENDAR-OUT-OF-OFFICE:TRUE`. Free/busy shows it as busy.
- A new period moves it; `away:false` deletes it. It has no attendees and produces no iTIP.

**Automatic Tentative.** With `tentative` true, while `start <= now < end`, KyCalendar answers
each new invitation `PARTSTAT=TENTATIVE`, never anything else. The REPLY carries `COMMENT`:

- `comment_local` when the organizer is a KyCalendar user;
- `comment_remote` otherwise. KyPost sends it only when its remote reply is on for everyone
  and allowed by its administrator; otherwise it is empty and the answer has no comment.

This covers external invitations (`kypost.itip.inbound` answers `applied` instead of
`pending`) and internal ones (the outbox delivery). Updates to an invitation already in the
calendar are not answered again. Such an answer is the user's standing consent, so the copy
is written the way `kypost.itip.respond` writes it.

| Status | Answer | When |
|---|---|---|
| 404 | (plain) | KyPost unlinked, or scheduling off |
| 401 | `bad_signature` | Wrong signature or event type |
| 200 | `{"outcome":"duplicate"}` | A verified replay |
| 400 | `bad_body` | Malformed JSON, or a rule above broken |
| 404 | `no_user` | No active everyday account for `(sub, issuer)`; administrator identities included |
| 429 | `rate_limited` | 60 per user per hour, its own budget (not invitations') |
| 507 | `quota` | The calendar is full |
| 500 | `error` | Retry under the same event ID; KyCalendar forgets it |
| 200 | `{"outcome":"applied"\|"stale"}` | Done, or an older revision |

**KyPost's side.**
- KyPost sends the whole state on every save (the web, an agent, the inbox Turn off) while
  the pair is up, the user's KyCalendar switch is on and the account has a live SSO link. It sends `away:true` only when out-of-office is on with both dates and
  "Show me as away in KyCalendar" is ticked.
- A failure is reported to the user and resent on their next save. KyPost never retries in
  the background.
- When the administrator turns outside replies off, KyPost resends every calendar-tied state at
  once, without `comment_remote`.
- KyPost removes from any REPLY it mails for KyCalendar each `COMMENT` that is an outside-reply
  text KyCalendar may still hold. The only exception is while the reply is on and active, both
  calendar boxes are ticked, and the remote reply goes to everyone with the administrator's
  permission.
- KyPost records each remote comment, with the revision of the newest sync carrying it, before
  sending. An `applied` answer for revision R marks superseded only the texts last carried
  before R, so answers may arrive in any order. A superseded text stays refused for 25 hours.
  A REPLY KyCalendar built earlier may still be retried in that time, and a text changed,
  cleared or turned off while KyCalendar was unreachable is still refused.
- **KyCalendar's half of that bound:** its scheduling outbox abandons an unsent message 24 hours
  after building it (`outboxGiveUp`, `internal/davbackend/scheduling.go`). Raising that limit
  means raising KyPost's 25-hour retention (`config.OOORemoteRetention`) in the same change.
- Logs and audit carry IDs only, never the period or the texts.

### How KyCalendar implements it

`internal/api/kypost_ooo.go` validates and stores the state (`store.SetOutOfOffice`, migration
29 `kypost_out_of_office`), then `davbackend.Scheduler.ApplyOutOfOffice` writes or deletes
the event, re-reading the stored row so a newer revision wins. `Scheduler.autoTentative` hooks
`Inbound`'s new-invitation branch and the outbox's internal delivery; `itip.SetComment` and
`attendeeReply` carry the comment. Tests: `TestKyPostOutOfOffice*`,
`TestAttendeeReplyCarriesANewComment`.

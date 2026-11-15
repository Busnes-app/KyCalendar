# Suite integrations (KyCalendar's half)

KyCalendar's copy of the wave 1 and wave 2 wire contracts (owner decisions 2026-11-14; source:
`ky-redesign/integrations/suite-integrations-contracts.md` and `suite-integrations-wave2.md`).
This copy is the durable one for this repository: change it together with the peer's copy.
Sections implemented here: shared rules, B (KyCalendar → KyMessages meetings), D (people picker,
KyCalendar's part), E (the `/new` deep link, receiver), F (the free/busy strip) and, from wave 2,
H (chat from the event view, KyCalendar's part).

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
  reported back; a later PUT picks it up.
- A `guests` field that is present and non-empty: 422 `guests_unsupported`. `windows` is ignored.
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
- Event view: a Join button. Beside the switch, one line saying people outside the organisation
  cannot join yet.
- PUT and DELETE go through a durable outbox (like `scheduling_outbox`): on create, on title or
  attendee change, on switch off, on event delete or cancel. Recurring events use one room for
  the series.
- Only the organizer's copy drives the room. An invitee's copy shows Join and no switch.

### How KyCalendar implements it, and where it is more specific than the contract

- **Issuer.** `issuer` is the KyIdentity issuer URL the organizer signed in with: the account's
  `sso_issuer` binding (`kyidentity <issuer URL>`) without its kind, so KyMessages can match it
  against its own issuer.
- **DELETE body.** The contract names none; KyCalendar signs `{"sub","issuer"}` of the organizer,
  like every other body. KyMessages may ignore it.
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

### Deviation: the Element host is not checked

The pairing names the KyMessages console's URL, not Element's host (KyMessages' `ChatHost`), so
KyCalendar cannot compare the answer's host with anything. Instead it admits only Element's user
view in shape: absolute `https`, no credentials, path empty or `/`, no query, fragment exactly
`/user/@<localpart>:<server>` with a well-formed Matrix ID (`kymessages.ValidChatURL`). Anything
else is `bad_reply` and never reaches the browser. The browser checks `https://` again and opens
it with `noopener,noreferrer`.

### How KyCalendar implements it

- **Session routes.** `POST /api/suite/chat {"email"}` (everyday, CSRF) → 200 `{"url"}`,
  `Cache-Control: no-store`. It resolves the address with `suitedir.Resolve` as the caller; no
  match, or the caller themself, is 404 `no_target` without asking KyMessages. Refusals: 400
  `bad_body` (not one address, body over 1 KiB), 429 `rate_limited` (20 per user per minute),
  404 `chat_off` (KyMessages not linked, or the account did not sign in through the live
  KyIdentity binding: the video meetings condition), 404 `no_target`/`no_user` (passed on from
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

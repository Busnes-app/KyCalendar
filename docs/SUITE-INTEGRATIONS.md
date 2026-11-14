# Suite integrations (KyCalendar's half)

KyCalendar's copy of the wave 1 wire contracts (owner decisions 2026-11-14; source:
`ky-redesign/integrations/suite-integrations-contracts.md`). This copy is the durable one for
this repository: change it together with the peer's copy. Sections implemented here: shared
rules, B (KyCalendar → KyMessages meetings), D (people picker, KyCalendar's part) and E (the
`/new` deep link, receiver). F (free/busy strip) follows in its own change.

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
KyIdentity binding signed in reach KyIdentity. The query is never logged. Until ky-primitives
ships `People` (branch `feat/suitedir-people`) the list is always empty.

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

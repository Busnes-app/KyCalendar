# KyCalendar v1b invitations design

Status: draft 2026-10-17, awaiting Yoshi's review. Not scheduled for the early-November 2026
release (see "Release fit").

## Outcome

People invite each other to meetings and answer from any client: the web UI, iOS/macOS
Calendar, DAVx5 and Thunderbird. Internal attendees are delivered by the server; external ones
get standard iMIP email. Native clients just work because the server speaks CalDAV Scheduling
(RFC 6638) and iTIP (RFC 5546), and with scheduling off nothing a client sees changes.

## Decisions

| Decision | Chosen | Rejected and why |
|---|---|---|
| Scheduling model | RFC 6638 implicit scheduling: the server reads ATTENDEE/ORGANIZER changes on every write and sends REQUEST, CANCEL and REPLY itself | Client-side iMIP only: iOS never sends iTIP mail itself, so iOS users could not invite anyone |
| Switch | `KYCALENDAR_SCHEDULING` (default off). Off: no `calendar-auto-schedule`, no scheduling properties, no inbox or outbox, no mail; `TestCalDAVWireSurface` must still match its golden byte for byte | Always on: `calendar-auto-schedule` makes Thunderbird stop sending its own invitations, a change no operator asked for |
| Outbound mail | An SMTP relay the admin configures: host, port, required TLS (STARTTLS or implicit), credentials sealed like the sign-in secret, a fixed sender address on a domain the relay may send for | KyPost: it is an IMAP web client that sends through each user's own SMTP account, and has no API for another app to send through; it has zero references to KyCalendar |
| From header | `"<Organizer name> via KyCalendar" <sender>` with `Reply-To` and the iCalendar ORGANIZER set to the organizer's address | `From: <organizer>`: spoofs the organizer's domain through the relay and fails DMARC wherever the relay is not that domain's |
| Calendar user address | `mailto:<email>` of an active everyday user, from KyIdentity/SCIM or set by an admin, unique case-insensitively among active users. A user without one cannot organize or be resolved as internal. Administrators never have one | Username-based addresses: no external client can address them |
| Internal attendee | Delivered into their scheduling inbox and their default calendar with PARTSTAT=NEEDS-ACTION | Inbox only: iOS expects the event to appear |
| External replies | `POST /api/itip` called by KyPost as the organizer when they act on an RSVP card or receive a REPLY. Until KyPost does, an external attendee's answer is not recorded | An inbound SMTP receiver: a second mail system to secure; KyPost already owns the mailbox |
| COUNTER | Refused (`DECLINECOUNTER` is never sent; the client sees `SCHEDULE-STATUS:3.14`) | Supporting it: Google's COUNTER support is unverified (v1a spec) |

## Scheduling surface (scheduling on)

- OPTIONS `DAV` adds `calendar-auto-schedule`.
- The principal gains `calendar-user-address-set`, `schedule-inbox-URL` (`/dav/<id>/inbox/`) and
  `schedule-outbox-URL` (`/dav/<id>/outbox/`). The inbox lists, GETs and DELETEs scheduling
  messages; it is never writable by PUT. `schedule-default-calendar-URL` names the default
  calendar; `schedule-calendar-transp` is read from each calendar (opaque by default,
  transparent for subscribed calendars).
- Objects carry `Schedule-Tag`; `If-Schedule-Tag-Match` is honoured, so an attendee's PARTSTAT
  change does not lose an organizer update.
- Implicit scheduling runs inside `davbackend.Write`, so CalDAV, the web API and MCP share it:
  - Organizer write: REQUEST to added or changed attendees, CANCEL to removed ones, CANCEL to
    all on delete. SEQUENCE must increase on a significant change; the server bumps it when
    the client did not.
  - Attendee write: REPLY to the organizer when the attendee's own PARTSTAT changes; nothing
    else they change is sent.
  - `SCHEDULE-AGENT=CLIENT` or `NONE` on an attendee is respected: the server sends nothing
    for them. `SCHEDULE-STATUS` is written back on every attendee the server handled.
- Outbox `POST` with `VFREEBUSY` (`METHOD:REQUEST`) answers free/busy per attendee:
  - Internal attendees: their opaque personal calendars, through `calendar.BusyTimes`.
  - External attendees: `3.7` (invalid calendar user).
  - Recipients are capped at 50 per request and ranges at 62 days.

## Hostile input

Everything inbound is untrusted: a CalDAV PUT carrying METHOD or attendees, an `/api/itip`
body, and anything KyPost relays from the internet.

- Parsing at the boundary: one VCALENDAR, at most `MaxObjectSize`; METHOD is one of REQUEST,
  REPLY, CANCEL; one UID; at most 100 attendees per component and 1000 per object; every
  address is `mailto:` with an RFC 5322 address, no CR/LF, at most 254 bytes.
- REPLY: accepted only for an event the recipient organizes whose UID matches. It changes only
  the PARTSTAT (and DELEGATED-*) of the one ATTENDEE whose address equals the sender, as KyPost
  vouches for it (`From` aligned with a DMARC pass; KyPost passes the result, or the reply is
  refused). It never changes times, text or other attendees.
- REQUEST and CANCEL from an external organizer: land in the recipient's inbox as a pending
  invitation, and are never written into a calendar without the user accepting. An update or
  cancel applies only to an event whose ORGANIZER equals the sender and which was created from
  that organizer's invitation. An external sender can never modify an event they don't
  organize.
- No relay, no spam vector:
  - Mail goes only to attendees of an event an active user organizes.
  - The message is generated from a fixed template; no client-supplied header reaches it.
  - The subject is the event's SUMMARY with control characters removed, cut to 200 bytes.
- Rate limits:
  - Outbound, per organizer: 100 external recipients per hour and 500 per day; per instance,
    the admin's cap (default 2000 per day).
  - Inbound `/api/itip`: 60 per user per hour.
  - Over a limit the attendee gets `SCHEDULE-STATUS:5.1` and the organizer sees it.
- Logs and audit carry IDs only (event UID hash, recipient count), never addresses or text.

## Web UI

- The event form gains Attendees: people on this instance by name (suggestions from active
  everyday users), or any email address. Each attendee shows a free/busy strip for the chosen
  day (outbox lookup).
- Event detail shows each attendee's RSVP status (accepted, declined, tentative, awaiting).
- An invitation shows Accept, Tentative and Decline; the answer is a REPLY through the same
  write path.
- Invitations awaiting an answer appear in a sidebar list fed by the inbox.

## MCP

- A new scope `mcp:invite`. Without it every write tool refuses (422 `invite_scope_required`)
  three cases: an object with ATTENDEE, an object where the user is ORGANIZER with attendees
  (so editing a meeting's text cannot mail every attendee), and an RSVP.
- `list_events` gains `attendees` and `my_status` read-only fields under `mcp:read`.
- The skill (`internal/agentskill`) documents the scope; `TestAgentSkill` keeps it in step.

## Testing

1. Scheduling off:
   - `TestCalDAVWireSurface` unchanged.
   - The interop harness passes, with `calendar-auto-schedule` absent.
   - A PUT with ATTENDEE/ORGANIZER is stored byte-identical and sends nothing.
2. Pure iTIP functions, table-driven: attendee diff, SEQUENCE rules, REPLY merge, CANCEL per
   occurrence (RECURRENCE-ID), `SCHEDULE-AGENT`.
3. Hostile input: each rule above has a test that fails without it. Covered: a forged REPLY
   from a non-attendee, a REPLY changing DTSTART, a REQUEST for another organizer's UID, header
   injection in SUMMARY and addresses, and the rate limits.
4. Authorization matrix rows for inbox, outbox and `/api/itip`.
5. Interop gate, recorded under `docs/evidence/`, with real iOS, macOS Calendar, DAVx5 and
   Thunderbird:
   - internal invite, accept and decline;
   - change and cancel one occurrence;
   - outbox free/busy;
   - an external round trip with Gmail and Outlook.com through the relay.

## Release fit

Not in the early-November 2026 release. Implicit scheduling, the inbox and outbox, iMIP mail,
the hostile-input rules, web UI and MCP scope are about three weeks of work before the
real-device gate, which only an operator can run. External replies also need KyPost work. The
November release ships `GET /healthz` and `free-busy-query`; invitations follow behind
`KYCALENDAR_SCHEDULING`, default off.

## Open questions for Yoshi

- Should the relay settings live on an admin screen (like sign-in) or in environment variables
  first? The draft assumes an admin screen.
- Are the outbound limits right for your users?
- Should internal invitations auto-add to the attendee's calendar (Apple and Google behaviour,
  assumed here) or wait in the inbox?

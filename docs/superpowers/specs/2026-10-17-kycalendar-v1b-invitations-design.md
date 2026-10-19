# KyCalendar v1b invitations and meetings design

Status: revised 2026-10-18 after Yoshi's decisions (below); awaiting review. Not in the
early-November 2026 release (see "Release fit").

Companion spec: `KyMessage-Server/docs/superpowers/specs/2026-10-18-kymessages-meeting-rooms-design.md`
(video rooms and guests). KyPost keeps no committed design specs (`docs/superpowers/` is
gitignored there); its half is defined in "KyPost interface" below and becomes a contract doc in
`kypost-server/docs/` in the same change set as its code.

## Outcome

People set up meetings, add attendees and get a KyMessages video room, from any client: the web
UI, KyPost, iOS/macOS Calendar, DAVx5 and Thunderbird. Updates and cancellations made on a phone
reach every attendee. Attendees outside the instance get standard email invitations from the
organizer's own mailbox and can join the video room through a waiting room for the time of the
meeting.

## Owner decisions (2026-10-18)

1. A KyCalendar install implies KyPost: invitation mail goes through KyPost only. There is no
   SMTP relay in KyCalendar.
2. Meetings are set up with attendees and a KyMessages video room; updates and cancellations from
   mobile must be sent.
3. External attendees may join the room for the time of the meeting. They enter a waiting room
   and must be let in by someone in the room; leaving means being let in again.
4. Products authenticate to each other with backend server credentials, not per-user tokens.
5. A meeting gets a video room by default.

## Why the server schedules

Phone clients never send invitation mail themselves:

- DAVx5's manual says it does not send invitations and relies on the server.
- macOS Calendar sends invitations on CalDAV only when the server does RFC 6638 scheduling.
- iOS offers invitees only on servers that support them.
- Thunderbird stops sending its own invitations once the server advertises
  `calendar-auto-schedule`.

So decision 2 needs RFC 6638 implicit scheduling in KyCalendar. That also makes new meetings from
iOS and Android work; the iOS part is unproven until the device gate below.

## Decisions

| Decision | Chosen | Rejected and why |
|---|---|---|
| Scheduling model | RFC 6638 implicit scheduling in `davbackend.Write`: every write path (CalDAV, web API, KyPost, MCP) is diffed for attendee changes and the server sends REQUEST, CANCEL and REPLY | Client-side scheduling: phones send nothing, which fails decision 2 |
| Switch | `KYCALENDAR_SCHEDULING`, default off; with it off `TestCalDAVWireSurface` matches its golden byte for byte. It also stays off until the KyPost transport exists: turning it on stops Thunderbird sending to external attendees | Always on: changes what every client does |
| Mail transport | KyPost sends as the organizer from their own mailbox, building the MIME itself from the iTIP object KyCalendar hands it | A relay in KyCalendar: a second mail system, and a From address that is not the organizer's |
| Inbound mail | KyPost hands iTIP mail that arrived in a user's mailbox to KyCalendar, as that user | An inbound SMTP receiver in KyCalendar |
| Service credentials | Pairwise HMAC secrets signed with `ky-primitives/syncauth` (scheme v1, already used by KyIdentity's webhooks to KyPost and KyMessages), each scoped to a fixed route set and naming the acting user per request | Per-user tokens: every user would set them up by hand (decision 4). Bearer tokens: no replay protection |
| Video | KyMessages creates one Matrix room per meeting series; KyCalendar stores its link in `CONFERENCE` (RFC 7986) and in `LOCATION` when that is empty | A link in DESCRIPTION only: clients cannot show it as a join button |
| COUNTER | Refused (`SCHEDULE-STATUS:3.14` to the sender) | Google COUNTER support is unverified (v1a spec) |

## Calendar user addresses

- A user's calendar user address is `mailto:<email>` from KyIdentity/SCIM (or set by an admin
  for a local account), unique case-insensitively among active everyday users. Administrators
  have none.
- `calendar-user-address-set` lists it, so clients recognise the user as organizer.
- A write whose ORGANIZER is not the writer's address is stored, sends nothing, and gets
  `SCHEDULE-STATUS:5.3` on each attendee. Silently sending nothing is the classic DAVx5 failure:
  its account name is used as the organizer.
- KyPost refuses to send as an address the user cannot send from; KyCalendar shows that refusal
  as `5.3` too.

## Scheduling surface (flag on)

- OPTIONS `DAV` adds `calendar-auto-schedule`.
- The principal gains `calendar-user-address-set`, `schedule-inbox-URL` (`/dav/<id>/inbox/`),
  `schedule-outbox-URL` (`/dav/<id>/outbox/`) and `schedule-default-calendar-URL` (the default
  calendar).
- The inbox lists, GETs and DELETEs scheduling messages and is never writable by PUT.
- `schedule-calendar-transp` reads from each calendar: opaque by default, transparent for
  subscribed calendars.
- Objects carry `Schedule-Tag`, and `If-Schedule-Tag-Match` is honoured.
- On an organizer's write:
  - REQUEST goes to added or changed attendees and CANCEL to removed ones;
  - CANCEL goes to everyone on delete;
  - per-occurrence changes and cancels use RECURRENCE-ID;
  - SEQUENCE is bumped on a significant change when the client did not bump it.
- On an attendee's write, a REPLY goes to the organizer only when the attendee's own PARTSTAT
  changed.
- `SCHEDULE-AGENT=CLIENT` or `NONE` on an attendee is respected. `SCHEDULE-STATUS` is written
  back on every attendee the server handled.
- Internal attendees are delivered directly, with no mail: into their inbox, and into their
  default calendar with `PARTSTAT=NEEDS-ACTION`.
- External attendees get their copy through KyPost's outbound queue (below).
- Outbox `POST` with `VFREEBUSY` answers free/busy for internal attendees only, from their opaque
  personal calendars via `calendar.BusyTimes`. External attendees get `3.7`. At most 50
  recipients and 62 days per request.
- The same free/busy rule backs `GET /api/freebusy?users=…&start&end` for the web and KyPost
  attendee pickers, scoped to active everyday users on this instance.

## Meeting rooms

- **Default on:** a write by a local organizer that leaves at least one attendee on the event
  asks KyMessages for a room (`PUT /api/meetings/{id}`, idempotent).
- **What KyCalendar sends:** the organizer, the summary, the internal attendees, one opaque key
  per external attendee (never their name or address), and the occurrence windows.
  - A recurring series sends windows for the next 90 days; the daily loop extends them.
- **Rewriting the stored event:** the room's member link goes into `CONFERENCE;VALUE=URI;FEATURE=VIDEO;LABEL=KyMessages`
  and into `LOCATION` when that is empty. This is a server-side change to a scheduling object, so
  the ETag and Schedule-Tag move and clients refetch.
- **A client dropping the room:**
  - The link is re-added on the next write when a client drops `CONFERENCE`.
  - Clients are not trusted to signal an opt-out by dropping a property.
- **Opt-out:** per meeting, in the web UI or KyPost only, stored server-side (`meeting_rooms.disabled`).
- **External attendees' copies:** each external attendee's iMIP copy carries their personal guest
  link instead of the member link. Guest links never appear in the stored event, in CalDAV
  responses or in other attendees' copies.
- **Updates:**
  - A time change updates the room's windows.
  - Removing an attendee removes their access, revoking a guest's link.
  - Cancelling the series closes the room.
  - Cancelling an occurrence drops its window.
- **KyMessages unreachable:** the event is still saved and invitations still go out. The room
  request is queued and retried; once the room exists, an update goes out carrying the link.

## Backend credentials

- **Three pairings, each with its own secret, held by both sides:**
  - KyCalendar to KyPost, for outbound iTIP.
  - KyPost to KyCalendar, for inbound iTIP, RSVPs and free/busy.
  - KyCalendar to KyMessages, for rooms.
- **How a pairing is made:** the receiving product's admin screen (or the installer, through
  `ky.product-setup/1`) generates a pairing code; the calling product's admin claims it. Both
  store the secret sealed under a derived key. Unpairing on either side is a step-up, audited
  action.
- **Every request is signed with `syncauth.Sign`:**
  - The event type names the route (for example `kycalendar.itip.send`).
  - The event ID is the idempotency key: event UID hash, SEQUENCE, recipient and method. The
    receiver's replay guard turns a retry into a no-op rather than a second message.
  - The body carries the acting user's KyIdentity `sub` and issuer.
  - The receiver checks the signature with `syncauth` (timestamp window and replay guard) and
    accepts only its pairing's route set.
- **The receiver resolves the user by `sub` and issuer** (KyPost: `GetBySSOSubIssuer`) and
  refuses one that is not an active everyday user. Then it applies its own per-user rules.
- **Blast radius:** a stolen KyCalendar to KyPost secret can send invitation mail as any user who
  turned on calendar sending.
  - It cannot send arbitrary mail: KyPost builds the message itself and sends only to that
    object's attendees, as the user's own address or a verified send-as alias.
  - KyPost's existing per-user send quota and its audit bound it.
  - This is the cost of decision 4, recorded here.
- **Transport:** HTTPS only and no redirects. Private addresses are allowed only with an explicit
  opt-in, the same rule as KyRecovery.

## KyPost interface

What KyPost implements; its contract doc is written with the code.

- **Outbound, `POST /api/calendar/itip/send`** (KyCalendar's pairing): `{sub, issuer, from,
  recipients[], ics}`.
  - **Refused (409), so the attendee shows `5.3`:**
    - the user has not turned on "Let KyCalendar send invitations from this mailbox" (off by
      default);
    - `from` is neither the account's own address nor a verified send-as alias (the
      `resolveMailFrom` rule);
    - a recipient is not an ATTENDEE (or, for REPLY, the ORGANIZER) of the object.
  - **Accepted:**
    - KyPost builds a `text/calendar; method=<METHOD>` message from a fixed template, extending
      today's `calendarReply` path, which already sends REPLY;
    - the Subject is SUMMARY with control characters removed, cut to 200 bytes;
    - no caller-supplied header reaches the message;
    - it is charged to the user's send quota (`admitSend`/`chargeSend`; 429 with `Retry-After`
      when over, which KyCalendar retries);
    - a copy goes to the user's Sent folder.
  - METHOD is REQUEST, CANCEL or REPLY only.
- **Inbound:** for a message with a `text/calendar` part whose METHOD is REQUEST, REPLY or CANCEL
  (already detected as `calendarMethod`), KyPost verifies DKIM itself with go-msgauth, as it does
  for send-as proofs. Only a pass whose `d=` equals the From domain and whose signed headers
  include From is forwarded to KyCalendar's `/api/itip/inbound`, with `dkim_aligned: true`.
  - KyPost has no per-message DMARC or SPF result for mail it reads over IMAP, so DKIM alignment
    is the only sender proof available.
  - Everything else stays an ordinary message. Its RSVP card still works: the user's own Accept is
    the authorization (below).
- **RSVP cards:** the web, Android and iOS clients call KyPost, which calls KyCalendar's
  `/api/itip/respond` as the user. The REPLY to an external organizer then comes back through
  outbound. The direct `calendarReply` send becomes the fallback for a user whose KyPost is not
  paired with KyCalendar.

## Outbound queue

- Every external iTIP message is a row in `scheduling_outbox` in the same transaction as the
  write.
- A worker delivers rows to KyPost and retries 429/503 and network errors with backoff for up to
  24 hours, then marks the attendee `SCHEDULE-STATUS:5.1`.
- The attendee shows `1.0` (pending) until KyPost accepts the message and `1.2` (delivered) after.
- Replaying a row is safe: KyPost dedupes on the idempotency key.

## Inbound iTIP (from KyPost)

`POST /api/itip/inbound`: KyPost's pairing, as the user whose mailbox received the mail. The
body carries the raw iCalendar, the From address and `dkim_aligned`. KyCalendar refuses
anything without `dkim_aligned: true` and treats the rest as hostile.

- **Boundary checks:**
  - one VCALENDAR, at most `MaxObjectSize`;
  - METHOD is REQUEST, REPLY or CANCEL;
  - one UID;
  - at most 100 attendees per component and 1000 per object;
  - every address is `mailto:`, valid, at most 254 bytes, with no CR/LF.
- **REPLY:** only for an event the user organizes whose ATTENDEE list holds the sender. It
  changes only that attendee's PARTSTAT, and never times, text or other attendees.
  Delegation is not supported.
- **REQUEST from an external organizer:**
  - It lands in the inbox as a pending invitation and is written to a calendar only when the
    user accepts.
  - A later REQUEST or CANCEL applies only to an event that came from that same organizer's
    invitation.
  - An external sender can never change an event they don't organize.
- **Rate limit:** 60 inbound messages per user per hour; over it, 429.
- **Logs and audit** carry IDs only (UID hash, counts), never addresses or text.

## RSVP from KyPost

`POST /api/itip/respond` (KyPost's pairing, as the user) with `{uid, recurrence_id?, partstat}`,
plus the invitation's raw iCalendar when KyCalendar has not seen it. That happens when inbound
mail lacked an aligned DKIM signature.

- The user's PARTSTAT is written through `davbackend.Write`, and implicit scheduling sends the
  REPLY: internally, or through KyPost for an external organizer.
- Accepting an invitation KyCalendar has not seen writes it into the default calendar. The user's
  explicit Accept is the authorization, so an unverified sender can put an event on the calendar
  only when the user accepts it.
- Later updates and cancels from that sender still need `dkim_aligned`.

## Web UI

- **Event form:**
  - Attendees: people on this instance by name, from active everyday users, or any email address.
  - A free/busy strip per internal attendee.
  - A "Video meeting" switch, on by default.
- **Event detail:**
  - each attendee's RSVP and delivery status;
  - a Join button for the room;
  - Accept, Tentative and Decline on an invitation.
- **Sidebar:** invitations awaiting an answer, fed by the inbox.

## MCP

- New scope `mcp:invite`. Without it, every write tool refuses (422 `invite_scope_required`):
  - an object with ATTENDEE;
  - any edit of an event the user organizes that has attendees, since an edit there would mail
    everyone;
  - an RSVP.
- `list_events` gains read-only `attendees`, `my_status` and `meeting_url` under `mcp:read`.
- `internal/agentskill` documents the scope, and `TestAgentSkill` keeps it in step.

## Testing

1. **Flag off:**
   - `TestCalDAVWireSurface` is unchanged.
   - The interop harness passes with `calendar-auto-schedule` absent.
   - A PUT with attendees is stored byte-identical, and no outbox row, room request or mail
     follows.
2. **Pure iTIP functions, table-driven:**
   - the attendee diff;
   - SEQUENCE rules;
   - REPLY merge;
   - per-occurrence CANCEL;
   - `SCHEDULE-AGENT`;
   - the organizer-address mismatch;
   - the `CONFERENCE` rewrite and re-add.
3. **Hostile input:** every rule above has a test that fails without it. Covered:
   - a forged REPLY from a non-attendee;
   - a REPLY that moves DTSTART;
   - a REQUEST for another organizer's UID;
   - a body without `dkim_aligned`;
   - header injection in SUMMARY and addresses;
   - rate limits.
4. **Credentials:** a missing, wrong, unpaired or out-of-scope token, and an inactive or
   administrator `sub`. The authorization matrix gains rows for the inbox, the outbox,
   `/api/itip/*` and `/api/freebusy`.
5. **Outbox:** a retried row never produces a second message, and a crash between commit and
   delivery still delivers once.
6. **Interop gate** with real iOS, macOS Calendar, DAVx5 and Thunderbird, recorded under
   `docs/evidence/`:
   - an internal invite, accept and decline;
   - an invite from the phone;
   - moving and cancelling one occurrence on the phone and checking every attendee is told;
   - outbox free/busy;
   - a Gmail and an Outlook.com round trip through KyPost;
   - an external guest joining through the waiting room.

## Release fit

Not in the early-November 2026 release. Rough estimates:

| Product | Work | Time |
|---|---|---|
| KyCalendar | scheduling core, outbox, inbound, rooms client, web UI, MCP scope | ~3 weeks |
| KyPost | outbound send (extending `calendarReply`), DKIM-checked inbound forwarding, RSVP wiring, pairing | ~2 weeks |
| KyMessages | meeting rooms, guest waiting room, starting with the Element spike | ~2–3 weeks |

On top of that comes the real-device gate, which only an operator can run. The flag stays off
until the KyPost transport has landed.

## Open questions for Yoshi

- Guest window: the draft lets a guest knock from 10 minutes before the start until 15 minutes
  after the end of each occurrence. Is that "the time of the meeting"?
- Who may let a guest in: any internal member of the meeting room (the organizer and internal
  attendees) or only the organizer? The draft assumes any internal member.
- Internal invitations auto-add to the attendee's calendar as `NEEDS-ACTION` (Apple and Google
  behaviour). Keep?

# Room and equipment booking: design

Owner approval 2026-11-15 (suite features wave, worker `g-rooms`). In code everything is a
**resource**, because "room" already means a KyMessages meeting room here. The UI says Room or
Equipment.

An administrator defines resources. A member adds one to an event from the event dialog, and
KyCalendar answers for the resource in the same database transaction that stores the event:
`ACCEPTED`, `DECLINED` with a reason, or `NEEDS-ACTION` while an approver decides.

## Threat model

| Actor | Wants | Stopped by |
|---|---|---|
| Two members racing for one slot | Both get the room | The answer is decided inside the event's own write transaction, under the `calendar-total` lock that every calendar object write takes. The check reads the resource's busy rows, decides, and writes the busy rows and the event together. Two writes cannot interleave, so two events never hold overlapping busy rows on one resource. |
| Member outside the allowed groups | Book a restricted room | `may book` is checked in the transaction against the writer: everyone, or membership of one of the resource's groups, read in the same transaction. Otherwise `DECLINED` (`not_allowed`). The finder lists only resources the caller may book; `GET /api/resources/{id}/busy` is 404 for anyone else. |
| Member editing the bytes | Mark the room `ACCEPTED` themselves, by CalDAV PUT or a crafted web body | The stored PARTSTAT of a resource attendee is always the ledger's answer, rewritten on every write; what the client sent is ignored. |
| Snoop | Read other people's meeting titles through a room | A resource's calendar is its ledger of busy spans: start, end, pending or not. It never holds or returns a title, description, attendee or organizer. Approvers see the organizer's name and the times, never the title. |
| Approver of room A | Decide a request for room B | The decision route loads the booking and checks the caller is an approver of that booking's resource in the same transaction; otherwise 404. |
| Administrator identity | Book rooms or approve | Administrators are not everyday identities: resource booking, the finder and approvals are `requireEveryday` (403). Administrators cannot be approvers (409 `admin_member`). |
| Attendee copy | Book the room a second time when the invitation lands in an attendee's calendar | Only the organizer's object books: an object books when it has no ORGANIZER, its ORGANIZER is the writer, or it already holds a booking. A delivered copy (ORGANIZER someone else) never books. |
| Scheduler | Mail the room's address | The room's calendar user address is `mailto:resource-<id>@kycalendar.invalid` (RFC 2606 `.invalid`, never routable). The scheduler drops every outbox row addressed to a resource, so nothing is queued, mailed or sent to KyPost for one. |
| Flooder | Exhaust the server | At most 10 resources per event. A booking expands at most 1000 occurrences within the horizon (else `DECLINED` `too_many_occurrences`). Resource reads (finder, busy) are rate limited per user, 120 per minute. Busy queries span at most 62 days. |
| Injection | Header or parameter injection through names | Names 1-100 characters, location at most 200, tags at most 20 of 1-40 characters each; no control characters and no `"` (go-ical cannot encode one in a CN parameter). React renders text. |

## Data

Migrations 28 (definitions) and 30 (bookings) add five tables.

- `resources`: `id` (32 hex), `kind` (`room` or `equipment`), `name` (unique ignoring case),
  `location`, `capacity` (0 = not stated), `tags` (JSON list), `audience` (`everyone` or
  `groups`), `approval` (`auto` or `approvers`), `max_minutes`, `horizon_days`, `enabled`,
  `created_at`, `updated_at`.
- `resource_groups (resource_id, group_id)` and `resource_approvers (resource_id, user_id)`,
  cascading on both sides. Deleting a group drops it from every resource.
- `resource_bookings` (migration 30): one row per resource per event object: `resource_id`,
  `calendar_id`, `object_name` (cascading from `calendar_objects`, so deleting the event or its
  calendar releases the room), `booked_by`, `status` (`accepted`, `pending`, `declined`),
  `reason`, `detail`, `fingerprint`, `decided_by`, `open_after`, times.
- `resource_busy`: the busy spans up to the horizon of accepted and pending bookings (unix seconds), cascading
  from the booking.

A resource is never deleted. **Disabling** keeps every booking and busy row; new bookings and
changed times are declined (`disabled`). This is the resource's history.

## How an answer is decided

`store.settleResources` runs in `putObjectPlanned` after the scheduling planner, so CalDAV PUT,
web and MCP writes all go through it. Pure rules are in `internal/resource`.

1. Skip unless the unfolded bytes mention `@kycalendar.invalid` or the object already holds a
   booking. Skip an attendee copy (ORGANIZER set and not the writer, and no booking).
2. For each resource attendee (at most 10), compute a fingerprint of the event's timing
   properties (DTSTART, DTEND, DURATION, RRULE, RDATE, EXDATE, RECURRENCE-ID, STATUS of every
   VEVENT). An existing booking with the same fingerprint keeps an accepted, pending or
   approver-rejected answer; any other decline holds nothing and is decided again, so a freed
   room is booked at the next save. An accepted or pending booking also stands, whoever writes,
   when the series rule (the components without RECURRENCE-ID) is unchanged and every occurrence
   of the new object from now, expanded past the last override of either version, is an
   occurrence of the previous object (`resource.Within`): an attendee's answer to one occurrence
   adds an override and must not release the organizer's room, while a moved, detached, longer or
   restored occurrence is new time and is decided again. Editing the title,
   description or other attendees never re-decides the room, and never re-declines a past one.
3. Otherwise decide, in order: unknown resource (`unknown`) or disabled (`disabled`); the event
   is over (`past`); an object that could be read two ways, a VEVENT with both DTEND and
   DURATION or two components for one occurrence (`ambiguous`); a single event ending after now + horizon, or a series whose first
   occurrence is past it (`beyond_horizon`); more than 1000 occurrences within the horizon
   (`too_many_occurrences`); an occurrence, a master, an override or an RDATE period anywhere in the series
   longer than the maximum (`too_long`); the writer may
   not book it (`not_allowed`); an occurrence overlapping another booking (`busy`, with the
   conflicting dates). Cancelled occurrences (STATUS:CANCELLED) need no room. Passing all:
   `accepted`, or `pending` under the approver policy.
4. Write the answer: the booking row, the busy rows (accepted and pending hold the slot), and
   the resource attendee rewritten to `CUTYPE=ROOM` (room) or `RESOURCE` (equipment),
   `CN=<name>`, `ROLE=NON-PARTICIPANT` and `PARTSTAT`, without RSVP. Bookings of resources the
   event no longer names are deleted.

**Recurring events.** Every occurrence from now to the horizon is checked. If any conflicts, the
whole series is declined and the reason lists up to five conflicting dates. A series may run past
the horizon, and with no end (the web's repeats have none): once accepted it reserves every later
occurrence too. Its busy rows stop at the horizon, and the booking records where (`open_after`);
every later check and the finder expand such a series past that point, so a later booking that
lands on one of its occurrences is declined. This is how an accepted series sits in an Exchange
room calendar, and it keeps the guarantee that two events never hold the same slot. Declining
every open-ended series (Exchange's `EnforceSchedulingHorizon`) was considered and rejected: the
web editor cannot set an end date, so no repeating meeting could have a room. A resource on only
some occurrences is booked for the whole series.

**Moving and cancelling.** A move changes the fingerprint, so the room is decided again, with
the event's own busy rows excluded. A move into a busy slot is declined. Deleting the event
deletes the booking. STATUS:CANCELLED on the whole event releases the busy rows.

**Approval.** A pending booking holds its slot. An approver accepts or declines it; the store
sets the booking's status and rewrites the organizer's object through the same write path in
one transaction. Changing the event's times re-decides it, and it waits for approval again.

## Routes

Administrator (`requireAdmin`, audited `admin.resource_create`, `admin.resource_update`):

- `GET /api/admin/resources`: every resource, with groups, approvers and its address.
- `POST /api/admin/resources`, `PUT /api/admin/resources/{id}`: the whole definition, including
  `enabled`. Approvers must be active everyday users; groups must exist.

Everyday (`requireEveryday`; 120 reads per user per minute):

- `GET /api/resources?start=&end=&calendar=&uid=`: enabled resources the caller may book, with
  `free` for `[start, end)` (excluding that event's own booking). The finder filters capacity
  and tags on this list.
- `GET /api/resources/{id}/busy?start=&end=`: busy spans only, at most 62 days. 404 unless the
  caller may book it.
- `GET /api/resources/requests`: pending bookings of resources the caller approves: resource,
  organizer display name and the first and last busy span. No title.
- `POST /api/resources/requests/{id}` `{decision: "accept"|"decline"}`: 404 unless the caller
  approves that resource, 409 `not_pending`.
- Event create and update take `resources` (resource IDs; absent keeps, `[]` removes). An empty
  LOCATION takes the rooms' names and locations. Event views carry `resources` (id, name, kind,
  location, status, reason) and leave resources out of `attendees`.

`GET /api/settings` carries `resource_approver` so the web shows Room requests only to approvers.

## Defaults

- No resources exist until an administrator creates one: nothing changes for anyone until then.
- A new resource: everyone may book, auto-accept, at most 1440 minutes per occurrence, 180 days
  ahead. Bounds: 5-20160 minutes, 1-730 days, capacity 0-10000.
- Works with scheduling off. Booking is part of the event write, not of iTIP delivery, so a
  launch deployment with `KYCALENDAR_SCHEDULING=false` books rooms from the web and CalDAV.

## CalDAV

A CalDAV client that adds the room's address (shown in the resource editor) as an attendee gets
the same answer in the stored object; the server changed the bytes, so the PUT returns no ETag
and the client refetches. Reasons are not in the object (the client shows `DECLINED`); the web
shows them. The scheduling outbox free/busy does not answer for resources (`3.7`), and principal
search does not list them.

## Invitations through KyPost

KyPost's invitation mail is a fixed sentence and the iTIP attachment. That attachment is the
organizer's event, which carries LOCATION (filled with the room when it was empty) and the
`ATTENDEE;CUTYPE=ROOM;CN=<name>` line, so the recipient's calendar app shows the room. No KyPost
change.

## Not built

- Delegates who book on behalf of a room, per-occurrence room answers, buffers between
  bookings, and working-hours limits.
- Resource free/busy over CalDAV scheduling (outbox `3.7`) and principal search for rooms.
- Notifying approvers by mail (they see Room requests in the web), and notifying attendees when
  an approver decides (the organizer's event shows it; an organizer PARTSTAT change sends no iTIP).
- Deleting a resource (disable keeps history).
- Titles for approvers (owner question 1).

## Owner questions (defaults applied)

1. Approvers see the organizer's name and times, not the event title. Default: no title.
2. An accepted series with no end keeps its room for every later occurrence, as in Exchange.
   Default: keep (declining open-ended series would make repeating meetings unbookable from the
   web). Alternative: decline any series that runs past the horizon.
3. Who may book is checked against whoever writes the change, so an editor of a group calendar
   who may not book a room cannot move that room's booking. Default: the writer.

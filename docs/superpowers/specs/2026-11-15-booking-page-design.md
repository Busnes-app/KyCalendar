# Booking pages: design

Owner approval 2026-11-15 (suite features wave). Status: deliverable 1 (booking page) and 2
(meeting room) built on this design.

An outside person opens a member's public link, picks a free slot and books it. It is the
suite's first feature that lets someone with no account make the server write to a member's
calendar and send mail from a member's mailbox, so most of this document is about what that
stranger can and cannot do.

## Threat model

| Actor | Wants | Stopped by |
|---|---|---|
| Scraper | Learn the member's schedule, event titles, attendees, other members | The page answers free slots only (start/end instants), the member's display name and the type's title. No busy periods, no titles, no zone, no email. A slot list cannot be told apart from "the member works short days". Unknown, disabled or rotated slugs and a server with the feature off all answer the same 404. |
| Slug guesser | Find booking pages | 128 random bits (`crypto.RandomHex(16)`, 32 hex characters); the view route is rate limited per IP. |
| Calendar filler | Fill the member's calendar with junk so real people cannot book | A booking exists only after the booker clicks a link mailed to the address they gave and then presses Confirm. Unconfirmed holds block a slot for 15 minutes, then vanish. At most 2 open holds per email address, 5 hold requests per IP per 15 minutes, 20 per booking type per hour, and the member's daily cap. |
| Mail bomber | Use the member's mailbox to mail a victim | Each hold sends one message. The per-email cap of 2 open holds and the per-type limit bound what one victim receives. The hold message carries no visitor-written text: no name, no note, a fixed summary and description, so the member's mailbox cannot be made to deliver an attacker's words to a stranger. Name and note reach the invitation only after the booker proved the address. KyPost's own per-user send quota still applies. |
| Link-prefetching mail scanner | Confirm a booking the person never wanted | The emailed link opens a page that shows the slot and a Confirm button. Confirming is a POST with a CSRF token, so a GET by a scanner changes nothing. |
| Racing bookers | Both get the same slot | Holds, confirmations and reschedules run in one database transaction under the `calendar-total` lock every calendar object write takes. Inside it they reread the member's events and the open holds for the slot (with buffers) and the day (for the cap), and refuse when the slot is taken. Two transactions cannot interleave, so two visitors never hold or book overlapping slots. |
| Grid prober | Book 09:07 or a slot outside the windows | A hold must name exactly one start the server's own slot generator produced at that moment. Anything else is 409 `slot_unavailable`. |
| Cross-site page | Make a visitor's browser book or cancel | Every public POST needs the pre-session CSRF token (`ValidatePreSessionCSRF`, the same binding sign-in uses), handed out by the page's own GET. Cross-site requests (`Sec-Fetch-Site`) are refused too. |
| Link leak via Referer or logs | Steal a confirm or manage token | Tokens ride in the URL fragment (`/book/c#<id>.<token>`, `/book/m#<id>.<token>`), which browsers never send to any server. The page POSTs them in the body. Only SHA-256 of each 256-bit token is stored and compared in constant time. |
| Injection via name or note | Script in the UI, header or property injection in ICS | Name: 1-100 characters, no control, format or separator characters, no `"` (go-ical cannot encode one in a parameter). Note: at most 1000 characters, plain text, newline and tab only. go-ical escapes TEXT values. React renders text, never HTML. KyPost builds the mail from a fixed template, and no caller header reaches it. |
| Administrator identity | Publish a booking page as an admin | Booking types are everyday-only (`requireEveryday`). Administrators get 403, like DAV and app passwords. |
| Other member | Edit someone else's booking type | Every member route loads the type by ID and owner. Another owner's type is 404. |
| Open redirect | Bounce a visitor elsewhere | No route redirects. Links in mail are built from `KY_APP_URL` only, never from the request. |

Out of scope: a determined attacker with many IP addresses can still hold slots for 15 minutes at
a time within the per-type limit. The member can rotate the link or turn the type off. CAPTCHA was
considered and left out: the mailed confirmation already proves a reachable mailbox, and the
existing PoW CAPTCHA is tuned for sign-in.

## Preconditions and the switch

The administrator switch is **Allow booking pages** (Server settings), stored as setting
`booking_pages_allowed`. It is off by default. Turning it on needs step-up and is audited
`admin.booking_pages` `from= to=`. It is refused with 409 `kypost_unpaired` while the KyPost link
is down, and with 409 `scheduling_off` while scheduling is off. The screen shows the reason and
the switch stays disabled.

The feature is **available** only while all three hold: the switch is on, scheduling is on
(`KYCALENDAR_SCHEDULING`), and KyPost is linked. Unavailable, every public route answers 404 and
the member's Settings tab says why (`off`, `kypost_unpaired`, `scheduling_off`). A member also
needs an email of their own and a KyIdentity binding (`sub`, `issuer`), since KyPost mails only
for those. Otherwise the tab says so (`no_address`, `no_kypost_account`) and a type cannot be
enabled.

The member's KyPost switch ("Let KyCalendar send invitations from this mailbox") is on KyPost's
side and is not visible here. When it is off, KyPost answers 409 `sending_off` to the hold mail,
and the visitor sees "This booking page cannot send mail right now". The hold is dropped.

## Data

Migration 27 adds two tables.

`booking_types`, one row per type:

- `id`, `user_id` (cascade on user delete)
- `slug`: unique, 32 hex characters
- `enabled`
- `title`: 1-100 characters, `cleanName`
- `duration_min`: 5-480
- `zone`: IANA name
- `windows`: JSON `[{"day":0-6,"start":"HH:MM","end":"HH:MM"}]`, at most 21, start before end
- `buffer_before_min`, `buffer_after_min`: 0-240
- `notice_min`: default 240, 0-43200
- `horizon_days`: default 30, 1-60
- `daily_cap`: 0 means no cap, at most 100
- `calendar_id`: a personal, non-subscribed event calendar the member owns
- `meeting_room` (deliverable 2)
- `created_at`, `updated_at`

There are at most 20 types per member.

The slug is stored as is. It is a public address by design (members paste it on websites), and
someone who can read the database can do no more with it than any visitor.

`bookings`, one row per hold or booking:

- `id`, `type_id` (cascade), `user_id`
- `start_at`, `end_at`: unix seconds
- `status`: `held`, `confirmed` or `cancelled`
- `expires_at`: holds only
- `email`, `email_hash` (SHA-256 of the lowercased address, for the per-email count), `name`, `note`
- `confirm_hash`, `manage_hash`: SHA-256 of the tokens
- `calendar_id`, `object_name`, `uid`: the event, once confirmed

Retention: a hold past `expires_at` is inactive at once and deleted, with its name, email and
note, by the 5-second scheduling loop's hourly prune. A cancelled booking loses name, email and
note at once. Confirmed rows are deleted 30 days after the booking ends, after which the manage
link answers 404. Deleting a type deletes its rows. Its events stay as ordinary events, and their
manage links stop working.

The confirmed booking **is an ordinary event** in the type's calendar, written as the member
through the one write path (quota, validation, change log, scheduler). Its object carries
`X-KYCALENDAR-BOOKING-TYPE:<type id>`, which the daily cap counts. It is the member's event: they
move, edit or cancel it as usual, and the scheduler tells the booker.

## Slots

`internal/booking` is pure:

- **Windows:** each day in the type's zone from today to the horizon expands into wall-clock
  windows, built with `time.Date` in that zone.
- **Grid:** candidate starts step by the duration from each window's start, so 09:00, 09:30 for 30
  minutes. A slot must end by the window's end.
- **DST:** steps are absolute durations from the window start, so a window over a transition
  gives the slots that fit in its real length. A window starting in a skipped hour starts at Go's
  normalised time.
- **Free:** a slot is free when no busy period, from the member's existing free/busy
  (`davbackend.PersonalBusy`: personal, non-subscribed calendars; tentative counts as busy) or
  another open hold, overlaps `[start - buffer_before, end + buffer_after)`.
- **Notice and horizon:** the slot starts at or after `now + notice` and before `now + horizon
  days`.
- **Daily cap:** the member's local day already holds `daily_cap` bookings of this type plus open
  holds.

The page shows the instants and the browser renders them in the visitor's zone (`Intl`), naming
it.

## Flows

1. **View:** `GET /api/book/{slug}` returns `{name, title, duration_min, slots:[{start,end}]}` and
   sets the pre-session cookie and CSRF token.
2. **Hold:** `POST /api/book/{slug}/hold` `{start, name, email, note}`:
   - Checks run in order: rate limits, input, the start on the current grid, then the
     transactional hold.
   - The server then mails the hold through KyPost (`kypost.Link.Send`, event ID
     `booking-hold-<id>`, METHOD:REQUEST, `STATUS:TENTATIVE`, `SEQUENCE:0`, the booking's UID).
     The mail holds the fixed text and the confirm link, valid for 15 minutes.
   - If KyPost refuses, the hold is deleted and the visitor is told.
   - The answer is 202 `{expires_at}`.
3. **Confirm page:** `/book/c#<id>.<token>` POSTs `/api/book/confirm/view` and shows the slot, the
   title and a Confirm button.
4. **Confirm:** `POST /api/book/confirm` `{id, token}`. In one transaction, it checks the hold is
   still held and unexpired and the slot is still free, then writes the event and marks the row
   confirmed.
   - The event has `SEQUENCE:1`, so it supersedes the tentative message, with ATTENDEE the booker
     (CN = name) and ORGANIZER the member.
   - The description reads: the booker's name, their note, then
     `Cancel or reschedule: <app>/book/m#<id>.<token>`.
   - The scheduler mails the booker a normal invitation through KyPost. A second confirm is 409
     `already_confirmed`, and an expired one 410 `expired`.
5. **Manage:** `/book/m#<id>.<token>` POSTs `/api/book/manage` and gets the booking and fresh
   slots.
   - `POST /api/book/manage/cancel` deletes the event through the scheduled delete path (the
     booker gets a CANCEL) and marks the booking cancelled.
   - `POST /api/book/manage/reschedule` `{start}` moves the event through the scheduled write path,
     with the same grid rule and the same transactional check, ignoring the booking's own event.
   - If the member already deleted the event, the booking shows as cancelled.

## Routes

| Method | Path | Guard | Notes |
|---|---|---|---|
| GET | `/api/admin/booking` | admin | `{allowed, available, reason?}` |
| PUT | `/api/admin/booking` | admin + step-up | `{allowed}`; 409 `kypost_unpaired`, `scheduling_off` when turning on; audited |
| GET | `/api/booking-types` | everyday | `{available, reason?, base_url, calendars, types}` |
| POST | `/api/booking-types` | everyday | create; 400 invalid; 409 `limit` at 20; 409 `unavailable` when enabling while unavailable |
| PUT | `/api/booking-types/{id}` | everyday, owner | full replace of the settings |
| POST | `/api/booking-types/{id}/rotate` | everyday, owner | new slug, the old one dead at once |
| DELETE | `/api/booking-types/{id}` | everyday, owner | 204 |
| GET | `/api/book/{slug}` | public, 60/min per IP, 600/min per type | 404 when unavailable or unknown |
| POST | `/api/book/{slug}/hold` | public + pre-session CSRF; 5/15 min per IP, 20/h per type | 202; 409 `slot_unavailable`, `too_many_holds`; 503 `mail_failed` |
| POST | `/api/book/confirm/view` | public + CSRF, 30/15 min per IP | the held slot |
| POST | `/api/book/confirm` | public + CSRF, 30/15 min per IP | 200; 404 bad token; 409 `slot_unavailable`, `already_confirmed`; 410 `expired` |
| POST | `/api/book/manage` | public + CSRF, 30/15 min per IP | booking and slots |
| POST | `/api/book/manage/cancel` | public + CSRF, 30/15 min per IP | 200 |
| POST | `/api/book/manage/reschedule` | public + CSRF, 30/15 min per IP | 200; 409 `slot_unavailable` |

The rate limits are the server's in-memory windows (`allowPerIP`, `allowAccountAttempt`), per
process, like the existing login and invite limits. Logs carry booking and type IDs only, never a
name, address, note or token.

## Meeting room (deliverable 2)

Per type, **Add a KyMessages meeting room**, off by default, offered only while video meetings are
available to the member (`meetingsFor`).

- A confirmed booking's event is written with the meeting switch on (`store.MeetingSwitch{Open:
  true}`), so the existing worker opens the room with the wave 1 PUT. The booker is an outside
  attendee, so the wave 2 guest path (`GuestLink`) adds a "Join as a guest:" line to the
  booker's copy only.
- When KyMessages answers `guests_off` or `guests_unsupported`, no guest line is added and the
  booker's invitation says nothing about joining. The member's own `Join:` link never goes to
  outside attendees.
- A KyMessages that is down does not hold up the booking. The worker retries, and the invitation
  waits for the guest link as for any meeting (`guest_pending`).

## Chosen not to build

- A visitor-side time zone picker: the browser's zone is used and named.
- Per-type questions or custom fields: name, email and a note only.
- A CAPTCHA, payment, group bookings, round-robin across members, or availability overrides for
  single dates.
- Sending a CANCEL when an unconfirmed hold expires. It would mail an address that never proved
  it wanted mail a second time. The tentative entry some mail clients add stays tentative.
- Booking against group calendars' busy time. Free/busy is the existing personal one.
- A separate mail channel. Everything goes through the KyPost iTIP path, so KyPost-Server is
  unchanged.
- HMAC-signed links. Manage and confirm links are 256-bit random capabilities whose SHA-256 is
  stored. That gives a signature's unforgeability, can be revoked per booking, and needs no key
  rotation story.

## Owner questions (defaults applied)

1. Holds and confirmations send mail from the member's own mailbox, through KyPost and charged to
   their quota. Default: yes, as the brief requires. A dedicated sender address would need
   KyPost work.
2. Launch deployments pin `KYCALENDAR_SCHEDULING=false` until the device gate passes, and booking
   pages need scheduling. Default: booking pages stay unavailable there, and the admin screen
   says why.
3. The slot grid steps by the duration. Default: no separate "start every N minutes" setting.

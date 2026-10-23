# Scheduling device gate (November 2026)

Status: **PENDING (Yoshi)**. Target: finished by about Tue 10 Nov 2026.

Decision (Yoshi, 2026-10-23): `KYCALENDAR_SCHEDULING` is turned on for the Ky Suite launch on
Tue 17 Nov 2026 **only if this test passes**. This is the interop gate from "Testing" item 6 of
`docs/superpowers/specs/2026-10-17-kycalendar-v1b-invitations-design.md`. The automated tests
(`TestCalDAVWireSurface`, `scripts/interop/run.sh`) cover the server. This checklist covers the
real apps on real devices, which only a person can run.

You do not need to know CalDAV. Every step says where to tap and what you should see. Fill in
the result tables as you go and commit this file with the results.

## Pass rule

- Every row in every result table is **Pass**, or **N/A** where the table says N/A is allowed.
- Any **Fail** keeps the switch off for launch, unless you decide in writing that the failure
  is acceptable. If you do, write that decision and its reason under "Decisions" at the end.
- A failure that comes from Gmail or Outlook.com themselves (spam folder, no DKIM signature)
  still counts. Write down what you saw so it can be traced.

## Out of scope

- **Video rooms and the guest waiting room** (the spec's last interop item). KyCalendar has no
  KyMessages room client yet: no `PUT /api/meetings/{id}` call, no `CONFERENCE` link. KyMessages
  has the design (`KyMessage-Server/docs/superpowers/specs/2026-10-18-kymessages-meeting-rooms-design.md`)
  and the guest spikes, but no meeting-room or waiting-room code (checked 2026-10-23 on both
  `main` branches). Invitations go out without a video link. Test the waiting room in its own gate
  once rooms exist.
- **Agents (MCP).** An agent still cannot change a meeting; that refusal is covered by tests.

## Known gap: answering an outside invitation from KyPost

When someone at Gmail or Outlook.com invites a KyCalendar user, KyPost forwards the invitation
to KyCalendar, which holds it as *pending*. It is written to the calendar only when the user
answers through KyPost's `POST /api/calendar/rsvp`. On 2026-10-23 no KyPost client calls that
route: the web app, Android and iOS still answer with a plain reply mail (`calendarReply`). So
the organizer gets the answer but **the event does not appear in KyCalendar**. Test 9 records
what happens. If it fails only for this reason, decide whether that blocks the launch (see
"Decisions").

## People and accounts you need

| Who | What it is | Needs |
|---|---|---|
| **Alice** | The person on the devices under test (you, or a test account) | KyCalendar everyday user who signs in through **KyIdentity** (not a local account), with an email address. A KyPost account for that same address that uses an **outside mail server** (IMAP/SMTP). KyPost cannot send invitations yet from a mailbox KyPost hosts itself. |
| **Bob** | A second person on this KyCalendar | Everyday user with an email address. You watch Bob's side in the KyCalendar web app, in a private browser window. |
| **Gmail** | An outside guest | Any Gmail account you can open on gmail.com. |
| **Outlook** | An outside guest | Any Outlook.com or Hotmail account you can open on outlook.live.com. |

Why these rules:

- Without an email address, KyCalendar does not schedule anything for that person.
- A local (non-KyIdentity) account cannot send to outside people: KyPost finds the user by
  their KyIdentity login.
- The address KyCalendar sends from is Alice's KyCalendar email. KyPost refuses it unless it is
  the KyPost account's own address or a verified send-as address.

## Before you start (once, about an hour)

Write the versions here:

| Item | Value |
|---|---|
| KyCalendar version (web app → Settings → Version) | |
| KyPost version | |
| KyCalendar address (`https://…`) | |
| KyPost address (`https://…`) | |
| Date started | |

### 1. Pair KyCalendar and KyPost

Both sides share one secret. Make it once and give the same file to both servers.

1. On the KyCalendar host, in the KyCalendar folder:

   ```sh
   (umask 077; openssl rand -hex 32 > data/kypost.secret)
   sudo chown 10001:10001 data/kypost.secret
   ```

2. Copy that file to the KyPost host, mode 600, where KyPost's container can read it, and
   readable by the user KyPost runs as.
3. KyCalendar `.env` (leave `KYCALENDAR_SCHEDULING` alone for now):

   ```sh
   KYCALENDAR_KYPOST_URL=https://<kypost host>
   KYCALENDAR_KYPOST_SECRET_FILE=/app/data/kypost.secret
   # Only if KyPost is on your own network (a 10.x, 192.168.x or similar address):
   # KYCALENDAR_KYPOST_ALLOW_PRIVATE=true
   ```

4. KyPost `.env`:

   ```sh
   KYPOST_KYCALENDAR_URL=https://<kycalendar host>
   KYPOST_KYCALENDAR_SECRET_FILE=<path to the secret, as KyPost's container sees it>
   # Only if KyCalendar is on your own network:
   # KYPOST_KYCALENDAR_ALLOW_PRIVATE=true
   ```

5. Restart both: `docker compose up -d` in each folder. Both servers refuse to start when a URL
   or the secret is wrong. If one does not come up, `docker compose logs --tail 50` says why.

### 2. Turn on KyPost's switch for Alice

Sign in to KyPost as Alice, open **Settings → Mail → KyCalendar** and turn on **Let KyCalendar
send invitations from this mailbox**. This one switch covers both directions: sending
invitations and passing incoming ones to KyCalendar. It is off for every user by default.

### 3. Turn the scheduling switch on

See "The switch" below. Do that now, then come back.

### 4. Check the server tells clients it schedules

Create an app password for Alice (KyCalendar web app → **Phones & apps** → name it "gate check"),
then on any computer:

```sh
curl -s -i -X OPTIONS -u 'alice:<app password>' https://<kycalendar host>/dav/ | grep -i '^dav:'
```

Pass: the `DAV:` line contains `calendar-auto-schedule`. If it doesn't, the switch is not on, or
Alice has no email address. Revoke the "gate check" password afterwards.

### 5. Add Alice's account to every device

Each device gets its own app password (KyCalendar web app as Alice → **Phones & apps** → one per
device). Steps per app are in "Client steps" below. Remove any old KyCalendar account from the
device first, so it reads the server fresh.

## The switch

### Turn it on

1. In the KyCalendar folder, edit `.env` and add or change:

   ```sh
   KYCALENDAR_SCHEDULING=true
   ```

2. `docker compose up -d` (this recreates the container; a few seconds of downtime).
3. Check: `docker compose exec app printenv KYCALENDAR_SCHEDULING` prints `true`, and step 4
   above passes.
4. On every device, refresh the account (or remove and re-add it) so the app sees the change.

What changes for people:

- Calendar apps hand invitations to the server instead of sending them themselves.
- People on this KyCalendar get the invitation straight into their default calendar, marked as
  not yet answered.
- People outside get an email from the organizer's own address, sent by KyPost.
- Apps can show other people's busy times on this KyCalendar.

### Roll it back

1. Edit `.env`: `KYCALENDAR_SCHEDULING=false` (or delete the line; off is the default).
2. `docker compose up -d`.
3. On every device, refresh or re-add the account. iPhone and Mac keep what they learned until
   they re-read the account; remove and re-add it if they still behave as before.

What rollback does and does not do:

- Events already saved, with their attendees, stay as they are. Nothing is deleted.
- From then on, nothing is sent from the server. iPhone, Mac and Android send no invitations of
  their own, so **attendees added on those devices get nothing** while the switch is off.
  Thunderbird goes back to mailing invitations itself (below).
- Messages still waiting to be sent stay queued. If you turn the switch on again within 24
  hours, they go out then, late. Messages older than 24 hours are dropped.
- **Lighter rollback, for outside mail only:** turn off Alice's switch in KyPost (Settings →
  Mail → KyCalendar), or remove the `KYPOST_KYCALENDAR_*` lines from KyPost's `.env` and restart
  it. People on this instance keep getting invitations; outside people get nothing until it is
  back on.

### What Thunderbird does differently once it is on

Thunderbird is the one app that sends invitation mail itself when the server doesn't.

- **Off:** Thunderbird emails the invitation from the identity set on the calendar.
- **On:** the server says `calendar-auto-schedule`, so Thunderbird stops mailing anything. The
  server sends instead: through KyPost for outside people, directly for people on this instance.
  - If KyPost is not paired, Alice's KyPost switch is off, or Alice is a local account,
    **outside attendees get nothing**. That is why the switch must not go on before KyPost works
    (step 1 above).
- **On:** Thunderbird's **Invite Attendees** window shows busy times for people on this instance.
  People outside show as unknown.
- **On:** Thunderbird must know which address is Alice's. Right-click the calendar →
  **Properties** → **Email**: pick Alice's address. Otherwise Thunderbird does not see Alice as
  the organizer.
- **Rolled back:** restart Thunderbird so it re-reads the server. From then on it mails
  invitations itself again.

## Where to look

- **Bob's side:** KyCalendar web app signed in as Bob, in a private window. An event's detail
  shows each attendee and their answer (accepted, declined, tentative, not answered).
- **Alice's side:** the device under test, and the KyCalendar web app as Alice to double-check.
- **Gmail / Outlook:** the web mail, including the spam folder.
- **Timing:** people on this instance get changes within about 10 seconds, plus however long the
  device takes to sync. Mail to and from outside depends on KyPost and the mail providers: allow
  5 minutes before calling it a Fail.
- **Make the device sync now:** iPhone and Mac: open Calendar → Calendars → pull down to
  refresh. DAVx5: open DAVx5 → the account → **Synchronize now**. Thunderbird: right-click the
  calendar → **Synchronize**.

### When something does not arrive

On the KyCalendar host, in the KyCalendar folder:

```sh
docker compose logs --since 30m app | grep -i -E 'sched|kypost'
sqlite3 -readonly data/kycalendar.db \
  "select created_at, method, recipient, status, attempts, last_error
   from scheduling_outbox order by created_at desc limit 20;"
```

(`sudo dnf install sqlite` if `sqlite3` is missing. This is for the default SQLite install.)

`status` is `pending`, `delivered` or `failed`. What `last_error` means:

| `last_error` | Meaning | Fix |
|---|---|---|
| `no_transport` | KyPost is not paired on KyCalendar's side | Step 1 |
| `no_kypost_account` | Alice signs in locally, not through KyIdentity | Use a KyIdentity account |
| `kypost_sending_off` | Alice's KyPost switch is off | Step 2 |
| `kypost_native_unsupported` | Alice's KyPost mailbox is hosted by KyPost itself | Use a mailbox on an outside mail server |
| `kypost_bad_from` | Alice's KyCalendar email isn't Alice's KyPost address or a verified send-as address | Make them match |
| `kypost_no_mailbox` | Alice's KyPost account has no outgoing mail server | Set it up in KyPost |
| `kypost_bad_recipient` | The organizer on the event isn't Alice's address | Usually DAVx5's account name; see DAVx5 below |
| `kypost_no_invitation` | A reply to an outside invitation KyPost never saw arrive with a valid signature | See test 9 |
| `uid_taken` | Another person already has an event with this ID | Delete the test event and start again |
| `status_<n>` | KyPost answered something unexpected | Check KyPost's logs |

On KyPost: `docker compose logs --since 30m | grep -i kycalendar`.

## The tests

Run tests 1 to 8 once on each of the four apps (iPhone, Mac, Android, Thunderbird), one app at a
time. Test 9 runs once and is checked on all four. Put the app's name in each event title (for
example "Gate iOS 3"), so you know which run each mail came from.

**Test 1: accept an invitation from Bob.**

1. As Bob (web app), create an event tomorrow at 10:00 called "Gate <app> 1" and add Alice's
   email as an attendee. Save.
2. On the device, sync. The event appears in Alice's calendar, marked as an invitation
   waiting for an answer.
3. On the device, accept it (see "Client steps" for where the button is).
4. In Bob's web app, open the event.

Pass: the event reached the device without anyone mailing it, and after about a minute Bob sees
Alice as **accepted**.

**Test 2: decline an invitation from Bob.**

Same as test 1 with "Gate <app> 2", but decline on the device. Pass: Bob sees Alice as
**declined**. The device may hide the declined event or show it struck through; either is fine.

**Test 3: invite Bob from the device.**

1. On the device, create "Gate <app> 3", tomorrow at 14:00, in Alice's KyCalendar calendar. Add
   Bob's email as an attendee. Save.
2. As Bob (web app), look at tomorrow.
3. As Bob, accept it.
4. On the device, sync and open the event.

Pass: the event shows up in Bob's calendar as not yet answered. After Bob accepts, the device
shows Bob as accepted. Nobody received an email for this: both people are on this instance.

**Test 4: see Bob's busy time (free/busy).**

1. As Bob (web app), create an event with no attendees, tomorrow 15:00-16:00, called
   "Bob busy".
2. On the device, start a new event tomorrow at 15:00 and add Bob as an attendee. Look at the
   availability view (see "Client steps"). Then cancel the new event without saving.

Pass: the device shows Bob as busy 15:00-16:00. It shows only that Bob is busy, not the title.
**N/A is allowed on Android**: Android calendar apps have no availability view.

**Test 5: invite Gmail from the device (round trip).**

1. On the device, create "Gate <app> 5", the day after tomorrow at 11:00. Add the Gmail
   address as an attendee. Save.
2. In Gmail, find the invitation. Check who it is **from**.
3. In Gmail, click **Yes** on the invitation.
4. On the device, sync and open the event.

Pass:

- the mail arrives in Gmail's inbox (not spam), from Alice's own address, with Gmail's
  Yes / Maybe / No buttons;
- after Yes, within 5 minutes, the device shows the Gmail attendee as accepted;
- the mail is in the Sent folder of Alice's mailbox.

**Test 6: invite Outlook.com from the device (round trip).**

Same as test 5 with "Gate <app> 6" and the Outlook.com address; accept in Outlook.com.

If the invitation arrives but the acceptance never comes back, open Outlook's reply in Alice's
mailbox and look at its headers for `DKIM-Signature: … d=`. KyPost passes a reply on only when
`d=` equals the sender's domain (`outlook.com` or `hotmail.com`). Write down what you saw.

**Test 7: move and cancel one occurrence from the device.**

1. On the device, create "Gate <app> 7", **repeating weekly**, starting next Monday at 09:00.
   Add Bob, the Gmail address and the Outlook.com address. Save.
2. Check that everyone has it: Bob (web app), Gmail and Outlook.com each show a weekly series.
   Accept it in each, to make the next steps easy to read.
3. On the device, open the **second** Monday's occurrence and move it to 11:00. Save it for
   **this event only**.
4. On the device, open the **third** Monday's occurrence and delete it, **this event only**.
5. Check each attendee.

Pass, for Bob, Gmail and Outlook.com alike:

- the second Monday is at 11:00;
- the third Monday is gone or marked cancelled;
- every other Monday is still at 09:00, and the series was not deleted.

Gmail and Outlook.com each get one update mail and one cancellation mail from Alice.

**Test 8: clean up (and check cancelling the whole series).**

On the device, delete "Gate <app> 7", **all events**. Pass: Bob, Gmail and Outlook.com each see
the whole series cancelled or removed. Then delete the other "Gate <app>" events from the device.
Deleting one you organized tells its attendees, which is fine.

**Test 9 (once): Gmail and Outlook.com invite Alice.**

1. In Gmail, create "Gate inbound Gmail", in two days at 16:00, and invite Alice's address.
2. In Outlook.com, create "Gate inbound Outlook", in two days at 17:00, and invite Alice.
3. Answer both, in whichever mail app Alice uses. Write down which app and which button. In the
   KyPost app it is the invitation card's Accept. In iPhone Mail or Thunderbird mail it may be
   **Add to Calendar** or **Accept**.
4. Check Gmail and Outlook.com for Alice's answer, and every device for the events.

Pass: Gmail and Outlook.com show Alice as accepted, **and** both events are in Alice's KyCalendar
on every device. Read "Known gap" above first: from the KyPost app the first half is expected to
pass and the second to fail.

## Client steps

Use the newest app version on each device and write it in that app's result table. Menu names
move between OS versions; if a step is named differently, write the real name in Notes.

### iPhone (iOS Calendar)

- **Add the account:** Settings → Apps → Calendar → Calendar Accounts → Add Account → Other →
  Add CalDAV Account. Server: `<kycalendar host>` (no `https://`, no path). User Name: Alice's
  KyCalendar user name. Password: the app password. Description: "KyCalendar". Tap Next, then
  Save. On older iOS: Settings → Calendar → Accounts.
- **Make KyCalendar the default:** Settings → Apps → Calendar → Default Calendar → pick the
  KyCalendar calendar. New events then go to the server, not to iCloud.
- **Accept or decline:** open the event; **Accept / Maybe / Decline** are at the bottom. An
  invitation may also show under **Inbox** at the bottom right of Calendar.
- **Add attendees:** new event (+) → **Invitees** → type the email → Done.
- **Availability (test 4):** after adding Bob, look for **Availability** under the invitees, or
  a busy mark next to Bob's name.
- **Repeat weekly:** in the new event, **Repeat** → Every Week.
- **One occurrence:** open it → Edit → change the time → Done → **Save for This Event Only**.
  Delete: open it → **Delete Event** → **Delete This Event Only**.
- **Whole series:** open it → **Delete Event** → **Delete All Future Events**, from the first one.

### Mac (macOS Calendar)

- **Add the account:** Calendar → Settings → Accounts → **+** → Other CalDAV Account. Account
  Type: **Manual**. User Name: Alice's user name. Password: the app password. Server Address:
  `<kycalendar host>`. Sign In.
- **Default calendar:** Calendar → Settings → General → Default Calendar → the KyCalendar one.
- **Accept or decline:** the inbox button (top left of the window, a tray icon), or double-click
  the event: **Accept / Maybe / Decline**.
- **Add attendees:** double-click the event → **Add Invitees** → type the email → Return.
- **Availability (test 4):** after adding Bob, click **Check Availability…** (or the
  Availability panel), and Bob's 15:00-16:00 should show as busy.
- **Repeat weekly:** in the event, **repeat** → Every Week.
- **One occurrence:** drag it to 11:00 (or edit its time), and when asked choose **Only This
  Event**. Delete: select it, press Delete, choose **Delete Only This Event**.
- **Whole series:** select the first one, press Delete, **Delete All Future Events**.

### Android (DAVx5)

DAVx5 syncs the calendar into Android. You use an ordinary Android calendar app on top (Google
Calendar or Etar). Write down which one.

- **Add the account:** DAVx5 → **+** → **Login with URL and user name**. Base URL:
  `https://<kycalendar host>/`. User name: Alice's user name. Password: the app password. Tap
  Login. **Account name: type Alice's email address exactly.** DAVx5 uses the account name as
  the organizer's address. Anything else and invitations from the phone go nowhere: the outbox
  shows `kypost_bad_recipient`, or nothing at all for Bob. Tap Create account.
- **Pick the calendar:** in DAVx5, open the account → **CalDAV** tab → tick the KyCalendar
  calendar → **Synchronize now**.
- **Sync now (do it after every change, on the phone):** DAVx5 → the account → **Synchronize
  now**. DAVx5 otherwise syncs only every so often.
- **Accept or decline:** in the calendar app, open the event. Google Calendar: **Going? Yes / No
  / Maybe** at the bottom. Etar: **Attending?** Then sync in DAVx5.
- **Add attendees:** new event → make sure the calendar picked is the DAVx5 one (shown with
  Alice's email) → **Add people** / Guests → type the email → Save. Then sync in DAVx5.
- **Availability (test 4):** **N/A**. Mark it N/A.
- **Repeat weekly:** **Does not repeat** → Every week.
- **One occurrence:** open it → edit → change the time → Save → **This event**. Delete: open it
  → delete → **This event**. Then sync in DAVx5.
- **Whole series:** open the first one → delete → **All events**. Then sync.

### Thunderbird

- **Add the account:** Calendar tab → **+** next to Calendars (New Calendar) → **On the
  Network** → Username: Alice's user name, Location: `https://<kycalendar host>/` → **Find
  Calendars** → enter the app password when asked (tick "remember") → pick the KyCalendar
  calendar → **Subscribe**.
- **Set the email:** right-click the calendar → **Properties** → **Email**: Alice's address.
  Needed for Thunderbird to see Alice as organizer and attendee.
- **Accept or decline:** open the event; Thunderbird shows **Accept / Tentative / Decline** (in
  the event window, or under its **Attend** menu).
- **Add attendees:** New Event → **Invite Attendees…** → type the email → OK. If Thunderbird
  asks whether to send invitations: answer is not important, the server sends either way. Write
  down what it asked.
- **Availability (test 4):** in **Invite Attendees…**, the grid to the right shows each
  person's busy time. Bob's 15:00-16:00 should be shaded busy.
- **Repeat weekly:** in the event, **Repeat** → Weekly.
- **One occurrence:** open it → **Edit only this occurrence** → change the time → Save and
  Close. Delete: select it → Delete → **Delete only this occurrence**.
- **Whole series:** select one → Delete → **Delete all occurrences**.

## Results

Fill one table per app. Result is **Pass**, **Fail** or **N/A** (only where a test allows it).
Notes: what you saw if it was not a clean Pass, and the outbox `last_error` if there was one.

### iPhone (iOS Calendar)

Device and iOS version: ______ · Date: ______

| # | Test | Result | Notes |
|---|---|---|---|
| 1 | Accept Bob's invitation | | |
| 2 | Decline Bob's invitation | | |
| 3 | Invite Bob from the device | | |
| 4 | Bob's busy time | | |
| 5 | Gmail round trip | | |
| 6 | Outlook.com round trip | | |
| 7 | Move and cancel one occurrence, all told | | |
| 8 | Delete the series, all told | | |

### Mac (macOS Calendar)

Mac and macOS version: ______ · Date: ______

| # | Test | Result | Notes |
|---|---|---|---|
| 1 | Accept Bob's invitation | | |
| 2 | Decline Bob's invitation | | |
| 3 | Invite Bob from the device | | |
| 4 | Bob's busy time | | |
| 5 | Gmail round trip | | |
| 6 | Outlook.com round trip | | |
| 7 | Move and cancel one occurrence, all told | | |
| 8 | Delete the series, all told | | |

### Android (DAVx5)

Phone, Android version, DAVx5 version, calendar app and version: ______ · Date: ______

| # | Test | Result | Notes |
|---|---|---|---|
| 1 | Accept Bob's invitation | | |
| 2 | Decline Bob's invitation | | |
| 3 | Invite Bob from the device | | |
| 4 | Bob's busy time (N/A allowed) | | |
| 5 | Gmail round trip | | |
| 6 | Outlook.com round trip | | |
| 7 | Move and cancel one occurrence, all told | | |
| 8 | Delete the series, all told | | |

### Thunderbird

Thunderbird version and OS: ______ · Date: ______

| # | Test | Result | Notes |
|---|---|---|---|
| 1 | Accept Bob's invitation | | |
| 2 | Decline Bob's invitation | | |
| 3 | Invite Bob from the device | | |
| 4 | Bob's busy time | | |
| 5 | Gmail round trip | | |
| 6 | Outlook.com round trip | | |
| 7 | Move and cancel one occurrence, all told | | |
| 8 | Delete the series, all told | | |

### Test 9: Gmail and Outlook.com invite Alice

How Alice answered (app and button): ______ · Date: ______

| Invitation from | Organizer sees Alice accepted | On iPhone | On Mac | On Android | In Thunderbird | Notes |
|---|---|---|---|---|---|---|
| Gmail | | | | | | |
| Outlook.com | | | | | | |

### Rollback rehearsal

Do this once, at the end, so you know rollback works before launch day.

| Step | Result | Notes |
|---|---|---|
| Switch off, `docker compose up -d`: the `curl` check no longer shows `calendar-auto-schedule` | | |
| Thunderbird, restarted, mails an invitation itself again | | |
| Switch back on: the `curl` check shows it again | | |

## Decisions

Write here any failure you accept for launch, and why (Yoshi, with the date).

-

## After the gate

- **If it passes:** set `KYCALENDAR_SCHEDULING=true` in the launch deployment's `.env`. The code
  default stays off. Open a follow-up PR that drops "Leave it off: unfinished" from
  `docs/ACCOUNTS-AND-GROUPS.md` and the "unfinished" note in `AGENTS.md`.
- **If it fails:** leave the switch off for launch. Record the failing rows here and open an
  issue for each.

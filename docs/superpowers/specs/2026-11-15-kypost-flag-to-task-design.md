# Flag an email to make a task (KyPost ↔ KyCalendar), 2026-11-15

Owner request 2026-11-15, deliverable 2 of the Tasks work (`2026-11-15-tasks-design.md`).
Worker `g-tasks`. KyPost gets a Flag action with due choices like Outlook; flagging makes a
VTODO in the user's KyCalendar Inbox list ("Tasks"), and completion stays in step both ways.
It rides the existing KyPost↔KyCalendar pair: same pairing, same shared secret, same
`ky-primitives/syncauth` signing, same `{sub, issuer}` naming (KyPost-Server
`docs/KYCALENDAR.md`, KyCalendar `internal/kypost/AGENTS.md`). No OAuth token exchange.

## Threat model

- **Actors:** the paired peer (KyPost or KyCalendar, holding the pair secret); anyone on the
  network; a user with an account on one side only; an administrator identity.
- **Assets:** the user's task list, the subject line of a flagged message, the flag state.
- **Answers:**
  - Forged or replayed requests: signature under the pair secret, the event type bound to its
    route (a `kypost.task.flag` is never accepted as `kycalendar.task.state`, nor reflected),
    the replay guard (`DefaultWindow`), a verified replay answered `duplicate` and not applied.
  - Writing into the wrong account: the user is `(issuer, sub)` of an active SSO account bound
    to the live sign-in binding; local accounts, administrators and unknown users are 404
    `no_user`. The task goes only into that user's own Inbox list.
  - Leaking mail into the calendar: the task carries the subject (sanitised, ≤ 255 bytes, no
    control characters) and an opaque reference, never the body, the sender's address, the
    Message-ID or attachments. The link is KyPost's own `https` URL built from the reference.
  - Loops: each side applies a peer's change without calling back, and a change equal to the
    stored state is a no-op (`unchanged`).
  - Flooding: 120 flag requests per user per hour on KyCalendar (429); KyPost bodies are read up
    to `kypost.Verify`'s 2 MiB cap, and the subject is cut to 255 bytes.
  - A link elsewhere: the task's `URL` must be `https` on the linked KyPost's own host (400).
  - Switch off on KyCalendar (the resource owner): 409 `tasks_off`, nothing written.
- **Out of model:** a compromised peer holding the pair secret can make tasks for paired users
  (as it can already send their invitations).

## Data stored

- KyCalendar: the task itself (VTODO in the Inbox list) with `X-KY-SOURCE:kypost`,
  `X-KY-REF:<ref>` and `URL:<https KyPost link>`; one row per user in a new
  `task_prefs (user_id, kypost_flags)` (absent = on). No mail content beyond the subject.
  The switch shows on the Tasks + menu's "Flagged from mail…" only while KyPost is linked;
  unlinked, the dialog shows only a note saying what happens once it is, since the switch
  would do nothing (council round 8).
- KyPost: the flag (its existing per-message `Flagged` state, written to IMAP `\Flagged`) and
  its due choice; nothing about the task beyond its UID for the clear path.

## Routes

| Direction | Event type | Route | Body | Answer |
|---|---|---|---|---|
| KyPost → KyCalendar | `kypost.task.flag` | `POST /api/tasks/flag` | `{sub, issuer, ref, subject, url, due?, flagged}`; `due` a `YYYY-MM-DD` KyPost computed in the user's zone, absent for No date | 200 `{outcome: created|updated|completed|unchanged|duplicate, calendar_id, uid}`; 401 `bad_signature`; 404 `no_user` or unpaired; 409 `tasks_off`; 400 `bad_body`; 429 |
| KyCalendar → KyPost | `kycalendar.task.state` | `POST /api/calendar/tasks/state` | `{sub, issuer, ref, done}` | 200 `{outcome: cleared|flagged|unchanged|gone|duplicate}`; KyPost's codes as for send |

- `ref` is KyPost's opaque message reference, `[A-Za-z0-9_-]{1,128}`; KyCalendar finds the task
  by `X-KY-REF` in the user's lists.
- Flagging an already flagged message updates the task's due date; unflagging completes it.
- KyCalendar sends `state` when a `kypost` task is completed, reopened or deleted through the
  web or MCP (delete sends `done: true`, so the flag clears). It is one attempt with a 5-second
  limit in tracked background work after the user's write has landed; it never delays the user's
  response. Shutdown cancels the send and waits for its `kypost.task_state` outcome audit. The
  audit context is independent of request cancellation, send timeout and shutdown; a
  failure leaves the flag set until the next flag change. A repeating task that moves on stays
  open, so its flag stays. Completions made in a CalDAV app are not sent (owner question 2).
- KyCalendar answers `flag` only while KyPost is linked, which (as for out-of-office) happens
  only with scheduling on or automatic; with `KYCALENDAR_SCHEDULING=false` the route is 404 and
  KyPost flags locally (owner question 1). It never verifies without its replay guard.
- A deleted message: the task keeps its link; KyPost's link page answers "This message is no
  longer available" (KyPost side).

## Defaults

- KyCalendar Settings → Tasks: "Make tasks from flagged mail" on for paired users; off sends
  `tasks_off` and KyPost flags locally only. Unpaired: KyPost's Flag flags locally, no task.
- Due choices: Today, Tomorrow, This week (Friday), Next week (next Monday), No date.

## Not built

- Native clients (KyPost Android, iOS, Linux, Mac): their own Flag menus are follow-ups; they
  call KyPost's same server action, so the task follows once they send the flag.
- Flagging from KyCalendar's side (a task cannot flag a message it did not come from).

## Owner questions (defaults in force)

1. KyCalendar links KyPost only with scheduling on or automatic, and launch deployments pin
   scheduling off until the device gate passes, so flag-to-task is off there too. Default: keep
   that (no second link path); the alternative is linking KyPost whatever the scheduling mode.
2. A task completed in Tasks.org or Apple Reminders does not clear the flag. Default: web and MCP
   only; the alternative is a periodic sweep of `kypost` tasks whose STATUS changed.

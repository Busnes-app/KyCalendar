# KyCalendar Tasks design (2026-11-15)

Owner request 2026-11-15: a Microsoft To Do-style Tasks screen in KyCalendar with tududi's
feature set, a day planner (Plan) and a Today screen, and "flag an email to make a task" in
KyPost. Worker `g-tasks`. Deliverable 1 is KyCalendar (API and web); deliverable 2 is the
KyPost flag, under the existing KyPost↔KyCalendar pair, after 1 merges.

## Decisions taken without the owner

Each is the safest option that still works for an ordinary user. Open questions are listed
at the end.

1. **Tasks are VTODO objects in calendars whose component set holds VTODO.** No new store
   for task content. The web, CalDAV and MCP read the same rows. The event API and MCP keep
   ignoring tasks (`TestEventAPIIgnoresTasks`).
2. **One write path.** Task writes go through `saveEvent` → `davbackend.WriteMeeting`, so
   quota, size, read-only and CalDAV validation apply exactly as for events and for a PUT.
3. **Edits mutate the stored VTODO in place.** The server decodes the stored object, changes
   only the properties the request names, and re-encodes. Every property and parameter the
   API does not own keeps its name, parameters and value unchanged (X-APPLE-*, X-MOZ-*,
   X-TASKS-ORG-*, VALARM children, ATTACH…). Line order and folding are the encoder's.
4. **Quick-add parsing happens in the browser.** The server takes structured fields; the
   preview shows exactly what will be sent. Plain text is always a valid title.
5. **Areas are KyCalendar-only.** One row per user (`task_areas`, JSON, capped). CalDAV
   clients never see them.
6. **Free/busy never reads VTODO.** The free/busy code already reads only VEVENT; a test pins
   that a placed task (DTSTART+DURATION) never appears busy.

## Threat model

- **Actors:** the signed-in everyday user; a group member with `reader`; another user of the
  instance; an administrator (never an everyday identity); anonymous callers; CalDAV clients
  writing hostile VTODOs that the web later renders.
- **Assets:** task text (titles, notes, tags) of personal and group lists; areas.
- **Threats and answers:**
  - Reading or changing another user's tasks: every route resolves the calendar through
    `calendarFor` (404 when unreadable) and checks `access.CanEditEvents` before a write
    (403 for readers and subscribed calendars). Admins are refused by `requireEveryday`.
  - A read-only group member completing a task: completion is a write; 403 (tested).
  - Oversized or hostile input: titles ≤ 1000 bytes, notes ≤ the event description cap,
    at most 20 tags of ≤ 64 bytes without commas or control characters, subtask depth ≤ 3
    levels, estimate 1 minute to 24 hours, at most 20 areas of ≤ 100 bytes. Over the cap is 400.
  - A parent cycle or a parent in another calendar: refused (400 `bad_parent`).
  - Lost updates: every change needs one strong `If-Match` (428/400/412 as for events).
  - Stored XSS from a CalDAV client: task text renders as text through `linkify`, never HTML.
  - Expensive listing: at most 5000 tasks per request (422 `too_many_tasks`); recurring
    completion computes one next occurrence, never an expansion.
  - Logging: task text, tags and calendar names are never logged or audited.
- **Out of model:** a malicious server operator; CalDAV clients that drop properties on their
  own writes (their behaviour, not ours).

## Interop mapping

| Feature | Property | Notes |
|---|---|---|
| Lists (tududi projects) | VTODO calendars | `components` contains `VTODO` |
| Inbox | the user's personal task list named "Tasks" | created on first use if missing |
| Priority high / medium / low / none | `PRIORITY` 1 / 5 / 9 / absent (0) | 1–4 read as high, 5 medium, 6–9 low (RFC 5545 §3.8.1.9) |
| Status | `STATUS` NEEDS-ACTION / IN-PROCESS / COMPLETED / CANCELLED | Completing writes `COMPLETED` (UTC) and `PERCENT-COMPLETE:100`; reopening removes `COMPLETED` and sets `PERCENT-COMPLETE:0` |
| Due date | `DUE` | `VALUE=DATE` for a day, or a date-time with `TZID` |
| Start / defer | `DTSTART` | a date: deferred until that day |
| Notes | `DESCRIPTION` | |
| Tags | `CATEGORIES` | one property, comma-separated, read from every CATEGORIES line |
| Subtasks | `RELATED-TO;RELTYPE=PARENT:<parent UID>` | a missing RELTYPE is PARENT (RFC 5545 §3.2.15) |
| Repeats | `RRULE` | daily, weekly (weekdays), monthly by day, monthly on the nth or last weekday (`BYDAY=2TU`, `BYDAY=-1FR`), last day of the month (`BYMONTHDAY=-1`), yearly, any `INTERVAL`, `UNTIL` or `COUNT` |
| Repeat from completion | see the interop research doc | the Tasks.org encoding |
| Reminders | `VALARM` with `ACTION:DISPLAY` and a `TRIGGER` relative to DUE (or START when there is no DUE) | existing alarms are kept as they are |
| Estimate | see the interop research doc | the length of a task, placed or not |
| Placed in the day | `DTSTART` (date-time) and `DURATION` | `DURATION` only when the task has no `DUE` (RFC 5545 §3.6.2: DUE and DURATION never both); otherwise the estimate property carries the length |
| Flagged from mail (deliverable 2) | `X-KY-SOURCE:kypost` and `URL` to KyPost's opaque message link | the link never carries the Message-ID |

`docs/superpowers/specs/2026-11-15-tasks-interop-research.md` holds the client research with
sources. `internal/tasks/testdata/` holds VTODOs as Tasks.org, Apple Reminders, Thunderbird and
jtx Board write them; `TestInteropFixturesSurviveWebEdits` loads each, applies every web edit
and checks that each property the edit did not name is unchanged.

### Completing a repeating task

Completing a repeating task follows Tasks.org, so the same task looks the same in every client:
the same VTODO moves to its next occurrence (DUE and DTSTART shift by the same amount, STATUS
returns to NEEDS-ACTION, PERCENT-COMPLETE and COMPLETED are cleared) and a `COUNT` is reduced
by one. With repeat-from-completion the next occurrence is computed from the completion time in
the task's zone; otherwise from the current DUE (or DTSTART). The wall-clock time is kept across
DST changes (a 09:00 task stays at 09:00). When the rule has no next occurrence the task is
completed. Recurring completions also count toward "Done this week", recorded in the
`task_completions` table (user, day; no text), because the VTODO itself no longer says COMPLETED.

## Data stored

- Task content: VTODO objects (existing `calendar_objects`).
- `task_areas` (migration 28): `user_id` primary key, `data` JSON `[{"id","name","lists":[calendar IDs]}]`,
  `updated_at`. Cascades with the user. Calendar IDs the user can no longer read are dropped
  when read.
- `task_completions` (migration 28): `(user_id, day)` with a count, for the Today insights.
  Rows older than 60 days are pruned daily with the change rows. No task text, no UID.

## Routes

All are `requireEveryday` (session cookie, CSRF as every JSON route; administrators 403).

| Method | Path | Auth | Answer |
|---|---|---|---|
| GET | `/api/tasks?calendar=<ids>&completed=<bool>` | read on each list | tasks of every readable task list (or the named ones); completed ones only with `completed=true`, newest 500 |
| POST | `/api/calendars/{id}/tasks` | editor or owner | create; 201 `{calendar_id,uid,etag}`; 422 `events_only` when the calendar holds no VTODO |
| PATCH | `/api/tasks/{cal}/{uid}` | editor or owner; `If-Match` | change the named fields only; `status: COMPLETED` on a repeating task moves it to the next occurrence |
| DELETE | `/api/tasks/{cal}/{uid}` | editor or owner; `If-Match` | 204; subtasks are kept and lose their parent |
| GET | `/api/tasks/inbox` | everyday | the Inbox list's ID, creating "Tasks" if the user has no task list |
| GET | `/api/tasks/areas` | everyday | the caller's areas |
| PUT | `/api/tasks/areas` | everyday | replace the caller's areas (≤ 20, each ≤ 100 bytes, lists the caller can read) |
| GET | `/api/tasks/stats?tz=` | everyday | `{done_this_week, streak}` |

The calendar views read tasks with a due time from `GET /api/tasks`; the Plan and Today
screens read the day's events from the existing `GET /api/events`.

## Defaults

- New task lists are personal; the add-calendar menu already offers "Tasks".
- Completed tasks are hidden from every view but Completed.
- Tasks with a due time show on the calendar views for every task list; each list's toggle
  lives in the calendar sidebar's list (stored in `localStorage`, `kycalendar.hiddenTaskLists`).
- Sort: due date, then priority.
- Flag-to-task (deliverable 2): on for a paired user, switch in KyCalendar's Settings.

## Screens

- **Navigation:** "Tasks" in the existing left navigation, below Calendar. The task lists and
  smart views render in the existing sidebar slot (the same portal the calendar list uses), so
  there is still one left column.
- **Smart views:** Today (the planner and the day list, below), Upcoming (14 days, grouped by
  day), Someday (no due date), Flagged from mail, Completed. Inbox and the user's lists, grouped
  by area; group lists under "Shared" with the group role.
- **Today:** progress ("3 of 9 done · 3h left"), the Next card (Push to later, Mark done), then
  the day in time order: events as plain rows with a calendar icon, tasks with a status menu
  (Not started, In progress, Done) and a left priority edge plus a text label. Replan re-places
  every unfinished task in the gaps after now. "Done this week" and the streak sit in the header.
- **Plan:** the day's timeline with every visible calendar's events read-only, free gaps
  labelled, the current time marked; "What could you do today?" candidates (overdue, due today,
  then the rest) with due state, list and estimate; "+" places a task in the first gap that fits;
  drag to place; placed tasks move and resize; the suggestion line with Place it and a 1/N
  stepper; the header "Xh planned of Yh free", Add task and Done. Phones: two tabs, Plan and
  Candidates.
- **Task row:** checkbox, title, relative due (red and the word "Overdue" when late), priority
  marker with a text label, tags, subtask count, repeat icon.
- **Task detail:** the existing `Modal` pattern with every field from the mapping table and
  inline subtasks.
- **Keyboard:** `n` new, `x` complete, `e` edit, `/` search, `j`/`k` move; each has a visible
  button or field.
- **Tasks against events:** tasks are an outlined, hatched block with a checkbox mark in the
  accent colour; events keep the filled calendar colour. They differ by shape and pattern, not
  only colour, in both themes.

## Not built

- tududi's notes (KyNotes owns notes), Telegram, assigning tasks to other people, an offline
  write queue, a separate insights dashboard.
- From the Plan/Today addendum: AI drafting, the motivational quote, goals, habits, the pomodoro
  timer, templates, saved views, people and "Assigned to me".
- Free/busy-only calendar views do not exist in KyCalendar today (every visible calendar is
  read with its titles), so the Plan timeline shows titles for every calendar the user can read.
  If a free/busy-only grant is added, the timeline must show those events as "Busy".
- Native clients (KyPost Android, iOS) for flag-to-task: follow-ups, listed in deliverable 2.

## Owner questions (defaults in force)

1. A placed task that has a DUE keeps its DUE and carries its length in the estimate property
   instead of `DURATION` (RFC 5545 forbids both). Default: as described.
2. Placing an overdue task today writes a DTSTART after its DUE, which RFC 5545 calls invalid
   but every client tested still reads. Default: allowed; the alternative is moving DUE.
3. Completing a repeating task follows Tasks.org (the same VTODO moves on) rather than RFC 5545
   per-occurrence completion, which Tasks.org and Apple Reminders do not use. Default: Tasks.org.

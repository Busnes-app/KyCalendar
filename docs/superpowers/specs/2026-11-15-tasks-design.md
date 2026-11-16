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
completed. Every web completion is logged in `task_completions` (IDs and time, no text), because a
repeating VTODO no longer says COMPLETED once it has moved on.

## Data stored

- Task content: VTODO objects (existing `calendar_objects`).
- Migration 28 adds, each cascading with its user (and the completion log with its calendar):
  - `task_areas`: `user_id` primary key, `data` JSON `[{"id","name","lists":[calendar IDs]}]`.
    Calendar IDs the user can no longer read are dropped when read.
  - `task_completions`: one row per completion `(user_id, calendar_id, uid, completed_at)`, for
    Today's "Done this week", habits, goals and Insights. IDs and times only, never text.
    Rows older than 400 days are pruned daily with the change rows.
  - `task_goals` and `task_views`: below.
  - the search index, below.

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
| GET | `/api/tasks/stats?tz=` | everyday | `{done_this_week, streak}` (1b) |

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

- tududi's notes stored in KyCalendar (KyNotes owns notes), Telegram, assigning tasks to other
  people, people and "Assigned to me", an offline write queue, AI drafting, the motivational
  quote, the pomodoro timer and templates.
- The "Note" choice in the capture box: KyNotes has no route that opens a new note with given
  text (its deep links are `#/<container>/<section|quick>/<page>`). KyCalendar hides the choice
  until KyNotes ships one; the proposed contract is in the addendum below.
- Free/busy-only calendar views do not exist in KyCalendar today (every visible calendar is
  read with its titles), so the Plan timeline shows titles for every calendar the user can read.
  If a free/busy-only grant is added, the timeline must show those events as "Busy".
- Native clients (KyPost Android, iOS) for flag-to-task: follow-ups, listed in deliverable 2.

## Addendum 2 (owner, 2026-11-15): the full tududi feature list

Delivery is four PRs, each behind the council gate: **1a** data, API, lists, areas, the capture
box, the task row and detail, subtasks, recurring, search, filters and views; **1b** Plan and
Today; **1c** matrix and Kanban; **1d** goals, habits and Insights.

### Interop research (cited)

Sources: Tasks.org `main` at `8f84221` (2026-11-15), jtx Board `main` the same day.

- **Repeat from completion.** Tasks.org keeps it in a local column (`Task.repeatFrom`,
  `data/.../entity/Task.kt`) and never writes it to CalDAV: `kmp/.../caldav/iCalendar.kt` writes
  `RRULE` from `task.recurrence` alone. Its pre-2022 local format embedded `FROM=COMPLETION` in
  the rule string, which its migrations now strip (`app/.../db/Migrations.kt`, `withoutFrom`); a
  non-standard RRULE part would break other clients, so KyCalendar does not copy it. KyCalendar
  writes `X-KY-REPEAT-FROM:COMPLETION` beside the rule. Tasks.org keeps unknown properties
  (`VTodoPreservationTest.aCachedVtodoKeepsWhatTheAppDoesNotModel`), so the marker survives its
  edits; Tasks.org itself repeats such a task from the due date.
- **Completing a repeating task.** Tasks.org moves the same VTODO to its next due date, clears
  the completion, decrements `COUNT` and completes the task when `COUNT=1` or no next date
  exists (`RepeatTaskHelper.handleRepeat`). KyCalendar does the same.
- **Estimate.** Tasks.org's `estimatedSeconds` is local only (no CalDAV mapping in
  `iCalendar.kt`); jtx Board has no estimate field. KyCalendar writes `X-KY-ESTIMATE` as an
  RFC 5545 duration (`X-KY-ESTIMATE:PT45M`).
- **Habit marker.** tududi keeps habits as task columns (`habit_mode`, `models/task.js`); no
  CalDAV client has one. KyCalendar writes `X-KY-HABIT:TRUE` on the recurring VTODO. Tasks.org,
  jtx Board, Apple Reminders and Thunderbird show it as an ordinary recurring task.
- **Fixtures.** `internal/tasks/testdata/` holds Apple Reminders (iOS 11/12, macOS 10.13) and
  Thunderbird VTODOs from Tasks.org's test resources (`app/src/test/resources/apple`,
  `.../thunderbird`), which Tasks.org captured from those clients, and a Tasks.org VTODO written
  with the properties its writer emits (`PRODID:+//IDN tasks.org//android-…`,
  `X-APPLE-SORT-ORDER`, `X-OC-HIDESUBTASKS`, `X-MOZ-SNOOZE-TIME`, `RELATED-TO;RELTYPE=PARENT`).

### The capture box

- One box on every screen (`n`, and a "New" button in the top bar). Modes: Task (parsed as in
  quick add), Inbox item (a task in Inbox with no date: parsing off) and List (a new VTODO list).
- **Files** reuse the event file links: `ATTACH;FMTTYPE=…;FILENAME=…:<https KyDrive link>` from
  the KyDrive picker, at most 20, validated exactly as for events (`eventFiles`).
- **Pasted images** become an inline `ATTACH;ENCODING=BASE64;VALUE=BINARY;FMTTYPE=image/png|jpeg|webp;FILENAME=…`
  on the task, titled "Screenshot HH:MM". The image never leaves KyCalendar. Cap: 512 KiB
  decoded, which keeps the object under `calendar.MaxObjectSize` (1 MiB) and the same quotas.
  The server checks the type by its magic bytes and refuses anything else (400 `bad_image`).
  The browser shows it as an `<img>` from a `blob:` URL it builds itself; the CSP already allows
  `img-src blob:` (checked in the PR).
- **Note** (not built): proposed KyNotes route `#/capture?text=<url-encoded>`; the fragment never
  reaches a server, and KyNotes encrypts the note as usual. Shown only when KyIdentity's app list
  holds `kynotes` and KyNotes has shipped the route.

### Goals

- `task_goals`: `id`, `user_id`, `title` (≤ 200 bytes), `period` (`season|quarter|year|custom`),
  `starts`, `ends` (dates, at most 366 days apart), `areas` and `lists` (JSON ID lists, ≤ 50
  each). At most 50 goals per user. Not in CalDAV.
- The goal page lists each linked list (an area expands to its lists): completions in the
  period (from `task_completions`), open tasks, the last completion, and "stalled" when the list
  had no completion in 14 days.

### Matrix and Kanban

- View switches on any list or smart view, kept per view in `localStorage`.
- Matrix: urgent = overdue or due within 2 days (by the viewer's day); important = `PRIORITY` 1–4.
  The rule is written on screen. Dropping in another quadrant changes priority (important ⇄ not:
  1 ⇄ none) and, only when the user picks it in the drop menu, the due date (urgent: today; not
  urgent: removed or kept).
- Kanban: Not started (NEEDS-ACTION), In progress (IN-PROCESS), Done (COMPLETED). Dropping sets
  `STATUS` through the same PATCH. Cancelled tasks are not shown.

### Habits

- A habit is a repeating task with `X-KY-HABIT:TRUE` (above); turning it on needs a rule.
- Each web completion writes a `task_completions` row after the CalDAV write succeeds; undoing
  it within the same day removes that row. Completions made in other clients are not logged
  (owner question 4).
- The habit view shows the current and longest streak (consecutive periods of the rule's
  frequency, daily or weekly, with a completion) and a 12-week grid.

### Saved views

- `task_views`: `id`, `user_id`, `name` (≤ 100 bytes), `query` JSON (`q`, `lists`, `areas`,
  `tags`, `priority`, `due_from`, `due_to`, `status`, `sort`, `layout`), ≤ 2 KiB, at most 50 per
  user. Listed under Tasks in the existing navigation. Built-in views (Today, Overdue, Upcoming,
  Someday, Flagged from mail) are code, not rows.

### Search and filters

- `GET /api/tasks?q=…` searches title, description and tags with SQLite FTS5 (`task_search`,
  `unicode61 remove_diacritics 2`, prefix match per word, all words required). On PostgreSQL the
  same index table is a plain table searched with `ILIKE` on each word. The index is reconciled
  per calendar at search time against `calendar_objects` ETags (only changed VTODOs are parsed),
  so CalDAV writes from any client are found without touching the shared write path.
- Only calendars the caller can read are searched. 30 searches per user per minute (429). The
  query is at most 200 bytes and 10 words; it is never logged or audited.
- Filters, all optional and combined with AND: `lists`, `area`, `tags`, `priority`
  (`high|medium|low|none`), `due_from`, `due_to` (dates), `status`. Smart sort: overdue, then due
  date, then priority, then title.

### Insights

- `GET /api/tasks/insights?tz=` returns completions per week (12 weeks), open and overdue per
  list, and Plan's planned versus free hours per day this week (planned: placed tasks; free: the
  working day 08:00–18:00 less events). One screen with an SVG bar chart, a text summary and a
  table fallback.

### Routes added by addendum 2

| Method | Path | Auth | Answer |
|---|---|---|---|
| GET | `/api/tasks?q&lists&area&tags&priority&due_from&due_to&status&completed` | everyday; read on each list | filtered, smart-sorted tasks; 429 `rate_limited` for `q` over the limit |
| GET, POST | `/api/tasks/goals` | everyday; own | list; create (201) |
| PUT, DELETE | `/api/tasks/goals/{id}` | everyday; own (404 otherwise) | replace; 204 |
| GET | `/api/tasks/goals/{id}/progress?tz` | everyday; own | per linked list: completed in period, open, last activity, stalled |
| GET, POST | `/api/tasks/views` | everyday; own | list; create (201) |
| PUT, DELETE | `/api/tasks/views/{id}` | everyday; own | replace; 204 |
| GET | `/api/tasks/{cal}/{uid}/habit?tz` | everyday; read | `{current, longest, days:[…]}` over 12 weeks |
| GET | `/api/tasks/insights?tz` | everyday | as above |

## Owner questions (defaults in force)

1. A placed task that has a DUE keeps its DUE and carries its length in the estimate property
   instead of `DURATION` (RFC 5545 forbids both). Default: as described.
2. Placing an overdue task today writes a DTSTART after its DUE, which RFC 5545 calls invalid
   but every client tested still reads. Default: allowed; the alternative is moving DUE.
3. Completing a repeating task follows Tasks.org (the same VTODO moves on) rather than RFC 5545
   per-occurrence completion, which Tasks.org and Apple Reminders do not use. Default: Tasks.org.
4. Habit streaks and Insights count completions made in the web app only; a completion made in
   Tasks.org or Apple Reminders moves the task on without a record KyCalendar can trust. Default:
   web completions only; the alternative is inferring completions from DUE moves.
5. The "Note" hand-off needs a KyNotes capture route (`#/capture?text=`). Default: hidden until
   KyNotes ships it.

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
| Repeat from completion | `X-KY-REPEAT-FROM:COMPLETION` beside the `RRULE` | Tasks.org writes nothing to CalDAV for it (Addendum 2, Interop research) |
| Reminders | `VALARM` with `ACTION:DISPLAY` and a `TRIGGER` relative to DUE (or START when there is no DUE) | existing alarms are kept as they are |
| Estimate | `X-KY-ESTIMATE:<RFC 5545 duration>` | neither Tasks.org nor jtx Board writes one (Addendum 2) |
| Placed in the day | `DTSTART` (date-time) and `DURATION` | `DURATION` only when the task has no `DUE` (RFC 5545 §3.6.2: DUE and DURATION never both); otherwise the estimate property carries the length |
| Flagged from mail (deliverable 2) | `X-KY-SOURCE:kypost` and `URL` to KyPost's opaque message link | the link never carries the Message-ID |

The client research with sources is in Addendum 2, Interop research. `internal/tasks/testdata/` holds VTODOs as Tasks.org, Apple Reminders, Thunderbird and
jtx Board write them; `TestInteropFixturesSurviveWebEdits` loads each, applies every web edit
and checks that every content line the edit does not own is still present byte for byte
(`testdata/SOURCES.md` cites each fixture). The encoder writes parameters sorted; RFC 5545
§3.2 gives their order no meaning, so the test compares a line's parameters as a set.

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
- Migration 32 adds, each cascading with its user (and the completion log with its calendar):
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
| DELETE | `/api/tasks/{cal}/{uid}` | editor or owner; `If-Match` | 204; subtasks are kept and show at the top level (their `RELATED-TO` names a missing task) |
| POST | `/api/tasks/inbox` | everyday | `{calendar_id}` of the Inbox list, creating "Tasks" when the user has none (POST: it can write) |
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
  day), and under All tasks: Open, Overdue, Someday (no due date), Flagged from mail, Completed.
  Inbox and the user's lists, grouped by area; group lists under "Shared" with the group role.
  The sidebar shows five rows before the lists (see "Owner review of the sidebar", below).
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
- **Task row:** checkbox, title, relative due (red and the word "Overdue" when late), a "High"
  label as the only priority mark, tags, subtask count, repeat icon.
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
  on a new Inbox item, titled "Screenshot HH:MM" (renameable before or after saving). The image
  never leaves KyCalendar and is not read (no OCR). Cap: 512 KiB decoded, which keeps the object
  under `calendar.MaxObjectSize` (1 MiB) and the same quotas. The server checks the type by its
  magic bytes and refuses anything else (400 `bad_image`), and serves an image only from
  `GET /api/tasks/{cal}/{uid}/images/{n}` to a caller who can read the list, typed by its bytes
  (never the stored FMTTYPE), with `nosniff`, `Content-Disposition: inline`, `Cache-Control:
  private, no-store` and `Content-Security-Policy: default-src 'none'; sandbox`; an ATTACH from
  another client whose bytes are not an image is never served. The capture box previews the
  pasted image from a `data:` URL, which the CSP's `img-src 'self' data:` already allows; the
  CSP is unchanged.
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
4. (Answered 2026-11-15: count every client's completions; see PR 1d.) Habit streaks and Insights count completions made in the web app only; a completion made in
   Tasks.org or Apple Reminders moves the task on without a record KyCalendar can trust. Default:
   web completions only; the alternative is inferring completions from DUE moves.
5. The "Note" hand-off needs a KyNotes capture route (`#/capture?text=`). Default: hidden until
   KyNotes ships it.

## Addendum 3 (owner, 2026-11-15): no matrix; AI through MCP

- **The Eisenhower matrix is dropped.** No matrix view, no urgent/important grouping and no "Do
  first" view; the Matrix section of Addendum 2 is void. PR 1c is Kanban only.
- **Agents use `/mcp`.** No AI drafting in the web UI; an agent drafts a day through these tools,
  added in PR 1a under the existing scopes and tokens (`withMCPAuth`):

| Tool | Scope | Does |
|---|---|---|
| `list_tasks` | `mcp:read` and up | the web's filters and search (`q`, `lists`, `tags`, `priority`, `due_from`, `due_to`, `status`, `sort`) over the caller's personal task lists |
| `get_task` | `mcp:read` and up | one task by `calendar_id` and `uid` |
| `day_gaps` | `mcp:read` and up | one day's busy blocks and free gaps (start and end only, never a title) from the caller's personal calendars, between `day_start` and `day_end` (default 08:00 to 18:00) |
| `create_task` | `mcp:write-own`, `mcp:write-any` | a task in a personal task list; recorded in `mcp_events` |
| `update_task` | `write-own`: only tasks this token created; `write-any`: any personal task | fields as the web PATCH, `status` included (complete, reopen), and placing (`start` + `duration_min`); needs the task's `etag` |

- Group lists: MCP serves personal calendars only, as for events (a group calendar is answered as
  missing on every tool), so "group lists follow the group role" means an agent never reaches one.
- Never through MCP: deleting tasks or lists, sharing, areas, views, goals and habit
  configuration (`habit` is not an input), files and images.
- Bounds are the web's (`taskBody.patch`), plus notes capped at 2 KiB in results like event
  descriptions. Tool calls audit `mcp.task_create` and `mcp.task_update` with IDs only.
- Negative tests: wrong scope, another user's task, a write-own token on a task it did not
  create, a `dav` token refused, a group list missing, oversize input.

## Owner review of the screens (2026-11-15)

The first screens were rejected as "a busy mess". The cuts, in force for every Tasks screen:
one way to add per screen (the capture box and quick add; no header New task, no Plan Add task);
search, filters and sort only on list views; no subtitles; one priority signal, a "High" label;
the accent only for the primary action and the now-line; on Plan a quiet suggestion line above
the candidates and none on Today; a checkbox per task on Today (status lives in the detail);
compact 24-hour times that never wrap; candidates with one meta line and a duration only when
set; Plan's only way out is "Back to Today"; "Push to later" is a text button. Planned tasks are
a calm solid card with a thin accent edge and a check mark; events are quiet and dashed.

## Council round 1 (2026-11-15)

Scores: Opus 6.5 (gate 8.0), Sol 7.2. Accepted and applied: one accent use per screen (checked
boxes, progress fill, task rails and the main navigation's Tasks row neutral); no mini progress
bars; Today without Replan, the Next task only in its card, earlier done tasks folded into one
line, borderless rows on phones and no calendar names; Plan blocks at least 28px and opaque,
events a quiet fill, its mark checked only when done, 44px pager buttons; capitalised relative dates; the calendar's Tasks link in muted ink; Sort inside Filters; the
shortcut list behind `?`; no repeated dates in Upcoming's groups; Due and Start a full row each
with a full-width priority control and a visible close; a short phone placeholder and the parse
feedback as one quiet line. Replan is gone: Plan my day covers it.

## Council round 2 (2026-11-15)

Scores: Opus 7.6, Sol 7.8. Applied: times follow the calendar's own format (the browser locale)
with en-dash ranges, replacing round 1's forced 24-hour; Today counts exactly the tasks it shows
and drops the week count; the phone's Tasks tab stays marked; the task dialog's actions are a
sticky footer; phone list and candidate rows are borderless; the board keeps the list width,
counts like the list, shows top-level tasks with their subtask progress, collapses empty columns
and drops the list name inside a list; the List/Board control has no outer frame; Plan block meta
lines up under the title and short blocks keep a one-line time.
## PR 1c: Kanban

A List/Board switch on every list view (not Today or Plan), remembered per view in the browser.
The board asks the server for open and done tasks of the view (`status=needs-action,in-process,completed`)
and shows three columns by `STATUS`; cancelled tasks are not shown. Dragging a card to another
column, or its arrow buttons (the keyboard and touch twin), sends `PATCH {status}`, so completing
a repeating task on the board moves it to its next occurrence as anywhere else. No new route.

## Council round 3 (2026-11-15)

Scores: Opus 7.9, Sol 8.3 (satisfied). Applied: times from the calendar's own FullCalendar
formatter and the calendar's now-line colour; one count everywhere (open top-level tasks, "1 open
task"); no resting tint on a row; short Plan blocks show their range without priority; the
progress fill in secondary ink; Done cards without strikethrough or High; the segment ring only
on keyboard focus; a neutral quick-add plus; Add reminder aligned with the labels; tighter phone
spacing on Plan.
## PR 1d: goals, habits and insights

- **Completions from every client** (owner answer, 2026-11-15): the log is written in the one
  write path, so a task completed in Tasks.org, Apple Reminders or Thunderbird counts like one
  completed on the web or over MCP. One row per writer, task and occurrence (the DUE, else
  DTSTART, value): the web's completion synced back from a phone counts once; un-completing
  removes it; a repeating task moved to the rule's next occurrence counts as completing the one it
  left; a COMPLETED time in the future or before CREATED becomes the receive time, an older one
  is kept (migration 33). Owner question 4 is answered; the web-only default is retired.
- **Goals:** `task_goals` per user, at most 50; a goal links areas and lists over a season,
  quarter, year or custom period (at most a year). Progress per linked list: done in the period,
  open, last done, and "No progress in 14 days".
- **Habits:** `X-KY-HABIT:TRUE` on a daily or weekly repeating task ("Track as a habit" in the
  detail). Streaks count consecutive days or weeks (from Monday) with a completion, ending today
  or the period before; the grid shows 12 weeks of days.
- **Insights:** completions per week (12 weeks), open and overdue per list, and planned against
  free time this week (08:00 to 18:00 less personal busy times). The chart is one neutral series
  with a table view; every other figure is a table.
- No new data leaves the instance; insights and goals are the caller's own, computed from their
  own log and readable lists.

## Owner review of the sidebar, Habits and Insights (2026-11-16)

The owner rejected the Tasks sidebar as too long ("make this side not as long") and Habits and
Insights for wasted space and no colour. The change is presentation only: no route, query,
stored field or figure changes.

- **Sidebar:** five rows before the lists: Inbox, Today, Upcoming, All tasks, Progress (was ten).
  Overdue folds into Today, whose count already includes overdue tasks. All tasks carries Open,
  Overdue, Someday, Flagged from mail and Completed as tabs; Progress carries Habits, Goals and
  Insights. The tabs are the Settings page's `ky-settings-tabs`, horizontal under the page head,
  so there is still one left navigation region and no tab rail beside it. At 1280×800 the
  user's first list is in view.
- **Old addresses:** task views never had their own URL; the only stored address is the
  browser's saved view (`kycalendar.tasksView`). Its shape is unchanged, so a view saved by the
  old sidebar opens on its tab, with the row that holds it marked. `#/tasks` is unchanged.
- **Habits:** one card per habit across the content column: current streak, longest, completion
  rate and this week as large figures, beside a grid that starts at the week the habit began
  (its creation or first completion), at most 12 weeks (the API keeps returning 12), cells up to
  2.5rem. Done cells use the habit's list colour (accent by default), lightened on dark themes;
  missed cells a tint of it inside an edge of the full colour; today (this week for a weekly
  habit) is only the edge while not yet done, and counts toward the rate only once done or
  over; days of the first week before the habit began have a dashed outline in the muted text
  colour and are left out of the rate (the key names today and pre-start days only when they
  show). A weekly habit is one cell per week, so a week with one completion is not shown as six
  missed days, and its figures sit in a row above the strip on wide cards. Phone cards use
  smaller figures and tighter spacing. The grid rules are `web/src/habitGrid.ts`.
- **Insights:** three cards. Completed per week in the accent: earlier weeks a tint, this week
  full and labelled, zero weeks a baseline stub, counts above bars; the chart is drawn at its
  box's pixel width so its text stays 12px. Planned in the accent on a tinted track, today's row
  marked and labelled. Each list with its colour as a swatch and a bar; overdue above zero in the
  danger colour with an icon and the word "overdue".
- **Goals:** each goal is a card like Insights'; its List column shows the list's swatch and a
  bar of done out of done plus open on a tinted track. Headers are "Done", "Open" and "Last
  done", never wrapping (the card shows the period). "New goal" sits at the end of the tab row,
  so the page head does not move between tabs; below 480px it is a "+" named "New goal" with a
  tooltip.
- **Contrast:** done cells against the card and against missed cells, missed cells' edge,
  pre-start outlines, the zero-week stub and every bar against the card hold 3:1 in light and
  dark for the accent and every palette colour (`web/browser/insights.spec.mjs`). Text stays on
  the existing ink tokens. The list palette leaves out the Busnes accent (#bf3f18, replaced by
  olive-ochre #7a6a12, a warm hue apart from the teal and blue beside it), so no list reads as
  the accent highlight.
- **Tabs on phones:** the chosen tab is centred in its row, which scrolls sideways. Flagged from
  mail and Completed have no quick add (flagged tasks come from KyPost); Flagged's empty state
  says "Tasks you flag in KyPost appear here." The Lists panel keeps its "Tasks" heading, so its
  "+" (Add a list or area) is not a bare mark.
- **Not changed:** the streak logic, the completion log and the insight queries.

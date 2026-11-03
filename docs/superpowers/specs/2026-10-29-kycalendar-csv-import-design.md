# KyCalendar CSV import — design

## Goal

A user uploads a calendar CSV once and gets its events as ordinary, editable events. The file can be an Outlook export or a file in Google Calendar's import format. This is a snapshot, not a subscription; for a calendar that keeps changing, the ICS subscription (`2026-10-15-kycalendar-ics-subscriptions-design.md`) is still the answer.

## Decisions

| Question | Decision |
|---|---|
| Formats | Outlook's "Export to a file → Comma Separated Values" and Google Calendar's documented import columns, matched by header name. There is no column-mapping UI |
| Target | Chosen at upload: any calendar the user can add events to (`canAddEvents`), or a new personal calendar, named after the file by default |
| Re-upload | The UID is a hash of title, start, end and all-day, so a second upload of the same rows into the same calendar adds nothing and reports them as already there |
| Zone | Timed rows are wall times in the browser's zone (`tz`) |
| Date order | Read from the file when a date settles it (a part over 12, or dotted dates); otherwise the browser locale's order (`date_order`). Year-first dates need no order |
| Encoding | The file is uploaded raw. The server reads UTF-8, UTF-16 with a BOM, and anything else as Windows-1252, which classic Outlook writes |
| Limits | The server-wide 1 MiB request cap (the web refuses a larger file before sending it) and 5000 data rows, counted while reading |
| Atomicity | None. Each row goes through the normal event write. A full quota stops the import and the earlier rows stay; the response says where it stopped |
| Ignored | Reminders, attendees, organizer, categories, privacy, busy status and recurrence. CSV has no recurrence, so every row is one event |

Out of scope: `.ics` file import, column mapping, other export formats (Apple, Thunderbird), and localised (non-English) Outlook headers.

## API

`POST /api/calendars/{id}/import?tz=<IANA>&date_order=mdy|dmy`, body the file. Access is the same as creating an event: editor or owner, never a subscribed calendar, never a tasks-only one; a calendar the caller cannot read is 404. A browser session needs its CSRF token like every other write. The response is 200 `{imported, duplicates, skipped:[{row, reason}], skipped_count, stopped?}`, and `skipped` lists the first 100. The handler extends its write deadline to 5 minutes, since 5000 writes can outlast the listener's 15 s `WriteTimeout`. The import is audited as `calendar.import` with the three counts. The route table in `internal/api/AGENTS.md` has the status codes.

## Reading rows

`internal/csvimport` holds the rules (see its `AGENTS.md`). The one that is easy to get wrong: Outlook writes an all-day event's End Date as the day after, and Google's format names the last day. A file is treated as Outlook when it has the `Reminder on/off` or `Show time as` column.

## Web

The calendar list's "+" menu has "Import a CSV file" after New calendar and Subscribe. It opens a dialog with a file, an "Add to" calendar, and a name and colour when "New calendar…" is chosen. A new calendar is created first and deleted again if the import fails. The result shows the counts and the first skipped rows with their reasons, as text.

## Testing

- `internal/csvimport`: Outlook and Google files, date order, skip reasons, five encodings and malformed bytes, refused files, the row cap at its boundary, a stable UID, and `FuzzParse`.
- `internal/api/imports_test.go`: import read back through `/api/events`, re-import as duplicates, a second calendar importing the same file, zone and date order, hostile rows, refusals (subscribed, reader, tasks-only, another user's calendar, bad `tz`/`date_order`, not a calendar, over the row cap, over 1 MiB, no CSRF token, no session) with nothing written, the stop at the quota, and the write deadline. The authz matrix row is `groupWrite`.
- Web (`CalendarSidebar.test.tsx`): import into an existing calendar, a failed import into a new calendar deleting it again, the size refusal, server text rendered as text, and the locale date order.

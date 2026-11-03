# CSV import

## Purpose
Reads calendar CSV files (an Outlook "Export to a file" CSV, or Google Calendar's import format) into plain event rows for a one-time import.

## Ownership
Owns CSV decoding, column matching, date and time reading and the import UID. Pure: no store, no HTTP. The route, text and year checks and the writes live in `internal/api/imports.go`.

## Local Contracts
- Columns are matched by header name, case-insensitively: `Subject` and `Start Date` are required (else `ErrNotCalendar`), and `Start Time`, `End Date`, `End Time`, `All Day Event`, `Description` and `Location` are read when present. Other columns are ignored. More than `MaxRows` (5000) data rows is `ErrTooManyRows`, found while reading record by record, so a file is never held as records past the cap. Blank rows are ignored but count toward the cap.
- Encoding: UTF-16 by its byte order mark (either endianness), UTF-8 (BOM dropped), and anything else, a UTF-8 file with one bad byte included, as Windows-1252, which classic Outlook writes. Rows always come out as valid UTF-8.
- Dates: three numbers split by `/`, `.` or `-`. A four-digit first number is year-first (`2026-03-02`, `2026/03/02`); otherwise a two-digit year means 20xx. Impossible dates (31/02) are skipped, never rolled over. The order comes from the file itself: a first part over 12, or dotted dates, means day first; a second part over 12 means month first. Only a file that could be read either way uses the caller's hint. Times use 24-hour or AM/PM forms, with or without seconds.
- A row is all-day when `All Day Event` is true/yes/1, or when it has no start and no end time. All-day ends are exclusive. Outlook files, recognised by a `Reminder on/off` or `Show time as` column, already write the day after. A Google file names the last day, so one day is added. An end on the start day (in either format) becomes the start day plus one; an end before the start day is skipped.
- Timed rows are wall times in the caller's zone. A missing end date means the start date, and a missing end time means one hour after the start. A row that ends before it starts is skipped.
- A row that cannot be read is a `Skip{row, reason}`, with the spreadsheet row number (the header is row 1); the rest still import.
- `Row.UID()` is `csv-` plus a hash of title, start, end and all-day, so importing the same file twice gives the same UIDs and the API skips them as duplicates.

## Verification
- `go test ./internal/csvimport/`; `FuzzParse` (`go test -fuzz FuzzParse ./internal/csvimport/`) holds any input to no panic, the cap, valid UTF-8 and ordered times. Route-level tests are in `internal/api/imports_test.go`.

## Child DOX Index
None.

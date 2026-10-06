# internal/calendar

## Purpose
Pure iCalendar rules: what a calendar object must contain and how it is indexed; sync token format.

## Local Contracts
- No I/O. Callers pass decoded `*ical.Calendar`.
- VEVENT only; one UID per object; every VEVENT needs DTSTART.
- `FirstStart`/`LastEnd` are conservative: unknown or floating zones widen by 14h; rules past 100 years, or needing more than 100000 occurrences, index as unbounded (`LastEnd == nil`).
- Recurrence work is bounded by the rrule-go set iterator and one 100000-occurrence budget per object (VEVENTs and RDATE instances); RDATE is scanned locally (go-ical reads EXDATE instead). Any RecurrenceSet error or unparseable RDATE indexes as unbounded.
- At most 10 RRULE-bearing VEVENTs per object are evaluated (the 11th and later index unbounded, no further expansion); FREQ=SECONDLY and FREQ=MINUTELY are never expanded (unbounded). RDATE parsing stops when the budget is spent (unbounded, FirstStart = epoch).
- Sync tokens are `urn:kycalendar:sync:<epoch>:<seq>`.

## Verification
- `go test ./internal/calendar/`

# internal/calendar

## Purpose
Pure iCalendar rules: what a calendar object must contain and how it is indexed; sync token format.

## Local Contracts
- No I/O. Callers pass decoded `*ical.Calendar`.
- VEVENT only; one UID per object; every VEVENT needs DTSTART.
- `FirstStart`/`LastEnd` are conservative: unknown or floating zones widen by 14h; rules past 100 years, or needing more than 100000 occurrences, index as unbounded (`LastEnd == nil`).
- Recurrence work is bounded by the rrule-go set iterator, never `Between`/`After`.
- Sync tokens are `urn:kycalendar:sync:<epoch>:<seq>`.

## Verification
- `go test ./internal/calendar/`

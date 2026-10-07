# internal/calendar

## Purpose
Pure iCalendar rules: what a calendar object must contain and how it is indexed; sync token format.

## Local Contracts
- No I/O. Callers pass decoded `*ical.Calendar`.
- VEVENT only; one UID per object; every VEVENT needs DTSTART.
- `ResolveZone(tzid)` is the only TZID lookup for expansion and edits: tries IANA names via Go's data (the binary embeds `time/tzdata`), then Windows names through `windowsZones` (generated from CLDR release-48-2, territory 001, by `go generate`; the generator pins the source file's SHA-256), then old Thunderbird `/mozilla.org/<version>/<IANA>` paths. `loadIANA` helper refuses "" and "Local" at all lookup points. Unresolved zones are shown in UTC and flagged by callers.
- `FirstStart`/`LastEnd` are conservative: unknown or floating zones widen by 14h; rules past 100 years, or needing more than 100000 occurrences, index as unbounded (`LastEnd == nil`).
- Recurrence work is bounded by the rrule-go set iterator and one 100000-occurrence budget per object (VEVENTs and RDATE instances); RDATE is scanned locally (go-ical reads EXDATE instead). RRULEs are expanded by `ruleLast` with iteration cut at the 100-year horizon; a rule with neither COUNT nor UNTIL, or an unparseable rule or RDATE, indexes as unbounded. EXDATE is ignored (it only narrows).
- At most 10 RRULE-bearing VEVENTs per object are evaluated (the 11th and later index unbounded, no further expansion); FREQ=SECONDLY and FREQ=MINUTELY are never expanded (unbounded). RDATE parsing stops when the budget is spent (unbounded, FirstStart = epoch).
- A rule whose BYHOUR x BYMINUTE x BYSECOND exceeds 96 indexes unbounded without expansion: rrule-go builds every day of a period times that set before the first occurrence, so the budget cannot cap it.
- An RRULE on a component with an unknown or floating DTSTART zone, or more than one RRULE on a component, indexes unbounded without expansion.
- `CheckProps` bounds calendar name (255 bytes), description (4096 bytes) and colour (`#RRGGBB[AA]` or empty) for CalDAV and the JSON API alike.
- Sync tokens are `urn:kycalendar:sync:<epoch>:<seq>`.

## Verification
- `go test ./internal/calendar/`

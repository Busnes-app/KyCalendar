# internal/resource

## Purpose
Pure rules for rooms and equipment (resources): the definition, its bounds and the resource's
calendar user address. Design: `docs/superpowers/specs/2026-11-15-resource-booking-design.md`.

## Ownership
Owns `Resource`, `Clean`, `Address` and `IDOf`. Storage (`store.ResourceStore`, migration 28) and
the routes (`internal/api/admin_resources.go`) belong to their packages; nothing here does I/O.

## Local Contracts
- Code says resource; the UI says Room or Equipment ("room" alone is a KyMessages meeting room).
- `Address(id)` is `mailto:resource-<id>@kycalendar.invalid` (32 lowercase hex ID; RFC 2606
  `.invalid` is never routed). `IDOf` accepts only that shape.
- `Clean` trims and bounds every field: kind `room` or `equipment`; name 1-100 and location at
  most 200 characters; capacity 0-10000; at most 20 tags of 1-40 characters, deduplicated
  ignoring case; audience `everyone` or `groups` (1-50 groups); approval `auto` or `approvers`
  (1-20 approvers); 5-20160 minutes; 1-730 days. Text holds no control, format or separator
  character and no `"`. Groups or approvers an unused policy names are dropped.
- A resource is never deleted; disabling keeps its history.
- Booking rules (`settle.go`): `IDs` (resource attendees, first seen), `Fingerprint` (versioned `v2:` digest over length-prefixed fields and components, so quoted parameters cannot alias; DTSTART, DTEND, DURATION, RRULE, RDATE, EXDATE, RECURRENCE-ID and STATUS of every VEVENT), `Plan` (unknown, disabled, past, ambiguous (DTEND beside DURATION, or two components for one occurrence by `calendar.OccurrenceKey`), beyond the horizon, more than 1000 occurrences, too long, including any VEVENT, master or override (measured by expanding it, so an implicit all-day day counts), or RDATE period past the horizon; spans to reserve from now, `OpenAfter` for a series that runs past the horizon), `SeriesFingerprint` and `Within` (an unchanged rule keeps its answer when every upcoming occurrence exists in the previous object; comparisons extend past both versions' last override using expansion's timezone resolution, and unreadable or ambiguous overrides refuse preservation; attendee replies and cancellations retain their answer, while moved, detached, longer or restored occurrences are decided again), `Conflicts`, `OpenSpans`, `SetAnswers` (PARTSTAT, CUTYPE ROOM/RESOURCE, CN, ROLE=NON-PARTICIPANT, no RSVP) and `SetList` (the web's list; fills an empty LOCATION with the rooms; sets the writer as organizer when there is none).

## Verification
- `go test ./internal/resource/ ./internal/api/ -run 'Resource'`

## Child DOX Index
None.

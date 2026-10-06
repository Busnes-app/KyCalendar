# internal/davbackend

## Purpose
Maps CalDAV (the go-webdav fork) onto the calendar store for one authenticated user per request.

## Local Contracts
- Paths: `/dav/<user-id>/calendars/<slug>/<name>`; anything outside the user's home is 403. Object names are opaque keys (clients use UIDs, including Outlook's `{...}` and base64): 1-255 bytes of valid UTF-8, no `/`, no control characters, not `.` or `..` (403 otherwise).
- Plan 1 serves personal calendars only (`owner_kind = 'user'`); a `default` calendar is created on listing or on access to its path only when the user owns no calendar.
- Bytes from PUT are stored as received; GET, calendar-data and sync serve `Raw` and never decode. Only objects that go through `caldav.Match` are decoded.
- Per-user caps answer 507 (`store.ErrQuotaExceeded`): objects on create, calendars on MKCALENDAR and default creation, stored bytes on every PUT (total minus the replaced object plus the new body). The store enforces them atomically; the backend only passes the limits.
- Calendar properties are bounded at this boundary (`checkCalendarProps`): `calendar-color` empty or `#RRGGBB`/`#RRGGBBAA`, `displayname` at most 255 bytes, `calendar-description` at most 4096 bytes. MKCALENDAR over a bound is 400; PROPPATCH is 403 for the whole request, nothing is stored.
- Calendars cannot be deleted over CalDAV (403).
- Query results keep objects whose filter match errors. In a query with a VEVENT time range, objects indexed unbounded (`LastEnd == nil`) or whose raw bytes contain `RRULE` or `RDATE` (R29, `bytes.Contains`, never decoded) skip `caldav.Match` and are returned, so the index alone decides overlap and a large series costs no expansion per query; other queries match every object.
- DELETE `If-Match` reaches the backend through `WithIfMatch`. Any `If-Match` (including `*`) on a missing object is 412 for PUT and DELETE; `*` is passed to the store, which checks it inside the write transaction.

## Verification
- `go test ./internal/api/ -run 'CalDAV|DAV'`

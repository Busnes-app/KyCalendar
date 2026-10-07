# internal/davbackend

## Purpose
Maps CalDAV (the go-webdav fork) onto the calendar store for one authenticated user per request.

## Ownership
Owns `Backend` (the `caldav.Backend` implementation), DAV path parsing and object-name rules, calendar property bounds, query matching policy and the mapping of store errors to DAV status codes. Authentication, the per-user path guard and the `caldav.Handler` mount belong to `internal/api`; storage and quotas to `internal/store`; iCalendar validation and indexing to `internal/calendar`.

## Local Contracts
- Paths: `/dav/<user-id>/calendars/<slug>/<name>`; anything outside the user's home is 403. Object names are opaque keys (clients use UIDs, including Outlook's `{...}` and base64): 1-255 bytes of valid UTF-8, no `/`, no control characters, not `.` or `..` (403 otherwise).
- The home lists the user's personal calendars (a default is created on first listing) and every group calendar a grant lets them read, at `_<calendar-id>/`. Roles come from `access.Resolve` over `Backend.Grants`, which `handleDAV` loads from `UserGrants` on every request, so membership changes apply to the next request.
- A group calendar the user cannot read answers 404 on every method. Reading needs reader, `PUT` and `DELETE` of objects need editor, and `PROPPATCH` needs manager (owner on personal calendars); otherwise 403. `ReadOnly` (no `write` in `current-user-privilege-set`) is set below editor. `MKCALENDAR` at a `_` segment is 403. Deleting any calendar over CalDAV stays 403.
- A group calendar is its own quota owner: the per-user object, byte and calendar limits apply per group calendar, and the instance byte cap still applies.
- Bytes from PUT are stored as received; GET, calendar-data and sync serve `Raw` and never decode. Only objects that go through `caldav.Match` are decoded.
- Per-user caps answer 507 (`store.ErrQuotaExceeded`): objects on create, calendars on MKCALENDAR and default creation, stored bytes on every PUT (total minus the replaced object plus the new body). The store enforces them atomically; the backend only passes the limits.
- Calendar properties are bounded at this boundary (`calendar.CheckProps`): `calendar-color` empty or `#RRGGBB`/`#RRGGBBAA`, `displayname` at most 255 bytes, `calendar-description` at most 4096 bytes. MKCALENDAR over a bound is 400; PROPPATCH is 403 for the whole request, nothing is stored.
- Calendars cannot be deleted over CalDAV (403).
- Query results keep objects whose filter match errors. In a query with a VEVENT time range, objects indexed unbounded (`LastEnd == nil`) or whose raw bytes contain `RRULE` or `RDATE` (R29, `bytes.Contains`, never decoded) skip `caldav.Match` and are returned, so the index alone decides overlap and a large series costs no expansion per query; other queries match every object.
- DELETE `If-Match` reaches the backend through `WithIfMatch`. Any `If-Match` (including `*`) on a missing object is 412 for PUT and DELETE; `*` is passed to the store, which checks it inside the write transaction.

## Verification
- `go test ./internal/api/ -run 'CalDAV|DAV'`

## Child DOX Index
None.

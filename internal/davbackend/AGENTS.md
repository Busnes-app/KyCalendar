# internal/davbackend

## Purpose
Maps CalDAV (the go-webdav fork) onto the calendar store for one authenticated user per request.

## Local Contracts
- Paths: `/dav/<user-id>/calendars/<slug>/<name>`; anything outside the user's home is 403. Object names must match `^[A-Za-z0-9][A-Za-z0-9_.@+-]{0,254}$` (403 otherwise).
- Plan 1 serves personal calendars only (`owner_kind = 'user'`); a `default` calendar is created on listing or on access to its path only when the user owns no calendar.
- Bytes from PUT are stored as received; GET, calendar-data and sync serve `Raw` and never decode. Only objects that go through `caldav.Match` are decoded.
- Per-user caps answer 507: objects on create, calendars on MKCALENDAR, stored bytes on every PUT (current total minus the replaced object plus the new body). The checks run before the store transaction, so concurrent writes can overshoot by the requests in flight.
- Calendars cannot be deleted over CalDAV (403).
- Query results keep objects whose filter match errors. In a query with a VEVENT time range, objects indexed unbounded (`LastEnd == nil`) skip `caldav.Match` and are returned; other queries match every object.
- DELETE `If-Match` reaches the backend through `WithIfMatch`. Any `If-Match` (including `*`) on a missing object is 412 for PUT and DELETE.

## Verification
- `go test ./internal/api/ -run 'CalDAV|DAV'`

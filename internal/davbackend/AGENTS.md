# internal/davbackend

## Purpose
Maps CalDAV (the go-webdav fork) onto the calendar store for one authenticated user per request.

## Local Contracts
- Paths: `/dav/<user-id>/calendars/<slug>/<name>`; anything outside the user's home is 403.
- Plan 1 serves personal calendars only (`owner_kind = 'user'`); the `default` calendar is created on first listing or first access to its path.
- Bytes from PUT are stored as received; GET and calendar-data serve them unchanged.
- New objects count against `KY_CALENDAR_MAX_OBJECTS_PER_USER` (507 when full).
- Calendars cannot be deleted over CalDAV (403).
- Query results keep objects whose filter match errors. Objects indexed unbounded (`LastEnd == nil`) skip `caldav.Match` and are always returned for a time-range query.
- DELETE `If-Match` reaches the backend through `WithIfMatch`; `*` means any current version.

## Verification
- `go test ./internal/api/ -run 'CalDAV|DAV'`

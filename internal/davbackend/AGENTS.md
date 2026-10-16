# internal/davbackend

## Purpose
Maps CalDAV (the go-webdav fork) onto the calendar store for one authenticated user per request.

## Ownership
Owns `Backend` (the `caldav.Backend` implementation), DAV path parsing and object-name rules, calendar property bounds, query matching policy and the mapping of store errors to DAV status codes. Authentication, the per-user path guard and the `caldav.Handler` mount belong to `internal/api`; storage and quotas to `internal/store`; iCalendar validation and indexing to `internal/calendar`.

## Local Contracts
- Paths: `/dav/<user-id>/calendars/<slug>/<name>`; anything outside the user's home is 403. Object names are opaque keys (clients use UIDs, including Outlook's `{...}` and base64): 1-255 bytes of valid UTF-8, no `/`, no control characters, not `.` or `..` (403 otherwise).
- The home lists the user's personal calendars and every group calendar a grant lets them read, at `_<calendar-id>/`. Roles come from `access.Resolve` over `Backend.Grants`, which `handleDAV` loads from `UserGrants` on every request, so membership changes apply to the next request.
- A group calendar the user cannot read answers 404 on every method. Reading needs reader, `PUT` and `DELETE` of objects need editor, and `PROPPATCH` needs manager (owner on personal calendars); otherwise 403. `ReadOnly` (no `write` in `current-user-privilege-set`) is set below editor and on a subscribed calendar (`access.CanEditEvents`), whose `PUT` and `DELETE` are 403 for everyone (`store.ErrReadOnly` too). `MKCALENDAR` at a `_` segment is 403. Deleting any calendar over CalDAV stays 403.
- A `default` personal calendar is created on first listing or on first access to its path (`ensureDefault`), only when the user owns no calendar.
- A group calendar is its own quota owner: only the per-owner object and byte limits apply to it, plus the instance byte cap. No calendar-count limit applies; admins create group calendars with no cap.
- `Write` is the one calendar-object write path, used by `PutCalendarObject` and the JSON event API (subscription refreshes run its checks through `Prepare` and write with `ReplaceSubscriptionObjects`): `ErrTooLarge` for an object over `calendar.MaxObjectSize` (before any other work), `caldav.ValidateCalendarObject` (`ErrInvalidResource`), `calendar.Inspect` (`ErrUnsupportedComponent`, `ErrInvalidData`), the calendar's component set (`store.Calendar.Holds`, `ErrUnsupportedComponent`; the set is immutable, so it is checked before the transaction), then `store.PutObject` with ETag preconditions, owner and instance quotas and the change row in one transaction. `EnsureDefault` creates a user's default calendar when they own none; CalDAV listing and `GET /api/calendars` both call it.
- Bytes from PUT are stored as received; GET, calendar-data and sync serve `Raw` and never decode. Only objects that go through `caldav.Match` are decoded.
- Per-user caps answer 507 (`store.ErrQuotaExceeded`): objects on create, calendars on MKCALENDAR and default creation, stored bytes on every PUT (total minus the replaced object plus the new body). The store enforces them atomically; the backend only passes the limits.
- Calendar properties are bounded at this boundary (`calendar.CheckProps`): `calendar-color` empty or `#RRGGBB`/`#RRGGBBAA`, `displayname` at most 255 bytes, `calendar-description` at most 4096 bytes. MKCALENDAR over a bound is 400; PROPPATCH is 403 for the whole request, nothing is stored.
- Calendars cannot be deleted over CalDAV (403).
- `supported-calendar-component-set` is the calendar's stored set. MKCALENDAR stores the requested set through `calendar.Components` (none is VEVENT; another name refuses with 403 and creates nothing). A PUT of a type outside the set is 409 `supported-calendar-component`.
- Query results keep objects whose filter match errors. A query whose VEVENT or VTODO comp-filter has a time range reads only that component's objects from the index (`queryRange`). In such a query, objects indexed unbounded (`LastEnd == nil`, not decoded) or for which `calendar.Recurring` holds on the decoded object (any spelling or folding of RRULE/RDATE, on any component) skip `caldav.Match` and are returned, so the index alone decides overlap and a series costs no expansion per query; other queries match every object.
- `SyncCalendar` refuses (`caldav.ErrInvalidSyncToken`, client resyncs) a token whose epoch or calendar ID differs from the calendar's, or whose seq is past it.
- DELETE `If-Match` reaches the backend through `WithIfMatch`. Any `If-Match` (including `*`) on a missing object is 412 for PUT and DELETE; `*` is passed to the store, which checks it inside the write transaction.

## Verification
- `go test ./internal/davbackend/`
- `go test ./internal/api/ -run 'CalDAV|DAV|GroupCalendar|MembershipRemoval|GroupSegment'`

## Child DOX Index
None.

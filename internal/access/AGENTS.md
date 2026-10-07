# internal/access

## Purpose
Pure authorization decisions for calendars: who is an administrator, and what role a user holds on one calendar.

## Ownership
Owns `AdminAppRole`, `IsAdmin`, `RoleValues` and the role resolver (`Resolve`, `Role` capabilities). Callers load users, calendars and grants; nothing here touches the store, HTTP or the clock.

## Local Contracts
- The KyCalendar administrator is the KyIdentity app role `kycalendar.admin`, exact match only. The global `role` claim, the webhook `role` field and values such as `admin` or another product's role never grant it.
- `RoleValues` accepts a string or a list of strings and `{"value": string}` objects (ID-token claim or SCIM attribute); other shapes contribute nothing.
- `Resolve(user, calendar, userGrants)` is the only place a calendar role is decided. Administrators get `None` everywhere. Personal calendars give their owner `Owner` and everyone else `None`, whatever grants exist. Group calendars take the highest valid grant naming that calendar. Unknown role strings and owner kinds resolve to `None`.
- Capabilities: `CanRead` is reader and above, `CanWrite` editor and above, `CanManage` manager and owner (rename, recolour, grants).

## Verification
- `go test ./internal/access/`

## Child DOX Index
None.

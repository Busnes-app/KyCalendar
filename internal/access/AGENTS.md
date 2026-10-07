# internal/access

## Purpose
Pure authorization decisions for calendars: who is an administrator, and what role a user holds on one calendar.

## Ownership
Owns `AdminAppRole`, `IsAdmin`, `RoleValues` and (Task 3) the role resolver. Callers load users, calendars and grants; nothing here touches the store, HTTP or the clock.

## Local Contracts
- The KyCalendar administrator is the KyIdentity app role `kycalendar.admin`, exact match only. The global `role` claim, the webhook `role` field and values such as `admin` or another product's role never grant it.
- `RoleValues` accepts a string or a list of strings and `{"value": string}` objects (ID-token claim or SCIM attribute); other shapes contribute nothing.

## Verification
- `go test ./internal/access/`

## Child DOX Index
None.

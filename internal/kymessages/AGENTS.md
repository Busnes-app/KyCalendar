# internal/kymessages

## Purpose
What KyCalendar sends KyMessages about video meetings, and its client. The wire contract is
`docs/SUITE-INTEGRATIONS.md` section B; KyMessages implements the other half.

## Local Contracts
- **Signing.** `ky-primitives/syncauth` v1 under the pair secret (`KYCALENDAR_KYMESSAGES_SECRET_FILE`,
  or KyIdentity's pair secret through `internal/api/kymessages_link.go`). Event types:
  `kycalendar.meeting.put` (`PUT /api/meetings/{id}`), `kycalendar.meeting.close`
  (`DELETE /api/meetings/{id}`). The caller passes a fresh event ID per attempt.
- **IDs** are `ValidID` (`[A-Za-z0-9_-]{16,64}`); anything else is refused before a request.
- **Outcomes:** 2xx success; 429, 5xx, the network and `kycalendar_off` are `ErrRetry`; a 401 on
  a `kyidentity` peer calls `Rotated` and is `ErrRetry`; anything else is `Refused{Code}` (a
  valid `[a-z0-9_]{1,64}` code, else `status_<n>`). At most 64 KiB of a reply is read.
- **`member_url`** passes `ValidURL` (https, no credentials, no spaces or controls, 2048 bytes)
  or the PUT is `Refused{bad_reply}`.
- `Title` makes any event title a valid room name.
- `Link` holds the live peer, swapped atomically; nil is `ErrNotConnected`.

## Verification
- `go test ./internal/kymessages/`

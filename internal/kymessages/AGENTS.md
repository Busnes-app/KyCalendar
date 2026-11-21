# internal/kymessages

## Purpose
What KyCalendar sends KyMessages about video meetings and chat, and its client. The wire
contracts are `docs/SUITE-INTEGRATIONS.md` sections B and H; KyMessages implements the other half.

## Local Contracts
- **Signing.** `ky-primitives/syncauth` v1 under the pair secret (`KYCALENDAR_KYMESSAGES_SECRET_FILE`,
  or KyIdentity's pair secret through `internal/api/kymessages_link.go`). Event types:
  `kycalendar.meeting.put` (`PUT /api/meetings/{id}`), `kycalendar.meeting.close`
  (`DELETE /api/meetings/{id}`), `kycalendar.chat.open` (`POST /api/suite/chat`, section H). The
  caller passes a fresh event ID per attempt.
- **Status** (`presence.go`, section P): `kycalendar.presence.busy`, `.me` and `.lookup`, all
  `POST /api/suite/presence/<calendar|me|lookup>`. `Busy` sends `[]` for no blocks, never null,
  and at most `MaxBusy` (200); `Lookup` at most `MaxLookup` (50) subjects, keeps only subjects
  asked for, reads an unknown state as `unknown` and drops `until` except on `in_a_meeting`
  and `out_of_office`. Bodies carry times and subjects only, never an event's text.
- **IDs** are `ValidID` (`[A-Za-z0-9_-]{16,64}`); anything else is refused before a request.
  Both signed bodies carry the ID as `"id"` too (`{"id","sub","issuer",...}`), because the
  signature does not cover the path.
- **Org:** in multi-tenant mode every body's `User` carries `org_id` (`Server.kymUser`,
  `store.PeerOrg`); single-tenant bodies omit it (tenancy spec D10).
- **Outcomes:** 2xx success; 429, 5xx, the network and `kycalendar_off` are `ErrRetry`; a 401 on
  a `kyidentity` peer calls `Rotated` and is `ErrRetry`; anything else is `Refused{Code}` (a
  valid `[a-z0-9_]{1,64}` code, else `status_<n>`). At most 64 KiB of a reply is read.
- **`member_url`** passes `ValidURL` (https, no credentials, no spaces or controls, 2048 bytes)
  or the PUT is `Refused{bad_reply}`.
- **Chat** answers pass `ValidChatURL` (a `ValidURL` that is exactly Element's user view,
  `https://<host>/#/user/@local:server`) or are `Refused{bad_reply}`; the caller checks the host against Element in the user's suite apps.
- `Title` makes any event title a valid room name.
- `Link` holds the live peer, swapped atomically; nil is `ErrNotConnected`.

## Verification
- `go test ./internal/kymessages/`

# internal/kypost

## Purpose
The one definition of what KyCalendar and KyPost send each other about invitations, and
KyCalendar's client for it. KyPost implements the other half; its contract doc lives in
kypost-server's `docs/` once written.

## Local Contracts
- **Signing.** Every request is signed with `ky-primitives/syncauth` (scheme v1) under one shared
  secret of at least 32 bytes (`KYCALENDAR_KYPOST_SECRET_FILE`). The event type names route and
  direction:
  - `kycalendar.itip.send`: KyCalendar to KyPost, `POST /api/calendar/itip/send`;
  - `kypost.itip.inbound`, `kypost.itip.respond`: KyPost to KyCalendar.

  A request is never accepted under another type, so one cannot be reflected back at its sender.
- **The event ID** is stable across retries of one message (the outbox dedupe key). A verified
  replay is acknowledged (`replayed`) and not acted on again, so a retry after a lost response is
  idempotent.
- **Every body** names the user as `{sub, issuer}`: their KyIdentity subject and issuer URL.
  Local accounts have none, so their external invitations fail `no_kypost_account`.
- **`Send` outcomes:**
  - 2xx: delivered.
  - 429, 5xx or a network error: `ErrRetry`.
  - Anything else: `Refused{Code}`. Only a 1-64 character `[a-z0-9_]` code from the peer is
    kept, else `status_<n>`.
  - At most 64 KiB of the reply is read.
- **`Verify`** reads at most 2 MiB, checks the event type, then the signature and replay window.
- **What KyPost must enforce** (its half of the contract):
  - **Sender:** mail only for a user who turned calendar sending on, only from their own address
    or a verified send-as alias.
  - **Recipients:** only the ATTENDEEs, or for a REPLY the ORGANIZER, of the object sent.
  - **Message:** build it from a fixed template, charged to the user's send quota.
  - **REPLY provenance:** send a REPLY only when the user's mailbox holds an invitation with that
    UID from that organizer. KyCalendar cannot prove the invitation was real; without the check, a
    user could aim replies at any address.
  - **Inbound:** forward only mail whose DKIM signature is aligned with its From domain and
    covers From.

## Verification
- `go test -race ./internal/kypost/ ./internal/api/ -run 'TestSend|TestVerify|TestKyPostRoutes'`

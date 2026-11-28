# Suite end-to-end harness

## Purpose
Drives the cross-app multi-tenant end-to-end run, the tenancy spec's last gate
(`docs/superpowers/specs/2026-11-24-multi-tenant-e2e-design.md`). Its own Go module (E1): it imports
no KyCalendar package and adds nothing to the product's `go.mod`, so it can move to its own
repository as a copy.

## Ownership
- `apps.lock`: `<app> <https://github.com/Busnes-app/<repo>.git> <full SHA>` per line. Pin only a
  commit whose `kye2e` gate has merged. Pinned: KyCalendar, KyIdentity, KyMessages, KyPost, KyNotes.
- `images/<app>.Dockerfile`: builds the pinned commit with `-tags kye2e` into the local image
  `kye2e/<app>:<sha>`. No app's own Dockerfile changes; the release image is the app's own
  Dockerfile at the same commit (`kye2e-release/<app>:<sha>`). Nothing is pushed. KyPost's
  release image is built with `INSTALL_OLLAMA=false`, and its kye2e image is that release image
  with only `kypost-server` replaced by the tagged build. A docker-container builder uses the
  local OCI layout because it cannot read the daemon's images. The harness reads the layout's
  image digest from Buildx metadata and uses it in both `FROM release@sha256:${RELEASE_SHA256}`
  and the named context. Scorecard recognizes the digest in the `FROM` reference. KyNotes commits
  `internal/web/dist`, so its kye2e image is a plain tagged build on the app's distroless runtime.
- `compose/compose.yml`: the topology. Every service sets `mem_limit` and an equal
  `memswap_limit`, together at most 5 GiB (dockerd's containers are outside any `kslot` scope);
  the driver refuses otherwise. Three random loopback ports are published for the driver and
  nothing else: `front` (Caddy), which serves `id`, `cal-x`, `cal-y`, `messages`, `post`, `notes` and
  `s3.e2e.test` over TLS from a CA the driver makes per run (apps trust it through
  `SSL_CERT_FILE`); KyIdentity's platform API; and the `mail` sink's API (Mailpit, STARTTLS,
  alias `smtp.e2e.test`). Compose mounts secrets root-owned and world-readable, so services that
  want private key files copy them at start. Caddy keeps a browser on one X replica for OIDC
  because the pinned KyCalendar signs nonces with a process-local key. PostgreSQL serves TLS
  from the run's CA with bounded keepalives, because KyPost's table mode requires
  `sslmode=verify-full`; the other apps connect without TLS as before. KyPost runs live in
  table-backed `multi` mode serving both cells (`e2e-1,e2e-2`) with `KYPOST_NATIVE_MAIL=false`
  (native receiving/SMTP waits on CoreDNS), `PAIRING_SECRET` bound to KyIdentity's
  `suite_webhook` secret, and `sso.json` stored in `kypost_volumes.documents`. `s3` is SeaweedFS
  (the image KyCalendar CI pins) behind `https://s3.e2e.test`; the driver creates the versioned
  `kynotes` bucket before KyNotes starts. KyNotes runs live in `multi` mode in cell `e2e-1`
  (`KYNOTES_REGION`: it serves one cell) on PostgreSQL and that bucket, its SSO client and
  directory poll fixed by the environment, native archives off.
- `smoke_test.go`: `start` brings one install up (images, KyIdentity's
  fresh PostgreSQL initialization, the bootstrap administrator's forced password change, the
  `kycalendar`, `kymessages`, `kypost` and `kynotes` OIDC clients, the `kynotes` bucket, `kycalendar`'s region map `e2e-1` → X,
  `e2e-2` → Y, and the `kypost` `suite_webhook` paired system linked to the `kypost` app
  registry record). `TestSmoke`, one row per app in `targets`: each pinned `kye2e` image starts
  in multi mode against the `.test` issuer (KyCalendar X as two replicas on one database in cell
  `e2e-1`, Y in `e2e-2`; KyIdentity; KyMessages serving both cells; KyPost serving both cells
  over HTTPS; KyNotes in `e2e-1`), the release image at the same commit refuses at the last gate, and the test image
  refuses a non-`.test` issuer (checked at startup for KyCalendar, KyIdentity, KyMessages, KyNotes, and
  at runtime via `kypost_volumes.documents` `sso.json` for KyPost).
- `kyidentity_test.go`: the driver's browser, one cookie jar per person over https through
  `front`, with each app's CSRF double submit, KyIdentity sign-in and step-up (with bounded
  retry on KyIdentity's step-up rate limit).
- `kynotes_test.go`: KyNotes helpers. `notesWrite` makes a member's notebook, page, S3 attachment,
  sealed and page share links and a paired device through KyNotes' API. KyNotes encrypts in the
  client, so the driver stores the notebook's key envelope with SQL, as KyNotes' own tests do
  (`keyForTest`); a documented shortcut, like S7's. `notesOrgRows` reads every `org_id` column.
- `scenarios_test.go`: `TestScenarios`, covering S1–S3, S6, and S7 of the design across all
  five apps. Runs today: platform mail and the suite mail domain set over the admin API, two
  platform tokens from KyIdentity's CLI (billing `orgs:*`, portal `members:*`), orgs A, B
  (`e2e-1`) and C (`e2e-2`) with holders a1, b1, c1 and member a2, each with a mailbox address
  on `mail.e2e.test`; activation from the mailed link and password sign-in (E8), with one cookie
  jar per person reused for their app sign-ins. The test clients permit all active users of an
  entitled org. a1 is refused at X with `access_denied` before any app is enabled.
  The scenario enables `kycalendar`, `kymessages`, `kypost` and `kynotes` with `PATCH /platform/v1/orgs/{id}`,
  resyncs the `kypost` paired system so KyIdentity delivers directory webhooks to KyPost, and
  exercises:
  - **S1–S3**: two orgs and cell routing across KyCalendar X/Y, KyMessages, and KyPost; S3's
    direct same-org CalDAV invitation and meeting link (contract B, KyMessages #58) under
    `meeting_pair`.
  - **KyNotes** (S1, S2, isolation, S6, S7): a1, a2 and b1 sign in at KyNotes and c1 (cell `e2e-2`)
    is refused `org_not_active`; KyNotes' storage reports for A and B reach KyIdentity's
    `org_storage` and grow with B's writes, and none is made for C. a1 and b1 each make data;
    neither's session, nor b1's device, reads the other org's page, and b1's notebook list does
    not hold A's. S6: while B is suspended, b1's session and device answer 401 and both links
    404, a1's keep working; after resume the links serve again, while the session and the SSO-derived
    device stay ended. S7: KyNotes purges B: after its purge fence is moved past the late-PUT
    window, no `org_id` column names B but `erased_subjects` (b1's subject, kept so a restore
    re-erases), no object key holds `orgs/<B>/` (bare or after `cells/<cell>/`), KyIdentity holds no usage for B from KyNotes,
    b1's account is gone, B's credentials are refused, and A's notebook and objects are intact.
  - **S6 (org suspension & resume)**: suspends org B via the platform API (`PATCH /v1/orgs/{id}`
    `{"status":"suspended"}`), verifies KyIdentity refuses b1 (`org_suspended`), KyCalendar X
    revokes b1's session and refuses b1's CalDAV app password, KyMessages revokes b1's session,
    and KyPost revokes b1's session, while org A stays active; resumes org B and verifies b1's
    CalDAV app password works again while revoked sessions require fresh sign-in across all apps.
  - **S7 (org deletion & purge)**: b1 starts account-holder deletion via `POST /api/user/erase`
    (`deletion_scheduled`), checks `export_only` sign-in on KyCalendar X and KyPost (export
    routes allowed, normal mutations refused), advances `orgs.delete_after` in KyIdentity's DB
    to trigger the scheduled erasure tick (documented shortcut — E5), and waits for KyIdentity
    to mark org B `deleted` and KyCalendar X, KyMessages, and KyPost to purge org B's rows
    (verifying KyPost removes `user_orgs` while retaining its opaque `subject_orgs` tombstone
    and orgs A and C stay intact).
  Checks the run cannot make without CoreDNS, S3/SeaweedFS, or KyRecovery are logged `NOT RUN:`.

## Local Contracts
- Nothing here creates, suspends or deletes an org, or enables an app, by any path but
  KyIdentity's platform API (E5), except S7's `orgs.delete_after` timestamp fast-forward to avoid
  waiting out the real deletion grace window. The driver's other SQL writes are two KyNotes time
  and client shortcuts, both named in `kynotes_test.go` or S7: the notebook key envelope a client
  would make, and moving KyNotes' purge fence back past its 20-minute late-PUT window.
- The driver generates secrets per run. Compose sets `PUSH_RELAY_URL` and `APNS_RELAY_URL` to empty for each test app.
- Keep test files discoverable by default so CodeQL finds the Go package. `enable_test.go`
  opts in with `kye2e`. Every Docker-backed test calls `start`, which skips before setup by default.
- Runs only from `run.sh`: on Major `E2E_BUILDER=<memory-capped buildx builder> kslot e2e/suite/run.sh`,
  and in `.github/workflows/suite-e2e.yml` (manual and nightly, never a required check).

## Verification
- `e2e/suite/run.sh` (Docker with compose and buildx; fetches the pinned commits from GitHub).
- `scripts/check-test-build-tag.sh` checks untagged package discovery and vets the tagged module on every pull request.

## Child DOX Index
None.

# Suite end-to-end harness

## Purpose
Drives the cross-app multi-tenant end-to-end run, the tenancy spec's last gate
(`docs/superpowers/specs/2026-11-24-multi-tenant-e2e-design.md`). Its own Go module (E1): it imports
no KyCalendar package and adds nothing to the product's `go.mod`, so it can move to its own
repository as a copy.

## Ownership
- `apps.lock`: `<app> <https://github.com/Busnes-app/<repo>.git> <full SHA>` per line. Pin only a
  commit whose `kye2e` gate has merged. Pinned: KyCalendar, KyIdentity, KyMessages. Not pinned
  yet: KyPost (no gate).
- `images/<app>.Dockerfile`: builds the pinned commit with `-tags kye2e` into the local image
  `kye2e/<app>:<sha>`. No app's own Dockerfile changes; the release image is the app's own
  Dockerfile at the same commit (`kye2e-release/<app>:<sha>`). Nothing is pushed.
- `compose/compose.yml`: the topology. Every service sets `mem_limit` and an equal
  `memswap_limit`, together at most 5 GiB (dockerd's containers are outside any `kslot` scope);
  the driver refuses otherwise. Three random loopback ports are published for the driver and
  nothing else: `front` (Caddy), which serves `id`, `cal-x`, `cal-y` and `messages.e2e.test` over
  TLS from a CA the driver makes per run (apps trust it through `SSL_CERT_FILE`); KyIdentity's
  platform API; and the `mail` sink's API (Mailpit, STARTTLS, alias `smtp.e2e.test`). Compose mounts secrets root-owned
  and world-readable, so services that want private key files copy them at start. Caddy keeps a
  browser on one X replica for OIDC because the pinned KyCalendar signs nonces with a process-local key.
- `smoke_test.go` (`//go:build kye2e`): `start` brings one install up (images, KyIdentity's
  fresh PostgreSQL initialization, the bootstrap administrator's forced password change, the `kycalendar` and `kymessages` OIDC
  clients and `kycalendar`'s region map `e2e-1` → X, `e2e-2` → Y). `TestSmoke`, one row per app in
  `targets`: each pinned `kye2e` image starts in multi mode against the `.test` issuer
  (KyCalendar X as two replicas on one database in cell `e2e-1`, Y in `e2e-2`; KyIdentity;
  KyMessages serving both cells), the release image at the same commit refuses at the last
  gate, and the test image refuses a non-`.test` issuer.
- `kyidentity_test.go`: the driver's browser, one cookie jar per person over https through
  `front`, with each app's CSRF double submit, KyIdentity sign-in and step-up.
- `scenarios_test.go`: `TestScenarios`, S1-S3 of the design. Runs today: platform mail and the
  suite mail domain set over the admin API, two platform tokens from KyIdentity's CLI (billing
  `orgs:*`, portal `members:*`), orgs A, B (`e2e-1`) and C (`e2e-2`) with holders a1, b1, c1 and
  member a2, each with a mailbox address on `mail.e2e.test`; activation from the mailed link and
  password sign-in (E8), with one cookie jar per person reused for their app sign-ins. The test
  clients permit all active users of an entitled org. a1 is refused at X with `access_denied`
  before any app is enabled.
  The scenario enables both apps with `PATCH /platform/v1/orgs/{id}` and exercises the app checks
  of S1–S3. Checks the run cannot make are logged `NOT RUN:`. KyPost rows are not run (open question 4).

## Local Contracts
- Nothing here creates, suspends or deletes an org, or enables an app, by any path but
  KyIdentity's platform API (E5).
- The driver generates secrets per run. Compose sets `PUSH_RELAY_URL` and `APNS_RELAY_URL` to empty for each test app.
- Runs only from `run.sh`: on Major `E2E_BUILDER=<memory-capped buildx builder> kslot e2e/suite/run.sh`,
  and in `.github/workflows/suite-e2e.yml` (manual and nightly, never a required check).

## Verification
- `e2e/suite/run.sh` (Docker with compose and buildx; fetches the pinned commits from GitHub).
- `scripts/check-test-build-tag.sh` vets the module with the tag on every pull request.

## Child DOX Index
None.

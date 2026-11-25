# Cross-app multi-tenant end-to-end test, design

Date: 2026-11-24. Status: proposed design, docs only. Nothing here is built, and no refusal is
lifted by it. It designs the last gate of the tenancy spec's step 6
(`2026-11-20-tenancy-design.md`, Phased build plan, step 6): "An end-to-end run with KyIdentity in
multi-tenant mode, KyPost and KyMessages: two orgs, an invitation within one delivered directly and
one across them by mail, and suspension and deletion of one."

Code references are to `origin/<default>` as fetched on 2026-11-24:

| Repo | Branch | Commit |
|---|---|---|
| KyCalendar | `main` | `4858d5b` |
| KyIdentity-server | `master` | `21f0eff` |
| KyPost-Server | `main` | `f47371b9` |
| KyMessages-Native | `main` | `a62234e` |

"Unverified" marks a claim this design did not check in code.

## Summary

- **The harness** lives in KyCalendar, under `e2e/suite/`, as its own Go module, so the product
  module gains no dependency. It builds each app from source at a pinned commit with the `kye2e`
  build tag. It runs on Major under `kslot` and in a manual and nightly GitHub workflow. It is never
  a required check on a pull request.
- **Startup refusals stay closed in every shipped binary.** Each app moves its refusal behind one
  constant whose value comes from a pair of build-tagged files. Only `go build -tags kye2e` opens
  it. No environment variable, flag, config value or linker flag can open it. A `kye2e` binary
  also refuses any issuer outside the reserved `.test` domain. Each app's release workflow reads
  the binary's build info and fails if `kye2e` is present.
- **The scenarios** use three orgs in two cells, each covering the spec's wording: A and B in cell
  `e2e-1` and C in cell `e2e-2`. They cover direct delivery inside A, mail from A to B and from A to
  C, the storage pool, suspension of B refused on every credential type, deletion and purge of B,
  and a restore of a backup kept from before the deletion, which must replay an erasure and the
  purge before anything serves.
- **It cannot run yet.** KyIdentity has no platform provisioning API (its PR 4). KyPost's PR 7
  waits on MH4, MH6, MH7 and MH9. All four apps need the gate change below. See "What blocks
  running it".

## Decisions

| # | Decision | Default | Why |
|---|---|---|---|
| E1 | Where the harness lives | KyCalendar `e2e/suite/`, a separate Go module (`e2e/suite/go.mod`) | KyCalendar's spec is the one that names this run as its gate. A separate module keeps the product's `go.mod`, `make ci` and vulnerability scan free of the harness's dependencies. If a second app makes this run its own gate (KyPost PR 7 asks for "a native-mail harness run against KyIdentity in multi mode", KyPost tenancy spec, Phased build plan, PR 7), move it to a new `Busnes-app/ky-suite-e2e` repository. Moving it is a copy, because the module imports none of KyCalendar's packages. |
| E2 | How it gets each app | Source builds at the commits pinned in `e2e/suite/apps.lock` (repo, full SHA), with `-tags kye2e`, into local images named `kye2e/<app>:<sha>` | `:latest` is a release image and by design cannot carry the tag (E4). A pinned SHA makes a result reproducible and tells a reader exactly which code passed. All four repositories are public, so a checkout needs no token. |
| E3 | Where it runs | Major under one `kslot` slot, and `.github/workflows/suite-e2e.yml` (`workflow_dispatch` and nightly). Not on K hosts. | It needs Docker, and `kjob` runs one `golang:1.27` container with no Docker. `kslot`'s 6 GB scope does not cover containers that `dockerd` starts, so every container gets its own `--memory` limit, adding up to at most 5 GB (budget below). It is not a required pull-request check: it builds four apps and runs for minutes, and a failure in one app must not block another app's unrelated pull request. |
| E4 | How a refusal is lifted for the test | The `kye2e` build tag selects a constant (Mechanism) | It is the narrowest lever: it exists only at compile time, it is recorded in the binary's build info, and nothing that reaches a running binary can change it. |
| E5 | How orgs are created, suspended and deleted | Through KyIdentity's platform provisioning API (its PR 4), called by the harness with a platform token | That is the production path. Seeding KyIdentity's database with SQL would skip its triggers, revisions and outbox, so the run would prove nothing about provisioning. KyIdentity's own test constructors panic outside a test binary (`internal/store/orgstore.go:80-83`, `testing.Testing()`). |
| E6 | Mail between hosted mailboxes | Real SMTP through KyPost's outbound path and its MX host, with an in-harness DNS zone (`e2e.test`) holding MX, SPF and DKIM records | KyCalendar takes an inbound invitation only when KyPost marks it `dkim_aligned` (tenancy spec, Sharing, invitations and free/busy; open question 6). Delivering mail inside KyPost without SMTP would not test that. |
| E7 | Restore | KyCalendar's own `restore` from the bucket copy, with a KyRecovery-server under a synthetic ceremony whose test shares the harness holds | `docs/RESTORE.md:161` allows shares on stdin "only for a rehearsal with synthetic test shares". The run uses its own ceremony, never real cards. |
| E8 | Member sign-in in the driver | The driver signs members in through KyIdentity's real OIDC flow over HTTP. | Unverified: whether KyIdentity lets a customer member sign in without a passkey in multi-tenant mode. If it does not, the driver carries a software WebAuthn authenticator. Settle this when KyIdentity's PR 4 lands. |

## Topology

One run, one Docker network, nothing published to the host except the driver's port:

| Service | Instances | Cells | Notes |
|---|---|---|---|
| PostgreSQL 17 (`postgres:17-alpine`) | 1 server | — | One database per deployment: `kyidentity`, `kycal_x`, `kycal_y`, `kypost`, `kymessages`, `kyrecovery` (if it uses PostgreSQL; unverified). A unique container name, removed by a trap. |
| S3 (the seaweedfs image KyCalendar CI pins) | 1 | — | One versioned bucket per app, so the run also proves cell prefixes keep deployments apart |
| KyIdentity | 1 | issuer for every cell | Issuer `https://id.e2e.test`. Region map for `kycalendar`: `e2e-1` → X, `e2e-2` → Y (`PUT /api/admin/clients/{id}/regions`, `internal/api/server.go:339`) |
| KyCalendar X | 2 replicas, one database | `KY_CELLS=e2e-1` | Two replicas, because hosted runs as several (D2) |
| KyCalendar Y | 1 | `KY_CELLS=e2e-2` | A second cell on its own database, the same bucket |
| KyPost | api, daemon, MX host | both cells (cells support is MH9, not built) | Built from KyPost's `docker-compose.hosted.yml` and `docker-compose.mx.yml` |
| KyMessages | 1 | `KY_CELLS=e2e-1,e2e-2` | C2: one deployment may serve several cells |
| KyRecovery | 1 | — | Synthetic ceremony (E7) |
| CoreDNS | 1 | — | The `e2e.test` zone: A, MX, SPF and DKIM records |
| Driver | 1 | — | `go test -tags kye2e ./...` in `e2e/suite`, with a `-timeout` |

Memory budget: 5 GB across the containers. The first run measures the real figures and records
them here.

## Mechanism: lifting a refusal only in a test build

Each app replaces its unconditional refusal with a check of one constant, defined twice:

```go
// gate_release.go
//go:build !kye2e

package config

const multiTenantBuild = false
```

```go
// gate_kye2e.go
//go:build kye2e

package config

const multiTenantBuild = true
```

The refusal becomes `if mode == multi && !multiTenantBuild { refuse }`. Every other requirement each
app already checks (PostgreSQL, the staff org, `KY_CELLS`, object storage, keys) still runs before
it, in both builds.

Measured on Major (Go 1.27.3, 2026-11-24) with a two-file probe of this shape, built
`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"` as the app Dockerfiles do:

- The release build prints `false`. `go version -m` lists no `-tags` entry.
- The `-tags kye2e` build prints `true`. `go version -m` lists `build -tags=kye2e`. Stripping
  (`-s -w`) and `-trimpath` do not remove the build info.
- `-ldflags "-X main.multiTenantBuild=true"` on the release build still prints `false`, because
  `-X` sets only string variables.

Also, a `kye2e` binary refuses to start unless its issuer's host ends in `.test`, the reserved
top-level domain of RFC 2606 (each app reads its issuer from configuration already). This
narrows what the binary will do and lifts nothing, so it is not the kind of value the rule
forbids. A test image deployed by mistake against the production issuer cannot serve.

### What each app changes

| App | Refusal today | Change |
|---|---|---|
| KyCalendar | `var multiTenantStartup = false` (`internal/config/config.go:57`), checked at `:347-349` (`ErrMultiTenantGated`, `:60`). Tests flip it through `OpenMultiTenantGateForTest` (`internal/config/export_test.go:4-7`). | `var multiTenantStartup = multiTenantBuild`. The variable stays so the existing test helper keeps working, and it is a `bool`, so `-X` cannot set it. `TestMultiTenantStartupRequirements` (`internal/config/config_test.go:501`) already asserts the refusal in an untagged build. |
| KyIdentity | `case "multi":` returns `KYIDENTITY_TENANCY=multi is not available in this release` unconditionally (`internal/config/config.go:323-335`, the return at `:332`), after its own `KYIDENTITY_REPLICATED` check (`:329-331`). Tests set `Config.Tenancy = "multi"` directly (`internal/api/cross_org_test.go:353`). | Return the error only when `!multiTenantBuild`. |
| KyPost | `CheckEnv` appends `ErrMultiTenancyUnavailable` for `multi` (`backend/internal/config/config.go:140-144`, error at `backend/internal/config/tenancy.go:44-46`). `BootstrapAdmin` returns the same error for `multi` (`backend/internal/app/bootstrap.go:36-41`), and so do the backup commands (`backend/internal/app/backup.go:65-71`). | Gate `CheckEnv` on `!multiTenantBuild`. `BootstrapAdmin` in multi mode returns `nil` in a `kye2e` build, because K6 means there is no bootstrap administrator, so there is nothing to bootstrap. The backup commands keep refusing: the run does not use KyPost backups. |
| KyMessages | `LoadFromEnv` refuses `multi` (`internal/config/config.go:272-284`, error at `:280`). `LoadForMaintenance` (`:286-290`) bypasses it for `invalidate-grants` only (`cmd/server/main.go:463`); server startup uses `LoadFromEnv` (`cmd/server/main.go:87`). | Call `loadFromEnv(mode)` for `multi` when `multiTenantBuild`. The comment at `:278-279` requires that lifting also refuse multi mode without `KY_ATTACHMENTS_S3_*`, so that check comes in the same PR and runs in both builds. |

Each app adds, in the same PR:

1. **A release check.** `release-image.yml`, and the `docker` job in `ci.yml` that builds `:latest`,
   copy the binary out of the image and fail if `go version -m` prints `-tags=` containing
   `kye2e`. They also start the image with a complete multi-tenant configuration and expect the
   refusal message. KyCalendar's `docker` job (`.github/workflows/ci.yml:342-360`) builds the image
   that becomes `:<sha>` and `:latest`, and `release-image.yml` builds `:X.Y.Z`. Both run the check.
2. **A source check** in CI: `kye2e` appears only in the two gate files, in `e2e/`, and in these
   checks. It never appears in `Dockerfile`, `Makefile`, `docker-compose*.yml` or a release
   workflow. A `TAGS` build argument in a Dockerfile would let a release build pass the tag
   without editing a gate file, so the check also fails on any `-tags` in those files.
3. **A test** that the untagged binary refuses `multi` with the release message. KyCalendar's
   test exists already; the others add one.

The harness builds the `kye2e` images from its own Dockerfiles under `e2e/suite/images/`. No
app's Dockerfile changes.

## Scenarios

Orgs: **A** (`e2e-1`, members a1 and a2), **B** (`e2e-1`, b1), **C** (`e2e-2`, c1). Each has a
mailbox address on the suite domain `mail.e2e.test` and kycalendar, kypost and kymessages enabled
with quotas. a1, b1 and c1 are their orgs' account holders. The staff org is the same in every
app. Every check below runs per org on every app, and the run fails on the first unmet one with
the app, org and credential named.

The scenarios share one install and run in order, so a step may only use state an earlier step
made and no step between destroyed. Each row says what it creates for later rows and what it puts
back. **K** is the backup S6b takes and S8 restores.

| # | Spec wording (step 6) | Steps | Checks: org gate, cells (C5), storage pool |
|---|---|---|---|
| S1 | "two orgs" | The platform API creates A, B and C with a1, b1 and c1 as holders, then a2, the mailbox addresses, and the app enablement. Each app's mirror poll runs. | KyCalendar X serves A and B and refuses C. Y serves C only (`orgServes`, `internal/store/orgs.go:309-313`). KyMessages serves all three. Each app's first storage report reaches KyIdentity (`POST /api/suite/storage`, KyIdentity `internal/api/server.go:232`). |
| S2 | (cells, C5) | c1 tries every way in at X: OIDC sign-in, KyPost inbound naming c1, booking page of a c1 slug made on Y. a1 tries the same at Y. | Each is refused as an org that does not serve, the same answer an unknown org gets. The same steps succeed at the home cell. Backup copies land under `cells/e2e-1/platform/backups/` for X and `cells/e2e-2/…` for Y (`internal/backup/objects.go:23`). KyMessages attachments land under `cells/<home_region>/orgs/<tenant_id>/` (KyMessages 2026-11-23 spec §6). Neither deployment lists the other's keys. |
| S3 | "an invitation within one delivered directly" | a1 PUTs an event over CalDAV at X with a2 as attendee and a KyMessages meeting link. | a2's calendar holds the event without any mail: the SMTP sink and KyPost's send log show nothing for a2 (D5; `CalendarUser`, `internal/store/scheduling.go:168`). a1's free/busy shows a2's busy time. KyMessages' meeting has only org A members (`member_subs`). Single-tenant golden answers are not part of this run; they stay in each app's own CI. |
| S4 | "one across them by mail" | a1 invites b1 (same cell), then c1 (other cell). b1 and c1 accept. | KyCalendar sends `kycalendar.itip.send` with `org_id` A. KyPost mails it over SMTP. The MX host takes it, DKIM aligned (E6). KyPost forwards `kypost.itip.inbound` with the recipient's `org_id` to the recipient's cell: X for b1, Y for c1, through `suitedir.Peer.URLFor(home_region)`. Nothing is written into b1's or c1's calendar until they answer (`pending`, `internal/davbackend/external.go:103-163`). After they answer, a1's event shows both attendees as `ACCEPTED`. a1's free/busy lookup of b1 gets the answer a stranger gets. |
| S5 | (storage pool, D14) | Staff lower B's pool below its usage, then b1 writes a growing event at X and uploads an attachment at KyMessages. Then b1 deletes an event. Last, staff put B's pool back to its S1 size and wait for each app's next grant. | While lowered, both growing writes are refused with each app's pool answer, and the delete passes. KyIdentity's view of B's usage is the sum of the apps' reports. After the reset, b1's growing write passes again, so S6 and S7 start from a pool that admits writes. |
| S6 | "suspension … of one" | First b1 makes, and checks once, every credential this row tests: a KyCalendar session, DAV app password and MCP token, a booking page, a KyPost session, device token and CardDAV password, and a KyMessages session and guest link. Then staff suspend B and the run waits one poll interval plus margin. During the suspension, a1 sends b1 a new invitation. Then staff resume B. | While B is suspended, every b1 credential is refused at its next request: KyIdentity sign-in (`org_suspended`); KyCalendar session cookie, DAV app password, MCP token, booking page (`404`), KyPost inbound (`404 no_user`); KyPost session, device token, CardDAV password, IMAP (unverified as a separate path), and mail to b1 is answered with a temporary `451` (KyPost tenancy spec, Lifecycle), so it is retried, not lost; KyMessages session, WebSocket, guest link. A's credentials keep working throughout. After resume: b1's DAV app password works again, sessions do not come back, and a1's invitation, refused with `451` during the suspension, arrives on KyPost's retry and waits `pending` in b1's account. |
| S6b | (backup for S8) | b1 signs in again. Then X runs a backup, and the driver keeps that copy as **K**: it records K's object key, digest and `capsule_id`, and it saves the driver-side counts S8 compares (rows naming B in each org-classified table; a2's user row and calendars). | K holds A with a1 and a2, and B active with b1's calendars, events, booking page and the accepted S4 invitation. Later backups never replace K: `KYCALENDAR_BACKUP_KEEP` is set high enough that pruning cannot reach it, and the driver also copies K out of the bucket. |
| S7 | "deletion of one" | b1, as B's holder, uses Delete everything. B goes to `deletion_scheduled`, then past `delete_after` to `deleted` (KyIdentity `internal/store/erasure.go:215,499`). | During `deletion_scheduled`, only export-only sign-in works, on each app that serves it. After `deleted`, each app purges B: no row in an org-classified table names B (the driver reads each database through a read-only role and checks every `org_id` column from `information_schema`), and no object is left under B's prefix in any bucket. B's mirror row stays as a tombstone, and a late token or peer message naming B is refused. B's pool report falls to zero. A and C are unchanged. K still holds B: backups keep a deleted org until they age out (tenancy spec, Residual risks), and S8 depends on that. |
| S8 | (restore replaying erasures and the purge) | After S7, a2 uses Delete everything for their own account (`user.erased`), which K predates. Then the driver stops both X replicas and restores **K** (E7), never a backup taken after S6b. It starts one X replica with its org-mirror poll held off, then lets the poll run. Then it starts the second replica. KyIdentity is not restored, so it still says B is `deleted` and a2 is erased. | (1) **The snapshot brings them back.** Before the first poll, X's database again holds B's rows (the S6b counts) and a2's account and calendars. (2) **Nothing serves yet.** At that moment every customer org at X is `stale` (`ResetAfterRestore`, `internal/store/sqlstore.go:122-129`), and the restore has revoked every session and app password (the reset's audit row lists them, `:151`). Each of a1's, a2's and b1's credentials, and b1's booking page, is refused. (3) **Erasure is replayed first.** The restore sets `tenancy_erased_pending` (`internal/store/sqlstore.go:133-136`, `internal/store/orgs.go:228-231`). The first poll deletes a2 again from `GET /api/suite/erased` (`internal/api/suite_erased.go:114-139`; KyIdentity `internal/api/server.go:231`) before it applies any org status (`internal/api/org_mirror.go:94-99`). The driver probes a1 throughout, and a1's first accepted sign-in comes only after a2's row is gone. (4) **The purge is replayed.** That poll writes B `deleted`. B is never served at any point after the restore, and the next `org-purge` run removes B's rows again, leaving the same S7 result: no row names B, and the tombstone stays. (5) **A is otherwise intact.** a1's account and calendars survive, and a1 signs in again (sessions do not survive a restore). C on Y is untouched. |

Order: S1–S5 build the state S6 needs, and S5 puts B's pool back. S6b keeps K after B's last
change and before S7 deletes B. a2's erasure in S8 comes after K, so K holds what S8 must see
replayed. Nothing after S7 uses B except the restore of K.

## What blocks running it

| App | Must land first | Status on 2026-11-24 |
|---|---|---|
| KyIdentity | **PR 4, platform provisioning API**: org create, members, mailbox address, suspend, resume, delete (KyIdentity tenancy spec, Phased build plan, step 4) | Not built: no `platform/v1/orgs` route on `master`. Orgs exist only through test constructors (`internal/store/orgstore.go:73-105`). |
| KyIdentity | **`org_apps` written by the platform API** (step 6, Entitlements), so `/api/suite/orgs` lists the orgs with apps enabled | Not built: only `EnableOrgAppForTest` (`internal/store/orgstore.go:96-105`) writes it. |
| KyIdentity | The `kye2e` gate (Mechanism) | Not built |
| KyCalendar | The `kye2e` gate, and open question 12 (handover slug collisions), which the tenancy spec keeps as a hosted acceptance gate | Not built; question 12 is open |
| KyPost | **PR 7, hosted mail** (allocation from the mailbox address, M's masked changes, per-org limits). It waits on MH1–MH7, RX 1–5 and MH9 (KyPost tenancy spec, amendments of 2026-11-22). | RX 1–5 built (receiving tier spec status). MH1, MH2 and MH3 built. MH4 and MH6 partly built, with the rest in open PRs #725 and #726. MH5 built (#722). MH7 not built. MH9 (cells) not built: no `KYPOST_REGION` or `KY_CELLS` in code. |
| KyPost | An answer to KyCalendar's open question 6: does KyPost mark mail between two hosted mailboxes `dkim_aligned`? | Unverified. S4 is the test that answers it. |
| KyPost | The `kye2e` gate, including the `BootstrapAdmin` change | Not built |
| KyMessages | The `kye2e` gate with the `KY_ATTACHMENTS_S3_*` requirement (`internal/config/config.go:278-279`) | Not built. Its tenancy build PRs #35, #38–#42 and #44–#51 are merged. Whether they cover every row of its spec's §12 is unverified, and the spec still lists "hosted qualification" as gated. |
| KyRecovery | A synthetic ceremony that a harness can run without a person | Unverified |
| Harness | `e2e/suite/` itself: images, `apps.lock`, compose file, driver, workflow | Not built. It can start once KyIdentity PR 4 lands, by running S1–S3 first. |

Order: the four gate PRs are independent and small, and each can merge with its release check while
nothing yet builds with the tag. KyIdentity PR 4 unblocks S1, S2, S3, S5, S6 and S7 (S7 for
KyCalendar and KyMessages). S4 and the KyPost rows of S6 and S7 wait for KyPost PR 7. KyCalendar's
`multiTenantStartup` stays `false` in release builds until the whole run passes and the tenancy spec
says so.

## Threat model

| Threat | Defence |
|---|---|
| An operator, a bad deploy or a typo starts a release image in multi mode | The release binary compiles the constant `false`. No environment variable, flag or config value reaches it. A complete multi config ends in the refusal message, as each app's release-check test proves. |
| A release build accidentally built with the tag | The release workflow reads the binary's build info and fails on `-tags=kye2e` before it pushes `:X.Y.Z` or `:latest`. The source check refuses `-tags` in `Dockerfile`, `Makefile`, compose files and release workflows, so the tag can only enter through an edit that the check flags. |
| Linker flags (`-ldflags -X`) used to flip the gate | `-X` sets only string variables, and the gate is a `bool` constant or a variable initialised from one (measured above). |
| A `kye2e` image pushed where production pulls from | The harness tags images `kye2e/<app>:<sha>` locally and pushes nothing. The workflow has `packages: read` at most. A `kye2e` binary refuses an issuer outside `.test`, so even if deployed it cannot take tokens from the production issuer. Its `go version -m` output still names the tag. |
| Someone edits `gate_release.go` to `true` | That is a code change to a two-line file in a reviewed PR. Each app's existing refusal test fails in the untagged CI build, and the release check fails too. |
| Test data or test keys reach production | The run creates its own databases, bucket, DNS zone, ceremony and keys inside one Docker network, and a trap removes them. Nothing is shared with a production host. |
| A test-only code path, other than the gate, grows behind the tag | The source check limits `kye2e` to the gate files. The tag opens startup and nothing else, so the run exercises the same code a release would run. |

Residual: a release check proves only that the tag is absent. It does not prove the multi-tenant
code is correct. That is what this run is for, and why it must pass before any release build opens
the gate.

## Open questions, with defaults

1. **Harness location** (E1). Default: KyCalendar `e2e/suite/`, moved to `Busnes-app/ky-suite-e2e`
   when a second app uses it as a gate. The alternative is a new repository from the start, which
   needs the owner to create it.
2. **Required check?** Default: no, the workflow is manual and nightly. The root thread runs it
   before lifting any app's gate.
3. **Member sign-in in the driver** (E8). Default: password, then the second factor KyIdentity
   requires, done over HTTP. A software WebAuthn authenticator if passkeys are mandatory for
   members.
4. **KyPost cells before MH9.** Default: wait for MH9. If KyPost PR 7 lands first, S2 and S4 run
   with KyPost as one deployment serving both cells and the KyPost rows of S2 are marked not run.
5. **LiveKit and media.** Default: out of scope. KyMessages' calls and media have their own hosted
   qualification. This run checks KyMessages' org and cell gates, meetings and attachments only.

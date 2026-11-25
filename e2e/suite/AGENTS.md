# Suite end-to-end harness

## Purpose
Drives the cross-app multi-tenant end-to-end run, the tenancy spec's last gate
(`docs/superpowers/specs/2026-11-24-multi-tenant-e2e-design.md`). Its own Go module (E1): it imports
no KyCalendar package and adds nothing to the product's `go.mod`, so it can move to its own
repository as a copy.

## Ownership
- `apps.lock`: `<app> <https://github.com/Busnes-app/<repo>.git> <full SHA>` per line. Pin only a
  commit whose `kye2e` gate has merged. Not pinned yet: KyIdentity, KyPost, KyMessages (no gate).
- `images/<app>.Dockerfile`: builds the pinned commit with `-tags kye2e` into the local image
  `kye2e/<app>:<sha>`. No app's own Dockerfile changes; the release image is the app's own
  Dockerfile at the same commit (`kye2e-release/<app>:<sha>`). Nothing is pushed.
- `compose/compose.yml`: the topology. Every service sets `mem_limit` and an equal
  `memswap_limit`, together at most 5 GiB (dockerd's containers are outside any `kslot` scope);
  the driver refuses otherwise. Nothing is published to the host.
- `smoke_test.go` (`//go:build kye2e`): the driver. Today only `TestSmoke`: each pinned `kye2e`
  image starts in multi mode against the `.test` issuer (KyCalendar X as two replicas on one
  database in cell `e2e-1`, Y in `e2e-2`, every `/readyz` ready), the release image at the same
  commit refuses at the last gate, and the test image refuses a non-`.test` issuer.

## Local Contracts
- Scenarios S1+ wait for KyIdentity's platform API (its PR 4). Nothing here creates, suspends or
  deletes an org by any other path (E5).
- Secrets are random per run, set by the driver; none is committed.
- Runs only from `run.sh`: on Major `E2E_BUILDER=<memory-capped buildx builder> kslot e2e/suite/run.sh`,
  and in `.github/workflows/suite-e2e.yml` (manual and nightly, never a required check).

## Verification
- `e2e/suite/run.sh` (Docker with compose and buildx; fetches the pinned commits from GitHub).
- `scripts/check-test-build-tag.sh` vets the module with the tag on every pull request.

## Child DOX Index
None.

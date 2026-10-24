# Verifying releases

[Product overview](../README.md) · [Standalone installation](../INSTALL.md) · [Restore](RESTORE.md)

A KyCalendar release is the container image `ghcr.io/busnes-app/kycalendar:X.Y.Z` (or `X.Y.Z.N`
for a dependency-only release), built for `linux/amd64`. This page shows how to check that an
image was built by this repository's release workflow from the tagged commit, how to read its
software bill of materials, and how to rebuild its server binary yourself and get the same bytes.

## What is signed, and by whom

- `release-image.yml` builds each release from its tag and publishes a SLSA build-provenance
  attestation for the image digest through GitHub's Sigstore integration. It verifies that
  attestation, and that the image reports the tag's version, before it moves `:stable`. It
  refuses a tag whose commit did not pass CI.
- Every push to `main` that passes CI publishes `:<commit sha>` and moves `:latest`, attested by
  `ci.yml` on `main`. The comment above `image:` in `docker-compose.yml` has the command for those.
- There is no long-lived signing key to download. The signature is tied to a workflow identity,
  and that identity is the thing you check:
  `https://github.com/Busnes-app/KyCalendar/.github/workflows/release-image.yml@refs/tags/v<version>`.
  `gh` fetches Sigstore's public trust root itself.
- Each release image also carries an SPDX software bill of materials made by BuildKit. The SBOM
  sits in the image index and is **not** separately signed; trust it as far as you trust the
  digest you verified.

## Verify a release image

You need `gh` (logged in) and `docker` with buildx. Name the version yourself, and verify the
version tag, not `:stable`: tags can move, so the check resolves the digest first and then requires
the attestation to name the commit the release tag points at.

```bash
v=<release version, e.g. 0.4.3>
sha=$(gh api repos/Busnes-app/KyCalendar/commits/refs/tags/v$v --jq .sha) \
  && d=$(docker buildx imagetools inspect ghcr.io/busnes-app/kycalendar:$v --format '{{.Manifest.Digest}}') \
  && gh attestation verify "oci://ghcr.io/busnes-app/kycalendar@$d" --repo Busnes-app/KyCalendar \
       --cert-identity https://github.com/Busnes-app/KyCalendar/.github/workflows/release-image.yml@refs/tags/v$v \
  && [ "$(gh attestation verify "oci://ghcr.io/busnes-app/kycalendar@$d" --repo Busnes-app/KyCalendar \
       --cert-identity https://github.com/Busnes-app/KyCalendar/.github/workflows/release-image.yml@refs/tags/v$v \
       --format json --jq '.[0].verificationResult.statement.predicate.buildDefinition.resolvedDependencies[0].digest.gitCommit')" = "$sha" ] \
  && echo "OK: ghcr.io/busnes-app/kycalendar@$d is release v$v, built from $sha"
```

Then run that digest, not the tag: set `KY_IMAGE=ghcr.io/busnes-app/kycalendar@$d` in `.env`.
The comment above `image:` in `docker-compose.yml` has a version of this command that writes
`.env` safely.

Any failure means: do not run that image. A different workflow, a branch instead of the tag, or
another repository all fail the `--cert-identity` or `--repo` check.

To read the SBOM:

```bash
docker buildx imagetools inspect ghcr.io/busnes-app/kycalendar@$d --format '{{ json .SBOM }}'
```

## Rebuild the binary and compare

**Releases after 0.4.3 only, and not yet checked against a published release.** From the first
release after 0.4.3 the Dockerfile builds with `-trimpath`, and the build context has no `.git`,
so the same commit, Go version and version string give the same binary on any machine and in any
directory. CI checks this on every push (`release binary is repeatable`). Images up to 0.4.3
recorded their build path (`/app`) and do not compare.

```bash
v=<release version>
git clone --branch v$v https://github.com/Busnes-app/KyCalendar.git kycalendar-v$v && cd kycalendar-v$v
go_version=$(sed -n 's/^FROM golang:\([0-9.]*\)-alpine@.*/\1/p' Dockerfile)
GOTOOLCHAIN=go$go_version CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath \
  -ldflags="-s -w -X github.com/Busnes-app/kycalendar/internal/appversion.release=$v" \
  -o kycalendar-from-source ./cmd/server
cid=$(docker create ghcr.io/busnes-app/kycalendar@$d) \
  && docker cp "$cid:/app/kycalendar" kycalendar-from-image && docker rm "$cid" >/dev/null
sha256sum kycalendar-from-source kycalendar-from-image
```

The two hashes must match. This works because the binary embeds the committed `web/dist`, and CI
fails any commit whose `web/dist` differs from a fresh `npm run build`. The Alpine layers around
the binary are not rebuilt bit for bit; the binary is what you are checking.

`-buildvcs=false` is needed only because a clone has a `.git` directory; the image build context
excludes `.git` (`.dockerignore`), so Go stamps nothing there.

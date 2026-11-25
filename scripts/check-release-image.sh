#!/usr/bin/env bash
# Usage: check-release-image.sh <image>
# Fails unless the image is a release build: its binary carries no end-to-end build tag, and a
# complete multi-tenant configuration still meets the last gate's refusal
# (docs/superpowers/specs/2026-11-24-multi-tenant-e2e-design.md, Mechanism).
set -euo pipefail
image=${1:?usage: check-release-image.sh <image>}
dir=$(mktemp -d)
cid=$(docker create "$image")
trap 'docker rm -f "$cid" >/dev/null; rm -rf "$dir"' EXIT
docker cp -q "$cid:/app/kycalendar" "$dir/kycalendar"

info=$(go version -m "$dir/kycalendar")
grep -q -P '^\tpath\tgithub.com/Busnes-app/kycalendar/cmd/server$' <<<"$info" ||
  { echo "::error::$image: no build info for kycalendar:" >&2; echo "$info" >&2; exit 1; }
if tags=$(grep -P '^\tbuild\t-tags=' <<<"$info") && grep -q -w kye2e <<<"$tags"; then
  echo "::error::$image was built with the end-to-end test tag: $tags" >&2
  exit 1
fi

# A complete multi-tenant configuration, issuer under .test as the test build wants: only the
# gate may refuse it. --network none: nothing it could reach exists anyway.
printf 's3-secret' > "$dir/s3" && chmod 0644 "$dir/s3"
set +e
out=$(docker run --rm --network none --memory 256m -v "$dir/s3:/run/s3:ro" \
  -e KYCALENDAR_TENANCY=multi -e KY_DB_DRIVER=postgres -e KY_DB_DSN=postgres://cal:pw@db/cal \
  -e KY_SESSION_SECRET="$(printf 's%.0s' {1..32})" -e KYCALENDAR_SCHEDULING=true \
  -e KY_ENCRYPTION_KEY="$(printf 'ab%.0s' {1..32})" -e KY_KYSIGNON_ISSUER=https://id.e2e.test \
  -e KYCALENDAR_S3_ENDPOINT=https://s3.e2e.test -e KYCALENDAR_S3_BUCKET=cal -e KYCALENDAR_S3_ACCESS_KEY=ak \
  -e KYCALENDAR_S3_SECRET_KEY_FILE=/run/s3 -e KYCALENDAR_STAFF_ORG_ID=org-staff -e KY_CELLS=e2e-1 \
  "$image" 2>&1)
code=$?
set -e
if [ "$code" -eq 0 ] || ! grep -q 'every requirement is met, but multi-tenant startup stays refused' <<<"$out"; then
  echo "::error::$image did not refuse a complete multi-tenant configuration at the last gate (exit $code):" >&2
  echo "$out" >&2
  exit 1
fi
echo "$image: release build, multi-tenant startup refused"

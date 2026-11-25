#!/usr/bin/env bash
# Runs the suite end-to-end driver. Needs Docker with compose and buildx, and Go. On Major:
# kslot e2e/suite/run.sh, with E2E_BUILDER naming a memory-capped buildx builder.
set -euo pipefail
cd "$(dirname "$0")"
exec go test -tags kye2e -count=1 -timeout 30m -v ./...

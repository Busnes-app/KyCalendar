#!/usr/bin/env bash
# Refuse classical-only TLS overrides and verify outbound hybrid PQ TLS
# (X25519MLKEM768) under both the default and FIPS 140-3 Go profiles.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail=0

curve_hits=$(git grep -n -w 'CurvePreferences' -- '*.go' ':!*_test.go' || true)
if [ -n "$curve_hits" ]; then
  echo "::error::CurvePreferences in non-test Go code (overrides Go's hybrid PQ TLS defaults):" >&2
  echo "$curve_hits" >&2
  fail=1
fi

mlkem_hits=$(git grep -n -i 'tlsmlkem' -- . ':!*_test.go' ':!*.md' ':!scripts/check-pqc-tls.sh' || true)
if [ -n "$mlkem_hits" ]; then
  echo "::error::tlsmlkem reference outside tests and docs:" >&2
  echo "$mlkem_hits" >&2
  fail=1
fi

[ "$fail" -eq 0 ] || exit 1

go test -count=1 -run '^TestGuardedClientNegotiatesHybridPQTLS$' ./internal/sso
GODEBUG=fips140=on go test -count=1 -run '^TestGuardedClientNegotiatesHybridPQTLS$' ./internal/sso

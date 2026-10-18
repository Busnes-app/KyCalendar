#!/usr/bin/env bash
# third_party/go-webdav is Busnes-app/go-webdav (branch kycalendar) at the commit pinned in
# third_party/go-webdav.commit, byte for byte, without .github. Change the fork first.
#
#   check-fork-sync.sh <fork-checkout>                   vendored tree equals the pinned commit
#   check-fork-sync.sh --write <fork-checkout> <commit>  vendor <commit> and pin it
#   check-fork-sync.sh --since <git-ref>                 offline (CI): a change to the vendored
#                                                         tree since <git-ref> moves the pin too
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR="$ROOT/third_party/go-webdav"
PIN="$ROOT/third_party/go-webdav.commit"

# extract <fork> <commit> <dir>: the fork's tree at commit, as vendored.
extract() {
  git -C "$1" cat-file -e "$2^{commit}"
  mkdir -p "$3"
  git -C "$1" archive "$2" | tar -x -C "$3"
  rm -rf "$3/.github"
}

case "${1:-}" in
--write)
  fork="$2" commit="$(git -C "$2" rev-parse "$3^{commit}")"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  extract "$fork" "$commit" "$tmp/tree"
  rm -rf "$VENDOR"
  mv "$tmp/tree" "$VENDOR"
  echo "$commit" >"$PIN"
  echo "vendored go-webdav $commit"
  ;;
--since)
  changed="$(git -C "$ROOT" diff --name-only "$2" HEAD -- third_party/go-webdav third_party/go-webdav.commit)"
  if printf '%s\n' "$changed" | grep -q '^third_party/go-webdav/' &&
    ! printf '%s\n' "$changed" | grep -qx 'third_party/go-webdav.commit'; then
    echo "third_party/go-webdav changed without moving third_party/go-webdav.commit:"
    echo "land the change in Busnes-app/go-webdav, then run scripts/check-fork-sync.sh --write"
    exit 1
  fi
  grep -qxE '[0-9a-f]{40}' "$PIN" || { echo "$PIN is not a commit id"; exit 1; }
  echo "fork pin consistent"
  ;;
"" | -*)
  sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
*)
  commit="$(cat "$PIN")"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  extract "$1" "$commit" "$tmp/tree"
  diff -r "$tmp/tree" "$VENDOR"
  echo "third_party/go-webdav matches Busnes-app/go-webdav $commit"
  ;;
esac

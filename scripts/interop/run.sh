#!/usr/bin/env bash
# CalDAV interop: drives a built server with an independent client (python caldav) and raw HTTP.
# Usage: scripts/interop/run.sh [binary]   (default: builds ./cmd/server)
# KY_INTEROP_FREEBUSY=1 adds free/busy checks; KY_INTEROP_SCHEDULING=1 runs the server with
# KYCALENDAR_SCHEDULING=true and adds the scheduling checks.
# Needs python3. The client is installed into a throwaway venv outside the repo.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PORT="${KY_INTEROP_PORT:-18961}"
BASE="http://127.0.0.1:$PORT"
WORK="$(mktemp -d)"
VENV="${KY_INTEROP_VENV:-${XDG_CACHE_HOME:-$HOME/.cache}/kycalendar-interop-venv}"
SERVER_PID=""
cleanup() { if [ -n "$SERVER_PID" ]; then kill "$SERVER_PID" 2>/dev/null || true; wait "$SERVER_PID" 2>/dev/null || true; fi; rm -rf "$WORK"; }
trap cleanup EXIT

BIN="${1:-}"
if [ -z "$BIN" ]; then
  BIN="$WORK/kycalendar"
  go -C "$ROOT" build -o "$BIN" ./cmd/server
fi
if [ ! -x "$VENV/bin/python" ]; then
  python3 -m venv "$VENV"
  "$VENV/bin/pip" install -q 'caldav==3.3.1'
fi

ADMIN_PASS="InteropAdminPass123!"
if [ "${KY_INTEROP_SCHEDULING:-}" = 1 ]; then KY_INTEROP_ENV="KYCALENDAR_SCHEDULING=true ${KY_INTEROP_ENV:-}"; fi
KY_PORT="$PORT" KY_HOST=127.0.0.1 KY_DATA_DIR="$WORK/data" KY_DB_DRIVER=sqlite \
  KY_ADMIN_PASSWORD="$ADMIN_PASS" KY_CAPTCHA_PROVIDER=none KY_APP_URL="$BASE" \
  env ${KY_INTEROP_ENV:-} "$BIN" >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
curl -s -o /dev/null --retry 30 --retry-delay 1 --retry-all-errors "$BASE/" || { cat "$WORK/server.log"; exit 1; }

json() { curl -s -H 'Content-Type: application/json' "$@"; }
field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
csrf() { awk '$6 == "ky_csrf" { print $7 }' "$1"; }

# Sign in, replacing a temporary password first. login <jar> <user> <temp> <final>
login() {
  json -c "$1" -d '{"username":"'"$2"'","password":"'"$3"'"}' "$BASE/api/auth/login" >/dev/null
  json -b "$1" -H "X-CSRF-Token: $(csrf "$1")" \
    -d '{"current_password":"'"$3"'","new_password":"'"$4"'"}' "$BASE/api/auth/change-password" >/dev/null
  json -c "$1" -d '{"username":"'"$2"'","password":"'"$4"'"}' "$BASE/api/auth/login" >/dev/null
}

ADMIN="$WORK/admin.jar"
login "$ADMIN" admin "$ADMIN_PASS" "InteropAdminFinal456!"

# person <username>: prints "<user-id> <dav-token>"
person() {
  local body temp jar="$WORK/$1.jar"
  body="$(json -b "$ADMIN" -H "X-CSRF-Token: $(csrf "$ADMIN")" \
    -d '{"username":"'"$1"'","display_name":"'"$1"'","email":"'"$1"'@example.com","role":"user"}' -X POST "$BASE/api/admin/users")"
  temp="$(printf '%s' "$body" | field temporary_password)"
  login "$jar" "$1" "$temp" "InteropPerson${1}Pass1!"
  local id tok
  id="$(json -b "$jar" "$BASE/api/auth/me" | field id)"
  tok="$(json -b "$jar" -H "X-CSRF-Token: $(csrf "$jar")" -d '{"label":"interop","scope":"dav"}' \
    -X POST "$BASE/api/app-passwords" | field password)"
  [ -n "$id" ] && [ -n "$tok" ] || { echo "provisioning $1 failed: $body" >&2; exit 1; }
  echo "$id $tok"
}
read -r ALICE_ID ALICE_TOK < <(person alice)
read -r BOB_ID BOB_TOK < <(person bob)

KY_BASE="$BASE" ALICE_ID="$ALICE_ID" ALICE_TOK="$ALICE_TOK" BOB_ID="$BOB_ID" BOB_TOK="$BOB_TOK" \
  "$VENV/bin/python" -I "$ROOT/scripts/interop/caldav_interop.py" || { tail -20 "$WORK/server.log"; exit 1; }

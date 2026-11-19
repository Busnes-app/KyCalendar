#!/usr/bin/env bash
# Proves the documented SQLite -> PostgreSQL cutover with the real image (spec AC27):
#   1. a SQLite install on a named /app/data volume, with the bootstrap admin, a person, a
#      calendar and an event;
#   2. `docker stop` at its default timeout exits 0 and leaves no kycalendar.db-wal;
#   3. the image's migrate-postgres copies into an empty PostgreSQL 17 (pinned by digest, on an
#      --internal network) with the source digest unchanged, and a repeat is refused;
#   4. the image starts on PostgreSQL with the required settings, /readyz answers 200, the
#      admin signs in and the person reads the event;
#   5. Back up now writes a local capsule and backup-drill passes.
# The push relay variables are empty and sign-in is off, so nothing reaches a production
# service. The app's port is published on loopback only. Every container, network and volume
# this run made is removed on exit.
#
# Usage: scripts/check-postgres-cutover.sh [image]   (default kycalendar:ci)
set -euo pipefail

image=${1:-kycalendar:ci}
pg_image=postgres:17@sha256:ff87138949ad9a46f32ff23bd6852b52ce024a13dffe9b4087be98292cdd3b4f
run_id=$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
name=kycalendar-cutover-$run_id
db_net=$name-db app_net=$name-app volume=$name-data pg=$name-pg sqlite_app=$name-sqlite pg_app=$name-postgres
work=$(mktemp -d)
here=$(cd "$(dirname "$0")" && pwd)

cleanup() {
  docker rm -f -v "$sqlite_app" "$pg_app" "$pg" >/dev/null 2>&1 || true
  docker volume rm -f "$volume" >/dev/null 2>&1 || true
  docker network rm "$db_net" "$app_net" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

fail() {
  echo "cutover check failed: $*" >&2
  for c in "$sqlite_app" "$pg_app" "$pg"; do
    docker logs --tail 40 "$c" 2>/dev/null | sed "s/^/[$c] /" >&2 || true
  done
  exit 1
}

session_secret=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
# Throwaway servers never reach the production relays; sign-in and scheduling are off.
common=(-e PUSH_RELAY_URL= -e APNS_RELAY_URL= -e KY_SSO_ENABLED=false -e KY_CAPTCHA_PROVIDER=none
  -e KY_SESSION_SECRET="$session_secret" -e KYCALENDAR_SCHEDULING=false -e KY_ADMIN_PASSWORD=Cutover-Admin-Pass-1
  -e KYCALENDAR_BACKUP_DIR=/app/data/backups)

port() { docker port "$1" 8080/tcp | head -n1 | sed 's/.*://'; }

ready() { # base URL
  for _ in $(seq 1 60); do
    if python3 "$here/cutover-client.py" ready "$1" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

# Runs the image on the volume with no network, for reading the SQLite file.
inspect() { docker run --rm --network none -v "$volume:/app/data" --entrypoint sh "$image" -c "$1"; }

docker network create --internal "$db_net" >/dev/null
docker network create "$app_net" >/dev/null
docker volume create "$volume" >/dev/null

echo "1. SQLite install with the bootstrap admin, a person, a calendar and an event"
docker run -d --name "$sqlite_app" --network "$app_net" -p 127.0.0.1::8080 -v "$volume:/app/data" "${common[@]}" "$image" >/dev/null
base="http://127.0.0.1:$(port "$sqlite_app")"
ready "$base" || fail "the SQLite server never became ready"
printf 'Cutover-Temp-Pass-1\n' | docker exec -i "$sqlite_app" /app/kycalendar create-user -username ann >/dev/null 2>&1 ||
  fail "create-user failed"
python3 "$here/cutover-client.py" seed "$base" || fail "could not write the event on SQLite"

echo "2. docker stop at its default timeout"
docker stop "$sqlite_app" >/dev/null
code=$(docker inspect -f '{{.State.ExitCode}}' "$sqlite_app")
[ "$code" = 0 ] || fail "the server exited $code on docker stop"
inspect 'test ! -s /app/data/kycalendar.db-wal' || fail "the stopped server left kycalendar.db-wal behind"
before=$(inspect 'sha256sum /app/data/kycalendar.db' | cut -d' ' -f1)

echo "3. PostgreSQL 17 on an internal network; migrate-postgres from the stopped volume"
password=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
docker run -d --name "$pg" --network "$db_net" -e POSTGRES_PASSWORD="$password" -e POSTGRES_USER=kycalendar \
  -e POSTGRES_DB=kycalendar "$pg_image" >/dev/null
for _ in $(seq 1 60); do
  # The image initialises on a socket-only server first; TCP means the real one is up.
  if docker exec "$pg" pg_isready -q -h 127.0.0.1 -U kycalendar; then break; fi
  sleep 1
done
docker exec "$pg" pg_isready -q -h 127.0.0.1 -U kycalendar || fail "PostgreSQL never became ready"
dsn="postgres://kycalendar:$password@$pg:5432/kycalendar?sslmode=disable"
# The DSN holds the password: it reaches containers through env files, never argv.
umask 077
printf 'KY_MIGRATE_POSTGRES_DSN=%s\n' "$dsn" >"$work/migration.env"
printf 'KY_DB_DRIVER=postgres\nKY_DB_DSN=%s\n' "$dsn" >"$work/postgres.env"

migrate() {
  docker run --rm --network "$db_net" -v "$volume:/app/data" --env-file "$work/migration.env" \
    -e PUSH_RELAY_URL= -e APNS_RELAY_URL= "$image" migrate-postgres --source /app/data/kycalendar.db 2>&1
}
out=$(migrate) || fail "the migration failed: $out"
case $out in *"every table verified"*) ;; *) fail "unexpected migration output: $out" ;; esac
case $out in *"$password"*) fail "the migration printed the database password" ;; esac
after=$(inspect 'sha256sum /app/data/kycalendar.db' | cut -d' ' -f1)
[ "$before" = "$after" ] || fail "the migration changed the SQLite file"
inspect 'test ! -e /app/data/kycalendar.db-wal && test ! -e /app/data/kycalendar.db-shm' || fail "the migration left -wal or -shm"

echo "4. a repeat migration into the populated database is refused"
if again=$(migrate); then
  fail "a second migration into the populated database succeeded"
fi
case $again in *"already holds data"*) ;; *) fail "the repeat was refused for another reason: $again" ;; esac

echo "5. the image starts on PostgreSQL"
docker create --name "$pg_app" --network "$app_net" -p 127.0.0.1::8080 -v "$volume:/app/data" \
  --env-file "$work/postgres.env" "${common[@]}" "$image" >/dev/null
docker network connect "$db_net" "$pg_app"
docker start "$pg_app" >/dev/null
base="http://127.0.0.1:$(port "$pg_app")"
ready "$base" || fail "the server never became ready on PostgreSQL"
python3 "$here/cutover-client.py" verify "$base" || fail "the migrated data is not served"
docker logs "$pg_app" 2>&1 | grep -qF "$password" && fail "the server logged the database password"

echo "6. Back up now writes a local capsule, and backup-drill passes"
python3 "$here/cutover-client.py" backup "$base" "$here/../internal/backup/testdata/pairing-v050.json" ||
  fail "Back up now failed on PostgreSQL"
docker exec "$pg_app" /app/kycalendar backup-drill >"$work/drill.txt" 2>&1 || {
  cat "$work/drill.txt" >&2
  fail "backup-drill failed"
}
grep -q "PASSED" "$work/drill.txt" || fail "backup-drill did not pass"

echo "cutover check passed: source digest $before unchanged, event served from PostgreSQL, capsule written and drilled"

#!/usr/bin/env bash
# Proves the documented SQLite -> PostgreSQL cutover (docs/RUNNING.md, spec AC27) through the
# shipped docker-compose.yml, with the documented commands:
#   1. a SQLite install with the bootstrap admin, a person, a calendar and an event;
#   2. `docker compose stop app` exits 0 and leaves no kycalendar.db-wal;
#   3. the bundled postgres profile; `docker compose run --rm --no-deps --env-from-file
#      migration.env app migrate-postgres --check` then without --check: the source digest is
#      unchanged, and a repeat is refused;
#   4. .env switched to PostgreSQL, `docker compose up -d app`: /readyz 200, the admin signs
#      in and the person reads the event;
#   5. Back up now writes a local capsule and backup-drill passes;
#   6. with the app stopped, `accept-replica-config` runs.
# A test override changes only what a throwaway run needs: the image under test, unique
# names, a random loopback port, named volumes, a digest-pinned PostgreSQL 17 on an
# --internal network, empty push relay variables and no CAPTCHA. Everything this run made is
# removed on exit.
#
# Usage: scripts/check-postgres-cutover.sh [image]   (default kycalendar:ci)
set -euo pipefail

image=${1:-kycalendar:ci}
pg_image=postgres:17@sha256:ff87138949ad9a46f32ff23bd6852b52ce024a13dffe9b4087be98292cdd3b4f
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
run_id=$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
name=kycal-cutover-$run_id
work=$(mktemp -d)

dc() { docker compose -p "$name" --project-directory "$work" -f "$repo/docker-compose.yml" -f "$work/override.yml" "$@"; }

cleanup() {
  dc --profile postgres down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

fail() {
  echo "cutover check failed: $*" >&2
  dc --profile postgres logs --tail 40 >&2 2>/dev/null || true
  exit 1
}

cat >"$work/override.yml" <<EOF
services:
  app:
    image: $image
    container_name: $name-app
    ports: !override
      - "127.0.0.1::8080"
    environment:
      - PUSH_RELAY_URL=
      - APNS_RELAY_URL=
      - KY_CAPTCHA_PROVIDER=none
    volumes: !override
      - data:/app/data
      - backups:/app/backups
    networks: [default, db]
  postgres:
    image: $pg_image
    container_name: $name-pg
    ports: !reset []
    networks: [db]
networks:
  db:
    internal: true
volumes:
  data:
  backups:
EOF

umask 077
session_secret=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
password=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
dsn="postgres://postgres:$password@postgres:5432/ky_server?sslmode=disable"
# Credentials live in .env and migration.env (mode 600), never on a command line.
cat >"$work/.env" <<EOF
KY_SESSION_SECRET=$session_secret
KY_ADMIN_PASSWORD=Cutover-Admin-Pass-1
KYCALENDAR_SCHEDULING=false
KY_POSTGRES_PASSWORD=$password
EOF
printf 'KY_MIGRATE_POSTGRES_DSN=%s\n' "$dsn" >"$work/migration.env"

base() { echo "http://127.0.0.1:$(dc port app 8080 | sed 's/.*://')"; }
ready() {
  for _ in $(seq 1 60); do
    if python3 "$here/cutover-client.py" ready "$1" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}
# Runs the image on the data volume with no network, for reading the SQLite file.
inspect() { dc run --rm --no-deps -T --entrypoint sh app -c "$1" 2>/dev/null; }

echo "1. SQLite install with the bootstrap admin, a person, a calendar and an event"
dc up -d app >/dev/null 2>&1 || fail "docker compose up failed"
url=$(base)
ready "$url" || fail "the SQLite server never became ready"
printf 'Cutover-Temp-Pass-1\n' | dc exec -T app /app/kycalendar create-user -username ann >/dev/null 2>&1 ||
  fail "create-user failed"
python3 "$here/cutover-client.py" seed "$url" || fail "could not write the event on SQLite"

echo "2. docker compose stop app"
dc stop app >/dev/null 2>&1
code=$(docker inspect -f '{{.State.ExitCode}}' "$name-app")
[ "$code" = 0 ] || fail "the server exited $code on stop"
inspect 'test ! -s /app/data/kycalendar.db-wal' || fail "the stopped server left kycalendar.db-wal behind"
before=$(inspect 'sha256sum /app/data/kycalendar.db' | cut -d' ' -f1)

echo "3. the postgres profile, then migrate-postgres --check and the copy"
dc --profile postgres up -d postgres >/dev/null 2>&1 || fail "the postgres service did not start"
for _ in $(seq 1 60); do
  # The image initialises on a socket-only server first; TCP means the real one is up.
  if dc exec -T postgres pg_isready -q -h 127.0.0.1 -U postgres; then break; fi
  sleep 1
done
dc exec -T postgres pg_isready -q -h 127.0.0.1 -U postgres || fail "PostgreSQL never became ready"
migrate() { dc run --rm --no-deps -T --env-from-file "$work/migration.env" app migrate-postgres "$@" --source /app/data/kycalendar.db 2>&1; }
out=$(migrate --check) || fail "migrate-postgres --check failed: $out"
case $out in *"Check passed"*) ;; *) fail "unexpected --check output: $out" ;; esac
out=$(migrate) || fail "the migration failed: $out"
case $out in *"every table verified"*) ;; *) fail "unexpected migration output: $out" ;; esac
case $out in *"$password"*) fail "the migration printed the database password" ;; esac
after=$(inspect 'sha256sum /app/data/kycalendar.db' | cut -d' ' -f1)
[ "$before" = "$after" ] || fail "the migration changed the SQLite file"
inspect 'test ! -e /app/data/kycalendar.db-wal && test ! -e /app/data/kycalendar.db-shm' || fail "the migration left -wal or -shm"
if again=$(migrate); then
  fail "a second migration into the populated database succeeded"
fi
case $again in *"already holds data"*) ;; *) fail "the repeat was refused for another reason: $again" ;; esac

echo "4. .env switched to PostgreSQL; the app starts on it"
printf 'KY_DB_DRIVER=postgres\nKY_DB_DSN=%s\n' "$dsn" >>"$work/.env"
dc up -d app >/dev/null 2>&1 || fail "docker compose up on PostgreSQL failed"
url=$(base)
ready "$url" || fail "the server never became ready on PostgreSQL"
python3 "$here/cutover-client.py" verify "$url" || fail "the migrated data is not served"
dc logs app 2>&1 | grep -qF "$password" && fail "the server logged the database password"

echo "5. Back up now writes a local capsule, and backup-drill passes"
python3 "$here/cutover-client.py" backup "$url" "$repo/internal/backup/testdata/pairing-v050.json" ||
  fail "Back up now failed on PostgreSQL"
drill=$(dc exec -T app /app/kycalendar backup-drill 2>&1) || fail "backup-drill failed: $drill"
case $drill in *PASSED*) ;; *) fail "backup-drill did not pass: $drill" ;; esac

echo "6. accept-replica-config with the app stopped"
dc stop app >/dev/null 2>&1
accepted=$(dc run --rm --no-deps -T app accept-replica-config 2>&1) || fail "accept-replica-config failed: $accepted"
case $accepted in *"Replica configuration"*) ;; *) fail "unexpected accept-replica-config output: $accepted" ;; esac

echo "cutover check passed through docker compose: source digest $before unchanged, event served from PostgreSQL, capsule written and drilled"

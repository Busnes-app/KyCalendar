#!/usr/bin/env bash
# Proves the PostgreSQL 17 -> 18 move through the shipped docker-compose.yml: data seeded by the
# previous postgres service (17, pgdata at /var/lib/postgresql/data) comes up on 18 with its rows,
# roles and passwords on the next `up`; 17 still starts on the old volume (rollback); a rerun is a
# no-op; a bad dump never yields an empty cluster; a fresh install initialises. Only the postgres
# services run. Everything this run made is removed on exit.
#
# Usage: scripts/test-postgres-17-to-18.sh
set -euo pipefail

P17=postgres:17.12-alpine3.24@sha256:f9cf3e1f0066f0eb0c48707d864f0ac5941e9ff454a072077d97a4d278d07d73
P18=postgres:18.7-alpine3.24@sha256:0115267ab5a509022df9319c77be365cef335adb71556c75f60f5360ee247e71
# sha256 of the kit's postgres-17-to-18.sh: the vendored copy must stay byte-identical.
kit=2992644109716cd34297fbcb76eba8907f4a5e3031a12c05aade010e5ea2b571
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
name=kycal-pg1718-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
work=$(mktemp -d)

# The previous postgres service (docker-compose.yml before PostgreSQL 18), with the 17 image
# pinned and no host port.
old() { docker compose -p "$name" --project-directory "$work" -f "$work/old.yml" --profile postgres "$@"; }
new() { docker compose -p "$name" --project-directory "$work" -f "$repo/docker-compose.yml" -f "$work/override.yml" --profile postgres "$@"; }
cleanup() {
  new down -v --remove-orphans >/dev/null 2>&1 || true
  old down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
fail() { echo "postgres 17 to 18 check failed: $*" >&2; docker logs "$name-pg" 2>&1 | tail -20 >&2 || true; exit 1; }

[ "$(sha256sum "$here/postgres-17-to-18.sh" | cut -d' ' -f1)" = "$kit" ] || fail "scripts/postgres-17-to-18.sh is not the kit's"
mkdir "$work/scripts" && cp "$here/postgres-17-to-18.sh" "$work/scripts/"
umask 077
printf 'KY_POSTGRES_PASSWORD=%s\n' "$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')" >"$work/.env"
cat >"$work/old.yml" <<EOF
services:
  postgres:
    image: $P17
    container_name: $name-pg
    profiles: ["postgres"]
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: \${KY_POSTGRES_PASSWORD:-}
      POSTGRES_DB: ky_server
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata:
EOF
# Test-only: unique names, no host port, and no restart so a failed start stays visible.
cat >"$work/override.yml" <<EOF
services:
  postgres:
    container_name: $name-pg
    restart: "no"
    ports: !reset []
EOF

images=$(new config --images | grep "^postgres:" | sort | tr '\n' ' ')
[ "$images" = "$P17 $P18 " ] || fail "postgres images are not the pinned 17 one-shot and 18: $images"

ready() {
  for _ in $(seq 1 120); do
    docker exec "$name-pg" pg_isready -q -h 127.0.0.1 -U postgres 2>/dev/null && return 0
    [ "$(docker inspect -f '{{.State.Running}}' "$name-pg" 2>/dev/null)" = true ] || return 1
    sleep 1
  done
  return 1
}
# Password login from another container on the compose network, as the app connects (inside the
# server container the image trusts 127.0.0.1, so a login there proves no password).
client() { docker run --rm --network "${name}_default" --env-file "$work/.env" "$@"; }
# shellcheck disable=SC2016 # expanded inside the client container
q() { client "$P18" sh -c 'PGPASSWORD=$KY_POSTGRES_PASSWORD psql -h postgres -U postgres -d ky_server -XAtc "$1"' sh "$1"; }
in_vol() { docker run --rm --network none -v "${name}_postgres:/v" "$P18" sh -c "$1"; }

echo "1. PostgreSQL 17 data from the previous compose definition"
old up -d postgres >/dev/null 2>&1 || fail "the 17 service did not start"
ready || fail "17 never became ready"
docker exec -i "$name-pg" psql -U postgres -d ky_server -X -q -v ON_ERROR_STOP=1 >/dev/null <<'SQL' || fail "seeding 17 failed"
create role reader login password 'readerpass';
create database cdb owner reader locale 'C' encoding 'UTF8' template template0;
create table t(id bigserial primary key, s text unique, j jsonb);
insert into t(s, j) select 'row' || g, jsonb_build_object('g', g) from generate_series(1, 50000) g;
\connect cdb reader
create table c(x text collate "C" primary key);
insert into c values ('a'), ('B'), ('é');
SQL

echo "2. the next docker compose up, with 17 still running, moves it to 18"
new up -d postgres >/dev/null 2>&1 || fail "docker compose up on the new file failed"
new logs postgres-17-dump 2>&1 | grep -q 'dumped' || fail "the one-shot did not dump"
ready || fail "18 never became ready after the move"
[ "$(q 'show server_version_num' | cut -c1-2)" = 18 ] || fail "not PostgreSQL 18"
[ "$(q "select count(*) || ',' || sum((j->>'g')::int) from t")" = "50000,1250025000" ] || fail "rows"
[ "$(client -e PGPASSWORD=readerpass "$P18" psql -h postgres -U reader -d cdb -XAtc 'select string_agg(x, $$,$$ order by x) from c')" = "B,a,é" ] ||
  fail "the reader role, its password or the C collation"
client -e PGPASSWORD=wrongpass "$P18" psql -h postgres -U reader -d cdb -XAtc 'select 1' >/dev/null 2>&1 &&
  fail "a wrong password was accepted"
[ "$(q 'show data_directory')" = /var/lib/postgresql/18/docker ] || fail "18 data directory"

echo "3. rollback: the previous definition starts 17 on the untouched pgdata volume"
new down >/dev/null 2>&1
old up -d postgres >/dev/null 2>&1 || fail "17 did not start again"
ready || fail "17 no longer starts on its data"
[ "$(docker exec "$name-pg" psql -U postgres -d ky_server -XAtc 'select count(*) from t')" = 50000 ] || fail "17 data changed"
old down >/dev/null 2>&1

echo "4. a rerun is a no-op"
new up -d postgres >/dev/null 2>&1 || fail "docker compose up failed on the rerun"
new logs postgres-17-dump 2>&1 | grep -q 'already done' || fail "the rerun dumped again"
ready || fail "18 did not restart"
docker logs "$name-pg" 2>&1 | grep -q 'restored' && fail "the rerun restored again"
[ "$(q 'select count(*) from t')" = 50000 ] || fail "rows after the rerun"
new down >/dev/null 2>&1

echo "5. a dump that fails to restore never yields a cluster, on any number of starts"
in_vol 'mv /v/18/docker /v/18/docker.saved && echo "THIS IS NOT SQL;" >> /v/upgrade-from-17/dump.sql'
for _ in 1 2; do
  new up -d postgres >/dev/null 2>&1 || true
  [ "$(docker wait "$name-pg")" != 0 ] || fail "18 started on a bad dump"
  in_vol 'test ! -e /v/18/docker' || fail "a bad dump produced a cluster"
done

echo "6. a fresh install initialises normally"
new down -v >/dev/null 2>&1
new up -d postgres >/dev/null 2>&1 || fail "docker compose up failed on a fresh install"
new logs postgres-17-dump 2>&1 | grep -q 'nothing to move' || fail "the fresh install did not skip the dump"
ready || fail "a fresh 18 never became ready"
docker logs "$name-pg" 2>&1 | grep -q 'restored' && fail "the fresh install restored something"
[ "$(q 'show data_directory')" = /var/lib/postgresql/18/docker ] || fail "fresh data directory"

echo "postgres 17 to 18 check passed through docker compose: moved, rolled back, reran, refused a bad dump, fresh install"

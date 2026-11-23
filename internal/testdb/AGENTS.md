# TestDB

## Purpose
Supplies tests with an isolated, empty database configuration so the same suite runs on either supported backend.

## Ownership
Owns test-only database provisioning and teardown. Never imported by production code.

## Local Contracts
- `Config(t *testing.T) config.DatabaseConfig` returns a SQLite temp file by default.
- With `KY_TEST_POSTGRES_DSN` set, it returns a Postgres config scoped to a per-caller schema, dropped on cleanup.
- Test files must obtain database config here, never by hardcoding a driver, so CI can switch backends.
- `SeedSQLite(t, dsn)` writes, at a SQLite DSN's file, a database every migration already ran on (made once per process, with its own sync epoch). Setup helpers that open the store at once use it: under `-race` the pure-Go SQLite takes about half a second per fresh migration. `Config` itself still hands out an empty database, which migration tests need.
- `NewRelay` is a loopback TCP relay to the test database that `Freeze` stops forwarding (connections stay open, nothing moves) and `Thaw` resumes; `Via` points a config at it. Tests of lock, readiness and process behaviour on a frozen database use it.

## Verification
- `go test ./...` (SQLite)
- `KY_TEST_POSTGRES_DSN=... go test -count=1 ./...` (PostgreSQL)

## Child DOX Index
None.

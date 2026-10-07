# Config

## Purpose
Manages environment and file-based configuration loading, defaults, and type conversions for kycalendar.

## Ownership
Owns environment variable parsing, configuration validation, default fallbacks, and security key generations.

## Local Contracts
- `LoadFromEnv() (*Config, error)` must supply safe, valid defaults for all subsystems.
- Never log plaintext secrets or sensitive tokens.
- `KY_TRUSTED_PROXIES` is a comma-separated list of reverse-proxy IPs or CIDRs, empty by default, parsed once at startup into `[]netip.Prefix`; an unparsable entry fails startup. Only a request whose peer address is in the list may speak for another client through `X-Forwarded-For`. `0.0.0.0/0` and `::/0` are refused at startup; list only the proxy's own address or subnet.
- Production startup requires an explicit, durable `KY_SESSION_SECRET`. The encryption key comes from `KY_ENCRYPTION_KEY` when set, otherwise from the keyfile at `<DataDir>/encryption.key`, which `keyfile.LoadOrCreate` mints on first start; either is a valid production configuration.

- The backup variables carry the `KYCALENDAR_` prefix; the retired `KY_BACKUP_*` names fail startup naming their replacement. `KYCALENDAR_BACKUP_DEPOSIT_INTERVAL` is a Go duration (default `24h`), only the default for the schedule the admin screen stores; `0` is off, anything else below `MinDepositInterval` (15m) or negative fails startup. `KYCALENDAR_BACKUP_DIR` (default empty, off) is the sealed local-copy directory and `KYCALENDAR_BACKUP_KEEP` (default 7) how many to retain; below 1 fails startup because the lib refuses it at write time. `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY` (default false) admits RFC1918 and CGNAT KyRecovery destinations only.
- Calendar caps (`CalendarConfig`): `KY_CALENDAR_MAX_OBJECTS_PER_USER` (default 20000), `KY_CALENDAR_MAX_CALENDARS_PER_USER` (default 50), `KY_CALENDAR_MAX_BYTES_PER_USER` (default 16 MiB). Each counts across all of a user's calendars; 0 or negative fails startup (unparsable falls back to the default, like every `getEnvInt` variable). `KY_CALENDAR_MAX_BYTES_TOTAL` caps every user's bytes together: default 40 MiB on any non-Postgres driver, keeping the SQLite snapshot under the capsule's 64 MiB per-file cap so one tenant cannot stop every backup; default 0 (off) on Postgres, which makes no capsules; negative fails startup.
- `CookieSecure` defaults on for `KY_ENV=production` or an `https://` `KY_APP_URL` (case-insensitive); an explicit `KY_COOKIE_SECURE` wins.
- `KY_CAPTCHA_PROVIDER` is `pow` (default) or `none`; anything else fails startup, because only PoW is verified server-side and an unverified name would silently disable the check.
- `KY_KYSIGNON_ISSUER`, `KY_KYSIGNON_CLIENT_ID` and `KY_KYSIGNON_SECRET` are the environment layer of the sign-in settings (`sso.Resolve`). `KY_KYSIGNON_ISSUER` fixes the provider to `kyidentity` and locks the issuer; the client ID and secret then lock their fields. Without the issuer those two are ignored, never paired with an issuer an admin typed in, and startup logs their names (never values). Under the environment issuer a saved provider of another kind is ignored whole, never merged. `KY_KYSIGNON_HMAC_SECRET` only authenticates the directory webhook and never changes or locks the provider. The issuer is used with its trailing slash trimmed.

## Verification
- `go test -v ./internal/config/...`
- `go test -v ./internal/auth/ -run TestClientIP` (the helper that consumes the allowlist)

## Child DOX Index
None.

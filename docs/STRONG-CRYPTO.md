# Supported key-strength and TLS configuration

[Running](RUNNING.md) · [Recovery](RECOVERY.md) · [Assurance case](ASSURANCE-CASE.md)

Use this configuration with the source/image revision that includes these changes.
It covers KyCalendar's enabled cryptographic boundaries; verify the independent
HTTPS edge and actual endpoints before rollout. It is not a FIPS certification.

## Preserve existing data and authority

Keep the database, `encryption.key`, environment and protected credential files,
recovery public-key pin, sealed token, capsules, receipts, local backup directory
and custodian cards. Retain the same data volume and Compose image/build, DNS and
network choices. An incompatible endpoint is a reason to repair its keys, never
to regenerate the application's data key or replace the recovery pin.

The shared primitives dependency is pinned to merged source `bae5c14dba4c`
(`v0.10.2-0.20261026050236-bae5c14dba4c`), which enforces the TOTP floor. This
also brings merged capsule tar-budget, Shamir sampling and setup-refusal repairs;
key/share/container formats are unchanged. Existing encrypted data and historical
capsules keep their original keys and custody procedure. Test a known restore
before upgrading an installation.

## Key rules in every mode

| Boundary | Rule |
| --- | --- |
| Session/PoW/CSRF signing | Supplied `KY_SESSION_SECRET` must have at least 16 literal bytes. Production still needs an explicit durable value; development generates 32 random bytes encoded as hex. Prefer a private, independent `openssl rand -hex 32` value. Length alone is not entropy. |
| Directory webhook | Nonempty `KY_KYSIGNON_HMAC_SECRET` must have at least 16 literal bytes, checked at startup and before webhook use. Empty leaves it unconfigured. Installer-owned directory credentials already require this floor. Coordinate a replacement with the sender; do not log it. |
| Data and derived keys | `KY_ENCRYPTION_KEY` or the retained keyfile must contain exactly 32 decoded bytes. AES-256-GCM uses purpose-specific derived keys and fresh nonces. Secret references, TOTP records and subscription URLs retain their purpose binding. |
| OIDC | Every RSA JWKS member needs at least 2048 actual modulus bits. The guard matches the verifier's envelope and case-sensitive JWK member parsing, bounds input and refuses redirects. Both provider kinds keep upstream signature/issuer/audience/expiry/nonce checks and cache/rotation behavior. EC/Ed25519 sizes and supported signing algorithms remain the maintained OIDC/JOSE libraries' responsibility. |
| TOTP | Generate 20 random decoded bytes. Authentication requires at least 16 decoded bytes through the shared RFC 4226/6238 implementation. Old sealed records remain readable and unchanged, but a short secret cannot authenticate. Use an enrolled recovery method or a deliberate administrator reset to re-enroll; keep other credentials. |
| Invitations | The KyPost shared secret file must contain at least 32 bytes; the signed exchange uses `ky-primitives/syncauth`. HTTPS-only endpoints and the existing address/redirect guards remain required when this optional integration is enabled. |
| Other credentials | Sessions and app-password secrets use random 256-bit values and SHA-256 storage. SCIM requires its existing explicit 32-character bearer token. OAuth client secrets and bearer tokens are credentials, separate from raw signing/cipher keys. Password hashing remains Argon2id in the shared library. |
| Recovery | Seal `kycap/3` to the pinned fixed-suite recovery public key. The service never opens product capsules; offline restore uses the recorded digest and custodian shares. |

Startup refuses an undersized signing secret without rewriting any keyfile or
database. Changing the session signing secret invalidates outstanding PoW/CSRF
values; refresh the browser's CSRF state. A weak IdP key requires deliberate
provider rotation, not a verification bypass. Restarting after upgrade replaces
the process's in-memory OIDC cache with the repaired verifier.

## Enable the Go TLS profile

Use the toolchain selected by `go.mod` and the pinned Docker builder. Set
`GODEBUG=fips140=on` before starting every server or remote Go CLI process.
Preserve other required GODEBUG entries with `fips140` set to `on`, then restart.
Use `on`, not the diagnostic `only` mode: Argon2id, HMAC-SHA1 TOTP and the suite's
fixed recovery format remain available.

The profile requires direct HTTPS issuer/authorization/token/JWKS URLs with no
URL credentials and refuses OIDC discovery/token redirects. Admin-entered
providers retain the guarded client and environment/installer providers retain
their credential ownership. Normal certificate and hostname verification stays
enabled. No failed login deletes the saved provider or its credentials.

Go filters the complete verified TLS chain, including explicitly trusted roots,
to RSA-2048+ and supported strong EC/Ed25519 certificate keys. This applies to
OIDC, published-calendar feeds, the optional KyPost invitation client, KyRecovery
and verified PostgreSQL TLS. Published-calendar redirects retain their existing
HTTPS, hop-count and per-dial address checks. A private CA does not waive the floor.

For Compose, append `docker-compose.strong-crypto.yml` to the existing protected
`.env` `COMPOSE_FILE` chain, keeping its base/build/LAN-DNS/static-IP overlays.
The overlay adds only the process setting. If another overlay supplies GODEBUG,
preserve its required entries explicitly with `fips140=on` in the resolved value.
Keep `.env` private and inspect `docker compose config` privately: it contains
credentials. `python3 docs/check-strong-crypto-compose.py` compares four chains
with synthetic inputs and starts no containers.

Use `KY_ENV=production`, an HTTPS `KY_APP_URL`, the original durable session secret,
secure cookies and exact trusted proxy address. SQLite is the local-data profile.
For remote PostgreSQL, use `sslmode=verify-full` and the correct CA/hostname;
backups retain the documented separate PostgreSQL operator procedure. The local
database fixture does not verify a deployed PostgreSQL TLS configuration.

## Independent HTTPS edge

KyCalendar serves private backend HTTP. Restrict that endpoint to its TLS proxy
and keep `KY_TRUSTED_PROXIES` limited to the proxy's actual address. Configure TLS
1.2/1.3 and a verified RSA-2048+ or P-256+ certificate chain. With Nginx/OpenSSL,
the relevant policy includes:

```nginx
ssl_protocols TLSv1.2 TLSv1.3;
ssl_ciphers 'DEFAULT@SECLEVEL=2';
```

Set the actual certificate/key paths and routing for the deployment. Go's
process setting does not configure browsers, native CalDAV clients or the
external proxy. SAML metadata publication is not an inbound assertion verifier.

## Verification and rollout

Run full ordinary/configured race suites against SQLite and PostgreSQL, the
vendored DAV module tests, frontend build/tests and browser regressions, the
Compose checker, reproducible builds, vulnerability scans, Docker health/version
and existing authentication/recovery smoke. Regressions cover real signed tokens,
JWK casing/order, key lengths, unchanged sealed legacy TOTP and trusted TLS roots
with a strong-leaf control. They do not establish successful real-device use.

Then check the deployed edge, normal/administrator sign-in, existing TOTP and
recovery access, directory revocation, subscribed-calendar refresh, applicable
invitation delivery, backup receipts and a custody-correct offline restore.

References: [RFC 4226](https://www.rfc-editor.org/rfc/rfc4226#section-4),
[RFC 6238](https://www.rfc-editor.org/rfc/rfc6238),
[Go FIPS runtime mode and limits](https://go.dev/doc/security/fips140).

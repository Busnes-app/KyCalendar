# Auth

## Purpose
Manages user authentication, password policies, Multi-Factor Authentication (RFC 6238 TOTP), recovery codes, session token lifecycle, and anti-abuse CAPTCHA (Proof-of-Work default).

## Ownership
Owns session issuance and verification, TOTP token generation/validation, recovery code redemption, and client-side PoW verification.

## Local Contracts
- Sessions for `must_change_password` users authenticate only GET `/api/auth/me` and POST `/api/auth/change-password` or `/api/auth/logout`; private settings and product actions fail closed. Session issuance is bound to the verified password hash and serialized with replacement.
- Passwords require at least 12 characters (`ValidatePassword`).
- `Session.CreatedAt` is the credential time; `IssueSession` stamps now. No route mints a session from another session.
- `IssueSSOSession` stores the provider's `sid` on the session so back-channel logout can end it, and fails with `store.ErrSSOSessionEnded`, setting no cookie, when that login was already ended.
- Active sessions are stored with SHA-256 hashed tokens and verified against secure HttpOnly / SameSite cookies or Bearer headers.
- Session authentication rejects inactive accounts and deletes their presented session.
- PoW solutions must carry unexpired server-signed challenge metadata, signed under `DeriveKey(sessionSecret, "kycalendar:pow")`, never the bare session secret. Each solution verifies once: `VerifyPoWSolution` records its salt in the caller's `PoWSpent` until the challenge expires. At 100 000 entries it drops expired, then random live entries rather than refusing, so a flood cannot fail every login.
- CSRF tokens are HMAC-SHA256 under `DeriveKey(sessionSecret, "kycalendar:csrf")` of `session:<raw session token>` (`SessionCSRFToken`) or `presession:<ky_presession value>` (`PreSessionCSRFToken`), checked in constant time against `X-CSRF-Token`; the `ky_csrf` cookie only carries the token to the SPA and is never compared. `IssueSession` writes the session token and clears `ky_presession`; `IssuePreSession` always mints a fresh random value (HttpOnly, browser-session cookie), never adopting a presented one; `RevokeSession` clears the session cookie and issues a pre-session.
- TOTP secrets are sealed with `SealTOTPSecret` (AES-256-GCM under `DeriveKey(encryptionKey, "kycalendar:totp")`) and opened with `OpenTOTPSecret`; nothing stores or reads them under the bare deployment key.
- TOTP authentication uses the pinned shared primitive's 16-decoded-byte floor.
  Preserve readable sealed legacy records and recovery methods while refusing
  authentication with a short key; generation remains 20 random bytes.
- Password verification creates a five-minute opaque MFA transaction; MFA endpoints never accept a user ID and consume the transaction once.
- Single-use recovery codes are invalidated immediately upon redemption.
- `ClientIP` is the one client-address helper: the rate limiter and session binding both use it. `X-Forwarded-For` is honoured only when the peer is in `KY_TRUSTED_PROXIES`, walking the chain from the right past trusted hops; `X-Real-IP` is ignored. Each entry may be a bare IP or `ip:port` (Azure App Service/Application Gateway append the port); anything else ends the walk and falls back to the peer.

- Cookie/Bearer authentication rechecks installer-owned identity authority on each request. `TrustedPeer` exposes the same peer allowlist decision used by `ClientIP` for the installer's public network self-check.

## Verification
- `go test -v ./internal/auth/...`

## Child DOX Index
None.

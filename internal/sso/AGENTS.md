# SSO

## Purpose
Provides Single Sign-On for KySignOn (OIDC with PKCE), KySignOn's signed directory webhook, and SAML 2.0 Service Provider metadata.

## Ownership
Owns the application adapters around OAuth/OIDC login, KySignOn HMAC-SHA256 signed directory sync webhooks, and SAML metadata publication.

## Local Contracts
- `KySignOnClient.HandleSyncWebhook` verifies HMAC-SHA256 signatures before modifying local user state.
- PKCE with `S256` is enforced on all OAuth/OIDC authorization requests.
- ID tokens require provider signature, issuer, audience, expiry, and one-time nonce verification before claims are trusted.
- OAuth discovery, authorization URLs, PKCE parameters, code exchange, and token verification are delegated to `golang.org/x/oauth2` and `coreos/go-oidc`; application code only maps verified claims.
- SAML assertion parsing is not implemented locally; metadata XML uses `encoding/xml` and no ACS route is exposed until a maintained SAML service-provider library is configured.
- Directory webhook timestamps are accepted only within five minutes, and an empty user `id` is refused. The `id` resolves to a `kysignon` user, else the `scim` user with that subject (KyIdentity's SCIM `externalId` is the same ID). A status change, or any update for a stored admin, revokes the user's sessions and app passwords before the change is stored, so a failed write still signs them out; the stored role is untouched and the next login re-evaluates it. Only a missing user is ignored on deactivate and delete; other store errors are returned. SCIM owns a SCIM-provisioned user: for one, the webhook only takes access away (deactivates, ends an admin's sessions) and never reactivates it, rewrites its profile or deletes it; `user.deleted` deactivates it and SCIM's own DELETE removes it. The webhook never sets the role: it has no `role` field, and new users are `user`. ID tokens are read for `roles` (`access.RoleValues`), never `role`.
- Sign-in settings live in settings keys `signin_provider`, `signin_display_name`, `signin_issuer`, `signin_client_id`, `signin_client_secret_sealed` and `signin_bound`. `Resolve` merges the environment over them, field by field, and reports each field's source. `KY_KYSIGNON_ISSUER`, `_CLIENT_ID` and `_SECRET` fix the provider to `kyidentity`; `KY_KYSIGNON_HMAC_SECRET` alone never changes it, because a webhook secret must not make a generic provider's `roles` claim grant administrator. `SealSecret`/`OpenSecret` encrypt under `DeriveKey(master, SecretLabel)`, so the sealed secret opens under no other key.
- A capsule restore brings sign-in settings back: the settings rows travel in `data/kycalendar.db` and the data-volume key that opens the sealed secret in `data/encryption.key`. Not drill-checked.

## Verification
- `go test -v ./internal/sso/...`

## Child DOX Index
None.

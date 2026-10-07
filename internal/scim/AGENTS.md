# SCIM

## Purpose
Adapts the `elimity-com/scim` RFC 7643/7644 server to the local user and group stores for enterprise provisioning from Okta, Azure AD / Microsoft Entra ID, OneLogin, and KySignOn.

## Ownership
Owns local persistence adapters and bearer authentication; the library owns `/scim/v2/Users`, `/scim/v2/Groups`, discovery endpoints, schema validation, filter/PATCH parsing, pagination, and SCIM error serialization.

## Local Contracts
- `POST /Users` with an `externalId` that names an existing `kysignon` user adopts that row (same ID, provider kept) through the Replace path, with its revocation, and audits `scim.user.adopt`. This join assumes the SCIM source is KyIdentity, whose `externalId` is its user ID and the KySignOn `sub`; do not feed SCIM from another IdP while KySignOn is enabled, or unrelated accounts with colliding IDs would be joined.
- Content-Type for all SCIM endpoints must be `application/scim+json`.
- Requests must be authenticated with the configured bearer token.
- User de-provisioning via `PATCH` with `active: false` updates user status to `inactive`. Any status or role change revokes the user's sessions and app passwords before the change is stored (`save`); a failed revocation or write returns an error and stores nothing, so no old session runs under the new role.
- No roles means no admin grant: PUT without roles or with an empty list, PATCH `remove` on `roles` (matched by attribute name, so URN-prefixed and value-filtered paths count) and PATCH replace with an empty value all set role `user` (and so revoke grants). Other PATCH `remove` operations are ignored.
- Only the value `kycalendar.admin` in `roles` makes an admin (`access.IsAdmin`); any other value is an everyday user. Admins are emitted with `roles: [{value: kycalendar.admin}]`.
- The only supported filter is one exact `eq` on `userName`, `email`/`emails`/`emails.value` or `displayName`, matched case-insensitively on that attribute alone; anything else is `invalidFilter`.
- SCIM protocol models and parsing must come from `github.com/elimity-com/scim`; do not add parallel local request/response implementations.

## Verification
- `go test -v ./internal/scim/...`

## Child DOX Index
None.

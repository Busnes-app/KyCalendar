# SCIM

## Purpose
Adapts the `elimity-com/scim` RFC 7643/7644 server to the local user and group stores for enterprise provisioning from Okta, Azure AD / Microsoft Entra ID, OneLogin, and KySignOn.

## Ownership
Owns local persistence adapters and bearer authentication; the library owns `/scim/v2/Users`, `/scim/v2/Groups`, discovery endpoints, schema validation, filter/PATCH parsing, pagination, and SCIM error serialization.

## Local Contracts
- Content-Type for all SCIM endpoints must be `application/scim+json`.
- Requests must be authenticated with the configured bearer token.
- User de-provisioning via `PATCH` with `active: false` updates user status to `inactive`; any status or role change also revokes the user's sessions and app passwords.
- No roles means no admin grant: PUT without roles or with an empty list, PATCH `remove` on `roles` and PATCH replace with an empty value all set role `user` (and so revoke grants). Other PATCH `remove` operations are ignored.
- The only supported filter is one exact `eq` on `userName`, `email`/`emails`/`emails.value` or `displayName`, matched case-insensitively on that attribute alone; anything else is `invalidFilter`.
- SCIM protocol models and parsing must come from `github.com/elimity-com/scim`; do not add parallel local request/response implementations.

## Verification
- `go test -v ./internal/scim/...`

## Child DOX Index
None.

# SCIM

## Purpose
Adapts the `elimity-com/scim` RFC 7643/7644 server to the local user and group stores for enterprise provisioning from Okta, Azure AD / Microsoft Entra ID, OneLogin, and KySignOn.

## Ownership
Owns local persistence adapters and bearer authentication; the library owns `/scim/v2/Users`, `/scim/v2/Groups`, discovery endpoints, schema validation, filter/PATCH parsing, pagination, and SCIM error serialization.

## Local Contracts
- `POST /Users` with an `externalId` that names an existing `kysignon` user whose `sso_issuer` equals the stored binding adopts that row; one of another binding (or unstamped) is never adopted: 409 `uniqueness`. Adoption keeps the row (same ID, provider kept) and goes through the Replace path, with its revocation, and audits `scim.user.adopt`. This join assumes the SCIM source is KyIdentity, whose `externalId` is its user ID and the KySignOn `sub`; do not feed SCIM from another IdP while KySignOn is enabled, or unrelated accounts with colliding IDs would be joined.
- A created user is stamped with the stored sign-in binding (`sso.Bound`, "" when none); replace, patch and adoption never change `sso_issuer`, so re-activating a row the binding moved away from does not let the new provider sign in as it.
- Groups have an owner (`groups.source`). SCIM creates `scim` groups and lists, reads, replaces, patches and deletes only those; a local group is 404 to SCIM, so a reconciling IdP never sees, renames, empties or deletes one. A create whose name a local group holds in any case is a 409 and sets setting `ConflictKey(name)`, which the Groups screen flags until the local group is renamed or deleted.
- Local accounts (`sso_provider = 'local'`) are not SCIM's, as with local groups: the user list and its filters omit them and their count (`UserFilter.SSOOnly`); GET, PUT, PATCH and DELETE on one are 404 with no write; a create whose `userName` a local account holds in any case is 409 `uniqueness`. Group members a create, PUT or PATCH names must be existing non-local accounts (`UserStore.SSOUserIDs`), else the whole request is 400 `invalidValue` before any write (an unknown and a local ID answer alike). A local membership a SCIM group already holds is neither reported nor removed by SCIM; the admin Groups screen still shows it.
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

# KyCalendar Plan 2: Group Calendars and Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Group calendars that KyIdentity groups reach through `reader`, `editor` and `manager` grants, an administrator role taken only from the `kycalendar.admin` app role, and an authorization matrix that fails when any route is left out.

**Architecture:** A pure resolver (`internal/access`) turns a user, a calendar and the user's grants into one role. The CalDAV backend and the JSON handlers load the facts once per request and ask the resolver. Grants live in a new `calendar_grants` table that cascades away when either the calendar or the group goes. The admin grant is decided at the identity boundaries: the SSO login, SCIM and the KySignOn webhook. Every route is registered through one recorder, and the matrix test walks it.

**Tech Stack:** Go 1.x on the `ky_server_base` scaffold, `database/sql` over SQLite (modernc) and PostgreSQL 17, the vendored `third_party/go-webdav` fork, React 19 + TypeScript + vitest.

**Spec:** `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md`, section 1 (identity, ownership and access), plus the access rows of sections 2 and 4. This is Plan 2 of 4 for v1a. Plan 1 (core CalDAV) is merged. Plan 3 covers recurrence, the JSON event API and the FullCalendar UI. Plan 4 covers KyRecovery backup, restore and the interop gate.

## Global Constraints

- Every calendar has one owner, a user or a group. Personal calendars are owner-only, with no per-user sharing in v1a.
- Group calendars: the KyCalendar admin creates them and grants KyIdentity groups a role. The roles are `reader` (see events), `editor` (create, edit, delete events) and `manager` (rename, recolour, change grants). A user's effective role is the highest across their groups. Group calendars belong to the organisation and survive offboarding.
- The KyCalendar admin role comes from the `roles` claim (the KyPost `AppAdmin` pattern); the global `role` claim is ignored. The admin value is the KyIdentity app role `kycalendar.admin`, and only an exact match counts.
- An admin identity can create group calendars, manage grants, run backups and read the audit log. It cannot hold a personal calendar, read events or create app passwords. This is enforced in the authorization layer for the API and CalDAV, not by hiding UI.
- Deactivation revokes sessions and app passwords immediately and keeps personal calendars. Reactivation restores them; group access follows current membership only.
- Group deletion removes its grants. Its calendars stay unassigned until an admin re-grants or deletes them. Deleting a group calendar is a separate step-up admin action.
- One app password covers the user's personal calendars and every group calendar they can read, each exposed read-only or writable by role. Writes from a reader get 403. Admin identities get 403 on every CalDAV route, discovery included.
- Logs and audit rows carry IDs only: never passwords, tokens or event content.
- Authorization matrix: every API and CalDAV route × owner, reader, editor, manager, non-member, admin identity and deactivated user. The test fails when a registered route is missing from the matrix.
- Never edit existing migrations. This plan adds migration 8.
- Both stores must pass: `make ci` runs SQLite; `make test-postgres` runs PostgreSQL when an instance is available.
- `web/dist` is committed and embedded. Rebuild and commit it after any frontend change.

Decisions this plan makes that the spec leaves open:

- **Group calendar rows.** A group calendar is stored with `owner_kind = 'group'` and `owner_id` = its own calendar ID. It is never owned by a KyIdentity group, so deleting a group cannot orphan it. The per-owner quotas (`KY_CALENDAR_MAX_OBJECTS_PER_USER`, `KY_CALENDAR_MAX_BYTES_PER_USER`) therefore apply to each group calendar on its own, and `KY_CALENDAR_MAX_BYTES_TOTAL` still caps everything. The `calendars.owner_kind` CHECK already allows `'group'`, so the `calendars` table needs no change.
- **CalDAV path.** A group calendar appears in each member's home as `/dav/<user-id>/calendars/_<calendar-id>/`. Personal slugs must start with a letter or digit, so a personal slug can never collide with a group calendar's segment.
- **Not found vs. forbidden.** A caller who cannot read a calendar gets 404, because a calendar they cannot see is indistinguishable from one that does not exist. A caller who can read it but lacks the role for the action gets 403.
- **Step-up.** Deleting a group calendar requires that the session's credentials were verified within the last 10 minutes (`store.Session.CreatedAt`). Otherwise the answer is 403 with `{"code":"reauth_required"}`. The scaffold has no other step-up mechanism.
- **When admin is decided.** The admin role is re-evaluated at every SSO login and on every SCIM user write. A change revokes the user's sessions and app passwords. Local `init-admin` accounts are break-glass and keep their stored role.
- **Manager group picker.** Managers change grants through `PUT`/`DELETE /api/calendars/{id}/grants/{group}`. Only admins can list the group directory in this plan; Plan 3 decides how managers pick a group.

Out of scope: the JSON calendar and event API and the web calendar (Plan 3); the manager screen (Plan 3); backup and restore (Plan 4); migrating existing mixed-use accounts (its own spec). A user promoted to admin keeps their personal calendar rows untouched, unreachable while they are an admin, for that migration.

## Review Focus

1. **Membership removed while a phone keeps syncing.** The next CalDAV request with the same app password must lose access to the group calendar: nothing caches grants across requests. Pinned in Task 4 (`TestMembershipRemovalTakesEffectNextRequest`).
2. **A KyIdentity group is deleted.** Its grants vanish, the calendar stays, the admin list shows it with no grants, and former members get 404. Pinned in Task 5 (`TestGroupDeletionLeavesCalendarUnassigned`).
3. **A token whose `roles` carries the global `admin`, another product's admin role, or a near miss.** This happens when the KyCalendar app in KyIdentity has no app roles yet, so KyIdentity copies the global role into `roles`. It must not make an admin. Pinned in Task 1 (`TestIsAdminExactMatch`, `TestSCIMAdminNeedsTheAppRole`).
4. **An admin deletes a group calendar from a session signed in hours ago.** The answer is 403 `reauth_required` and nothing is deleted. Pinned in Task 5 (`TestDeleteGroupCalendarNeedsRecentSignIn`).
5. **An everyday user is promoted to admin by the claim.** Their app passwords stop working and DAV refuses them, but their personal calendar survives for the later migration. Pinned in Task 1 (`TestUpsertSSOUserFollowsRolesClaim`).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/access/admin.go` (new) | `AdminAppRole`, `IsAdmin`, `RoleValues`: reading the admin grant from a claim or SCIM attribute | 1 |
| `internal/access/role.go` (new) | `Role`, `ParseRole`, `Resolve`: the permission resolver | 3 |
| `internal/access/AGENTS.md` (new) | Contract for the pure access package | 1, 3 |
| `internal/sso/{sso,oauth,kysignon}.go` | Parse `roles`; the webhook ignores the global role | 1 |
| `internal/api/sso_handlers.go` | `upsertSSOUser`: the admin grant follows the claim at every login | 1 |
| `internal/scim/handler.go` | SCIM `roles` maps to admin only for `kycalendar.admin` | 1 |
| `internal/store/{models,store,calendars}.go`, `migrations/migrations.go` | `CalendarGrant`, migration 8, grant and calendar lookups | 2 |
| `internal/davbackend/backend.go`, `internal/api/dav.go` | Group calendars in the home, privileges and write checks by role | 4 |
| `internal/calendar/props.go` (new) | `CheckProps`, moved out of davbackend so the API shares it | 5 |
| `internal/api/server.go`, `internal/api/app_passwords.go` | `authenticate`, `requireSession`, the session user in context; route recorder | 5, 6 |
| `internal/api/group_calendars.go` (new) | Admin group calendar routes, grant routes, groups and audit lists | 5 |
| `internal/api/authz_matrix_test.go` (new) | The route-complete authorization matrix | 6 |
| `web/src/pages/GroupCalendars.tsx` (new) | Admin screen: create, grant, delete | 7 |
| `AGENTS.md`, `README.md` | Root contracts and operator setup for `kycalendar.admin` | 8 |

---

### Task 1: The admin grant comes from the `kycalendar.admin` app role

**Files:**
- Create: `internal/access/admin.go`, `internal/access/admin_test.go`, `internal/access/AGENTS.md`
- Modify: `internal/sso/sso.go` (`IdentityClaims`), `internal/sso/oauth.go:78-97` (`claimsFromIDToken`), `internal/sso/kysignon.go:46-130` (`KySignOnSyncPayload`, `HandleSyncWebhook`), `internal/sso/sso_test.go:61-70`
- Modify: `internal/api/sso_handlers.go:46-96` (`handleKySignOnCallback`)
- Create: `internal/api/sso_upsert_internal_test.go`
- Modify: `internal/scim/handler.go` (`roleOrUser` and its three callers, `userResource`), `internal/scim/scim_test.go`
- Docs: `internal/sso/AGENTS.md`, `internal/scim/AGENTS.md`, `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: nothing new.
- Produces: `access.AdminAppRole = "kycalendar.admin"`; `access.IsAdmin(roles []string) bool`; `access.RoleValues(v any) []string`; `sso.IdentityClaims.Roles []string` (replaces `Role string`); `(*api.Server).upsertSSOUser(ctx context.Context, claims *sso.IdentityClaims) (*store.User, error)` with sentinel errors `errNotProvisioned`, `errAccountInactive`. `store.User.Role` keeps its values `"admin"` and `"user"`, and every existing `Role == "admin"` check stays.

- [ ] **Step 1: Write the failing access test**

`internal/access/admin_test.go`:

```go
package access_test

import (
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
)

// Only the exact app role makes an administrator. KyIdentity copies the global role into
// `roles` for an app that has no app roles yet, so "admin" must not count.
func TestIsAdminExactMatch(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"bare string list", []any{"kycalendar.admin"}, true},
		{"scim objects", []any{map[string]any{"value": "kycalendar.admin", "primary": true}}, true},
		{"single string", "kycalendar.admin", true},
		{"typed list", []string{"x", "kycalendar.admin"}, true},
		{"global admin", []any{"admin"}, false},
		{"other product", []any{"kypost.admin"}, false},
		{"case differs", []any{"KyCalendar.Admin"}, false},
		{"longer name", []any{"kycalendar.admin.extra"}, false},
		{"absent", nil, false},
		{"empty", []any{}, false},
		{"wrong shapes", []any{42, map[string]any{"value": 7}, []any{"kycalendar.admin"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := access.IsAdmin(access.RoleValues(tc.in)); got != tc.want {
				t.Fatalf("IsAdmin(RoleValues(%#v)) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/access/`
Expected: FAIL, `no non-test Go files` / undefined `access.IsAdmin`.

- [ ] **Step 3: Write `internal/access/admin.go`**

```go
// Package access decides what a user may do with a calendar. It is pure: callers load the
// user, the calendar and the user's grants, and ask.
package access

import "slices"

// AdminAppRole is the KyIdentity app role that makes an identity a KyCalendar administrator.
// Only an exact match counts: KyIdentity's global `role` is never a product admin.
const AdminAppRole = "kycalendar.admin"

// IsAdmin reports whether roles grant KyCalendar administration.
func IsAdmin(roles []string) bool { return slices.Contains(roles, AdminAppRole) }

// RoleValues reads a `roles` ID-token claim or SCIM attribute: one string, or a list of strings
// and {"value": string} objects. Any other shape contributes nothing.
func RoleValues(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case map[string]any:
		if s, ok := t["value"].(string); ok {
			return []string{s}
		}
	case []any:
		var out []string
		for _, e := range t {
			switch e := e.(type) {
			case string:
				out = append(out, e)
			case map[string]any:
				if s, ok := e["value"].(string); ok {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `go test ./internal/access/`
Expected: PASS.

- [ ] **Step 5: Write the failing SSO upsert test**

`internal/api/sso_upsert_internal_test.go` (package `api`, reusing `davInternalServer` from `dav_auth_internal_test.go`):

```go
package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// The admin grant follows the `roles` claim at every login. A change revokes app passwords,
// so a promoted user's phones stop syncing, but the personal calendar stays for migration.
func TestUpsertSSOUserFollowsRolesClaim(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	claims := &sso.IdentityClaims{Subject: "sub-1", PreferredUsername: "carol", Provider: "kysignon"}

	u, err := s.upsertSSOUser(ctx, claims)
	if err != nil || u.Role != "user" {
		t.Fatalf("first login: %+v %v", u, err)
	}
	cal := &store.Calendar{ID: "cal_carol", OwnerKind: "user", OwnerID: u.ID, Slug: "default", Name: "Calendar"}
	if err := s.store.Calendars().CreateCalendar(ctx, cal, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.store.AppPasswords().Create(ctx, &store.AppPassword{ID: "pw_carol", UserID: u.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}

	claims.Roles = []string{access.AdminAppRole}
	if u, err = s.upsertSSOUser(ctx, claims); err != nil || u.Role != "admin" {
		t.Fatalf("promotion: %+v %v", u, err)
	}
	if list, _ := s.store.AppPasswords().ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatal("promotion kept the app passwords")
	}
	if _, err := s.store.Calendars().GetCalendarBySlug(ctx, "user", u.ID, "default"); err != nil {
		t.Fatalf("promotion removed the personal calendar: %v", err)
	}

	claims.Roles = []string{"admin", "kypost.admin"}
	if u, err = s.upsertSSOUser(ctx, claims); err != nil || u.Role != "user" {
		t.Fatalf("global admin must not keep the grant: %+v %v", u, err)
	}
	recs, _, _ := s.store.Audit().ListAuditRecords(ctx, 0, 50)
	changes := 0
	for _, r := range recs {
		if r.Action == "sso.role_changed" {
			changes++
		}
	}
	if changes != 2 {
		t.Fatalf("want 2 sso.role_changed audit rows, got %d", changes)
	}

	u.Status = "inactive"
	if err := s.store.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertSSOUser(ctx, claims); !errors.Is(err, errAccountInactive) {
		t.Fatalf("inactive login: want errAccountInactive, got %v", err)
	}
}
```

- [ ] **Step 6: Run it to see it fail**

Run: `go test ./internal/api/ -run TestUpsertSSOUserFollowsRolesClaim`
Expected: FAIL to compile (`claims.Roles`, `s.upsertSSOUser`, `errAccountInactive` undefined).

- [ ] **Step 7: Parse `roles` in the SSO claims**

In `internal/sso/sso.go`, replace the `Role` field of `IdentityClaims`:

```go
	// Roles are the token's `roles` claim: KyIdentity app roles for this client only.
	Roles    []string `json:"roles,omitempty"`
```

In `internal/sso/oauth.go` `claimsFromIDToken`, replace the `Role string` field of `raw` with `Roles any \`json:"roles"\``. Add the import `"github.com/Busnes-app/kycalendar/internal/access"` and change the return to:

```go
	// The global `role` claim is deliberately not read: it is never a KyCalendar admin.
	return &IdentityClaims{Subject: raw.Sub, Email: raw.Email, Name: raw.Name, PreferredUsername: username, Roles: access.RoleValues(raw.Roles)}, nil
```

Run `grep -rn 'claims.Role\b\|\.Role:' internal/sso internal/api` and confirm no other reader of `IdentityClaims.Role` remains.

- [ ] **Step 8: The webhook ignores the global role**

In `internal/sso/kysignon.go`, delete the `Role` field from `KySignOnSyncPayload`, so the field cannot be mapped by accident. Replace the `"user.created", "user.updated"` case body with:

```go
	case "user.created", "user.updated":
		existing, err := k.store.Users().GetUserBySSO(ctx, "kysignon", payload.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		status := payload.Status
		if status == "" {
			status = "active"
		}

		// The admin grant comes from the `roles` claim at login and SCIM `roles`, never from
		// this webhook, whose legacy `role` is KyIdentity's global role.
		if existing != nil {
			statusChanged := existing.Status != status
			existing.Username = payload.Username
			existing.Email = payload.Email
			existing.DisplayName = payload.DisplayName
			existing.Status = status
			if err := k.store.Users().UpdateUser(ctx, existing); err != nil {
				return err
			}
			if statusChanged {
				if err := k.store.Sessions().DeleteUserSessions(ctx, existing.ID); err != nil {
					return err
				}
				return k.store.AppPasswords().DeleteByUser(ctx, existing.ID)
			}
			return nil
		}

		newUser := &store.User{
			ID:          fmt.Sprintf("usr_%s", crypto.RandomHex(12)),
			Username:    payload.Username,
			Email:       payload.Email,
			DisplayName: payload.DisplayName,
			Role:        "user",
			Status:      status,
			SSOProvider: "kysignon",
			SSOSubject:  payload.ID,
		}
		return k.store.Users().CreateUser(ctx, newUser)
```

In `internal/sso/sso_test.go`, remove the `Role: "user",` line from the payload literal in `TestKySignOnWebhookSync`. Then append this test:

```go
// The webhook's legacy role is the global KyIdentity role: it never grants admin.
func TestKySignOnWebhookIgnoresGlobalRole(t *testing.T) {
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "webhook-secret-999"
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	body, _ := json.Marshal(map[string]any{
		"event": "user.created", "id": "ext-admin", "username": "root", "role": "admin",
		"status": "active", "timestamp": time.Now().Unix(),
	})
	if err := client.HandleSyncWebhook(context.Background(), body, crypto.ComputeHMACSHA256(body, secret)); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "ext-admin")
	if err != nil || u.Role != "user" {
		t.Fatalf("webhook role leaked: %+v %v", u, err)
	}
}
```

- [ ] **Step 9: `upsertSSOUser` and the callback**

In `internal/api/sso_handlers.go`, add `"context"` and `"errors"` to the imports, plus `"github.com/Busnes-app/kycalendar/internal/access"` and `"github.com/Busnes-app/kycalendar/internal/sso"`. Add:

```go
var (
	errNotProvisioned  = errors.New("user account not provisioned")
	errAccountInactive = errors.New("account is not active")
)

// upsertSSOUser maps a verified login onto a local user. The admin grant follows the token's
// `roles` claim on every login; a change revokes the user's sessions and app passwords first.
func (s *Server) upsertSSOUser(ctx context.Context, claims *sso.IdentityClaims) (*store.User, error) {
	role := "user"
	if access.IsAdmin(claims.Roles) {
		role = "admin"
	}
	user, err := s.store.Users().GetUserBySSO(ctx, claims.Provider, claims.Subject)
	if errors.Is(err, store.ErrNotFound) {
		if !s.config.SSO.AutoProvision {
			return nil, errNotProvisioned
		}
		user = &store.User{
			ID:          fmt.Sprintf("usr_%s", crypto.RandomHex(12)),
			Username:    claims.PreferredUsername,
			Email:       claims.Email,
			DisplayName: claims.Name,
			Role:        role,
			Status:      "active",
			SSOProvider: claims.Provider,
			SSOSubject:  claims.Subject,
		}
		return user, s.store.Users().CreateUser(ctx, user)
	}
	if err != nil {
		return nil, err
	}
	if user.Status != "active" {
		return nil, errAccountInactive
	}
	if user.Role == role {
		return user, nil
	}
	from := user.Role
	user.Role = role
	if err := s.store.Users().UpdateUser(ctx, user); err != nil {
		return nil, err
	}
	if err := s.store.Sessions().DeleteUserSessions(ctx, user.ID); err != nil {
		return nil, err
	}
	if err := s.store.AppPasswords().DeleteByUser(ctx, user.ID); err != nil {
		return nil, err
	}
	_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: user.ID, Action: "sso.role_changed", Resource: user.ID, Details: "from=" + from + " to=" + role})
	return user, nil
}
```

In `handleKySignOnCallback`, replace everything from `// Upsert user` up to `IssueSession` with:

```go
	user, err := s.upsertSSOUser(r.Context(), claims)
	switch {
	case errors.Is(err, errNotProvisioned):
		s.writeError(w, http.StatusForbidden, "User account not provisioned")
		return
	case errors.Is(err, errAccountInactive):
		s.writeError(w, http.StatusForbidden, "Account is not active")
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to provision SSO user")
		return
	}
```

`ExchangeCode` already sets `claims.Provider = "kysignon"`.

- [ ] **Step 10: SCIM `roles` grants admin only for the app role**

In `internal/scim/handler.go`, import `"github.com/Busnes-app/kycalendar/internal/access"` and replace `roleOrUser` with:

```go
// roleFromSCIM is "admin" only when the IdP sends the KyCalendar app role; any other value,
// including KyIdentity's global "admin", is an everyday user.
func roleFromSCIM(value interface{}) string {
	if access.IsAdmin(access.RoleValues(value)) {
		return "admin"
	}
	return "user"
}
```

Rename the three callers (in `Create`, `Replace` and `applyUserValue`) from `roleOrUser` to `roleFromSCIM`. In `userResource`, replace the `if user.Role != ""` block with:

```go
	if user.Role == "admin" {
		attrs["roles"] = []interface{}{map[string]interface{}{"value": access.AdminAppRole, "primary": true}}
	}
```

Append to `internal/scim/scim_test.go`:

```go
func TestSCIMAdminNeedsTheAppRole(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	handler := srv.AuthMiddleware(mux)

	for value, want := range map[string]string{"kycalendar.admin": "admin", "admin": "user", "kypost.admin": "user"} {
		name := "u_" + strings.NewReplacer(".", "_").Replace(value)
		body := map[string]any{"schemas": []string{scim.SchemaUser}, "userName": name, "active": true,
			"roles": []any{map[string]any{"value": value, "primary": true}}}
		if w := scimDo(t, handler, token, "POST", "/scim/v2/Users", body); w.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", value, w.Code, w.Body.String())
		}
		u, err := st.Users().GetUserByUsername(ctx, name)
		if err != nil || u.Role != want {
			t.Fatalf("roles %q: role %q, want %q (%v)", value, u.Role, want, err)
		}
	}
}

// Deactivation revokes app passwords and keeps the personal calendar; reactivation finds it.
func TestSCIMDeactivationKeepsCalendars(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	handler := srv.AuthMiddleware(mux)

	id := "usr_dana"
	if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: "dana", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_dana", OwnerKind: "user", OwnerID: id, Slug: "default", Name: "Calendar"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: "pw_dana", UserID: id, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	patch := func(active bool) {
		body := map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "active", "value": active}}}
		if w := scimDo(t, handler, token, "PATCH", "/scim/v2/Users/"+id, body); w.Code != http.StatusOK {
			t.Fatalf("PATCH active=%v: %d %s", active, w.Code, w.Body.String())
		}
	}
	patch(false)
	if u, _ := st.Users().GetUserByID(ctx, id); u.Status != "inactive" {
		t.Fatalf("status %q after deactivation", u.Status)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, id); len(list) != 0 {
		t.Fatal("deactivation kept the app passwords")
	}
	patch(true)
	if _, err := st.Calendars().GetCalendarBySlug(ctx, "user", id, "default"); err != nil {
		t.Fatalf("the personal calendar did not survive deactivation: %v", err)
	}
}
```

- [ ] **Step 11: Run the package tests**

Run: `go test ./internal/access/ ./internal/sso/ ./internal/scim/ ./internal/api/`
Expected: PASS, including the existing `TestSCIMRemovingRolesDemotesAdmin`. Removing any roles value still demotes.

- [ ] **Step 12: DOX**

- Create `internal/access/AGENTS.md`:

```markdown
# internal/access

## Purpose
Pure authorization decisions for calendars: who is an administrator, and what role a user holds on one calendar.

## Ownership
Owns `AdminAppRole`, `IsAdmin`, `RoleValues` and (Task 3) the role resolver. Callers load users, calendars and grants; nothing here touches the store, HTTP or the clock.

## Local Contracts
- The KyCalendar administrator is the KyIdentity app role `kycalendar.admin`, exact match only. The global `role` claim, the webhook `role` field and values such as `admin` or another product's role never grant it.
- `RoleValues` accepts a string or a list of strings and `{"value": string}` objects (ID-token claim or SCIM attribute); other shapes contribute nothing.

## Verification
- `go test ./internal/access/`

## Child DOX Index
None.
```

- In `internal/sso/AGENTS.md`, replace the webhook line with: "Directory webhook timestamps are accepted only within five minutes. A status change revokes the user's sessions and app passwords. The webhook never sets the role: it has no `role` field, and new users are `user`. ID tokens are read for `roles` (`access.RoleValues`), never `role`."
- In `internal/scim/AGENTS.md`, add this after the "No roles means no admin grant" line: "Only the value `kycalendar.admin` in `roles` makes an admin (`access.IsAdmin`); any other value is an everyday user. Admins are emitted with `roles: [{value: kycalendar.admin}]`."
- In `internal/api/AGENTS.md`, after the `requireSSO` line add: "`upsertSSOUser` decides the admin grant from the `roles` claim at every KySignOn login (`kycalendar.admin` only). A change updates the user, revokes sessions and app passwords and audits `sso.role_changed`. Inactive users get 403 and no session."

- [ ] **Step 13: Commit**

```bash
git add internal/access internal/sso internal/scim internal/api/sso_handlers.go internal/api/sso_upsert_internal_test.go internal/api/AGENTS.md
git commit -m "feat(access)!: take the admin grant only from the kycalendar.admin app role"
```

---

### Task 2: Grants schema and store

**Files:**
- Modify: `internal/store/models.go` (add `CalendarGrant`), `internal/store/store.go` (`CalendarStore`), `internal/store/calendars.go`, `internal/store/migrations/migrations.go` (append version 8)
- Create: `internal/store/grants_test.go`
- Docs: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `store.Group`, `GroupStore.CreateGroup/AddGroupMember/RemoveGroupMember/DeleteGroup`.
- Produces, all on `CalendarStore`:
  - `GetCalendarByID(ctx, id string) (*Calendar, error)`
  - `ListCalendarsByKind(ctx, ownerKind string) ([]*Calendar, error)`
  - `DeleteCalendar(ctx, id string) error`: `ErrNotFound` when absent
  - `ListGrants(ctx, calendarID string) ([]CalendarGrant, error)`
  - `SetGrant(ctx, g CalendarGrant) error`: `ErrNotFound` unless the calendar is a group calendar and the group exists
  - `DeleteGrant(ctx, calendarID, groupID string) error`: idempotent
  - `UserGrants(ctx, userID string) ([]CalendarGrant, error)`
  - `type CalendarGrant struct { CalendarID, GroupID, GroupName, Role string }`. `GroupName` is filled on reads and ignored by `SetGrant`.

- [ ] **Step 1: Write the failing store tests**

`internal/store/grants_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// grantWorld has users a and b, groups Readers (a) and Editors (a, b), a group calendar and
// a's personal calendar.
func grantWorld(t *testing.T) store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, id := range []string{"usr_a", "usr_b"} {
		if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: id, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	for id, name := range map[string]string{"grp_r": "Readers", "grp_e": "Editors"} {
		if err := st.Groups().CreateGroup(ctx, &store.Group{ID: id, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range [][2]string{{"grp_r", "usr_a"}, {"grp_e", "usr_a"}, {"grp_e", "usr_b"}} {
		if err := st.Groups().AddGroupMember(ctx, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []*store.Calendar{
		{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g", Slug: "group", Name: "Team"},
		{ID: "cal_p", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Calendar"},
	} {
		if err := st.Calendars().CreateCalendar(ctx, c, 0); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestSetGrantUpsertsAndLists(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	for _, role := range []string{"reader", "editor"} {
		if err := cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := cs.ListGrants(ctx, "cal_g")
	if err != nil || len(got) != 1 || got[0].Role != "editor" || got[0].GroupName != "Readers" {
		t.Fatalf("grants %+v %v", got, err)
	}
}

func TestSetGrantRefusesPersonalOrMissing(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	for name, g := range map[string]store.CalendarGrant{
		"personal calendar": {CalendarID: "cal_p", GroupID: "grp_r", Role: "reader"},
		"missing group":     {CalendarID: "cal_g", GroupID: "grp_x", Role: "reader"},
		"missing calendar":  {CalendarID: "cal_x", GroupID: "grp_r", Role: "reader"},
	} {
		if err := cs.SetGrant(ctx, g); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: want ErrNotFound, got %v", name, err)
		}
	}
}

func TestUserGrantsFollowMembership(t *testing.T) {
	ctx := context.Background()
	st := grantWorld(t)
	cs := st.Calendars()
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: "reader"})
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_e", Role: "editor"})
	if got, err := cs.UserGrants(ctx, "usr_a"); err != nil || len(got) != 2 {
		t.Fatalf("usr_a grants %+v %v", got, err)
	}
	if err := st.Groups().RemoveGroupMember(ctx, "grp_e", "usr_a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := cs.UserGrants(ctx, "usr_a"); len(got) != 1 || got[0].Role != "reader" {
		t.Fatalf("after leaving Editors: %+v", got)
	}
}

func TestDeleteGroupRemovesGrantsKeepsCalendar(t *testing.T) {
	ctx := context.Background()
	st := grantWorld(t)
	cs := st.Calendars()
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: "reader"})
	if err := st.Groups().DeleteGroup(ctx, "grp_r"); err != nil {
		t.Fatal(err)
	}
	if got, _ := cs.ListGrants(ctx, "cal_g"); len(got) != 0 {
		t.Fatalf("grants survived the group: %+v", got)
	}
	if _, err := cs.GetCalendarByID(ctx, "cal_g"); err != nil {
		t.Fatalf("the calendar did not survive the group: %v", err)
	}
}

func TestDeleteCalendarCascades(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: "reader"})
	if _, err := cs.PutObject(ctx, obj("cal_g", "a.ics", "a", "x", 0, nil), "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := cs.DeleteCalendar(ctx, "cal_g"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetObject(ctx, "cal_g", "a.ics"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("object survived: %v", err)
	}
	if got, _ := cs.ListGrants(ctx, "cal_g"); len(got) != 0 {
		t.Fatalf("grants survived: %+v", got)
	}
	if err := cs.DeleteCalendar(ctx, "cal_g"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}
}

func TestDeleteGrantIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	for i := 0; i < 2; i++ {
		if err := cs.DeleteGrant(ctx, "cal_g", "grp_r"); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
}

func TestListCalendarsByKind(t *testing.T) {
	cs := grantWorld(t).Calendars()
	got, err := cs.ListCalendarsByKind(context.Background(), "group")
	if err != nil || len(got) != 1 || got[0].ID != "cal_g" {
		t.Fatalf("group calendars %+v %v", got, err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/store/ -run 'Grant|DeleteCalendar|ByKind'`
Expected: FAIL to compile (`store.CalendarGrant`, `SetGrant` and the rest undefined).

- [ ] **Step 3: Migration 8**

Append to `registry` in `internal/store/migrations/migrations.go`:

```go
	{
		// Grants cascade with their calendar and their group: deleting a KyIdentity group
		// removes its access and leaves the calendar for an admin to re-grant or delete.
		Version: 8,
		Name:    "calendar_grants",
		SQLite: `
CREATE TABLE calendar_grants (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('reader', 'editor', 'manager')),
    PRIMARY KEY (calendar_id, group_id)
);
CREATE INDEX idx_calendar_grants_group ON calendar_grants(group_id);`,
		Postgres: `
CREATE TABLE calendar_grants (
    calendar_id VARCHAR(64) NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    group_id VARCHAR(64) NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    role VARCHAR(16) NOT NULL CHECK (role IN ('reader', 'editor', 'manager')),
    PRIMARY KEY (calendar_id, group_id)
);
CREATE INDEX idx_calendar_grants_group ON calendar_grants(group_id);`,
	},
```

- [ ] **Step 4: Model and interface**

Append to `internal/store/models.go`:

```go
// CalendarGrant gives one group a role ("reader", "editor" or "manager") on one group
// calendar. GroupName is filled on reads and ignored on writes.
type CalendarGrant struct {
	CalendarID, GroupID, GroupName, Role string
}
```

Add to the `CalendarStore` interface in `internal/store/store.go`:

```go
	GetCalendarByID(ctx context.Context, id string) (*Calendar, error)
	ListCalendarsByKind(ctx context.Context, ownerKind string) ([]*Calendar, error)
	// DeleteCalendar removes a calendar with its objects, changes and grants.
	DeleteCalendar(ctx context.Context, id string) error
	ListGrants(ctx context.Context, calendarID string) ([]CalendarGrant, error)
	// SetGrant creates or replaces one group's role; ErrNotFound unless the calendar is a
	// group calendar and the group exists.
	SetGrant(ctx context.Context, g CalendarGrant) error
	DeleteGrant(ctx context.Context, calendarID, groupID string) error // absent is not an error
	// UserGrants lists every grant that reaches userID through group membership.
	UserGrants(ctx context.Context, userID string) ([]CalendarGrant, error)
```

- [ ] **Step 5: Implement in `internal/store/calendars.go`**

Replace `ListCalendarsByOwner` with a shared query helper, and add the new methods:

```go
func (c *calendarStore) queryCalendars(ctx context.Context, query string, args ...any) ([]*Calendar, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Calendar
	for rows.Next() {
		cal, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cal)
	}
	return out, rows.Err()
}

func (c *calendarStore) ListCalendarsByOwner(ctx context.Context, ownerKind, ownerID string) ([]*Calendar, error) {
	return c.queryCalendars(ctx, `SELECT `+calendarCols+` FROM calendars WHERE owner_kind = ? AND owner_id = ? ORDER BY created_at, id`, ownerKind, ownerID)
}

func (c *calendarStore) ListCalendarsByKind(ctx context.Context, ownerKind string) ([]*Calendar, error) {
	return c.queryCalendars(ctx, `SELECT `+calendarCols+` FROM calendars WHERE owner_kind = ? ORDER BY name, id`, ownerKind)
}

func (c *calendarStore) GetCalendarByID(ctx context.Context, id string) (*Calendar, error) {
	return scanCalendar(c.store.db.QueryRowContext(ctx, c.q(`SELECT `+calendarCols+` FROM calendars WHERE id = ?`), id))
}

func (c *calendarStore) DeleteCalendar(ctx context.Context, id string) error {
	res, err := c.store.db.ExecContext(ctx, c.q(`DELETE FROM calendars WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const grantSelect = `SELECT g.calendar_id, g.group_id, gr.display_name, g.role FROM calendar_grants g JOIN groups gr ON gr.id = g.group_id `

func (c *calendarStore) queryGrants(ctx context.Context, rest string, args ...any) ([]CalendarGrant, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(grantSelect+rest), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarGrant
	for rows.Next() {
		var g CalendarGrant
		if err := rows.Scan(&g.CalendarID, &g.GroupID, &g.GroupName, &g.Role); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (c *calendarStore) ListGrants(ctx context.Context, calendarID string) ([]CalendarGrant, error) {
	return c.queryGrants(ctx, `WHERE g.calendar_id = ? ORDER BY gr.display_name, g.group_id`, calendarID)
}

func (c *calendarStore) UserGrants(ctx context.Context, userID string) ([]CalendarGrant, error) {
	return c.queryGrants(ctx, `JOIN group_members m ON m.group_id = g.group_id WHERE m.user_id = ? ORDER BY g.calendar_id, g.group_id`, userID)
}

func (c *calendarStore) SetGrant(ctx context.Context, g CalendarGrant) error {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var found int
	err = tx.QueryRowContext(ctx, c.q(`SELECT (SELECT COUNT(*) FROM calendars WHERE id = ? AND owner_kind = 'group') + (SELECT COUNT(*) FROM groups WHERE id = ?)`),
		g.CalendarID, g.GroupID).Scan(&found)
	if err != nil {
		return err
	}
	if found != 2 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendar_grants (calendar_id, group_id, role) VALUES (?, ?, ?)
ON CONFLICT (calendar_id, group_id) DO UPDATE SET role = excluded.role`), g.CalendarID, g.GroupID, g.Role); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *calendarStore) DeleteGrant(ctx context.Context, calendarID, groupID string) error {
	_, err := c.store.db.ExecContext(ctx, c.q(`DELETE FROM calendar_grants WHERE calendar_id = ? AND group_id = ?`), calendarID, groupID)
	return err
}
```

- [ ] **Step 6: Run the store tests**

Run: `go test ./internal/store/`
Expected: PASS, with every existing test still green. Then, if a Postgres instance is available: `make test-postgres`.

- [ ] **Step 7: DOX**

In `internal/store/AGENTS.md`, after the migration 7 line add:

```markdown
- Migration 8 adds `calendar_grants` (calendar_id and group_id, both `ON DELETE CASCADE`; role `reader|editor|manager`; one row per calendar and group). Deleting a group removes its grants; deleting a calendar removes its objects, changes and grants.
- A group calendar is `owner_kind = 'group'` with `owner_id` = its own calendar ID: no KyIdentity group owns it, so per-owner quotas apply per group calendar. `SetGrant` refuses (`ErrNotFound`) a personal calendar or a missing group; `DeleteGrant` is idempotent; `UserGrants` joins `group_members`, so access follows current membership.
```

Add the new methods to the Ownership list if it names `CalendarStore` methods (it names only interfaces, so leave it).

- [ ] **Step 8: Commit**

```bash
git add internal/store
git commit -m "feat(store): add calendar grants and group calendar lookups"
```

---

### Task 3: The permission resolver

**Files:**
- Create: `internal/access/role.go`, `internal/access/role_test.go`
- Docs: `internal/access/AGENTS.md`

**Interfaces:**
- Consumes: `store.User`, `store.Calendar`, `store.CalendarGrant` (Task 2).
- Produces: `type access.Role int` with constants `None < Reader < Editor < Manager < Owner`; `access.ParseRole(s string) (Role, bool)`; `(Role).CanRead() / CanWrite() / CanManage() bool`; `access.Resolve(u *store.User, c *store.Calendar, grants []store.CalendarGrant) Role`. `grants` must be the user's own grants (`UserGrants`); grants naming other calendars are ignored.

- [ ] **Step 1: Write the failing resolver test**

`internal/access/role_test.go`:

```go
package access_test

import (
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestResolve(t *testing.T) {
	alice := &store.User{ID: "usr_a", Role: "user"}
	root := &store.User{ID: "usr_root", Role: "admin"}
	personal := &store.Calendar{ID: "cal_p", OwnerKind: "user", OwnerID: "usr_a"}
	rootsOwn := &store.Calendar{ID: "cal_r", OwnerKind: "user", OwnerID: "usr_root"}
	group := &store.Calendar{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g"}
	grant := func(cal, role string) store.CalendarGrant { return store.CalendarGrant{CalendarID: cal, Role: role} }

	cases := []struct {
		name   string
		user   *store.User
		cal    *store.Calendar
		grants []store.CalendarGrant
		want   access.Role
	}{
		{"owner of a personal calendar", alice, personal, nil, access.Owner},
		{"someone else's personal calendar", &store.User{ID: "usr_b", Role: "user"}, personal, nil, access.None},
		{"grants never reach a personal calendar", &store.User{ID: "usr_b", Role: "user"}, personal, []store.CalendarGrant{grant("cal_p", "manager")}, access.None},
		{"admin owns nothing, even their own rows", root, rootsOwn, nil, access.None},
		{"admin with a grant still reads nothing", root, group, []store.CalendarGrant{grant("cal_g", "manager")}, access.None},
		{"group without grants", alice, group, nil, access.None},
		{"highest role wins", alice, group, []store.CalendarGrant{grant("cal_g", "reader"), grant("cal_g", "editor")}, access.Editor},
		{"grant on another calendar", alice, group, []store.CalendarGrant{grant("cal_x", "manager")}, access.None},
		{"unknown role string is ignored", alice, group, []store.CalendarGrant{grant("cal_g", "owner"), grant("cal_g", "reader")}, access.Reader},
		{"unknown owner kind", alice, &store.Calendar{ID: "cal_g", OwnerKind: "org"}, []store.CalendarGrant{grant("cal_g", "manager")}, access.None},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := access.Resolve(tc.user, tc.cal, tc.grants); got != tc.want {
				t.Fatalf("Resolve = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRoleCapabilities(t *testing.T) {
	cases := []struct {
		role                  access.Role
		read, write, manage bool
	}{
		{access.None, false, false, false},
		{access.Reader, true, false, false},
		{access.Editor, true, true, false},
		{access.Manager, true, true, true},
		{access.Owner, true, true, true},
	}
	for _, tc := range cases {
		if tc.role.CanRead() != tc.read || tc.role.CanWrite() != tc.write || tc.role.CanManage() != tc.manage {
			t.Errorf("role %v: read %v write %v manage %v", tc.role, tc.role.CanRead(), tc.role.CanWrite(), tc.role.CanManage())
		}
	}
	for _, s := range []string{"", "owner", "none", "Reader"} {
		if _, ok := access.ParseRole(s); ok {
			t.Errorf("ParseRole(%q) accepted", s)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/access/ -run 'Resolve|Capabilities'`
Expected: FAIL to compile (`access.Resolve`, `access.Owner` undefined).

- [ ] **Step 3: Write `internal/access/role.go`**

```go
package access

import "github.com/Busnes-app/kycalendar/internal/store"

// Role is what a user may do with one calendar; each role includes every role below it.
type Role int

const (
	None    Role = iota
	Reader       // sees events
	Editor       // creates, edits and deletes events
	Manager      // also renames, recolours and changes grants
	Owner        // a personal calendar's owner
)

// ParseRole reads a grant's role. Owner and None are never granted.
func ParseRole(s string) (Role, bool) {
	switch s {
	case "reader":
		return Reader, true
	case "editor":
		return Editor, true
	case "manager":
		return Manager, true
	}
	return None, false
}

func (r Role) CanRead() bool   { return r >= Reader }
func (r Role) CanWrite() bool  { return r >= Editor }
func (r Role) CanManage() bool { return r >= Manager }

// Resolve is u's role on c. grants must be u's own grants. Administrators get None on every
// calendar, because they never read events. Personal calendars are owner-only. A group calendar
// takes the highest role among the grants naming it.
func Resolve(u *store.User, c *store.Calendar, grants []store.CalendarGrant) Role {
	if u.Role == "admin" {
		return None
	}
	switch c.OwnerKind {
	case "user":
		if c.OwnerID == u.ID {
			return Owner
		}
	case "group":
		best := None
		for _, g := range grants {
			if r, ok := ParseRole(g.Role); ok && g.CalendarID == c.ID && r > best {
				best = r
			}
		}
		return best
	}
	return None
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `go test ./internal/access/`
Expected: PASS.

- [ ] **Step 5: DOX**

In `internal/access/AGENTS.md` Local Contracts, add:

```markdown
- `Resolve(user, calendar, userGrants)` is the only place a calendar role is decided. Administrators get `None` everywhere. Personal calendars give their owner `Owner` and everyone else `None`, whatever grants exist. Group calendars take the highest valid grant naming that calendar. Unknown role strings and owner kinds resolve to `None`.
- Capabilities: `CanRead` is reader and above, `CanWrite` editor and above, `CanManage` manager and owner (rename, recolour, grants).
```

- [ ] **Step 6: Commit**

```bash
git add internal/access
git commit -m "feat(access): resolve a user's role on a calendar"
```

---

### Task 4: Group calendars over CalDAV

**Files:**
- Modify: `internal/davbackend/backend.go`, `internal/api/dav.go:31-48` (`handleDAV`)
- Create: `internal/api/dav_group_test.go`
- Docs: `internal/davbackend/AGENTS.md`

**Interfaces:**
- Consumes: `access.Resolve`, `access.Role`, `access.Owner` (Task 3); `CalendarStore.GetCalendarByID`, `UserGrants`, `SetGrant` (Task 2).
- Produces: a `davbackend.Backend.Grants []store.CalendarGrant` field that `handleDAV` fills from `UserGrants` once per request. The group segment is `"_" + calendar ID`. Test helpers in package `api_test`, which Task 6 reuses:
  - `groupCalendar(t *testing.T, st store.Store, name string) *store.Calendar`
  - `grantRole(t *testing.T, st store.Store, cal *store.Calendar, role, userID string)`, which creates group `grp_<role>_<calendar id>`, adds the user and grants the role
  - `eventICS(uid string) string`

- [ ] **Step 1: Write the failing CalDAV tests**

`internal/api/dav_group_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func eventICS(uid string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nDTEND:20261007T100000Z\r\nSUMMARY:m\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

// groupCalendar stores a group calendar the way the admin route creates one.
func groupCalendar(t *testing.T, st store.Store, name string) *store.Calendar {
	t.Helper()
	id := "cal_" + uuid.NewString()
	c := &store.Calendar{ID: id, OwnerKind: "group", OwnerID: id, Slug: "group", Name: name}
	if err := st.Calendars().CreateCalendar(context.Background(), c, 0); err != nil {
		t.Fatal(err)
	}
	return c
}

// grantRole gives userID the role on cal through a group of its own.
func grantRole(t *testing.T, st store.Store, cal *store.Calendar, role, userID string) {
	t.Helper()
	ctx := context.Background()
	g := &store.Group{ID: "grp_" + role + "_" + cal.ID, DisplayName: role + " " + cal.ID}
	if err := st.Groups().CreateGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, g.ID, userID); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: g.ID, Role: role}); err != nil {
		t.Fatal(err)
	}
}

var writePriv = regexp.MustCompile(`<(\w+:)?write[\s/>]`)

const privBody = `<d:propfind xmlns:d="DAV:"><d:prop><d:current-user-privilege-set/></d:prop></d:propfind>`

func TestGroupCalendarInEveryMembersHome(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := groupCalendar(t, st, "Team")
	tokens := map[string]string{}
	for _, name := range []string{"reader", "editor", "nonmember"} {
		tokens[name] = davUser(t, st, name, "user")
	}
	grantRole(t, st, cal, "reader", "usr_reader")
	grantRole(t, st, cal, "editor", "usr_editor")

	for name, wantListed := range map[string]bool{"reader": true, "editor": true, "nonmember": false} {
		c := davClient(t, ts, name, tokens[name])
		home := "/dav/usr_" + name + "/calendars/"
		cals, err := c.FindCalendars(context.Background(), home)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		listed := false
		for _, dc := range cals {
			listed = listed || dc.Path == home+"_"+cal.ID+"/"
		}
		if listed != wantListed {
			t.Errorf("%s: group calendar listed %v, want %v", name, listed, wantListed)
		}
	}
	for name, wantWrite := range map[string]bool{"reader": false, "editor": true} {
		r := rawDAV(t, ts, "PROPFIND", "/dav/usr_"+name+"/calendars/_"+cal.ID+"/", name, tokens[name], privBody, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
		body := readAll(r)
		if r.StatusCode != http.StatusMultiStatus || writePriv.MatchString(body) != wantWrite {
			t.Errorf("%s: %d write privilege %v, want %v: %s", name, r.StatusCode, writePriv.MatchString(body), wantWrite, body)
		}
	}
}

func TestGroupCalendarRoles(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := groupCalendar(t, st, "Team")
	tok := map[string]string{}
	for _, name := range []string{"reader", "editor", "manager", "nonmember"} {
		tok[name] = davUser(t, st, name, "user")
	}
	for _, role := range []string{"reader", "editor", "manager"} {
		grantRole(t, st, cal, role, "usr_"+role)
	}
	at := func(user, name string) string { return "/dav/usr_" + user + "/calendars/_" + cal.ID + "/" + name }
	ics := map[string]string{"Content-Type": "text/calendar"}
	xmlCT := map[string]string{"Content-Type": "application/xml"}
	patch := `<d:propertyupdate xmlns:d="DAV:"><d:set><d:prop><d:displayname>Renamed</d:displayname></d:prop></d:set></d:propertyupdate>`
	mk := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:displayname>x</d:displayname></d:prop></d:set></c:mkcalendar>`

	steps := []struct {
		who, method, path, body string
		hdr                     map[string]string
		want                    int
	}{
		{"editor", "PUT", at("editor", "e.ics"), eventICS("e"), ics, http.StatusCreated},
		{"reader", "GET", at("reader", "e.ics"), "", nil, http.StatusOK},
		{"reader", "PUT", at("reader", "r.ics"), eventICS("r"), ics, http.StatusForbidden},
		{"reader", "DELETE", at("reader", "e.ics"), "", nil, http.StatusForbidden},
		{"reader", "PROPPATCH", at("reader", ""), patch, xmlCT, http.StatusForbidden},
		{"editor", "PROPPATCH", at("editor", ""), patch, xmlCT, http.StatusForbidden},
		{"manager", "PROPPATCH", at("manager", ""), patch, xmlCT, http.StatusMultiStatus},
		{"manager", "MKCALENDAR", "/dav/usr_manager/calendars/_cal_" + uuid.NewString() + "/", mk, xmlCT, http.StatusForbidden},
		{"nonmember", "GET", at("nonmember", "e.ics"), "", nil, http.StatusNotFound},
		{"nonmember", "PUT", at("nonmember", "n.ics"), eventICS("n"), ics, http.StatusNotFound},
		{"editor", "DELETE", at("editor", "e.ics"), "", nil, http.StatusNoContent},
	}
	for i, s := range steps {
		if r := rawDAV(t, ts, s.method, s.path, s.who, tok[s.who], s.body, s.hdr); r.StatusCode != s.want {
			t.Fatalf("step %d %s %s %s: %d, want %d: %s", i, s.who, s.method, s.path, r.StatusCode, s.want, readAll(r))
		}
	}
	if got, _ := st.Calendars().GetCalendarByID(context.Background(), cal.ID); got.Name != "Renamed" {
		t.Fatalf("manager rename not stored: %q", got.Name)
	}
}

// Grants are loaded per request: leaving the group cuts a syncing phone off at once.
func TestMembershipRemovalTakesEffectNextRequest(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := groupCalendar(t, st, "Team")
	token := davUser(t, st, "reader", "user")
	grantRole(t, st, cal, "reader", "usr_reader")
	path := "/dav/usr_reader/calendars/_" + cal.ID + "/"
	if r := rawDAV(t, ts, "PROPFIND", path, "reader", token, privBody, map[string]string{"Depth": "0"}); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("member: %d", r.StatusCode)
	}
	if err := st.Groups().RemoveGroupMember(context.Background(), "grp_reader_"+cal.ID, "usr_reader"); err != nil {
		t.Fatal(err)
	}
	if r := rawDAV(t, ts, "PROPFIND", path, "reader", token, privBody, map[string]string{"Depth": "0"}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("after leaving the group: %d, want 404", r.StatusCode)
	}
}

// A group segment never reaches a personal calendar, even with the right ID.
func TestGroupSegmentNeverReachesPersonalCalendar(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	aliceTok := davUser(t, st, "alice", "user")
	bobTok := davUser(t, st, "bob", "user")
	if r := rawDAV(t, ts, "PROPFIND", "/dav/usr_alice/calendars/", "alice", aliceTok, privBody, map[string]string{"Depth": "1"}); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("alice home: %d", r.StatusCode)
	}
	cals, _ := st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_alice")
	for _, who := range []struct{ user, tok string }{{"bob", bobTok}, {"alice", aliceTok}} {
		p := "/dav/usr_" + who.user + "/calendars/_" + cals[0].ID + "/"
		if r := rawDAV(t, ts, "PROPFIND", p, who.user, who.tok, privBody, map[string]string{"Depth": "0"}); r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s via group segment: %d, want 404", who.user, r.StatusCode)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api/ -run 'GroupCalendar|MembershipRemoval|GroupSegment'`
Expected: FAIL. The group calendar is not listed, and its path answers 404 for members.

- [ ] **Step 3: Resolve group calendars in the backend**

In `internal/davbackend/backend.go`, import `"github.com/Busnes-app/kycalendar/internal/access"` and make these changes.

Constants and patterns:

```go
const (
	ownerUser   = "user"
	ownerGroup  = "group"
	defaultSlug = "default"
	// groupPrefix marks a group calendar in every member's home: "_" + calendar ID. Personal
	// slugs start with a letter or digit, so the two never collide.
	groupPrefix = "_"
)

var groupSegment = regexp.MustCompile(`^_cal_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var (
	errNoCalendar = webdav.NewHTTPError(http.StatusNotFound, errors.New("no such calendar"))
	errReadOnly   = webdav.NewHTTPError(http.StatusForbidden, errors.New("this calendar is read-only for you"))
)
```

Add the field to `Backend` and replace its comment:

```go
// Backend is built per request; User is the authenticated, active, non-admin user and Grants
// are every grant reaching User through group membership, loaded for this request.
type Backend struct {
	Store               store.Store
	User                *store.User
	Grants              []store.CalendarGrant
	MaxObjectsPerUser   int
	MaxCalendarsPerUser int
	MaxBytesPerUser     int64
	MaxBytesTotal       int64
}
```

In `split`, accept group segments:

```go
	if !slugPattern.MatchString(slug) && !groupSegment.MatchString(slug) {
		return "", "", webdav.NewHTTPError(http.StatusNotFound, errors.New("no such calendar"))
	}
```

Add `segment` and change `toDAV` to take the role:

```go
func (b *Backend) segment(c *store.Calendar) string {
	if c.OwnerKind == ownerGroup {
		return groupPrefix + c.ID
	}
	return c.Slug
}

func (b *Backend) toDAV(ctx context.Context, c *store.Calendar, role access.Role) (caldav.Calendar, error) {
	epoch, err := b.Store.Calendars().SyncEpoch(ctx)
	if err != nil {
		return caldav.Calendar{}, err
	}
	return caldav.Calendar{
		Path:                  b.home() + b.segment(c) + "/",
		Name:                  c.Name,
		Description:           c.Description,
		Color:                 c.Color,
		CTag:                  strconv.FormatInt(c.Seq, 10),
		SyncToken:             calendar.FormatSyncToken(epoch, c.Seq),
		MaxResourceSize:       calendar.MaxObjectSize,
		SupportedComponentSet: []string{ical.CompEvent},
		ReadOnly:              !role.CanWrite(),
	}, nil
}
```

Replace `calendar`:

```go
// calendar resolves a home segment to a calendar and the user's role on it. A group calendar
// the user cannot read answers 404, exactly like one that does not exist.
func (b *Backend) calendar(ctx context.Context, seg string) (*store.Calendar, access.Role, error) {
	if id, ok := strings.CutPrefix(seg, groupPrefix); ok {
		c, err := b.Store.Calendars().GetCalendarByID(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, access.None, errNoCalendar
		}
		if err != nil {
			return nil, access.None, err
		}
		role := access.Resolve(b.User, c, b.Grants)
		if c.OwnerKind != ownerGroup || !role.CanRead() {
			return nil, access.None, errNoCalendar
		}
		return c, role, nil
	}
	c, err := b.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, b.User.ID, seg)
	if errors.Is(err, store.ErrNotFound) && seg == defaultSlug {
		// A client may write to the default calendar before it ever lists calendars.
		if err := b.ensureDefault(ctx); err != nil {
			return nil, access.None, err
		}
		c, err = b.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, b.User.ID, seg)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, access.None, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	if err != nil {
		return nil, access.None, err
	}
	return c, access.Owner, nil
}
```

Replace `ListCalendars`:

```go
func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	if err := b.ensureDefault(ctx); err != nil {
		return nil, err
	}
	cals, err := b.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, b.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.Calendar, 0, len(cals)+len(b.Grants))
	for _, c := range cals {
		dc, err := b.toDAV(ctx, c, access.Owner)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	seen := map[string]bool{}
	for _, g := range b.Grants {
		if seen[g.CalendarID] {
			continue
		}
		seen[g.CalendarID] = true
		c, role, err := b.calendar(ctx, groupPrefix+g.CalendarID)
		if errors.Is(err, errNoCalendar) {
			continue
		}
		if err != nil {
			return nil, err
		}
		dc, err := b.toDAV(ctx, c, role)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	return out, nil
}
```

`GetCalendar`: `c, role, err := b.calendar(ctx, slug)`, then `dc, err := b.toDAV(ctx, c, role)`.

`CreateCalendar`: right after the `split` check, refuse group segments:

```go
	if strings.HasPrefix(slug, groupPrefix) {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("group calendars are created by an administrator"))
	}
```

`UpdateCalendar`: resolve the role and require manage before the property checks:

```go
	c, role, err := b.calendar(ctx, slug)
	if err != nil {
		return err
	}
	if !role.CanManage() {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("only a manager can change this calendar"))
	}
```

`objectAt` returns the role as well:

```go
func (b *Backend) objectAt(ctx context.Context, p string) (string, *store.Calendar, access.Role, string, error) {
	seg, name, err := b.split(p)
	if err != nil {
		return "", nil, access.None, "", err
	}
	c, role, err := b.calendar(ctx, seg)
	return seg, c, role, name, err
}
```

Update every caller. `GetCalendarObject`, `ListCalendarObjects`, `QueryCalendarObjects` and `SyncCalendar` take `slug, c, _, name, err :=` (or `slug, c, _, _, err :=`) and change nothing else, because `calendar` already refused anyone who cannot read. In `PutCalendarObject`, after the `name == ""` check, add `if !role.CanWrite() { return nil, errReadOnly }`. In `DeleteCalendarObject`, after its `name == ""` check, add `if !role.CanWrite() { return errReadOnly }`.

Above the `PutObject` call in `PutCalendarObject`, change the quota comment to: `// A group calendar is its own owner, so the per-owner limits apply to each group calendar.`

- [ ] **Step 4: Load the grants per request**

In `internal/api/dav.go` `handleDAV`, before building the backend:

```go
	grants, err := s.store.Calendars().UserGrants(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
```

Then add `Grants: grants,` to the `davbackend.Backend` literal.

- [ ] **Step 5: Run the CalDAV tests**

Run: `go test ./internal/api/ -run 'CalDAV|GroupCalendar|MembershipRemoval|GroupSegment|DAV'`
Expected: PASS, the new tests and every Plan 1 CalDAV test alike.

- [ ] **Step 6: DOX**

In `internal/davbackend/AGENTS.md`, replace the line "Plan 1 serves personal calendars only (`owner_kind = 'user'`); a default calendar is created on first listing." with:

```markdown
- The home lists the user's personal calendars (a default is created on first listing) and every group calendar a grant lets them read, at `_<calendar-id>/`. Roles come from `access.Resolve` over `Backend.Grants`, which `handleDAV` loads from `UserGrants` on every request, so membership changes apply to the next request.
- A group calendar the user cannot read answers 404 on every method. Reading needs reader, `PUT` and `DELETE` of objects need editor, and `PROPPATCH` needs manager (owner on personal calendars); otherwise 403. `ReadOnly` (no `write` in `current-user-privilege-set`) is set below editor. `MKCALENDAR` at a `_` segment is 403. Deleting any calendar over CalDAV stays 403.
- A group calendar is its own quota owner: the per-user object, byte and calendar limits apply per group calendar, and the instance byte cap still applies.
```

- [ ] **Step 7: Commit**

```bash
git add internal/davbackend internal/api/dav.go internal/api/dav_group_test.go
git commit -m "feat(dav): serve group calendars with role-based privileges"
```

---

### Task 5: Group calendar and grant API

**Files:**
- Create: `internal/calendar/props.go`, `internal/calendar/props_test.go`
- Modify: `internal/davbackend/backend.go` (use `calendar.CheckProps`, delete `checkCalendarProps`, `colorPattern` and the two length constants)
- Modify: `internal/api/server.go` (`authenticate`, `requireSession`, `requireAdmin`, routes), `internal/api/app_passwords.go` (`requireEveryday`), `internal/api/export_test.go`
- Create: `internal/api/group_calendars.go`, `internal/api/group_calendars_test.go`
- Docs: `internal/api/AGENTS.md`, `internal/calendar/AGENTS.md`

**Interfaces:**
- Consumes: Task 2 store methods; `access.Resolve`, `access.ParseRole` (Task 3).
- Produces:
  - `calendar.CheckProps(name, description, color *string) error`, where nil means unchanged.
  - `(*Server).authenticate(w, r) *store.User`, `(*Server).requireSession(h) http.HandlerFunc`, and `sessionUser(ctx) *store.User`. `requireAdmin`, `requireEveryday` and `requireSession` all put the user in context.
  - The routes below, and `api.SetStepUpWindowForTest(d time.Duration) (restore func())`.

| Method | Path | Guard | Response |
|---|---|---|---|
| GET | `/api/admin/calendars` | admin | `[{id,name,color,description,created_at,grants:[{group_id,group_name,role}]}]` |
| POST | `/api/admin/calendars` | admin | `{name,color?,description?}` → 201 the calendar |
| DELETE | `/api/admin/calendars/{id}` | admin + step-up | 204; 403 `reauth_required`; 404 not a group calendar |
| GET | `/api/admin/groups` | admin | `{groups:[{id,display_name}],total}`, `offset`/`limit` ≤ 200 |
| GET | `/api/admin/audit` | admin | `{records:[...],total}`, `offset`/`limit` ≤ 200 |
| GET | `/api/calendars/{id}/grants` | session: admin or manager | `[grant]` |
| PUT | `/api/calendars/{id}/grants/{group}` | session: admin or manager | `{role}` → 200 `[grant]`; 400 bad role; 404 no such group |
| DELETE | `/api/calendars/{id}/grants/{group}` | session: admin or manager | 200 `[grant]` (idempotent) |

On the grant routes, a caller who cannot read the calendar gets 404, and a reader or editor gets 403.

- [ ] **Step 1: Move the property bounds to `internal/calendar`**

`internal/calendar/props.go`:

```go
package calendar

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	MaxNameBytes        = 255
	MaxDescriptionBytes = 4096
)

var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`)

// CheckProps bounds client-set calendar properties; nil means unchanged and "" clears a colour.
func CheckProps(name, description, color *string) error {
	switch {
	case name != nil && len(*name) > MaxNameBytes:
		return fmt.Errorf("displayname over %d bytes", MaxNameBytes)
	case description != nil && len(*description) > MaxDescriptionBytes:
		return fmt.Errorf("calendar-description over %d bytes", MaxDescriptionBytes)
	case color != nil && *color != "" && !colorPattern.MatchString(*color):
		return errors.New("calendar-color must be #RRGGBB or #RRGGBBAA")
	}
	return nil
}
```

`internal/calendar/props_test.go`:

```go
package calendar_test

import (
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

func TestCheckProps(t *testing.T) {
	s := func(v string) *string { return &v }
	ok := []struct{ name, desc, color *string }{
		{nil, nil, nil}, {s("Team"), s(""), s("")}, {nil, nil, s("#00aa11")}, {nil, nil, s("#00aa11ff")},
		{s(strings.Repeat("n", calendar.MaxNameBytes)), s(strings.Repeat("d", calendar.MaxDescriptionBytes)), nil},
	}
	for i, c := range ok {
		if err := calendar.CheckProps(c.name, c.desc, c.color); err != nil {
			t.Errorf("ok %d: %v", i, err)
		}
	}
	bad := []struct{ name, desc, color *string }{
		{s(strings.Repeat("n", calendar.MaxNameBytes+1)), nil, nil},
		{nil, s(strings.Repeat("d", calendar.MaxDescriptionBytes+1)), nil},
		{nil, nil, s("red")}, {nil, nil, s("#12345")},
	}
	for i, c := range bad {
		if err := calendar.CheckProps(c.name, c.desc, c.color); err == nil {
			t.Errorf("bad %d accepted", i)
		}
	}
}
```

In `internal/davbackend/backend.go`, delete `colorPattern`, `maxCalendarName`, `maxCalendarDescription` and `checkCalendarProps`, and call `calendar.CheckProps` in their place (two call sites).

Run: `go test ./internal/calendar/ ./internal/api/ -run 'CheckProps|Mkcalendar|Proppatch'`
Expected: PASS.

- [ ] **Step 2: Write the failing API tests**

`internal/api/group_calendars_test.go`:

```go
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func call(t *testing.T, srv *api.Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
		req.Header.Set(auth.HeaderCSRF, "test-csrf")
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func audited(t *testing.T, st store.Store, action string) bool {
	t.Helper()
	recs, _, err := st.Audit().ListAuditRecords(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Action == action {
			return true
		}
	}
	return false
}

func TestAdminCreatesGroupCalendarAndGrants(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	bobTok := davUser(t, st, "bob", "user")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_team", DisplayName: "Team"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, "grp_team", "usr_bob"); err != nil {
		t.Fatal(err)
	}

	w := call(t, srv, "POST", "/api/admin/calendars", `{"name":"Rota","color":"#00aa11"}`, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = call(t, srv, "PUT", "/api/calendars/"+created.ID+"/grants/grp_team", `{"role":"reader"}`, admin)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"group_name":"Team"`)) {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/admin/calendars", "", admin); !bytes.Contains(w.Body.Bytes(), []byte(`"role":"reader"`)) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	if r := rawDAV(t, ts, "PROPFIND", "/dav/usr_bob/calendars/_"+created.ID+"/", "bob", bobTok, privBody, map[string]string{"Depth": "0"}); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("the granted member cannot reach the calendar: %d", r.StatusCode)
	}
	for _, action := range []string{"admin.calendar_create", "calendar.grant_set"} {
		if !audited(t, st, action) {
			t.Errorf("no %s audit row", action)
		}
	}
}

func TestCreateGroupCalendarValidates(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	for _, body := range []string{`{"name":"  "}`, `{"name":"x","color":"red"}`, `not json`} {
		if w := call(t, srv, "POST", "/api/admin/calendars", body, admin); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
}

func TestManagerChangesGrantsOthersCannot(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	cal := groupCalendar(t, st, "Team")
	carol := loginAs(t, srv, st, "carol", "user")
	dave := loginAs(t, srv, st, "dave", "user")
	erin := loginAs(t, srv, st, "erin", "user")
	admin := loginAs(t, srv, st, "root", "admin")
	grantRole(t, st, cal, "manager", "usr_carol")
	grantRole(t, st, cal, "editor", "usr_dave")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_new", DisplayName: "New"}); err != nil {
		t.Fatal(err)
	}
	path := "/api/calendars/" + cal.ID + "/grants/grp_new"
	for who, want := range map[string]struct {
		c    *http.Cookie
		code int
	}{"manager": {carol, 200}, "editor": {dave, 403}, "nonmember": {erin, 404}, "admin": {admin, 200}} {
		if w := call(t, srv, "PUT", path, `{"role":"reader"}`, want.c); w.Code != want.code {
			t.Errorf("%s: %d, want %d: %s", who, w.Code, want.code, w.Body.String())
		}
	}
	if w := call(t, srv, "PUT", path, `{"role":"owner"}`, carol); w.Code != http.StatusBadRequest {
		t.Errorf("owner role: %d, want 400", w.Code)
	}
	if w := call(t, srv, "PUT", "/api/calendars/"+cal.ID+"/grants/grp_missing", `{"role":"reader"}`, carol); w.Code != http.StatusNotFound {
		t.Errorf("missing group: %d, want 404", w.Code)
	}
}

func TestDeleteGroupCalendarNeedsRecentSignIn(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cal := groupCalendar(t, st, "Doomed")
	admin := loginAs(t, srv, st, "root", "admin")
	restore := api.SetStepUpWindowForTest(0)
	w := call(t, srv, "DELETE", "/api/admin/calendars/"+cal.ID, "", admin)
	restore()
	if w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte(`"reauth_required"`)) {
		t.Fatalf("stale session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Calendars().GetCalendarByID(context.Background(), cal.ID); err != nil {
		t.Fatalf("a refused delete removed the calendar: %v", err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/calendars/"+cal.ID, "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("fresh session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Calendars().GetCalendarByID(context.Background(), cal.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("calendar survived: %v", err)
	}
	if !audited(t, st, "admin.calendar_delete") {
		t.Error("no admin.calendar_delete audit row")
	}
}

func TestGroupDeletionLeavesCalendarUnassigned(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	cal := groupCalendar(t, st, "Team")
	tok := davUser(t, st, "bob", "user")
	grantRole(t, st, cal, "reader", "usr_bob")
	admin := loginAs(t, srv, st, "root", "admin")
	if err := st.Groups().DeleteGroup(ctx, "grp_reader_"+cal.ID); err != nil {
		t.Fatal(err)
	}
	w := call(t, srv, "GET", "/api/admin/calendars", "", admin)
	if !bytes.Contains(w.Body.Bytes(), []byte(cal.ID)) || !bytes.Contains(w.Body.Bytes(), []byte(`"grants":[]`)) {
		t.Fatalf("calendar should be listed with no grants: %s", w.Body.String())
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	if r := rawDAV(t, ts, "PROPFIND", "/dav/usr_bob/calendars/_"+cal.ID+"/", "bob", tok, privBody, map[string]string{"Depth": "0"}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("former member: %d, want 404", r.StatusCode)
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/api/ -run 'GroupCalendar|Grants|RecentSignIn|Unassigned'`
Expected: FAIL to compile (`api.SetStepUpWindowForTest` undefined).

- [ ] **Step 4: Session helpers**

In `internal/api/server.go`, replace `requireAdmin` with:

```go
type sessionUserKey struct{}

// authenticate resolves the session, or writes the 401 (or password-change 403) answer and
// returns nil.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) *store.User {
	user, _, err := s.sessions.AuthenticateRequest(r)
	if err == nil {
		return user
	}
	if errors.Is(err, auth.ErrPasswordChangeRequired) {
		s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Change your password before continuing", "code": "password_change_required"})
	} else {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
	}
	return nil
}

func withSessionUser(r *http.Request, u *store.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionUserKey{}, u))
}

// sessionUser is the user requireSession, requireAdmin or requireEveryday authenticated.
func sessionUser(ctx context.Context) *store.User {
	u, _ := ctx.Value(sessionUserKey{}).(*store.User)
	return u
}

// requireSession admits any signed-in user; the handler decides by role.
func (s *Server) requireSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if user := s.authenticate(w, r); user != nil {
			h(w, withSessionUser(r, user))
		}
	}
}

// requireAdmin rejects requests without a valid session, or with a non-admin one.
func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.authenticate(w, r)
		if user == nil {
			return
		}
		if user.Role != "admin" {
			s.writeError(w, http.StatusForbidden, "Administrator role required")
			return
		}
		h(w, withSessionUser(r, user))
	}
}
```

In `internal/api/app_passwords.go`, replace the body of `requireEveryday` with:

```go
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.authenticate(w, r)
		if user == nil {
			return
		}
		if user.Role == "admin" {
			s.writeError(w, http.StatusForbidden, "Administrator accounts cannot use calendars")
			return
		}
		h(w, withSessionUser(r, user))
	}
```

Remove imports that are now unused (`errors` and `auth` in `app_passwords.go`, if nothing else uses them there).

- [ ] **Step 5: Write `internal/api/group_calendars.go`**

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// stepUpWindow is how recently an admin must have signed in to delete a group calendar.
var stepUpWindow = 10 * time.Minute

const maxListPage = 200

type grantView struct {
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
	Role      string `json:"role"`
}

type groupCalendarView struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Color       string      `json:"color"`
	Description string      `json:"description"`
	CreatedAt   time.Time   `json:"created_at"`
	Grants      []grantView `json:"grants"`
}

func grantViews(gs []store.CalendarGrant) []grantView {
	out := make([]grantView, 0, len(gs))
	for _, g := range gs {
		out = append(out, grantView{GroupID: g.GroupID, GroupName: g.GroupName, Role: g.Role})
	}
	return out
}

func calendarView(c *store.Calendar, gs []store.CalendarGrant) groupCalendarView {
	return groupCalendarView{ID: c.ID, Name: c.Name, Color: c.Color, Description: c.Description, CreatedAt: c.CreatedAt, Grants: grantViews(gs)}
}

// auditCalendar records an access change by the session user; IDs only.
func (s *Server) auditCalendar(r *http.Request, action, resource, details string) {
	actor := ""
	if u := sessionUser(r.Context()); u != nil {
		actor = u.ID
	}
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: actor, Action: action, Resource: resource, Details: details, IPAddress: s.requestIP(r)})
}

func listPage(r *http.Request) (offset, limit int) {
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > maxListPage {
		limit = maxListPage
	}
	return offset, limit
}

func (s *Server) handleListGroupCalendars(w http.ResponseWriter, r *http.Request) {
	cals, err := s.store.Calendars().ListCalendarsByKind(r.Context(), "group")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list calendars")
		return
	}
	out := make([]groupCalendarView, 0, len(cals))
	for _, c := range cals {
		gs, err := s.store.Calendars().ListGrants(r.Context(), c.ID)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to list grants")
			return
		}
		out = append(out, calendarView(c, gs))
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateGroupCalendar(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		s.writeError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if err := calendar.CheckProps(&body.Name, &body.Description, &body.Color); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := "cal_" + uuid.NewString()
	c := &store.Calendar{ID: id, OwnerKind: "group", OwnerID: id, Slug: "group", Name: body.Name, Color: body.Color, Description: body.Description}
	if err := s.store.Calendars().CreateCalendar(r.Context(), c, 0); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to create the calendar")
		return
	}
	s.auditCalendar(r, "admin.calendar_create", c.ID, "")
	s.writeJSON(w, http.StatusCreated, calendarView(c, nil))
}

// handleDeleteGroupCalendar deletes a group calendar with every event in it. It is a step-up
// action: the session's credentials must be younger than stepUpWindow.
func (s *Server) handleDeleteGroupCalendar(w http.ResponseWriter, r *http.Request) {
	_, sess, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if time.Since(sess.CreatedAt) > stepUpWindow {
		s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign in again to delete a calendar", "code": "reauth_required"})
		return
	}
	id := r.PathValue("id")
	c, err := s.store.Calendars().GetCalendarByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.OwnerKind != "group") {
		s.writeError(w, http.StatusNotFound, "No such group calendar")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return
	}
	if err := s.store.Calendars().DeleteCalendar(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the calendar")
		return
	}
	s.auditCalendar(r, "admin.calendar_delete", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	offset, limit := listPage(r)
	groups, total, err := s.store.Groups().ListGroups(r.Context(), offset, limit)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	type groupView struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupView{ID: g.ID, DisplayName: g.DisplayName})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"groups": out, "total": total})
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	offset, limit := listPage(r)
	recs, total, err := s.store.Audit().ListAuditRecords(r.Context(), offset, limit)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list audit records")
		return
	}
	if recs == nil {
		recs = []*store.AuditRecord{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"records": recs, "total": total})
}

// grantableCalendar loads the group calendar named in the path if the session user may change
// its grants: administrators always, everyday users with the manager role. A user who cannot
// read it gets 404, a reader or editor 403.
func (s *Server) grantableCalendar(w http.ResponseWriter, r *http.Request) *store.Calendar {
	user := sessionUser(r.Context())
	c, err := s.store.Calendars().GetCalendarByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.OwnerKind != "group") {
		s.writeError(w, http.StatusNotFound, "No such group calendar")
		return nil
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return nil
	}
	if user.Role == "admin" {
		return c
	}
	grants, err := s.store.Calendars().UserGrants(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load access")
		return nil
	}
	switch role := access.Resolve(user, c, grants); {
	case !role.CanRead():
		s.writeError(w, http.StatusNotFound, "No such group calendar")
		return nil
	case !role.CanManage():
		s.writeError(w, http.StatusForbidden, "Only a manager can change who has access")
		return nil
	}
	return c
}

func (s *Server) writeGrants(w http.ResponseWriter, r *http.Request, calendarID string) {
	gs, err := s.store.Calendars().ListGrants(r.Context(), calendarID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list grants")
		return
	}
	s.writeJSON(w, http.StatusOK, grantViews(gs))
}

func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	if c := s.grantableCalendar(w, r); c != nil {
		s.writeGrants(w, r, c.ID)
	}
}

func (s *Server) handleSetGrant(w http.ResponseWriter, r *http.Request) {
	c := s.grantableCalendar(w, r)
	if c == nil {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if _, ok := access.ParseRole(body.Role); !ok {
		s.writeError(w, http.StatusBadRequest, "Role must be reader, editor or manager")
		return
	}
	group := r.PathValue("group")
	err := s.store.Calendars().SetGrant(r.Context(), store.CalendarGrant{CalendarID: c.ID, GroupID: group, Role: body.Role})
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such group")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to change access")
		return
	}
	s.auditCalendar(r, "calendar.grant_set", c.ID, "group="+group+" role="+body.Role)
	s.writeGrants(w, r, c.ID)
}

func (s *Server) handleDeleteGrant(w http.ResponseWriter, r *http.Request) {
	c := s.grantableCalendar(w, r)
	if c == nil {
		return
	}
	group := r.PathValue("group")
	if err := s.store.Calendars().DeleteGrant(r.Context(), c.ID, group); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to remove access")
		return
	}
	s.auditCalendar(r, "calendar.grant_remove", c.ID, "group="+group)
	s.writeGrants(w, r, c.ID)
}
```

Append to `internal/api/export_test.go`:

```go
// SetStepUpWindowForTest changes how recent a sign-in must be for step-up actions and returns
// the restore func. Test-only.
func SetStepUpWindowForTest(d time.Duration) func() {
	old := stepUpWindow
	stepUpWindow = d
	return func() { stepUpWindow = old }
}
```

- [ ] **Step 6: Register the routes**

In `routes()` in `internal/api/server.go`, after the app-password block:

```go
	// Group calendars: administrators create and delete them; administrators and managers
	// change grants. None of these routes reads or writes events.
	s.mux.HandleFunc("GET /api/admin/calendars", s.requireAdmin(s.handleListGroupCalendars))
	s.mux.HandleFunc("POST /api/admin/calendars", s.requireAdmin(s.handleCreateGroupCalendar))
	s.mux.HandleFunc("DELETE /api/admin/calendars/{id}", s.requireAdmin(s.handleDeleteGroupCalendar))
	s.mux.HandleFunc("GET /api/admin/groups", s.requireAdmin(s.handleListGroups))
	s.mux.HandleFunc("GET /api/admin/audit", s.requireAdmin(s.handleListAudit))
	s.mux.HandleFunc("GET /api/calendars/{id}/grants", s.requireSession(s.handleListGrants))
	s.mux.HandleFunc("PUT /api/calendars/{id}/grants/{group}", s.requireSession(s.handleSetGrant))
	s.mux.HandleFunc("DELETE /api/calendars/{id}/grants/{group}", s.requireSession(s.handleDeleteGrant))
```

- [ ] **Step 7: Run the API tests**

Run: `go test ./internal/api/ ./internal/calendar/ ./internal/davbackend/`
Expected: PASS, including the existing `TestPrivilegedEndpointsRequireAdmin` and the app-password tests.

- [ ] **Step 8: DOX**

- In `internal/api/AGENTS.md`:
  - Replace "The scaffold has no step-up" with: "Group calendar deletion is the one step-up action: the session's credentials must be younger than 10 minutes (`stepUpWindow`, from `Session.CreatedAt`), else 403 `reauth_required`. Other destructive backup routes rely on admin-only plus `TestPrivilegedEndpointsRequireAdmin`."
  - Add the route table from this task's Interfaces block.
  - Add: "`requireSession`, `requireAdmin` and `requireEveryday` share `authenticate` and put the user in context (`sessionUser`). Grant routes are `requireSession`. `grantableCalendar` admits admins and managers (via `access.Resolve`), answers 404 to users who cannot read the calendar and 403 to readers and editors. Audit actions `admin.calendar_create`, `admin.calendar_delete`, `calendar.grant_set`, `calendar.grant_remove` carry the session user ID, the calendar ID and `group=`/`role=` details."
- In `internal/calendar/AGENTS.md` Local Contracts, add: "`CheckProps` bounds calendar name (255 bytes), description (4096 bytes) and colour (`#RRGGBB[AA]` or empty) for CalDAV and the JSON API alike."

- [ ] **Step 9: Commit**

```bash
git add internal/calendar internal/davbackend internal/api
git commit -m "feat(api): add group calendar, grant, group and audit routes"
```

---

### Task 6: Route registry and the authorization matrix

**Files:**
- Modify: `internal/api/server.go` (`Server.patterns`, `handle`, `routes()`), `internal/scim/handler.go` (`RegisterRoutes`), `internal/scim/scim_test.go` (every `srv.RegisterRoutes(mux)` becomes `srv.RegisterRoutes(mux.Handle)`), `internal/api/export_test.go`
- Create: `internal/api/authz_matrix_test.go`
- Docs: `internal/api/AGENTS.md`, root `AGENTS.md` Verification bullet

**Interfaces:**
- Consumes: `loginAs`, `call` (Task 5), `rawDAV`, `readAll`, `groupCalendar`, `eventICS` (Task 4), `apppass.Generate`.
- Produces: `(*Server).handle(pattern string, h http.Handler)`; `(*scim.Server).RegisterRoutes(handle func(pattern string, h http.Handler))`; `api.RoutesForTest(s *Server) []string`.

- [ ] **Step 1: Record every route**

In `internal/api/server.go`, add `patterns []string // every registered route, for the authorization matrix` to `Server`, and add:

```go
// handle registers a route and records its pattern, so the authorization matrix can prove it
// covers every route.
func (s *Server) handle(pattern string, h http.Handler) {
	s.patterns = append(s.patterns, pattern)
	s.mux.Handle(pattern, h)
}
```

Replace `routes()` with:

```go
func (s *Server) routes() {
	// Auth
	s.handle("/api/auth/pow-challenge", http.HandlerFunc(s.handlePoWChallenge))
	s.handle("/api/auth/login", http.HandlerFunc(s.handleLogin))
	s.handle("/api/auth/mfa/totp", http.HandlerFunc(s.handleMFATOTP))
	s.handle("/api/auth/mfa/recovery-code", http.HandlerFunc(s.handleMFARecovery))
	s.handle("/api/auth/logout", http.HandlerFunc(s.handleLogout))
	s.handle("/api/auth/me", http.HandlerFunc(s.handleMe))
	s.handle("/api/auth/change-password", http.HandlerFunc(s.handleChangePassword))

	// SSO
	s.handle("/api/sso/kysignon/login", s.requireSSO(s.handleKySignOnLogin))
	s.handle("/api/sso/kysignon/callback", s.requireSSO(s.handleKySignOnCallback))
	s.handle("/api/sso/kysignon/sync", s.requireSSO(s.handleKySignOnSyncWebhook))
	s.handle("/saml/metadata", http.HandlerFunc(s.handleSAMLMetadata))

	// Feature 0 KyBackup & Restore Drills. Capsules carry site data and keys: admins only.
	// Method patterns: only the declared method reaches a handler. Export is a POST so the
	// CSRF check covers a download that carries the whole instance.
	s.handle("POST /api/backup/drill", s.requireAdmin(s.handleBackupDrill))
	s.handle("POST /api/backup/export-capsule", s.requireAdmin(s.handleExportCapsule))
	s.handle("POST /api/backup/pair-remote", s.tracked(s.requireAdmin(s.handlePairRemoteRecovery)))
	s.handle("POST /api/backup/deposit", s.tracked(s.requireAdmin(s.handleRunBackup)))
	s.handle("DELETE /api/backup/pairing", s.requireAdmin(s.handleUnpair))
	s.handle("POST /api/backup/pin-key", s.tracked(s.requireAdmin(s.handlePinKey)))
	s.handle("PUT /api/backup/schedule", s.requireAdmin(s.handleSetSchedule))
	s.handle("GET /api/backup/status", s.requireAdmin(s.handleBackupStatus))

	// Settings & Theme. The read endpoint tiers its own payload by role.
	s.handle("/api/settings", http.HandlerFunc(s.handleGetSettings))
	s.handle("/api/settings/theme", s.requireAdmin(s.handleSetTheme))

	// SCIM 2.0 routes
	s.scim.RegisterRoutes(s.handle)

	// App passwords for native CalDAV clients. Everyday users only.
	s.handle("GET /api/app-passwords", s.requireEveryday(s.handleListAppPasswords))
	s.handle("POST /api/app-passwords", s.requireEveryday(s.handleCreateAppPassword))
	s.handle("DELETE /api/app-passwords/{id}", s.requireEveryday(s.handleDeleteAppPassword))

	// Group calendars: administrators create and delete them; administrators and managers
	// change grants. None of these routes reads or writes events.
	s.handle("GET /api/admin/calendars", s.requireAdmin(s.handleListGroupCalendars))
	s.handle("POST /api/admin/calendars", s.requireAdmin(s.handleCreateGroupCalendar))
	s.handle("DELETE /api/admin/calendars/{id}", s.requireAdmin(s.handleDeleteGroupCalendar))
	s.handle("GET /api/admin/groups", s.requireAdmin(s.handleListGroups))
	s.handle("GET /api/admin/audit", s.requireAdmin(s.handleListAudit))
	s.handle("GET /api/calendars/{id}/grants", s.requireSession(s.handleListGrants))
	s.handle("PUT /api/calendars/{id}/grants/{group}", s.requireSession(s.handleSetGrant))
	s.handle("DELETE /api/calendars/{id}/grants/{group}", s.requireSession(s.handleDeleteGrant))

	// CalDAV for native clients; app-password Basic auth, never the session cookie.
	dav := s.withDAVAuth(http.HandlerFunc(s.handleDAV))
	s.handle("/dav/", dav)
	s.handle("/.well-known/caldav", dav)

	// Embedded React PWA Frontend
	s.handle("/", web.Handler())
}
```

Before replacing, compare it with the current `routes()` line by line: the only differences must be `s.handle` and the `http.HandlerFunc(...)` conversions. If the current file registers a route this block lacks, keep that route.

In `internal/scim/handler.go`:

```go
// RegisterRoutes mounts the SCIM endpoints through handle, which records them.
func (s *Server) RegisterRoutes(handle func(pattern string, h http.Handler)) {
	h := http.StripPrefix("/scim", s.protocol)
	handle("/scim/v2", h)
	handle("/scim/v2/", h)
}
```

Replace every `srv.RegisterRoutes(mux)` in `internal/scim/scim_test.go` with `srv.RegisterRoutes(mux.Handle)`.

Append to `internal/api/export_test.go` (add `"slices"` to its imports):

```go
// RoutesForTest returns every registered route pattern. Test-only.
func RoutesForTest(s *Server) []string { return slices.Clone(s.patterns) }
```

Run: `go build ./... && go test ./internal/scim/ ./internal/api/`
Expected: PASS. Registration is unchanged apart from the recording.

- [ ] **Step 2: Write the matrix**

`internal/api/authz_matrix_test.go`:

```go
package api_test

import (
	"cmp"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// The authorization matrix: every registered route and every CalDAV operation against every
// kind of caller. TestEveryRouteIsInTheMatrix fails when a route has no row.

type actor string

const (
	anon        actor = "anonymous"
	owner       actor = "owner"
	reader      actor = "reader"
	editor      actor = "editor"
	manager     actor = "manager"
	nonmember   actor = "nonmember"
	admin       actor = "admin"
	deactivated actor = "deactivated"
)

var actors = []actor{anon, owner, reader, editor, manager, nonmember, admin, deactivated}

// allow means authorization let the request through: any status but 401, 403 or 404.
const allow = 0

type expect map[actor]int

var (
	adminOnly    = expect{anon: 401, deactivated: 401, admin: allow, owner: 403, reader: 403, editor: 403, manager: 403, nonmember: 403}
	everyday     = expect{anon: 401, deactivated: 401, admin: 403, owner: allow, reader: allow, editor: allow, manager: allow, nonmember: allow}
	anySession   = expect{anon: 401, deactivated: 401, admin: allow, owner: allow, reader: allow, editor: allow, manager: allow, nonmember: allow}
	grantManager = expect{anon: 401, deactivated: 401, admin: allow, manager: allow, reader: 403, editor: 403, owner: 404, nonmember: 404}
	// SCIM takes only its bearer token: a session cookie is never enough.
	scimOnly = expect{anon: 401, deactivated: 401, admin: 401, owner: 401, reader: 401, editor: 401, manager: 401, nonmember: 401}
)

// public routes authenticate (or not) by design; they have no authorization to test here.
// Each has its own tests: login, MFA and password limits, SSO, settings tiering, the SPA.
var public = expect(nil)

// davPatterns are covered by davRows, operation by operation.
var davPatterns = map[string]bool{"/dav/": true, "/.well-known/caldav": true}

type world struct {
	srv     *api.Server
	st      store.Store
	cookies map[actor]*http.Cookie
	tokens  map[actor]string
	passIDs map[actor]string
	group   *store.Calendar // reader, editor and manager each hold their role on it
	doomed  *store.Calendar // the group calendar the admin row deletes
}

func newWorld(t *testing.T) *world {
	t.Helper()
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	w := &world{srv: srv, st: st, cookies: map[actor]*http.Cookie{}, tokens: map[actor]string{}, passIDs: map[actor]string{}}
	for _, a := range actors[1:] {
		role := "user"
		if a == admin {
			role = "admin"
		}
		w.cookies[a] = loginAs(t, srv, st, string(a), role)
		id, token, hash, err := apppass.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: id, UserID: "usr_" + string(a), Label: "matrix", Hash: hash}); err != nil {
			t.Fatal(err)
		}
		w.tokens[a], w.passIDs[a] = token, id
	}
	w.group = groupCalendar(t, st, "Team")
	w.doomed = groupCalendar(t, st, "Doomed")
	for _, a := range []actor{reader, editor, manager} {
		grantRole(t, st, w.group, string(a), "usr_"+string(a))
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_extra", DisplayName: "Extra"}); err != nil {
		t.Fatal(err)
	}
	put := func(name string) {
		o := &store.CalendarObject{CalendarID: w.group.ID, Name: name, UID: name, Data: []byte(eventICS(name))}
		if _, err := st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	put("seed.ics")
	for _, a := range actors[1:] {
		put(string(a) + "-del.ics")
	}
	// Only the status changes: the session and app password stay, so this proves the status
	// check rather than the revocation (which the SCIM and webhook tests pin).
	u, err := st.Users().GetUserByID(ctx, "usr_deactivated")
	if err != nil {
		t.Fatal(err)
	}
	u.Status = "inactive"
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	return w
}

type apiRow struct {
	method, path, body string
	pathFor            func(a actor) string // overrides path when the target differs per caller
	want               expect
}

// apiRows has exactly one row per registered pattern, keyed by that pattern.
func apiRows(w *world) map[string]apiRow {
	g := "/api/calendars/" + w.group.ID + "/grants"
	return map[string]apiRow{
		"/api/auth/pow-challenge":      {want: public},
		"/api/auth/login":              {want: public},
		"/api/auth/mfa/totp":           {want: public},
		"/api/auth/mfa/recovery-code":  {want: public},
		"/api/auth/logout":             {want: public},
		"/api/auth/me":                 {want: public},
		"/api/sso/kysignon/login":      {want: public},
		"/api/sso/kysignon/callback":   {want: public},
		"/api/sso/kysignon/sync":       {want: public},
		"/saml/metadata":               {want: public},
		"/api/settings":                {want: public},
		"/":                            {want: public},
		"/api/auth/change-password":    {method: "POST", path: "/api/auth/change-password", body: "{}", want: anySession},
		"POST /api/backup/drill":         {method: "POST", path: "/api/backup/drill", want: adminOnly},
		"POST /api/backup/export-capsule": {method: "POST", path: "/api/backup/export-capsule", want: adminOnly},
		"POST /api/backup/pair-remote":   {method: "POST", path: "/api/backup/pair-remote", body: "{}", want: adminOnly},
		"POST /api/backup/deposit":       {method: "POST", path: "/api/backup/deposit", want: adminOnly},
		"DELETE /api/backup/pairing":     {method: "DELETE", path: "/api/backup/pairing", want: adminOnly},
		"POST /api/backup/pin-key":       {method: "POST", path: "/api/backup/pin-key", body: "{}", want: adminOnly},
		"PUT /api/backup/schedule":       {method: "PUT", path: "/api/backup/schedule", body: "{}", want: adminOnly},
		"GET /api/backup/status":         {method: "GET", path: "/api/backup/status", want: adminOnly},
		"/api/settings/theme":            {method: "POST", path: "/api/settings/theme", body: "{}", want: adminOnly},
		"/scim/v2":                       {method: "GET", path: "/scim/v2", want: scimOnly},
		"/scim/v2/":                      {method: "GET", path: "/scim/v2/Users", want: scimOnly},
		"GET /api/app-passwords":         {method: "GET", path: "/api/app-passwords", want: everyday},
		"POST /api/app-passwords":        {method: "POST", path: "/api/app-passwords", body: `{"label":"matrix-new"}`, want: everyday},
		// Each caller deletes its own app password; the anonymous caller names a placeholder so
		// the path still matches the pattern instead of falling through to the SPA.
		"DELETE /api/app-passwords/{id}": {method: "DELETE", pathFor: func(a actor) string { return "/api/app-passwords/" + cmp.Or(w.passIDs[a], "none") }, want: everyday},
		"GET /api/admin/calendars":       {method: "GET", path: "/api/admin/calendars", want: adminOnly},
		"POST /api/admin/calendars":      {method: "POST", path: "/api/admin/calendars", body: `{"name":"Matrix"}`, want: adminOnly},
		"DELETE /api/admin/calendars/{id}": {method: "DELETE", path: "/api/admin/calendars/" + w.doomed.ID, want: adminOnly},
		"GET /api/admin/groups":          {method: "GET", path: "/api/admin/groups", want: adminOnly},
		"GET /api/admin/audit":           {method: "GET", path: "/api/admin/audit", want: adminOnly},
		"GET /api/calendars/{id}/grants":             {method: "GET", path: g, want: grantManager},
		"PUT /api/calendars/{id}/grants/{group}":     {method: "PUT", path: g + "/grp_extra", body: `{"role":"reader"}`, want: grantManager},
		"DELETE /api/calendars/{id}/grants/{group}":  {method: "DELETE", path: g + "/grp_extra", want: grantManager},
	}
}

func check(t *testing.T, got, want int) {
	t.Helper()
	if want == allow {
		if got == 401 || got == 403 || got == 404 {
			t.Errorf("blocked with %d, want it let through", got)
		}
		return
	}
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestEveryRouteIsInTheMatrix(t *testing.T) {
	w := newWorld(t)
	rows := apiRows(w)
	routes := api.RoutesForTest(w.srv)
	for _, p := range routes {
		if _, ok := rows[p]; !ok && !davPatterns[p] {
			t.Errorf("route %q has no authorization matrix row", p)
		}
	}
	for p := range rows {
		if !slices.Contains(routes, p) {
			t.Errorf("matrix row %q matches no registered route", p)
		}
	}
	for p := range davPatterns {
		if !slices.Contains(routes, p) {
			t.Errorf("CalDAV pattern %q is not registered", p)
		}
	}
	for p, row := range rows {
		for _, a := range actors {
			if _, ok := row.want[a]; row.want != nil && !ok {
				t.Errorf("row %q has no expectation for %s", p, a)
			}
		}
	}
}

func TestAPIAuthorizationMatrix(t *testing.T) {
	w := newWorld(t)
	for pattern, row := range apiRows(w) {
		if row.want == nil {
			continue
		}
		for _, a := range actors {
			t.Run(pattern+"/"+string(a), func(t *testing.T) {
				path := row.path
				if row.pathFor != nil {
					path = row.pathFor(a)
				}
				check(t, call(t, w.srv, row.method, path, row.body, w.cookies[a]).Code, row.want[a])
			})
		}
	}
}

type davRow struct {
	name, method string
	path         func(me string) string // me is the caller's user ID
	body         string
	header       map[string]string
	want         expect
}

// davExpect fills in the callers every CalDAV row treats alike: no credentials 401, a
// deactivated user 401, an administrator 403.
func davExpect(e expect) expect {
	e[anon], e[deactivated], e[admin] = 401, 401, 403
	return e
}

func TestCalDAVAuthorizationMatrix(t *testing.T) {
	w := newWorld(t)
	ts := httptest.NewServer(w.srv)
	defer ts.Close()
	gseg := "_" + w.group.ID
	name := func(me string) string { return strings.TrimPrefix(me, "usr_") }
	xmlCT := map[string]string{"Content-Type": "application/xml"}
	depth0 := map[string]string{"Content-Type": "application/xml", "Depth": "0"}
	depth1 := map[string]string{"Content-Type": "application/xml", "Depth": "1"}
	newICS := map[string]string{"Content-Type": "text/calendar", "If-None-Match": "*"}
	propfind := `<d:propfind xmlns:d="DAV:"><d:prop><d:displayname/></d:prop></d:propfind>`
	patch := `<d:propertyupdate xmlns:d="DAV:"><d:set><d:prop><d:displayname>Renamed</d:displayname></d:prop></d:set></d:propertyupdate>`
	sync := `<d:sync-collection xmlns:d="DAV:"><d:sync-token></d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	mk := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:displayname>x</d:displayname></d:prop></d:set></c:mkcalendar>`
	group := func(me string) string { return "/dav/" + me + "/calendars/" + gseg + "/" }

	rows := []davRow{
		{"discover", "PROPFIND", func(string) string { return "/.well-known/caldav" }, propfind, depth0,
			davExpect(expect{owner: allow, reader: allow, editor: allow, manager: allow, nonmember: allow})},
		{"home", "PROPFIND", func(me string) string { return "/dav/" + me + "/calendars/" }, propfind, depth1,
			davExpect(expect{owner: 207, reader: 207, editor: 207, manager: 207, nonmember: 207})},
		{"group calendar", "PROPFIND", group, propfind, depth0,
			davExpect(expect{reader: 207, editor: 207, manager: 207, owner: 404, nonmember: 404})},
		{"group sync", "REPORT", group, sync, depth1,
			davExpect(expect{reader: 207, editor: 207, manager: 207, owner: 404, nonmember: 404})},
		{"group get", "GET", func(me string) string { return group(me) + "seed.ics" }, "", nil,
			davExpect(expect{reader: 200, editor: 200, manager: 200, owner: 404, nonmember: 404})},
		{"group put", "PUT", func(me string) string { return group(me) + name(me) + "-new.ics" }, "", newICS,
			davExpect(expect{editor: 201, manager: 201, reader: 403, owner: 404, nonmember: 404})},
		{"group delete", "DELETE", func(me string) string { return group(me) + name(me) + "-del.ics" }, "", nil,
			davExpect(expect{editor: 204, manager: 204, reader: 403, owner: 404, nonmember: 404})},
		{"group proppatch", "PROPPATCH", group, patch, xmlCT,
			davExpect(expect{manager: 207, reader: 403, editor: 403, owner: 404, nonmember: 404})},
		{"group collection delete", "DELETE", group, "", nil,
			davExpect(expect{reader: 403, editor: 403, manager: 403, owner: 404, nonmember: 404})},
		{"mkcalendar at a group segment", "MKCALENDAR", func(me string) string { return "/dav/" + me + "/calendars/_cal_" + uuid.NewString() + "/" }, mk, xmlCT,
			davExpect(expect{owner: 403, reader: 403, editor: 403, manager: 403, nonmember: 403})},
		{"owner's calendar", "PROPFIND", func(string) string { return "/dav/usr_owner/calendars/default/" }, propfind, depth0,
			davExpect(expect{owner: 207, reader: 403, editor: 403, manager: 403, nonmember: 403})},
		{"owner's calendar put", "PUT", func(me string) string { return "/dav/usr_owner/calendars/default/" + name(me) + "-p.ics" }, "", newICS,
			davExpect(expect{owner: 201, reader: 403, editor: 403, manager: 403, nonmember: 403})},
	}
	for _, row := range rows {
		for _, a := range actors {
			t.Run(row.name+"/"+string(a), func(t *testing.T) {
				me := "usr_" + string(a)
				body := row.body
				if row.method == "PUT" {
					body = eventICS(row.name + "-" + string(a))
				}
				user, pass := "", ""
				if a != anon {
					user, pass = string(a), w.tokens[a]
				}
				r := rawDAV(t, ts, row.method, row.path(me), user, pass, body, row.header)
				check(t, r.StatusCode, row.want[a])
			})
		}
	}
}
```

`rawDAV` calls `SetBasicAuth` unconditionally. For the anonymous caller, change `rawDAV` in `dav_test.go` to skip `SetBasicAuth` when `user == ""`. That is a one-line `if user != ""` guard, and every existing caller passes a user.

- [ ] **Step 3: Run the matrix**

Run: `go test ./internal/api/ -run 'Matrix|EveryRoute' -v 2>&1 | grep -E '^(=== RUN|--- FAIL|FAIL|ok|PASS)' | grep -v '=== RUN'`
Expected: PASS. If a row fails, read the handler before touching the expectation. Change an expectation only when the observed behaviour is the one the Global Constraints require. If behaviour violates a constraint, fix the code. Never loosen an admin, everyday, grant or CalDAV row to make it pass.

- [ ] **Step 4: Prove the coverage check bites**

Temporarily add `s.handle("GET /api/matrix-probe", http.HandlerFunc(s.handleMe))` to `routes()` and run `go test ./internal/api/ -run TestEveryRouteIsInTheMatrix`.
Expected: FAIL with `route "GET /api/matrix-probe" has no authorization matrix row`. Remove the probe line and re-run: PASS.

- [ ] **Step 5: Run everything**

Run: `make ci`
Expected: PASS. Then, if a Postgres instance is available: `make test-postgres`.

- [ ] **Step 6: DOX**

- In `internal/api/AGENTS.md`, replace the "New routes are unauthenticated only by deliberate choice" line with: "Every route is registered through `s.handle`, which records its pattern. `authz_matrix_test.go` holds one row per pattern (public, session, everyday, admin-only, grant-manager, SCIM) and one row per CalDAV operation, each run against anonymous, owner, reader, editor, manager, non-member, admin and deactivated callers. `TestEveryRouteIsInTheMatrix` fails on a route without a row, so adding a route means adding its row."
- Under Verification there, add `authz_matrix_test.go` beside `authz_test.go`.
- In the root `AGENTS.md` `#### Verification` list, add: "- The authorization matrix (`internal/api/authz_matrix_test.go`) runs inside `go test` and fails when a registered route has no row."

- [ ] **Step 7: Commit**

```bash
git add internal/api internal/scim AGENTS.md
git commit -m "test(api): pin every route in a route-complete authorization matrix"
```

---

### Task 7: Group calendars admin screen

**Files:**
- Create: `web/src/pages/GroupCalendars.tsx`, `web/src/pages/GroupCalendars.test.tsx`
- Modify: `web/src/components/AppHeader.tsx` (nav item), `web/src/App.tsx` (tab)
- Rebuild: `web/dist`
- Docs: `web/AGENTS.md`

**Interfaces:**
- Consumes: the Task 5 routes `GET/POST /api/admin/calendars`, `DELETE /api/admin/calendars/{id}`, `GET /api/admin/groups`, `PUT/DELETE /api/calendars/{id}/grants/{group}`.
- Produces: a default-exported `GroupCalendars` component and an admin-only tab with id `group-calendars`.

- [ ] **Step 1: Write the failing component test**

`web/src/pages/GroupCalendars.test.tsx`:

```tsx
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import GroupCalendars from "./GroupCalendars";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

type Handler = (init?: RequestInit) => unknown;

function mockFetch(handlers: Record<string, Handler>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const team = { id: "cal_1", name: "Team", color: "", description: "", created_at: "2026-10-06T00:00:00Z", grants: [] };
const groups = { groups: [{ id: "grp_1", display_name: "Sales" }], total: 1 };

describe("GroupCalendars", () => {
  it("grants a group a role", async () => {
    const put = vi.fn((init?: RequestInit) => {
      expect(JSON.parse(String(init?.body))).toEqual({ role: "editor" });
      return [{ group_id: "grp_1", group_name: "Sales", role: "editor" }];
    });
    mockFetch({
      "GET /api/admin/calendars": () => [team],
      "GET /api/admin/groups": () => groups,
      "PUT /api/calendars/cal_1/grants/grp_1": put,
    });
    render(<GroupCalendars />);
    await screen.findByRole("heading", { name: "Team" });
    fireEvent.change(screen.getByLabelText("Group"), { target: { value: "grp_1" } });
    fireEvent.change(screen.getByLabelText("Role"), { target: { value: "editor" } });
    fireEvent.click(screen.getByRole("button", { name: /add group/i }));
    await waitFor(() => expect(put).toHaveBeenCalled());
  });

  it("asks for a fresh sign-in when delete needs step-up", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/calendars": () => [team],
      "GET /api/admin/groups": () => groups,
      "DELETE /api/admin/calendars/cal_1": () =>
        new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<GroupCalendars />);
    fireEvent.click(await screen.findByRole("button", { name: /delete calendar/i }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("alerts when the list cannot load", async () => {
    mockFetch({
      "GET /api/admin/calendars": () => new Response("{}", { status: 500 }),
      "GET /api/admin/groups": () => groups,
    });
    render(<GroupCalendars />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load/i);
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/pages/GroupCalendars.test.tsx`
Expected: FAIL, `Failed to resolve import "./GroupCalendars"`.

- [ ] **Step 3: Write `web/src/pages/GroupCalendars.tsx`**

```tsx
import { useCallback, useEffect, useState } from "react";
import { secureFetch } from "../api";

interface Grant {
  group_id: string;
  group_name: string;
  role: string;
}

interface GroupCalendar {
  id: string;
  name: string;
  color: string;
  description: string;
  grants: Grant[];
}

interface Group {
  id: string;
  display_name: string;
}

const ROLES = ["reader", "editor", "manager"];
const JSON_HEADERS = { "Content-Type": "application/json" };

// Resolves to null on a network error so every caller handles one failure path.
const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

async function errorText(res: Response | null, fallback: string): Promise<string> {
  return (await res?.json().catch(() => null))?.error ?? fallback;
}

export default function GroupCalendars() {
  const [calendars, setCalendars] = useState<GroupCalendar[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [cals, grps] = await Promise.all([send("/api/admin/calendars"), send("/api/admin/groups")]);
    if (!cals?.ok || !grps?.ok) {
      setError("Could not load group calendars. Reload the page to try again.");
      return;
    }
    setCalendars(await cals.json());
    setGroups((await grps.json()).groups);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send("/api/admin/calendars", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify({ name }) });
    if (!res?.ok) {
      setError(await errorText(res, "Could not create the calendar"));
      return;
    }
    setName("");
    await load();
  }

  async function setGrant(cal: string, group: string, role: string) {
    setError(null);
    const url = `/api/calendars/${encodeURIComponent(cal)}/grants/${encodeURIComponent(group)}`;
    const res = await send(url, { method: "PUT", headers: JSON_HEADERS, body: JSON.stringify({ role }) });
    if (!res?.ok) {
      setError(await errorText(res, "Could not change access"));
      return;
    }
    await load();
  }

  async function removeGrant(cal: string, group: string) {
    setError(null);
    const res = await send(`/api/calendars/${encodeURIComponent(cal)}/grants/${encodeURIComponent(group)}`, { method: "DELETE" });
    if (!res?.ok) {
      setError("Could not remove access. The group still has it; try again.");
      return;
    }
    await load();
  }

  async function remove(cal: GroupCalendar) {
    if (!window.confirm(`Delete ${cal.name} and every event in it? This cannot be undone.`)) return;
    setError(null);
    const res = await send(`/api/admin/calendars/${encodeURIComponent(cal.id)}`, { method: "DELETE" });
    if (res?.status === 403 && (await res.json().catch(() => null))?.code === "reauth_required") {
      setError("Sign out and sign in again, then delete the calendar within 10 minutes.");
      return;
    }
    if (!res?.ok) {
      setError("Could not delete the calendar. It still exists.");
      return;
    }
    await load();
  }

  return (
    <section className="page">
      <h1>Group calendars</h1>
      <p>
        Group calendars belong to the organisation. Readers see events, editors change them, and managers also rename the
        calendar and change who has access. Administrators manage access but never see events.
      </p>
      <form onSubmit={create}>
        <label>
          Calendar name
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={255} required />
        </label>
        <button type="submit">Create</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {calendars.length === 0 ? (
        <p>No group calendars yet.</p>
      ) : (
        calendars.map((cal) => (
          <CalendarCard key={cal.id} cal={cal} groups={groups} onSet={setGrant} onRemove={removeGrant} onDelete={remove} />
        ))
      )}
    </section>
  );
}

interface CardProps {
  cal: GroupCalendar;
  groups: Group[];
  onSet: (cal: string, group: string, role: string) => Promise<void>;
  onRemove: (cal: string, group: string) => Promise<void>;
  onDelete: (cal: GroupCalendar) => Promise<void>;
}

function CalendarCard({ cal, groups, onSet, onRemove, onDelete }: CardProps) {
  const [group, setGroup] = useState("");
  const [role, setRole] = useState("reader");
  const available = groups.filter((g) => !cal.grants.some((x) => x.group_id === g.id));

  return (
    <article aria-labelledby={`cal-${cal.id}`}>
      <h2 id={`cal-${cal.id}`}>{cal.name}</h2>
      {cal.grants.length === 0 ? (
        <p>No group has access. Members cannot see this calendar until you add a group.</p>
      ) : (
        <ul>
          {cal.grants.map((g) => (
            <li key={g.group_id}>
              <span>{g.group_name}</span>{" "}
              <select aria-label={`Role for ${g.group_name}`} value={g.role} onChange={(e) => void onSet(cal.id, g.group_id, e.target.value)}>
                {ROLES.map((r) => (
                  <option key={r} value={r}>{r}</option>
                ))}
              </select>{" "}
              <button type="button" aria-label={`Remove ${g.group_name}`} onClick={() => void onRemove(cal.id, g.group_id)}>
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!group) return;
          void onSet(cal.id, group, role);
          setGroup("");
        }}
      >
        {/* htmlFor, not nesting: a nested select's options would join the label's name. */}
        <label htmlFor={`group-${cal.id}`}>Group</label>
        <select id={`group-${cal.id}`} value={group} onChange={(e) => setGroup(e.target.value)} required>
          <option value="">Choose a group</option>
          {available.map((g) => (
            <option key={g.id} value={g.id}>{g.display_name}</option>
          ))}
        </select>
        <label htmlFor={`role-${cal.id}`}>Role</label>
        <select id={`role-${cal.id}`} value={role} onChange={(e) => setRole(e.target.value)}>
          {ROLES.map((r) => (
            <option key={r} value={r}>{r}</option>
          ))}
        </select>
        <button type="submit">Add group</button>
      </form>
      <button type="button" onClick={() => void onDelete(cal)}>Delete calendar</button>
    </article>
  );
}
```

- [ ] **Step 4: Wire the tab**

In `web/src/components/AppHeader.tsx`, add `CalendarDays` to the `lucide-react` import and add this before the `devices` item in `navItems`:

```tsx
    ...(user?.role === 'admin' ? [{ id: 'group-calendars', label: 'Group calendars', icon: CalendarDays }] : []),
```

In `web/src/App.tsx`, add `import GroupCalendars from './pages/GroupCalendars';` and inside `<main>`:

```tsx
        {activeTab === 'group-calendars' && user.role === 'admin' && <GroupCalendars />}
```

- [ ] **Step 5: Test, build, browser check**

Run: `cd web && npm test && npm run build`
Expected: vitest PASS (the new file and the existing ones); the build succeeds.

Run the browser suite per `web/AGENTS.md`: `go build -o .browser/server ./cmd/server` at the repo root, then `cd web && npx playwright install chromium && npm run test:browser`.
Expected: PASS. The keyboard test walks the first two nav buttons by index, and the new item is added after them.

- [ ] **Step 6: DOX**

In `web/AGENTS.md` Local Contracts, add: "`src/pages/GroupCalendars.tsx` is admin-only (tab `group-calendars`): create a group calendar, add a group with a role, change or remove a grant, delete a calendar after a confirm. A 403 `reauth_required` asks for a fresh sign-in. Every failure shows a `role="alert"` message, and nothing on it shows events." Update the Purpose line to mention the group calendars admin screen.

- [ ] **Step 7: Commit**

```bash
git add web
git commit -m "feat(web): add the group calendars admin screen"
```

---

### Task 8: Root contracts and operator setup

**Files:**
- Modify: `AGENTS.md` (`## KyCalendar`), `README.md`
- Modify: `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md`, Open items section only

**Interfaces:**
- Consumes: everything above.
- Produces: operator documentation; no code.

- [ ] **Step 1: Root `AGENTS.md`**

Under `## KyCalendar` → `### Server contracts`, add a `#### Plan 2 access contracts` subsection after `#### Plan 1 CalDAV contracts`:

```markdown
#### Plan 2 access contracts

- Administrator = the KyIdentity app role `kycalendar.admin`, exact match, read from the ID token's `roles` at every KySignOn login and from SCIM `roles`. The global `role` claim and the webhook's `role` never grant it. A change revokes the user's sessions and app passwords. Local `init-admin` accounts are break-glass and keep their stored role.
- Roles are decided only by `internal/access.Resolve`: owner on personal calendars; the highest of `reader`/`editor`/`manager` across the user's groups on group calendars; `None` for administrators everywhere.
- A group calendar is `owner_kind = 'group'` with `owner_id` = its own ID, so it survives the deletion of any KyIdentity group. Its grants cascade with the group. Its CalDAV segment in each member's home is `_<calendar-id>`.
- A caller who cannot read a calendar gets 404 on every API and CalDAV route; one who can read but lacks the role gets 403.
- Deleting a group calendar requires a sign-in younger than 10 minutes (403 `reauth_required` otherwise).
- `internal/api/authz_matrix_test.go` covers every registered route and every CalDAV operation for anonymous, owner, reader, editor, manager, non-member, admin and deactivated callers, and fails when a route has no row.
```

Add `- [internal/access/AGENTS.md](internal/access/AGENTS.md): Pure calendar authorization: the admin app role and the role resolver.` to the `#### Server child DOX index`.

In `#### Plan 1 CalDAV contracts`, change "Administrators are refused (403) on DAV and on `/api/app-passwords`: admin identities are not everyday identities." to "Administrators (`kycalendar.admin`, see Plan 2) are refused (403) on DAV and on `/api/app-passwords`: admin identities are not everyday identities."

- [ ] **Step 2: `README.md` operator section**

Add an `## Administrators and group calendars` section after the sign-in section:

```markdown
## Administrators and group calendars

KyCalendar administrators manage group calendars, access, backups and the audit log. They never see events and cannot use calendars on their devices. Give a dedicated KyIdentity login the admin role, and keep everyday logins for calendar use.

In KyIdentity, on the KyCalendar app:

1. Create the app role `kycalendar.admin`. Creating an app's first role also stops KyIdentity from sending its global admin role to KyCalendar; until then, a global admin is still not a KyCalendar admin.
2. Assign `kycalendar.admin` to the dedicated administrator login, directly or through a group.
3. For group calendars, enable group delivery on the KyCalendar SCIM connector so groups and their members reach KyCalendar.

A role change takes effect at that user's next sign-in, or at once through SCIM, and signs them out of KyCalendar everywhere.

In KyCalendar, **Group calendars** lists every group calendar. Create one, then add groups:

- **reader** sees events;
- **editor** creates, changes and deletes events;
- **manager** also renames and recolours the calendar and changes who has access.

A member's phone shows every group calendar they can read under the same app password, read-only below editor. Removing someone from a group cuts their access on their next sync. Deleting a KyIdentity group removes its access and leaves the calendar for you to re-grant or delete. Deleting a calendar needs a sign-in from the last 10 minutes.
```

- [ ] **Step 3: Spec open items**

In `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md`, replace the KyIdentity open item with: "KyIdentity: register the `kycalendar` OIDC client, create the app role `kycalendar.admin` (Plan 2 grants admin on that exact value), and confirm the SCIM connector delivers groups as it does for KyDrive. This is an operator step; Plan 2 does not automate it."

- [ ] **Step 4: Final verification**

Run: `make ci`
Expected: PASS. Run `make test-postgres` if a Postgres instance is available, and record whether it ran.

- [ ] **Step 5: Commit**

```bash
git add AGENTS.md README.md docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md
git commit -m "docs: record the Plan 2 access contracts and the kycalendar.admin setup"
```

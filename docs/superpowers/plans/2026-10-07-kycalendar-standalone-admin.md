# KyCalendar Standalone Administration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An operator who installs only KyCalendar runs it from its own People, Groups and Sign-in screens, with KyIdentity (or any OIDC provider) optional and still the owner of what it synchronises.
**Architecture:** Three PRs on the existing Go scaffold and React SPA. Groups gain an owner column (migration 10) so admin writes reach only local groups and SCIM only its own; People reuse the store's column-scoped writes with one locked transaction for role and status; Sign-in resolves `signin_*` settings under the environment, seals the client secret, and swaps one `atomic.Pointer[sso.Provider]` that every login reads.
**Tech Stack:** Go (net/http, database/sql, SQLite+PostgreSQL), coreos/go-oidc + x/oauth2, React 19 + TypeScript + Vite, vitest, Playwright (Chromium)
**Spec:** docs/superpowers/specs/2026-10-07-kycalendar-standalone-admin-design.md

## Global Constraints

- Step-up window is 10 minutes (`stepUpWindow`); a stale session gets 403 `{"error":"Sign in again to …","code":"reauth_required"}` from `requireStepUp`.
- A write to a synced person or group is 409 `{"code":"managed_externally"}`; only `sso_provider = 'local'` people and `source = 'local'` groups are writable here.
- The client secret lives only in setting `signin_client_secret_sealed`, AES-GCM under `crypto.DeriveKey(cfg.Security.EncryptionKey, "kycalendar:setting:signin_client_secret")`; no response carries it, and `extra_settings` drops every key starting `signin_client_secret`.
- The SSO callback stays `/api/sso/kysignon/callback` for every provider.
- Migration 10 (`group_source`) is added in both dialects; never edit migrations 1-9.
- Administrator means the exact app role `kycalendar.admin` (`access.AdminAppRole`) on a `kyidentity` login; an `oidc` login is never an administrator and never adopts a SCIM row.
- Accounts are never linked by username; case twins (`alice`/`Alice`, `Team`/`team`) are refused, never merged.
- Audit action names are exactly: `admin.user_create`, `admin.user_update`, `admin.user_reset_password`, `admin.user_role`, `admin.user_disable`, `admin.user_enable`, `admin.group_create`, `admin.group_rename`, `admin.group_delete`, `admin.group_member_add`, `admin.group_member_remove`, `admin.signin_test`, `admin.signin_save`, `admin.signin_provider_change`. Never the secret, a temporary password or a fetched response.
- Every new route gets a row in `apiRows` in `internal/api/authz_matrix_test.go` (`TestEveryRouteIsInTheMatrix` fails otherwise); a fixture a row mutates belongs to that row alone, because map iteration order is random.
- Admin edits use column-scoped store methods (`RenameUser`, `UpdateProfile`, `SetRole`, `SetStatus`, `ResetPassword`), never a whole-row `UpdateUser`, which can undo a concurrent password change.
- `web/dist` is embedded and CI diffs it: every task that changes `web/src` runs `npm run build` and commits `web/dist`.
- Run heavy suites one at a time (one `go test` package group per command with `-p 1`, never Go tests beside the browser suite): this machine ran out of memory on parallel runs.
- Local smoke needs `KY_SMOKE_PORT=28931`; 18080 is taken.
- Shell steps use explicit paths and literal arguments, no `$VAR`-assembled commands.

## Review Focus

- A reconciling SCIM client that lists every group and deletes the ones it did not create, or sends `team` while a local `Team` exists. Owner: Task 3 (`TestSCIMGroupsNeverTouchLocalGroups`, `TestSCIMGroupNameClashFlagsTheLocalGroup`).
- Two administrators demoting or disabling each other at the same instant, or a KyIdentity administrator taking away the only local administrator. Owner: Task 15 (`TestConcurrentDemotionKeepsOneLocalAdmin`), Task 19 (`TestAdminRoleAndStatusRules`, `last_admin` from an SSO admin).
- Names that differ only in case on create, rename and SCIM create. Owner: Task 2 (`TestGroupNamesAreUniqueIgnoringCase`), Task 17 (`ANN` beside `ann`), Task 18 (`BOB` beside `bob`, case-only self-rename allowed).
- An issuer that is plain http, redirects, resolves after DNS to loopback, link-local or the metadata address, or whose discovery document points at http endpoints. Owner: Task 25 (`TestDiscoverRefusals`, `TestGuardedClientRefusesPlainHTTP`, `TestRefuseLocal`).
- A provider or issuer change, saved on the screen or edited in the environment between restarts, followed by a login whose `sub` matches an old account, or a login already in flight across the swap. Owner: Task 26 (`TestCallbackAfterProviderSwapFailsOnce`), Task 28 (`TestProviderChangeRefusesTheOldSubject`, `TestLoadSignInAppliesSavedSettingsUnderTheEnvironment`), Task 29 (`TestSignInProviderChangeNeedsConfirmationAndDisables`).

## File Structure

| File | PR | Responsibility |
|---|---|---|
| `internal/store/migrations/migrations.go` | 1 | Migration 10: `groups.source`, backfill `scim` |
| `internal/store/models.go` | 1 | `Group.Source`, `GroupSourceLocal`/`GroupSourceSCIM`, `UserFieldSearch` |
| `internal/store/store.go` | 1-3 | `ListGroups` source filter; `RenameUser` actor; `UpdateProfile`, `SetRole`, `SetStatus`, `ErrLastAdmin`; `DisableSSOAccounts`, `CountSSOAccounts` |
| `internal/store/sqlstore.go` | 1-3 | Group owner and case-insensitive names under `lockedTx`; user search; access changes with the last-admin guard; `last_login_at`; provider-wide disable |
| `internal/store/migration10_test.go`, `groups_test.go`, `group_names_test.go`, `users_search_test.go` | 1 | Store tests for PR 1 |
| `internal/store/access_test.go`, `last_login_test.go` | 2 | Store tests for PR 2 (and the `seedUsers`/`seedSession` helpers) |
| `internal/store/sso_accounts_test.go` | 3 | Provider-wide disable |
| `internal/scim/handler.go`, `internal/scim/groups_test.go` | 1 | SCIM writes `scim` groups only, flags a local-name clash (`ConflictKey`) |
| `internal/sso/oidc.go` | 1 | Deleted (dead generic-OIDC client) |
| `internal/config/config.go` | 1 | `GenericOIDC*` fields and `KY_OIDC_*` reads removed |
| `internal/api/stepup.go`, `stepup_internal_test.go` | 1 | `stepUpWindow` and the one `requireStepUp` |
| `internal/api/admin_groups.go` + `admin_groups_test.go`, `admin_group_members_test.go`, `admin_group_delete_test.go` | 1 | Group admin routes |
| `internal/api/admin_users.go` + `admin_users_test.go` | 1-2 | People routes (list in PR 1, writes in PR 2) |
| `internal/api/admin_users_create_test.go`, `admin_users_update_test.go`, `admin_users_access_test.go` | 2 | People route tests |
| `internal/api/group_calendars.go`, `backup_handlers.go`, `calendars.go` | 1 | Step-up copies removed; `auditCalendar` renamed `auditAction` |
| `internal/api/server.go` | 1-3 | Route registration; live provider fields |
| `internal/api/authz_matrix_test.go` | 1-3 | Fixtures and rows for every new route |
| `internal/sso/settings.go`, `settings_test.go` | 3 | Keys, `Resolve` (environment precedence), sealed secret |
| `internal/sso/discovery.go`, `discovery_test.go` | 3 | Guarded HTTP client and `Discover` |
| `internal/sso/provider.go`, `provider_internal_test.go`, `oauth.go`, `kysignon.go` | 3 | Swappable `Provider`; webhook client keeps only the webhook |
| `internal/api/signin.go`, `signin_internal_test.go`, `sso_provider_internal_test.go` | 3 | Build, load and bind the live provider |
| `internal/api/admin_signin.go`, `admin_signin_test.go` | 3 | Sign-in admin routes |
| `internal/api/sso_handlers.go`, `settings_handlers.go`, `export_test.go`, `cmd/server/main.go` | 2-3 | Login through the live provider; `signin_name`; secret filter; `LoadSignIn` at startup |
| `cmd/server/renameuser.go`, `renameuser_test.go` | 2 | CLI passes actor `system` |
| `web/src/admin.tsx` | 1 | Shared admin fetch helpers and the source badge |
| `web/src/pages/Groups.tsx`, `People.tsx`, `SignIn.tsx` + `*.test.tsx` | 1-3 | The three screens |
| `web/src/App.tsx`, `App.test.tsx`, `components/AppHeader.tsx`, `pages/Dashboard.tsx` | 1-3 | Admin-only pages, nav items, dashboard cards |
| `web/src/pages/Login.tsx`, `Login.test.tsx` | 3 | Button only for a configured provider |
| `web/browser/groups.spec.mjs`, `people.spec.mjs` | 1-2 | Chromium flows |
| `scripts/smoke-test.sh` | 2 | Create and list a person on the built binary |
| `README.md`, `AGENTS.md`, `internal/{api,store,scim,sso,config}/AGENTS.md`, `web/AGENTS.md`, `web/browser/AGENTS.md` | 1-3 | DOX and operator docs |

---

# PR 1 — Groups

Branch `feat/standalone-groups` from `main`. Ends with Task 13.

## Task 1: Migration 10 gives groups an owner

**Files:**
- Modify: `internal/store/migrations/migrations.go` (registry tail, after version 9, ~:383-392)
- Modify: `internal/store/models.go:63-71` (`Group`)
- Modify: `internal/store/store.go:92` (`GroupStore.ListGroups`)
- Modify: `internal/store/sqlstore.go:699-883` (group store)
- Modify: `internal/api/group_calendars.go:153`, `internal/scim/handler.go:288` (the two `ListGroups` callers)
- Create: `internal/store/migration10_test.go`, `internal/store/groups_test.go`
- Modify: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `migrations.Migration{Version, Name, SQLite, Postgres}`; `isUniqueViolation(err error) bool` (`internal/store/calendars.go:17`); `errorsIs`.
- Produces: `store.Group.Source string` (json `source`); `const store.GroupSourceLocal = "local"`, `store.GroupSourceSCIM = "scim"`; `GroupStore.ListGroups(ctx context.Context, offset, limit int, source string) ([]*Group, int, error)` (`""` = every owner); `const groupColumns`; `func scanGroup(row interface{ Scan(...any) error }) (*Group, error)`.

- [ ] **Step 1: Branch**

```bash
git switch main && git pull --ff-only && git switch -c feat/standalone-groups
```

- [ ] **Step 2: Write the failing tests**

Create `internal/store/migration10_test.go`:

```go
package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Migration 10 gives groups an owner. Before it SCIM was the only group writer, so every
// existing row becomes SCIM's; rows written afterwards default to local.
func TestMigration10BackfillsGroupSource(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_old", DisplayName: "Old"}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Pretend the database predates migration 10, then reopen to run it.
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE groups DROP COLUMN source", "DELETE FROM schema_migrations WHERE version = 10"} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = raw.Close()
	st, err = store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	old, err := st.Groups().GetGroupByID(ctx, "grp_old")
	if err != nil || old.Source != store.GroupSourceSCIM {
		t.Fatalf("existing group after migration: %+v %v, want source scim", old, err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_new", DisplayName: "New"}); err != nil {
		t.Fatal(err)
	}
	if g, err := st.Groups().GetGroupByID(ctx, "grp_new"); err != nil || g.Source != store.GroupSourceLocal {
		t.Fatalf("new group: %+v %v, want source local", g, err)
	}
}
```

Create `internal/store/groups_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestGroupSourceIsStoredAndFiltered(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	for _, g := range []*store.Group{
		{ID: "grp_l", DisplayName: "Local team"},
		{ID: "grp_s", DisplayName: "Synced team", Source: store.GroupSourceSCIM},
	} {
		if err := st.Groups().CreateGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	all, total, err := st.Groups().ListGroups(ctx, 0, 10, "")
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("all groups: %d %d %v", len(all), total, err)
	}
	scim, total, err := st.Groups().ListGroups(ctx, 0, 10, store.GroupSourceSCIM)
	if err != nil || total != 1 || len(scim) != 1 || scim[0].ID != "grp_s" {
		t.Fatalf("scim groups: %+v %d %v", scim, total, err)
	}
	if g, _ := st.Groups().GetGroupByID(ctx, "grp_l"); g.Source != store.GroupSourceLocal {
		t.Fatalf("empty source stored as %q, want local", g.Source)
	}
	if g, _ := st.Groups().GetGroupByName(ctx, "synced TEAM"); g == nil || g.Source != store.GroupSourceSCIM {
		t.Fatalf("by name: %+v", g)
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test -count=1 ./internal/store/ -run 'TestMigration10|TestGroupSource'`
Expected: build failure, `undefined: store.GroupSourceSCIM` and `too many arguments in call to st.Groups().ListGroups`.

- [ ] **Step 4: Add migration 10**

In `internal/store/migrations/migrations.go`, replace the end of the registry:

```go
		Version:  9,
		Name:     "revoke_sso_admin_sessions",
		SQLite:   revokeSSOAdminSessions,
		Postgres: revokeSSOAdminSessions,
	},
}
```

with:

```go
		Version:  9,
		Name:     "revoke_sso_admin_sessions",
		SQLite:   revokeSSOAdminSessions,
		Postgres: revokeSSOAdminSessions,
	}, {
		// Groups get an owner. SCIM was the only group writer before this, so every existing row
		// is SCIM's; rows inserted from here on default to local.
		Version:  10,
		Name:     "group_source",
		SQLite:   groupSource,
		Postgres: groupSource,
	},
}

const groupSource = `ALTER TABLE groups ADD COLUMN source TEXT NOT NULL DEFAULT 'local';
UPDATE groups SET source = 'scim';`
```

- [ ] **Step 5: Model and interface**

In `internal/store/models.go`, replace the `Group` struct with:

```go
// Group represents a SCIM/RBAC user group.
type Group struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	ExternalID  string `json:"external_id,omitempty"`
	// Source owns the group: GroupSourceLocal (the admin screens) or GroupSourceSCIM. Only the
	// owner writes it. Empty on insert means local.
	Source    string    `json:"source"`
	Members   []string  `json:"members,omitempty"` // User IDs
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Group owners.
const (
	GroupSourceLocal = "local"
	GroupSourceSCIM  = "scim"
)
```

In `internal/store/store.go`, replace `	ListGroups(ctx context.Context, offset, limit int) ([]*Group, int, error)` with:

```go
	// ListGroups pages groups by name; source "" lists every owner.
	ListGroups(ctx context.Context, offset, limit int, source string) ([]*Group, int, error)
```

- [ ] **Step 6: The store reads and writes the owner**

In `internal/store/sqlstore.go`, insert above `func (g *groupStore) CreateGroup` and replace `CreateGroup` with:

```go
const groupColumns = "id, display_name, external_id, source, created_at, updated_at"

func scanGroup(row interface{ Scan(...any) error }) (*Group, error) {
	var grp Group
	if err := row.Scan(&grp.ID, &grp.DisplayName, &grp.ExternalID, &grp.Source, &grp.CreatedAt, &grp.UpdatedAt); err != nil {
		if errorsIs(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &grp, nil
}

func (g *groupStore) CreateGroup(ctx context.Context, group *Group) error {
	now := time.Now().UTC()
	if group.CreatedAt.IsZero() {
		group.CreatedAt = now
	}
	if group.UpdatedAt.IsZero() {
		group.UpdatedAt = now
	}
	if group.Source == "" {
		group.Source = GroupSourceLocal
	}
	q := g.store.rebind("INSERT INTO groups (" + groupColumns + ") VALUES (?, ?, ?, ?, ?, ?)")
	if _, err := g.store.db.ExecContext(ctx, q, group.ID, group.DisplayName, group.ExternalID, group.Source, group.CreatedAt, group.UpdatedAt); err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}
```

Replace `GetGroupByID`, `GetGroupByName`, `ListGroups` and `GetUserGroups` with:

```go
func (g *groupStore) GetGroupByID(ctx context.Context, id string) (*Group, error) {
	grp, err := scanGroup(g.store.db.QueryRowContext(ctx, g.store.rebind("SELECT "+groupColumns+" FROM groups WHERE id = ?"), id))
	if err != nil {
		return nil, err
	}
	if grp.Members, err = g.getMembers(ctx, grp.ID); err != nil {
		return nil, err
	}
	return grp, nil
}

func (g *groupStore) GetGroupByName(ctx context.Context, name string) (*Group, error) {
	grp, err := scanGroup(g.store.db.QueryRowContext(ctx, g.store.rebind("SELECT "+groupColumns+" FROM groups WHERE LOWER(display_name) = LOWER(?)"), name))
	if err != nil {
		return nil, err
	}
	if grp.Members, err = g.getMembers(ctx, grp.ID); err != nil {
		return nil, err
	}
	return grp, nil
}

func (g *groupStore) ListGroups(ctx context.Context, offset, limit int, source string) ([]*Group, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where, args := "", []any{}
	if source != "" {
		where, args = " WHERE source = ?", []any{source}
	}

	var count int
	if err := g.store.db.QueryRowContext(ctx, g.store.rebind("SELECT COUNT(1) FROM groups"+where), args...).Scan(&count); err != nil {
		return nil, 0, err
	}

	q := g.store.rebind("SELECT " + groupColumns + " FROM groups" + where + " ORDER BY display_name ASC LIMIT ? OFFSET ?")
	rows, err := g.store.db.QueryContext(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var groups []*Group
	for rows.Next() {
		grp, err := scanGroup(rows)
		if err != nil {
			return nil, 0, err
		}
		groups = append(groups, grp)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	for _, grp := range groups {
		members, _ := g.getMembers(ctx, grp.ID)
		grp.Members = members
	}

	return groups, count, nil
}

func (g *groupStore) GetUserGroups(ctx context.Context, userID string) ([]*Group, error) {
	q := g.store.rebind(`
SELECT g.id, g.display_name, g.external_id, g.source, g.created_at, g.updated_at
FROM groups g
JOIN group_members gm ON g.id = gm.group_id
WHERE gm.user_id = ?
ORDER BY g.display_name ASC
`)
	rows, err := g.store.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*Group
	for rows.Next() {
		grp, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, grp)
	}
	return groups, rows.Err()
}
```

(`getMembers`, `UpdateGroup`, `DeleteGroup`, `AddGroupMember` and `RemoveGroupMember` are unchanged.)

- [ ] **Step 7: Callers list every owner for now**

`internal/api/group_calendars.go`: `ListGroups(r.Context(), offset, limit)` becomes `ListGroups(r.Context(), offset, limit, "")`.
`internal/scim/handler.go`: `ListGroups(r.Context(), params.StartIndex-1, params.Count)` becomes `ListGroups(r.Context(), params.StartIndex-1, params.Count, "")` (Task 3 narrows it to SCIM's groups).

- [ ] **Step 8: Run the tests**

Run: `go build ./... && go test -count=1 ./internal/store/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/store`

- [ ] **Step 9: DOX**

In `internal/store/AGENTS.md`, after the Migration 9 bullet add:

```markdown
- Migration 10 adds `groups.source`: `local` by default, every row that existed before it `scim` (SCIM was the only group writer). `Group.Source` is the owner and only the owner writes a group; `ListGroups(..., source)` filters by it (`""` lists every owner); an empty source on insert means `local`.
```

- [ ] **Step 10: Commit**

```bash
git add internal/store/migrations/migrations.go internal/store/models.go internal/store/store.go internal/store/sqlstore.go internal/store/migration10_test.go internal/store/groups_test.go internal/store/AGENTS.md internal/api/group_calendars.go internal/scim/handler.go
git commit -m "feat(store): groups record their owner (migration 10)" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 2: Group names are unique ignoring case

**Files:**
- Modify: `internal/store/sqlstore.go` (`lockedTx` above the User Store banner; `CreateGroup`, `UpdateGroup`)
- Modify: `internal/store/store.go:87` (doc comment)
- Create: `internal/store/group_names_test.go`
- Modify: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `groupColumns`, `isUniqueViolation`, `SQLStore.rebind`.
- Produces: `func (s *SQLStore) lockedTx(ctx context.Context, key string) (*sql.Tx, error)` (Postgres advisory lock on `key` at READ COMMITTED; plain transaction on SQLite); `func (g *groupStore) refuseTwin(ctx context.Context, tx *sql.Tx, id, name string) error`; `CreateGroup`/`UpdateGroup` return `ErrAlreadyExists` for a case twin.

- [ ] **Step 1: Write the failing test**

Create `internal/store/group_names_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// Names are unique ignoring case across both owners: SCIM cannot create "team" beside a local
// "Team", and a rename cannot produce a case twin either.
func TestGroupNamesAreUniqueIgnoringCase(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_a", DisplayName: "Team"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_b", DisplayName: "team", Source: store.GroupSourceSCIM}); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("case twin create: %v, want ErrAlreadyExists", err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_c", DisplayName: "Other"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().UpdateGroup(ctx, &store.Group{ID: "grp_c", DisplayName: "TEAM"}); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("case twin rename: %v, want ErrAlreadyExists", err)
	}
	if err := st.Groups().UpdateGroup(ctx, &store.Group{ID: "grp_a", DisplayName: "TEAM"}); err != nil {
		t.Fatalf("renaming a group to its own name in another case: %v", err)
	}
	if g, _ := st.Groups().GetGroupByID(ctx, "grp_a"); g.Source != store.GroupSourceLocal {
		t.Fatalf("UpdateGroup changed the source to %q", g.Source)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/store/ -run TestGroupNamesAreUniqueIgnoringCase`
Expected: FAIL, `case twin create: <nil>, want ErrAlreadyExists` (the unique index is case-sensitive).

- [ ] **Step 3: The locked transaction**

In `internal/store/sqlstore.go`, insert directly above the `// User Store` banner:

```go
// lockedTx begins a transaction that holds a Postgres advisory lock on key until it ends, at
// READ COMMITTED so every statement after the lock sees writes committed before it (a server
// default of REPEATABLE READ would freeze the snapshot first). SQLite takes no lock: its single
// connection already serializes transactions.
func (s *SQLStore) lockedTx(ctx context.Context, key string) (*sql.Tx, error) {
	if s.driver != "postgres" {
		return s.db.BeginTx(ctx, nil)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
```

- [ ] **Step 4: Refuse case twins**

Insert `refuseTwin` above `CreateGroup`, and replace `CreateGroup` and `UpdateGroup`, with:

```go
// refuseTwin is ErrAlreadyExists when a group other than id holds name in any case. The unique
// index is case-sensitive; this is what keeps "Team" and "team" from both existing.
func (g *groupStore) refuseTwin(ctx context.Context, tx *sql.Tx, id, name string) error {
	var n int
	if err := tx.QueryRowContext(ctx, g.store.rebind("SELECT COUNT(1) FROM groups WHERE LOWER(display_name) = LOWER(?) AND id <> ?"), name, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrAlreadyExists
	}
	return nil
}

func (g *groupStore) CreateGroup(ctx context.Context, group *Group) error {
	now := time.Now().UTC()
	if group.CreatedAt.IsZero() {
		group.CreatedAt = now
	}
	if group.UpdatedAt.IsZero() {
		group.UpdatedAt = now
	}
	if group.Source == "" {
		group.Source = GroupSourceLocal
	}
	tx, err := g.store.lockedTx(ctx, "group-names")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := g.refuseTwin(ctx, tx, group.ID, group.DisplayName); err != nil {
		return err
	}
	q := g.store.rebind("INSERT INTO groups (" + groupColumns + ") VALUES (?, ?, ?, ?, ?, ?)")
	if _, err := tx.ExecContext(ctx, q, group.ID, group.DisplayName, group.ExternalID, group.Source, group.CreatedAt, group.UpdatedAt); err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return tx.Commit()
}

// UpdateGroup writes the name and external ID. The source never changes.
func (g *groupStore) UpdateGroup(ctx context.Context, group *Group) error {
	group.UpdatedAt = time.Now().UTC()
	tx, err := g.store.lockedTx(ctx, "group-names")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := g.refuseTwin(ctx, tx, group.ID, group.DisplayName); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, g.store.rebind("UPDATE groups SET display_name = ?, external_id = ?, updated_at = ? WHERE id = ?"), group.DisplayName, group.ExternalID, group.UpdatedAt, group.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
```

In `internal/store/store.go`, above `	CreateGroup(ctx context.Context, g *Group) error`, add:

```go
	// CreateGroup and UpdateGroup refuse (ErrAlreadyExists) a name another group holds in any case.
```

- [ ] **Step 5: Run the store tests**

Run: `go test -count=1 ./internal/store/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/store`

- [ ] **Step 6: DOX**

In `internal/store/AGENTS.md`, after the Migration 10 bullet add:

```markdown
- Group names are unique ignoring case across both owners. `CreateGroup` and `UpdateGroup` look for a case twin inside `lockedTx("group-names")` and return `ErrAlreadyExists`. `lockedTx` takes a Postgres transaction-scoped advisory lock at READ COMMITTED (a REPEATABLE READ default would freeze the snapshot before the lock); SQLite's single connection already serialises.
```

- [ ] **Step 7: Commit**

```bash
git add internal/store/sqlstore.go internal/store/store.go internal/store/group_names_test.go internal/store/AGENTS.md
git commit -m "feat(store): refuse group names that differ only in case" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 3: SCIM owns only SCIM groups

**Files:**
- Modify: `internal/scim/handler.go:268-330` (`groupResourceHandler` through `Patch`; import `time`)
- Create: `internal/scim/groups_test.go`
- Modify: `internal/scim/AGENTS.md`

**Interfaces:**
- Consumes: `store.GroupSourceSCIM`, `store.GroupSourceLocal`, `ListGroups(..., source)`, `crypto.SHA256Hex`, `scimStoreError`.
- Produces: `func scim.ConflictKey(name string) string` (`"scim_group_conflict:" + sha256hex(lower(name))`, which fits the 128-byte Postgres key column); SCIM `Get`/`Replace`/`Patch`/`Delete` answer 404 for a local group; `GetAll` lists `scim` groups only; a clashing create sets `ConflictKey(name)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/scim/groups_test.go`:

```go
package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func scimWithStore(t *testing.T) (http.Handler, store.Store, string) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-groups-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	return srv.AuthMiddleware(mux), st, token
}

// A reconciling IdP lists every group it can see and deletes what it does not know. Local
// groups must be invisible to it, or one sync would delete them and their calendar access.
func TestSCIMGroupsNeverTouchLocalGroups(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_local", DisplayName: "Local crew"}); err != nil {
		t.Fatal(err)
	}
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Synced crew"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if g, err := st.Groups().GetGroupByID(ctx, created.ID); err != nil || g.Source != store.GroupSourceSCIM {
		t.Fatalf("SCIM-created group: %+v %v, want source scim", g, err)
	}

	list := scimDo(t, h, token, "GET", "/scim/v2/Groups", nil).Body.String()
	if strings.Contains(list, "grp_local") || !strings.Contains(list, created.ID) {
		t.Fatalf("SCIM list must hold only SCIM groups: %s", list)
	}
	for _, tc := range []struct {
		method string
		body   any
	}{
		{"GET", nil},
		{"PUT", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Hijacked"}},
		{"PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "displayName", "value": "Hijacked"}}}},
		{"DELETE", nil},
	} {
		if w := scimDo(t, h, token, tc.method, "/scim/v2/Groups/grp_local", tc.body); w.Code != http.StatusNotFound {
			t.Errorf("%s on a local group: %d %s, want 404", tc.method, w.Code, w.Body.String())
		}
	}
	if g, err := st.Groups().GetGroupByID(ctx, "grp_local"); err != nil || g.DisplayName != "Local crew" {
		t.Fatalf("local group changed through SCIM: %+v %v", g, err)
	}
}

// A SCIM create whose name a local group holds (in any case) fails with a conflict and leaves a
// flag the Groups screen shows; the local group is untouched.
func TestSCIMGroupNameClashFlagsTheLocalGroup(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_local", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "crew"})
	if w.Code != http.StatusConflict {
		t.Fatalf("clashing create: %d %s, want 409", w.Code, w.Body.String())
	}
	if v, err := st.Settings().GetSetting(ctx, scim.ConflictKey("Crew")); err != nil || v == "" {
		t.Fatalf("clash not flagged: %q %v", v, err)
	}
	if g, _ := st.Groups().GetGroupByID(ctx, "grp_local"); g.Source != store.GroupSourceLocal || g.DisplayName != "Crew" {
		t.Fatalf("local group changed: %+v", g)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/scim/ -run 'TestSCIMGroups|TestSCIMGroupName'`
Expected: build failure, `undefined: scim.ConflictKey`.

- [ ] **Step 3: Implement**

In `internal/scim/handler.go`, add `"time"` to the standard-library imports, then replace everything from `type groupResourceHandler struct{ store store.Store }` up to (not including) `func (h *groupResourceHandler) replaceMembers(` with:

```go
type groupResourceHandler struct{ store store.Store }

// ConflictKey names the setting that flags a SCIM group name a local group already holds, so the
// Groups screen can ask an admin to rename the local group. Hashed: names outgrow the key column.
func ConflictKey(name string) string {
	return "scim_group_conflict:" + crypto.SHA256Hex([]byte(strings.ToLower(name)))
}

// scimGroup loads a group SCIM owns. A local group is not SCIM's to read or change, so it reads
// as missing: an IdP reconciling its directory must never rename, empty or delete one.
func (h *groupResourceHandler) scimGroup(r *http.Request, id string) (*store.Group, error) {
	group, err := h.store.Groups().GetGroupByID(r.Context(), id)
	if err == nil && group.Source != store.GroupSourceSCIM {
		err = store.ErrNotFound
	}
	if err != nil {
		return nil, scimStoreError(err, id)
	}
	return group, nil
}

func (h *groupResourceHandler) Create(r *http.Request, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	group := &store.Group{ID: "grp_" + crypto.RandomHex(12), DisplayName: stringValue(attrs, "displayName", ""), ExternalID: stringValue(attrs, "externalId", ""), Source: store.GroupSourceSCIM}
	if err := h.store.Groups().CreateGroup(r.Context(), group); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			h.flagLocalTwin(r, group.DisplayName)
		}
		return protocol.Resource{}, scimStoreError(err, group.ID)
	}
	if err := h.replaceMembers(r, group.ID, nil, memberValues(attrs["members"])); err != nil {
		return protocol.Resource{}, err
	}
	group.Members = memberValues(attrs["members"])
	return groupResource(group), nil
}

// flagLocalTwin records that a local group holds name, so SCIM could not create it.
func (h *groupResourceHandler) flagLocalTwin(r *http.Request, name string) {
	if g, err := h.store.Groups().GetGroupByName(r.Context(), name); err == nil && g.Source == store.GroupSourceLocal {
		_ = h.store.Settings().SetSetting(r.Context(), ConflictKey(name), time.Now().UTC().Format(time.RFC3339))
	}
}

func (h *groupResourceHandler) Get(r *http.Request, id string) (protocol.Resource, error) {
	group, err := h.scimGroup(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	return groupResource(group), nil
}
func (h *groupResourceHandler) GetAll(r *http.Request, params protocol.ListRequestParams) (protocol.Page, error) {
	groups, total, err := h.store.Groups().ListGroups(r.Context(), params.StartIndex-1, params.Count, store.GroupSourceSCIM)
	if err != nil {
		return protocol.Page{}, err
	}
	resources := make([]protocol.Resource, 0, len(groups))
	for _, group := range groups {
		resources = append(resources, groupResource(group))
	}
	return protocol.Page{TotalResults: total, Resources: resources}, nil
}
func (h *groupResourceHandler) Replace(r *http.Request, id string, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	group, err := h.scimGroup(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	old := group.Members
	group.DisplayName = stringValue(attrs, "displayName", group.DisplayName)
	group.ExternalID = stringValue(attrs, "externalId", group.ExternalID)
	group.Members = memberValues(attrs["members"])
	if err := h.store.Groups().UpdateGroup(r.Context(), group); err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	if err := h.replaceMembers(r, id, old, group.Members); err != nil {
		return protocol.Resource{}, err
	}
	return groupResource(group), nil
}
func (h *groupResourceHandler) Delete(r *http.Request, id string) error {
	if _, err := h.scimGroup(r, id); err != nil {
		return err
	}
	return scimStoreError(h.store.Groups().DeleteGroup(r.Context(), id), id)
}
func (h *groupResourceHandler) Patch(r *http.Request, id string, operations []protocol.PatchOperation) (protocol.Resource, error) {
	group, err := h.scimGroup(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	attrs := protocol.ResourceAttributes{"displayName": group.DisplayName, "members": memberMaps(group.Members)}
	for _, op := range operations {
		if op.Path != nil {
			attrs[op.Path.String()] = op.Value
		}
	}
	return h.Replace(r, id, attrs)
}
```

- [ ] **Step 4: Run the SCIM tests**

Run: `go test -count=1 ./internal/scim/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/scim`

- [ ] **Step 5: DOX**

In `internal/scim/AGENTS.md`, under Local Contracts add:

```markdown
- Groups have an owner (`groups.source`). SCIM creates `scim` groups and lists, reads, replaces, patches and deletes only those; a local group is 404 to SCIM, so a reconciling IdP never sees, renames, empties or deletes one. A create whose name a local group holds in any case is a 409 and sets setting `ConflictKey(name)`, which the Groups screen flags until the local group is renamed or deleted.
```

- [ ] **Step 6: Commit**

```bash
git add internal/scim/handler.go internal/scim/groups_test.go internal/scim/AGENTS.md
git commit -m "fix(scim): never read or write local groups; flag name clashes" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 4: Delete the dead generic-OIDC client

**Files:**
- Delete: `internal/sso/oidc.go`
- Modify: `internal/api/server.go:36,139,149` (field, construction, assignment)
- Modify: `internal/config/config.go:72-84,240-252` (`GenericOIDC*` fields and `KY_OIDC_*` reads)
- Modify: `internal/sso/AGENTS.md` (Purpose)

**Interfaces:**
- Consumes: nothing new.
- Produces: no `sso.GenericOIDCClient`, no `config.SSOConfig.GenericOIDC*`. `KY_OIDC_ISSUER`/`_CLIENT_ID`/`_SECRET` were read but never reached a route, so a deployment that sets them sees no change.

- [ ] **Step 1: Prove it is unused**

Run: `grep -rn 'GenericOIDC\|s\.oidc\b\|KY_OIDC' --include='*.go' cmd internal`
Expected: only `internal/sso/oidc.go`, the `oidc` field and its construction and assignment in `internal/api/server.go`, and the three fields and three `getEnv` lines in `internal/config/config.go`. Any other hit: stop and report it.

- [ ] **Step 2: Delete**

```bash
git rm internal/sso/oidc.go
```

In `internal/api/server.go` remove the line `	oidc       *sso.GenericOIDCClient` from `Server`, the line `	oidc := sso.NewGenericOIDCClient(cfg.SSO, st)` from `NewServer`, and the line `		oidc:     oidc,` from the struct literal.

In `internal/config/config.go`, make the `SSOConfig` struct and its literal in `LoadFromEnv` read:

```go
// SSOConfig holds identity provider and federation parameters.
type SSOConfig struct {
	Enabled            bool   `json:"enabled"`
	KySignOnIssuer     string `json:"kysignon_issuer"`
	KySignOnClientID   string `json:"kysignon_client_id"`
	KySignOnSecret     string `json:"kysignon_secret"`
	KySignOnHMACSecret string `json:"kysignon_hmac_secret"`
	SAMLEntityID       string `json:"saml_entity_id"`
	SAMLMetadataURL    string `json:"saml_metadata_url"`
	AutoProvision      bool   `json:"auto_provision"`
}
```

```go
		SSO: SSOConfig{
			Enabled:            getEnvBool("KY_SSO_ENABLED", true),
			KySignOnIssuer:     getEnv("KY_KYSIGNON_ISSUER", ""),
			KySignOnClientID:   getEnv("KY_KYSIGNON_CLIENT_ID", ""),
			KySignOnSecret:     getEnv("KY_KYSIGNON_SECRET", ""),
			KySignOnHMACSecret: getEnv("KY_KYSIGNON_HMAC_SECRET", ""),
			SAMLEntityID:       getEnv("KY_SAML_ENTITY_ID", ""),
			SAMLMetadataURL:    getEnv("KY_SAML_METADATA_URL", ""),
			AutoProvision:      getEnvBool("KY_SSO_AUTO_PROVISION", true),
		},
```

- [ ] **Step 3: Check nothing refers to it and the packages still pass**

Run: `grep -rn 'GenericOIDC\|KY_OIDC' --include='*.go' cmd internal; gofmt -l internal cmd; go build ./... && go test -count=1 ./internal/sso/ ./internal/config/`
Expected: no grep or gofmt output; `ok` for both packages.

- [ ] **Step 4: DOX**

In `internal/sso/AGENTS.md`, replace the Purpose line with:

```markdown
Provides Single Sign-On for KySignOn (OIDC with PKCE), KySignOn's signed directory webhook, and SAML 2.0 Service Provider metadata.
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/config/config.go internal/sso/AGENTS.md
git commit -m "refactor(sso): delete the unused generic OIDC client" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 5: One `requireStepUp` helper

**Files:**
- Create: `internal/api/stepup.go`, `internal/api/stepup_internal_test.go`
- Modify: `internal/api/group_calendars.go:19-20,51-58,121-130`, `internal/api/backup_handlers.go:365-373`, `internal/api/calendars.go:208,234`
- Modify: `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: `s.sessions.AuthenticateRequest(r) (*store.User, *store.Session, error)`, `Session.CreatedAt`, `s.writeJSON`, `s.writeError`.
- Produces: `var stepUpWindow = 10 * time.Minute` (moved to `stepup.go`; `SetStepUpWindowForTest` keeps working); `func (s *Server) requireStepUp(w http.ResponseWriter, r *http.Request, what string) bool`; `auditCalendar` renamed `func (s *Server) auditAction(ctx context.Context, r *http.Request, action, resource, details string)`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/stepup_internal_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestRequireStepUp(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	u := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	if err := s.store.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.sessions.IssueSession(ctx, httptest.NewRecorder(), httptest.NewRequest("POST", "/api/auth/login", nil), u)
	if err != nil {
		t.Fatal(err)
	}
	attempt := func(withSession bool) (bool, *httptest.ResponseRecorder) {
		r := httptest.NewRequest("POST", "/", nil)
		if withSession {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		return s.requireStepUp(w, r, "do this"), w
	}

	if ok, w := attempt(true); !ok || w.Body.Len() != 0 {
		t.Fatalf("fresh session: ok=%v body=%q, want true and nothing written", ok, w.Body.String())
	}

	old := stepUpWindow
	stepUpWindow = -time.Second // every session is older than this
	t.Cleanup(func() { stepUpWindow = old })
	ok, w := attempt(true)
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if ok || w.Code != http.StatusForbidden || body["code"] != "reauth_required" || body["error"] != "Sign in again to do this" {
		t.Fatalf("stale session: ok=%v %d %v", ok, w.Code, body)
	}

	if ok, w := attempt(false); ok || w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: ok=%v %d", ok, w.Code)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/api/ -run TestRequireStepUp`
Expected: build failure, `s.requireStepUp undefined (type *Server has no field or method requireStepUp)`.

- [ ] **Step 3: The helper**

Create `internal/api/stepup.go`:

```go
package api

import (
	"net/http"
	"time"
)

// stepUpWindow is how recently the session's credentials must have been verified for a step-up
// action: deleting a group calendar or a group, unpairing, and the People and Sign-in changes
// that can lock someone out.
var stepUpWindow = 10 * time.Minute

// requireStepUp reports whether the request's session was signed in within stepUpWindow. When it
// was not, it writes 403 reauth_required ("Sign in again to <what>") and returns false.
func (s *Server) requireStepUp(w http.ResponseWriter, r *http.Request, what string) bool {
	_, sess, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return false
	}
	if time.Since(sess.CreatedAt) > stepUpWindow {
		s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign in again to " + what, "code": "reauth_required"})
		return false
	}
	return true
}
```

In `internal/api/group_calendars.go` delete

```go
// stepUpWindow is how recently an admin must have signed in to delete a group calendar or unpair.
var stepUpWindow = 10 * time.Minute

```

and in `handleDeleteGroupCalendar` replace

```go
	_, sess, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if time.Since(sess.CreatedAt) > stepUpWindow {
		s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign in again to delete a calendar", "code": "reauth_required"})
		return
	}
```

with

```go
	if !s.requireStepUp(w, r, "delete a calendar") {
		return
	}
```

In `internal/api/backup_handlers.go` `handleUnpair`, replace the same block (its message is `"Sign in again to unpair"`) with:

```go
	if !s.requireStepUp(w, r, "unpair") {
		return
	}
```

- [ ] **Step 4: Rename the audit helper**

It records every admin change from here on, not only calendar ones.

```bash
sed -i 's/s\.auditCalendar(/s.auditAction(/g; s/^func (s \*Server) auditCalendar(/func (s *Server) auditAction(/; s#^// auditCalendar records an access change by the session user; IDs only\.#// auditAction records an admin or access change by the session user; IDs and names only.#' internal/api/group_calendars.go internal/api/calendars.go
grep -rn 'auditCalendar' internal/
```

Expected: the grep prints nothing.

- [ ] **Step 5: Run the step-up tests**

Run: `go test -count=1 ./internal/api/ -run 'TestRequireStepUp|TestDeleteGroupCalendarNeedsRecentSignIn|TestUnpairNeedsRecentSignIn'`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 6: DOX**

In `internal/api/AGENTS.md`, replace the sentence that begins ``Group calendar deletion and `DELETE /api/backup/pairing` are the step-up actions:`` (through ``else 403 `reauth_required`.``) with:

```markdown
Step-up actions call `requireStepUp(w, r, what)` (`stepup.go`): the session's credentials must be younger than 10 minutes (`stepUpWindow`, from `Session.CreatedAt`), else 403 `reauth_required` with `Sign in again to <what>`. The route tables mark every step-up route.
```

and in the bullet that lists `admin.calendar_create`, replace `carry the session user ID` with ``are written by `auditAction` and carry the session user ID``.

- [ ] **Step 7: Commit**

```bash
git add internal/api/stepup.go internal/api/stepup_internal_test.go internal/api/group_calendars.go internal/api/backup_handlers.go internal/api/calendars.go internal/api/AGENTS.md
git commit -m "refactor(api): one requireStepUp helper for every step-up route" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 6: Admin people list with search

The Groups screen needs it for search-to-add; People (PR 2) builds on it.

**Files:**
- Modify: `internal/store/models.go` (`UserFieldSearch`), `internal/store/sqlstore.go:331-397` (`ListUsers`)
- Create: `internal/store/users_search_test.go`
- Create: `internal/api/admin_users.go`, `internal/api/admin_users_test.go`
- Modify: `internal/api/server.go` (`routes`), `internal/api/authz_matrix_test.go` (`apiRows`)
- Modify: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `listPage(r) (offset, limit int)`, `UserStore.ListUsers(ctx, offset, limit int, filter UserFilter)`.
- Produces: `store.UserFieldSearch` (case-insensitive substring of username, email or display name; LIKE wildcards escaped); `type userView struct{ID, Username, DisplayName, Email, Role, Status, Source string; MFA, MustChangePassword bool; LastLoginAt *time.Time}` with json `id, username, display_name, email, role, status, source, mfa, must_change_password, last_login_at`; `func userViewOf(u *store.User) userView`; route `GET /api/admin/users?q=&offset=&limit=` → `{"users":[userView],"total":n}`.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/users_search_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestListUsersSearchMatchesSubstringsLiterally(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	for _, u := range []*store.User{
		{ID: "u1", Username: "alice", Email: "alice@example.com", DisplayName: "Alice Liddell"},
		{ID: "u2", Username: "bob", Email: "bob@example.com", DisplayName: "Bob 100%"},
		{ID: "u3", Username: "carol_x", Email: "c@example.com", DisplayName: "Carol"},
		{ID: "u4", Username: "carolyx", Email: "cy@example.com", DisplayName: "Caroly"},
	} {
		u.Role, u.Status, u.SSOProvider = "user", "active", "local"
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"LIDD", []string{"u1"}},
		{"@example.com", []string{"u1", "u2", "u3", "u4"}},
		{"100%", []string{"u2"}},
		{"%", []string{"u2"}},
		{"carol_", []string{"u3"}},
		{"nobody", nil},
	} {
		users, total, err := st.Users().ListUsers(ctx, 0, 50, store.UserFilter{Field: store.UserFieldSearch, Value: tc.q})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, u := range users {
			got[u.ID] = true
		}
		if total != len(tc.want) || len(got) != len(tc.want) {
			t.Errorf("q=%q: got %v (total %d), want %v", tc.q, got, total, tc.want)
			continue
		}
		for _, id := range tc.want {
			if !got[id] {
				t.Errorf("q=%q: missing %s in %v", tc.q, id, got)
			}
		}
	}
}
```

Create `internal/api/admin_users_test.go`:

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdminListUsersSearchesAndHidesSecrets(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	_ = loginAs(t, srv, st, "alice", "user")
	_ = loginAs(t, srv, st, "bob", "user")

	w := call(t, srv, "GET", "/api/admin/users?q=LIC", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Users []struct {
			Username string `json:"username"`
			Source   string `json:"source"`
			Role     string `json:"role"`
		} `json:"users"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Users) != 1 || body.Users[0].Username != "alice" || body.Users[0].Source != "local" {
		t.Fatalf("q=LIC: %+v", body)
	}

	all := call(t, srv, "GET", "/api/admin/users", "", admin).Body.Bytes()
	for _, secret := range []string{"argon2", "password_hash", "totp_secret", "recovery_codes"} {
		if bytes.Contains(all, []byte(secret)) {
			t.Errorf("people list leaked %q: %s", secret, all)
		}
	}
	if w := call(t, srv, "GET", "/api/admin/users?q="+strings.Repeat("x", 256), "", admin); w.Code != http.StatusBadRequest {
		t.Errorf("overlong search: %d, want 400", w.Code)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/store/ -run TestListUsersSearch`
Expected: build failure, `undefined: store.UserFieldSearch`.

- [ ] **Step 3: Store search**

In `internal/store/models.go`, add after `UserFieldDisplayName` in the `UserField` constants:

```go
	// UserFieldSearch is a case-insensitive substring of username, email or display name, for
	// the admin People list. SCIM filters never map to it.
	UserFieldSearch
```

In `internal/store/sqlstore.go`, replace `ListUsers` with:

```go
// likeEscaper escapes LIKE wildcards, so a search for "a_b" matches only that text.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (u *userStore) ListUsers(ctx context.Context, offset, limit int, filter UserFilter) ([]*User, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	where, args := "", []any{}
	if col, ok := userFilterColumns[filter.Field]; ok {
		where, args = "WHERE LOWER("+col+") = LOWER(?)", []any{filter.Value}
	} else if filter.Field == UserFieldSearch {
		p := "%" + likeEscaper.Replace(filter.Value) + "%"
		where = `WHERE LOWER(username) LIKE LOWER(?) ESCAPE '\' OR LOWER(email) LIKE LOWER(?) ESCAPE '\' OR LOWER(display_name) LIKE LOWER(?) ESCAPE '\'`
		args = []any{p, p, p}
	}

	var total int
	if err := u.store.db.QueryRowContext(ctx, u.store.rebind("SELECT COUNT(1) FROM users "+where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	listQuery := `
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, push_device_id, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users
` + where + `
ORDER BY created_at DESC LIMIT ? OFFSET ?`
	rows, err := u.store.db.QueryContext(ctx, u.store.rebind(listQuery), append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user, err := u.scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, user)
	}

	return users, total, rows.Err()
}
```

- [ ] **Step 4: The route**

Create `internal/api/admin_users.go`:

```go
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// userView is a person as the admin screens see them: never a hash, a TOTP secret or a code.
type userView struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"display_name"`
	Email              string     `json:"email"`
	Role               string     `json:"role"`
	Status             string     `json:"status"`
	Source             string     `json:"source"`
	MFA                bool       `json:"mfa"`
	MustChangePassword bool       `json:"must_change_password"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

func userViewOf(u *store.User) userView {
	return userView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email, Role: u.Role, Status: u.Status,
		Source: u.SSOProvider, MFA: u.TOTPEnabled, MustChangePassword: u.MustChangePassword, LastLoginAt: u.LastLoginAt}
}

// handleListUsers pages people, newest first; q is a case-insensitive substring of username,
// email or display name.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	offset, limit := listPage(r)
	var filter store.UserFilter
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		if len(q) > 255 {
			s.writeError(w, http.StatusBadRequest, "Search is too long")
			return
		}
		filter = store.UserFilter{Field: store.UserFieldSearch, Value: q}
	}
	users, total, err := s.store.Users().ListUsers(r.Context(), offset, limit, filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list people")
		return
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, userViewOf(u))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"users": out, "total": total})
}
```

In `internal/api/server.go` `routes()`, after `	s.handle("GET /api/admin/groups", s.requireAdmin(s.handleListGroups))` add:

```go
	s.handle("GET /api/admin/users", s.requireAdmin(s.handleListUsers))
```

In `internal/api/authz_matrix_test.go` `apiRows`, after the `"GET /api/admin/groups"` row add this row, then run `gofmt -w internal/api/authz_matrix_test.go`:

```go
		"GET /api/admin/users": {method: "GET", path: "/api/admin/users?q=owner", want: adminOnly},
```

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/store/ -run TestListUsers && go test -count=1 ./internal/api/ -run 'TestAdminListUsers|TestEveryRouteIsInTheMatrix|TestAPIAuthorizationMatrix'`
Expected: `ok` twice.

- [ ] **Step 6: DOX**

In `internal/store/AGENTS.md`, extend the `ListUsers` bullet with: ``The admin People list alone uses `UserFieldSearch`, a case-insensitive substring of username, email or display name with `%`, `_` and `\` escaped; SCIM filters never map to it.``

- [ ] **Step 7: Commit**

```bash
git add internal/store/models.go internal/store/sqlstore.go internal/store/users_search_test.go internal/store/AGENTS.md internal/api/admin_users.go internal/api/admin_users_test.go internal/api/server.go internal/api/authz_matrix_test.go
git commit -m "feat(api): admin people list with substring search" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 7: Group admin API: list, detail, create, rename

**Files:**
- Create: `internal/api/admin_groups.go`, `internal/api/admin_groups_test.go`
- Modify: `internal/api/group_calendars.go` (remove `handleListGroups`, ~:151-167)
- Modify: `internal/api/server.go` (`routes`), `internal/api/authz_matrix_test.go` (`newWorld` fixtures, `apiRows`)

**Interfaces:**
- Consumes: `requireAdmin`, `listPage`, `auditAction`, `scim.ConflictKey`, `GroupStore`, `CalendarStore.ListCalendarsByKind`/`ListGrants`; test helpers `setupTestServer`, `loginAs`, `call`, `groupCalendar`, `auditRows`.
- Produces: `type groupView struct{ID, DisplayName, Source string; MemberCount, CalendarCount int; SCIMConflict bool}`; `type memberView struct{ID, Username, DisplayName, Source string}`; `func cleanName(s string) (string, bool)`; `func (s *Server) writeManaged(w http.ResponseWriter, what string)`; `func (s *Server) grantCounts(ctx context.Context) (map[string]int, error)`; `func (s *Server) adminGroup(w http.ResponseWriter, r *http.Request, local bool) *store.Group`; `func (s *Server) writeNameTaken(w http.ResponseWriter)`; routes `GET|POST /api/admin/groups`, `GET|PATCH /api/admin/groups/{id}`. Test helpers: type `adminGroup`, `listAdminGroups`, `auditDetails`, `codeOf`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/admin_groups_test.go`:

```go
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type adminGroup struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Source        string `json:"source"`
	MemberCount   int    `json:"member_count"`
	CalendarCount int    `json:"calendar_count"`
	SCIMConflict  bool   `json:"scim_conflict"`
}

func listAdminGroups(t *testing.T, srv *api.Server, admin *http.Cookie) map[string]adminGroup {
	t.Helper()
	w := call(t, srv, "GET", "/api/admin/groups", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("list groups: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Groups []adminGroup `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]adminGroup{}
	for _, g := range body.Groups {
		out[g.DisplayName] = g
	}
	return out
}

// auditDetails returns the details of every audit row with action.
func auditDetails(t *testing.T, st store.Store, action string) []string {
	t.Helper()
	var out []string
	for _, r := range auditRows(t, st, action) {
		out = append(out, r.Details)
	}
	return out
}

func codeOf(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Code
}

func TestAdminGroupsCreateListRename(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")

	w := call(t, srv, "POST", "/api/admin/groups", `{"display_name":"  Crew "}`, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var crew adminGroup
	_ = json.Unmarshal(w.Body.Bytes(), &crew)
	if crew.DisplayName != "Crew" || crew.Source != "local" {
		t.Fatalf("created %+v, want trimmed local group", crew)
	}
	if w := call(t, srv, "POST", "/api/admin/groups", `{"display_name":"crew"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Fatalf("case twin: %d %s, want 409 name_taken", w.Code, w.Body.String())
	}
	for _, bad := range []string{`{"display_name":"   "}`, `{"display_name":"a\u0007b"}`, `{"display_name":"` + strings.Repeat("x", 256) + `"}`} {
		if w := call(t, srv, "POST", "/api/admin/groups", bad, admin); w.Code != http.StatusBadRequest {
			t.Errorf("bad name %s: %d, want 400", bad, w.Code)
		}
	}
	if len(auditDetails(t, st, "admin.group_create")) != 1 {
		t.Error("want one admin.group_create audit row")
	}

	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_bob", Username: "bob", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s-bob"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_scim", DisplayName: "Synced", Source: store.GroupSourceSCIM}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, "grp_scim", "usr_bob"); err != nil {
		t.Fatal(err)
	}
	cal := groupCalendar(t, st, "Rota")
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: crew.ID, Role: "reader"}); err != nil {
		t.Fatal(err)
	}
	groups := listAdminGroups(t, srv, admin)
	if g := groups["Crew"]; g.Source != "local" || g.MemberCount != 0 || g.CalendarCount != 1 {
		t.Errorf("Crew in list: %+v", g)
	}
	if g := groups["Synced"]; g.Source != "scim" || g.MemberCount != 1 || g.CalendarCount != 0 {
		t.Errorf("Synced in list: %+v", g)
	}

	w = call(t, srv, "GET", "/api/admin/groups/grp_scim", "", admin)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"username":"bob"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"source":"scim"`)) {
		t.Fatalf("SCIM group detail: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/admin/groups/grp_missing", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("missing group: %d, want 404", w.Code)
	}

	if w := call(t, srv, "PATCH", "/api/admin/groups/"+crew.ID, `{"display_name":"Crew 2"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/"+crew.ID, `{"display_name":"SYNCED"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Errorf("rename onto a case twin: %d %s, want 409 name_taken", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/groups/grp_scim", `{"display_name":"Mine now"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
		t.Errorf("rename a SCIM group: %d %s, want 409 managed_externally", w.Code, w.Body.String())
	}
	if d := auditDetails(t, st, "admin.group_rename"); len(d) != 1 || !strings.Contains(d[0], `to="Crew 2"`) {
		t.Errorf("rename audit: %v", d)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/api/ -run TestAdminGroupsCreateListRename`
Expected: FAIL, `create: 200 …` (no route yet; the SPA catch-all answers the POST).

- [ ] **Step 3: Implement**

Create `internal/api/admin_groups.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type groupView struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Source        string `json:"source"`
	MemberCount   int    `json:"member_count"`
	CalendarCount int    `json:"calendar_count"`
	// SCIMConflict marks a local group whose name SCIM tried and failed to create.
	SCIMConflict bool `json:"scim_conflict,omitempty"`
}

type memberView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Source      string `json:"source"`
}

// cleanName trims a display name; ok is false when it is empty, over 255 bytes or holds a
// control character.
func cleanName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, s != "" && len(s) <= 255 && strings.IndexFunc(s, unicode.IsControl) < 0
}

// writeManaged answers a write to a synced person or group: its identity provider owns it.
func (s *Server) writeManaged(w http.ResponseWriter, what string) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "This " + what + " is managed by your identity provider; change it there", "code": "managed_externally"})
}

// grantCounts maps each group ID to the number of group calendars that grant it a role.
func (s *Server) grantCounts(ctx context.Context) (map[string]int, error) {
	cals, err := s.store.Calendars().ListCalendarsByKind(ctx, "group")
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, c := range cals {
		gs, err := s.store.Calendars().ListGrants(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			counts[g.GroupID]++
		}
	}
	return counts, nil
}

// adminGroup loads the {id} group: 404 when missing; with local set, 409 managed_externally for
// a SCIM group.
func (s *Server) adminGroup(w http.ResponseWriter, r *http.Request, local bool) *store.Group {
	g, err := s.store.Groups().GetGroupByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such group")
		return nil
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the group")
		return nil
	}
	if local && g.Source != store.GroupSourceLocal {
		s.writeManaged(w, "group")
		return nil
	}
	return g
}

func (s *Server) writeNameTaken(w http.ResponseWriter) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "A group with this name already exists", "code": "name_taken"})
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	offset, limit := listPage(r)
	groups, total, err := s.store.Groups().ListGroups(r.Context(), offset, limit, "")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	counts, err := s.grantCounts(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count group calendars")
		return
	}
	settings, err := s.store.Settings().GetAllSettings(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load settings")
		return
	}
	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		v := groupView{ID: g.ID, DisplayName: g.DisplayName, Source: g.Source, MemberCount: len(g.Members), CalendarCount: counts[g.ID]}
		v.SCIMConflict = g.Source == store.GroupSourceLocal && settings[scim.ConflictKey(g.DisplayName)] != ""
		out = append(out, v)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"groups": out, "total": total})
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	g := s.adminGroup(w, r, false)
	if g == nil {
		return
	}
	counts, err := s.grantCounts(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count group calendars")
		return
	}
	members := make([]memberView, 0, len(g.Members))
	for _, id := range g.Members {
		u, err := s.store.Users().GetUserByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to load members")
			return
		}
		members = append(members, memberView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Source: u.SSOProvider})
	}
	slices.SortFunc(members, func(a, b memberView) int {
		return strings.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username))
	})
	s.writeJSON(w, http.StatusOK, map[string]any{
		"id": g.ID, "display_name": g.DisplayName, "source": g.Source, "calendar_count": counts[g.ID], "members": members,
	})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	name, ok := cleanName(body.DisplayName)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Name must be 1-255 characters with no control characters")
		return
	}
	g := &store.Group{ID: "grp_" + crypto.RandomHex(12), DisplayName: name, Source: store.GroupSourceLocal}
	if err := s.store.Groups().CreateGroup(r.Context(), g); errors.Is(err, store.ErrAlreadyExists) {
		s.writeNameTaken(w)
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to create the group")
		return
	}
	s.auditAction(r.Context(), r, "admin.group_create", g.ID, "name="+strconv.Quote(name))
	s.writeJSON(w, http.StatusCreated, groupView{ID: g.ID, DisplayName: g.DisplayName, Source: g.Source})
}

func (s *Server) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	name, ok := cleanName(body.DisplayName)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Name must be 1-255 characters with no control characters")
		return
	}
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	from := g.DisplayName
	g.DisplayName = name
	if err := s.store.Groups().UpdateGroup(r.Context(), g); errors.Is(err, store.ErrAlreadyExists) {
		s.writeNameTaken(w)
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to rename the group")
		return
	}
	// The old name is free now; a SCIM clash recorded against it is resolved.
	_ = s.store.Settings().DeleteSetting(r.Context(), scim.ConflictKey(from))
	s.auditAction(r.Context(), r, "admin.group_rename", g.ID, "from="+strconv.Quote(from)+" to="+strconv.Quote(name))
	s.writeJSON(w, http.StatusOK, groupView{ID: g.ID, DisplayName: g.DisplayName, Source: g.Source, MemberCount: len(g.Members)})
}
```

Delete `handleListGroups` from `internal/api/group_calendars.go` (it now lives in `admin_groups.go`).

In `internal/api/server.go` `routes()`, after the `GET /api/admin/groups` line add:

```go
	s.handle("POST /api/admin/groups", s.requireAdmin(s.handleCreateGroup))
	s.handle("GET /api/admin/groups/{id}", s.requireAdmin(s.handleGetGroup))
	s.handle("PATCH /api/admin/groups/{id}", s.requireAdmin(s.handleRenameGroup))
```

- [ ] **Step 4: Matrix fixtures and rows**

In `internal/api/authz_matrix_test.go` `newWorld`, replace

```go
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_extra", DisplayName: "Extra"}); err != nil {
		t.Fatal(err)
	}
```

with

```go
	// grp_extra takes grants; grp_matrix takes members and a rename, so no row's membership
	// change can grant a caller access another row expects refused; grp_doomed is deleted.
	for _, g := range []*store.Group{{ID: "grp_extra", DisplayName: "Extra"}, {ID: "grp_matrix", DisplayName: "Matrix"}, {ID: "grp_doomed", DisplayName: "Doomed"}} {
		if err := st.Groups().CreateGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
```

and add to `apiRows` after `"GET /api/admin/groups"` (then `gofmt -w internal/api/authz_matrix_test.go`):

```go
		"POST /api/admin/groups":       {method: "POST", path: "/api/admin/groups", body: `{"display_name":"Matrix new"}`, want: adminOnly},
		"GET /api/admin/groups/{id}":   {method: "GET", path: "/api/admin/groups/grp_matrix", want: adminOnly},
		"PATCH /api/admin/groups/{id}": {method: "PATCH", path: "/api/admin/groups/grp_matrix", body: `{"display_name":"Matrix"}`, want: adminOnly},
```

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/api/ -run 'TestAdminGroups|TestAdminCreatesGroupCalendarAndGrants|TestEveryRouteIsInTheMatrix|TestAPIAuthorizationMatrix'`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin_groups.go internal/api/admin_groups_test.go internal/api/group_calendars.go internal/api/server.go internal/api/authz_matrix_test.go
git commit -m "feat(api): create, list and rename local groups" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 8: Group members

**Files:**
- Modify: `internal/api/admin_groups.go` (append), `internal/api/server.go`, `internal/api/authz_matrix_test.go`
- Create: `internal/api/admin_group_members_test.go`

**Interfaces:**
- Consumes: `adminGroup`, `auditAction`, `GroupStore.AddGroupMember` (`ON CONFLICT DO NOTHING`), `RemoveGroupMember`.
- Produces: `PUT|DELETE /api/admin/groups/{id}/members/{userId}` → 204, idempotent, audited only on a change; 404 no such person; 409 `admin_member`, `inactive_member`, `managed_externally`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/admin_group_members_test.go`:

```go
package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestAdminGroupMembers(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	bob := loginAs(t, srv, st, "bob", "user")
	_ = loginAs(t, srv, st, "other", "admin")
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_gone", Username: "gone", Role: "user", Status: "inactive", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_crew", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_scim", DisplayName: "Synced", Source: store.GroupSourceSCIM}); err != nil {
		t.Fatal(err)
	}
	cal := groupCalendar(t, st, "Rota")
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: "grp_crew", Role: "reader"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // idempotent
		if w := call(t, srv, "PUT", "/api/admin/groups/grp_crew/members/usr_bob", "", admin); w.Code != http.StatusNoContent {
			t.Fatalf("add bob (%d): %d %s", i, w.Code, w.Body.String())
		}
	}
	if n := len(auditDetails(t, st, "admin.group_member_add")); n != 1 {
		t.Errorf("repeated add audited %d times, want once", n)
	}
	if w := call(t, srv, "GET", "/api/calendars", "", bob); !bytes.Contains(w.Body.Bytes(), []byte(cal.ID)) {
		t.Fatalf("member does not see the group calendar: %s", w.Body.String())
	}

	for _, tc := range []struct{ path, code string }{
		{"/api/admin/groups/grp_crew/members/usr_other", "admin_member"},
		{"/api/admin/groups/grp_crew/members/usr_gone", "inactive_member"},
		{"/api/admin/groups/grp_scim/members/usr_bob", "managed_externally"},
	} {
		if w := call(t, srv, "PUT", tc.path, "", admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != tc.code {
			t.Errorf("PUT %s: %d %s, want 409 %s", tc.path, w.Code, w.Body.String(), tc.code)
		}
	}
	if w := call(t, srv, "PUT", "/api/admin/groups/grp_crew/members/usr_nobody", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("unknown person: %d, want 404", w.Code)
	}

	for i := 0; i < 2; i++ {
		if w := call(t, srv, "DELETE", "/api/admin/groups/grp_crew/members/usr_bob", "", admin); w.Code != http.StatusNoContent {
			t.Fatalf("remove bob (%d): %d %s", i, w.Code, w.Body.String())
		}
	}
	// Access is resolved live: the calendar is gone, the session is not.
	w := call(t, srv, "GET", "/api/calendars", "", bob)
	if w.Code != http.StatusOK || bytes.Contains(w.Body.Bytes(), []byte(cal.ID)) {
		t.Fatalf("after removal: %d %s", w.Code, w.Body.String())
	}
	if n := len(auditDetails(t, st, "admin.group_member_remove")); n != 1 {
		t.Errorf("repeated remove audited %d times, want once", n)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/api/ -run TestAdminGroupMembers`
Expected: FAIL, `add bob (0): 200 …` (no route yet; the SPA answers).

- [ ] **Step 3: Implement**

Append to `internal/api/admin_groups.go`:

```go
// handleAddGroupMember adds an active everyday user to a local group; adding a member twice is
// not an error. Administrators never see calendars, so they cannot be members.
func (s *Server) handleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	u, err := s.store.Users().GetUserByID(r.Context(), r.PathValue("userId"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such person")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the person")
		return
	}
	if u.Role == "admin" {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Administrators cannot be group members: they never see calendars", "code": "admin_member"})
		return
	}
	if u.Status != "active" {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Only active people can join a group", "code": "inactive_member"})
		return
	}
	if slices.Contains(g.Members, u.ID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.Groups().AddGroupMember(r.Context(), g.ID, u.ID); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to add the member")
		return
	}
	s.auditAction(r.Context(), r, "admin.group_member_add", g.ID, "user="+strconv.Quote(u.ID))
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveGroupMember removes a member of a local group; removing a non-member is not an
// error. Access follows membership at the next request, so nothing else is revoked.
func (s *Server) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	userID := r.PathValue("userId")
	if !slices.Contains(g.Members, userID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.Groups().RemoveGroupMember(r.Context(), g.ID, userID); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to remove the member")
		return
	}
	s.auditAction(r.Context(), r, "admin.group_member_remove", g.ID, "user="+strconv.Quote(userID))
	w.WriteHeader(http.StatusNoContent)
}
```

In `routes()`, after the `PATCH /api/admin/groups/{id}` line:

```go
	s.handle("PUT /api/admin/groups/{id}/members/{userId}", s.requireAdmin(s.handleAddGroupMember))
	s.handle("DELETE /api/admin/groups/{id}/members/{userId}", s.requireAdmin(s.handleRemoveGroupMember))
```

In `apiRows` (then `gofmt -w internal/api/authz_matrix_test.go`):

```go
		"PUT /api/admin/groups/{id}/members/{userId}":    {method: "PUT", path: "/api/admin/groups/grp_matrix/members/usr_nonmember", want: adminOnly},
		"DELETE /api/admin/groups/{id}/members/{userId}": {method: "DELETE", path: "/api/admin/groups/grp_matrix/members/usr_nonmember", want: adminOnly},
```

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/api/ -run 'TestAdminGroup|TestEveryRouteIsInTheMatrix|TestAPIAuthorizationMatrix|TestMembershipRemovalTakesEffectNextRequest'`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin_groups.go internal/api/admin_group_members_test.go internal/api/server.go internal/api/authz_matrix_test.go
git commit -m "feat(api): add and remove local group members" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 9: Delete a group

**Files:**
- Modify: `internal/api/admin_groups.go` (append), `internal/api/server.go`, `internal/api/authz_matrix_test.go`
- Create: `internal/api/admin_group_delete_test.go`
- Modify: `internal/api/AGENTS.md`, `AGENTS.md` (root)

**Interfaces:**
- Consumes: `requireStepUp`, `adminGroup`, `grantCounts`, `tracked`, `scim.ConflictKey`.
- Produces: `DELETE /api/admin/groups/{id}` → 204; step-up; local only; runs on `context.WithoutCancel` and is registered `s.tracked(s.requireAdmin(...))`; audit details `name="…" calendars=N`; clears the SCIM clash flag.

- [ ] **Step 1: Write the failing test**

Create `internal/api/admin_group_delete_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestAdminDeleteGroup(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_crew", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_scim", DisplayName: "Synced", Source: store.GroupSourceSCIM}); err != nil {
		t.Fatal(err)
	}
	cal := groupCalendar(t, st, "Rota")
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: "grp_crew", Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().SetSetting(ctx, scim.ConflictKey("Crew"), "2026-10-07T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if !listAdminGroups(t, srv, admin)["Crew"].SCIMConflict {
		t.Fatal("a SCIM clash on Crew is not flagged in the list")
	}

	restore := api.SetStepUpWindowForTest(0)
	w := call(t, srv, "DELETE", "/api/admin/groups/grp_crew", "", admin)
	restore()
	if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
		t.Fatalf("stale session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Groups().GetGroupByID(ctx, "grp_crew"); err != nil {
		t.Fatalf("a refused delete removed the group: %v", err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/groups/grp_scim", "", admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
		t.Errorf("delete a SCIM group: %d %s, want 409 managed_externally", w.Code, w.Body.String())
	}

	if w := call(t, srv, "DELETE", "/api/admin/groups/grp_crew", "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Calendars().GetCalendarByID(ctx, cal.ID); err != nil {
		t.Fatalf("the group calendar must survive its group: %v", err)
	}
	if gs, _ := st.Calendars().ListGrants(ctx, cal.ID); len(gs) != 0 {
		t.Fatalf("grants survived the group: %+v", gs)
	}
	if _, err := st.Settings().GetSetting(ctx, scim.ConflictKey("Crew")); err == nil {
		t.Error("the SCIM clash flag outlived the local group")
	}
	if d := auditDetails(t, st, "admin.group_delete"); len(d) != 1 || !strings.Contains(d[0], "calendars=1") {
		t.Errorf("delete audit: %v", d)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/api/ -run TestAdminDeleteGroup`
Expected: FAIL, `stale session: 200 …`.

- [ ] **Step 3: Implement**

Append to `internal/api/admin_groups.go`:

```go
// handleDeleteGroup deletes a local group with its memberships and calendar grants; the group
// calendars stay. A step-up action, detached so a dropped connection cannot lose the audit row.
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "delete a group") {
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	counts, err := s.grantCounts(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count group calendars")
		return
	}
	if err := s.store.Groups().DeleteGroup(r.Context(), g.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the group")
		return
	}
	_ = s.store.Settings().DeleteSetting(r.Context(), scim.ConflictKey(g.DisplayName))
	s.auditAction(r.Context(), r, "admin.group_delete", g.ID, "name="+strconv.Quote(g.DisplayName)+" calendars="+strconv.Itoa(counts[g.ID]))
	w.WriteHeader(http.StatusNoContent)
}
```

In `routes()`, after the `PATCH /api/admin/groups/{id}` line:

```go
	s.handle("DELETE /api/admin/groups/{id}", s.tracked(s.requireAdmin(s.handleDeleteGroup)))
```

In `apiRows` (then `gofmt -w internal/api/authz_matrix_test.go`):

```go
		"DELETE /api/admin/groups/{id}": {method: "DELETE", path: "/api/admin/groups/grp_doomed", want: adminOnly},
```

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/api/ -run 'TestAdminDeleteGroup|TestGroupDeletionLeavesCalendarUnassigned|TestEveryRouteIsInTheMatrix|TestAPIAuthorizationMatrix'`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 5: DOX**

In `internal/api/AGENTS.md`, replace the `GET /api/admin/groups` row of the admin table with:

```markdown
| GET | `/api/admin/groups` | admin | `{groups:[{id,display_name,source,member_count,calendar_count,scim_conflict?}],total}`, `offset`/`limit` <= 200 |
| POST | `/api/admin/groups` | admin | `{display_name}` -> 201 local group; 400 empty, over 255 bytes or control characters; 409 `name_taken` (any case) |
| GET | `/api/admin/groups/{id}` | admin | `{id,display_name,source,calendar_count,members:[{id,username,display_name,source}]}` |
| PATCH | `/api/admin/groups/{id}` | admin; local | `{display_name}` -> 200; 409 `name_taken`, `managed_externally` |
| DELETE | `/api/admin/groups/{id}` | admin + step-up; local; detached, tracked | 204; memberships and grants cascade, group calendars stay |
| PUT | `/api/admin/groups/{id}/members/{userId}` | admin; local | 204, idempotent; 404 no person; 409 `admin_member`, `inactive_member`, `managed_externally` |
| DELETE | `/api/admin/groups/{id}/members/{userId}` | admin; local | 204, idempotent |
| GET | `/api/admin/users?q=` | admin | `{users:[{id,username,display_name,email,role,status,source,mfa,must_change_password,last_login_at}],total}`; `q` a case-insensitive substring (400 over 255 bytes); never a hash |
```

In the root `AGENTS.md`, append to the User Preferences bullet that ends with the standalone design link: ``Plan: `docs/superpowers/plans/2026-10-07-kycalendar-standalone-admin.md`.`` Then insert before `#### Server child DOX index`:

```markdown
#### Standalone administration contracts

Spec `docs/superpowers/specs/2026-10-07-kycalendar-standalone-admin-design.md`.

- Groups have an owner, `groups.source` (migration 10): `local` (the Groups screen) or `scim`; rows older than migration 10 are `scim`. Admin writes reach local groups only (409 `managed_externally`); SCIM sees, reads and writes only `scim` groups, so a reconciling IdP never renames, empties or deletes a local group. Names are unique ignoring case across both owners; a SCIM create that collides with a local name is a SCIM 409 and sets `scim.ConflictKey(name)`, which the Groups screen flags until the local group is renamed or deleted.
- Group members are active everyday users (409 `admin_member`, `inactive_member`). Membership changes revoke nothing: `access.Resolve` reads membership on every request, so CalDAV clients see the change at their next sync. Deleting a group (step-up) cascades memberships and grants; group calendars stay.
- `requireStepUp` (10 minutes, 403 `reauth_required`) guards every step-up route.
- Audit: `admin.group_create`, `admin.group_rename`, `admin.group_delete` (details `calendars=N`), `admin.group_member_add`, `admin.group_member_remove`; a repeated, idempotent call writes no row.
```

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin_groups.go internal/api/admin_group_delete_test.go internal/api/server.go internal/api/authz_matrix_test.go internal/api/AGENTS.md AGENTS.md
git commit -m "feat(api): delete local groups behind step-up" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 10: Groups page

**Files:**
- Create: `web/src/admin.tsx`, `web/src/pages/Groups.tsx`, `web/src/pages/Groups.test.tsx`
- Modify: `web/AGENTS.md`; rebuilt `web/dist`

**Interfaces:**
- Consumes: `secureFetch` (`web/src/api.ts`); routes from Tasks 6-9.
- Produces: `admin.tsx` exports `JSON_HEADERS`, `send(url, init?) => Promise<Response | null>`, `interface ApiFailure {error?, code?, count?}`, `failure(res) => Promise<ApiFailure>`, `REAUTH`, `managedBy(source) => string | null`, `SourceBadge({source})`; `pages/Groups.tsx` default export `Groups()`.

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/Groups.test.tsx`:

```tsx
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Groups from "./Groups";

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
    return new Response(body === null ? null : JSON.stringify(body), { status: body === null ? 204 : 200 });
  });
}

const crew = { id: "grp_1", display_name: "Crew", source: "local", member_count: 0, calendar_count: 2 };
const synced = { id: "grp_2", display_name: "Synced", source: "scim", member_count: 1, calendar_count: 0 };
const list = (...groups: unknown[]) => ({ groups, total: groups.length });

describe("Groups", () => {
  it("creates a local group", async () => {
    let posted: unknown;
    mockFetch({
      "GET /api/admin/groups": () => list(),
      "POST /api/admin/groups": (init) => {
        posted = JSON.parse(String(init?.body));
        return crew;
      },
    });
    render(<Groups />);
    fireEvent.change(screen.getByLabelText("Group name"), { target: { value: "Crew" } });
    fireEvent.click(screen.getByRole("button", { name: "Create group" }));
    await waitFor(() => expect(posted).toEqual({ display_name: "Crew" }));
  });

  it("shows synced groups read-only", async () => {
    mockFetch({
      "GET /api/admin/groups": () => list(synced),
      "GET /api/admin/groups/grp_2": () => ({ ...synced, members: [{ id: "u1", username: "bob", display_name: "Bob", source: "scim" }] }),
    });
    render(<Groups />);
    const card = await screen.findByRole("article", { name: "Synced" });
    expect(within(card).getByText("Managed by SCIM")).toBeTruthy();
    expect(within(card).queryByRole("button", { name: "Rename" })).toBeNull();
    expect(within(card).queryByRole("button", { name: "Delete group" })).toBeNull();
    fireEvent.click(within(card).getByRole("button", { name: "Members" }));
    expect(await within(card).findByText("Bob")).toBeTruthy();
    expect(within(card).queryByRole("button", { name: /Remove/ })).toBeNull();
    expect(within(card).queryByLabelText("Find a person")).toBeNull();
  });

  it("names the calendars a delete affects and asks for a fresh sign-in", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/groups": () => list(crew),
      "DELETE /api/admin/groups/grp_1": () => new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<Groups />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete group" }));
    expect(confirm.mock.calls[0][0]).toMatch(/2 group calendars lose this group's access/);
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("adds only active everyday people found by search", async () => {
    const put = vi.fn(() => null);
    mockFetch({
      "GET /api/admin/groups": () => list(crew),
      "GET /api/admin/groups/grp_1": () => ({ ...crew, members: [] }),
      "GET /api/admin/users?q=wal": () => ({
        users: [
          { id: "u1", username: "walter", display_name: "Walter", role: "user", status: "active" },
          { id: "u2", username: "walt-admin", display_name: "Walt", role: "admin", status: "active" },
          { id: "u3", username: "wally", display_name: "Wally", role: "user", status: "inactive" },
        ],
        total: 3,
      }),
      "PUT /api/admin/groups/grp_1/members/u1": put,
    });
    render(<Groups />);
    const card = await screen.findByRole("article", { name: "Crew" });
    fireEvent.click(within(card).getByRole("button", { name: "Members" }));
    fireEvent.change(await within(card).findByLabelText("Find a person"), { target: { value: "wal" } });
    fireEvent.click(within(card).getByRole("button", { name: "Search" }));
    const add = await within(card).findByRole("button", { name: "Add walter" });
    expect(within(card).queryByRole("button", { name: "Add walt-admin" })).toBeNull();
    expect(within(card).queryByRole("button", { name: "Add wally" })).toBeNull();
    fireEvent.click(add);
    await waitFor(() => expect(put).toHaveBeenCalled());
  });

  it("flags a local group whose name SCIM could not create", async () => {
    mockFetch({ "GET /api/admin/groups": () => list({ ...crew, scim_conflict: true }) });
    render(<Groups />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/cannot create it/);
  });

  it("alerts when the list cannot load", async () => {
    mockFetch({ "GET /api/admin/groups": () => new Response("{}", { status: 500 }) });
    render(<Groups />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load/i);
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/pages/Groups.test.tsx`
Expected: FAIL, `Failed to resolve import "./Groups"`.

- [ ] **Step 3: Shared admin helpers**

Create `web/src/admin.tsx`:

```tsx
import { secureFetch } from "./api";

export const JSON_HEADERS = { "Content-Type": "application/json" };

/** Resolves to null on a network error, so every caller handles one failure path. */
export const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

export interface ApiFailure {
  error?: string;
  code?: string;
  count?: number;
}

export async function failure(res: Response | null): Promise<ApiFailure> {
  return (await res?.json().catch(() => null)) ?? {};
}

export const REAUTH = "Sign out and sign in again, then try again within 10 minutes.";

/** The badge for a synced person or group; local ones have none. */
export function managedBy(source: string): string | null {
  switch (source) {
    case "local":
      return null;
    case "scim":
      return "Managed by SCIM";
    case "kysignon":
      return "Managed by KyIdentity";
    case "oidc":
      return "Managed by the sign-in provider";
    default:
      return `Managed by ${source}`;
  }
}

export function SourceBadge({ source }: { source: string }) {
  const label = managedBy(source);
  return label ? <span className="badge">{label}</span> : null;
}
```

- [ ] **Step 4: The page**

Create `web/src/pages/Groups.tsx`:

```tsx
import { useCallback, useEffect, useState } from "react";
import { JSON_HEADERS, REAUTH, SourceBadge, failure, send } from "../admin";

interface Group {
  id: string;
  display_name: string;
  source: string;
  member_count: number;
  calendar_count: number;
  scim_conflict?: boolean;
}

interface Member {
  id: string;
  username: string;
  display_name: string;
  source: string;
}

interface Person {
  id: string;
  username: string;
  display_name: string;
  role: string;
  status: string;
}

function calendarsLosing(n: number): string {
  return n === 1 ? "1 group calendar loses" : `${n} group calendars lose`;
}

export default function Groups() {
  const [groups, setGroups] = useState<Group[]>([]);
  const [name, setName] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const res = await send("/api/admin/groups");
    if (!res?.ok) {
      setError("Could not load groups. Reload the page to try again.");
      return;
    }
    setGroups((await res.json()).groups);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send("/api/admin/groups", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify({ display_name: name }) });
    if (!res?.ok) {
      setError((await failure(res)).error ?? "Could not create the group");
      return;
    }
    setName("");
    await load();
  }

  async function rename(g: Group, next: string) {
    setError(null);
    const res = await send(`/api/admin/groups/${encodeURIComponent(g.id)}`, {
      method: "PATCH",
      headers: JSON_HEADERS,
      body: JSON.stringify({ display_name: next }),
    });
    if (!res?.ok) {
      setError((await failure(res)).error ?? "Could not rename the group");
      return;
    }
    await load();
  }

  async function remove(g: Group) {
    if (!window.confirm(`Delete ${g.display_name}? ${calendarsLosing(g.calendar_count)} this group's access. The calendars stay.`)) return;
    setError(null);
    const res = await send(`/api/admin/groups/${encodeURIComponent(g.id)}`, { method: "DELETE" });
    if (res?.status === 403 && (await failure(res)).code === "reauth_required") {
      setError(REAUTH);
      return;
    }
    if (!res?.ok) {
      setError("Could not delete the group. It still exists.");
      return;
    }
    if (open === g.id) setOpen(null);
    await load();
  }

  return (
    <section className="page">
      <h1>Groups</h1>
      <p>
        Groups give people access to group calendars. Groups from your identity provider are read-only here; change them
        there.
      </p>
      <form onSubmit={create}>
        <label>
          Group name
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={255} required />
        </label>
        <button type="submit">Create group</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {groups.length === 0 ? (
        <p>No groups yet.</p>
      ) : (
        groups.map((g) => (
          <GroupCard
            key={g.id}
            group={g}
            open={open === g.id}
            onToggle={() => setOpen(open === g.id ? null : g.id)}
            onRename={rename}
            onDelete={remove}
            onChanged={load}
            onError={setError}
          />
        ))
      )}
    </section>
  );
}

interface CardProps {
  group: Group;
  open: boolean;
  onToggle: () => void;
  onRename: (g: Group, next: string) => Promise<void>;
  onDelete: (g: Group) => Promise<void>;
  onChanged: () => Promise<void>;
  onError: (message: string) => void;
}

function GroupCard({ group, open, onToggle, onRename, onDelete, onChanged, onError }: CardProps) {
  const local = group.source === "local";
  const [draft, setDraft] = useState(group.display_name);

  return (
    <article aria-labelledby={`group-${group.id}`}>
      <h2 id={`group-${group.id}`}>{group.display_name}</h2>
      <SourceBadge source={group.source} />
      <p>
        {group.member_count} {group.member_count === 1 ? "member" : "members"} · {group.calendar_count} group{" "}
        {group.calendar_count === 1 ? "calendar" : "calendars"}
      </p>
      {group.scim_conflict && (
        <p role="alert">
          Your identity provider has a group with this name and cannot create it. Rename this group to let it through.
        </p>
      )}
      {local && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void onRename(group, draft);
          }}
        >
          <label htmlFor={`rename-${group.id}`}>New name</label>
          <input id={`rename-${group.id}`} value={draft} onChange={(e) => setDraft(e.target.value)} maxLength={255} required />
          <button type="submit">Rename</button>
        </form>
      )}
      <button type="button" aria-expanded={open} onClick={onToggle}>
        Members
      </button>
      {local && (
        <button type="button" onClick={() => void onDelete(group)}>
          Delete group
        </button>
      )}
      {open && <MembersPanel group={group} onChanged={onChanged} onError={onError} />}
    </article>
  );
}

function MembersPanel({ group, onChanged, onError }: { group: Group; onChanged: () => Promise<void>; onError: (m: string) => void }) {
  const local = group.source === "local";
  const [members, setMembers] = useState<Member[]>([]);
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<Person[] | null>(null);
  const base = `/api/admin/groups/${encodeURIComponent(group.id)}`;

  const load = useCallback(async () => {
    const res = await send(base);
    if (!res?.ok) {
      onError("Could not load the members.");
      return;
    }
    setMembers((await res.json()).members);
  }, [base, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  async function search(e: React.FormEvent) {
    e.preventDefault();
    const res = await send(`/api/admin/users?q=${encodeURIComponent(query)}`);
    if (!res?.ok) {
      onError("Could not search people.");
      return;
    }
    const people: Person[] = (await res.json()).users;
    // Administrators never see calendars and inactive people cannot sign in: neither can join.
    setFound(people.filter((p) => p.role === "user" && p.status === "active" && !members.some((m) => m.id === p.id)));
  }

  async function change(method: "PUT" | "DELETE", userId: string, failed: string) {
    const res = await send(`${base}/members/${encodeURIComponent(userId)}`, { method });
    if (!res?.ok) {
      onError((await failure(res)).error ?? failed);
      return;
    }
    setFound(null);
    await load();
    await onChanged();
  }

  return (
    <div>
      {members.length === 0 ? (
        <p>No members.</p>
      ) : (
        <ul aria-label={`Members of ${group.display_name}`}>
          {members.map((m) => (
            <li key={m.id}>
              <span>{m.display_name || m.username}</span> <small>{m.username}</small>{" "}
              {local && (
                <button type="button" aria-label={`Remove ${m.username}`} onClick={() => void change("DELETE", m.id, "Could not remove the member")}>
                  Remove
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {local && (
        <form onSubmit={search}>
          <label htmlFor={`find-${group.id}`}>Find a person</label>
          <input id={`find-${group.id}`} value={query} onChange={(e) => setQuery(e.target.value)} maxLength={255} />
          <button type="submit">Search</button>
        </form>
      )}
      {found && (found.length === 0 ? (
        <p>No one else can join: only active everyday people can be members.</p>
      ) : (
        <ul aria-label="Search results">
          {found.map((p) => (
            <li key={p.id}>
              <span>{p.display_name || p.username}</span>{" "}
              <button type="button" aria-label={`Add ${p.username}`} onClick={() => void change("PUT", p.id, "Could not add the member")}>
                Add
              </button>
            </li>
          ))}
        </ul>
      ))}
    </div>
  );
}
```

- [ ] **Step 5: Run the tests and build**

Run: `cd web && npx vitest run src/pages/Groups.test.tsx && npm run build`
Expected: `6 passed`; the build rewrites `web/dist`.

- [ ] **Step 6: DOX**

In `web/AGENTS.md` Local Contracts, after the `GroupCalendars.tsx` bullet add:

```markdown
- `src/admin.tsx` holds the admin screens' fetch helpers (`send` resolves to null on a network error, `failure` reads `{error, code, count}`), the step-up message and `SourceBadge` ("Managed by …" for any source but `local`).
- `src/pages/Groups.tsx` is admin-only (tab `groups`): create, rename and delete local groups, and add or remove members found by search (only active everyday people are offered). SCIM groups show their badge and their members read-only. The delete confirm names how many group calendars lose the group's access; a 403 `reauth_required` asks for a fresh sign-in; a `scim_conflict` group shows an alert to rename it.
```

- [ ] **Step 7: Commit**

```bash
git add web/src/admin.tsx web/src/pages/Groups.tsx web/src/pages/Groups.test.tsx web/AGENTS.md web/dist
git commit -m "feat(web): Groups screen for local groups and members" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 11: Admin-only navigation, Groups item and card

**Files:**
- Modify: `web/src/App.tsx:1-14,102-122`, `web/src/components/AppHeader.tsx:2,14-22`, `web/src/pages/Dashboard.tsx:2,11-12`, `web/src/App.test.tsx`
- Modify: `README.md` (new Groups section), `web/AGENTS.md`; rebuilt `web/dist`

**Interfaces:**
- Consumes: `Groups` (Task 10).
- Produces: tab `groups`; `dashboard`, `scim`, `backup`, `settings`, `group-calendars` and `groups` render only for `user.role === 'admin'`; everyday users see only Calendar and Phones & apps.

- [ ] **Step 1: Write the failing tests**

In `web/src/App.test.tsx`, change the first import to `import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';`, add to the fetch mock in `signedInAs`, after the `/api/calendars` line:

```tsx
    if (path === '/api/admin/groups') return new Response('{"groups":[],"total":0}', { status: 200 });
```

and append inside `describe('App landing', …)`:

```tsx
  it('keeps admin pages from everyday users', async () => {
    signedInAs('user');
    render(<App />);
    const nav = await screen.findByRole('navigation', { name: 'Primary' });
    for (const label of ['Overview', 'Groups', 'Directory & SCIM', 'KyBackup (Feature 0)', 'Settings & DB']) {
      expect(within(nav).queryByRole('button', { name: label })).toBeNull();
    }
  });

  it('opens the Groups page for admins', async () => {
    signedInAs('admin');
    render(<App />);
    const nav = await screen.findByRole('navigation', { name: 'Primary' });
    fireEvent.click(within(nav).getByRole('button', { name: 'Groups' }));
    expect(await screen.findByRole('heading', { name: 'Groups' })).toBeTruthy();
  });
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/App.test.tsx`
Expected: FAIL: the everyday nav still has an Overview button, and `Unable to find role="button" and name "Groups"`.

- [ ] **Step 3: Navigation**

In `web/src/components/AppHeader.tsx`, add `UsersRound` to the `lucide-react` import and replace the `navItems` array with:

```tsx
  // Administrators run the instance and never see calendars; everyday users see nothing else.
  const navItems = user?.role === 'admin'
    ? [
        { id: 'dashboard', label: 'Overview', icon: LayoutDashboard },
        { id: 'groups', label: 'Groups', icon: UsersRound },
        { id: 'group-calendars', label: 'Group calendars', icon: CalendarDays },
        { id: 'scim', label: 'Directory & SCIM', icon: Users },
        { id: 'backup', label: 'KyBackup (Feature 0)', icon: Archive },
        { id: 'settings', label: 'Settings & DB', icon: SettingsIcon },
      ]
    : [
        { id: 'calendar', label: 'Calendar', icon: CalendarDays },
        { id: 'devices', label: 'Phones & apps', icon: Smartphone },
      ];
```

- [ ] **Step 4: Gate the pages**

In `web/src/App.tsx`, add `import Groups from './pages/Groups';` after the `GroupCalendars` import; replace `  const activeTab = chosenTab ?? (user.role === 'admin' ? 'dashboard' : 'calendar');` with

```tsx
  const isAdmin = user.role === 'admin';
  const activeTab = chosenTab ?? (isAdmin ? 'dashboard' : 'calendar');
```

and replace the seven page lines inside `<main className="app-main">` with:

```tsx
        {activeTab === 'calendar' && !isAdmin && <Suspense fallback={<p>Loading calendar…</p>}><CalendarPage /></Suspense>}
        {activeTab === 'devices' && !isAdmin && <AppPasswords username={user.username} />}
        {/* Admin pages: the API refuses everyday users anyway; never render them either. */}
        {isAdmin && activeTab === 'dashboard' && <Dashboard settings={settings} user={user} onNavigate={(tab) => setActiveTab(tab)} />}
        {isAdmin && activeTab === 'groups' && <Groups />}
        {isAdmin && activeTab === 'group-calendars' && <GroupCalendars />}
        {isAdmin && activeTab === 'scim' && <SCIMAdmin />}
        {isAdmin && activeTab === 'backup' && <Backup />}
        {isAdmin && activeTab === 'settings' && <Settings settings={settings} />}
```

- [ ] **Step 5: Dashboard card**

In `web/src/pages/Dashboard.tsx`, add `UsersRound` to the `lucide-react` import and make this the first entry of `cards`:

```tsx
    {
      title: 'Groups',
      desc: 'Local groups for group calendars, beside the groups your identity provider syncs.',
      status: 'Local and synced',
      statusType: 'accent',
      icon: UsersRound,
      action: () => onNavigate('groups'),
      actionLabel: 'Manage groups',
    },
```

- [ ] **Step 6: Run the web suite and build**

Run: `cd web && npm test && npm run build`
Expected: every test file passes; the build rewrites `web/dist`.

- [ ] **Step 7: DOX and README**

In `web/AGENTS.md` Local Contracts add:

```markdown
- `App.tsx` renders the admin pages (`dashboard`, `groups`, `group-calendars`, `scim`, `backup`, `settings`) only for `role === 'admin'`, and `calendar` and `devices` only for everyday users; `AppHeader` offers the same split.
```

In `README.md`, insert after the `## Administrators and group calendars` section:

```markdown
## Groups

**Groups** lists every group: local ones you create here and, when SCIM is connected, the ones your identity provider syncs (badged "Managed by SCIM"; change those in the identity provider). Create a group, rename it, search for people to add, remove members, or delete it. Only active everyday people can be members: administrators never see calendars. Adding or removing someone reaches their phone at its next sync. Deleting a group removes its members and its access to group calendars (the confirm says how many); the calendars stay. Deleting needs a sign-in from the last 10 minutes.

Names are unique ignoring case. If your identity provider sends a group whose name a local group already has, KyCalendar refuses it and the local group shows a warning: rename the local group and the next sync creates the synced one.
```

- [ ] **Step 8: Commit**

```bash
git add web/src/App.tsx web/src/App.test.tsx web/src/components/AppHeader.tsx web/src/pages/Dashboard.tsx web/AGENTS.md README.md web/dist
git commit -m "feat(web): admin-only pages, Groups navigation and card" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 12: Chromium: a group calendar reaches a member

**Files:**
- Create: `web/browser/groups.spec.mjs`
- Modify: `web/browser/AGENTS.md`, root `AGENTS.md` (Verification, Chromium bullet)

**Interfaces:**
- Consumes: `server.mjs` (admin `admin`, everyday `walter`), `setup.mjs` passwords `BrowserUpdated456!` / `WalterUpdated456!`; the Groups and Group calendars screens.
- Produces: one spec, run in all four projects; names carry the project and time, so projects never collide.

- [ ] **Step 1: Write the spec**

Create `web/browser/groups.spec.mjs`:

```js
import { test, expect } from '@playwright/test';

async function signIn(page, username, password) {
  await page.goto('/');
  await page.getByPlaceholder('admin', { exact: true }).fill(username);
  await page.locator('input[type=password]').fill(password);
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
}

test('groups: an admin puts walter in a group and walter sees its calendar', async ({ page, browser }, testInfo) => {
  const suffix = `${testInfo.project.name} ${Date.now()}`;
  const group = `Crew ${suffix}`;
  const calendar = `Rota ${suffix}`;

  await signIn(page, 'admin', 'BrowserUpdated456!');
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await nav.getByRole('button', { name: 'Groups', exact: true }).click();
  await page.getByLabel('Group name').fill(group);
  await page.getByRole('button', { name: 'Create group' }).click();

  const card = page.getByRole('article', { name: group });
  await card.getByRole('button', { name: 'Members' }).click();
  await card.getByLabel('Find a person').fill('walter');
  await card.getByRole('button', { name: 'Search' }).click();
  await card.getByRole('button', { name: 'Add walter' }).click();
  await expect(card.getByRole('button', { name: 'Remove walter' })).toBeVisible();

  await nav.getByRole('button', { name: 'Group calendars', exact: true }).click();
  await page.getByLabel('Calendar name').fill(calendar);
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  const calCard = page.getByRole('article', { name: calendar });
  await calCard.getByLabel('Group', { exact: true }).selectOption({ label: group });
  await calCard.getByRole('button', { name: 'Add group' }).click();
  await expect(calCard.getByRole('button', { name: `Remove ${group}` })).toBeVisible();

  // walter signs in in a separate cookie jar and finds the calendar beside his own.
  const context = await browser.newContext();
  const walter = await context.newPage();
  await signIn(walter, 'walter', 'WalterUpdated456!');
  await expect(walter.getByRole('complementary', { name: 'Calendars' }).getByLabel(`Show ${calendar}`)).toBeVisible();
  await context.close();
});
```

- [ ] **Step 2: Run it against the built server**

Run: `go build -o .browser/server ./cmd/server && cd web && npx playwright install chromium && npx playwright test browser/groups.spec.mjs`
Expected: `4 passed`.

- [ ] **Step 3: DOX**

In `web/browser/AGENTS.md` Local Contracts add: ``- `groups.spec.mjs` signs in as `admin`, creates a group, adds `walter` by search, grants the group a new group calendar, then signs `walter` in through a separate browser context and finds the calendar in his sidebar.`` In the root `AGENTS.md` Verification list, change the Chromium bullet to end: `…responsive layout, keyboard navigation and the admin flows (a local group's calendar reaching its member); the browser job gates publishing.`

- [ ] **Step 4: Commit**

```bash
git add web/browser/groups.spec.mjs web/browser/AGENTS.md AGENTS.md
git commit -m "test(browser): a local group's calendar reaches its member" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 13: PR 1 final verification and pull request

Run each command alone and let it finish before starting the next.

- [ ] **Step 1:** `make tidy-check` — Expected: no diff.
- [ ] **Step 2:** `make lint` — Expected: no gofmt output, vet clean.
- [ ] **Step 3:** `go test -race -count=1 -p 1 ./internal/store/...` — Expected: `ok` for both packages.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/api/...` — Expected: `ok`.
- [ ] **Step 5:** `go test -race -count=1 -p 1 ./cmd/... ./internal/access/... ./internal/apppass/... ./internal/auth/... ./internal/backup/... ./internal/calendar/... ./internal/config/... ./internal/crypto/... ./internal/davbackend/... ./internal/scim/... ./internal/sso/... ./internal/testdb/... ./web/...` — Expected: every package `ok` or `no test files`.
- [ ] **Step 6:** `make test-fork` — Expected: `ok`.
- [ ] **Step 7:** `cd web && npm ci && npm test` — Expected: every test file passes.
- [ ] **Step 8:** `cd web && npm run build && cd .. && git status --porcelain web/dist` — Expected: no output. If it lists files: `git add web/dist && git commit -m "build(web): refresh dist" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"`.
- [ ] **Step 9:** `make build && KY_SMOKE_PORT=28931 ./scripts/smoke-test.sh` — Expected: `smoke test: all checks passed`.
- [ ] **Step 10:** `go build -o .browser/server ./cmd/server && cd web && npx playwright install chromium && npm run test:browser` — Expected: every spec passes in all four projects.
- [ ] **Step 11 (when a PostgreSQL is reachable):** `make test-postgres` — Expected: `ok` everywhere; migration 10 and the `group-names` lock run on Postgres here.
- [ ] **Step 12: Open the PR**

```bash
git push -u origin feat/standalone-groups
gh pr create --base main --title "Standalone administration 1/3: local groups" --body "$(cat <<'BODY'
Local groups for group calendars, managed from KyCalendar's own Groups screen.

- Migration 10: `groups.source` (`local`/`scim`); existing rows backfilled to `scim`.
- Group names unique ignoring case; SCIM never sees or writes a local group, and a name clash is flagged on screen.
- Admin routes: list (source and counts), detail, create, rename, members, delete (step-up); people list with search.
- One `requireStepUp` helper replaces the two copies; the dead generic-OIDC client is gone.
- Admin pages render for administrators only, on the client too.

Spec: docs/superpowers/specs/2026-10-07-kycalendar-standalone-admin-design.md

🤖 Generated with [Claude Code](https://claude.com/claude-code)
BODY
)"
```

---

# PR 2 — People

Branch `feat/standalone-people` from `main` after PR 1 merges. Ends with Task 23.

## Task 14: `RenameUser` records the acting admin

**Files:**
- Modify: `internal/store/store.go:53-55`, `internal/store/sqlstore.go` (`RenameUser`, ~:472-498)
- Modify: `cmd/server/renameuser.go:65`, `cmd/server/renameuser_test.go:37`
- Create: `internal/store/rename_test.go`
- Modify: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: the existing rename transaction.
- Produces: `UserStore.RenameUser(ctx context.Context, actor, userID, newName string) error`; the `user.renamed` row's `user_id` is `actor`. The CLI passes `"system"`.

- [ ] **Step 1: Branch**

```bash
git switch main && git pull --ff-only && git switch -c feat/standalone-people
```

- [ ] **Step 2: Write the failing test**

Create `internal/store/rename_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// The People screen renames as an admin; the audit row names that admin, not "system".
func TestRenameUserAuditsTheActor(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_l", Username: "lee", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().RenameUser(ctx, "usr_admin", "usr_l", "lee2"); err != nil {
		t.Fatal(err)
	}
	recs, _, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 || recs[0].Action != "user.renamed" || recs[0].UserID != "usr_admin" || recs[0].Details != "from=lee to=lee2" {
		t.Fatalf("audit %+v, want user.renamed by usr_admin", recs)
	}
}
```

In `cmd/server/renameuser_test.go`, change the audit check to also require the CLI actor:

```go
	if len(recs) == 0 || recs[0].Action != "user.renamed" || recs[0].Resource != before.ID || recs[0].UserID != "system" {
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test -count=1 ./internal/store/ -run TestRenameUserAuditsTheActor`
Expected: build failure, `too many arguments in call to st.Users().RenameUser`.

- [ ] **Step 4: Implement**

In `internal/store/store.go`, replace the `RenameUser` comment and method with:

```go
	// RenameUser changes only a local account's username and audits it as actor in one
	// transaction. Not local or missing: ErrNotFound; name taken: ErrAlreadyExists.
	RenameUser(ctx context.Context, actor, userID, newName string) error
```

In `internal/store/sqlstore.go`, replace `RenameUser` with:

```go
func (u *userStore) RenameUser(ctx context.Context, actor, userID, newName string) error {
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from string
	err = tx.QueryRowContext(ctx, u.store.rebind(`SELECT username FROM users WHERE id = ? AND sso_provider = 'local'`), userID).Scan(&from)
	if errorsIs(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, u.store.rebind(`UPDATE users SET username = ?, updated_at = ? WHERE id = ?`), newName, now, userID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate key") {
			return ErrAlreadyExists
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, u.store.rebind(`INSERT INTO audit_records (user_id, action, resource, details, ip_address, created_at) VALUES (?, ?, ?, ?, ?, ?)`),
		actor, "user.renamed", userID, "from="+from+" to="+newName, "", now); err != nil {
		return err
	}
	return tx.Commit()
}
```

In `cmd/server/renameuser.go`, `st.Users().RenameUser(ctx, u.ID, to)` becomes `st.Users().RenameUser(ctx, "system", u.ID, to)`.

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/store/ -run TestRenameUser && go test -count=1 ./cmd/server/ -run TestRenameUser`
Expected: `ok` twice.

- [ ] **Step 6: DOX**

In `internal/store/AGENTS.md`, change the `RenameUser` bullet's ending to: ``…and audits `user.renamed` with the given actor (`system` from the CLI, the acting admin from the People screen) in the same transaction.``

- [ ] **Step 7: Commit**

```bash
git add internal/store/store.go internal/store/sqlstore.go internal/store/rename_test.go internal/store/AGENTS.md cmd/server/renameuser.go cmd/server/renameuser_test.go
git commit -m "feat(store): RenameUser audits the acting account" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 15: Store: profile, role and status changes keep a local admin

**Files:**
- Modify: `internal/store/store.go` (errors; `UserStore`), `internal/store/sqlstore.go` (`revokePasswordGrants` ~:524; new methods after it)
- Create: `internal/store/access_test.go`
- Modify: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `lockedTx` (Task 2), `errorsIs`, `SQLStore.rebind`.
- Produces: `store.ErrLastAdmin`; `UpdateProfile(ctx, userID, displayName, email string) error`; `SetRole(ctx, userID, role string) error`; `SetStatus(ctx, userID, status string) error` (local only; one transaction that revokes sessions, MFA challenges, device pairings and app passwords; `ErrLastAdmin` when no other active local admin would remain); `var grantTables`; `func (u *userStore) revokeGrants(ctx, tx, userID) error`. Test helpers `seedUsers(t, st, users...)`, `seedSession(t, st, u)` (session `tok_<id>` plus app password `ap_<id>`).

- [ ] **Step 1: Write the failing tests**

Create `internal/store/access_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func seedUsers(t *testing.T, st store.Store, users ...*store.User) {
	t.Helper()
	for _, u := range users {
		if err := st.Users().CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
}

func seedSession(t *testing.T, st store.Store, u *store.User) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.Sessions().CreateSession(context.Background(), &store.Session{TokenHash: "tok_" + u.ID, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(context.Background(), &store.AppPassword{ID: "ap_" + u.ID, UserID: u.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
}

func TestSetRoleAndStatusRevokeAndKeepALocalAdmin(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	ann := &store.User{ID: "usr_ann", Username: "ann", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, root, ann,
		&store.User{ID: "usr_sso", Username: "sso", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"},
		&store.User{ID: "usr_off", Username: "off", Role: "admin", Status: "inactive", SSOProvider: "local"},
	)
	seedSession(t, st, ann)

	if err := st.Users().SetRole(ctx, ann.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions().GetSession(ctx, "tok_"+ann.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("promotion kept the session: %v", err)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, ann.ID); len(list) != 0 {
		t.Errorf("promotion kept %d app passwords", len(list))
	}
	if err := st.Users().SetRole(ctx, root.ID, "user"); err != nil {
		t.Fatalf("demote one of two local admins: %v", err)
	}
	// ann is now the only active local admin: an SSO admin and an inactive local admin do not count.
	if err := st.Users().SetStatus(ctx, ann.ID, "inactive"); !errors.Is(err, store.ErrLastAdmin) {
		t.Errorf("disable the last local admin: %v, want ErrLastAdmin", err)
	}
	if err := st.Users().SetRole(ctx, ann.ID, "user"); !errors.Is(err, store.ErrLastAdmin) {
		t.Errorf("demote the last local admin: %v, want ErrLastAdmin", err)
	}
	if u, _ := st.Users().GetUserByID(ctx, ann.ID); u.Role != "admin" || u.Status != "active" {
		t.Errorf("a refused change was stored: %+v", u)
	}
	if err := st.Users().SetStatus(ctx, "usr_sso", "inactive"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SSO account: %v, want ErrNotFound", err)
	}
	if err := st.Users().SetStatus(ctx, root.ID, "inactive"); err != nil {
		t.Fatalf("disable an everyday account: %v", err)
	}
	if err := st.Users().SetStatus(ctx, root.ID, "active"); err != nil {
		t.Fatalf("enable it again: %v", err)
	}
}

// Two admins demoting each other at once: exactly one may succeed.
func TestConcurrentDemotionKeepsOneLocalAdmin(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seedUsers(t, st,
		&store.User{ID: "usr_a", Username: "a", Role: "admin", Status: "active", SSOProvider: "local"},
		&store.User{ID: "usr_b", Username: "b", Role: "admin", Status: "active", SSOProvider: "local"},
	)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, id := range []string{"usr_a", "usr_b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = st.Users().SetRole(ctx, id, "user")
		}()
	}
	close(start)
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("want exactly one demotion, got %v and %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil && !errors.Is(err, store.ErrLastAdmin) {
			t.Fatalf("refusal: %v, want ErrLastAdmin", err)
		}
	}
}

func TestUpdateProfileIsLocalOnly(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seedUsers(t, st,
		&store.User{ID: "usr_l", Username: "l", Role: "user", Status: "active", SSOProvider: "local"},
		&store.User{ID: "usr_s", Username: "s", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "x"},
	)
	if err := st.Users().UpdateProfile(ctx, "usr_l", "Ella L", "l@example.com"); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_l"); u.DisplayName != "Ella L" || u.Email != "l@example.com" {
		t.Fatalf("profile not stored: %+v", u)
	}
	if err := st.Users().UpdateProfile(ctx, "usr_s", "Taken", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SCIM account: %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/store/ -run 'TestSetRole|TestConcurrentDemotion|TestUpdateProfile'`
Expected: build failure, `st.Users().SetRole undefined` and `undefined: store.ErrLastAdmin`.

- [ ] **Step 3: Interface**

In `internal/store/store.go`, add after `ErrQuotaExceeded` in the error block:

```go
	// ErrLastAdmin refuses a change that would leave no active local administrator.
	ErrLastAdmin = errors.New("last active local administrator")
```

and after the `RenameUser` method in `UserStore`:

```go
	// UpdateProfile sets a local account's display name and email and nothing else. Not local
	// or missing: ErrNotFound.
	UpdateProfile(ctx context.Context, userID, displayName, email string) error
	// SetRole and SetStatus change a local account's role or status and, in the same
	// transaction, delete its sessions, MFA challenges, device pairings and app passwords. A
	// change that would leave no active local administrator is ErrLastAdmin. Not local or
	// missing: ErrNotFound.
	SetRole(ctx context.Context, userID, role string) error
	SetStatus(ctx context.Context, userID, status string) error
```

- [ ] **Step 4: Implement**

In `internal/store/sqlstore.go`, replace `revokePasswordGrants` with the following (it now shares the table list), and add the new methods after it:

```go
// grantTables hold everything a credential or a role has handed out.
var grantTables = []string{"sessions", "mfa_challenges", "device_pairings", "app_passwords"}

// revokeGrants deletes every session, MFA challenge, device pairing and app password of userID.
func (u *userStore) revokeGrants(ctx context.Context, tx *sql.Tx, userID string) error {
	for _, table := range grantTables {
		if _, err := tx.ExecContext(ctx, u.store.rebind("DELETE FROM "+table+" WHERE user_id = ?"), userID); err != nil {
			return err
		}
	}
	return nil
}

func (u *userStore) revokePasswordGrants(ctx context.Context, tx *sql.Tx, userID, details, ip string, now time.Time) error {
	if err := u.revokeGrants(ctx, tx, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, u.store.rebind(`INSERT INTO audit_records (user_id, action, resource, details, ip_address, created_at) VALUES (?, ?, ?, ?, ?, ?)`), userID, "auth.password_changed", "user", details, ip, now)
	return err
}

func (u *userStore) UpdateProfile(ctx context.Context, userID, displayName, email string) error {
	res, err := u.store.db.ExecContext(ctx, u.store.rebind(`UPDATE users SET display_name = ?, email = ?, updated_at = ? WHERE id = ? AND sso_provider = 'local'`), displayName, email, time.Now().UTC(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (u *userStore) SetRole(ctx context.Context, userID, role string) error {
	return u.changeAccess(ctx, userID, func(_, status string) (string, string) { return role, status })
}

func (u *userStore) SetStatus(ctx context.Context, userID, status string) error {
	return u.changeAccess(ctx, userID, func(role, _ string) (string, string) { return role, status })
}

// changeAccess applies change to a local account's role and status under the local-admins lock,
// so two admins demoting each other cannot both pass the last-admin check.
func (u *userStore) changeAccess(ctx context.Context, userID string, change func(role, status string) (string, string)) error {
	tx, err := u.store.lockedTx(ctx, "local-admins")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role, status string
	err = tx.QueryRowContext(ctx, u.store.rebind(`SELECT role, status FROM users WHERE id = ? AND sso_provider = 'local'`), userID).Scan(&role, &status)
	if errorsIs(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	wasAdmin := role == "admin" && status == "active"
	role, status = change(role, status)
	if wasAdmin && (role != "admin" || status != "active") {
		var others int
		if err := tx.QueryRowContext(ctx, u.store.rebind(`SELECT COUNT(1) FROM users WHERE role = 'admin' AND status = 'active' AND sso_provider = 'local' AND id <> ?`), userID).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	if err := u.revokeGrants(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, u.store.rebind(`UPDATE users SET role = ?, status = ?, updated_at = ? WHERE id = ?`), role, status, time.Now().UTC(), userID); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 5: Run the store tests**

Run: `go test -count=1 ./internal/store/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/store`

- [ ] **Step 6: DOX**

In `internal/store/AGENTS.md` add:

```markdown
- `SetRole` and `SetStatus` change a local account inside `lockedTx("local-admins")`, deleting its sessions, MFA challenges, device pairings and app passwords in the same transaction, and refuse (`ErrLastAdmin`) a change that leaves no active local administrator; SSO administrators do not count, because the local admin is the way back in when the IdP is gone. `UpdateProfile` writes only a local account's display name and email.
```

- [ ] **Step 7: Commit**

```bash
git add internal/store/store.go internal/store/sqlstore.go internal/store/access_test.go internal/store/AGENTS.md
git commit -m "feat(store): role, status and profile changes that keep a local admin" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 16: Record the last sign-in

`users.last_login_at` exists but nothing writes it; the People list shows it.

**Files:**
- Modify: `internal/store/sqlstore.go` (`sessionStore.CreateSession`, ~:438-443)
- Create: `internal/store/last_login_test.go`
- Modify: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `withPassword`, `seedUsers`, `seedSession` (Task 15).
- Produces: every session issued (password, MFA or SSO) sets `users.last_login_at = sess.CreatedAt` in the same transaction.

- [ ] **Step 1: Write the failing test**

Create `internal/store/last_login_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestCreateSessionRecordsLastLogin(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := &store.User{ID: "usr_l", Username: "l", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, u)
	seedSession(t, st, u)
	got, _ := st.Users().GetUserByID(ctx, u.ID)
	if got.LastLoginAt == nil || time.Since(*got.LastLoginAt) > time.Minute {
		t.Fatalf("last_login_at = %v, want the session time", got.LastLoginAt)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/store/ -run TestCreateSessionRecordsLastLogin`
Expected: FAIL, `last_login_at = <nil>, want the session time`.

- [ ] **Step 3: Implement**

Replace `CreateSession` in `internal/store/sqlstore.go` with:

```go
// CreateSession also records the sign-in as the user's last_login_at.
func (s *sessionStore) CreateSession(ctx context.Context, sess *Session, expectedPasswordHash string) error {
	return s.store.withPassword(ctx, sess.UserID, expectedPasswordHash, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.store.rebind(`INSERT INTO sessions (token_hash, user_id, user_agent, ip_address, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`), sess.TokenHash, sess.UserID, sess.UserAgent, sess.IPAddress, sess.CreatedAt, sess.ExpiresAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, s.store.rebind(`UPDATE users SET last_login_at = ? WHERE id = ?`), sess.CreatedAt, sess.UserID)
		return err
	})
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/store/ && go test -count=1 ./internal/auth/`
Expected: `ok` twice.

- [ ] **Step 5: DOX**

In `internal/store/AGENTS.md` add: ``- `CreateSession` stamps `users.last_login_at` with the session's `CreatedAt` in the same transaction; the People list shows it.``

- [ ] **Step 6: Commit**

```bash
git add internal/store/sqlstore.go internal/store/last_login_test.go internal/store/AGENTS.md
git commit -m "feat(store): record each account's last sign-in" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 17: Create a person and reset a password

**Files:**
- Modify: `internal/api/admin_users.go` (imports; append), `internal/api/server.go`, `internal/api/authz_matrix_test.go` (`newWorld`, `apiRows`)
- Create: `internal/api/admin_users_create_test.go`

**Interfaces:**
- Consumes: `crypto.RandomBase64URL`, `crypto.RandomHex`, `auth.ValidatePassword`/`ValidateUsername`/`ValidateEmail`, `password.Hash`, `UserStore.ResetPassword` (operator reset: revokes sessions, MFA challenges, device pairings and app passwords), `requireStepUp`, `cleanName`, `writeManaged`, `tracked`.
- Produces: `func newTemporaryPassword() (plain, hash string, err error)` (20 characters, 120 random bits); `func (s *Server) adminUser(w, r, local bool) *store.User`; `func (s *Server) usernameFree(w, r, name, self string) bool` (case-insensitive, any provider); `func (s *Server) writeOnce(w, status, body)` (`Cache-Control: no-store`); `POST /api/admin/users` → 201 `{"user":userView,"temporary_password":"…"}` (step-up only for `role: admin`); `POST /api/admin/users/{id}/reset-password` → 200 `{"temporary_password":"…"}` (step-up). Test helper `passwordLogin(t, srv, username, pw)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/api/admin_users_create_test.go`:

```go
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/api"
)

func passwordLogin(t *testing.T, srv *api.Server, username, pw string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": pw})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestAdminCreateUserReturnsThePasswordOnce(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")

	w := call(t, srv, "POST", "/api/admin/users", `{"username":"ann","display_name":"Ann Lee","email":"ann@example.com","role":"user"}`, admin)
	if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	var created struct {
		User struct {
			ID     string `json:"id"`
			Source string `json:"source"`
		} `json:"user"`
		TemporaryPassword string `json:"temporary_password"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	temp := created.TemporaryPassword
	if len(temp) < 12 || created.User.Source != "local" {
		t.Fatalf("created %+v", created)
	}
	u, err := st.Users().GetUserByID(ctx, created.User.ID)
	if err != nil || !u.MustChangePassword || u.Role != "user" || strings.Contains(u.PasswordHash, temp) {
		t.Fatalf("stored user %+v %v", u, err)
	}
	if ok, err := password.Verify(temp, u.PasswordHash); !ok || err != nil {
		t.Fatalf("the stored hash does not verify the returned password: %v", err)
	}
	if bytes.Contains(call(t, srv, "GET", "/api/admin/users", "", admin).Body.Bytes(), []byte(temp)) {
		t.Fatal("the temporary password is listed again")
	}
	for _, d := range auditDetails(t, st, "admin.user_create") {
		if strings.Contains(d, temp) {
			t.Fatal("the temporary password reached the audit log")
		}
	}
	if w := passwordLogin(t, srv, "ann", temp); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"must_change_password":true`)) {
		t.Fatalf("first sign-in: %d %s", w.Code, w.Body.String())
	}

	if w := call(t, srv, "POST", "/api/admin/users", `{"username":"ANN"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Errorf("case twin: %d %s, want 409 name_taken", w.Code, w.Body.String())
	}
	for _, bad := range []string{`{"username":"a b"}`, `{"username":"zed","email":"nope"}`, `{"username":"zed","role":"owner"}`, `{"username":"zed","display_name":"x\u0000y"}`} {
		if w := call(t, srv, "POST", "/api/admin/users", bad, admin); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}

	restore := api.SetStepUpWindowForTest(0)
	defer restore()
	if w := call(t, srv, "POST", "/api/admin/users", `{"username":"boss","role":"admin"}`, admin); w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
		t.Errorf("admin creation on a stale session: %d %s, want 403 reauth_required", w.Code, w.Body.String())
	}
	if w := call(t, srv, "POST", "/api/admin/users", `{"username":"cal"}`, admin); w.Code != http.StatusCreated {
		t.Errorf("everyday creation needs no step-up: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminResetPassword(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	bob := loginAs(t, srv, st, "bob", "user")
	w := call(t, srv, "POST", "/api/admin/users/usr_bob/reset-password", "", admin)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w := call(t, srv, "GET", "/api/auth/me", "", bob); bytes.Contains(w.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Error("bob's session survived the reset")
	}
	if w := passwordLogin(t, srv, "bob", body.TemporaryPassword); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"must_change_password":true`)) {
		t.Fatalf("sign-in with the temporary password: %d %s", w.Code, w.Body.String())
	}
	if d := auditDetails(t, st, "admin.user_reset_password"); len(d) != 1 || strings.Contains(d[0], body.TemporaryPassword) {
		t.Errorf("reset audit: %v", d)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/api/ -run 'TestAdminCreateUser|TestAdminResetPassword'`
Expected: FAIL, `create: 200 "" …` (no route yet; the SPA answers the POST).

- [ ] **Step 3: Implement**

In `internal/api/admin_users.go`, replace the import block with:

```go
import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)
```

and append:

```go
// newTemporaryPassword returns a one-time password (120 random bits, 20 characters) and its
// hash. The plain text goes to the admin once, in the response; only the hash is stored.
func newTemporaryPassword() (plain, hash string, err error) {
	plain = crypto.RandomBase64URL(15)
	if err := auth.ValidatePassword(plain); err != nil {
		return "", "", err
	}
	hash, err = password.Hash(plain)
	return plain, hash, err
}

// adminUser loads the {id} person: 404 when missing; with local set, 409 managed_externally for
// an account an identity provider owns.
func (s *Server) adminUser(w http.ResponseWriter, r *http.Request, local bool) *store.User {
	u, err := s.store.Users().GetUserByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such person")
		return nil
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the person")
		return nil
	}
	if local && u.SSOProvider != "local" {
		s.writeManaged(w, "person")
		return nil
	}
	return u
}

// usernameFree is false, with 409 name_taken written, when another account holds name in any
// case: a case twin would split one person's sign-in across two rows.
func (s *Server) usernameFree(w http.ResponseWriter, r *http.Request, name, self string) bool {
	other, err := s.store.Users().GetUserByUsername(r.Context(), name)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && other.ID == self):
		return true
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to check the username")
	default:
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Another account already uses this username", "code": "name_taken"})
	}
	return false
}

// writeOnce sends a response carrying a one-time password; nothing may cache it.
func (s *Server) writeOnce(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, status, body)
}

// handleCreateUser adds a local person with a server-generated temporary password, returned
// once. Creating an administrator is a step-up action.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	body.Role = cmp.Or(body.Role, "user")
	if body.Role != "user" && body.Role != "admin" {
		s.writeError(w, http.StatusBadRequest, "Role must be user or admin")
		return
	}
	if body.Role == "admin" && !s.requireStepUp(w, r, "add an administrator") {
		return
	}
	body.Username, body.Email = strings.TrimSpace(body.Username), strings.TrimSpace(body.Email)
	if err := auth.ValidateUsername(body.Username); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidateEmail(body.Email); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	display, ok := cleanName(cmp.Or(strings.TrimSpace(body.DisplayName), body.Username))
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Display name must be 1-255 characters with no control characters")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if !s.usernameFree(w, r, body.Username, "") {
		return
	}
	plain, hash, err := newTemporaryPassword()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate a password")
		return
	}
	u := &store.User{ID: "usr_" + crypto.RandomHex(12), Username: body.Username, DisplayName: display, Email: body.Email,
		PasswordHash: hash, Role: body.Role, Status: "active", SSOProvider: "local", MustChangePassword: true}
	if err := s.store.Users().CreateUser(ctx, u); errors.Is(err, store.ErrAlreadyExists) {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Another account already uses this username", "code": "name_taken"})
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to create the person")
		return
	}
	s.auditAction(ctx, r, "admin.user_create", u.ID, "role="+u.Role)
	s.writeOnce(w, http.StatusCreated, map[string]any{"user": userViewOf(u), "temporary_password": plain})
}

// handleResetUserPassword sets a new temporary password on a local person, returned once; the
// store's operator reset revokes their sessions, MFA challenges and app passwords.
func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "reset a password") {
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	plain, hash, err := newTemporaryPassword()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate a password")
		return
	}
	if err := s.store.Users().ResetPassword(r.Context(), u.ID, hash); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to reset the password")
		return
	}
	s.auditAction(r.Context(), r, "admin.user_reset_password", u.ID, "")
	s.writeOnce(w, http.StatusOK, map[string]string{"temporary_password": plain})
}
```

In `routes()`, after the `GET /api/admin/users` line:

```go
	s.handle("POST /api/admin/users", s.tracked(s.requireAdmin(s.handleCreateUser)))
	s.handle("POST /api/admin/users/{id}/reset-password", s.tracked(s.requireAdmin(s.handleResetUserPassword)))
```

- [ ] **Step 4: Matrix fixture and rows**

In `newWorld`, before the `// grp_extra takes grants;` comment, add:

```go
	// victim is the local account the People rows reset, promote, disable and enable; no row
	// signs in as it.
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_victim", Username: "victim", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
```

and to `apiRows` (then `gofmt -w internal/api/authz_matrix_test.go`):

```go
		"POST /api/admin/users":                     {method: "POST", path: "/api/admin/users", body: `{"username":"matrix-new","role":"user"}`, want: adminOnly},
		"POST /api/admin/users/{id}/reset-password": {method: "POST", path: "/api/admin/users/usr_victim/reset-password", want: adminOnly},
```

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/api/ -run 'TestAdminCreateUser|TestAdminResetPassword|TestEveryRouteIsInTheMatrix|TestAPIAuthorizationMatrix'`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin_users.go internal/api/admin_users_create_test.go internal/api/server.go internal/api/authz_matrix_test.go
git commit -m "feat(api): add people and reset passwords with one-time passwords" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 18: Edit a person

**Files:**
- Modify: `internal/api/admin_users.go` (append), `internal/api/server.go`, `internal/api/authz_matrix_test.go`, `internal/api/sso_handlers.go:152` (the 409 message now points at the People screen)
- Create: `internal/api/admin_users_update_test.go`

**Interfaces:**
- Consumes: `adminUser`, `usernameFree`, `cleanName`, `RenameUser(ctx, actor, …)`, `UpdateProfile`.
- Produces: `PATCH /api/admin/users/{id}` with optional `username`, `display_name`, `email`; every field validated before any write; audit `admin.user_update` with `fields=username,profile`; `func (s *Server) writeUser(w, r, id string)`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/admin_users_update_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"testing"
)

func TestAdminUpdateUser(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	_ = loginAs(t, srv, st, "alice", "user")
	_ = loginAs(t, srv, st, "bob", "user")

	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"username":"alice2","display_name":"Alice L","email":"a@example.com"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	u, _ := st.Users().GetUserByID(ctx, "usr_alice")
	if u.Username != "alice2" || u.DisplayName != "Alice L" || u.Email != "a@example.com" {
		t.Fatalf("after update: %+v", u)
	}
	recs, _, _ := st.Audit().ListAuditRecords(ctx, 0, 50)
	renamedBy := ""
	for _, r := range recs {
		if r.Action == "user.renamed" {
			renamedBy = r.UserID
		}
	}
	if renamedBy != "usr_root" {
		t.Errorf("user.renamed actor = %q, want the acting admin", renamedBy)
	}
	if d := auditDetails(t, st, "admin.user_update"); len(d) != 1 || d[0] != "fields=username,profile" {
		t.Errorf("update audit: %v", d)
	}

	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"username":"BOB"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Errorf("rename onto a case twin: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"username":"Alice2"}`, admin); w.Code != http.StatusOK {
		t.Errorf("a case-only rename of the same account: %d %s", w.Code, w.Body.String())
	}
	// Nothing is written when any field is invalid.
	if w := call(t, srv, "PATCH", "/api/admin/users/usr_alice", `{"username":"alice3","email":"nope"}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("bad email: %d", w.Code)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_alice"); u.Username != "Alice2" {
		t.Errorf("a refused update renamed the account to %q", u.Username)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/api/ -run TestAdminUpdateUser`
Expected: FAIL, `after update: &{… Username:alice …}` (the SPA answered the PATCH with 200; nothing changed).

- [ ] **Step 3: Implement**

Append to `internal/api/admin_users.go`:

```go
// handleUpdateUser changes a local person's username, display name or email. Every field is
// checked before any is written.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    *string `json:"username"`
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	username, display, email := u.Username, u.DisplayName, u.Email
	if body.Username != nil {
		username = strings.TrimSpace(*body.Username)
		if err := auth.ValidateUsername(username); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.DisplayName != nil {
		var ok bool
		if display, ok = cleanName(*body.DisplayName); !ok {
			s.writeError(w, http.StatusBadRequest, "Display name must be 1-255 characters with no control characters")
			return
		}
	}
	if body.Email != nil {
		email = strings.TrimSpace(*body.Email)
		if err := auth.ValidateEmail(email); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var changed []string
	if username != u.Username {
		if !s.usernameFree(w, r, username, u.ID) {
			return
		}
		actor := sessionUser(r.Context()).ID
		if err := s.store.Users().RenameUser(r.Context(), actor, u.ID, username); errors.Is(err, store.ErrAlreadyExists) {
			s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Another account already uses this username", "code": "name_taken"})
			return
		} else if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to rename the person")
			return
		}
		changed = append(changed, "username")
	}
	if display != u.DisplayName || email != u.Email {
		if err := s.store.Users().UpdateProfile(r.Context(), u.ID, display, email); err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to update the person")
			return
		}
		changed = append(changed, "profile")
	}
	if len(changed) > 0 {
		s.auditAction(r.Context(), r, "admin.user_update", u.ID, "fields="+strings.Join(changed, ","))
	}
	s.writeUser(w, r, u.ID)
}

func (s *Server) writeUser(w http.ResponseWriter, r *http.Request, id string) {
	u, err := s.store.Users().GetUserByID(r.Context(), id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the person")
		return
	}
	s.writeJSON(w, http.StatusOK, userViewOf(u))
}
```

In `routes()`:

```go
	s.handle("PATCH /api/admin/users/{id}", s.requireAdmin(s.handleUpdateUser))
```

In `apiRows` (then `gofmt -w internal/api/authz_matrix_test.go`):

```go
		"PATCH /api/admin/users/{id}": {method: "PATCH", path: "/api/admin/users/usr_victim", body: `{"display_name":"Victim"}`, want: adminOnly},
```

In `internal/api/sso_handlers.go`, the username-clash message becomes `"Another KyCalendar account already uses this username; an administrator must rename it on the People screen"`.

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/api/ -run 'TestAdminUpdateUser|TestUpsertSSOUser|TestEveryRouteIsInTheMatrix|TestAPIAuthorizationMatrix'`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin_users.go internal/api/admin_users_update_test.go internal/api/server.go internal/api/authz_matrix_test.go internal/api/sso_handlers.go
git commit -m "feat(api): edit a local person's username and profile" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 19: Role, disable and enable

**Files:**
- Modify: `internal/api/admin_users.go` (append), `internal/api/server.go`, `internal/api/authz_matrix_test.go`
- Create: `internal/api/admin_users_access_test.go`
- Modify: `internal/api/AGENTS.md`, `AGENTS.md` (root)

**Interfaces:**
- Consumes: `SetRole`, `SetStatus`, `ErrLastAdmin`, `requireStepUp`, `adminUser`, `writeUser`, `sessionUser`.
- Produces: `POST /api/admin/users/{id}/role` `{role: user|admin}` (step-up), `POST …/disable` (step-up), `POST …/enable`; 409 `self` (own demotion or disable), 409 `last_admin`; no-ops write no audit row; `func (s *Server) writeAccessError(w, err)`, `func (s *Server) writeSelf(w, what)`, `func (s *Server) setUserStatus(w, r, status, action string)`. Test helper `sessionFor(t, st, u) *http.Cookie` (a session without a password, as an SSO login makes).

- [ ] **Step 1: Write the failing tests**

Create `internal/api/admin_users_access_test.go`:

```go
package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// sessionFor signs u in without a password, as an SSO login would, and returns its cookie.
func sessionFor(t *testing.T, st store.Store, u *store.User) *http.Cookie {
	t.Helper()
	raw := "tok-" + u.ID
	now := time.Now().UTC()
	if err := st.Sessions().CreateSession(context.Background(), &store.Session{TokenHash: crypto.SHA256Hex([]byte(raw)), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: raw}
}

func TestAdminUserWritesAreLocalOnly(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_synced", Username: "synced", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s1"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "/api/admin/users/usr_synced", `{"display_name":"Mine"}`},
		{"POST", "/api/admin/users/usr_synced/reset-password", ""},
		{"POST", "/api/admin/users/usr_synced/role", `{"role":"admin"}`},
		{"POST", "/api/admin/users/usr_synced/disable", ""},
		{"POST", "/api/admin/users/usr_synced/enable", ""},
	} {
		if w := call(t, srv, tc.method, tc.path, tc.body, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
			t.Errorf("%s %s: %d %s, want 409 managed_externally", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if u, _ := st.Users().GetUserByID(context.Background(), "usr_synced"); u.DisplayName != "" || u.Role != "user" || u.Status != "active" {
		t.Fatalf("a synced account changed: %+v", u)
	}
	if w := call(t, srv, "POST", "/api/admin/users/usr_nobody/disable", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("unknown person: %d, want 404", w.Code)
	}
}

func TestAdminRoleAndStatusRules(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	bob := loginAs(t, srv, st, "bob", "user")
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: "ap_bob", UserID: "usr_bob", Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/admin/users/usr_root/disable"} {
		if w := call(t, srv, "POST", path, "", admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "self" {
			t.Errorf("%s: %d %s, want 409 self", path, w.Code, w.Body.String())
		}
	}
	if w := call(t, srv, "POST", "/api/admin/users/usr_root/role", `{"role":"user"}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "self" {
		t.Errorf("self demotion: %d %s, want 409 self", w.Code, w.Body.String())
	}

	if w := call(t, srv, "POST", "/api/admin/users/usr_bob/role", `{"role":"admin"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("promote bob: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/auth/me", "", bob); bytes.Contains(w.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Error("bob's session survived the role change")
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, "usr_bob"); len(list) != 0 {
		t.Error("bob's app passwords survived the role change")
	}
	if d := auditDetails(t, st, "admin.user_role"); len(d) != 1 || d[0] != "from=user to=admin" {
		t.Errorf("role audit: %v", d)
	}

	restore := api.SetStepUpWindowForTest(0)
	for _, tc := range []struct{ path, body string }{
		{"/api/admin/users/usr_bob/role", `{"role":"user"}`},
		{"/api/admin/users/usr_bob/disable", ""},
		{"/api/admin/users/usr_bob/reset-password", ""},
	} {
		if w := call(t, srv, "POST", tc.path, tc.body, admin); w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
			t.Errorf("%s on a stale session: %d %s, want 403 reauth_required", tc.path, w.Code, w.Body.String())
		}
	}
	restore()

	if w := call(t, srv, "POST", "/api/admin/users/usr_bob/disable", "", admin); w.Code != http.StatusOK {
		t.Fatalf("disable bob: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "POST", "/api/admin/users/usr_bob/enable", "", admin); w.Code != http.StatusOK {
		t.Fatalf("enable bob: %d %s", w.Code, w.Body.String())
	}
	if len(auditDetails(t, st, "admin.user_disable")) != 1 || len(auditDetails(t, st, "admin.user_enable")) != 1 {
		t.Error("want one admin.user_disable and one admin.user_enable row")
	}

	// An SSO admin cannot take away the last active local admin: that account is the way back in.
	if err := st.Users().SetRole(ctx, "usr_bob", "user"); err != nil {
		t.Fatal(err)
	}
	ky := &store.User{ID: "usr_ky", Username: "ky", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "k1"}
	if err := st.Users().CreateUser(ctx, ky); err != nil {
		t.Fatal(err)
	}
	kyAdmin := sessionFor(t, st, ky)
	for _, tc := range []struct{ path, body string }{
		{"/api/admin/users/usr_root/disable", ""},
		{"/api/admin/users/usr_root/role", `{"role":"user"}`},
	} {
		if w := call(t, srv, "POST", tc.path, tc.body, kyAdmin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "last_admin" {
			t.Errorf("%s: %d %s, want 409 last_admin", tc.path, w.Code, w.Body.String())
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/api/ -run 'TestAdminRoleAndStatusRules|TestAdminUserWritesAreLocalOnly'`
Expected: FAIL, `…/usr_root/disable: 200 …, want 409 self` (no route yet).

- [ ] **Step 3: Implement**

Append to `internal/api/admin_users.go`:

```go
// writeAccessError answers a refused SetRole or SetStatus.
func (s *Server) writeAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "This is the last active local administrator; make another one first", "code": "last_admin"})
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "No such person")
	default:
		s.writeError(w, http.StatusInternalServerError, "Failed to change the person")
	}
}

func (s *Server) writeSelf(w http.ResponseWriter, what string) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "You cannot " + what + " yourself; ask another administrator", "code": "self"})
}

// handleSetUserRole makes a local person an administrator or an everyday user. Sessions and app
// passwords are revoked with the change, so nothing runs under the old role.
func (s *Server) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "change a role") {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Role != "user" && body.Role != "admin") {
		s.writeError(w, http.StatusBadRequest, "Role must be user or admin")
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	if u.ID == sessionUser(r.Context()).ID && body.Role != "admin" {
		s.writeSelf(w, "demote")
		return
	}
	if u.Role != body.Role {
		if err := s.store.Users().SetRole(r.Context(), u.ID, body.Role); err != nil {
			s.writeAccessError(w, err)
			return
		}
		s.auditAction(r.Context(), r, "admin.user_role", u.ID, "from="+u.Role+" to="+body.Role)
	}
	s.writeUser(w, r, u.ID)
}

// handleDisableUser deactivates a local person and revokes their sessions and app passwords.
func (s *Server) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "disable a person") {
		return
	}
	s.setUserStatus(w, r, "inactive", "admin.user_disable")
}

func (s *Server) handleEnableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "active", "admin.user_enable")
}

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request, status, action string) {
	r = r.WithContext(context.WithoutCancel(r.Context()))
	u := s.adminUser(w, r, true)
	if u == nil {
		return
	}
	if u.ID == sessionUser(r.Context()).ID && status != "active" {
		s.writeSelf(w, "disable")
		return
	}
	if u.Status != status {
		if err := s.store.Users().SetStatus(r.Context(), u.ID, status); err != nil {
			s.writeAccessError(w, err)
			return
		}
		s.auditAction(r.Context(), r, action, u.ID, "")
	}
	s.writeUser(w, r, u.ID)
}
```

In `routes()`:

```go
	s.handle("POST /api/admin/users/{id}/role", s.tracked(s.requireAdmin(s.handleSetUserRole)))
	s.handle("POST /api/admin/users/{id}/disable", s.tracked(s.requireAdmin(s.handleDisableUser)))
	s.handle("POST /api/admin/users/{id}/enable", s.tracked(s.requireAdmin(s.handleEnableUser)))
```

In `apiRows` (then `gofmt -w internal/api/authz_matrix_test.go`):

```go
		"POST /api/admin/users/{id}/role":    {method: "POST", path: "/api/admin/users/usr_victim/role", body: `{"role":"user"}`, want: adminOnly},
		"POST /api/admin/users/{id}/disable": {method: "POST", path: "/api/admin/users/usr_victim/disable", want: adminOnly},
		"POST /api/admin/users/{id}/enable":  {method: "POST", path: "/api/admin/users/usr_victim/enable", want: adminOnly},
```

- [ ] **Step 4: Run the API suite**

Run: `go test -count=1 -p 1 ./internal/api/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 5: DOX**

In `internal/api/AGENTS.md`, add after the `GET /api/admin/users` row:

```markdown
| POST | `/api/admin/users` | admin (+ step-up for `role: admin`); detached, tracked | `{username,display_name?,email?,role?}` -> 201 `{user,temporary_password}` (`Cache-Control: no-store`); 409 `name_taken` (any case, any provider) |
| PATCH | `/api/admin/users/{id}` | admin; local | partial `{username?,display_name?,email?}`, every field checked before any write -> 200 user; 409 `name_taken`, `managed_externally` |
| POST | `/api/admin/users/{id}/reset-password` | admin + step-up; local; detached, tracked | 200 `{temporary_password}` (`no-store`); the operator reset revokes sessions, MFA challenges and app passwords |
| POST | `/api/admin/users/{id}/role` | admin + step-up; local; detached, tracked | `{role}` `user` or `admin` -> 200 user; 409 `self`, `last_admin` |
| POST | `/api/admin/users/{id}/disable` | admin + step-up; local; detached, tracked | 200 user; 409 `self`, `last_admin` |
| POST | `/api/admin/users/{id}/enable` | admin; local; detached, tracked | 200 user |
```

In the root `AGENTS.md`, append to `#### Standalone administration contracts`:

```markdown
- People: admin writes reach `local` accounts only (409 `managed_externally`); there is no delete (it would delete the person's calendars) and admins never type passwords. Create and reset return a server-generated temporary password once (`Cache-Control: no-store`), stored only as a hash, with a forced change at first sign-in. Role and status changes revoke sessions, MFA challenges, device pairings and app passwords in the same transaction under the `local-admins` lock: nobody can demote or disable themselves (409 `self`), and the last active local administrator cannot be demoted or disabled by anyone (409 `last_admin`). Step-up: creating an administrator, reset, role change, disable. Audit: `admin.user_create`, `admin.user_update`, `admin.user_reset_password`, `admin.user_role`, `admin.user_disable`, `admin.user_enable`; a rename also writes `user.renamed` with the acting admin.
```

and in the Plan 2 bullet that starts `Administrator = the KyIdentity app role`, replace the last sentence `Local \`init-admin\` accounts are break-glass and keep their stored role.` with `Local accounts get their role from \`init-admin\` or the People screen and keep it.`

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin_users.go internal/api/admin_users_access_test.go internal/api/server.go internal/api/authz_matrix_test.go internal/api/AGENTS.md AGENTS.md
git commit -m "feat(api): change roles, disable and enable local people" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 20: People page

**Files:**
- Create: `web/src/pages/People.tsx`, `web/src/pages/People.test.tsx`
- Modify: `web/src/App.tsx`, `web/src/components/AppHeader.tsx`, `web/src/pages/Dashboard.tsx`, `web/src/App.test.tsx`
- Modify: `README.md`, `web/AGENTS.md`; rebuilt `web/dist`

**Interfaces:**
- Consumes: `send`, `failure`, `JSON_HEADERS`, `REAUTH`, `SourceBadge` (`web/src/admin.tsx`); routes from Tasks 17-19.
- Produces: default export `People({ me }: { me: string })`; tab `people`; the temporary password renders once in `<code data-testid="temporary-password">` inside the "Temporary password" dialog.

- [ ] **Step 1: Write the failing tests**

Create `web/src/pages/People.test.tsx`:

```tsx
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import People from "./People";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockFetch(handlers: Record<string, (init?: RequestInit) => unknown>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const person = (over: Record<string, unknown>) => ({
  id: "u1", username: "ann", display_name: "Ann", email: "", role: "user", status: "active", source: "local",
  mfa: false, must_change_password: false, last_login_at: null, ...over,
});
const users = (...list: unknown[]) => ({ users: list, total: list.length });

describe("People", () => {
  it("shows a new person's temporary password once", async () => {
    let posted: unknown;
    mockFetch({
      "GET /api/admin/users": () => users(),
      "POST /api/admin/users": (init) => {
        posted = JSON.parse(String(init?.body));
        return { user: person({}), temporary_password: "Tmp-Once-Only-1234" };
      },
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Add person" }));
    const add = screen.getByRole("dialog", { name: "Add person" });
    fireEvent.change(within(add).getByLabelText("Username"), { target: { value: "ann" } });
    fireEvent.click(within(add).getByRole("button", { name: "Create" }));
    const handover = await screen.findByRole("dialog", { name: "Temporary password" });
    expect(within(handover).getByTestId("temporary-password").textContent).toBe("Tmp-Once-Only-1234");
    expect(posted).toEqual({ username: "ann", display_name: "", email: "", role: "user" });
    fireEvent.click(within(handover).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByText("Tmp-Once-Only-1234")).toBeNull());
  });

  it("shows synced people with a badge and no actions", async () => {
    mockFetch({ "GET /api/admin/users": () => users(person({ id: "u2", username: "sam", source: "kysignon" })) });
    render(<People me="u0" />);
    const row = (await screen.findByText("Managed by KyIdentity")).closest("tr")!;
    expect(within(row).queryAllByRole("button")).toHaveLength(0);
  });

  it("offers no demote or disable on your own row", async () => {
    mockFetch({ "GET /api/admin/users": () => users(person({ id: "me", username: "root", role: "admin" })) });
    render(<People me="me" />);
    await screen.findByRole("button", { name: "Edit root" });
    expect(screen.queryByRole("button", { name: /Make root/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Disable root/ })).toBeNull();
  });

  it("asks for a fresh sign-in when a reset needs step-up", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/users": () => users(person({})),
      "POST /api/admin/users/u1/reset-password": () => new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Reset password for ann" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("explains why the last local admin cannot be disabled", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/users": () => users(person({ role: "admin" })),
      "POST /api/admin/users/u1/disable": () =>
        new Response(JSON.stringify({ error: "This is the last active local administrator; make another one first", code: "last_admin" }), { status: 409 }),
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Disable ann" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/last active local administrator/);
  });
});
```

In `web/src/App.test.tsx`, add `'People'` to the label list in `keeps admin pages from everyday users`.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/pages/People.test.tsx`
Expected: FAIL, `Failed to resolve import "./People"`.

- [ ] **Step 3: The page**

Create `web/src/pages/People.tsx`:

```tsx
import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { JSON_HEADERS, REAUTH, SourceBadge, failure, send } from "../admin";

interface Person {
  id: string;
  username: string;
  display_name: string;
  email: string;
  role: string;
  status: string;
  source: string;
  mfa: boolean;
  must_change_password: boolean;
  last_login_at: string | null;
}

interface Handover {
  username: string;
  password: string;
}

type Action = "reset-password" | "role" | "disable" | "enable";

async function refusal(res: Response | null, fallback: string): Promise<string> {
  const f = await failure(res);
  return f.code === "reauth_required" ? REAUTH : (f.error ?? fallback);
}

export default function People({ me }: { me: string }) {
  const [people, setPeople] = useState<Person[]>([]);
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Person | null>(null);
  const [handover, setHandover] = useState<Handover | null>(null);

  const load = useCallback(async (q = "") => {
    const res = await send(q ? `/api/admin/users?q=${encodeURIComponent(q)}` : "/api/admin/users");
    if (!res?.ok) {
      setError("Could not load people. Reload the page to try again.");
      return;
    }
    setPeople((await res.json()).users);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function act(p: Person, action: Action, body?: object) {
    setError(null);
    const res = await send(`/api/admin/users/${encodeURIComponent(p.id)}/${action}`, {
      method: "POST",
      headers: body ? JSON_HEADERS : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res?.ok) {
      setError(await refusal(res, "Could not change this person"));
      return;
    }
    if (action === "reset-password") setHandover({ username: p.username, password: (await res.json()).temporary_password });
    await load(query);
  }

  function reset(p: Person) {
    if (window.confirm(`Reset ${p.username}'s password? They are signed out everywhere and their phones stop syncing until they add new app passwords.`)) {
      void act(p, "reset-password");
    }
  }

  function toggleRole(p: Person) {
    const to = p.role === "admin" ? "user" : "admin";
    const text =
      to === "admin"
        ? `Make ${p.username} an administrator? Administrators never see calendars. They are signed out everywhere.`
        : `Make ${p.username} an everyday user? They are signed out everywhere.`;
    if (window.confirm(text)) void act(p, "role", { role: to });
  }

  function toggleStatus(p: Person) {
    if (p.status !== "active") {
      void act(p, "enable");
      return;
    }
    if (window.confirm(`Disable ${p.username}? They are signed out everywhere and their phones stop syncing. Their calendars stay.`)) {
      void act(p, "disable");
    }
  }

  return (
    <section className="page">
      <h1>People</h1>
      <p>
        Add people who sign in with a password here. People from your identity provider are read-only; change them there.
        Administrators manage this instance and never see calendars.
      </p>
      <form
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          void load(query);
        }}
      >
        <label>
          Search people
          <input value={query} onChange={(e) => setQuery(e.target.value)} maxLength={255} />
        </label>
        <button type="submit">Search</button>
      </form>
      <button type="button" onClick={() => setAdding(true)}>
        Add person
      </button>
      {error && <p role="alert">{error}</p>}
      <table>
        <thead>
          <tr>
            <th>Username</th>
            <th>Name</th>
            <th>Email</th>
            <th>Role</th>
            <th>Status</th>
            <th>Last sign-in</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {people.map((p) => (
            <tr key={p.id}>
              <td>
                {p.username} <SourceBadge source={p.source} />
              </td>
              <td>{p.display_name}</td>
              <td>{p.email}</td>
              <td>{p.role === "admin" ? "Administrator" : "Everyday"}</td>
              <td>
                {p.status === "active" ? "Active" : "Disabled"}
                {p.mfa ? " · MFA" : ""}
              </td>
              <td>{p.last_login_at ? new Date(p.last_login_at).toLocaleString() : "Never"}</td>
              <td>
                {p.source === "local" && (
                  <>
                    <button type="button" aria-label={`Edit ${p.username}`} onClick={() => setEditing(p)}>
                      Edit
                    </button>
                    <button type="button" aria-label={`Reset password for ${p.username}`} onClick={() => reset(p)}>
                      Reset password
                    </button>
                    {p.id !== me && (
                      <>
                        <button type="button" onClick={() => toggleRole(p)}>
                          {p.role === "admin" ? `Make ${p.username} a user` : `Make ${p.username} an admin`}
                        </button>
                        <button type="button" onClick={() => toggleStatus(p)}>
                          {p.status === "active" ? `Disable ${p.username}` : `Enable ${p.username}`}
                        </button>
                      </>
                    )}
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {adding && (
        <AddPerson
          onClose={() => setAdding(false)}
          onCreated={(h) => {
            setAdding(false);
            setHandover(h);
            void load(query);
          }}
        />
      )}
      {editing && (
        <EditPerson
          person={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void load(query);
          }}
        />
      )}
      {handover && <PasswordHandover handover={handover} onClose={() => setHandover(null)} />}
    </section>
  );
}

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current!;
    d.showModal();
    return () => d.close();
  }, []);
  return (
    <dialog
      ref={ref}
      aria-label={title}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <h2>{title}</h2>
      {children}
    </dialog>
  );
}

function AddPerson({ onClose, onCreated }: { onClose: () => void; onCreated: (h: Handover) => void }) {
  const [form, setForm] = useState({ username: "", display_name: "", email: "", role: "user" });
  const [error, setError] = useState<string | null>(null);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send("/api/admin/users", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify(form) });
    if (!res?.ok) {
      setError(await refusal(res, "Could not add the person"));
      return;
    }
    onCreated({ username: form.username, password: (await res.json()).temporary_password });
  }

  return (
    <Modal title="Add person" onClose={onClose}>
      <form onSubmit={submit}>
        <label htmlFor="person-username">Username</label>
        <input id="person-username" value={form.username} onChange={set("username")} maxLength={64} required autoFocus />
        <label htmlFor="person-name">Display name</label>
        <input id="person-name" value={form.display_name} onChange={set("display_name")} maxLength={255} />
        <label htmlFor="person-email">Email</label>
        <input id="person-email" type="email" value={form.email} onChange={set("email")} />
        <label htmlFor="person-role">Role</label>
        <select id="person-role" value={form.role} onChange={set("role")}>
          <option value="user">Everyday user</option>
          <option value="admin">Administrator (no calendars)</option>
        </select>
        {error && <p role="alert">{error}</p>}
        <button type="submit">Create</button>
        <button type="button" className="btn-secondary" onClick={onClose}>
          Cancel
        </button>
      </form>
    </Modal>
  );
}

function EditPerson({ person, onClose, onSaved }: { person: Person; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState({ username: person.username, display_name: person.display_name, email: person.email });
  const [error, setError] = useState<string | null>(null);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send(`/api/admin/users/${encodeURIComponent(person.id)}`, { method: "PATCH", headers: JSON_HEADERS, body: JSON.stringify(form) });
    if (!res?.ok) {
      setError(await refusal(res, "Could not save the changes"));
      return;
    }
    onSaved();
  }

  return (
    <Modal title={`Edit ${person.username}`} onClose={onClose}>
      <form onSubmit={submit}>
        <label htmlFor="edit-username">Username</label>
        <input id="edit-username" value={form.username} onChange={set("username")} maxLength={64} required />
        <label htmlFor="edit-name">Display name</label>
        <input id="edit-name" value={form.display_name} onChange={set("display_name")} maxLength={255} required />
        <label htmlFor="edit-email">Email</label>
        <input id="edit-email" type="email" value={form.email} onChange={set("email")} />
        {error && <p role="alert">{error}</p>}
        <button type="submit">Save</button>
        <button type="button" className="btn-secondary" onClick={onClose}>
          Cancel
        </button>
      </form>
    </Modal>
  );
}

function PasswordHandover({ handover, onClose }: { handover: Handover; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <Modal title="Temporary password" onClose={onClose}>
      <p>
        Give this to {handover.username} in person or over another channel you trust, not in the same message as the
        username. It is shown once: they choose their own password at first sign-in.
      </p>
      <p>
        <code data-testid="temporary-password">{handover.password}</code>
      </p>
      <button
        type="button"
        onClick={() =>
          void navigator.clipboard?.writeText(handover.password).then(
            () => setCopied(true),
            () => setCopied(false),
          )
        }
      >
        {copied ? "Copied" : "Copy"}
      </button>
      <button type="button" onClick={onClose}>
        Done
      </button>
    </Modal>
  );
}
```

- [ ] **Step 4: Navigation, page and card**

`web/src/components/AppHeader.tsx`: add `UserRound` to the `lucide-react` import and, in the admin list after Overview, the item `{ id: 'people', label: 'People', icon: UserRound },`.

`web/src/App.tsx`: add `import People from './pages/People';` after the `Groups` import, and after the admin `dashboard` line:

```tsx
        {isAdmin && activeTab === 'people' && <People me={user.id} />}
```

`web/src/pages/Dashboard.tsx`: add `UserRound` to the `lucide-react` import and make this the first entry of `cards`:

```tsx
    {
      title: 'People',
      desc: 'Add people who sign in with a password, reset them, make administrators, disable accounts.',
      status: 'Local and synced',
      statusType: 'accent',
      icon: UserRound,
      action: () => onNavigate('people'),
      actionLabel: 'Manage people',
    },
```

- [ ] **Step 5: Run the web suite and build**

Run: `cd web && npm test && npm run build`
Expected: every test file passes; the build rewrites `web/dist`.

- [ ] **Step 6: DOX and README**

In `web/AGENTS.md`, add `people` to the `App.tsx` admin tab list and add:

```markdown
- `src/pages/People.tsx` is admin-only (tab `people`): search, add a person (the server-generated temporary password shows once in a dialog with Copy and an out-of-band hand-over note), and, on local rows only, edit, reset password, make admin or user, disable or enable, each destructive one behind a confirm. Your own row offers no demote or disable. Synced rows show their badge and no actions. A 403 `reauth_required` asks for a fresh sign-in; `last_admin` and `self` show the server's message.
```

In `README.md`, at the top of `## Local everyday accounts` add: `The **People** screen is the everyday way to add and manage local accounts; these commands are for scripts and recovery.` Then insert a section before `## Groups`:

```markdown
## People

**People** lists every account. Local accounts (people who sign in with a password) can be edited here; accounts from KyIdentity or SCIM carry a "Managed by …" badge and change only in the identity provider.

- **Add person**: username, optional display name and email, and Everyday user or Administrator. KyCalendar generates a temporary password and shows it once: give it to the person in person or over another channel you trust, not in the same message as the username. They choose their own password at first sign-in.
- **Reset password** shows a new temporary password once and signs the person out everywhere; their phones stop syncing until they create new app passwords.
- **Make admin / Make user** and **Disable** sign the person out everywhere. Administrators never see calendars. You cannot demote or disable yourself, and nobody can demote or disable the last active local administrator: that account is the way back in when single sign-on is down.
- Adding an administrator, resetting, changing a role and disabling need a sign-in from the last 10 minutes. There is no delete: disabling keeps the person's calendars.
```

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/People.tsx web/src/pages/People.test.tsx web/src/App.tsx web/src/App.test.tsx web/src/components/AppHeader.tsx web/src/pages/Dashboard.tsx web/AGENTS.md README.md web/dist
git commit -m "feat(web): People screen with one-time password hand-over" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 21: Chromium: a new person signs in

**Files:**
- Create: `web/browser/people.spec.mjs`
- Modify: `web/browser/AGENTS.md`, root `AGENTS.md` (Chromium bullet)

**Interfaces:**
- Consumes: the People screen, `ChangePassword` (labels `Current password`, `New password`, `Confirm new password`), App's `role="status"` notice after a password change.
- Produces: one spec; the username carries the time and project, so the four projects never collide.

- [ ] **Step 1: Write the spec**

Create `web/browser/people.spec.mjs`:

```js
import { test, expect } from '@playwright/test';

async function signIn(page, username, password) {
  await page.goto('/');
  await page.getByPlaceholder('admin', { exact: true }).fill(username);
  await page.locator('input[type=password]').fill(password);
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
}

test('people: an admin adds a person who signs in and lands on the calendar', async ({ page, browser }, testInfo) => {
  const username = `p${Date.now()}-${testInfo.project.name}`;

  await signIn(page, 'admin', 'BrowserUpdated456!');
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'People', exact: true }).click();
  await page.getByRole('button', { name: 'Add person' }).click();
  const add = page.getByRole('dialog', { name: 'Add person' });
  await add.getByLabel('Username').fill(username);
  await add.getByLabel('Display name').fill('Pat Example');
  await add.getByRole('button', { name: 'Create' }).click();

  const handover = page.getByRole('dialog', { name: 'Temporary password' });
  const temporary = (await handover.getByTestId('temporary-password').textContent()).trim();
  expect(temporary.length).toBeGreaterThanOrEqual(12);
  await handover.getByRole('button', { name: 'Done' }).click();
  await expect(page.getByRole('row').filter({ hasText: username })).toBeVisible();
  await expect(page.getByText(temporary)).toHaveCount(0);

  // The person signs in with the temporary password, must replace it, then lands on the calendar.
  const context = await browser.newContext();
  const person = await context.newPage();
  await signIn(person, username, temporary);
  await person.getByLabel('Current password').fill(temporary);
  await person.getByLabel('New password', { exact: true }).fill('PersonChosen789!');
  await person.getByLabel('Confirm new password').fill('PersonChosen789!');
  await person.getByRole('button', { name: 'Change password' }).click();
  await expect(person.getByRole('status')).toContainText('Password changed');
  await person.getByPlaceholder('admin', { exact: true }).fill(username);
  await person.locator('input[type=password]').fill('PersonChosen789!');
  await person.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(person.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Calendar' })).toHaveAttribute('aria-current', 'page');
  await expect(person.locator('.fc')).toBeVisible();
  await context.close();
});
```

- [ ] **Step 2: Run it against the built server**

Run: `go build -o .browser/server ./cmd/server && cd web && npx playwright install chromium && npx playwright test browser/people.spec.mjs`
Expected: `4 passed`.

- [ ] **Step 3: DOX**

In `web/browser/AGENTS.md` add: ``- `people.spec.mjs` has `admin` add a person, reads the one-time password from the dialog, then signs that person in through a separate context, replaces the password and lands on the calendar.`` In the root `AGENTS.md` Chromium bullet, extend the admin flows to `(a local group's calendar reaching its member, a new person signing in)`.

- [ ] **Step 4: Commit**

```bash
git add web/browser/people.spec.mjs web/browser/AGENTS.md AGENTS.md
git commit -m "test(browser): a person added on the People screen signs in" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 22: Smoke: create and list a person on the built binary

**Files:**
- Modify: `scripts/smoke-test.sh` (after `check "unpair refuses while unpaired"`, ~:187)

**Interfaces:**
- Consumes: the smoke helpers `check`, `contains`, `status`; `$WORK/cookies` holds the admin session; `CSRF` is current at that point.
- Produces: five checks on the real binary.

- [ ] **Step 1: Add the checks**

Insert after the line `check "unpair refuses while unpaired" …`:

```bash
# People: an admin adds a local person; the temporary password comes back once, never a hash.
PERSON_BODY="$(curl -s -b "$WORK/cookies" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d '{"username":"smoke-person","display_name":"Smoke Person","role":"user"}' -X POST "$BASE/api/admin/users")"
contains "admin adds a person" "$PERSON_BODY" '"temporary_password"'
TEMP_PASS="$(printf '%s' "$PERSON_BODY" | sed -n 's/.*"temporary_password":"\([^"]*\)".*/\1/p')"
contains "the temporary password signs in, flagged for replacement" \
  "$(curl -s -H 'Content-Type: application/json' -d '{"username":"smoke-person","password":"'"$TEMP_PASS"'"}' "$BASE/api/auth/login")" '"must_change_password":true'
PEOPLE_JSON="$(curl -s -b "$WORK/cookies" "$BASE/api/admin/users?q=smoke-person")"
contains "people list finds the new person" "$PEOPLE_JSON" '"username":"smoke-person"'
check "people list carries no hashes" "$(if printf '%s' "$PEOPLE_JSON" | grep -q 'argon2'; then echo leaked; else echo clean; fi)" "clean"
check "anonymous cannot list people" "$(status "$BASE/api/admin/users")" "401"
```

- [ ] **Step 2: Run the smoke test**

Run: `make build && KY_SMOKE_PORT=28931 ./scripts/smoke-test.sh`
Expected: the five new `[ok]` lines and `smoke test: all checks passed`.

- [ ] **Step 3: Commit**

```bash
git add scripts/smoke-test.sh
git commit -m "test(smoke): add and list a person through the API" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 23: PR 2 final verification and pull request

Run each command alone and let it finish before starting the next.

- [ ] **Step 1:** `make tidy-check` — Expected: no diff.
- [ ] **Step 2:** `make lint` — Expected: clean.
- [ ] **Step 3:** `go test -race -count=1 -p 1 ./internal/store/...` — Expected: `ok`; `TestConcurrentDemotionKeepsOneLocalAdmin` runs under the race detector here.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/api/...` — Expected: `ok`.
- [ ] **Step 5:** `go test -race -count=1 -p 1 ./cmd/... ./internal/access/... ./internal/apppass/... ./internal/auth/... ./internal/backup/... ./internal/calendar/... ./internal/config/... ./internal/crypto/... ./internal/davbackend/... ./internal/scim/... ./internal/sso/... ./internal/testdb/... ./web/...` — Expected: every package `ok` or `no test files`.
- [ ] **Step 6:** `make test-fork` — Expected: `ok`.
- [ ] **Step 7:** `cd web && npm ci && npm test` — Expected: every test file passes.
- [ ] **Step 8:** `cd web && npm run build && cd .. && git status --porcelain web/dist` — Expected: no output (otherwise commit `web/dist` as in Task 13).
- [ ] **Step 9:** `make build && KY_SMOKE_PORT=28931 ./scripts/smoke-test.sh` — Expected: `smoke test: all checks passed`.
- [ ] **Step 10:** `go build -o .browser/server ./cmd/server && cd web && npx playwright install chromium && npm run test:browser` — Expected: every spec passes in all four projects.
- [ ] **Step 11 (when a PostgreSQL is reachable):** `make test-postgres` — Expected: `ok`; the `local-admins` advisory lock is what the concurrent-demotion test proves there.
- [ ] **Step 12: Open the PR**

```bash
git push -u origin feat/standalone-people
gh pr create --base main --title "Standalone administration 2/3: people" --body "$(cat <<'BODY'
Local people managed from KyCalendar's own People screen.

- Admin routes: create (one-time temporary password), edit, reset password, role, disable, enable; synced accounts are read-only (409 managed_externally).
- Role and status changes revoke grants in one transaction; nobody can demote or disable themselves, and the last active local admin stays.
- Step-up for adding an admin, reset, role change and disable; every change audited with the acting admin.
- Last sign-in is recorded; smoke and Chromium cover a new person signing in.

Spec: docs/superpowers/specs/2026-10-07-kycalendar-standalone-admin-design.md

🤖 Generated with [Claude Code](https://claude.com/claude-code)
BODY
)"
```

---

# PR 3 — Sign-in

Branch `feat/standalone-signin` from `main` after PR 2 merges. Ends with Task 32.

## Task 24: Sign-in settings: environment precedence and the sealed secret

**Files:**
- Create: `internal/sso/settings.go`, `internal/sso/settings_test.go`
- Modify: `internal/sso/AGENTS.md`, `internal/config/AGENTS.md`

**Interfaces:**
- Consumes: `config.SSOConfig` (`KySignOnIssuer`, `KySignOnClientID`, `KySignOnSecret`, `KySignOnHMACSecret`), `crypto.EncryptAESGCM`, `crypto.DecryptAESGCM`, `crypto.DeriveKey`.
- Produces: `sso.KindNone|KindKyIdentity|KindOIDC`; keys `sso.KeyProvider` (`signin_provider`), `KeyDisplayName` (`signin_display_name`), `KeyIssuer` (`signin_issuer`), `KeyClientID` (`signin_client_id`), `KeySecretSealed` (`signin_client_secret_sealed`), `KeyBound` (`signin_bound`); `sso.SecretLabel = "kycalendar:setting:signin_client_secret"`; `SourceEnvironment|SourceSaved|SourceUnset`; `type Field struct{Value, Source string}` (json `value`, `source`); `type Settings struct{Provider, DisplayName, Issuer, ClientID, Secret Field}`; `func Resolve(env config.SSOConfig, saved map[string]string) Settings`; `func (Settings) Live() bool`; `func (Settings) Identity() string` (`"<kind> <issuer>"`); `func AccountProviders(kind string) []string`; `func SealSecret(master []byte, secret string) (string, error)`; `func OpenSecret(master []byte, sealed string) (string, error)`.

- [ ] **Step 1: Branch**

```bash
git switch main && git pull --ff-only && git switch -c feat/standalone-signin
```

- [ ] **Step 2: Write the failing tests**

Create `internal/sso/settings_test.go`:

```go
package sso_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
)

func TestResolveEnvironmentWinsAndLocks(t *testing.T) {
	saved := map[string]string{
		sso.KeyProvider: sso.KindOIDC, sso.KeyDisplayName: "Acme", sso.KeyIssuer: "https://saved.example",
		sso.KeyClientID: "saved-client", sso.KeySecretSealed: "sealed-blob",
	}
	st := sso.Resolve(config.SSOConfig{KySignOnIssuer: "https://id.example/", KySignOnSecret: "env-secret"}, saved)
	want := sso.Settings{
		Provider:    sso.Field{Value: sso.KindKyIdentity, Source: sso.SourceEnvironment},
		DisplayName: sso.Field{Value: "Acme", Source: sso.SourceSaved},
		Issuer:      sso.Field{Value: "https://id.example", Source: sso.SourceEnvironment},
		ClientID:    sso.Field{Value: "saved-client", Source: sso.SourceSaved},
		Secret:      sso.Field{Value: "env-secret", Source: sso.SourceEnvironment},
	}
	if st != want {
		t.Fatalf("got %+v\nwant %+v", st, want)
	}
	// The webhook secret alone still means KyIdentity runs this instance's directory.
	if st := sso.Resolve(config.SSOConfig{KySignOnHMACSecret: "h"}, saved); st.Provider != (sso.Field{Value: sso.KindKyIdentity, Source: sso.SourceEnvironment}) {
		t.Errorf("HMAC secret only: provider %+v", st.Provider)
	}
}

func TestResolveSavedAndUnset(t *testing.T) {
	st := sso.Resolve(config.SSOConfig{}, map[string]string{
		sso.KeyProvider: sso.KindOIDC, sso.KeyIssuer: "https://saved.example", sso.KeyClientID: "c", sso.KeySecretSealed: "sealed-blob",
	})
	if st.Provider.Source != sso.SourceSaved || st.Issuer.Value != "https://saved.example" || !st.Live() {
		t.Fatalf("saved oidc: %+v", st)
	}
	if st.Secret != (sso.Field{Source: sso.SourceSaved}) {
		t.Fatalf("a saved secret must stay sealed until opened: %+v", st.Secret)
	}
	if st.Identity() != "oidc https://saved.example" {
		t.Fatalf("identity %q", st.Identity())
	}
	empty := sso.Resolve(config.SSOConfig{}, nil)
	if empty.Provider != (sso.Field{Value: sso.KindNone, Source: sso.SourceUnset}) || empty.Live() {
		t.Fatalf("nothing configured: %+v", empty)
	}
	if ky := sso.Resolve(config.SSOConfig{}, map[string]string{sso.KeyProvider: sso.KindKyIdentity}); ky.DisplayName.Value != "KyIdentity" {
		t.Fatalf("kyidentity default label: %+v", ky.DisplayName)
	}
}

func TestSealedSecretRoundTripAndLabel(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	sealed, err := sso.SealSecret(master, "s3cret-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "s3cret") {
		t.Fatal("the sealed form carries the plain secret")
	}
	if got, err := sso.OpenSecret(master, sealed); err != nil || got != "s3cret-value" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := crypto.DecryptAESGCM(sealed, crypto.DeriveKey(master, "kycalendar:setting:other")); err == nil {
		t.Fatal("the secret opened under another label's key")
	}
	if _, err := crypto.DecryptAESGCM(sealed, master); err == nil {
		t.Fatal("the secret opened under the master key itself")
	}
	if _, err := sso.OpenSecret(bytes.Repeat([]byte{8}, 32), sealed); err == nil {
		t.Fatal("the secret opened under another data-volume key")
	}
}

func TestAccountProviders(t *testing.T) {
	if got := sso.AccountProviders(sso.KindKyIdentity); strings.Join(got, ",") != "kysignon,scim" {
		t.Errorf("kyidentity: %v", got)
	}
	if got := sso.AccountProviders(sso.KindOIDC); strings.Join(got, ",") != "oidc" {
		t.Errorf("oidc: %v", got)
	}
	if got := sso.AccountProviders(sso.KindNone); got != nil {
		t.Errorf("none: %v", got)
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test -count=1 ./internal/sso/ -run 'TestResolve|TestSealedSecret|TestAccountProviders'`
Expected: build failure, `undefined: sso.Resolve`.

- [ ] **Step 4: Implement**

Create `internal/sso/settings.go`:

```go
package sso

import (
	"strings"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// Provider kinds. Both run the same OIDC flow; only kyidentity logins can be administrators or
// adopt SCIM accounts.
const (
	KindNone       = "none"
	KindKyIdentity = "kyidentity"
	KindOIDC       = "oidc"
)

// Settings-store keys. The client secret is stored only sealed.
const (
	KeyProvider     = "signin_provider"
	KeyDisplayName  = "signin_display_name"
	KeyIssuer       = "signin_issuer"
	KeyClientID     = "signin_client_id"
	KeySecretSealed = "signin_client_secret_sealed"
	// KeyBound is the Identity of the provider existing SSO accounts belong to.
	KeyBound = "signin_bound"
)

// SecretLabel derives the key that seals the client secret from the data-volume key.
const SecretLabel = "kycalendar:setting:signin_client_secret"

// Where a field's value comes from.
const (
	SourceEnvironment = "environment"
	SourceSaved       = "saved"
	SourceUnset       = "unset"
)

type Field struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// Settings is the effective sign-in configuration. Secret.Value is the plain secret once the
// caller has opened a saved one; it is never serialised.
type Settings struct {
	Provider, DisplayName, Issuer, ClientID, Secret Field
}

// Resolve merges the environment over saved settings. A KY_KYSIGNON_* value set in the
// environment wins and locks its field, and any of them fixes the provider to kyidentity. A
// saved secret is reported as set but left sealed: Secret.Value is empty until the caller opens it.
func Resolve(env config.SSOConfig, saved map[string]string) Settings {
	pick := func(envValue, key string) Field {
		switch {
		case envValue != "":
			return Field{envValue, SourceEnvironment}
		case saved[key] != "":
			return Field{saved[key], SourceSaved}
		}
		return Field{"", SourceUnset}
	}
	st := Settings{
		Provider:    pick("", KeyProvider),
		DisplayName: pick("", KeyDisplayName),
		Issuer:      pick(strings.TrimRight(env.KySignOnIssuer, "/"), KeyIssuer),
		ClientID:    pick(env.KySignOnClientID, KeyClientID),
		Secret:      pick(env.KySignOnSecret, KeySecretSealed),
	}
	if st.Secret.Source == SourceSaved {
		st.Secret.Value = ""
	}
	if env.KySignOnIssuer != "" || env.KySignOnClientID != "" || env.KySignOnSecret != "" || env.KySignOnHMACSecret != "" {
		st.Provider = Field{KindKyIdentity, SourceEnvironment}
	}
	if st.Provider.Value == "" {
		st.Provider = Field{KindNone, SourceUnset}
	}
	if st.DisplayName.Value == "" && st.Provider.Value == KindKyIdentity {
		st.DisplayName.Value = "KyIdentity"
	}
	return st
}

// Live reports whether the settings name a provider people can sign in with.
func (st Settings) Live() bool {
	return st.Provider.Value != KindNone && st.Issuer.Value != "" && st.ClientID.Value != ""
}

// Identity is what account binding compares: the provider kind and its issuer.
func (st Settings) Identity() string { return st.Provider.Value + " " + st.Issuer.Value }

// AccountProviders lists the users.sso_provider values a kind's logins reach. A KyIdentity
// login also signs in as the SCIM row with its sub, so those rows are KyIdentity's too.
func AccountProviders(kind string) []string {
	switch kind {
	case KindKyIdentity:
		return []string{"kysignon", "scim"}
	case KindOIDC:
		return []string{"oidc"}
	}
	return nil
}

// SealSecret encrypts the client secret under a key derived for this one setting.
func SealSecret(master []byte, secret string) (string, error) {
	return crypto.EncryptAESGCM([]byte(secret), crypto.DeriveKey(master, SecretLabel))
}

func OpenSecret(master []byte, sealed string) (string, error) {
	plain, err := crypto.DecryptAESGCM(sealed, crypto.DeriveKey(master, SecretLabel))
	return string(plain), err
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/sso/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/sso`

- [ ] **Step 6: DOX**

In `internal/sso/AGENTS.md` Local Contracts add:

```markdown
- Sign-in settings live in settings keys `signin_provider`, `signin_display_name`, `signin_issuer`, `signin_client_id`, `signin_client_secret_sealed` and `signin_bound`. `Resolve` merges the environment over them, field by field, and reports each field's source. `SealSecret`/`OpenSecret` encrypt under `DeriveKey(master, SecretLabel)`, so the sealed secret opens under no other key.
```

In `internal/config/AGENTS.md` Local Contracts add:

```markdown
- `KY_KYSIGNON_ISSUER`, `KY_KYSIGNON_CLIENT_ID` and `KY_KYSIGNON_SECRET` are the environment layer of the sign-in settings: each one set locks its field on the Sign-in screen, and any `KY_KYSIGNON_*` (including `_HMAC_SECRET`) fixes the provider to `kyidentity` (`sso.Resolve`). The issuer is used with its trailing slash trimmed, as before.
```

- [ ] **Step 7: Commit**

```bash
git add internal/sso/settings.go internal/sso/settings_test.go internal/sso/AGENTS.md internal/config/AGENTS.md
git commit -m "feat(sso): sign-in settings with environment precedence and a sealed secret" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 25: Guarded discovery

**Files:**
- Create: `internal/sso/discovery.go`, `internal/sso/discovery_test.go`
- Modify: `internal/sso/AGENTS.md`

**Interfaces:**
- Consumes: `oidc.NewProvider`, `oidc.ClientContext`, `oidc.IssuerMismatchError`, `(*oidc.Provider).Claims`.
- Produces: `var sso.ErrAddressRefused`; `type AddrPolicy func(netip.Addr) error`; `func RefuseLocal(a netip.Addr) error`; `func NewGuardedClient(policy AddrPolicy, roots *x509.CertPool) *http.Client` (HTTPS only, no redirects, no proxy, 1 MiB bodies, policy checked on the dialled address); `func Discover(ctx context.Context, client *http.Client, issuer string) (*oidc.Provider, error)` with plain-text errors. httptest listens on loopback, so tests pass an allow-all policy plus `ts.Certificate()` as root; the loopback, metadata and link-local cases use `RefuseLocal` and are refused before any packet leaves.

- [ ] **Step 1: Write the failing tests**

Create `internal/sso/discovery_test.go`:

```go
package sso_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
)

func goodDoc(issuer string) map[string]any {
	return map[string]any{
		"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
		"jwks_uri": issuer + "/keys", "code_challenge_methods_supported": []string{"plain", "S256"},
	}
}

// tlsIssuer serves doc(issuer) as the discovery document of an httptest TLS server.
func tlsIssuer(t *testing.T, doc func(issuer string) map[string]any) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(doc(ts.URL))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// trusting is a guarded client that trusts httptest's certificate. httptest binds loopback, so
// tests that need a successful dial pass an allow-all policy.
func trusting(ts *httptest.Server, policy sso.AddrPolicy) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return sso.NewGuardedClient(policy, pool)
}

func allowAll(netip.Addr) error { return nil }

func TestDiscoverAcceptsAnExactHTTPSIssuer(t *testing.T) {
	ts := tlsIssuer(t, goodDoc)
	p, err := sso.Discover(context.Background(), trusting(ts, allowAll), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if p.Endpoint().AuthURL != ts.URL+"/authorize" {
		t.Fatalf("endpoint %+v", p.Endpoint())
	}
}

func TestDiscoverRefusals(t *testing.T) {
	good := tlsIssuer(t, goodDoc)
	wrongIssuer := tlsIssuer(t, func(string) map[string]any { return goodDoc("https://other.example") })
	noS256 := tlsIssuer(t, func(issuer string) map[string]any {
		d := goodDoc(issuer)
		delete(d, "code_challenge_methods_supported")
		return d
	})
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, good.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	for _, tc := range []struct {
		name, issuer string
		client       *http.Client
		want         string
	}{
		{"plain http", "http://" + good.Listener.Addr().String(), trusting(good, allowAll), "https://"},
		{"issuer mismatch", wrongIssuer.URL, trusting(wrongIssuer, allowAll), `"https://other.example"`},
		{"no S256", noS256.URL, trusting(noS256, allowAll), "S256"},
		{"redirect", redirect.URL, trusting(redirect, allowAll), "redirect"},
		{"loopback", good.URL, trusting(good, sso.RefuseLocal), "loopback or link-local"},
		{"cloud metadata", "https://169.254.169.254", trusting(good, sso.RefuseLocal), "loopback or link-local"},
		{"link-local v6", "https://[fe80::1]", trusting(good, sso.RefuseLocal), "loopback or link-local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sso.Discover(context.Background(), tc.client, tc.issuer)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// A discovery document may name endpoints elsewhere; the client refuses any that is not https.
func TestGuardedClientRefusesPlainHTTP(t *testing.T) {
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	if _, err := sso.NewGuardedClient(allowAll, nil).Get(plain.URL); err == nil {
		t.Fatal("an http:// request went through")
	}
}

func TestRefuseLocal(t *testing.T) {
	for addr, refused := range map[string]bool{
		"127.0.0.1": true, "::1": true, "169.254.169.254": true, "fe80::1": true, "::ffff:127.0.0.1": true,
		"0.0.0.0": true, "224.0.0.1": true,
		"10.0.0.5": false, "192.168.1.2": false, "172.16.0.1": false, "100.64.0.1": false, "203.0.113.7": false,
	} {
		if err := sso.RefuseLocal(netip.MustParseAddr(addr)); (err != nil) != refused {
			t.Errorf("%s: refused=%v, want %v", addr, err != nil, refused)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/sso/ -run 'TestDiscover|TestGuardedClient|TestRefuseLocal'`
Expected: build failure, `undefined: sso.Discover`.

- [ ] **Step 3: Implement**

Create `internal/sso/discovery.go`:

```go
package sso

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	// ErrAddressRefused is a dial to a loopback, link-local, unspecified or multicast address.
	ErrAddressRefused = errors.New("address refused")
	errRedirect       = errors.New("redirect refused")
	errNotHTTPS       = errors.New("not an https URL")
)

// AddrPolicy decides whether a resolved address may be dialled.
type AddrPolicy func(netip.Addr) error

// RefuseLocal admits public and private LAN addresses and refuses loopback, link-local (cloud
// metadata answers at 169.254.169.254), unspecified and multicast ones.
func RefuseLocal(a netip.Addr) error {
	a = a.Unmap()
	if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return fmt.Errorf("%w: %s", ErrAddressRefused, a)
	}
	return nil
}

// NewGuardedClient is the HTTP client for identity providers an admin typed in: HTTPS only, no
// redirects, no proxy, bodies capped at 1 MiB, and policy applied to the address actually dialled
// (after DNS), so a name that resolves to the metadata service is refused too. nil roots means
// the system pool.
func NewGuardedClient(policy AddrPolicy, roots *x509.CertPool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		a, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		return policy(a)
	}}
	transport := &http.Transport{
		DialContext:            dialer.DialContext,
		TLSClientConfig:        &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport:     httpsOnly{transport},
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
	}
}

// httpsOnly refuses any request that is not https, which covers endpoints a discovery document
// names as well as the issuer, and caps every response body.
type httpsOnly struct{ next http.RoundTripper }

func (h httpsOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return nil, errNotHTTPS
	}
	resp, err := h.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, 1<<20), resp.Body}
	return resp, nil
}

// Discover fetches issuer's discovery document through client and checks what sign-in relies
// on: an https issuer, the exact issuer string back, and PKCE S256. The error text is for the
// admin screen; it never carries the provider's response.
func Discover(ctx context.Context, client *http.Client, issuer string) (*oidc.Provider, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("the issuer must be an https:// URL with no user, query or fragment")
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, client), issuer)
	if err != nil {
		return nil, discoveryError(err)
	}
	var meta struct {
		Methods []string `json:"code_challenge_methods_supported"`
	}
	if err := p.Claims(&meta); err != nil || !slices.Contains(meta.Methods, "S256") {
		return nil, errors.New("the provider does not list S256 in code_challenge_methods_supported; KyCalendar requires PKCE S256")
	}
	return p, nil
}

func discoveryError(err error) error {
	var mismatch *oidc.IssuerMismatchError
	switch {
	case errors.As(err, &mismatch):
		return fmt.Errorf("the provider names its issuer %q; enter exactly that", mismatch.Discovered)
	case errors.Is(err, ErrAddressRefused):
		return errors.New("the issuer resolves to a loopback or link-local address, which is refused")
	case errors.Is(err, errRedirect):
		return errors.New("the provider answered with a redirect, which is refused; enter the final issuer URL")
	case errors.Is(err, errNotHTTPS):
		return errors.New("the provider pointed at a URL that is not https, which is refused")
	}
	return errors.New("could not read a discovery document at the issuer's /.well-known/openid-configuration")
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/sso/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/sso`

- [ ] **Step 5: DOX**

In `internal/sso/AGENTS.md` add:

```markdown
- `Discover` is what an admin-entered issuer must pass: https with no user, query or fragment; the discovery document names exactly that issuer; `S256` is in `code_challenge_methods_supported`. `NewGuardedClient` refuses non-https requests (endpoints a discovery document names included), redirects and proxies, caps bodies at 1 MiB, and applies its `AddrPolicy` to the address actually dialled, after DNS. `RefuseLocal` refuses loopback, link-local (cloud metadata), unspecified and multicast addresses and admits private LAN ones. Error text never carries the provider's response.
```

- [ ] **Step 6: Commit**

```bash
git add internal/sso/discovery.go internal/sso/discovery_test.go internal/sso/AGENTS.md
git commit -m "feat(sso): guarded OIDC discovery for admin-entered issuers" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 26: One swappable provider

**Files:**
- Create: `internal/sso/provider.go`, `internal/sso/provider_internal_test.go`
- Modify: `internal/sso/oauth.go` (client field; no issuer trimming), `internal/sso/kysignon.go` (webhook only), `internal/sso/sso_test.go:35-36`
- Create: `internal/api/signin.go`, `internal/api/sso_provider_internal_test.go`
- Modify: `internal/api/server.go` (imports, `Server`, `NewServer`), `internal/api/sso_handlers.go` (imports, `upsertSSOUser`, login, callback)
- Modify: `internal/sso/AGENTS.md`, `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: `Resolve`, `Settings.Live`, `NewGuardedClient`, `RefuseLocal` (Tasks 24-25).
- Produces: `type sso.Provider struct{Kind, DisplayName, ID string; …}`; `func NewProvider(kind, displayName, issuer, clientID, secret string, client *http.Client) *Provider` (new random `ID` per build); `(*Provider).AuthURL(ctx, redirectURI, state, verifier, nonce string) (string, error)`; `(*Provider).Exchange(ctx, code, verifier, redirectURI, nonce string) (*IdentityClaims, error)` (kyidentity → `Provider "kysignon"`, roles kept; oidc → `Provider "oidc"`, roles dropped); `newOAuthFlow(issuer, clientID, clientSecret string, client *http.Client)`; `KySignOnClient` loses `BuildAuthURL`/`ExchangeCode`. In `api`: fields `signin atomic.Pointer[sso.Provider]`, `signinHTTP *http.Client`; `func (s *Server) buildProvider(st sso.Settings) *sso.Provider`; the nonce cookie is `"<provider ID>.<nonce>"`; login with no provider is 404; `upsertSSOUser` grants admin only when `claims.Provider == "kysignon"`.

- [ ] **Step 1: Write the failing tests**

Create `internal/sso/provider_internal_test.go`:

```go
package sso

import (
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
)

// Only KyIdentity's roles can make an administrator: an oidc provider's roles claim is dropped.
func TestStampKeepsRolesForKyIdentityOnly(t *testing.T) {
	ky := (&Provider{Kind: KindKyIdentity}).stamp(&IdentityClaims{Subject: "s", Roles: []string{access.AdminAppRole}})
	if ky.Provider != "kysignon" || !access.IsAdmin(ky.Roles) {
		t.Fatalf("kyidentity: %+v", ky)
	}
	oidc := (&Provider{Kind: KindOIDC}).stamp(&IdentityClaims{Subject: "s", Roles: []string{access.AdminAppRole}})
	if oidc.Provider != "oidc" || oidc.Roles != nil {
		t.Fatalf("oidc: %+v", oidc)
	}
}
```

Create `internal/api/sso_provider_internal_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// A login started under one provider build fails once if a save swaps the provider before the
// callback, even to identical settings: the nonce cookie carries the build's ID.
func TestCallbackAfterProviderSwapFailsOnce(t *testing.T) {
	s, _ := davInternalServer(t)
	var idp *httptest.Server
	idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize",
			"token_endpoint": idp.URL + "/token", "jwks_uri": idp.URL + "/keys",
		})
	}))
	t.Cleanup(idp.Close)
	s.signin.Store(sso.NewProvider(sso.KindOIDC, "Acme", idp.URL, "kc", "", nil))
	login := httptest.NewRecorder()
	s.ServeHTTP(login, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	loc, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}

	s.signin.Store(sso.NewProvider(sso.KindOIDC, "Acme", idp.URL, "kc", "", nil))
	req := httptest.NewRequest("GET", "/api/sso/kysignon/callback?code=c&state="+loc.Query().Get("state"), nil)
	for _, c := range login.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "start again") {
		t.Fatalf("callback across a swap: %d %s", w.Code, w.Body.String())
	}

	s.signin.Store(nil)
	none := httptest.NewRecorder()
	s.ServeHTTP(none, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if none.Code != http.StatusNotFound {
		t.Fatalf("login with no provider: %d, want 404", none.Code)
	}
}

// An oidc provider never makes an administrator and never signs in as a SCIM row.
func TestOIDCLoginsAreEverydayAndNeverAdoptSCIM(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if err := s.store.Users().CreateUser(ctx, &store.User{ID: "usr_scim", Username: "sam", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "o-2"}); err != nil {
		t.Fatal(err)
	}
	u, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "oidc", Subject: "o-1", PreferredUsername: "olive", Roles: []string{access.AdminAppRole}})
	if err != nil || u.Role != "user" || u.SSOProvider != "oidc" {
		t.Fatalf("oidc login with the admin role: %+v %v, want an everyday oidc user", u, err)
	}
	u, err = s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "oidc", Subject: "o-2", PreferredUsername: "oscar"})
	if err != nil || u.ID == "usr_scim" {
		t.Fatalf("oidc sub matching a SCIM row: %+v %v, want a separate account", u, err)
	}
}
```

In `internal/sso/sso_test.go` `TestOAuthAuthorizationURLUsesDiscoveryAndPKCE`, replace the two lines that build and call the client with:

```go
	p := sso.NewProvider(sso.KindKyIdentity, "KyIdentity", issuer, "client", "", nil)
	authURL, err := p.AuthURL(context.Background(), "https://app.example/callback", "state", "verifier", "nonce")
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/sso/ -run 'TestStamp|TestOAuthAuthorizationURL'`
Expected: build failure, `undefined: Provider` / `undefined: sso.NewProvider`.

- [ ] **Step 3: The provider**

Create `internal/sso/provider.go`:

```go
package sso

import (
	"context"
	"net/http"

	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// Provider is the live sign-in provider. Saving sign-in settings replaces it whole; ID is new on
// every build, so a login started under one provider cannot finish under the next.
type Provider struct {
	Kind        string // KindKyIdentity or KindOIDC
	DisplayName string
	ID          string
	flow        *oauthFlow
}

// NewProvider builds a provider; a nil client uses the default transport.
func NewProvider(kind, displayName, issuer, clientID, secret string, client *http.Client) *Provider {
	return &Provider{Kind: kind, DisplayName: displayName, ID: crypto.RandomHex(8), flow: newOAuthFlow(issuer, clientID, secret, client)}
}

// AuthURL is the authorization request with PKCE S256 and the nonce.
func (p *Provider) AuthURL(ctx context.Context, redirectURI, state, verifier, nonce string) (string, error) {
	return p.flow.authCodeURL(ctx, redirectURI, state, verifier, nonce)
}

// Exchange redeems the code and returns the verified claims, labelled with the account provider.
func (p *Provider) Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (*IdentityClaims, error) {
	claims, err := p.flow.exchange(ctx, code, verifier, redirectURI, nonce)
	if err != nil {
		return nil, err
	}
	return p.stamp(claims), nil
}

// stamp sets the account provider. Only KyIdentity's roles mean anything here: an oidc
// provider's roles are dropped, so no outside IdP can make an administrator.
func (p *Provider) stamp(c *IdentityClaims) *IdentityClaims {
	if p.Kind == KindKyIdentity {
		c.Provider = "kysignon"
		return c
	}
	c.Provider, c.Roles = "oidc", nil
	return c
}
```

In `internal/sso/oauth.go`, replace `"strings"` with `"net/http"` in the imports, and replace the `oauthFlow` struct, `newOAuthFlow` and `getProvider` with:

```go
type oauthFlow struct {
	issuer       string
	clientID     string
	clientSecret string
	client       *http.Client // nil: the default transport
	mu           sync.Mutex
	provider     *oidc.Provider
}

func newOAuthFlow(issuer, clientID, clientSecret string, client *http.Client) *oauthFlow {
	return &oauthFlow{issuer: issuer, clientID: clientID, clientSecret: clientSecret, client: client}
}

// withClient routes discovery, the token exchange and key fetches through f.client.
func (f *oauthFlow) withClient(ctx context.Context) context.Context {
	if f.client == nil {
		return ctx
	}
	return oidc.ClientContext(ctx, f.client)
}

func (f *oauthFlow) getProvider(ctx context.Context) (*oidc.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provider != nil {
		return f.provider, nil
	}
	provider, err := oidc.NewProvider(f.withClient(ctx), f.issuer)
	if err != nil {
		return nil, err
	}
	f.provider = provider
	return provider, nil
}
```

and in `exchange` route the token exchange and the ID-token verification through the client:

```go
	token, err := config.Exchange(f.withClient(ctx), code, oauth2.VerifierOption(verifier))
```

```go
	idToken, err := provider.Verifier(&oidc.Config{ClientID: f.clientID}).Verify(f.withClient(ctx), rawIDToken)
```

In `internal/sso/kysignon.go`, replace the `KySignOnClient` struct, `NewKySignOnClient`, `BuildAuthURL` and `ExchangeCode` with:

```go
// KySignOnClient receives KyIdentity's signed directory webhooks. Sign-in is Provider's.
type KySignOnClient struct {
	config config.SSOConfig
	store  store.Store
}

func NewKySignOnClient(cfg config.SSOConfig, st store.Store) *KySignOnClient {
	return &KySignOnClient{config: cfg, store: st}
}
```

- [ ] **Step 4: The live provider in the API**

Create `internal/api/signin.go`:

```go
package api

import "github.com/Busnes-app/kycalendar/internal/sso"

// buildProvider is the live provider for st, or nil. An issuer the operator set in the
// environment keeps the default transport, as before this screen existed; one an admin typed
// in goes through the guarded client.
func (s *Server) buildProvider(st sso.Settings) *sso.Provider {
	if !st.Live() {
		return nil
	}
	client := s.signinHTTP
	if st.Issuer.Source == sso.SourceEnvironment {
		client = nil
	}
	return sso.NewProvider(st.Provider.Value, st.DisplayName.Value, st.Issuer.Value, st.ClientID.Value, st.Secret.Value, client)
}
```

In `internal/api/server.go`, add `"sync/atomic"` to the imports; add to `Server` after the `saml` field (then `gofmt -w internal/api/server.go`):

```go
	// signin is the live sign-in provider, nil when none is configured; saving sign-in
	// settings swaps it.
	signin atomic.Pointer[sso.Provider]
	// signinHTTP reaches admin-entered providers: HTTPS only, no redirects, no loopback or
	// link-local targets.
	signinHTTP *http.Client
```

and in `NewServer`, directly before `	s.routes()`:

```go
	s.signinHTTP = sso.NewGuardedClient(sso.RefuseLocal, nil)
	// The environment's provider until LoadSignIn adds the saved settings.
	s.signin.Store(s.buildProvider(sso.Resolve(cfg.SSO, nil)))
```

In `internal/api/sso_handlers.go`, add `"strings"` to the imports and replace the comment and opening of `upsertSSOUser`:

```go
// upsertSSOUser maps a verified login onto a local user. A KyIdentity login's admin grant
// follows the token's `roles` claim on every login; any other provider's users are everyday. A
// change revokes the user's sessions and app passwords first.
func (s *Server) upsertSSOUser(ctx context.Context, claims *sso.IdentityClaims) (*store.User, error) {
	role := "user"
	if claims.Provider == "kysignon" && access.IsAdmin(claims.Roles) {
		role = "admin"
	}
```

Replace the start of `handleKySignOnLogin`, through its `BuildAuthURL` error branch, with:

```go
func (s *Server) handleKySignOnLogin(w http.ResponseWriter, r *http.Request) {
	p := s.signin.Load()
	if p == nil {
		s.writeError(w, http.StatusNotFound, "Single sign-on is not configured")
		return
	}
	state := crypto.RandomHex(16)
	nonce := crypto.RandomHex(16)
	verifier := oauth2.GenerateVerifier()

	redirectURI := fmt.Sprintf("%s/api/sso/kysignon/callback", s.config.Server.AppURL)
	authURL, err := p.AuthURL(r.Context(), redirectURI, state, verifier, nonce)
	if err != nil {
		log.Printf("sso: %s authorization URL failed: %v", p.Kind, err)
		s.writeError(w, http.StatusBadGateway, "The sign-in provider could not be reached")
		return
	}
```

and the nonce cookie's opening with:

```go
	// The nonce cookie carries the provider's ID: a callback after a provider swap fails.
	http.SetCookie(w, &http.Cookie{
		Name:     "ky_nonce_" + state,
		Value:    p.ID + "." + nonce,
```

In `handleKySignOnCallback`, replace

```go
	redirectURI := fmt.Sprintf("%s/api/sso/kysignon/callback", s.config.Server.AppURL)
	claims, err := s.kysignon.ExchangeCode(r.Context(), code, verifier, redirectURI, nonceCookie.Value)
```

with

```go
	p := s.signin.Load()
	providerID, nonce, _ := strings.Cut(nonceCookie.Value, ".")
	if p == nil || providerID != p.ID || nonce == "" {
		s.writeError(w, http.StatusBadRequest, "Sign-in settings changed while you were signing in; start again")
		return
	}
	redirectURI := fmt.Sprintf("%s/api/sso/kysignon/callback", s.config.Server.AppURL)
	claims, err := p.Exchange(r.Context(), code, verifier, redirectURI, nonce)
```

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/sso/ && go test -count=1 ./internal/api/ -run 'TestCallbackAfterProviderSwapFailsOnce|TestOIDCLogins|TestUpsertSSOUser|TestSSODisabledRefusesKySignOnRoutes'`
Expected: `ok` twice.

- [ ] **Step 6: DOX**

In `internal/sso/AGENTS.md` add:

```markdown
- `Provider` is the one OIDC flow for both kinds; saving sign-in settings replaces it whole and every build has a new random `ID`. `Exchange` labels claims: a `kyidentity` login becomes provider `kysignon` with its `roles`; an `oidc` login becomes `oidc` with its roles dropped, so no outside IdP can make an administrator. A provider built with a client sends discovery, the token exchange and key fetches through it. `KySignOnClient` only verifies and applies the directory webhook.
```

In `internal/api/AGENTS.md`, replace ``KySignOn login, callback and sync are wrapped in `requireSSO`; `KY_SSO_ENABLED=false` answers 404 on all three.`` with:

```markdown
- KySignOn login, callback and sync are wrapped in `requireSSO`; `KY_SSO_ENABLED=false` answers 404 on all three. Login and callback use the live provider, `s.signin` (an `atomic.Pointer[sso.Provider]`): none configured is 404. The nonce cookie is `<provider ID>.<nonce>`, so a callback that started under a provider since replaced is 400 and the user starts again.
```

and in the `upsertSSOUser` bullet, replace `decides the admin grant from the \`roles\` claim at every KySignOn login (\`kycalendar.admin\` only)` with ``decides the admin grant from the `roles` claim at every login through the `kyidentity` provider (`claims.Provider == "kysignon"`, `kycalendar.admin` only); `oidc` logins are always everyday users``.

- [ ] **Step 7: Commit**

```bash
git add internal/sso/provider.go internal/sso/provider_internal_test.go internal/sso/oauth.go internal/sso/kysignon.go internal/sso/sso_test.go internal/sso/AGENTS.md internal/api/signin.go internal/api/sso_provider_internal_test.go internal/api/server.go internal/api/sso_handlers.go internal/api/AGENTS.md
git commit -m "feat(sso): one swappable sign-in provider; oidc logins never admin" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 27: Store: disable a provider's accounts

**Files:**
- Modify: `internal/store/store.go` (`UserStore`), `internal/store/sqlstore.go` (after `revokePasswordGrants`)
- Create: `internal/store/sso_accounts_test.go`
- Modify: `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `grantTables` (Task 15), `seedUsers`, `seedSession`.
- Produces: `DisableSSOAccounts(ctx context.Context, providers []string) (int, error)` (one transaction: grants of every account of those providers deleted, active ones set `inactive`; returns how many were deactivated; a second run returns 0); `CountSSOAccounts(ctx context.Context, providers []string) (int, error)` (active only); `func inList(values []string) (string, []any)`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/sso_accounts_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestDisableSSOAccounts(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ky := &store.User{ID: "usr_ky", Username: "ky", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"}
	sc := &store.User{ID: "usr_sc", Username: "sc", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s2"}
	oi := &store.User{ID: "usr_oi", Username: "oi", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "s3"}
	lo := &store.User{ID: "usr_lo", Username: "lo", Role: "admin", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, ky, sc, oi, lo)
	for _, u := range []*store.User{ky, sc, oi, lo} {
		seedSession(t, st, u)
	}
	providers := []string{"kysignon", "scim"}
	if n, err := st.Users().CountSSOAccounts(ctx, providers); err != nil || n != 2 {
		t.Fatalf("count: %d %v, want 2", n, err)
	}
	if n, err := st.Users().DisableSSOAccounts(ctx, providers); err != nil || n != 2 {
		t.Fatalf("disable: %d %v, want 2", n, err)
	}
	for _, u := range []*store.User{ky, sc, oi, lo} {
		got, _ := st.Users().GetUserByID(ctx, u.ID)
		_, sessErr := st.Sessions().GetSession(ctx, "tok_"+u.ID)
		aps, _ := st.AppPasswords().ListByUser(ctx, u.ID)
		hit := u == ky || u == sc
		if (got.Status == "inactive") != hit || errors.Is(sessErr, store.ErrNotFound) != hit || (len(aps) == 0) != hit {
			t.Errorf("%s: status %s, session %v, %d app passwords; disabled want %v", u.ID, got.Status, sessErr, len(aps), hit)
		}
	}
	if n, err := st.Users().DisableSSOAccounts(ctx, providers); err != nil || n != 0 {
		t.Fatalf("second run: %d %v, want 0", n, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -count=1 ./internal/store/ -run TestDisableSSOAccounts`
Expected: build failure, `st.Users().CountSSOAccounts undefined`.

- [ ] **Step 3: Implement**

In `internal/store/store.go`, add to `UserStore` after `SetStatus`:

```go
	// DisableSSOAccounts makes every active account whose sso_provider is in providers inactive
	// and deletes those accounts' sessions, MFA challenges, device pairings and app passwords, in
	// one transaction. It returns how many accounts it deactivated.
	DisableSSOAccounts(ctx context.Context, providers []string) (int, error)
	// CountSSOAccounts counts the active accounts DisableSSOAccounts would deactivate.
	CountSSOAccounts(ctx context.Context, providers []string) (int, error)
```

In `internal/store/sqlstore.go`, after `revokePasswordGrants`, add:

```go
// inList is "?, ?, ..." for values, with the values as query arguments.
func inList(values []string) (string, []any) {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", "), args
}

func (u *userStore) DisableSSOAccounts(ctx context.Context, providers []string) (int, error) {
	if len(providers) == 0 {
		return 0, nil
	}
	in, args := inList(providers)
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, table := range grantTables {
		if _, err := tx.ExecContext(ctx, u.store.rebind("DELETE FROM "+table+" WHERE user_id IN (SELECT id FROM users WHERE sso_provider IN ("+in+"))"), args...); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, u.store.rebind("UPDATE users SET status = 'inactive', updated_at = ? WHERE status = 'active' AND sso_provider IN ("+in+")"), append([]any{time.Now().UTC()}, args...)...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

func (u *userStore) CountSSOAccounts(ctx context.Context, providers []string) (int, error) {
	if len(providers) == 0 {
		return 0, nil
	}
	in, args := inList(providers)
	var n int
	err := u.store.db.QueryRowContext(ctx, u.store.rebind("SELECT COUNT(1) FROM users WHERE status = 'active' AND sso_provider IN ("+in+")"), args...).Scan(&n)
	return n, err
}
```

- [ ] **Step 4: Run the store tests**

Run: `go test -count=1 ./internal/store/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/store`

- [ ] **Step 5: DOX**

In `internal/store/AGENTS.md` add: ``- `DisableSSOAccounts` deactivates every active account of the given `sso_provider` values and deletes their grants in one transaction (idempotent; a second run disables 0); `CountSSOAccounts` is the number it would disable. Calendars are untouched.``

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/sqlstore.go internal/store/sso_accounts_test.go internal/store/AGENTS.md
git commit -m "feat(store): disable every account of one sign-in provider" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 28: Bind accounts to the provider

**Files:**
- Modify: `internal/api/signin.go` (imports; append), `cmd/server/main.go` (after `api.NewServer`, ~:140)
- Create: `internal/api/signin_internal_test.go`
- Modify: `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: `sso.Resolve`, `sso.OpenSecret`, `sso.AccountProviders`, `Settings.Identity`, `DisableSSOAccounts`, `CountSSOAccounts`, `buildProvider`.
- Produces: `func (s *Server) signInSettings(ctx) (sso.Settings, error)` (saved under environment, secret opened); `func (s *Server) LoadSignIn(ctx context.Context) error` (exported; `cmd/server` calls it once); `func (s *Server) bindAccounts(ctx, st sso.Settings) (prev string, disabled int, err error)`; `func (s *Server) pendingDisable(ctx, st) (int, error)`; `func (s *Server) boundTo(ctx) (string, error)`; `func bindDetails(prev string, st sso.Settings, n int) string`.

- [ ] **Step 1: Write the failing tests**

Create `internal/api/signin_internal_test.go`:

```go
package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func kyidentityAt(issuer string) sso.Settings {
	return sso.Settings{
		Provider: sso.Field{Value: sso.KindKyIdentity}, DisplayName: sso.Field{Value: "KyIdentity"},
		Issuer: sso.Field{Value: issuer}, ClientID: sso.Field{Value: "kc"},
	}
}

// After an issuer change, the new provider's colliding sub reaches the disabled account of the
// old one and is refused: never a takeover of its calendars.
func TestProviderChangeRefusesTheOldSubject(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	for _, u := range []*store.User{
		{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-1"},
		{ID: "usr_dave", Username: "dave", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-2"},
	} {
		if err := s.store.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if prev, n, err := s.bindAccounts(ctx, kyidentityAt("https://old.example")); err != nil || prev != "" || n != 0 {
		t.Fatalf("first binding: %q %d %v, want nothing disabled", prev, n, err)
	}
	prev, n, err := s.bindAccounts(ctx, kyidentityAt("https://new.example"))
	if err != nil || prev != "kyidentity https://old.example" || n != 2 {
		t.Fatalf("issuer change: %q %d %v", prev, n, err)
	}
	for _, sub := range []string{"sub-1", "sub-2"} {
		_, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Provider: "kysignon", Subject: sub, PreferredUsername: "intruder-" + sub})
		if !errors.Is(err, errAccountInactive) {
			t.Errorf("%s through the new provider: %v, want errAccountInactive", sub, err)
		}
	}
	if prev, n, err := s.bindAccounts(ctx, kyidentityAt("https://new.example")); err != nil || prev != "" || n != 0 {
		t.Fatalf("rebinding the same provider: %q %d %v, want a no-op", prev, n, err)
	}
}

func TestLoadSignInAppliesSavedSettingsUnderTheEnvironment(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	sealed, err := sso.SealSecret(s.config.Security.EncryptionKey, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		sso.KeyProvider: sso.KindOIDC, sso.KeyDisplayName: "Acme", sso.KeyIssuer: "https://acme.example",
		sso.KeyClientID: "kc", sso.KeySecretSealed: sealed,
	} {
		if err := s.store.Settings().SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if p := s.signin.Load(); p == nil || p.Kind != sso.KindOIDC || p.DisplayName != "Acme" {
		t.Fatalf("live provider %+v", p)
	}
	if err := s.store.Users().CreateUser(ctx, &store.User{ID: "usr_o", Username: "o", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "o-1"}); err != nil {
		t.Fatal(err)
	}

	// An operator sets KyIdentity in the environment and restarts: it wins, and the oidc
	// accounts are disabled at startup.
	s.config.SSO.KySignOnIssuer, s.config.SSO.KySignOnClientID = "https://id.example", "env-client"
	if err := s.LoadSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if p := s.signin.Load(); p == nil || p.Kind != sso.KindKyIdentity {
		t.Fatalf("live provider after the environment change %+v", p)
	}
	if u, _ := s.store.Users().GetUserByID(ctx, "usr_o"); u.Status != "inactive" {
		t.Fatalf("oidc account after the change: %s", u.Status)
	}

	// A secret sealed under another key cannot be opened: refuse rather than run without it.
	other, _ := sso.SealSecret(make([]byte, 32), "s3cret")
	if err := s.store.Settings().SetSetting(ctx, sso.KeySecretSealed, other); err != nil {
		t.Fatal(err)
	}
	s.config.SSO.KySignOnIssuer, s.config.SSO.KySignOnClientID = "", ""
	if err := s.LoadSignIn(ctx); err == nil {
		t.Fatal("a secret sealed under another key was accepted")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/api/ -run 'TestProviderChangeRefusesTheOldSubject|TestLoadSignIn'`
Expected: build failure, `s.bindAccounts undefined`.

- [ ] **Step 3: Implement**

In `internal/api/signin.go`, replace the import line with:

```go
import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)
```

and append:

```go
// signInSettings resolves the saved settings under the environment and opens a saved secret.
func (s *Server) signInSettings(ctx context.Context) (sso.Settings, error) {
	saved, err := s.store.Settings().GetAllSettings(ctx)
	if err != nil {
		return sso.Settings{}, err
	}
	st := sso.Resolve(s.config.SSO, saved)
	if st.Secret.Source == sso.SourceSaved {
		if st.Secret.Value, err = sso.OpenSecret(s.config.Security.EncryptionKey, saved[sso.KeySecretSealed]); err != nil {
			return sso.Settings{}, fmt.Errorf("open the saved client secret: %w", err)
		}
	}
	return st, nil
}

// LoadSignIn makes the saved sign-in settings live and binds accounts to the result, disabling
// the previous provider's accounts if the provider or issuer changed while the server was down
// (an environment edit). cmd/server calls it once at startup.
func (s *Server) LoadSignIn(ctx context.Context) error {
	st, err := s.signInSettings(ctx)
	if err != nil {
		return err
	}
	prev, n, err := s.bindAccounts(ctx, st)
	if err != nil {
		return err
	}
	if prev != "" {
		_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: "system", Action: "admin.signin_provider_change", Resource: st.Provider.Value, Details: bindDetails(prev, st, n)})
		log.Printf("[SSO] sign-in provider changed from %q to %q: %d accounts of the previous provider disabled", prev, st.Identity(), n)
	}
	s.signin.Store(s.buildProvider(st))
	return nil
}

func bindDetails(prev string, st sso.Settings, n int) string {
	return "from=" + strconv.Quote(prev) + " to=" + strconv.Quote(st.Identity()) + " disabled=" + strconv.Itoa(n)
}

// boundTo is the identity accounts are bound to, "" when nothing is bound yet.
func (s *Server) boundTo(ctx context.Context) (string, error) {
	bound, err := s.store.Settings().GetSetting(ctx, sso.KeyBound)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return bound, err
}

// pendingDisable is how many accounts binding to st would disable.
func (s *Server) pendingDisable(ctx context.Context, st sso.Settings) (int, error) {
	bound, err := s.boundTo(ctx)
	if err != nil || !st.Live() || bound == "" || bound == st.Identity() {
		return 0, err
	}
	kind, _, _ := strings.Cut(bound, " ")
	return s.store.Users().CountSSOAccounts(ctx, sso.AccountProviders(kind))
}

// bindAccounts makes st the provider SSO accounts belong to. On a change of kind or issuer the
// previous provider's accounts are disabled first, so a colliding sub from the new provider
// reaches a disabled account (403), never a takeover. The first binding disables nothing. It
// returns the previous identity ("" when nothing changed) and how many accounts it disabled.
// Running it twice is safe: the second run finds the binding already current.
func (s *Server) bindAccounts(ctx context.Context, st sso.Settings) (string, int, error) {
	if !st.Live() {
		return "", 0, nil
	}
	bound, err := s.boundTo(ctx)
	if err != nil || bound == st.Identity() {
		return "", 0, err
	}
	n := 0
	if bound != "" {
		kind, _, _ := strings.Cut(bound, " ")
		if n, err = s.store.Users().DisableSSOAccounts(ctx, sso.AccountProviders(kind)); err != nil {
			return "", 0, err
		}
	}
	if err := s.store.Settings().SetSetting(ctx, sso.KeyBound, st.Identity()); err != nil {
		return "", 0, err
	}
	return bound, n, nil
}
```

In `cmd/server/main.go` `runServer`, directly after `	srv := api.NewServer(cfg, st)`:

```go
	if err := srv.LoadSignIn(ctx); err != nil {
		log.Printf("[SSO] saved sign-in settings not applied: %v", err)
	}
```

- [ ] **Step 4: Run the tests**

Run: `go build ./... && go test -count=1 ./internal/api/ -run 'TestProviderChange|TestLoadSignIn|TestCallbackAfterProviderSwap|TestOIDCLogins'`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 5: DOX**

In `internal/api/AGENTS.md` add:

```markdown
- `LoadSignIn`, called once by `cmd/server` after `NewServer`, resolves the saved sign-in settings under the environment, opens the sealed secret and binds accounts. `bindAccounts` compares the provider's identity (`<kind> <issuer>`) with setting `signin_bound`; on a change it disables the previous provider's accounts (`kysignon` and `scim` for KyIdentity, `oidc` for oidc) before recording the new binding, so a colliding `sub` from the new provider reaches a disabled account (403), never a takeover. The first binding disables nothing; rerunning is a no-op. A secret that will not open leaves the environment's provider live and logs why.
```

- [ ] **Step 6: Commit**

```bash
git add internal/api/signin.go internal/api/signin_internal_test.go cmd/server/main.go internal/api/AGENTS.md
git commit -m "feat(api): bind SSO accounts to the provider; disable on a change" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 29: Sign-in admin routes

**Files:**
- Create: `internal/api/admin_signin.go`, `internal/api/admin_signin_test.go`
- Modify: `internal/api/server.go` (`signinMu`; `routes`), `internal/api/settings_handlers.go:19-53`, `internal/api/export_test.go`, `internal/api/authz_matrix_test.go`
- Modify: `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: `signInSettings`, `pendingDisable`, `bindAccounts`, `bindDetails`, `buildProvider`, `sso.Discover`, `sso.SealSecret`, `cleanName`, `requireStepUp`, `auditAction`, `codeOf`, `auditDetails`.
- Produces: `GET /api/admin/signin` → `signinView`; `POST /api/admin/signin/test` → 200 `{ok:true}`, 400, or 422 with the plain-text reason; `PUT /api/admin/signin` (step-up, detached, tracked) → 200 `signinView`, 409 `{code:"confirm_provider_change",count}` until `confirm_disable` equals `count`, 422 discovery refusal; public settings gain `signin_name` while a provider is live and SSO is enabled; `extra_settings` drops `signin_client_secret*`; `api.SetSignInHTTPClientForTest(s *Server, c *http.Client)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/api/admin_signin_test.go`:

```go
package api_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// tlsIdP is an httptest TLS server whose discovery document names itself as the issuer.
func tlsIdP(t *testing.T) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": ts.URL, "authorization_endpoint": ts.URL + "/authorize", "token_endpoint": ts.URL + "/token",
			"jwks_uri": ts.URL + "/keys", "code_challenge_methods_supported": []string{"S256"},
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// trustIdPs lets srv reach httptest TLS servers, which all share one certificate and listen on
// loopback (refused by the production policy).
func trustIdPs(srv *api.Server, ts *httptest.Server) {
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	api.SetSignInHTTPClientForTest(srv, sso.NewGuardedClient(func(netip.Addr) error { return nil }, pool))
}

func startLogin(t *testing.T, srv *api.Server) (*url.URL, []*http.Cookie) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc, w.Result().Cookies()
}

func saveSignIn(t *testing.T, srv *api.Server, admin *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, srv, "PUT", "/api/admin/signin", body, admin)
}

func oidcBody(issuer, extra string) string {
	return `{"provider":"oidc","display_name":"Acme","issuer":"` + issuer + `","client_id":"kc"` + extra + `}`
}

func TestSignInSaveSealsTheSecretAndSwapsLive(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	a, b := tlsIdP(t), tlsIdP(t)
	trustIdPs(srv, a)

	if w := call(t, srv, "GET", "/api/sso/kysignon/login", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("login with no provider: %d, want 404", w.Code)
	}
	if bytes.Contains(call(t, srv, "GET", "/api/settings", "", nil).Body.Bytes(), []byte("signin_name")) {
		t.Fatal("the login button is offered with no provider")
	}

	if w := saveSignIn(t, srv, admin, oidcBody(a.URL, `,"client_secret":"s3cret-value"`)); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	sealed, err := st.Settings().GetSetting(ctx, sso.KeySecretSealed)
	if err != nil || sealed == "" || strings.Contains(sealed, "s3cret") {
		t.Fatalf("stored secret %q %v, want it sealed", sealed, err)
	}
	if loc, _ := startLogin(t, srv); loc.Scheme+"://"+loc.Host != a.URL || loc.Path != "/authorize" || loc.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("login goes to %s", loc)
	}
	if w := call(t, srv, "GET", "/api/settings", "", nil); !bytes.Contains(w.Body.Bytes(), []byte(`"signin_name":"Acme"`)) {
		t.Fatalf("public settings: %s", w.Body.String())
	}
	for _, path := range []string{"/api/settings", "/api/admin/signin"} {
		body := call(t, srv, "GET", path, "", admin).Body.Bytes()
		if bytes.Contains(body, []byte("s3cret")) || bytes.Contains(body, []byte(sealed)) || bytes.Contains(body, []byte(sso.KeySecretSealed)) {
			t.Errorf("%s leaked the client secret: %s", path, body)
		}
	}
	if w := call(t, srv, "GET", "/api/admin/signin", "", admin); !bytes.Contains(w.Body.Bytes(), []byte(`"client_secret":{"set":true,"source":"saved"}`)) {
		t.Errorf("admin view: %s", w.Body.String())
	}

	// Swap to b with no restart; a blank secret keeps the sealed one.
	if w := saveSignIn(t, srv, admin, oidcBody(b.URL, "")); w.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", w.Code, w.Body.String())
	}
	if loc, _ := startLogin(t, srv); loc.Scheme+"://"+loc.Host != b.URL {
		t.Fatalf("after the swap the login goes to %s", loc)
	}
	if again, _ := st.Settings().GetSetting(ctx, sso.KeySecretSealed); again != sealed {
		t.Error("a blank secret replaced the stored one")
	}
	if n := len(auditDetails(t, st, "admin.signin_save")); n != 2 {
		t.Errorf("want 2 admin.signin_save rows, got %d", n)
	}

	cfg.SSO.Enabled = false
	if bytes.Contains(call(t, srv, "GET", "/api/settings", "", nil).Body.Bytes(), []byte("signin_name")) {
		t.Error("KY_SSO_ENABLED=false must hide the login button")
	}
}

func TestSignInProviderChangeNeedsConfirmationAndDisables(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	a, b := tlsIdP(t), tlsIdP(t)
	trustIdPs(srv, a)
	if w := saveSignIn(t, srv, admin, `{"provider":"kyidentity","issuer":"`+a.URL+`","client_id":"kc"}`); w.Code != http.StatusOK {
		t.Fatalf("save kyidentity: %d %s", w.Code, w.Body.String())
	}
	carol := &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-1"}
	if err := st.Users().CreateUser(ctx, carol); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: "ap_carol", UserID: carol.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}

	w := saveSignIn(t, srv, admin, oidcBody(b.URL, ""))
	var refusal struct {
		Code  string `json:"code"`
		Count int    `json:"count"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &refusal)
	if w.Code != http.StatusConflict || refusal.Code != "confirm_provider_change" || refusal.Count != 1 {
		t.Fatalf("unconfirmed change: %d %s", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.Status != "active" {
		t.Fatal("an unconfirmed change disabled an account")
	}

	if w := saveSignIn(t, srv, admin, oidcBody(b.URL, `,"confirm_disable":1`)); w.Code != http.StatusOK {
		t.Fatalf("confirmed change: %d %s", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.Status != "inactive" {
		t.Fatalf("previous provider's account still %s", u.Status)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, carol.ID); len(list) != 0 {
		t.Fatal("the disabled account kept its app passwords")
	}
	if _, err := st.Calendars().ListCalendarsByOwner(ctx, "user", carol.ID); err != nil {
		t.Fatalf("calendars must stay: %v", err)
	}
	if d := auditDetails(t, st, "admin.signin_provider_change"); len(d) != 1 || !strings.Contains(d[0], "disabled=1") {
		t.Fatalf("provider change audit: %v", d)
	}
}

func TestSignInEnvironmentLocksFields(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	a := tlsIdP(t)
	trustIdPs(srv, a)
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.KySignOnSecret = a.URL, "env-client", "env-secret"

	w := call(t, srv, "GET", "/api/admin/signin", "", admin)
	for _, want := range []string{
		`"provider":{"value":"kyidentity","source":"environment"}`,
		`"issuer":{"value":"` + a.URL + `","source":"environment"}`,
		`"client_secret":{"set":true,"source":"environment"}`,
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("admin view lacks %s: %s", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "env-secret") {
		t.Fatal("the admin view leaked the environment secret")
	}

	body := `{"provider":"oidc","display_name":"Mine","issuer":"https://evil.example","client_id":"evil","client_secret":"evil-secret"}`
	if w := saveSignIn(t, srv, admin, body); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	for _, key := range []string{sso.KeyProvider, sso.KeyIssuer, sso.KeyClientID, sso.KeySecretSealed} {
		if v, err := st.Settings().GetSetting(ctx, key); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s saved as %q over the environment", key, v)
		}
	}
	if v, _ := st.Settings().GetSetting(ctx, sso.KeyDisplayName); v != "Mine" {
		t.Errorf("the unlocked label was not saved: %q", v)
	}
}

func TestSignInSaveNeedsStepUp(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	restore := api.SetStepUpWindowForTest(0)
	defer restore()
	w := saveSignIn(t, srv, admin, `{"provider":"none"}`)
	if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "reauth_required" {
		t.Fatalf("stale session: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Settings().GetSetting(context.Background(), sso.KeyProvider); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a refused save stored settings")
	}
}

// The test route uses the production policy here: httptest listens on loopback, so it is refused.
func TestSignInTestRouteRefusesAndSavesNothing(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	a := tlsIdP(t)
	for _, tc := range []struct {
		body string
		code int
		want string
	}{
		{oidcBody(a.URL, ""), http.StatusUnprocessableEntity, "loopback"},
		{oidcBody("http://idp.example", ""), http.StatusUnprocessableEntity, "https://"},
		{`{"provider":"oidc","display_name":"Acme","issuer":"https://idp.example"}`, http.StatusBadRequest, "client ID"},
		{`{"provider":"none"}`, http.StatusBadRequest, "Choose a provider"},
	} {
		w := call(t, srv, "POST", "/api/admin/signin/test", tc.body, admin)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want %d mentioning %q", tc.body, w.Code, w.Body.String(), tc.code, tc.want)
		}
	}
	trustIdPs(srv, a)
	if w := call(t, srv, "POST", "/api/admin/signin/test", oidcBody(a.URL, ""), admin); w.Code != http.StatusOK {
		t.Fatalf("reachable provider: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Settings().GetSetting(context.Background(), sso.KeyIssuer); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the test route saved settings")
	}
	if n := len(auditDetails(t, st, "admin.signin_test")); n != 3 {
		t.Errorf("want 3 admin.signin_test rows (two refusals, one success), got %d", n)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 ./internal/api/ -run TestSignIn`
Expected: build failure, `undefined: api.SetSignInHTTPClientForTest`.

- [ ] **Step 3: Implement the routes**

Create `internal/api/admin_signin.go`:

```go
package api

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/Busnes-app/kycalendar/internal/sso"
)

type signinRequest struct {
	Provider     string `json:"provider"`
	DisplayName  string `json:"display_name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// ConfirmDisable must equal the number of previous-provider accounts a provider change
	// disables; the screen learns it from a 409 confirm_provider_change.
	ConfirmDisable int `json:"confirm_disable"`
}

type secretView struct {
	Set    bool   `json:"set"`
	Source string `json:"source"`
}

// signinView never carries the secret, only whether one is set and where it comes from.
type signinView struct {
	Provider     sso.Field  `json:"provider"`
	DisplayName  sso.Field  `json:"display_name"`
	Issuer       sso.Field  `json:"issuer"`
	ClientID     sso.Field  `json:"client_id"`
	ClientSecret secretView `json:"client_secret"`
	CallbackURL  string     `json:"callback_url"`
	SSOEnabled   bool       `json:"sso_enabled"`
	Live         bool       `json:"live"`
}

func (s *Server) signinViewOf(st sso.Settings) signinView {
	return signinView{
		Provider: st.Provider, DisplayName: st.DisplayName, Issuer: st.Issuer, ClientID: st.ClientID,
		ClientSecret: secretView{Set: st.Secret.Source != sso.SourceUnset, Source: st.Secret.Source},
		CallbackURL:  s.config.Server.AppURL + "/api/sso/kysignon/callback",
		SSOEnabled:   s.config.SSO.Enabled,
		Live:         s.signin.Load() != nil,
	}
}

func (s *Server) handleGetSignIn(w http.ResponseWriter, r *http.Request) {
	saved, err := s.store.Settings().GetAllSettings(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load sign-in settings")
		return
	}
	s.writeJSON(w, http.StatusOK, s.signinViewOf(sso.Resolve(s.config.SSO, saved)))
}

// submitted is the request resolved as if saved, under the environment: a field the
// environment sets keeps its value whatever the request says. A blank secret keeps the stored one.
func (s *Server) submitted(ctx context.Context, req signinRequest) (sso.Settings, error) {
	saved, err := s.store.Settings().GetAllSettings(ctx)
	if err != nil {
		return sso.Settings{}, err
	}
	st := sso.Resolve(s.config.SSO, map[string]string{
		sso.KeyProvider:    strings.TrimSpace(req.Provider),
		sso.KeyDisplayName: strings.TrimSpace(req.DisplayName),
		sso.KeyIssuer:      strings.TrimSpace(req.Issuer),
		sso.KeyClientID:    strings.TrimSpace(req.ClientID),
		// Only presence matters here: Resolve reports a saved secret without its value.
		sso.KeySecretSealed: cmp.Or(req.ClientSecret, saved[sso.KeySecretSealed]),
	})
	if st.Secret.Source == sso.SourceSaved {
		if req.ClientSecret != "" {
			st.Secret.Value = req.ClientSecret
		} else if st.Secret.Value, err = sso.OpenSecret(s.config.Security.EncryptionKey, saved[sso.KeySecretSealed]); err != nil {
			return sso.Settings{}, fmt.Errorf("open the saved client secret: %w", err)
		}
	}
	return st, nil
}

// signinProblem is what is wrong with st, "" when nothing is.
func signinProblem(st sso.Settings) string {
	switch st.Provider.Value {
	case sso.KindNone:
		return ""
	case sso.KindKyIdentity, sso.KindOIDC:
	default:
		return "Provider must be none, kyidentity or oidc"
	}
	if st.Issuer.Value == "" || st.ClientID.Value == "" {
		return "The issuer and the client ID are required"
	}
	if _, ok := cleanName(st.DisplayName.Value); !ok {
		return "The button label must be 1-255 characters with no control characters"
	}
	if len(st.Issuer.Value) > 2048 || len(st.ClientID.Value) > 255 || strings.IndexFunc(st.ClientID.Value, unicode.IsControl) >= 0 {
		return "The issuer or the client ID is not valid"
	}
	if len(st.Secret.Value) > 1024 {
		return "The client secret is too long"
	}
	return ""
}

// handleTestSignIn runs discovery for the submitted values without saving anything.
func (s *Server) handleTestSignIn(w http.ResponseWriter, r *http.Request) {
	var req signinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	st, err := s.submitted(r.Context(), req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load sign-in settings")
		return
	}
	msg := signinProblem(st)
	if msg == "" && st.Provider.Value == sso.KindNone {
		msg = "Choose a provider to test"
	}
	if msg != "" {
		s.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, err := sso.Discover(r.Context(), s.signinHTTP, st.Issuer.Value); err != nil {
		s.auditAction(r.Context(), r, "admin.signin_test", st.Provider.Value, "outcome=failure")
		s.writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.auditAction(r.Context(), r, "admin.signin_test", st.Provider.Value, "outcome=success")
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSaveSignIn tests, saves and makes the settings live with no restart. A provider or
// issuer change disables the previous provider's accounts, after the admin confirms the count.
func (s *Server) handleSaveSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "change sign-in settings") {
		return
	}
	var req signinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	s.signinMu.Lock()
	defer s.signinMu.Unlock()

	st, err := s.submitted(ctx, req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load sign-in settings")
		return
	}
	if msg := signinProblem(st); msg != "" {
		s.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if st.Provider.Value != sso.KindNone {
		if _, err := sso.Discover(ctx, s.signinHTTP, st.Issuer.Value); err != nil {
			s.auditAction(ctx, r, "admin.signin_save", st.Provider.Value, "outcome=failure")
			s.writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	n, err := s.pendingDisable(ctx, st)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count accounts")
		return
	}
	if n > 0 && req.ConfirmDisable != n {
		s.writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("Saving disables %d accounts of the previous provider", n),
			"code":  "confirm_provider_change", "count": n,
		})
		return
	}
	if err := s.saveSignIn(ctx, st, req.ClientSecret); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save sign-in settings")
		return
	}
	prev, disabled, err := s.bindAccounts(ctx, st)
	if err != nil {
		// Saved but not live: LoadSignIn binds at the next start.
		s.writeError(w, http.StatusInternalServerError, "Saved, but the previous provider's accounts could not be disabled; restart the server to finish")
		return
	}
	if prev != "" {
		s.auditAction(ctx, r, "admin.signin_provider_change", st.Provider.Value, bindDetails(prev, st, disabled))
	}
	s.signin.Store(s.buildProvider(st))
	s.auditAction(ctx, r, "admin.signin_save", st.Provider.Value, "outcome=success")
	s.writeJSON(w, http.StatusOK, s.signinViewOf(st))
}

// saveSignIn stores every field the environment does not set; a blank secret keeps the sealed one.
func (s *Server) saveSignIn(ctx context.Context, st sso.Settings, secret string) error {
	set := s.store.Settings().SetSetting
	for key, f := range map[string]sso.Field{sso.KeyProvider: st.Provider, sso.KeyDisplayName: st.DisplayName, sso.KeyIssuer: st.Issuer, sso.KeyClientID: st.ClientID} {
		if f.Source == sso.SourceEnvironment {
			continue
		}
		if err := set(ctx, key, f.Value); err != nil {
			return err
		}
	}
	if secret == "" || st.Secret.Source == sso.SourceEnvironment {
		return nil
	}
	sealed, err := sso.SealSecret(s.config.Security.EncryptionKey, secret)
	if err != nil {
		return err
	}
	return set(ctx, sso.KeySecretSealed, sealed)
}
```

In `internal/api/server.go`, add to `Server` after `signin` (then `gofmt -w internal/api/server.go`):

```go
	signinMu sync.Mutex // one sign-in save at a time
```

and in `routes()`, after the `GET /api/admin/audit` line:

```go
	s.handle("GET /api/admin/signin", s.requireAdmin(s.handleGetSignIn))
	s.handle("POST /api/admin/signin/test", s.requireAdmin(s.handleTestSignIn))
	s.handle("PUT /api/admin/signin", s.tracked(s.requireAdmin(s.handleSaveSignIn)))
```

In `internal/api/export_test.go`, add `"net/http"` to the imports and append:

```go
// SetSignInHTTPClientForTest replaces the guarded client that reaches admin-entered providers,
// so a test can trust httptest's certificate and dial its loopback listener. Test-only.
func SetSignInHTTPClientForTest(s *Server, c *http.Client) { s.signinHTTP = c }
```

- [ ] **Step 4: Public settings**

In `internal/api/settings_handlers.go` `handleGetSettings`, after the `out := map[string]any{…}` literal add:

```go
	// The login button shows only for a configured provider with SSO enabled.
	if p := s.signin.Load(); p != nil && s.config.SSO.Enabled {
		out["signin_name"] = p.DisplayName
	}
```

and change the filter loop's condition to:

```go
			if strings.HasPrefix(k, "kyrecovery_token") || strings.HasPrefix(k, "signin_client_secret") {
```

with its comment ending `…must not leak by default. The sign-in client secret never leaves either.`

- [ ] **Step 5: Matrix rows**

In `apiRows` (then `gofmt -w internal/api/authz_matrix_test.go`):

```go
		"GET /api/admin/signin": {method: "GET", path: "/api/admin/signin", want: adminOnly},
		// http:// is refused before any request, so the admin cell is a 422 without the network.
		"POST /api/admin/signin/test": {method: "POST", path: "/api/admin/signin/test", body: `{"provider":"oidc","display_name":"M","issuer":"http://idp.invalid","client_id":"m"}`, want: adminOnly},
		"PUT /api/admin/signin":       {method: "PUT", path: "/api/admin/signin", body: `{"provider":"none"}`, want: adminOnly},
```

- [ ] **Step 6: Run the API suite**

Run: `go test -count=1 -p 1 ./internal/api/`
Expected: `ok  	github.com/Busnes-app/kycalendar/internal/api`

- [ ] **Step 7: DOX**

In `internal/api/AGENTS.md`, add to the admin route table:

```markdown
| GET | `/api/admin/signin` | admin | `provider`, `display_name`, `issuer`, `client_id` each `{value,source}` (`environment`, `saved`, `unset`); `client_secret:{set,source}`, never the value; `callback_url`, `sso_enabled`, `live` |
| POST | `/api/admin/signin/test` | admin | the PUT body; discovery only, nothing saved -> 200 `{ok}`; 400 invalid; 422 plain-text refusal |
| PUT | `/api/admin/signin` | admin + step-up; detached, tracked | `{provider,display_name,issuer,client_id,client_secret?,confirm_disable?}`; environment-locked fields are ignored and a blank secret keeps the stored one; 422 discovery refusal; 409 `confirm_provider_change` `{count}` until `confirm_disable` equals it; 200 the GET view after the live swap |
```

and change the `GET /api/settings` bullet to: ``…public fields for the login screen (`signin_name`, the live provider's button label, only while SSO is enabled), `db_driver`/`scim_enabled` for any session, and `extra_settings` for admins only; KyRecovery tokens and the sign-in client secret are omitted, sealed or not, by the `kyrecovery_token` and `signin_client_secret` key prefixes.``

- [ ] **Step 8: Commit**

```bash
git add internal/api/admin_signin.go internal/api/admin_signin_test.go internal/api/server.go internal/api/settings_handlers.go internal/api/export_test.go internal/api/authz_matrix_test.go internal/api/AGENTS.md
git commit -m "feat(api): sign-in settings routes with live swap and confirmed provider change" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 30: The login button only when a provider is configured

**Files:**
- Modify: `web/src/pages/Login.tsx:6-9,15,212-229`, `web/src/App.tsx` (`<Login …>`)
- Create: `web/src/pages/Login.test.tsx`
- Modify: `web/AGENTS.md`; rebuilt `web/dist`

**Interfaces:**
- Consumes: `signin_name` from `GET /api/settings` (Task 29).
- Produces: `Login` prop `signinName?: string`; the link `Continue with <signinName>` to `/api/sso/kysignon/login` renders only when it is set.

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/Login.test.tsx`:

```tsx
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../components/CaptchaWidget', () => ({ CaptchaWidget: () => null }));

import { Login } from './Login';

afterEach(cleanup);

describe('Login', () => {
  it('offers single sign-on only when a provider is configured', () => {
    const { rerender } = render(<Login appName="Cal" onSuccess={() => {}} />);
    expect(screen.queryByRole('link', { name: /Continue with/ })).toBeNull();
    rerender(<Login appName="Cal" onSuccess={() => {}} signinName="Acme" />);
    expect(screen.getByRole('link', { name: 'Continue with Acme' }).getAttribute('href')).toBe('/api/sso/kysignon/login');
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/pages/Login.test.tsx`
Expected: FAIL, `Unable to find an accessible element with the role "link" and name "Continue with Acme"`.

- [ ] **Step 3: Implement**

In `web/src/pages/Login.tsx`, add the prop:

```tsx
interface LoginProps {
  onSuccess: (user: any) => void;
  appName: string;
  /** The configured provider's button label; no button without one. */
  signinName?: string;
}
```

destructure it (`({ onSuccess, appName, signinName })`), and replace the whole single sign-on block under the CAPTCHA and Sign In button (the `<div style={{ marginTop: '20px', … }}>` holding "Or continue with Single Sign-On" and the `KySignOn Identity` link) with:

```tsx
              {signinName && (
                <div style={{ marginTop: '20px', borderTop: '1px solid var(--line)', paddingTop: '16px' }}>
                  <a
                    href="/api/sso/kysignon/login"
                    className="btn btn-secondary"
                    style={{ width: '100%', justifyContent: 'center', textDecoration: 'none' }}
                  >
                    <Key size={16} style={{ color: 'var(--accent)' }} />
                    <span>Continue with {signinName}</span>
                  </a>
                </div>
              )}
```

In `web/src/App.tsx`, pass it: add `        signinName={settings?.signin_name}` after `appName={…}` in `<Login …>`.

- [ ] **Step 4: Run the web suite and build**

Run: `cd web && npm test && npm run build`
Expected: every test file passes; the build rewrites `web/dist`.

- [ ] **Step 5: DOX**

In `web/AGENTS.md` add: ``- `Login.tsx` shows "Continue with <label>" only when `/api/settings` carries `signin_name` (a live provider with SSO enabled); there is no SSO button otherwise.``

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/Login.tsx web/src/pages/Login.test.tsx web/src/App.tsx web/AGENTS.md web/dist
git commit -m "feat(web): show the single sign-on button only for a configured provider" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 31: Sign-in page

**Files:**
- Create: `web/src/pages/SignIn.tsx`, `web/src/pages/SignIn.test.tsx`
- Modify: `web/src/App.tsx`, `web/src/App.test.tsx`, `web/src/components/AppHeader.tsx`, `web/src/pages/Dashboard.tsx`
- Modify: `README.md`, `AGENTS.md` (root), `web/AGENTS.md`; rebuilt `web/dist`

**Interfaces:**
- Consumes: `send`, `failure`, `JSON_HEADERS`, `REAUTH`; the routes from Task 29.
- Produces: default export `SignIn()`; tab `signin`; fields locked (disabled, with the variable named) when their source is `environment`; the provider-change confirm states the count and resends with `confirm_disable`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/pages/SignIn.test.tsx`:

```tsx
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import SignIn from "./SignIn";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockFetch(handlers: Record<string, (init?: RequestInit) => unknown>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const field = (value: string, source = "saved") => ({ value, source });
const saved = {
  provider: field("kyidentity"),
  display_name: field("KyIdentity"),
  issuer: field("https://id.example"),
  client_id: field("kc"),
  client_secret: { set: true, source: "saved" },
  callback_url: "https://cal.example/api/sso/kysignon/callback",
  sso_enabled: true,
  live: true,
};

describe("SignIn", () => {
  it("locks the fields the environment sets", async () => {
    mockFetch({
      "GET /api/admin/signin": () => ({
        ...saved,
        provider: field("kyidentity", "environment"),
        issuer: field("https://id.example", "environment"),
        client_id: field("kc", "environment"),
        client_secret: { set: true, source: "environment" },
      }),
    });
    render(<SignIn />);
    expect((await screen.findByLabelText<HTMLSelectElement>("Provider")).disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Issuer URL").disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Client ID").disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Client secret").disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Button label").disabled).toBe(false);
    expect(screen.getByText(/KY_KYSIGNON_ISSUER/)).toBeTruthy();
    expect(screen.getByText("https://cal.example/api/sso/kysignon/callback")).toBeTruthy();
  });

  it("keeps a stored secret unless a new one is typed", async () => {
    let body: Record<string, unknown> = {};
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "PUT /api/admin/signin": (init) => {
        body = JSON.parse(String(init?.body));
        return saved;
      },
    });
    render(<SignIn />);
    expect((await screen.findByLabelText<HTMLInputElement>("Client secret")).placeholder).toMatch(/leave blank to keep/);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(body.client_secret).toBe(""));
  });

  it("states how many accounts a provider change disables before saving", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const bodies: Record<string, unknown>[] = [];
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "PUT /api/admin/signin": (init) => {
        bodies.push(JSON.parse(String(init?.body)));
        if (bodies.length === 1) {
          return new Response(JSON.stringify({ error: "Saving disables 3 accounts", code: "confirm_provider_change", count: 3 }), { status: 409 });
        }
        return { ...saved, provider: field("oidc"), display_name: field("Acme") };
      },
    });
    render(<SignIn />);
    fireEvent.change(await screen.findByLabelText("Provider"), { target: { value: "oidc" } });
    fireEvent.change(screen.getByLabelText("Button label"), { target: { value: "Acme" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect((await screen.findByRole("status")).textContent).toMatch(/Continue with Acme/);
    expect(confirm.mock.calls[0][0]).toMatch(/disables 3 accounts/);
    expect(bodies.map((b) => b.confirm_disable)).toEqual([0, 3]);
  });

  it("asks for a fresh sign-in when saving needs step-up", async () => {
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "PUT /api/admin/signin": () => new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<SignIn />);
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("shows why a test failed", async () => {
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "POST /api/admin/signin/test": () =>
        new Response(JSON.stringify({ error: "the issuer resolves to a loopback or link-local address, which is refused" }), { status: 422 }),
    });
    render(<SignIn />);
    fireEvent.click(await screen.findByRole("button", { name: "Test" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/loopback or link-local/);
  });
});
```

In `web/src/App.test.tsx`, add `'Sign-in'` to the label list in `keeps admin pages from everyday users`.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/pages/SignIn.test.tsx`
Expected: FAIL, `Failed to resolve import "./SignIn"`.

- [ ] **Step 3: The page**

Create `web/src/pages/SignIn.tsx`:

```tsx
import { useEffect, useState } from "react";
import { JSON_HEADERS, REAUTH, failure, send } from "../admin";

interface Field {
  value: string;
  source: "environment" | "saved" | "unset";
}

interface View {
  provider: Field;
  display_name: Field;
  issuer: Field;
  client_id: Field;
  client_secret: { set: boolean; source: string };
  callback_url: string;
  sso_enabled: boolean;
  live: boolean;
}

interface Form {
  provider: string;
  display_name: string;
  issuer: string;
  client_id: string;
  client_secret: string;
}

function formOf(v: View): Form {
  return { provider: v.provider.value, display_name: v.display_name.value, issuer: v.issuer.value, client_id: v.client_id.value, client_secret: "" };
}

function EnvNote({ name }: { name: string }) {
  return <small>Set by the environment ({name}); change it there.</small>;
}

export default function SignIn() {
  const [view, setView] = useState<View | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      const res = await send("/api/admin/signin");
      if (!res?.ok) {
        setError("Could not load the sign-in settings. Reload the page to try again.");
        return;
      }
      const v: View = await res.json();
      setView(v);
      setForm(formOf(v));
    })();
  }, []);

  if (!view || !form) {
    return (
      <section className="page">
        <h1>Sign-in</h1>
        {error && <p role="alert">{error}</p>}
      </section>
    );
  }

  const env = (f: Field) => f.source === "environment";
  const secretLocked = view.client_secret.source === "environment";
  const set = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });

  async function test() {
    setError(null);
    setNotice(null);
    const res = await send("/api/admin/signin/test", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify(form) });
    if (!res?.ok) {
      setError((await failure(res)).error ?? "The test failed");
      return;
    }
    setNotice("The provider answered correctly. Save to use it.");
  }

  async function save(confirmed = 0) {
    setError(null);
    setNotice(null);
    const res = await send("/api/admin/signin", { method: "PUT", headers: JSON_HEADERS, body: JSON.stringify({ ...form, confirm_disable: confirmed }) });
    if (!res?.ok) {
      const f = await failure(res);
      if (f.code === "confirm_provider_change" && f.count !== undefined) {
        const accounts = f.count === 1 ? "1 account" : `${f.count} accounts`;
        if (window.confirm(`Switching provider disables ${accounts} from the previous provider. They keep their calendars but cannot sign in again. Continue?`)) {
          await save(f.count);
        }
        return;
      }
      setError(f.code === "reauth_required" ? REAUTH : (f.error ?? "Could not save the sign-in settings"));
      return;
    }
    const v: View = await res.json();
    setView(v);
    setForm(formOf(v));
    setNotice(v.live ? `Saved. The login page now offers “Continue with ${v.display_name.value}”.` : "Saved. Single sign-on is off.");
  }

  return (
    <section className="page">
      <h1>Sign-in</h1>
      <p>
        Let people sign in with KyIdentity or another OpenID Connect provider. Only KyIdentity's <code>kycalendar.admin</code>{" "}
        role makes an administrator; everyone from another provider is an everyday user. Switching provider or issuer disables
        the previous provider's accounts.
      </p>
      {!view.sso_enabled && <p role="status">KY_SSO_ENABLED=false: single sign-on stays off whatever you save here.</p>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <label htmlFor="signin-provider">Provider</label>
        <select id="signin-provider" value={form.provider} onChange={set("provider")} disabled={env(view.provider)}>
          <option value="none">None (passwords only)</option>
          <option value="kyidentity">KyIdentity</option>
          <option value="oidc">Another OpenID Connect provider</option>
        </select>
        {env(view.provider) && <EnvNote name="KY_KYSIGNON_*" />}
        {form.provider !== "none" && (
          <>
            <label htmlFor="signin-name">Button label</label>
            <input id="signin-name" value={form.display_name} onChange={set("display_name")} maxLength={255} required={form.provider === "oidc"} />
            <label htmlFor="signin-issuer">Issuer URL</label>
            <input
              id="signin-issuer"
              type="url"
              value={form.issuer}
              onChange={set("issuer")}
              disabled={env(view.issuer)}
              placeholder="https://id.example.com"
              required
            />
            {env(view.issuer) && <EnvNote name="KY_KYSIGNON_ISSUER" />}
            <label htmlFor="signin-client">Client ID</label>
            <input id="signin-client" value={form.client_id} onChange={set("client_id")} disabled={env(view.client_id)} required />
            {env(view.client_id) && <EnvNote name="KY_KYSIGNON_CLIENT_ID" />}
            <label htmlFor="signin-secret">Client secret</label>
            <input
              id="signin-secret"
              type="password"
              autoComplete="off"
              value={form.client_secret}
              onChange={set("client_secret")}
              disabled={secretLocked}
              placeholder={view.client_secret.set ? "Stored; leave blank to keep it" : ""}
            />
            {secretLocked && <EnvNote name="KY_KYSIGNON_SECRET" />}
            <p>
              Redirect URI to register with the provider: <code>{view.callback_url}</code>
            </p>
            <button type="button" onClick={() => void test()}>
              Test
            </button>
          </>
        )}
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
        <button type="submit">Save</button>
      </form>
    </section>
  );
}
```

- [ ] **Step 4: Navigation, page and card**

`web/src/components/AppHeader.tsx`: add `KeyRound` to the `lucide-react` import and, in the admin list after Group calendars, `{ id: 'signin', label: 'Sign-in', icon: KeyRound },`.

`web/src/App.tsx`: add `import SignIn from './pages/SignIn';` after the `People` import, and after the admin `group-calendars` line:

```tsx
        {isAdmin && activeTab === 'signin' && <SignIn />}
```

`web/src/pages/Dashboard.tsx`: replace the `Single Sign-On & Federation` card with:

```tsx
      title: 'Sign-in',
      desc: 'KyIdentity or any OpenID Connect provider, set here or locked by environment variables.',
      status: settings?.signin_name ? `Continue with ${settings.signin_name}` : 'Passwords only',
      statusType: settings?.signin_name ? 'success' : 'neutral',
      icon: Key,
      action: () => onNavigate('signin'),
      actionLabel: 'Sign-in settings',
```

- [ ] **Step 5: Run the web suite and build**

Run: `cd web && npm test && npm run build`
Expected: every test file passes; the build rewrites `web/dist`.

- [ ] **Step 6: DOX and README**

In `web/AGENTS.md`, add `signin` to the `App.tsx` admin tab list and add:

```markdown
- `src/pages/SignIn.tsx` is admin-only (tab `signin`): provider (none, KyIdentity, another OIDC provider), button label, issuer, client ID and secret, with the callback URL to copy. A field whose source is `environment` is disabled and names its variable; a stored secret shows only as a placeholder, and a blank one keeps it. Test runs discovery without saving; Save asks for confirmation with the server's count when a provider change disables accounts, and a 403 `reauth_required` asks for a fresh sign-in.
```

In the root `AGENTS.md`, append to `#### Standalone administration contracts`:

```markdown
- Sign-in: one provider, `none`, `kyidentity` or `oidc`, from settings `signin_*` under the environment (`KY_KYSIGNON_ISSUER`, `_CLIENT_ID`, `_SECRET` lock their fields; any `KY_KYSIGNON_*` fixes the provider to `kyidentity`; `KY_SSO_ENABLED=false` stays the kill switch). The client secret is stored only as `signin_client_secret_sealed`, AES-GCM under `crypto.DeriveKey(encryptionKey, "kycalendar:setting:signin_client_secret")`, and never returned; `extra_settings` drops every `signin_client_secret*` key. Test and Save run discovery through the guarded client (HTTPS, exact issuer, S256; no redirects, loopback or link-local; private LAN allowed). Save (step-up) swaps the live `atomic.Pointer[sso.Provider]` with no restart, and a login in flight across the swap fails once. `signin_bound` names the provider SSO accounts belong to: a change of kind or issuer, saved here or made in the environment between restarts (`LoadSignIn`), first disables the previous provider's accounts (`kysignon` and `scim` for KyIdentity, `oidc` for oidc), after the admin confirms the count (409 `confirm_provider_change`). `oidc` logins are everyday users stored as `sso_provider = 'oidc'` and never adopt SCIM rows. Capsule backups carry the settings and the data key, so a restore restores sign-in. Audit: `admin.signin_test`, `admin.signin_save`, `admin.signin_provider_change`.
```

and in the Plan 2 bullet, replace `read from the ID token's \`roles\` at every KySignOn login` with ``read from the ID token's `roles` at every login through the `kyidentity` provider (an `oidc` provider's logins are never administrators)``.

In `README.md`, change the intro's `local and federated sign-in (KySignOn, OIDC, SAML)` to `local and federated sign-in (KyIdentity or any OpenID Connect provider)`, and insert before `## Groups`:

```markdown
## Sign-in

KyCalendar always accepts local passwords. **Sign-in** connects one single sign-on provider:

- **KyIdentity**: logins with the `kycalendar.admin` app role are administrators (see above), and SCIM users from KyIdentity sign in as themselves.
- **Another OpenID Connect provider** (Keycloak, Authentik, Google, Entra ID and others): everyone who signs in is an everyday user, whatever roles the provider sends. Administrators stay local accounts.

Register a client with PKCE (S256) at the provider, with the redirect URI the screen shows (`https://<your host>/api/sso/kysignon/callback`), then enter the issuer URL, client ID, client secret and the button label. **Test** reads the provider's discovery document without saving; **Save** tests again, needs a sign-in from the last 10 minutes, and takes effect at once. The issuer must use HTTPS and must name itself exactly as typed; redirects, loopback and link-local addresses are refused, private LAN addresses are allowed. The secret is stored encrypted under the instance's data key and never shown again; leave it blank to keep it. Backups carry the settings and the key, so a restore restores sign-in.

Switching provider, or changing the issuer, disables every account of the previous provider (their calendars stay) so that a different provider can never sign in as one of them; the screen states how many before you confirm. The login page shows "Continue with <label>" only while a provider is configured.

Environment variables win: `KY_KYSIGNON_ISSUER`, `KY_KYSIGNON_CLIENT_ID` and `KY_KYSIGNON_SECRET` lock their fields, and any `KY_KYSIGNON_*` variable fixes the provider to KyIdentity. Changing them between restarts counts as a provider change too. `KY_SSO_ENABLED=false` switches single sign-on off whatever is saved. Save tests the issuer even when the environment sets it, so an issuer reachable only over loopback or plain HTTP must stay environment-only.
```

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/SignIn.tsx web/src/pages/SignIn.test.tsx web/src/App.tsx web/src/App.test.tsx web/src/components/AppHeader.tsx web/src/pages/Dashboard.tsx web/AGENTS.md README.md AGENTS.md web/dist
git commit -m "feat(web): Sign-in screen with environment-locked fields" -m "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

## Task 32: PR 3 final verification and pull request

Run each command alone and let it finish before starting the next.

- [ ] **Step 1:** `make tidy-check` — Expected: no diff.
- [ ] **Step 2:** `make lint` — Expected: clean.
- [ ] **Step 3:** `go test -race -count=1 -p 1 ./internal/store/...` — Expected: `ok`.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/api/...` — Expected: `ok`.
- [ ] **Step 5:** `go test -race -count=1 -p 1 ./cmd/... ./internal/access/... ./internal/apppass/... ./internal/auth/... ./internal/backup/... ./internal/calendar/... ./internal/config/... ./internal/crypto/... ./internal/davbackend/... ./internal/scim/... ./internal/sso/... ./internal/testdb/... ./web/...` — Expected: every package `ok` or `no test files`.
- [ ] **Step 6:** `make test-fork` — Expected: `ok`.
- [ ] **Step 7:** `cd web && npm ci && npm test` — Expected: every test file passes.
- [ ] **Step 8:** `cd web && npm run build && cd .. && git status --porcelain web/dist` — Expected: no output (otherwise commit `web/dist` as in Task 13).
- [ ] **Step 9:** `make build && KY_SMOKE_PORT=28931 ./scripts/smoke-test.sh` — Expected: `smoke test: all checks passed` (the anonymous settings carry no `signin_name`: no provider is configured there).
- [ ] **Step 10:** `go build -o .browser/server ./cmd/server && cd web && npx playwright install chromium && npm run test:browser` — Expected: every spec passes in all four projects; the login page shows no SSO button.
- [ ] **Step 11 (when a PostgreSQL is reachable):** `make test-postgres` — Expected: `ok`.
- [ ] **Step 12: Open the PR**

```bash
git push -u origin feat/standalone-signin
gh pr create --base main --title "Standalone administration 3/3: sign-in settings" --body "$(cat <<'BODY'
Connect KyIdentity or any OIDC provider from KyCalendar's own Sign-in screen.

- `signin_*` settings under environment precedence (`KY_KYSIGNON_*` lock their fields); the client secret is sealed under a derived key and never returned.
- Test and Save run guarded discovery (HTTPS, exact issuer, S256; no redirects, loopback or link-local).
- Save swaps the live provider with no restart; a login in flight across the swap fails once.
- A provider or issuer change, saved or made in the environment, disables the previous provider's accounts after the admin confirms the count.
- `oidc` logins are always everyday users; the login button shows only for a configured provider.

Spec: docs/superpowers/specs/2026-10-07-kycalendar-standalone-admin-design.md

🤖 Generated with [Claude Code](https://claude.com/claude-code)
BODY
)"
```

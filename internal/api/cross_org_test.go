package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// addOrg inserts org id into cfg's database directly: orgs come from KyIdentity in
// multi-tenant mode (tenancy PR 4), so the store has no writer for them yet.
func addOrg(t *testing.T, cfg *config.Config, id string) {
	t.Helper()
	driver, q := "sqlite", `INSERT INTO orgs (id, status) VALUES (?, 'active')`
	if cfg.Database.Driver == "postgres" {
		driver, q = "pgx", `INSERT INTO orgs (id, status) VALUES ($1, 'active')`
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, id); err != nil {
		t.Fatal(err)
	}
}

// TestCrossOrgMatrix is the tenancy spec's cross-org test, area by area (tenancy PR 2): every
// route given another org's ID answers byte for byte as for an ID that exists nowhere, and
// writes nothing. This part covers users, groups, memberships and grants.
func TestCrossOrgMatrix(t *testing.T) {
	ctx := context.Background()
	srv, st, cfg := setupTestServer(t)
	const b = "org-b"
	addOrg(t, cfg, b)

	manager := loginAs(t, srv, st, "manager", "user")
	admin := loginAs(t, srv, st, "admin", "admin")
	for _, g := range []*store.Group{{ID: "grp_a", DisplayName: "A"}, {ID: "grp_b", DisplayName: "B", OrgID: b}} {
		if err := st.Groups().CreateGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_b", Username: "b", Role: "user", Status: "active", SSOProvider: "local", OrgID: b}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g", Slug: "g", Name: "G"}, 0); err != nil {
		t.Fatal(err)
	}
	a := st.Org(store.DefaultOrg)
	if err := a.AddGroupMember(ctx, "grp_a", "usr_manager"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_a", Role: "manager"}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, method, other, missing, body string
		as                                 *http.Cookie
	}{
		{"grant to another org's group", "PUT", "/api/calendars/cal_g/grants/grp_b", "/api/calendars/cal_g/grants/grp_none", `{"role":"reader"}`, manager},
		{"read another org's group", "GET", "/api/admin/groups/grp_b", "/api/admin/groups/grp_none", "", admin},
		{"add another org's person", "PUT", "/api/admin/groups/grp_a/members/usr_b", "/api/admin/groups/grp_a/members/usr_none", "", admin},
	} {
		other := call(t, srv, c.method, c.other, c.body, c.as)
		missing := call(t, srv, c.method, c.missing, c.body, c.as)
		if other.Code != missing.Code || other.Body.String() != missing.Body.String() || other.Code < 400 {
			t.Errorf("%s: %d %q, missing: %d %q", c.name, other.Code, other.Body.String(), missing.Code, missing.Body.String())
		}
	}
	if grants, err := st.Calendars().ListGrants(ctx, "cal_g"); err != nil || len(grants) != 1 {
		t.Errorf("grants after cross-org calls: %+v %v", grants, err)
	}
	if g, err := st.Org(b).GetGroup(ctx, "grp_b"); err != nil || len(g.Members) != 0 {
		t.Errorf("org b's group after cross-org calls: %+v %v", g, err)
	}
}

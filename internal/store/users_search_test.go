package store_test

import (
	"context"
	"errors"
	"slices"
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

// unbound is SCIM's scope on an instance with no sign-in binding.
var unbound = store.SCIMScope{Providers: []string{"kysignon", "scim"}}

// A SCIM scope keeps only its providers' rows bound to its issuer, in the rows and the total,
// combined with any filter, in SCIMUserIDs and in the scoped writes: never a local account, an
// OIDC login or another binding's row. A scope with no provider reaches nothing.
func TestSCIMScopeListsIDsAndWrites(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	bound := "kyidentity https://a.example"
	for _, u := range []*store.User{
		{ID: "loc", Username: "ann", SSOProvider: "local"},
		{ID: "sci", Username: "Anna", SSOProvider: "scim", SSOIssuer: bound},
		{ID: "kys", Username: "bob", SSOProvider: "kysignon", SSOIssuer: bound},
		{ID: "oid", Username: "annie", SSOProvider: "oidc", SSOIssuer: bound},
		{ID: "old", Username: "anne", SSOProvider: "scim", SSOIssuer: "kyidentity https://old.example"},
	} {
		u.Role, u.Status = "user", "active"
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	scope := store.SCIMScope{Issuer: bound, Providers: []string{"kysignon", "scim"}}
	for _, tc := range []struct {
		filter store.UserFilter
		want   int
	}{
		{store.UserFilter{SCIM: &scope}, 2},
		{store.UserFilter{SCIM: &scope, Field: store.UserFieldUsername, Value: "ANN"}, 0},
		{store.UserFilter{SCIM: &scope, Field: store.UserFieldSearch, Value: "ann"}, 1},
		{store.UserFilter{SCIM: &store.SCIMScope{Issuer: bound}}, 0},
		{store.UserFilter{Field: store.UserFieldSearch, Value: "ann"}, 4},
	} {
		users, total, err := st.Users().ListUsers(ctx, 0, 50, tc.filter)
		if err != nil || total != tc.want || len(users) != tc.want {
			t.Errorf("%+v: %d rows, total %d, %v; want %d", tc.filter, len(users), total, err, tc.want)
		}
		for _, u := range users {
			if tc.filter.SCIM != nil && !tc.filter.SCIM.Contains(u) {
				t.Errorf("%+v listed %s outside the scope", tc.filter, u.ID)
			}
		}
	}
	got, err := st.Users().SCIMUserIDs(ctx, scope, []string{"loc", "sci", "nobody", "kys", "oid", "old"})
	if err != nil || len(got) != 2 || !slices.Contains(got, "sci") || !slices.Contains(got, "kys") {
		t.Fatalf("SCIMUserIDs: %v %v, want sci and kys", got, err)
	}
	for _, id := range []string{"loc", "oid", "old"} {
		u, _ := st.Users().GetUserByID(ctx, id)
		edit := *u
		edit.Status = "inactive"
		if err := st.Users().UpdateSCIMUser(ctx, scope, &edit, "user", "active"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UpdateSCIMUser %s: %v, want ErrNotFound", id, err)
		}
		if err := st.Users().DeleteSCIMUser(ctx, scope, id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("DeleteSCIMUser %s: %v, want ErrNotFound", id, err)
		}
		if u, err := st.Users().GetUserByID(ctx, id); err != nil || u.Status != "active" {
			t.Errorf("%s after refused writes: %+v %v", id, u, err)
		}
	}
	if err := st.Users().DeleteSCIMUser(ctx, scope, "sci"); err != nil {
		t.Fatalf("DeleteSCIMUser in scope: %v", err)
	}
}

// A SCIM group write is all or nothing and keeps memberships outside the scope.
func TestSCIMGroupWritesAreOneTransaction(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	for _, u := range []*store.User{
		{ID: "sci", Username: "sci", SSOProvider: "scim"},
		{ID: "kys", Username: "kys", SSOProvider: "kysignon"},
		{ID: "loc", Username: "loc", SSOProvider: "local"},
	} {
		u.Role, u.Status = "user", "active"
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Groups().CreateSCIMGroup(ctx, unbound, &store.Group{ID: "grp_bad", DisplayName: "Bad", Members: []string{"sci", "loc"}}); !errors.Is(err, store.ErrInvalidMember) {
		t.Fatalf("create with a local member: %v", err)
	}
	if _, err := st.Groups().GetGroupByID(ctx, "grp_bad"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused create left its group: %v", err)
	}
	if err := st.Groups().CreateSCIMGroup(ctx, unbound, &store.Group{ID: "grp_1", DisplayName: "Crew", Members: []string{"sci", "sci"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, "grp_1", "loc"); err != nil {
		t.Fatal(err)
	}
	g, err := st.Groups().UpdateSCIMGroup(ctx, unbound, "grp_1", func(g *store.Group) error {
		if !slices.Equal(g.Members, []string{"sci"}) {
			t.Errorf("change saw %v, want the in-scope members", g.Members)
		}
		g.DisplayName, g.Members = "Renamed", []string{"kys", "loc"}
		return nil
	})
	if !errors.Is(err, store.ErrInvalidMember) || g != nil {
		t.Fatalf("update adding a local member: %+v %v", g, err)
	}
	if g, _ := st.Groups().GetGroupByID(ctx, "grp_1"); g.DisplayName != "Crew" || !slices.Equal(slices.Sorted(slices.Values(g.Members)), []string{"loc", "sci"}) {
		t.Fatalf("a refused update wrote: %+v", g)
	}
	if _, err := st.Groups().UpdateSCIMGroup(ctx, unbound, "grp_1", func(g *store.Group) error {
		g.Members = []string{"kys"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if g, _ := st.Groups().GetGroupByID(ctx, "grp_1"); !slices.Equal(slices.Sorted(slices.Values(g.Members)), []string{"kys", "loc"}) {
		t.Fatalf("members %v, want kys and the kept local one", g.Members)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_local", DisplayName: "Local"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Groups().UpdateSCIMGroup(ctx, unbound, "grp_local", func(*store.Group) error { return nil }); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of a local group: %v", err)
	}
}

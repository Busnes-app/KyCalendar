package store_test

import (
	"context"
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

// SSOOnly drops local accounts from the rows and the total, combined with any filter; SSOUserIDs
// keeps only the IDs of existing non-local accounts. SCIM sees accounts through both.
func TestSSOOnlyListsAndIDs(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	for _, u := range []*store.User{
		{ID: "loc", Username: "ann", SSOProvider: "local"},
		{ID: "sci", Username: "Anna", SSOProvider: "scim"},
		{ID: "kys", Username: "bob", SSOProvider: "kysignon"},
	} {
		u.Role, u.Status = "user", "active"
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		filter store.UserFilter
		want   int
	}{
		{store.UserFilter{SSOOnly: true}, 2},
		{store.UserFilter{SSOOnly: true, Field: store.UserFieldUsername, Value: "ANN"}, 0},
		{store.UserFilter{SSOOnly: true, Field: store.UserFieldSearch, Value: "ann"}, 1},
		{store.UserFilter{Field: store.UserFieldSearch, Value: "ann"}, 2},
	} {
		users, total, err := st.Users().ListUsers(ctx, 0, 50, tc.filter)
		if err != nil || total != tc.want || len(users) != tc.want {
			t.Errorf("%+v: %d rows, total %d, %v; want %d", tc.filter, len(users), total, err, tc.want)
		}
		for _, u := range users {
			if tc.filter.SSOOnly && u.SSOProvider == "local" {
				t.Errorf("%+v listed local %s", tc.filter, u.ID)
			}
		}
	}
	got, err := st.Users().SSOUserIDs(ctx, []string{"loc", "sci", "nobody", "kys"})
	if err != nil || len(got) != 2 || !slices.Contains(got, "sci") || !slices.Contains(got, "kys") {
		t.Fatalf("SSOUserIDs: %v %v, want sci and kys", got, err)
	}
}

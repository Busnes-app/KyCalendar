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

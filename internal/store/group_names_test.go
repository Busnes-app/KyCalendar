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
	if err := st.Groups().UpdateGroup(ctx, &store.Group{ID: "grp_missing", DisplayName: "Nobody"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of a missing group: %v, want ErrNotFound", err)
	}
}

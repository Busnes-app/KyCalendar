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

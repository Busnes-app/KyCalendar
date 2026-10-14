package scim_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// Concurrent PATCHes of one group never lose each other's writes: each applies to the group as
// the previous one left it, so a rename and an externalId change made together both land.
func TestSCIMConcurrentGroupPatchesKeepEveryWrite(t *testing.T) {
	h, st, token, id := groupWith(t, "usr_a")
	for i := range 40 {
		name, ext := fmt.Sprintf("Crew %d", i), fmt.Sprintf("ext-%d", i)
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for j, op := range []map[string]any{
			{"op": "replace", "path": "displayName", "value": name},
			{"op": "replace", "path": "externalId", "value": ext},
		} {
			wg.Go(func() { codes[j] = scimDo(t, h, token, "PATCH", "/scim/v2/Groups/"+id, patchOps(op)).Code })
		}
		wg.Wait()
		g, err := st.Groups().GetGroupByID(context.Background(), id)
		if err != nil || codes[0] != http.StatusOK || codes[1] != http.StatusOK {
			t.Fatalf("round %d: %v %v", i, codes, err)
		}
		if g.DisplayName != name || g.ExternalID != ext {
			t.Fatalf("round %d lost a write: %q %q, want %q %q", i, g.DisplayName, g.ExternalID, name, ext)
		}
	}
}

package suite

import (
	"os"
	"slices"
	"testing"
)

// A real fs.tree of the bucket root from the pinned SeaweedFS, holding both key layouts.
func TestOrgPrefixes(t *testing.T) {
	tree, err := os.ReadFile("testdata/fs-tree-seaweedfs-4.52.txt")
	if err != nil {
		t.Fatal(err)
	}
	for org, want := range map[string][]string{
		"A": {"cells/e2e-1/orgs/A/", "orgs/A/"},
		"B": {"cells/e2e-1/orgs/B/", "orgs/B/"},
		"x": nil, // a directory under an org, not an org
		"C": nil,
	} {
		if got := orgPrefixes(string(tree), org); !slices.Equal(got, want) {
			t.Errorf("orgPrefixes(%q) = %q, want %q", org, got, want)
		}
	}
}

package apppass

import (
	"strings"
	"testing"
)

func TestGenerateParseMatch(t *testing.T) {
	id, token, hash, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "kc_"+id+"_") || strings.Contains(hash, token) {
		t.Fatalf("token %q hash %q", token, hash)
	}
	gotID, secret, ok := Parse(token)
	if !ok || gotID != id || !Matches(hash, secret) {
		t.Fatalf("round trip failed: %v %q", ok, gotID)
	}
	if Matches(hash, secret+"x") {
		t.Fatal("wrong secret matched")
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "kc_", "kc_abc", "xx_abc_def", "kc__def", "kc_abc_"} {
		if _, _, ok := Parse(s); ok {
			t.Errorf("accepted %q", s)
		}
	}
}

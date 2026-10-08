package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// quoteSCIM escapes a value the way KyIdentity does (internal/sync/scim.go findSCIMResource).
func quoteSCIM(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// KyIdentity escapes `\` and `"` inside the quoted filter value; the lookup must unescape them.
func TestSCIMFilterValueEscapes(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	const bound = "kyidentity https://id.example"
	if err := st.Settings().SetSetting(ctx, "signin_bound", bound); err != nil {
		t.Fatal(err)
	}
	const odd = `a"b\c`
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_odd", Username: "odd", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: odd, SSOIssuer: bound}); err != nil {
		t.Fatal(err)
	}
	if total, ids := listIDs(t, h, token, "/scim/v2/Users", "externalId eq "+quoteSCIM(odd)); total != 1 || len(ids) != 1 || ids[0] != "usr_odd" {
		t.Errorf("escaped user lookup: total %d %v, want usr_odd", total, ids)
	}
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Odd", "externalId": odd})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var g struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	if total, ids := listIDs(t, h, token, "/scim/v2/Groups", "externalId eq "+quoteSCIM(odd)); total != 1 || len(ids) != 1 || ids[0] != g.ID {
		t.Errorf("escaped group lookup: total %d %v, want %s", total, ids, g.ID)
	}

	for _, bad := range []string{
		`externalId eq "a\nb"`,                              // unknown escape
		`externalId eq "abc`,                                // unterminated
		`externalId eq "abc\"`,                              // escaped closing quote
		`externalId eq "a"b"`,                               // bare quote
		`externalId eq "` + strings.Repeat("x", 1025) + `"`, // over 1024 bytes
		`externalId ne "x"`,
		`externalId eq "x" and userName eq "y"`,
		`members eq "x"`,
	} {
		for _, path := range []string{"/scim/v2/Users", "/scim/v2/Groups"} {
			w := scimDo(t, h, token, "GET", path+"?filter="+url.QueryEscape(bad), nil)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalidFilter") {
				t.Errorf("%s %s: %d %s, want 400 invalidFilter", path, bad, w.Code, w.Body.String())
			}
		}
	}
	if total, _ := listIDs(t, h, token, "/scim/v2/Users", `externalId eq "`+strings.Repeat("x", 1024)+`"`); total != 0 {
		t.Errorf("1024-byte value: total %d, want 0", total)
	}
}

package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func memberList(ids ...string) []map[string]any {
	out := []map[string]any{}
	for _, id := range ids {
		out = append(out, map[string]any{"value": id})
	}
	return out
}

func patchOps(ops ...map[string]any) map[string]any {
	return map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": ops}
}

// groupWith creates SCIM users a, b, c, a local account and a SCIM group holding members.
func groupWith(t *testing.T, members ...string) (http.Handler, store.Store, string, string) {
	t.Helper()
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	for _, id := range []string{"usr_a", "usr_b", "usr_c"} {
		if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: id, Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
			t.Fatal(err)
		}
	}
	localRow(t, st)
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "members": memberList(members...)})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var g struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	return h, st, token, g.ID
}

func storedMembers(t *testing.T, st store.Store, id string) []string {
	t.Helper()
	g, err := st.Groups().GetGroupByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(slices.Values(g.Members))
}

// RFC 7644 3.5.2: add appends to a multi-valued attribute, remove with a value filter removes
// that value, remove without one empties it, replace replaces it.
func TestSCIMGroupPatchMemberOperations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start []string
		ops   []map[string]any
		want  []string
	}{
		{"add appends", []string{"usr_a"}, []map[string]any{{"op": "add", "path": "members", "value": memberList("usr_b")}}, []string{"usr_a", "usr_b"}},
		{"add is idempotent", []string{"usr_a"}, []map[string]any{{"op": "add", "path": "members", "value": memberList("usr_a", "usr_b", "usr_b")}}, []string{"usr_a", "usr_b"}},
		{"add without path", []string{"usr_a"}, []map[string]any{{"op": "add", "value": map[string]any{"members": memberList("usr_c")}}}, []string{"usr_a", "usr_c"}},
		{"Add capitalised (Entra)", []string{"usr_a"}, []map[string]any{{"op": "Add", "path": "members", "value": memberList("usr_b")}}, []string{"usr_a", "usr_b"}},
		{"remove by filter", []string{"usr_a", "usr_b"}, []map[string]any{{"op": "remove", "path": `members[value eq "usr_a"]`}}, []string{"usr_b"}},
		{"remove an absent member", []string{"usr_a"}, []map[string]any{{"op": "remove", "path": `members[value eq "usr_c"]`}}, []string{"usr_a"}},
		{"remove all", []string{"usr_a", "usr_b"}, []map[string]any{{"op": "remove", "path": "members"}}, []string{}},
		{"replace", []string{"usr_a", "usr_b"}, []map[string]any{{"op": "replace", "path": "members", "value": memberList("usr_c")}}, []string{"usr_c"}},
		{"mixed", []string{"usr_a"}, []map[string]any{
			{"op": "add", "path": "members", "value": memberList("usr_b", "usr_c")},
			{"op": "remove", "path": `members[value eq "usr_a"]`},
			{"op": "replace", "path": "displayName", "value": "Crew 2"},
		}, []string{"usr_b", "usr_c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, token, id := groupWith(t, tc.start...)
			w := scimDo(t, h, token, "PATCH", "/scim/v2/Groups/"+id, patchOps(tc.ops...))
			if w.Code != http.StatusOK {
				t.Fatalf("patch: %d %s", w.Code, w.Body.String())
			}
			if got := storedMembers(t, st, id); !slices.Equal(got, tc.want) {
				t.Fatalf("members %v, want %v", got, tc.want)
			}
			var res struct {
				Members []struct{ Value string } `json:"members"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &res)
			if len(res.Members) != len(tc.want) {
				t.Errorf("response members %+v, want %v", res.Members, tc.want)
			}
		})
	}
}

func TestSCIMGroupPatchNameAndExternalID(t *testing.T) {
	h, st, token, id := groupWith(t, "usr_a")
	w := scimDo(t, h, token, "PATCH", "/scim/v2/Groups/"+id, patchOps(
		map[string]any{"op": "replace", "path": "displayName", "value": "Renamed"},
		map[string]any{"op": "replace", "path": "externalId", "value": "kgrp-9"},
	))
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	g, err := st.Groups().GetGroupByID(context.Background(), id)
	if err != nil || g.DisplayName != "Renamed" || g.ExternalID != "kgrp-9" || !slices.Equal(g.Members, []string{"usr_a"}) {
		t.Fatalf("after patch: %+v %v", g, err)
	}
}

// A local or unknown member anywhere in the PATCH refuses the whole request before any write,
// and a local membership the group already holds survives every member operation.
func TestSCIMGroupPatchRefusesLocalMembersAndKeepsThem(t *testing.T) {
	h, st, token, id := groupWith(t, "usr_a")
	if err := st.Groups().AddGroupMember(context.Background(), id, "usr_local"); err != nil {
		t.Fatal(err)
	}
	for _, ops := range [][]map[string]any{
		{{"op": "add", "path": "members", "value": memberList("usr_local")}},
		{{"op": "add", "path": "members", "value": memberList("usr_nobody")}},
		{{"op": "replace", "path": "displayName", "value": "Changed"}, {"op": "add", "path": "members", "value": memberList("usr_b", "usr_local")}},
		{{"op": "replace", "path": "members", "value": memberList("usr_local")}},
	} {
		if w := scimDo(t, h, token, "PATCH", "/scim/v2/Groups/"+id, patchOps(ops...)); w.Code != http.StatusBadRequest {
			t.Errorf("%v: %d %s, want 400", ops, w.Code, w.Body.String())
		}
	}
	g, _ := st.Groups().GetGroupByID(context.Background(), id)
	if g.DisplayName != "Crew" || !slices.Equal(storedMembers(t, st, id), []string{"usr_a", "usr_local"}) {
		t.Fatalf("a refused patch wrote: %+v", g)
	}
	for _, op := range []map[string]any{
		{"op": "remove", "path": "members"},
		{"op": "replace", "path": "members", "value": memberList("usr_b")},
		{"op": "remove", "path": `members[value eq "usr_local"]`},
	} {
		if w := scimDo(t, h, token, "PATCH", "/scim/v2/Groups/"+id, patchOps(op)); w.Code != http.StatusOK {
			t.Fatalf("%v: %d %s", op, w.Code, w.Body.String())
		}
		if got := storedMembers(t, st, id); !slices.Contains(got, "usr_local") {
			t.Fatalf("%v removed the local member: %v", op, got)
		}
	}
	if w := scimDo(t, h, token, "GET", "/scim/v2/Groups/"+id, nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "usr_local") {
		t.Fatalf("local member reported: %s", w.Body.String())
	}
}

// A member filter other than `value eq "<id>"` is refused rather than silently ignored.
func TestSCIMGroupPatchRefusesOtherMemberFilters(t *testing.T) {
	h, st, token, id := groupWith(t, "usr_a", "usr_b")
	for _, path := range []string{`members[value ne "usr_a"]`, `members[display eq "x"]`, `members[value eq "usr_a" or value eq "usr_b"]`} {
		if w := scimDo(t, h, token, "PATCH", "/scim/v2/Groups/"+id, patchOps(map[string]any{"op": "remove", "path": path})); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", path, w.Code, w.Body.String())
		}
	}
	if got := storedMembers(t, st, id); !slices.Equal(got, []string{"usr_a", "usr_b"}) {
		t.Fatalf("members changed: %v", got)
	}
}

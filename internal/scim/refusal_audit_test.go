package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func auditOf(t *testing.T, st store.Store, action string) []*store.AuditRecord {
	t.Helper()
	rows, _, err := st.Audit().ListAuditRecords(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []*store.AuditRecord
	for _, r := range rows {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

// hasRefusal reports an audit row of action on resource whose details are exactly reason=<reason>.
func hasRefusal(rows []*store.AuditRecord, resource, reason string) bool {
	for _, r := range rows {
		if r.Resource == resource && r.Details == "reason="+reason {
			return true
		}
	}
	return false
}

// A refused SCIM write leaves an audit row with IDs and a reason code, never the payload.
func TestSCIMRefusalsAreAudited(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	if err := st.Settings().SetSetting(ctx, "signin_bound", "kyidentity https://b.example"); err != nil {
		t.Fatal(err)
	}
	localRow(t, st)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_yan", Username: "yan", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-y", SSOIssuer: "kyidentity https://a.example"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_local", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}

	if w := scimDo(t, h, token, "POST", "/scim/v2/Users", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "yan.k", "externalId": "sub-y"}); w.Code != http.StatusConflict {
		t.Fatalf("other binding: %d %s", w.Code, w.Body.String())
	}
	u, _ := st.Users().GetUserByID(ctx, "usr_local")
	if w := scimDo(t, h, token, "POST", "/scim/v2/Users", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": strings.ToUpper(u.Username)}); w.Code != http.StatusConflict {
		t.Fatalf("local name: %d %s", w.Code, w.Body.String())
	}
	users := auditOf(t, st, "scim.user.refused")
	if !hasRefusal(users, "usr_yan", "other_binding") || !hasRefusal(users, "", "name_taken") {
		t.Errorf("user refusals: %+v", rowsString(users))
	}

	if w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "crew"}); w.Code != http.StatusConflict {
		t.Fatalf("group name: %d %s", w.Code, w.Body.String())
	}
	if w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Sneaky", "members": memberList("usr_local")}); w.Code != http.StatusBadRequest {
		t.Fatalf("group create member: %d %s", w.Code, w.Body.String())
	}
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Synced"})
	var g struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	if w := scimDo(t, h, token, "PATCH", "/scim/v2/Groups/"+g.ID, patchOps(map[string]any{"op": "add", "path": "members", "value": memberList("usr_local")})); w.Code != http.StatusBadRequest {
		t.Fatalf("group patch member: %d %s", w.Code, w.Body.String())
	}
	groups := auditOf(t, st, "scim.group.refused")
	if !hasRefusal(groups, "grp_local", "name_taken") || !hasRefusal(groups, "", "invalid_member") || !hasRefusal(groups, g.ID, "invalid_member") {
		t.Errorf("group refusals: %+v", rowsString(groups))
	}
	for _, r := range append(users, groups...) {
		if strings.Contains(r.Details+r.Resource, "usr_local") || strings.Contains(r.Details+r.Resource, "yan.k") {
			t.Errorf("refusal row carries payload: %+v", *r)
		}
	}
}

// A 412 (the account changed since SCIM read it) is audited too.
func TestSCIMAccessChangedRefusalIsAudited(t *testing.T) {
	h, st, token := racingSCIM(t, func(st store.Store) {
		_ = st.Users().UpdateSCIMUser(context.Background(), &store.User{ID: "usr_bo", Username: "bo", Role: "user", Status: "active"}, "user", "inactive")
	})
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_bo", Username: "bo", Role: "user", Status: "inactive", SSOProvider: "scim", SSOSubject: "b1"}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "bo", "active": true, "roles": []any{map[string]any{"value": "kycalendar.admin"}}}
	if w := scimDo(t, h, token, "PUT", "/scim/v2/Users/usr_bo", body); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if rows := auditOf(t, st, "scim.user.refused"); !hasRefusal(rows, "usr_bo", "access_changed") {
		t.Fatalf("412 not audited: %+v", rowsString(rows))
	}
}

func rowsString(rows []*store.AuditRecord) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Resource+" "+r.Details)
	}
	return out
}

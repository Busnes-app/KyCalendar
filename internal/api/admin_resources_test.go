package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

type adminResource struct {
	ID          string   `json:"id"`
	Address     string   `json:"address"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Groups      []string `json:"groups"`
	Approvers   []string `json:"approvers"`
	Enabled     bool     `json:"enabled"`
	HorizonDays int      `json:"horizon_days"`
}

const roomBody = `{"kind":"room","name":"Fjord","location":"Floor 2","capacity":8,"tags":["Screen","screen","video"],
"audience":"everyone","approval":"auto","max_minutes":120,"horizon_days":60,"enabled":true}`

func TestAdminResources(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "boss", "admin")
	user := loginAs(t, srv, st, "carol", "user")

	if w := call(t, srv, "POST", "/api/admin/resources", roomBody, user); w.Code != http.StatusForbidden {
		t.Fatalf("everyday create: %d", w.Code)
	}
	w := call(t, srv, "POST", "/api/admin/resources", roomBody, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var r adminResource
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.ID) != 32 || r.Address != "resource-"+r.ID+"@kycalendar.invalid" || len(r.Tags) != 2 || !r.Enabled {
		t.Fatalf("created %+v", r)
	}
	if got := auditDetails(t, st, "admin.resource_create"); len(got) != 1 {
		t.Fatalf("audit %v", got)
	}

	if w := call(t, srv, "POST", "/api/admin/resources", `{"kind":"room","name":"FJORD","audience":"everyone","approval":"auto","max_minutes":60,"horizon_days":10}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "name_taken" {
		t.Fatalf("case twin: %d %s", w.Code, w.Body.String())
	}

	for name, body := range map[string]string{
		"kind":     `{"kind":"car","name":"X","audience":"everyone","approval":"auto","max_minutes":60,"horizon_days":10}`,
		"quote":    `{"kind":"room","name":"A\"B","audience":"everyone","approval":"auto","max_minutes":60,"horizon_days":10}`,
		"control":  `{"kind":"room","name":"A\u0007","audience":"everyone","approval":"auto","max_minutes":60,"horizon_days":10}`,
		"horizon":  `{"kind":"room","name":"X","audience":"everyone","approval":"auto","max_minutes":60,"horizon_days":731}`,
		"minutes":  `{"kind":"room","name":"X","audience":"everyone","approval":"auto","max_minutes":4,"horizon_days":10}`,
		"groups":   `{"kind":"room","name":"X","audience":"groups","groups":[],"approval":"auto","max_minutes":60,"horizon_days":10}`,
		"approver": `{"kind":"room","name":"X","audience":"everyone","approval":"approvers","approvers":[],"max_minutes":60,"horizon_days":10}`,
		"unknown":  `{"kind":"room","name":"X","audience":"everyone","approval":"auto","max_minutes":60,"horizon_days":10,"owner":"me"}`,
	} {
		if w := call(t, srv, "POST", "/api/admin/resources", body, admin); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}

	// Groups must exist; approvers must be active everyday users.
	if w := call(t, srv, "PUT", "/api/admin/resources/"+r.ID, `{"kind":"room","name":"Fjord","audience":"groups","groups":["grp_none"],"approval":"auto","max_minutes":60,"horizon_days":10}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "unknown_group" {
		t.Fatalf("unknown group: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PUT", "/api/admin/resources/"+r.ID, `{"kind":"room","name":"Fjord","audience":"everyone","approval":"approvers","approvers":["usr_boss"],"max_minutes":60,"horizon_days":10}`, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "bad_approver" {
		t.Fatalf("admin approver: %d %s", w.Code, w.Body.String())
	}
	if err := st.Groups().CreateGroup(t.Context(), &store.Group{ID: "grp_ops", DisplayName: "Ops"}); err != nil {
		t.Fatal(err)
	}
	w = call(t, srv, "PUT", "/api/admin/resources/"+r.ID, `{"kind":"equipment","name":"Projector","audience":"groups","groups":["grp_ops","grp_ops"],"approval":"approvers","approvers":["usr_carol"],"max_minutes":60,"horizon_days":10,"enabled":false}`, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	got, err := st.Resources().GetResource(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "equipment" || got.Enabled || len(got.Groups) != 1 || len(got.Approvers) != 1 || got.Approvers[0] != "usr_carol" {
		t.Fatalf("stored %+v", got)
	}
	if w := call(t, srv, "PUT", "/api/admin/resources/ffffffffffffffffffffffffffffffff", roomBody, admin); w.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", w.Code)
	}

	// Deleting a group drops it from the resource.
	if err := st.Groups().DeleteGroup(t.Context(), "grp_ops"); err != nil {
		t.Fatal(err)
	}
	w = call(t, srv, "GET", "/api/admin/resources", "", admin)
	var list struct {
		Resources []adminResource `json:"resources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Resources) != 1 || len(list.Resources[0].Groups) != 0 {
		t.Fatalf("list after group delete: %s", w.Body.String())
	}
}

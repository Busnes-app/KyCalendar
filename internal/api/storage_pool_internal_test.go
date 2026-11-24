package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const poolOrgTest = "11111111-1111-4111-8111-111111111111"
const poolOtherTest = "22222222-2222-4222-8222-222222222222"

func TestStorageReporterMirrorsAndRenews(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{poolOrgTest: "active", poolOtherTest: "suspended", "org-staff": "active"})
	var got []suitedir.StorageReport
	idp.suite = func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/suite/orgs":
			fmt.Fprintf(w, `{"orgs":[{"id":%q,"status":"active","enabled":true,"revision":1,"quotas":{}},{"id":%q,"status":"suspended","enabled":true,"revision":2,"quotas":{}}]}`, poolOrgTest, poolOtherTest)
		case "/api/suite/storage":
			var in struct {
				Reports []suitedir.StorageReport `json:"reports"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			got = in.Reports
			results := []map[string]any{}
			for _, v := range in.Reports {
				extra := int64(256)
				if v.OrgID == poolOtherTest {
					extra = 0
				}
				results = append(results, map[string]any{"org_id": v.OrgID, "result": "ok", "seq": v.Seq, "ceiling_bytes": v.UsedBytes + extra, "ttl_seconds": 900, "pool_bytes": 10000, "org_used_bytes": v.UsedBytes})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
		default:
			http.NotFound(w, r)
		}
	}
	s.SyncOrgs(context.Background())
	if len(got) != 2 {
		t.Fatalf("expected two customer reports, no staff/default: %+v", got)
	}
	for _, r := range got {
		if r.Seq != 1 || r.UsedBytes != 0 {
			t.Fatalf("first report: %+v", r)
		}
		if r.OrgID == poolOtherTest && r.WantBytes != 0 {
			t.Fatal("suspended org requested growth")
		}
	}
	if err := s.syncStoragePool(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Seq != 2 {
			t.Fatalf("renewal reused sequence: %+v", r)
		}
	}
}
func TestStorageReporterRetriesFrozenRequest(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{poolOrgTest: "active"})
	calls := 0
	var first suitedir.StorageReport
	idp.suite = func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reports []suitedir.StorageReport `json:"reports"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		calls++
		if len(in.Reports) != 1 {
			t.Fatalf("reports: %+v", in)
		}
		if calls == 1 {
			first = in.Reports[0]
			w.WriteHeader(503)
			return
		}
		if in.Reports[0] != first {
			t.Errorf("retry changed: %+v -> %+v", first, in.Reports[0])
		}
		json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"org_id": poolOrgTest, "result": "unknown_org"}}})
	}
	if err := s.syncStoragePool(context.Background()); err == nil {
		t.Fatal("accepted failed report")
	}
	if err := s.syncStoragePool(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Unknown_org acknowledges that sequence without reviving its initial grace.
	next, err := s.store.BeginStorageReport(context.Background(), store.OrgID(poolOrgTest), 1)
	if err != nil || next.Seq != first.Seq+1 {
		t.Fatalf("unknown-org transition: %+v %v", next, err)
	}
}
func TestStorageReporterIsSingleModeNoop(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{poolOrgTest: "active"})
	idp.suite = func(w http.ResponseWriter, r *http.Request) { t.Error("single mode contacted storage") }
	if err := s.store.Settings().SetSetting(context.Background(), store.TenancySetting, "single"); err != nil {
		t.Fatal(err)
	}
	if err := s.syncStoragePool(context.Background()); err != nil {
		t.Fatal(err)
	}
}

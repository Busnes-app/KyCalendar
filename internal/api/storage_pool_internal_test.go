package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/auth"
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

func TestStorageEarlyRefreshOnlyMarkedOrPending(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{poolOrgTest: "active", poolOtherTest: "active"})
	var requests []suitedir.StorageReport
	idp.suite = func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reports []suitedir.StorageReport `json:"reports"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		requests = append(requests, in.Reports...)
		results := []map[string]any{}
		for _, v := range in.Reports {
			results = append(results, map[string]any{"org_id": v.OrgID, "result": "ok", "seq": v.Seq, "ceiling_bytes": v.UsedBytes + 256, "ttl_seconds": 900, "pool_bytes": 10000, "org_used_bytes": v.UsedBytes})
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}
	s.SyncStorageRefresh(context.Background())
	if len(requests) != 0 {
		t.Fatal("unmarked orgs were reported early")
	}
	if err := s.store.MarkStorageRefresh(context.Background(), store.OrgID(poolOtherTest)); err != nil {
		t.Fatal(err)
	}
	s.SyncStorageRefresh(context.Background())
	if len(requests) != 1 || requests[0].OrgID != poolOtherTest {
		t.Fatalf("early subset: %+v", requests)
	}
	if err := s.store.Settings().SetSetting(context.Background(), store.ErasedPendingSetting, "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.MarkStorageRefresh(context.Background(), store.OrgID(poolOrgTest)); err != nil {
		t.Fatal(err)
	}
	s.SyncStorageRefresh(context.Background())
	if len(requests) != 1 {
		t.Fatal("reported before restored erasures were reconciled")
	}
}

func TestWritesWaitForInflightStoragePoolRefresh(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{poolOrgTest: "active"})
	ctx := context.Background()
	org := store.OrgID(poolOrgTest)

	// Sign in a customer user so their personal default calendar and session exist.
	idp.extra = map[string]any{"org_id": poolOrgTest}
	login := ssoLogin(t, s, idp, "pool-writer", "sid-pool-writer")
	if login.Code != http.StatusFound {
		t.Fatalf("login status: %d", login.Code)
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookieName:
			sessionCookie = c
		case auth.CSRFCookieName:
			csrfCookie = c
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("missing cookies: session=%v csrf=%v", sessionCookie, csrfCookie)
	}
	u, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "pool-writer")
	if err != nil {
		t.Fatal(err)
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/calendars", nil)
	listReq.AddCookie(sessionCookie)
	listW := httptest.NewRecorder()
	s.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("GET /api/calendars: %d %s", listW.Code, listW.Body.String())
	}
	cals, err := s.store.Calendars().ListCalendarsByOwner(ctx, "user", u.ID)
	if err != nil || len(cals) == 0 {
		t.Fatalf("calendars: %v %v", cals, err)
	}
	calID := cals[0].ID

	// Provision a CalDAV app password for the same user.
	apID, apToken, apHash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{
		Scope: store.ScopeDAV, ID: apID, UserID: u.ID, Label: "dav", Hash: apHash,
	}, 0); err != nil {
		t.Fatal(err)
	}

	// Establish an initial active 1 MiB storage-pool grant.
	r0, err := s.store.BeginStorageReport(ctx, org, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.ApplyStorageGrant(ctx, org, r0.Seq, 1<<20, store.StorageGrantTTL); err != nil {
		t.Fatal(err)
	}

	t.Run("rest event create waits for inflight refresh", func(t *testing.T) {
		r, err := s.store.BeginStorageReport(ctx, org, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			time.Sleep(60 * time.Millisecond)
			done <- s.store.ApplyStorageGrant(ctx, org, r.Seq, r.UsedBytes+(1<<20), store.StorageGrantTTL)
		}()

		body := `{"title":"Concurrent Event","start":"2026-06-01T10:00:00Z","end":"2026-06-01T11:00:00Z","zone":"UTC"}`
		req := httptest.NewRequest(http.MethodPost, "/api/calendars/"+calID+"/events", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(auth.HeaderCSRF, csrfCookie.Value)
		req.AddCookie(sessionCookie)
		req.AddCookie(csrfCookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)

		if gerr := <-done; gerr != nil {
			t.Fatalf("ApplyStorageGrant: %v", gerr)
		}
		if w.Code != http.StatusCreated {
			t.Fatalf("POST event during inflight refresh: want 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("rest event create waits for live syncStoragePool HTTP round-trip", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		idp.suite = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/suite/storage" {
				http.NotFound(w, r)
				return
			}
			var in struct {
				Reports []suitedir.StorageReport `json:"reports"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			close(entered)
			<-release
			results := make([]map[string]any, 0, len(in.Reports))
			for _, v := range in.Reports {
				results = append(results, map[string]any{
					"org_id": v.OrgID, "result": "ok", "seq": v.Seq,
					"ceiling_bytes": v.UsedBytes + (1 << 20), "ttl_seconds": 900,
					"pool_bytes": 10 << 20, "org_used_bytes": v.UsedBytes,
				})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
		}

		syncDone := make(chan error, 1)
		go func() {
			syncDone <- s.syncStoragePool(ctx)
		}()
		<-entered // pool_inflight = TRUE in DB while HTTP call to KyIdentity is in flight

		go func() {
			time.Sleep(60 * time.Millisecond)
			close(release)
		}()

		body := `{"title":"During SyncStoragePool","start":"2026-06-01T12:00:00Z","end":"2026-06-01T13:00:00Z","zone":"UTC"}`
		req := httptest.NewRequest(http.MethodPost, "/api/calendars/"+calID+"/events", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(auth.HeaderCSRF, csrfCookie.Value)
		req.AddCookie(sessionCookie)
		req.AddCookie(csrfCookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)

		if serr := <-syncDone; serr != nil {
			t.Fatalf("syncStoragePool: %v", serr)
		}
		if w.Code != http.StatusCreated {
			t.Fatalf("POST event during syncStoragePool: want 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("caldav put waits for inflight refresh", func(t *testing.T) {
		r, err := s.store.BeginStorageReport(ctx, org, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			time.Sleep(60 * time.Millisecond)
			done <- s.store.ApplyStorageGrant(ctx, org, r.Seq, r.UsedBytes+(1<<20), store.StorageGrantTTL)
		}()

		ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//Test//EN\r\nBEGIN:VEVENT\r\nUID:dav-inflight-1@example.test\r\nDTSTAMP:20260601T000000Z\r\nDTSTART:20260602T100000Z\r\nDTEND:20260602T110000Z\r\nSUMMARY:DAV Refresh Test\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		req := httptest.NewRequest(http.MethodPut, "/dav/"+u.ID+"/calendars/default/dav-inflight-1.ics", strings.NewReader(ics))
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.SetBasicAuth(u.Username, apToken)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)

		if gerr := <-done; gerr != nil {
			t.Fatalf("ApplyStorageGrant: %v", gerr)
		}
		if w.Code != http.StatusCreated {
			t.Fatalf("CalDAV PUT during inflight refresh: want 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("genuine quota exhaustion fails immediately with 507", func(t *testing.T) {
		// Shrink ceiling to current usage + 10 bytes (smaller than any event).
		r, err := s.store.BeginStorageReport(ctx, org, 10)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.store.ApplyStorageGrant(ctx, org, r.Seq, r.UsedBytes+10, store.StorageGrantTTL); err != nil {
			t.Fatal(err)
		}
		// Even with an inflight report open, the write exceeds the unexpired ceiling so it must fail immediately.
		if _, err := s.store.BeginStorageReport(ctx, org, 10); err != nil {
			t.Fatal(err)
		}

		start := time.Now()
		body := `{"title":"Over Ceiling Event","start":"2026-06-03T10:00:00Z","end":"2026-06-03T11:00:00Z","zone":"UTC"}`
		req := httptest.NewRequest(http.MethodPost, "/api/calendars/"+calID+"/events", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(auth.HeaderCSRF, csrfCookie.Value)
		req.AddCookie(sessionCookie)
		req.AddCookie(csrfCookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		elapsed := time.Since(start)

		if w.Code != http.StatusInsufficientStorage {
			t.Fatalf("over-ceiling POST: want 507, got %d: %s", w.Code, w.Body.String())
		}
		if elapsed > 200*time.Millisecond {
			t.Fatalf("genuine quota exhaustion waited %v instead of failing immediately", elapsed)
		}
	})
}

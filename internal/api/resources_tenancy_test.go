package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/resource"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// offD9 is the one plain 404 every route tenancy D9 turns off gives in multi-tenant mode
// (singleTenantOnly), the same for every role and for no session.
const offD9 = "404 page not found\n"

// D9 keeps all room and equipment routes off on a multi-tenant install.
func TestMultiTenantResourceRoutesAnswerAsMissing(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	alice, _ := webUser(t, srv, st, "alice")
	admin := loginAs(t, srv, st, "admin", "admin")
	addOrg(t, cfg, "org-b")
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_bob", Username: "bob", Email: "bob@example.com", Role: "user", Status: "active", SSOProvider: "local", OrgID: "org-b"}); err != nil {
		t.Fatal(err)
	}
	if err := davbackend.EnsureDefault(ctx, st, "usr_bob", 0); err != nil {
		t.Fatal(err)
	}
	cal, err := st.Calendars().GetCalendarBySlug(ctx, "user", "usr_bob", "default")
	if err != nil {
		t.Fatal(err)
	}
	room := makeResource(t, st, resource.Resource{Name: "Room"})
	equipment := makeResource(t, st, resource.Resource{Name: "Equipment", Kind: resource.Equipment, Approval: resource.Approvers, Approvers: []string{"usr_alice"}})
	start, end := slot(3, 9)
	for uid, id := range map[string]string{"booked": room, "pending": equipment} {
		fields := "DTSTART:" + strings.ReplaceAll(strings.ReplaceAll(start, "-", ""), ":", "") + "\r\nDTEND:" + strings.ReplaceAll(strings.ReplaceAll(end, "-", ""), ":", "") + "\r\n"
		data := strings.ReplaceAll(roomICS(uid, id, fields), "ORGANIZER:mailto:alice@example.com", "ORGANIZER:mailto:bob@example.com")
		putRaw(t, st, cal.ID, uid+".ics", data, "usr_bob")
	}
	bookings, err := st.Resources().BookingsIn(ctx, cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := bookings["pending.ics"][equipment]
	if pending == nil || pending.Status != resource.Pending || bookings["booked.ics"][room].Status != resource.Accepted {
		t.Fatalf("bookings: %+v", bookings)
	}
	query := "?start=" + start + "&end=" + end
	missing := call(t, srv, "GET", "/api/resources/missing/busy"+query, "", alice)
	if missing.Code != http.StatusNotFound || missing.Body.String() != "{\"error\":\"No such room or equipment\"}\n" {
		t.Fatalf("missing: %d %s", missing.Code, missing.Body.String())
	}
	for _, path := range []string{"/api/resources" + query, "/api/resources/" + room + "/busy" + query, "/api/resources/requests"} {
		if w := call(t, srv, "GET", path, "", alice); w.Code != http.StatusOK {
			t.Fatalf("single-tenant control %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	routes := map[string]struct {
		paths []string
		body  string
	}{
		"GET /api/resources":                {paths: []string{"/api/resources", "/api/resources" + query, "/api/resources?start=bad"}},
		"GET /api/resources/{id}/busy":      {paths: []string{"/api/resources/" + room + "/busy" + query, "/api/resources/" + equipment + "/busy" + query, "/api/resources/missing/busy" + query, "/api/resources/" + room + "/busy?start=bad"}},
		"GET /api/resources/requests":       {paths: []string{"/api/resources/requests"}},
		"POST /api/resources/requests/{id}": {paths: []string{"/api/resources/requests/" + pending.ID, "/api/resources/requests/missing"}, body: `{"decision":"accept"}`},
		"GET /api/admin/resources":          {paths: []string{"/api/admin/resources"}},
		"POST /api/admin/resources":         {paths: []string{"/api/admin/resources"}, body: `{"kind":"room","name":"New room","audience":"everyone","approval":"auto","max_minutes":240,"horizon_days":60,"enabled":true}`},
		"PUT /api/admin/resources/{id}":     {paths: []string{"/api/admin/resources/" + room, "/api/admin/resources/" + equipment, "/api/admin/resources/missing"}, body: `{}`},
	}
	for _, pattern := range api.RoutesForTest(srv) {
		if strings.Contains(pattern, " /api/resources") || strings.Contains(pattern, " /api/admin/resources") {
			if _, ok := routes[pattern]; !ok {
				t.Fatalf("resource route needs a D9 test: %s", pattern)
			}
		}
	}
	if err := st.Settings().SetSetting(ctx, store.TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	for pattern, tc := range routes {
		t.Run(pattern, func(t *testing.T) {
			method, _, _ := strings.Cut(pattern, " ")
			for role, cookie := range map[string]*http.Cookie{"member": alice, "admin": admin} {
				for _, path := range tc.paths {
					for _, body := range []string{tc.body, "invalid JSON"} {
						w := call(t, srv, method, path, body, cookie)
						if w.Code != http.StatusNotFound || w.Body.String() != offD9 {
							t.Errorf("%s %s: %d %q; want the plain 404 every D9 route gives", role, path, w.Code, w.Body.String())
						}
					}
				}
			}
		})
	}
	after, err := st.Resources().BookingsIn(ctx, cal.ID)
	if err != nil || after["pending.ics"][equipment].Status != resource.Pending {
		t.Fatalf("pending booking changed: %+v %v", after, err)
	}
}

// A missing mode is not permission to serve the single-tenant feature.
func TestResourceRoutesRequireRecordedSingleTenantMode(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	if err := st.Settings().DeleteSetting(ctx, store.TenancySetting); err != nil {
		t.Fatal(err)
	}
	for _, pattern := range api.RoutesForTest(srv) {
		if !strings.Contains(pattern, " /api/resources") && !strings.Contains(pattern, " /api/admin/resources") {
			continue
		}
		method, path, _ := strings.Cut(pattern, " ")
		path = strings.ReplaceAll(path, "{id}", "missing")
		w := call(t, srv, method, path, "invalid JSON", nil)
		if w.Code != http.StatusNotFound || w.Body.String() != offD9 {
			t.Errorf("missing mode %s: %d %q", pattern, w.Code, w.Body.String())
		}
	}
	srv = api.NewServer(cfg, unreadableResourceTenancy{st})
	for _, pattern := range api.RoutesForTest(srv) {
		if !strings.Contains(pattern, " /api/resources") && !strings.Contains(pattern, " /api/admin/resources") {
			continue
		}
		method, path, _ := strings.Cut(pattern, " ")
		w := call(t, srv, method, strings.ReplaceAll(path, "{id}", "missing"), "invalid JSON", nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("unreadable mode %s: %d %s", pattern, w.Code, w.Body.String())
		}
	}
}

type unreadableResourceTenancy struct{ store.Store }

func (s unreadableResourceTenancy) Tenancy(context.Context) (store.Tenancy, error) {
	return store.Tenancy{}, errInjected
}

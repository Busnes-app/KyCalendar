package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// staffCall is method path as a signed-in session of userID (with its CSRF token), or anonymous
// when token is "".
func staffCall(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
		csrf := auth.SessionCSRFToken(s.config.Security.SessionSecret, token)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		req.Header.Set(auth.HeaderCSRF, csrf)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func sessionFor(t *testing.T, s *Server, userID, token string) {
	t.Helper()
	now := time.Now().UTC()
	if err := s.store.Sessions().CreateSession(context.Background(), &store.Session{TokenHash: crypto.SHA256Hex([]byte(token)), UserID: userID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, store.Credentials{}); err != nil {
		t.Fatal(err)
	}
}

// Tenancy build step 5 (Admin surface): in multi-tenant mode staff read one chosen org at a time,
// every such read leaves a platform staff.read row, account writes and business features answer
// one plain 404 for everyone, the sign-in provider is fixed, and the org mirror is
// listed. Single-tenant keeps every route.
func TestMultiTenantAdminSurface(t *testing.T) {
	s, _ := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-b": "active", "org-staff": "active"})
	ctx := context.Background()
	for _, u := range []*store.User{
		{ID: "usr_staff", Username: "staff", Role: "admin", OrgID: "org-staff"},
		{ID: "usr_alice", Username: "alice@a.example", Email: "alice@a.example", Role: "user", OrgID: "org-a"},
		{ID: "usr_bert", Username: "bert@b.example", Email: "bert@b.example", Role: "user", OrgID: "org-b"},
	} {
		u.Status, u.SSOProvider = "active", "local"
		if err := s.store.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	sessionFor(t, s, "usr_staff", "tok-staff")

	// Off: one plain 404, the same for staff and for no session, so it tells nobody anything.
	off := "404 page not found\n"
	for _, rt := range [][2]string{
		{"GET", "/api/admin/groups"}, {"POST", "/api/admin/groups"}, {"GET", "/api/admin/groups/g1"}, {"PATCH", "/api/admin/groups/g1"},
		{"DELETE", "/api/admin/groups/g1"}, {"PUT", "/api/admin/groups/g1/members/usr_alice"}, {"DELETE", "/api/admin/groups/g1/members/usr_alice"},
		{"GET", "/api/admin/calendars"}, {"POST", "/api/admin/calendars"}, {"DELETE", "/api/admin/calendars/c1"},
		{"GET", "/api/admin/resources"}, {"POST", "/api/admin/resources"}, {"PUT", "/api/admin/resources/r1"},
		{"GET", "/api/resources"}, {"GET", "/api/resources/r1/busy"}, {"GET", "/api/resources/requests"}, {"POST", "/api/resources/requests/x"},
		{"POST", "/api/admin/users"}, {"PATCH", "/api/admin/users/usr_alice"}, {"POST", "/api/admin/users/usr_alice/reset-password"},
		{"POST", "/api/admin/users/usr_alice/role"}, {"POST", "/api/admin/users/usr_alice/disable"}, {"POST", "/api/admin/users/usr_alice/enable"},
		{"POST", "/api/admin/users/usr_alice/reattach"}, {"GET", "/scim/v2/Users"}, {"POST", "/scim/v2/Users"},
	} {
		for _, tok := range []string{"tok-staff", ""} {
			if w := staffCall(t, s, tok, rt[0], rt[1], "{}"); w.Code != http.StatusNotFound || w.Body.String() != off {
				t.Errorf("%s %s (session %q): %d %q, want the plain 404", rt[0], rt[1], tok, w.Code, w.Body.String())
			}
		}
	}

	// The sign-in provider is fixed (D2).
	for _, rt := range [][2]string{{"PUT", "/api/admin/signin"}, {"POST", "/api/admin/signin/test"}} {
		if w := staffCall(t, s, "tok-staff", rt[0], rt[1], "{}"); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "sign_in_fixed") {
			t.Errorf("%s %s: %d %s", rt[0], rt[1], w.Code, w.Body.String())
		}
	}
	if w := staffCall(t, s, "tok-staff", "GET", "/api/admin/signin", ""); w.Code != http.StatusOK {
		t.Errorf("reading the sign-in provider: %d", w.Code)
	}

	// People: one chosen org, recorded.
	if w := staffCall(t, s, "tok-staff", "GET", "/api/admin/users", ""); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "org_required") {
		t.Errorf("people without ?org=: %d %s", w.Code, w.Body.String())
	}
	if w := staffCall(t, s, "tok-staff", "GET", "/api/admin/users?org=bad%20id", ""); w.Code != http.StatusBadRequest {
		t.Errorf("people with a malformed org: %d", w.Code)
	}
	if err := davbackend.EnsureDefault(ctx, s.store, "usr_alice", 0); err != nil {
		t.Fatal(err)
	}
	cal, err := s.store.Calendars().GetCalendarBySlug(ctx, "user", "usr_alice", "default")
	if err != nil {
		t.Fatal(err)
	}
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:a1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	parsed, _ := ical.NewDecoder(strings.NewReader(raw)).Decode()
	obj, err := davbackend.Prepare(cal.ID, "a1.ics", parsed, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Calendars().PutObject(ctx, obj, "", true, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	w := staffCall(t, s, "tok-staff", "GET", "/api/admin/users?org=org-a", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "alice@a.example") || strings.Contains(w.Body.String(), "bert@b.example") ||
		strings.Contains(w.Body.String(), `"staff"`) {
		t.Errorf("people of org-a: %d %s", w.Code, w.Body.String())
	}
	var people struct{ Bytes map[string]int64 }
	if err := json.Unmarshal(w.Body.Bytes(), &people); err != nil || people.Bytes["usr_alice"] != int64(len(raw)) {
		t.Errorf("alice's stored bytes %v, want %d", people.Bytes, len(raw))
	}

	// Audit: platform rows by default, one org's with ?org=, and every org read is a platform
	// staff.read row naming the staff member, the org and the route.
	if err := s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: "usr_alice", Action: "alice.did"}); err != nil {
		t.Fatal(err)
	}
	actions := func(path string) string {
		t.Helper()
		w := staffCall(t, s, "tok-staff", "GET", path, "")
		var out struct{ Records []store.AuditRecord }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var a []string
		for _, r := range out.Records {
			a = append(a, r.UserID+":"+r.Action+":"+r.Details)
		}
		return strings.Join(a, " | ")
	}
	platform := actions("/api/admin/audit")
	if !strings.Contains(platform, "usr_staff:staff.read:org=org-a route=GET /api/admin/users") || strings.Contains(platform, "alice.did") {
		t.Errorf("platform audit rows: %s", platform)
	}
	if got := actions("/api/admin/audit?org=org-a"); !strings.Contains(got, "usr_alice:alice.did") || strings.Contains(got, "staff.read") {
		t.Errorf("org-a's audit rows: %s", got)
	}
	if got := actions("/api/admin/audit"); !strings.Contains(got, "staff.read:org=org-a route=GET /api/admin/audit") {
		t.Errorf("reading org-a's audit was not recorded: %s", got)
	}
	// The staff.read rows outlive org-a's own rows, as they must outlive its purge.
	recs, _, err := s.store.Audit().ListOrgAuditRecords(ctx, "org-a", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Action == "staff.read" {
			t.Errorf("a staff.read row joined org-a: %+v", r)
		}
	}

	// SCIM shows off; the org mirror is listed.
	if w := staffCall(t, s, "tok-staff", "GET", "/api/admin/scim", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("SCIM status: %d %s", w.Code, w.Body.String())
	}
	w = staffCall(t, s, "tok-staff", "GET", "/api/admin/orgs", "")
	var orgs struct{ Orgs []store.MirroredOrgView }
	if err := json.Unmarshal(w.Body.Bytes(), &orgs); err != nil || w.Code != http.StatusOK || len(orgs.Orgs) < 3 {
		t.Errorf("orgs: %d %s", w.Code, w.Body.String())
	}
	if w := staffCall(t, s, "", "GET", "/api/admin/orgs", ""); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("orgs without a session: %d", w.Code)
	}
}

// Single-tenant keeps every route: the gated routes still answer, people need no ?org=, and
// /api/admin/orgs does not exist.
func TestSingleTenantAdminSurfaceUnchanged(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	if err := s.store.Users().CreateUser(ctx, root); err != nil {
		t.Fatal(err)
	}
	sessionFor(t, s, "usr_root", "tok-root")
	for _, path := range []string{"/api/admin/users", "/api/admin/groups", "/api/admin/calendars", "/api/admin/resources", "/api/admin/audit"} {
		if w := staffCall(t, s, "tok-root", "GET", path, ""); w.Code != http.StatusOK {
			t.Errorf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := staffCall(t, s, "tok-root", "GET", "/api/admin/orgs", ""); w.Code != http.StatusNotFound {
		t.Errorf("GET /api/admin/orgs in single-tenant mode: %d", w.Code)
	}
	recs, _, _ := s.store.Audit().ListAuditRecords(ctx, 0, 50)
	for _, r := range recs {
		if r.Action == "staff.read" {
			t.Errorf("single-tenant reads wrote %+v", r)
		}
	}
}

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/calexport"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/password"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// multiTenantSignIn is a server signing in through idp, switched to multi-tenant mode with staff
// as its staff org and the given orgs mirrored (id -> status).
func multiTenantSignIn(t *testing.T, staff string, orgs map[string]string) (*Server, *signingIdP) {
	t.Helper()
	s, _ := davInternalServer(t)
	ctx := context.Background()
	idp := newSigningIdP(t)
	if _, _, err := s.bindAccounts(ctx, kyidentityAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", idp.Client()))
	driver, q := "sqlite", `INSERT INTO orgs (id, status, seen_at) VALUES (?, ?, `+strconv.FormatInt(time.Now().Unix(), 10)+`)`
	if s.config.Database.Driver == "postgres" {
		driver, q = "pgx", `INSERT INTO orgs (id, status, seen_at) VALUES ($1, $2, `+strconv.FormatInt(time.Now().Unix(), 10)+`)`
	}
	db, err := sql.Open(driver, s.config.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, status := range orgs {
		if _, err := db.Exec(q, id, status); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{store.TenancySetting: "multi", store.StaffOrgSetting: staff} {
		if err := s.store.Settings().SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return s, idp
}

// loginWithOrg signs sub in with org as the token's org_id ("" sends none) and roles.
func loginWithOrg(t *testing.T, s *Server, idp *signingIdP, sub, org string, roles ...string) int {
	t.Helper()
	idp.extra = map[string]any{}
	if org != "" {
		idp.extra["org_id"] = org
	}
	idp.roles = roles
	return ssoLogin(t, s, idp, sub, "sid-"+sub).Code
}

func auditDetails(t *testing.T, s *Server, action string) []string {
	t.Helper()
	recs, _, err := s.store.Audit().ListAuditRecords(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range recs {
		if r.Action == action {
			out = append(out, r.UserID+" "+r.Details)
		}
	}
	return out
}

// In multi-tenant mode the org comes only from the verified ID token: none is refused, an org
// that does not serve is refused, a new account joins the token's org, and a known account's
// token naming another org changes nothing (tenancy spec, Identity binding).
func TestMultiTenantSignInBindsTheTokensOrg(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-b": "active", "org-gone": "suspended", "org-staff": "active"})
	ctx := context.Background()

	if code := loginWithOrg(t, s, idp, "alice", ""); code != http.StatusForbidden {
		t.Fatalf("no org_id: %d", code)
	}
	if got := auditDetails(t, s, "auth.sso_refused"); len(got) != 1 || !strings.Contains(got[0], "reason=no_org subject=alice") {
		t.Fatalf("no_org audit: %q", got)
	}
	for _, org := range []string{"org-unknown", "org-gone"} {
		if code := loginWithOrg(t, s, idp, "alice", org); code != http.StatusForbidden {
			t.Fatalf("%s: %d", org, code)
		}
	}
	if _, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "alice"); err == nil {
		t.Fatal("a refused sign-in created the account")
	}

	if code := loginWithOrg(t, s, idp, "alice", "org-a"); code != http.StatusFound && code != http.StatusSeeOther {
		t.Fatalf("org-a sign-in: %d", code)
	}
	alice, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "alice")
	if err != nil || store.OrgOf(alice) != "org-a" {
		t.Fatalf("new account: %+v %v", alice, err)
	}
	if code := loginWithOrg(t, s, idp, "alice", "org-b"); code != http.StatusForbidden {
		t.Fatalf("org_mismatch: %d", code)
	}
	if got := auditDetails(t, s, "auth.sso_refused"); len(got) != 2 || !strings.Contains(got[0]+got[1], alice.ID+" reason=org_mismatch org=org-a token_org=org-b") {
		t.Fatalf("org_mismatch audit: %q", got)
	}
	if after, _ := s.store.Users().GetUserByID(ctx, alice.ID); store.OrgOf(after) != "org-a" || after.Status != "active" {
		t.Fatalf("a mismatched token moved or changed the account: %+v", after)
	}
}

// D7: in multi-tenant mode kycalendar.admin makes an administrator only in the staff org.
func TestMultiTenantAdminOnlyInTheStaffOrg(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-staff": "active"})
	ctx := context.Background()
	for sub, org := range map[string]string{"customer": "org-a", "staffer": "org-staff"} {
		if code := loginWithOrg(t, s, idp, sub, org, access.AdminAppRole); code >= 400 {
			t.Fatalf("%s: %d", sub, code)
		}
	}
	for sub, want := range map[string]string{"customer": "user", "staffer": "admin"} {
		if u, err := s.store.Users().GetUserBySSO(ctx, "kysignon", sub); err != nil || u.Role != want {
			t.Errorf("%s: %+v %v, want role %s", sub, u, err, want)
		}
	}
}

// With no staff org recorded, nobody is an administrator.
func TestMultiTenantWithoutAStaffOrgHasNoAdmins(t *testing.T) {
	s, idp := multiTenantSignIn(t, "", map[string]string{"org-a": "active"})
	if code := loginWithOrg(t, s, idp, "x", "org-a", access.AdminAppRole); code >= 400 {
		t.Fatalf("sign-in: %d", code)
	}
	if u, err := s.store.Users().GetUserBySSO(context.Background(), "kysignon", "x"); err != nil || u.Role != "user" {
		t.Fatalf("%+v %v", u, err)
	}
}

// In multi-tenant mode a logout token must name the org of the account it ends, by sub or by a
// live session's sid; a missing or other org_id is refused and audited before anything changes.
func TestMultiTenantLogoutChecksTheOrgFirst(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active"})
	idp.extra = map[string]any{"org_id": "org-a"}
	idp.roles = nil
	first := ssoSession(ssoLogin(t, s, idp, "alice", "sid-1"))
	if first == nil || !signedIn(s, first) {
		t.Fatal("sign-in did not issue a working session")
	}
	token := func(jti string, claims map[string]any) string {
		c := map[string]any{"iss": idp.URL, "aud": "kc", "jti": jti, "iat": time.Now().Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
			"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}}
		for k, v := range claims {
			c[k] = v
		}
		return idp.sign(t, "logout+jwt", c)
	}
	for name, claims := range map[string]map[string]any{
		"no org_id":          {"sub": "alice", "sid": "sid-1"},
		"another org by sub": {"sub": "alice", "org_id": "org-b"},
		"another org by sid": {"sid": "sid-1", "org_id": "org-b"},
		"another org, both":  {"sub": "alice", "sid": "sid-1", "org_id": "org-b"},
	} {
		if w := postLogout(s, token(name, claims)); w.Code != http.StatusBadRequest || !signedIn(s, first) {
			t.Fatalf("%s: %d, signed in %v", name, w.Code, signedIn(s, first))
		}
	}
	if got := auditDetails(t, s, "auth.sso_org_mismatch"); len(got) != 4 {
		t.Fatalf("audit: %q", got)
	}
	if w := postLogout(s, token("ok", map[string]any{"sub": "alice", "sid": "sid-1", "org_id": "org-a"})); w.Code != http.StatusOK || signedIn(s, first) {
		t.Fatalf("logout of the account's org: %d, still signed in %v", w.Code, signedIn(s, first))
	}
}

// A logout token is applied at most once: a replay is refused and audited, and changes nothing.
func TestLogoutTokenIsAppliedOnce(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active"})
	idp.extra = map[string]any{"org_id": "org-a"}
	idp.roles = nil
	raw := idp.sign(t, "logout+jwt", map[string]any{"iss": idp.URL, "aud": "kc", "sub": "alice", "jti": "once", "org_id": "org-a",
		"iat": time.Now().Add(-2 * time.Second).Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}})
	if w := postLogout(s, raw); w.Code != http.StatusOK {
		t.Fatalf("first delivery: %d", w.Code)
	}
	if w := postLogout(s, raw); w.Code != http.StatusBadRequest {
		t.Fatalf("replay: %d", w.Code)
	}
	if got := auditDetails(t, s, "auth.sso_logout_replayed"); len(got) != 1 {
		t.Fatalf("replay audit: %q", got)
	}
}

// D8: a password admits only a staff administrator in multi-tenant mode.
func TestMultiTenantPasswordOnlyForStaffAdmins(t *testing.T) {
	s, _ := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active", "org-staff": "active"})
	s.config.Captcha.Provider = "" // the proof-of-work puzzle is not what this test is about
	ctx := context.Background()
	hash, err := password.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_breakglass", Username: "breakglass", Role: "admin", OrgID: "org-staff"},
		{ID: "usr_customer_admin", Username: "custadmin", Role: "admin", OrgID: "org-a"},
		{ID: "usr_member", Username: "member", Role: "user", OrgID: "org-staff"},
	} {
		u.Status, u.SSOProvider, u.PasswordHash = "active", "local", hash
		if err := s.store.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]int{"breakglass": http.StatusOK, "custadmin": http.StatusUnauthorized, "member": http.StatusUnauthorized} {
		if got := passwordLogin(t, s, name, "correct horse battery staple"); got != want {
			t.Errorf("%s: %d, want %d", name, got, want)
		}
	}
}

func passwordLogin(t *testing.T, s *Server, username, pw string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": pw})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w.Code
}

// An export_only sign-in never gets an ordinary session: refused while the mirror says active,
// admitted to the export routes alone while the org is deletion_scheduled, for at most an hour,
// for an existing account only, and ended once the org moves on.
func TestExportOnlySignInReachesOnlyTheExport(t *testing.T) {
	s, idp := multiTenantSignIn(t, "org-staff", map[string]string{"org-a": "active"})
	ctx := context.Background()
	if code := loginWithOrg(t, s, idp, "alice", "org-a"); code != http.StatusFound {
		t.Fatalf("ordinary sign-in: %d", code)
	}
	alice, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal-a", OwnerKind: "user", OwnerID: alice.ID, Slug: "work", Name: "Work"}, 0); err != nil {
		t.Fatal(err)
	}
	ics := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:t\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nSUMMARY:Board review\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	if _, err := s.store.Calendars().PutObjectPlanned(ctx, &store.CalendarObject{CalendarID: "cal-a", Name: "e1.ics", Component: "VEVENT", UID: "e1", ETag: "1", Data: ics, WriterID: alice.ID}, "", true, store.OwnerLimits{}, nil); err != nil {
		t.Fatal(err)
	}
	setStatus := func(status string) {
		t.Helper()
		if _, err := rawSQL(t, s).Exec(fmt.Sprintf(`UPDATE orgs SET status = '%s' WHERE id = 'org-a'`, status)); err != nil {
			t.Fatal(err)
		}
	}
	signIn := func(sub string, exportOnly bool) *httptest.ResponseRecorder {
		t.Helper()
		idp.extra, idp.roles = map[string]any{"org_id": "org-a"}, nil
		if exportOnly {
			idp.extra["export_only"] = true
		}
		return ssoLogin(t, s, idp, sub, "sid-"+sub)
	}
	get := func(c *http.Cookie, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(c)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		return w
	}

	if w := signIn("alice", true); w.Code != http.StatusForbidden || ssoSession(w) != nil {
		t.Fatalf("export_only while the mirror says active: %d, session %v", w.Code, ssoSession(w))
	}
	setStatus("deletion_scheduled")
	if w := signIn("alice", false); w.Code != http.StatusForbidden || ssoSession(w) != nil {
		t.Fatalf("an ordinary token during deletion_scheduled: %d", w.Code)
	}
	if w := signIn("nobody", true); w.Code != http.StatusForbidden || ssoSession(w) != nil {
		t.Fatalf("export_only for an unknown subject: %d", w.Code)
	}
	if _, err := s.store.Users().GetUserBySSO(ctx, "kysignon", "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("export_only created an account: %v", err)
	}

	w := signIn("alice", true)
	c := ssoSession(w)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/api/export" || c == nil {
		t.Fatalf("export_only during deletion_scheduled: %d %q", w.Code, w.Header().Get("Location"))
	}
	if time.Until(c.Expires) > auth.ExportOnlyTTL+time.Minute {
		t.Fatalf("export-only session lives until %v", c.Expires)
	}
	if me := get(c, "/api/auth/me"); !strings.Contains(me.Body.String(), `"exportOnly":true`) {
		t.Fatalf("/api/auth/me: %s", me.Body.String())
	}
	for _, path := range []string{"/api/calendars", "/api/app-passwords"} {
		if w := get(c, path); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "export_only") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	ex := get(c, "/api/export")
	if ex.Code != http.StatusOK || ex.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %q", ex.Code, ex.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(ex.Body.Bytes()), int64(ex.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	if !strings.Contains(files["calendars/cal-a.ics"], "SUMMARY:Board review") || !strings.Contains(files["manifest.json"], `"name": "Work"`) {
		t.Fatalf("export holds %v", files)
	}

	// A calendar that fails before the download starts is an error, never an empty zip.
	defer func(f func(context.Context, store.Store, string) ([]byte, int, []*store.CalendarObject, error)) {
		calendarICS = f
	}(calendarICS)
	calendarICS = func(context.Context, store.Store, string) ([]byte, int, []*store.CalendarObject, error) {
		return nil, 0, nil, errors.New("database unavailable")
	}
	if w := get(c, "/api/export"); w.Code != http.StatusServiceUnavailable || w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("export whose first calendar fails: %d %q", w.Code, w.Header().Get("Content-Disposition"))
	}

	// With KyIdentity unreachable past store.OrgFresh, export admission fails closed too.
	if _, err := rawSQL(t, s).Exec(fmt.Sprintf(`UPDATE orgs SET seen_at = seen_at - %d WHERE id = 'org-a'`, int(store.OrgFresh.Seconds())+1)); err != nil {
		t.Fatal(err)
	}
	calendarICS = calexport.CalendarICS
	if w := get(c, "/api/export"); w.Code != http.StatusUnauthorized {
		t.Fatalf("export with a stale org answer: %d", w.Code)
	}
	if w := signIn("alice", true); w.Code != http.StatusForbidden || ssoSession(w) != nil {
		t.Fatalf("export_only sign-in with a stale org answer: %d", w.Code)
	}
	if _, err := rawSQL(t, s).Exec(fmt.Sprintf(`UPDATE orgs SET seen_at = %d WHERE id = 'org-a'`, time.Now().Unix())); err != nil {
		t.Fatal(err)
	}
	if w := signIn("alice", true); ssoSession(w) == nil {
		t.Fatalf("export_only sign-in after a fresh answer: %d", w.Code)
	} else {
		c = ssoSession(w)
	}

	setStatus("deleted")
	if w := get(c, "/api/export"); w.Code != http.StatusUnauthorized {
		t.Fatalf("export after the org was deleted: %d", w.Code)
	}
}

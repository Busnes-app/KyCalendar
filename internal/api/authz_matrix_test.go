package api_test

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// The authorization matrix: every registered route and every CalDAV operation against every
// kind of caller. TestEveryRouteIsInTheMatrix fails when a route has no row.

type actor string

const (
	anon        actor = "anonymous"
	owner       actor = "owner"
	reader      actor = "reader"
	editor      actor = "editor"
	manager     actor = "manager"
	nonmember   actor = "nonmember"
	admin       actor = "admin"
	deactivated actor = "deactivated"
)

var actors = []actor{anon, owner, reader, editor, manager, nonmember, admin, deactivated}

// allow means authorization let the request through and the handler did not fail: any status
// but 401, 403, 404 or 5xx.
const allow = 0

type expect map[actor]int

var (
	adminOnly    = expect{anon: 401, deactivated: 401, admin: allow, owner: 403, reader: 403, editor: 403, manager: 403, nonmember: 403}
	everyday     = expect{anon: 401, deactivated: 401, admin: 403, owner: allow, reader: allow, editor: allow, manager: allow, nonmember: allow}
	anySession   = expect{anon: 401, deactivated: 401, admin: allow, owner: allow, reader: allow, editor: allow, manager: allow, nonmember: allow}
	grantManager = expect{anon: 401, deactivated: 401, admin: allow, manager: allow, reader: 403, editor: 403, owner: 404, nonmember: 404}
	groupManage  = expect{anon: 401, deactivated: 401, admin: 403, manager: allow, reader: 403, editor: 403, owner: 404, nonmember: 404}
	groupWrite   = expect{anon: 401, deactivated: 401, admin: 403, editor: allow, manager: allow, reader: 403, owner: 404, nonmember: 404}
	ownerOnly    = expect{anon: 401, deactivated: 401, admin: 403, owner: allow, reader: 404, editor: 404, manager: 404, nonmember: 404}
	// SCIM takes only its bearer token: a session cookie is never enough.
	scimOnly = expect{anon: 401, deactivated: 401, admin: 401, owner: 401, reader: 401, editor: 401, manager: 401, nonmember: 401}
)

// public routes authenticate (or not) by design; they have no authorization to test here.
// Each has its own tests: login, MFA and password limits, SSO, settings tiering, the SPA.
// Session-tiered public routes (/api/auth/me, /api/settings) are pinned by TestSessionTieredPublicRoutes.
var public = expect(nil)

// davPatterns are covered by davRows, operation by operation.
var davPatterns = map[string]bool{"/dav/": true, "/.well-known/caldav": true}

type world struct {
	srv     *api.Server
	st      store.Store
	cookies map[actor]*http.Cookie
	tokens  map[actor]string
	passIDs map[actor]string
	group   *store.Calendar // reader, editor and manager each hold their role on it
	doomed  *store.Calendar // the group calendar the admin row deletes
	spare   *store.Calendar // a personal calendar of the owner, deleted by the owner row
}

func newWorld(t *testing.T) *world {
	t.Helper()
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	w := &world{srv: srv, st: st, cookies: map[actor]*http.Cookie{}, tokens: map[actor]string{}, passIDs: map[actor]string{}}
	for _, a := range actors[1:] {
		role := "user"
		if a == admin {
			role = "admin"
		}
		w.cookies[a] = loginAs(t, srv, st, string(a), role)
		id, token, hash, err := apppass.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: id, UserID: "usr_" + string(a), Label: "matrix", Hash: hash}); err != nil {
			t.Fatal(err)
		}
		w.tokens[a], w.passIDs[a] = token, id
	}
	w.group = groupCalendar(t, st, "Team")
	w.doomed = groupCalendar(t, st, "Doomed")
	for _, a := range []actor{reader, editor, manager} {
		grantRole(t, st, w.group, string(a), "usr_"+string(a))
	}
	// Local accounts the People rows change; no row signs in as one. victim takes the reset and
	// the profile edit, which succeed in any order; each role or status row owns its own.
	for _, u := range []*store.User{
		{ID: "usr_victim", Username: "victim", Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_promotee", Username: "promotee", Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_disablee", Username: "disablee", Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_enablee", Username: "enablee", Role: "user", Status: "inactive", SSOProvider: "local"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	// grp_extra takes grants; grp_matrix takes members and a rename, so no row's membership
	// change can grant a caller access another row expects refused; grp_doomed is deleted.
	for _, g := range []*store.Group{{ID: "grp_extra", DisplayName: "Extra"}, {ID: "grp_matrix", DisplayName: "Matrix"}, {ID: "grp_doomed", DisplayName: "Doomed"}} {
		if err := st.Groups().CreateGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	put := func(name, uid string) {
		o := &store.CalendarObject{CalendarID: w.group.ID, Name: name, UID: uid, Data: []byte(eventICS(uid))}
		if _, err := st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	put("seed.ics", "seed-uid")
	for _, a := range actors[1:] {
		put(string(a)+"-del.ics", string(a)+"-del")
	}
	// Only the status changes: the session and app password stay, so this proves the status
	// check rather than the revocation (which the SCIM and webhook tests pin).
	u, err := st.Users().GetUserByID(ctx, "usr_deactivated")
	if err != nil {
		t.Fatal(err)
	}
	u.Status = "inactive"
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	// The spare would otherwise count as the owner's only calendar and suppress the lazily created default.
	if err := davbackend.EnsureDefault(ctx, st, "usr_owner", 0); err != nil {
		t.Fatal(err)
	}
	w.spare = &store.Calendar{ID: "cal_spare_owner", OwnerKind: "user", OwnerID: "usr_owner", Slug: "spare", Name: "Spare"}
	if err := st.Calendars().CreateCalendar(ctx, w.spare, 0); err != nil {
		t.Fatal(err)
	}
	return w
}

type apiRow struct {
	method, path, body string
	pathFor            func(a actor) string // overrides path when the target differs per caller
	want               expect
}

// apiRows has exactly one row per registered pattern, keyed by that pattern.
func apiRows(w *world) map[string]apiRow {
	g := "/api/calendars/" + w.group.ID + "/grants"
	return map[string]apiRow{
		"/api/auth/pow-challenge":         {want: public},
		"/api/auth/login":                 {want: public},
		"/api/auth/mfa/totp":              {want: public},
		"/api/auth/mfa/recovery-code":     {want: public},
		"/api/auth/logout":                {want: public},
		"/api/auth/me":                    {want: public},
		"/api/sso/kysignon/login":         {want: public},
		"/api/sso/kysignon/callback":      {want: public},
		"/api/sso/kysignon/sync":          {want: public},
		"/saml/metadata":                  {want: public},
		"/api/settings":                   {want: public},
		"/":                               {want: public},
		"/api/auth/change-password":       {method: "POST", path: "/api/auth/change-password", body: "{}", want: anySession},
		"POST /api/backup/drill":          {method: "POST", path: "/api/backup/drill", want: adminOnly},
		"POST /api/backup/export-capsule": {method: "POST", path: "/api/backup/export-capsule", want: adminOnly},
		"POST /api/backup/pair-remote":    {method: "POST", path: "/api/backup/pair-remote", body: "{}", want: adminOnly},
		"POST /api/backup/deposit":        {method: "POST", path: "/api/backup/deposit", want: adminOnly},
		"DELETE /api/backup/pairing":      {method: "DELETE", path: "/api/backup/pairing", want: adminOnly},
		"POST /api/backup/pin-key":        {method: "POST", path: "/api/backup/pin-key", body: "{}", want: adminOnly},
		"PUT /api/backup/schedule":        {method: "PUT", path: "/api/backup/schedule", body: "{}", want: adminOnly},
		"GET /api/backup/status":          {method: "GET", path: "/api/backup/status", want: adminOnly},
		"/api/settings/theme":             {method: "POST", path: "/api/settings/theme", body: "{}", want: adminOnly},
		"/scim/v2":                        {method: "GET", path: "/scim/v2", want: scimOnly},
		"/scim/v2/":                       {method: "GET", path: "/scim/v2/Users", want: scimOnly},
		"GET /api/app-passwords":          {method: "GET", path: "/api/app-passwords", want: everyday},
		"POST /api/app-passwords":         {method: "POST", path: "/api/app-passwords", body: `{"label":"matrix-new"}`, want: everyday},
		// Each caller deletes its own app password; the anonymous caller names a placeholder so
		// the path still matches the pattern instead of falling through to the SPA.
		"DELETE /api/app-passwords/{id}":                 {method: "DELETE", pathFor: func(a actor) string { return "/api/app-passwords/" + cmp.Or(w.passIDs[a], "none") }, want: everyday},
		"GET /api/admin/calendars":                       {method: "GET", path: "/api/admin/calendars", want: adminOnly},
		"POST /api/admin/calendars":                      {method: "POST", path: "/api/admin/calendars", body: `{"name":"Matrix"}`, want: adminOnly},
		"DELETE /api/admin/calendars/{id}":               {method: "DELETE", path: "/api/admin/calendars/" + w.doomed.ID, want: adminOnly},
		"GET /api/admin/groups":                          {method: "GET", path: "/api/admin/groups", want: adminOnly},
		"POST /api/admin/groups":                         {method: "POST", path: "/api/admin/groups", body: `{"display_name":"Matrix new"}`, want: adminOnly},
		"GET /api/admin/groups/{id}":                     {method: "GET", path: "/api/admin/groups/grp_matrix", want: adminOnly},
		"PATCH /api/admin/groups/{id}":                   {method: "PATCH", path: "/api/admin/groups/grp_matrix", body: `{"display_name":"Matrix"}`, want: adminOnly},
		"DELETE /api/admin/groups/{id}":                  {method: "DELETE", path: "/api/admin/groups/grp_doomed", want: adminOnly},
		"PUT /api/admin/groups/{id}/members/{userId}":    {method: "PUT", path: "/api/admin/groups/grp_matrix/members/usr_nonmember", want: adminOnly},
		"DELETE /api/admin/groups/{id}/members/{userId}": {method: "DELETE", path: "/api/admin/groups/grp_matrix/members/usr_nonmember", want: adminOnly},
		"GET /api/admin/users":                           {method: "GET", path: "/api/admin/users?q=owner", want: adminOnly},
		"POST /api/admin/users":                          {method: "POST", path: "/api/admin/users", body: `{"username":"matrix-new","role":"user"}`, want: adminOnly},
		"POST /api/admin/users/{id}/reset-password":      {method: "POST", path: "/api/admin/users/usr_victim/reset-password", want: adminOnly},
		"PATCH /api/admin/users/{id}":                    {method: "PATCH", path: "/api/admin/users/usr_victim", body: `{"display_name":"Victim"}`, want: adminOnly},
		"POST /api/admin/users/{id}/role":                {method: "POST", path: "/api/admin/users/usr_promotee/role", body: `{"role":"admin"}`, want: adminOnly},
		"POST /api/admin/users/{id}/disable":             {method: "POST", path: "/api/admin/users/usr_disablee/disable", want: adminOnly},
		"POST /api/admin/users/{id}/enable":              {method: "POST", path: "/api/admin/users/usr_enablee/enable", want: adminOnly},
		"GET /api/admin/audit":                           {method: "GET", path: "/api/admin/audit", want: adminOnly},
		"GET /api/calendars/{id}/grants":                 {method: "GET", path: g, want: grantManager},
		"PUT /api/calendars/{id}/grants/{group}":         {method: "PUT", path: g + "/grp_extra", body: `{"role":"reader"}`, want: grantManager},
		"DELETE /api/calendars/{id}/grants/{group}":      {method: "DELETE", path: g + "/grp_extra", want: grantManager},
		"GET /api/calendars":                             {method: "GET", path: "/api/calendars", want: everyday},
		"POST /api/calendars":                            {method: "POST", path: "/api/calendars", body: `{"name":"Matrix"}`, want: everyday},
		"PATCH /api/calendars/{id}":                      {method: "PATCH", path: "/api/calendars/" + w.group.ID, body: `{"name":"Team"}`, want: groupManage},
		"DELETE /api/calendars/{id}":                     {method: "DELETE", path: "/api/calendars/" + w.spare.ID, want: ownerOnly},
		"GET /api/events":                                {method: "GET", path: "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-31T00:00:00Z", want: everyday},
		"GET /api/admin/signin":                          {method: "GET", path: "/api/admin/signin", want: adminOnly},
		// http:// is refused before any request, so the admin cell is a 422 without the network.
		"POST /api/admin/signin/test": {method: "POST", path: "/api/admin/signin/test", body: `{"provider":"oidc","display_name":"M","issuer":"http://idp.invalid","client_id":"m"}`, want: adminOnly},
		// none touches no account and leaves sign-in closed, as every other row expects.
		"PUT /api/admin/signin": {method: "PUT", path: "/api/admin/signin", body: `{"provider":"none"}`, want: adminOnly},
		"POST /api/calendars/{id}/events": {method: "POST", path: "/api/calendars/" + w.group.ID + "/events",
			body: `{"title":"M","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, want: groupWrite},
		"PUT /api/events/{cal}/{uid}": {method: "PUT", path: "/api/events/" + w.group.ID + "/seed-uid",
			body: `{"title":"M","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, want: groupWrite},
		"DELETE /api/events/{cal}/{uid}": {method: "DELETE", path: "/api/events/" + w.group.ID + "/seed-uid?scope=all", want: groupWrite},
	}
}

func check(t *testing.T, got, want int) {
	t.Helper()
	if want == allow {
		if got == 401 || got == 403 || got == 404 || got >= 500 {
			t.Errorf("got %d, want it let through and handled", got)
		}
		return
	}
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestEveryRouteIsInTheMatrix(t *testing.T) {
	w := newWorld(t)
	rows := apiRows(w)
	routes := api.RoutesForTest(w.srv)
	for _, p := range routes {
		if _, ok := rows[p]; !ok && !davPatterns[p] {
			t.Errorf("route %q has no authorization matrix row", p)
		}
	}
	for p := range rows {
		if !slices.Contains(routes, p) {
			t.Errorf("matrix row %q matches no registered route", p)
		}
	}
	for p := range davPatterns {
		if !slices.Contains(routes, p) {
			t.Errorf("CalDAV pattern %q is not registered", p)
		}
	}
	for p, row := range rows {
		for _, a := range actors {
			if _, ok := row.want[a]; row.want != nil && !ok {
				t.Errorf("row %q has no expectation for %s", p, a)
			}
		}
	}
}

func TestAPIAuthorizationMatrix(t *testing.T) {
	w := newWorld(t)
	for pattern, row := range apiRows(w) {
		if row.want == nil {
			continue
		}
		for _, a := range actors {
			t.Run(pattern+"/"+string(a), func(t *testing.T) {
				path := row.path
				if row.pathFor != nil {
					path = row.pathFor(a)
				}
				check(t, call(t, w.srv, row.method, path, row.body, w.cookies[a]).Code, row.want[a])
			})
		}
	}
}

type davRow struct {
	name, method string
	path         func(me string) string // me is the caller's user ID
	body         string
	header       map[string]string
	want         expect
}

// davExpect fills in the callers every CalDAV row treats alike: no credentials 401, a
// deactivated user 401, an administrator 403.
func davExpect(e expect) expect {
	e[anon], e[deactivated], e[admin] = 401, 401, 403
	return e
}

func TestCalDAVAuthorizationMatrix(t *testing.T) {
	// Each row gets its own world: DAV counts failed logins per IP (10 per 15 minutes), and the
	// anonymous and deactivated callers fail on every row, so a shared server would answer 429.
	// It also keeps one row's writes from leaking into the next.
	var w *world // the current row's world; path funcs read it when called
	name := func(me string) string { return strings.TrimPrefix(me, "usr_") }
	xmlCT := map[string]string{"Content-Type": "application/xml"}
	depth0 := map[string]string{"Content-Type": "application/xml", "Depth": "0"}
	depth1 := map[string]string{"Content-Type": "application/xml", "Depth": "1"}
	newICS := map[string]string{"Content-Type": "text/calendar", "If-None-Match": "*"}
	propfind := `<d:propfind xmlns:d="DAV:"><d:prop><d:displayname/></d:prop></d:propfind>`
	patch := `<d:propertyupdate xmlns:d="DAV:"><d:set><d:prop><d:displayname>Renamed</d:displayname></d:prop></d:set></d:propertyupdate>`
	sync := `<d:sync-collection xmlns:d="DAV:"><d:sync-token></d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	mk := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:displayname>x</d:displayname></d:prop></d:set></c:mkcalendar>`
	group := func(me string) string { return "/dav/" + me + "/calendars/_" + w.group.ID + "/" }

	rows := []davRow{
		{"discover", "PROPFIND", func(string) string { return "/.well-known/caldav" }, propfind, depth0,
			davExpect(expect{owner: allow, reader: allow, editor: allow, manager: allow, nonmember: allow})},
		{"home", "PROPFIND", func(me string) string { return "/dav/" + me + "/calendars/" }, propfind, depth1,
			davExpect(expect{owner: 207, reader: 207, editor: 207, manager: 207, nonmember: 207})},
		{"group calendar", "PROPFIND", group, propfind, depth0,
			davExpect(expect{reader: 207, editor: 207, manager: 207, owner: 404, nonmember: 404})},
		{"group sync", "REPORT", group, sync, depth1,
			davExpect(expect{reader: 207, editor: 207, manager: 207, owner: 404, nonmember: 404})},
		{"group get", "GET", func(me string) string { return group(me) + "seed.ics" }, "", nil,
			davExpect(expect{reader: 200, editor: 200, manager: 200, owner: 404, nonmember: 404})},
		{"group put", "PUT", func(me string) string { return group(me) + name(me) + "-new.ics" }, "", newICS,
			davExpect(expect{editor: 201, manager: 201, reader: 403, owner: 404, nonmember: 404})},
		{"group delete", "DELETE", func(me string) string { return group(me) + name(me) + "-del.ics" }, "", nil,
			davExpect(expect{editor: 204, manager: 204, reader: 403, owner: 404, nonmember: 404})},
		{"group proppatch", "PROPPATCH", group, patch, xmlCT,
			davExpect(expect{manager: 207, reader: 403, editor: 403, owner: 404, nonmember: 404})},
		{"group collection delete", "DELETE", group, "", nil,
			davExpect(expect{reader: 403, editor: 403, manager: 403, owner: 404, nonmember: 404})},
		{"mkcalendar at a group segment", "MKCALENDAR", func(me string) string { return "/dav/" + me + "/calendars/_cal_" + uuid.NewString() + "/" }, mk, xmlCT,
			davExpect(expect{owner: 403, reader: 403, editor: 403, manager: 403, nonmember: 403})},
		{"owner's calendar", "PROPFIND", func(string) string { return "/dav/usr_owner/calendars/default/" }, propfind, depth0,
			davExpect(expect{owner: 207, reader: 403, editor: 403, manager: 403, nonmember: 403})},
		{"owner's calendar put", "PUT", func(me string) string { return "/dav/usr_owner/calendars/default/" + name(me) + "-p.ics" }, "", newICS,
			davExpect(expect{owner: 201, reader: 403, editor: 403, manager: 403, nonmember: 403})},
	}
	for _, row := range rows {
		w = newWorld(t)
		ts := httptest.NewServer(w.srv)
		t.Cleanup(ts.Close)
		for _, a := range actors {
			t.Run(row.name+"/"+string(a), func(t *testing.T) {
				me := "usr_" + string(a)
				body := row.body
				if row.method == "PUT" {
					body = eventICS(row.name + "-" + string(a))
				}
				user, pass := "", ""
				if a != anon {
					user, pass = string(a), w.tokens[a]
				}
				r := rawDAV(t, ts, row.method, row.path(me), user, pass, body, row.header)
				check(t, r.StatusCode, row.want[a])
			})
		}
	}
}

func TestSessionTieredPublicRoutes(t *testing.T) {
	w := newWorld(t)
	for _, a := range actors {
		t.Run("me/"+string(a), func(t *testing.T) {
			rec := call(t, w.srv, "GET", "/api/auth/me", "", w.cookies[a])
			var got struct {
				Authenticated bool `json:"authenticated"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if want := a != anon && a != deactivated; got.Authenticated != want {
				t.Errorf("authenticated = %v, want %v", got.Authenticated, want)
			}
		})
		t.Run("settings/"+string(a), func(t *testing.T) {
			rec := call(t, w.srv, "GET", "/api/settings", "", w.cookies[a])
			if rec.Code != 200 {
				t.Fatalf("got %d, want 200", rec.Code)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if _, has := got["extra_settings"]; has != (a == admin) {
				t.Errorf("extra_settings present = %v, want %v", has, a == admin)
			}
		})
	}
}

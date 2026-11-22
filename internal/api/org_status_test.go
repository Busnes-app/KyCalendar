package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// setOrgStatus sets org id's mirror status directly, as a poll would (store.ApplyOrgs never
// touches the default org, whose fixtures this test reuses).
func setOrgStatus(t *testing.T, cfg *config.Config, id store.OrgID, status string) {
	t.Helper()
	driver, q := "sqlite", `UPDATE orgs SET status = ? WHERE id = ?`
	if cfg.Database.Driver == "postgres" {
		driver, q = "pgx", `UPDATE orgs SET status = $1 WHERE id = $2`
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if res, err := db.Exec(q, status, string(id)); err != nil {
		t.Fatal(err)
	} else if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("org %s: %d rows", id, n)
	}
}

// setOrgQuotas sets org id's mirrored quotas, as a poll would for a customer org.
func setOrgQuotas(t *testing.T, cfg *config.Config, id store.OrgID, quotas string) {
	t.Helper()
	driver, q := "sqlite", `UPDATE orgs SET quotas_json = ? WHERE id = ?`
	if cfg.Database.Driver == "postgres" {
		driver, q = "pgx", `UPDATE orgs SET quotas_json = $1 WHERE id = $2`
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, quotas, string(id)); err != nil {
		t.Fatal(err)
	}
}

// An org whose booking_pages quota is false has no booking pages: its members are told why, may
// not turn a type on, and its public pages answer as for nobody. The types are kept, so turning
// the quota back on brings the same page back.
func TestAnOrgWithoutBookingPagesHasNone(t *testing.T) {
	w := newBookingWorld(t)
	page := func() int { return call(t, w.srv, "GET", "/api/book/"+w.slug, "", nil).Code }
	if page() != 200 {
		t.Fatal("control: the page does not answer")
	}
	setOrgQuotas(t, w.cfg, store.DefaultOrg, `{"booking_pages":false}`)
	if got := page(); got != 404 {
		t.Fatalf("the page of an org without booking pages: %d", got)
	}
	r := call(t, w.srv, "GET", "/api/booking-types", "", w.bob)
	if !strings.Contains(r.Body.String(), `"reason":"org_off"`) || !strings.Contains(r.Body.String(), w.typ) {
		t.Fatalf("member list: %s", r.Body.String())
	}
	if r := call(t, w.srv, "PUT", "/api/booking-types/"+w.typ, w.typeBody(true), w.bob); r.Code != 409 || codeOf(t, r.Body.Bytes()) != "org_off" {
		t.Fatalf("enabling a type: %d %s", r.Code, r.Body.String())
	}
	setOrgQuotas(t, w.cfg, store.DefaultOrg, `{"booking_pages":true}`)
	if got := page(); got != 200 {
		t.Fatalf("the quota back on: %d", got)
	}
}

// An org that is not active is refused on every credential type (tenancy spec D6, Lifecycle):
// its session ends, DAV and MCP answer org_not_active to the right token only, its booking pages
// and its KyPost messages answer as for nobody. A resume brings DAV and MCP back with the same
// app passwords; the session stays gone.
func TestASuspendedOrgIsRefusedEverywhere(t *testing.T) {
	w := newBookingWorld(t)
	ts := httptest.NewServer(w.srv)
	t.Cleanup(ts.Close)
	_, davToken := seedToken(t, w.st, "usr_bob", store.ScopeDAV)
	_, mcpToken := seedToken(t, w.st, "usr_bob", store.ScopeMCPRead)

	session := func() int { return call(t, w.srv, "GET", "/api/calendars", "", w.bob).Code }
	dav := func(pass string) string {
		r := rawDAV(t, ts, "PROPFIND", "/dav/usr_bob/", "bob", pass, `<propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`,
			map[string]string{"Depth": "0", "Content-Type": "application/xml"})
		return fmt.Sprint(r.StatusCode)
	}
	mcp := func() string {
		r := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + mcpToken})
		return fmt.Sprint(r.Code, " ", strings.Contains(r.Body.String(), "org_not_active"))
	}
	page := func(slug string) string {
		r := call(t, w.srv, "GET", "/api/book/"+slug, "", nil)
		return fmt.Sprint(r.Code, " ", r.Body.String())
	}
	n := 0
	kyPost := func(sub string) string {
		n++
		in := kypost.InboundRequest{User: kypost.User{Sub: sub, Issuer: "https://id.example"}, From: "carol@example.net", DKIMAligned: true, ICS: "x"}
		r := signedPost(t, w.srv, "/api/itip/inbound", kypost.EventInbound, fmt.Sprintf("org-status-%d", n), in)
		return fmt.Sprint(r.Code, " ", r.Body.String())
	}
	noPage, nobody := page(strings.Repeat("z", 32)), kyPost("sub-nobody")
	loginAs(t, w.srv, w.st, "carol", "user") // a password account in the same org
	login := func() int {
		r := call(t, w.srv, "POST", "/api/auth/login", `{"username":"carol","password":"SuperSecretPass123!"}`, nil)
		return r.Code
	}

	if session() != 200 || dav(davToken) != "207" || mcp() != "200 false" || page(w.slug) == noPage || kyPost("sub-bob") == nobody {
		t.Fatalf("control: session %d dav %s mcp %s page %.3s kypost %q", session(), dav(davToken), mcp(), page(w.slug), kyPost("sub-bob"))
	}

	setOrgStatus(t, w.cfg, store.DefaultOrg, "suspended")
	if got := session(); got != http.StatusUnauthorized {
		t.Errorf("session: %d", got)
	}
	if got := login(); got != http.StatusUnauthorized {
		t.Errorf("password sign-in: %d", got)
	}
	if got := dav(davToken); got != "403" {
		t.Errorf("DAV with the right token: %s", got)
	}
	if got := dav("kc_wrong_secret"); got != "401" {
		t.Errorf("DAV with a wrong token: %s, want the usual challenge", got)
	}
	if got := mcp(); got != "403 true" {
		t.Errorf("MCP: %s", got)
	}
	if got := page(w.slug); got != noPage {
		t.Errorf("booking page %.40q, want %q", got, noPage)
	}
	if got := kyPost("sub-bob"); got != nobody {
		t.Errorf("KyPost for bob %q, want %q", got, nobody)
	}

	setOrgStatus(t, w.cfg, store.DefaultOrg, "active")
	if got := dav(davToken); got != "207" {
		t.Errorf("DAV after the resume: %s", got)
	}
	if got := mcp(); got != "200 false" {
		t.Errorf("MCP after the resume: %s", got)
	}
	if got := session(); got != http.StatusUnauthorized {
		t.Errorf("the session came back with the resume: %d", got)
	}
}

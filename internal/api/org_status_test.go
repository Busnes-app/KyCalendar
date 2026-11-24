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

// orgProbes reaches w as each credential type does (session, password sign-in, DAV with the
// right and a wrong token, MCP, booking page, KyPost inbound) and records what each answers
// when the org serves, for refused to compare against what nobody gets.
type orgProbes struct {
	session, login func() int
	dav            func(pass string) string
	mcp            func() string
	page           func(slug string) string
	kyPost         func(sub string) string
	davToken       string
	noPage, nobody string
}

func newOrgProbes(t *testing.T, w *bookingWorld) *orgProbes {
	t.Helper()
	ts := httptest.NewServer(w.srv)
	t.Cleanup(ts.Close)
	_, davToken := seedToken(t, w.st, "usr_bob", store.ScopeDAV)
	_, mcpToken := seedToken(t, w.st, "usr_bob", store.ScopeMCPRead)
	p := &orgProbes{davToken: davToken}
	p.session = func() int { return call(t, w.srv, "GET", "/api/calendars", "", w.bob).Code }
	p.dav = func(pass string) string {
		r := rawDAV(t, ts, "PROPFIND", "/dav/usr_bob/", "bob", pass, `<propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`,
			map[string]string{"Depth": "0", "Content-Type": "application/xml"})
		return fmt.Sprint(r.StatusCode)
	}
	p.mcp = func() string {
		r := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + mcpToken})
		return fmt.Sprint(r.Code, " ", strings.Contains(r.Body.String(), "org_not_active"))
	}
	p.page = func(slug string) string {
		r := call(t, w.srv, "GET", "/api/book/"+slug, "", nil)
		return fmt.Sprint(r.Code, " ", r.Body.String())
	}
	n := 0
	p.kyPost = func(sub string) string {
		n++
		in := kypost.InboundRequest{User: kypost.User{Sub: sub, Issuer: "https://id.example"}, From: "carol@example.net", DKIMAligned: true, ICS: "x"}
		r := signedPost(t, w.srv, "/api/itip/inbound", kypost.EventInbound, fmt.Sprintf("org-status-%s-%d", t.Name(), n), in)
		return fmt.Sprint(r.Code, " ", r.Body.String())
	}
	p.noPage, p.nobody = p.page(strings.Repeat("z", 32)), p.kyPost("sub-nobody")
	loginAs(t, w.srv, w.st, "carol", "user") // a password account in the same org
	p.login = func() int {
		r := call(t, w.srv, "POST", "/api/auth/login", `{"username":"carol","password":"SuperSecretPass123!"}`, nil)
		return r.Code
	}
	return p
}

// served fails t unless every credential type reaches w.
func (p *orgProbes) served(t *testing.T, w *bookingWorld, when string) {
	t.Helper()
	if got := p.dav(p.davToken); got != "207" {
		t.Errorf("%s: DAV %s", when, got)
	}
	if got := p.mcp(); got != "200 false" {
		t.Errorf("%s: MCP %s", when, got)
	}
	if got := p.page(w.slug); got == p.noPage {
		t.Errorf("%s: the booking page answers as for nobody", when)
	}
	if got := p.kyPost("sub-bob"); got == p.nobody {
		t.Errorf("%s: KyPost for bob answers as for nobody", when)
	}
}

// refused fails t unless every credential type is refused as for an org that does not serve.
func (p *orgProbes) refused(t *testing.T, w *bookingWorld, when string) {
	t.Helper()
	if got := p.session(); got != http.StatusUnauthorized {
		t.Errorf("%s: session %d", when, got)
	}
	if got := p.login(); got != http.StatusUnauthorized {
		t.Errorf("%s: password sign-in %d", when, got)
	}
	if got := p.dav(p.davToken); got != "403" {
		t.Errorf("%s: DAV with the right token %s", when, got)
	}
	if got := p.dav("kc_wrong_secret"); got != "401" {
		t.Errorf("%s: DAV with a wrong token %s, want the usual challenge", when, got)
	}
	if got := p.mcp(); got != "403 true" {
		t.Errorf("%s: MCP %s", when, got)
	}
	if got := p.page(w.slug); got != p.noPage {
		t.Errorf("%s: booking page %.40q, want %q", when, got, p.noPage)
	}
	if got := p.kyPost("sub-bob"); got != p.nobody {
		t.Errorf("%s: KyPost for bob %q, want %q", when, got, p.nobody)
	}
}

// An org that is not active is refused on every credential type (tenancy spec D6, Lifecycle):
// its session ends, DAV and MCP answer org_not_active to the right token only, its booking pages
// and its KyPost messages answer as for nobody. A resume brings DAV and MCP back with the same
// app passwords; the session stays gone.
func TestASuspendedOrgIsRefusedEverywhere(t *testing.T) {
	w := newBookingWorld(t)
	p := newOrgProbes(t, w)
	if p.session() != 200 {
		t.Fatal("control: no session")
	}
	p.served(t, w, "control")
	setOrgStatus(t, w.cfg, store.DefaultOrg, "suspended")
	p.refused(t, w, "suspended")
	setOrgStatus(t, w.cfg, store.DefaultOrg, "active")
	if got := p.dav(p.davToken); got != "207" {
		t.Errorf("DAV after the resume: %s", got)
	}
	if got := p.mcp(); got != "200 false" {
		t.Errorf("MCP after the resume: %s", got)
	}
	if got := p.session(); got != http.StatusUnauthorized {
		t.Errorf("the session came back with the resume: %d", got)
	}
}

// setOrgRegion sets org id's mirrored home_region, as a poll would.
func setOrgRegion(t *testing.T, cfg *config.Config, id store.OrgID, region string) {
	t.Helper()
	driver, q := "sqlite", `UPDATE orgs SET home_region = ? WHERE id = ?`
	if cfg.Database.Driver == "postgres" {
		driver, q = "pgx", `UPDATE orgs SET home_region = $1 WHERE id = $2`
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, region, string(id)); err != nil {
		t.Fatal(err)
	}
}

// Cells (tenancy spec, C5): with KY_CELLS set, an active org whose home_region is not one of
// them is refused on every credential type, exactly as a suspended one; in a served cell it
// serves; a move to another cell refuses it again. The org here is the fixtures' default org,
// which in a deployment with cells is checked like any customer org.
func TestAnOrgOfAnotherCellIsRefusedEverywhere(t *testing.T) {
	w := newBookingWorld(t)
	p := newOrgProbes(t, w)
	if p.session() != 200 {
		t.Fatal("control: no session")
	}
	p.served(t, w, "no cells")
	if err := w.st.ServeCells([]string{"us-east-2", "us-east-3"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.st.ServeCells(nil) })
	p.refused(t, w, "no home region")
	setOrgRegion(t, w.cfg, store.DefaultOrg, "us-east-3")
	p.served(t, w, "a served cell")
	setOrgRegion(t, w.cfg, store.DefaultOrg, "us-east-1")
	p.refused(t, w, "another cell")
}
